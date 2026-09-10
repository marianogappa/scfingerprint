// Command seed-dataset reads a labeled feature CSV and the corpus pro-player
// mapping, enrolls every pro-labeled player that passes hygiene gates, and
// writes identity JSON files under dataset/players/.
//
// For pros with multiple aurora IDs in the corpus, a merge is attempted with
// hygiene validation. Players that fail self-consistency are skipped. A full
// duplicate scan runs at the end.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/marianogappa/scfingerprint/internal/dataset"
	"github.com/marianogappa/scfingerprint/internal/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/hygiene"
	"github.com/marianogappa/scfingerprint/internal/scoring"
	"github.com/marianogappa/scfingerprint/internal/training"
)

// nullMinGames is the fewest games an account needs before its centroid is a
// meaningful impostor; nullMinAccounts is the fewest impostors needed before a
// 95th percentile is worth trusting.
const (
	nullMinGames    = 8
	nullMinAccounts = 30
)

// enrollment is one identity on its way into the dataset, carrying the
// provenance needed to measure its null before it is written out.
type enrollment struct {
	id         string
	fp         *fingerprint.Fingerprint
	confidence string
	aliases    []dataset.Alias
	manifest   []string
	notes      string
	games      int
	selfCon    float64
	auroraIDs  map[string]bool // the accounts this identity was built from
	nullP95    float64
}

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV (required)")
	prosPath := flag.String("pros", "corpus/pros_merged.json", "pro name → aurora ID mapping")
	aliasPath := flag.String("pro-aliases", "corpus/pro_aliases.json", "curated extra display names per pro (ring names that are not toons)")
	registryPath := flag.String("registry", "internal/registry/registry.json", "identity map, so a pro's own alts are not counted as impostors when measuring their null")
	exclusionsPath := flag.String("pro-exclusions", "corpus/pro_exclusions.json", "aurora IDs that must not be enrolled under a given pro name")
	outDir := flag.String("out", "dataset/players", "output directory for identity JSON files")
	minGames := flag.Int("min-games", 20, "minimum games per identity")
	maxGames := flag.Int("max-games", 0, "cap each identity at its N most recent games (0 = no cap)")
	flag.Parse()

	if *csvPath == "" {
		log.Fatal("-csv is required")
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("read %d samples", len(samples))

	byPlayer := map[string][]training.Sample{}
	for _, s := range samples {
		byPlayer[s.Player] = append(byPlayer[s.Player], s)
	}

	proMap, err := loadProMapping(*prosPath)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded %d pro names", len(proMap))

	proAliases, err := loadProAliases(*aliasPath)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("loaded curated aliases for %d pros", len(proAliases))

	proExclusions, err := loadProExclusions(*exclusionsPath)
	if err != nil {
		log.Fatal(err)
	}
	for name, ids := range proExclusions {
		kept := proMap[name][:0]
		for _, aid := range proMap[name] {
			if reason, bad := ids[aid]; bad {
				log.Printf("EXCLUDE %s aurora %s: %s", name, aid, reason)
				continue
			}
			kept = append(kept, aid)
		}
		proMap[name] = kept
	}

	registryAuroras, err := loadRegistryAuroras(*registryPath)
	if err != nil {
		log.Printf("WARN: %v — nulls will treat a pro's unlisted alts as impostors", err)
	}
	log.Printf("registry knows accounts for %d names", len(registryAuroras))

	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		log.Fatal(err)
	}

	co := hygiene.BuildCoOccurrence(hygiene.ManifestFromSamples(samples))
	th := hygiene.DefaultThresholds()

	var enrollments []enrollment
	var skippedSelfCon int

	for proName, auroraIDs := range proMap {
		capped := capRecent(byPlayer, auroraIDs, *maxGames)
		var groups [][]training.Sample
		var groupIDs []string
		for _, aid := range auroraIDs {
			if s, ok := capped[aid]; ok && len(s) >= 5 {
				groups = append(groups, s)
				groupIDs = append(groupIDs, aid)
			}
		}
		if len(groups) == 0 {
			continue
		}
		groups, groupIDs = anchorLargestFirst(groups, groupIDs)

		var fp *fingerprint.Fingerprint
		var manifest []string
		var aliasNames []string
		var enrolled []training.Sample

		if len(groups) == 1 {
			fp, manifest = enrollGroup(groups[0], proName)
			aliasNames = collectToons(groups[0])
			enrolled = groups[0]
		} else {
			fps := make([]*fingerprint.Fingerprint, len(groups))
			for i, g := range groups {
				fps[i], _ = enrollGroup(g, proName+"_"+groupIDs[i])
			}

			merged := fps[0]
			mergedManifest := manifestFromGroup(groups[0])
			allToons := collectToons(groups[0])
			mergedSamples := append([]training.Sample{}, groups[0]...)
			for i := 1; i < len(fps); i++ {
				v, mergeErr := hygiene.ValidateMerge(merged, fps[i], scorer, co, th)
				if mergeErr != nil {
					log.Printf("WARN: %s merge validation error (group %s): %v", proName, groupIDs[i], mergeErr)
					continue
				}
				if !v.OK {
					log.Printf("SKIP merge %s group %s (%d games discarded): %s", proName, groupIDs[i], len(groups[i]), v.Reason)
					continue
				}
				log.Printf("MERGE %s group %s: cross=%.3f selfCon=%.3f", proName, groupIDs[i], v.CrossSimilarity, v.MergedSelfConsistency)
				m, mergeErr := fingerprint.Merge(merged, fps[i], fingerprint.Meta{
					Label:  proName,
					Source: "cwal-harvest",
				})
				if mergeErr != nil {
					log.Printf("WARN: %s merge failed: %v", proName, mergeErr)
					continue
				}
				merged = m
				mergedManifest = append(mergedManifest, manifestFromGroup(groups[i])...)
				allToons = append(allToons, collectToons(groups[i])...)
				mergedSamples = append(mergedSamples, groups[i]...)
			}
			// A per-group label ("Sai_18665802") is scaffolding for merge
			// validation. It leaks to the user as the match label whenever
			// every merge is rejected and one group becomes the fingerprint.
			merged.Meta.Label = proName
			fp = merged
			manifest = mergedManifest
			aliasNames = allToons
			enrolled = mergedSamples
		}

		if fp.N() < *minGames {
			continue
		}

		sort.SliceStable(enrolled, func(i, j int) bool {
			return enrolled[i].StartTime.Before(enrolled[j].StartTime)
		})
		audit, scErr := hygiene.SelfConsistencyGateRaceAware(enrolled, scorer, th)
		if scErr != nil {
			log.Printf("SKIP %s: %v (mixed=%.3f strata=%v)", proName, scErr, audit.Mixed, roundStrata(audit.Strata))
			skippedSelfCon++
			continue
		}
		selfCon := audit.GatedSelfConsistency()

		conf := dataset.ConfidenceHigh
		if fp.N() >= 40 && selfCon >= 0.90 {
			conf = dataset.ConfidenceConfirmed
		}

		aliases := buildAliases(proName, append(append([]string{}, proAliases[proName]...), aliasNames...))
		owned := map[string]bool{}
		for _, aid := range proMap[proName] {
			owned[aid] = true
		}
		enrollments = append(enrollments, enrollment{
			auroraIDs:  owned,
			id:         strings.ToLower(proName),
			fp:         fp,
			confidence: conf,
			aliases:    aliases,
			manifest:   manifest,
			notes:      "enrolled from cwal-harvest corpus",
			games:      fp.N(),
			selfCon:    selfCon,
		})
	}

	sort.Slice(enrollments, func(i, j int) bool {
		return enrollments[i].id < enrollments[j].id
	})

	log.Printf("enrolled %d identities (%d skipped self-consistency)", len(enrollments), skippedSelfCon)

	// Measure each identity's null: how it scores against accounts that are
	// not this player. A crowded identity scores respectably against
	// strangers, and its claim bar has to rise to match — otherwise it
	// collects everyone who merely plays like it.
	// A pro's own alts must not count as impostors: scoring one against the
	// pro inflates their null and can push the bar above a genuine match.
	// pros_merged is not enough — the registry routinely knows accounts it
	// does not (that is how Soo's own alt was inflating Soo's bar).
	for i := range enrollments {
		for _, aid := range registryAuroras[strings.ToLower(enrollments[i].id)] {
			enrollments[i].auroraIDs[aid] = true
		}
	}
	measureNulls(enrollments, byPlayer, scorer)

	fps := make([]*fingerprint.Fingerprint, len(enrollments))
	for i, e := range enrollments {
		fps[i] = e.fp
	}
	dups, err := hygiene.ScanDuplicates(fps, scorer, th)
	if err != nil {
		log.Fatalf("duplicate scan: %v", err)
	}
	if len(dups) > 0 {
		log.Printf("WARNING: %d duplicate pairs found — review before shipping:", len(dups))
		for _, d := range dups {
			log.Printf("  %s ↔ %s  sim=%.3f", d.LabelA, d.LabelB, d.Similarity)
		}
		log.Fatal("resolve duplicates before writing dataset (merge them or exclude one)")
	}
	log.Println("duplicate scan: clean")

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, e := range enrollments {
		blob, err := e.fp.MarshalString()
		if err != nil {
			log.Fatalf("MarshalString for %s: %v", e.id, err)
		}
		id := dataset.Identity{
			ID:             e.id,
			Fingerprint:    blob,
			Confidence:     e.confidence,
			Aliases:        e.aliases,
			ReplayManifest: e.manifest,
			NullP95:        math.Round(e.nullP95*1000) / 1000,
			Notes:          e.notes,
		}
		data, err := json.MarshalIndent(id, "", " ")
		if err != nil {
			log.Fatal(err)
		}
		path := filepath.Join(*outDir, e.id+".json")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "  %s: %d games, selfCon=%.3f, nullP95=%.2f (bar %.2f), %s\n",
			e.id, e.games, e.selfCon, e.nullP95, id.IdentityBar(), e.confidence)
	}
	log.Printf("wrote %d identity files to %s", len(enrollments), *outDir)
}

