package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/marianogappa/scfingerprint"
	"github.com/marianogappa/scfingerprint/internal/dataset"
	"github.com/marianogappa/scfingerprint/internal/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/hygiene"
)

// catalogEntry is the machine shape of one built-in catalog player: identity
// and provenance, plus a summary of the training data behind the fingerprint.
type catalogEntry struct {
	ID             string          `json:"id"`
	Label          string          `json:"label"`
	Confidence     string          `json:"confidence"`
	Races          map[string]int  `json:"races"` // games per race code (z/t/p/r)
	Games          int             `json:"games"`
	Replays        int             `json:"replays"`
	FeatureVersion int             `json:"feature_version"`
	Source         string          `json:"source,omitempty"`
	DateFrom       string          `json:"date_from,omitempty"`
	DateTo         string          `json:"date_to,omitempty"`
	Aliases        []dataset.Alias `json:"aliases"`
	Liquipedia     string          `json:"liquipedia,omitempty"`
	Notes          string          `json:"notes,omitempty"`
}

func newCatalogEntry(id dataset.Identity, fp *fingerprint.Fingerprint) catalogEntry {
	return catalogEntry{
		ID:             id.ID,
		Label:          catalogLabel(id, fp),
		Confidence:     id.Confidence,
		Races:          fp.RaceCounts(),
		Games:          fp.N(),
		Replays:        len(id.ReplayManifest),
		FeatureVersion: fp.Version(),
		Source:         fp.Meta.Source,
		DateFrom:       fp.Meta.DateFrom,
		DateTo:         fp.Meta.DateTo,
		Aliases:        id.Aliases,
		Liquipedia:     id.Liquipedia,
		Notes:          id.Notes,
	}
}

// catalogLabel prefers the fingerprint's display label, falling back to the
// primary alias and then the ID.
func catalogLabel(id dataset.Identity, fp *fingerprint.Fingerprint) string {
	if fp.Meta.Label != "" {
		return fp.Meta.Label
	}
	for _, a := range id.Aliases {
		if a.Primary {
			return a.Name
		}
	}
	return id.ID
}

var raceNames = map[string]string{"z": "Zerg", "t": "Terran", "p": "Protoss", "r": "Random"}

// racesPhrase renders race counts as "Zerg (44), Terran (2)", biggest first.
func racesPhrase(counts map[string]int) string {
	type rc struct {
		name string
		n    int
	}
	var rcs []rc
	for code, n := range counts {
		name := raceNames[code]
		if name == "" {
			name = code
		}
		rcs = append(rcs, rc{name, n})
	}
	sort.Slice(rcs, func(i, j int) bool {
		if rcs[i].n != rcs[j].n {
			return rcs[i].n > rcs[j].n
		}
		return rcs[i].name < rcs[j].name
	})
	parts := make([]string, len(rcs))
	for i, r := range rcs {
		parts[i] = fmt.Sprintf("%s (%d)", r.name, r.n)
	}
	return strings.Join(parts, ", ")
}

// loadCatalog loads every embedded identity with its parsed fingerprint,
// already sorted by ID.
func loadCatalog() ([]dataset.Identity, []*fingerprint.Fingerprint, error) {
	return dataset.LoadEmbedded()
}

// findCatalogPlayer resolves a player by ID, label, or any alias name,
// case-insensitively.
func findCatalogPlayer(ids []dataset.Identity, fps []*fingerprint.Fingerprint, key string) (dataset.Identity, *fingerprint.Fingerprint, error) {
	for i, id := range ids {
		if strings.EqualFold(id.ID, key) || strings.EqualFold(fps[i].Meta.Label, key) {
			return id, fps[i], nil
		}
		for _, a := range id.Aliases {
			if strings.EqualFold(a.Name, key) {
				return id, fps[i], nil
			}
		}
	}
	return dataset.Identity{}, nil, fmt.Errorf("player %q is not in the catalog (try: scfingerprint dataset list)", key)
}

