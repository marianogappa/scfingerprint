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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marianogappa/scfingerprint/dataset"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/hygiene"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV (required)")
	prosPath := flag.String("pros", "corpus/pros_merged.json", "pro name → aurora ID mapping")
	outDir := flag.String("out", "dataset/players", "output directory for identity JSON files")
	minGames := flag.Int("min-games", 20, "minimum games per identity")
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

	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		log.Fatal(err)
	}

	co := hygiene.BuildCoOccurrence(hygiene.ManifestFromSamples(samples))
	th := hygiene.DefaultThresholds()

	type enrollment struct {
		id         string
		fp         *fingerprint.Fingerprint
		confidence string
		aliases    []dataset.Alias
		manifest   []string
		notes      string
		games      int
		selfCon    float64
	}

	var enrollments []enrollment
	var skippedSelfCon int

	for proName, auroraIDs := range proMap {
		var groups [][]training.Sample
		var groupIDs []string
		for _, aid := range auroraIDs {
			if s, ok := byPlayer[aid]; ok && len(s) >= 5 {
				groups = append(groups, s)
				groupIDs = append(groupIDs, aid)
			}
		}
		if len(groups) == 0 {
			continue
		}

		var fp *fingerprint.Fingerprint
		var manifest []string
		var aliasNames []string

		if len(groups) == 1 {
			fp, manifest = enrollGroup(groups[0], proName)
			aliasNames = collectToons(groups[0])
		} else {
			fps := make([]*fingerprint.Fingerprint, len(groups))
			for i, g := range groups {
				fps[i], _ = enrollGroup(g, proName+"_"+groupIDs[i])
			}

			merged := fps[0]
			mergedManifest := manifestFromGroup(groups[0])
			allToons := collectToons(groups[0])
			for i := 1; i < len(fps); i++ {
				v, mergeErr := hygiene.ValidateMerge(merged, fps[i], scorer, co, th)
				if mergeErr != nil {
					log.Printf("WARN: %s merge validation error (group %s): %v", proName, groupIDs[i], mergeErr)
					continue
				}
				if !v.OK {
					log.Printf("SKIP merge %s group %s: %s", proName, groupIDs[i], v.Reason)
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
			}
			fp = merged
			manifest = mergedManifest
			aliasNames = allToons
		}

		if fp.N() < *minGames {
			continue
		}

		selfCon, scErr := hygiene.SelfConsistencyGate(fp, scorer, th)
		if scErr != nil {
			log.Printf("SKIP %s: %v (selfCon=%.3f)", proName, scErr, selfCon)
			skippedSelfCon++
			continue
		}

		conf := dataset.ConfidenceHigh
		if fp.N() >= 40 && selfCon >= 0.90 {
			conf = dataset.ConfidenceConfirmed
		}

		aliases := buildAliases(proName, aliasNames)
		enrollments = append(enrollments, enrollment{
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
		fmt.Fprintf(os.Stderr, "  %s: %d games, selfCon=%.3f, %s\n", e.id, e.games, e.selfCon, e.confidence)
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