func loadProMapping(path string) (map[string][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string][]int
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for name, ids := range raw {
		strs := make([]string, len(ids))
		for i, id := range ids {
			strs[i] = fmt.Sprintf("%d", id)
		}
		out[name] = strs
	}
	return out, nil
}

// loadProAliases reads the curated alias map. A pro can be known by a ring
// name that never appears as a toon — Organ is PianO on Liquipedia — and a
// catalog lookup should resolve either spelling. A missing file is not an
// error; the map is optional curation.
// capRecent keeps only the max most recent games across all of a pro's aurora
// IDs, then regroups them by ID so the merge hygiene checks still run per
// account. Past ~60 games a fingerprint stops moving, so the extra replays buy
// no accuracy and cost Git LFS budget the thin players need. max <= 0 disables
// the cap.
func capRecent(byPlayer map[string][]training.Sample, auroraIDs []string, max int) map[string][]training.Sample {
	out := map[string][]training.Sample{}
	total := 0
	for _, aid := range auroraIDs {
		if s, ok := byPlayer[aid]; ok {
			out[aid] = s
			total += len(s)
		}
	}
	if max <= 0 || total <= max {
		return out
	}

	type owned struct {
		aid string
		s   training.Sample
	}
	all := make([]owned, 0, total)
	for aid, ss := range out {
		for _, s := range ss {
			all = append(all, owned{aid, s})
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].s.StartTime.After(all[j].s.StartTime)
	})

	kept := map[string][]training.Sample{}
	for _, o := range all[:max] {
		kept[o.aid] = append(kept[o.aid], o.s)
	}
	for aid := range kept {
		ss := kept[aid]
		sort.SliceStable(ss, func(i, j int) bool { return ss[i].StartTime.Before(ss[j].StartTime) })
		kept[aid] = ss
	}
	return kept
}