func cmdDatasetList(args []string) int {
	fs := flag.NewFlagSet("dataset list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON on stdout even when it is a terminal")
	noColor := fs.Bool("no-color", false, "disable colour on stderr")
	minConfidence := fs.String("min-confidence", dataset.ConfidenceCandidate, "minimum confidence tier to list (confirmed/high/candidate)")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	pal := newPalette(colorEnabled(*noColor))

	ids, fps, err := loadCatalog()
	if err != nil {
		return fail(err)
	}
	rank := map[string]int{dataset.ConfidenceConfirmed: 3, dataset.ConfidenceHigh: 2, dataset.ConfidenceCandidate: 1}
	minRank, ok := rank[*minConfidence]
	if !ok {
		return fail(fmt.Errorf("invalid --min-confidence %q (want: confirmed, high or candidate)", *minConfidence))
	}

	var entries []catalogEntry
	tally := map[string]int{}
	for i, id := range ids {
		if rank[id.Confidence] < minRank {
			continue
		}
		entries = append(entries, newCatalogEntry(id, fps[i]))
		tally[id.Confidence]++
	}

	w := os.Stderr
	_, _ = fmt.Fprintf(w, "%s%d players%s in the built-in catalog: %d confirmed, %d high, %d candidate\n\n",
		pal.bold, len(entries), pal.reset,
		tally[dataset.ConfidenceConfirmed], tally[dataset.ConfidenceHigh], tally[dataset.ConfidenceCandidate])
	tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  PLAYER\tRACES\tCONFIDENCE\tGAMES\tREPLAYS\tLIQUIPEDIA")
	for _, e := range entries {
		_, _ = fmt.Fprintf(tw, "  %s%s%s\t%s\t%s\t%d\t%d\t%s\n",
			pal.cyan, e.Label, pal.reset, racesPhrase(e.Races), e.Confidence, e.Games, e.Replays, e.Liquipedia)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "%s  Details for one player: scfingerprint dataset show <player>.%s\n", pal.dim, pal.reset)
	_, _ = fmt.Fprintf(w, "%s  Confidence is the curation tier of the entry, not a match verdict.%s\n", pal.dim, pal.reset)

	if stdoutWantsJSON(*asJSON) {
		printJSON(entries)
	}
	return exitOK
}

func cmdDatasetShow(args []string) int {
	fs := flag.NewFlagSet("dataset show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON on stdout even when it is a terminal")
	noColor := fs.Bool("no-color", false, "disable colour on stderr")
	positional, err := parseAll(fs, args)
	if err != nil {
		return exitError
	}
	if len(positional) != 1 {
		return fail(fmt.Errorf("dataset show takes exactly one player (ID, label or alias)"))
	}
	pal := newPalette(colorEnabled(*noColor))

	ids, fps, err := loadCatalog()
	if err != nil {
		return fail(err)
	}
	id, fp, err := findCatalogPlayer(ids, fps, positional[0])
	if err != nil {
		return fail(err)
	}
	e := newCatalogEntry(id, fp)

	w := os.Stderr
	_, _ = fmt.Fprintf(w, "%s%s%s%s (id: %s)\n\n", pal.bold, pal.cyan, e.Label, pal.reset, e.ID)
	field := func(name, value string) {
		if value != "" {
			_, _ = fmt.Fprintf(w, "  %s%-12s%s %s\n", pal.bold, name+":", pal.reset, value)
		}
	}
	field("Confidence", e.Confidence)
	field("Races", racesPhrase(e.Races))
	field("Training", fmt.Sprintf("%d games from %d catalogued replays, feature version %d", e.Games, e.Replays, e.FeatureVersion))
	field("Source", e.Source)
	if e.DateFrom != "" || e.DateTo != "" {
		field("Dates", strings.TrimSpace(e.DateFrom+" to "+e.DateTo))
	}
	var aliases []string
	for _, a := range e.Aliases {
		s := a.Name
		var tags []string
		if a.Primary {
			tags = append(tags, "primary")
		}
		if a.ZScore != 0 {
			tags = append(tags, fmt.Sprintf("z=%.1f", a.ZScore))
		}
		if a.CoOccurred {
			tags = append(tags, "co-occurred")
		}
		if len(tags) > 0 {
			s += " (" + strings.Join(tags, ", ") + ")"
		}
		aliases = append(aliases, s)
	}
	field("Aliases", strings.Join(aliases, ", "))
	field("Liquipedia", pal.cyan+e.Liquipedia+pal.reset)
	field("Notes", e.Notes)
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "%s  Export the fingerprint: scfingerprint dataset fingerprint %s.%s\n", pal.dim, e.ID, pal.reset)

	if stdoutWantsJSON(*asJSON) {
		printJSON(e)
	}
	return exitOK
}