// anchorLargestFirst puts the account with the most games first, keeping the
// rest in mapping order. A rejected merge leaves the anchor group as the whole
// fingerprint, so the anchor has to be the best-evidenced account rather than
// whichever aurora ID the mapping happens to list first. Sai's two accounts
// fingerprint as different people (cross-similarity 0.00), and list order alone
// enrolled the 20-game one over the 58-game one.
func anchorLargestFirst(groups [][]training.Sample, groupIDs []string) ([][]training.Sample, []string) {
	order := make([]int, len(groups))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return len(groups[order[a]]) > len(groups[order[b]]) })

	outGroups := make([][]training.Sample, len(groups))
	outIDs := make([]string, len(groupIDs))
	for i, idx := range order {
		outGroups[i], outIDs[i] = groups[idx], groupIDs[idx]
	}
	return outGroups, outIDs
}

// loadProExclusions reads the curated {pro: {auroraID: reason}} map of
// accounts CWAL attributes to a pro that must not be enrolled under that
// name — an account that fingerprints as somebody else poisons the catalog
// with a duplicate. Keys starting with "_" are comments. A missing file is
// not an error.
func roundStrata(m map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(m))
	for k, v := range m {
		out[k] = math.Round(v*1000) / 1000
	}
	return out
}

func loadProExclusions(path string) (map[string]map[string]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	out := map[string]map[string]string{}
	for name, blob := range raw {
		if strings.HasPrefix(name, "_") {
			continue
		}
		var ids map[string]string
		if err := json.Unmarshal(blob, &ids); err != nil {
			return nil, fmt.Errorf("parsing %s entry %q: %w", path, name, err)
		}
		out[name] = ids
	}
	return out, nil
}