func cmdDatasetFingerprint(args []string) int {
	fs := flag.NewFlagSet("dataset fingerprint", flag.ContinueOnError)
	positional, err := parseAll(fs, args)
	if err != nil {
		return exitError
	}
	if len(positional) != 1 {
		return fail(fmt.Errorf("dataset fingerprint takes exactly one player (ID, label or alias)"))
	}

	ids, fps, err := loadCatalog()
	if err != nil {
		return fail(err)
	}
	id, fp, err := findCatalogPlayer(ids, fps, positional[0])
	if err != nil {
		return fail(err)
	}

	// The blob is the structured output, so it goes to stdout even on a
	// terminal; the context line goes to stderr like all human output.
	fmt.Fprintf(os.Stderr, "fingerprint for %s: %d games, feature version %d\n", id.ID, fp.N(), fp.Version())
	printBlob(os.Stdout, id.Fingerprint)
	return exitOK
}

func printBlob(w io.Writer, blob string) {
	_, _ = fmt.Fprintln(w, blob)
}

// replayAurora is the part of one corpus/replays.jsonl line that says which
// Battle.net account a replay belonged to.
type replayAurora struct {
	MatchID     string `json:"matchId"`
	File        string `json:"file"`
	AuroraID    int64  `json:"auroraId"`
	OppAuroraID int64  `json:"oppAuroraId"`
}

// accounts returns both players' accounts. The metadata does not say which
// side a given catalog entry played, so a single replay only narrows the
// entry's account to one of two — see collectAccountEvidence.
func (r replayAurora) accounts() []int64 {
	return []int64{r.AuroraID, r.OppAuroraID}
}

// collectAccountEvidence assembles the account-level view of the catalog: the
// Battle.net accounts the built-in identity map attributes to each entry's
// name, and — when replay metadata is supplied — the accounts each entry's
// enrolment replays actually came from.
//
// The map half always runs, because the map is embedded. The replay half needs
// corpus/replays.jsonl, which only a checkout has, so it is opt-in via a path
// rather than assumed: an installed binary has no corpus.
func collectAccountEvidence(ids []dataset.Identity, replayMetadataPath string) ([]hygiene.IdentityAccounts, error) {
	reg, err := scfingerprint.BuiltinRegistry()
	if err != nil {
		return nil, err
	}
	byMatchID, err := loadReplayAuroras(replayMetadataPath)
	if err != nil {
		return nil, err
	}

	out := make([]hygiene.IdentityAccounts, 0, len(ids))
	for _, id := range ids {
		e := hygiene.IdentityAccounts{Label: id.ID}
		for _, acc := range reg.LookupName(id.ID) {
			e.ByName = append(e.ByName, acc.AuroraID)
		}
		for _, a := range id.Aliases {
			for _, acc := range reg.LookupName(a.Name) {
				e.ByName = append(e.ByName, acc.AuroraID)
			}
			if acc, ok := reg.LookupToon(a.Name); ok {
				e.ByName = append(e.ByName, acc.AuroraID)
			}
		}
		// Every replay names both players and does not say which side this
		// entry was, so the account is whichever one keeps recurring while
		// the opponents change.
		perReplay := make([][]int64, 0, len(id.ReplayManifest))
		for _, rel := range id.ReplayManifest {
			if accounts, ok := lookupReplayAccounts(byMatchID, rel); ok {
				perReplay = append(perReplay, accounts)
			}
		}
		e.ByReplays, e.ReplayCount, e.ReplaysOnAccount = hygiene.DominantAccount(perReplay)
		out = append(out, e)
	}
	return out, nil
}

// loadReplayAuroras indexes replay metadata by both its file path and its
// match ID, because manifests reference replays either way and some live in
// subdirectories where the basename alone is not the match ID.
//
// An empty path means the caller has no corpus, which is not an error — the
// account-by-name gates still run.
func loadReplayAuroras(path string) (map[string][]int64, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading replay metadata: %w", err)
	}
	out := map[string][]int64{}
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r replayAurora
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("parsing %s line %d: %w", path, i+1, err)
		}
		if r.MatchID != "" {
			out[r.MatchID] = r.accounts()
		}
		if r.File != "" {
			out[r.File] = r.accounts()
		}
	}
	return out, nil
}

// lookupReplayAccounts resolves one manifest entry to both of its players'
// accounts, trying the path as written and then the bare match ID.
func lookupReplayAccounts(index map[string][]int64, manifestPath string) ([]int64, bool) {
	if accounts, ok := index[manifestPath]; ok {
		return accounts, true
	}
	accounts, ok := index[strings.TrimSuffix(path.Base(manifestPath), ".rep")]
	return accounts, ok
}