func loadProAliases(path string) (map[string][]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string][]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out map[string][]string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return out, nil
}

func enrollGroup(samples []training.Sample, label string) (*fingerprint.Fingerprint, []string) {
	fp := fingerprint.New(fingerprint.Meta{
		Label:  label,
		Source: "cwal-harvest",
	})
	var manifest []string
	for _, s := range samples {
		race := strings.ToLower(s.Race)
		_ = fp.Add(s.Vector, race)
		manifest = append(manifest, s.File)
	}
	return fp, manifest
}

func manifestFromGroup(samples []training.Sample) []string {
	out := make([]string, len(samples))
	for i, s := range samples {
		out[i] = s.File
	}
	return out
}

func collectToons(samples []training.Sample) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range samples {
		if s.File != "" && !seen[s.File] {
			seen[s.File] = true
		}
	}
	// Player names aren't in the CSV player column (that's aurora ID);
	// we derive toons from the identity mapping. Just return empty.
	_ = out
	return nil
}

func buildAliases(proName string, toons []string) []dataset.Alias {
	aliases := []dataset.Alias{{Name: proName, Primary: true}}
	seen := map[string]bool{proName: true}
	for _, t := range toons {
		if t != "" && !seen[t] {
			seen[t] = true
			aliases = append(aliases, dataset.Alias{Name: t})
		}
	}
	return aliases
}

// measureNulls fills each enrollment's nullP95: the 95th percentile of its
// fingerprint's score against every account that is not this player. Accounts
// belonging to the identity are excluded by aurora id, so a pro's own smurfs
// never count as impostors and never inflate the bar.
func measureNulls(enrollments []enrollment, byPlayer map[string][]training.Sample, scorer *scoring.Scorer) {
	accounts := make([]string, 0, len(byPlayer))
	for a, v := range byPlayer {
		// aurora 0 is the unidentified-opponent bucket: one label over
		// thousands of humans, so its centroid is meaningless as an impostor.
		if a != "0" && len(v) >= nullMinGames {
			accounts = append(accounts, a)
		}
	}
	sort.Strings(accounts)

	whitened := map[string][][]float64{}
	for _, a := range accounts {
		for _, s := range byPlayer[a] {
			w, err := scorer.Transform(s.Vector)
			if err == nil {
				whitened[a] = append(whitened[a], w)
			}
		}
	}

	for i := range enrollments {
		e := &enrollments[i]
		target, err := e.fp.Projected(scorer)
		if err != nil {
			continue
		}
		var zs []float64
		for _, a := range accounts {
			if e.auroraIDs[a] || len(whitened[a]) == 0 {
				continue
			}
			sc, err := scorer.Score(whitened[a], target)
			if err != nil {
				continue
			}
			zs = append(zs, sc.Z)
		}
		if len(zs) < nullMinAccounts {
			continue // too few impostors to estimate; leave 0 and use the default bar
		}
		sort.Float64s(zs)
		e.nullP95 = zs[int(float64(len(zs))*0.95)]
	}
}

// loadRegistryAuroras maps a lowercased pro name to every aurora id the
// identity map attributes to them, as decimal strings to match the CSV's
// player labels.
func loadRegistryAuroras(path string) (map[string][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading registry %s: %w", path, err)
	}
	var reg struct {
		Accounts []struct {
			Name     string `json:"name"`
			AuroraID int64  `json:"aurora_id"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		return nil, fmt.Errorf("parsing registry %s: %w", path, err)
	}
	out := map[string][]string{}
	for _, a := range reg.Accounts {
		if a.Name == "" || a.AuroraID == 0 {
			continue
		}
		k := strings.ToLower(a.Name)
		out[k] = append(out[k], strconv.FormatInt(a.AuroraID, 10))
	}
	return out, nil
}
