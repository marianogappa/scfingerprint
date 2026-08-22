package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"text/tabwriter"

	scfingerprint "github.com/marianogappa/scfingerprint"
	"github.com/marianogappa/scfingerprint/dataset"
	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/hygiene"
	"github.com/marianogappa/scfingerprint/scoring"
)

const syntheticBanner = `
╔══════════════════════════════════════════════════════════════════╗
║  WARNING: model trained on SYNTHETIC data — scores are not      ║
║  meaningful. See docs/METHODOLOGY.md for details.               ║
╚══════════════════════════════════════════════════════════════════╝
`

func warnIfSynthetic(scorer *scoring.Scorer, strict bool) int {
	if !scorer.IsSynthetic() {
		return -1
	}
	fmt.Fprint(os.Stderr, syntheticBanner)
	if strict {
		fmt.Fprintln(os.Stderr, "error: --strict is set and the model is synthetic; refusing to continue")
		return exitError
	}
	return -1
}

// parseAll parses flags interleaved with positional arguments (Go's flag
// package stops at the first positional), returning the positionals.
func parseAll(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

// interpretation returns the plain-language confidence line for a match result.
// It uses the search-level (Šidák-corrected) operating points when available,
// since Match performs a 1:N sweep and per-comparison FPRs overstate confidence.
func interpretation(evidenceN int, searchOps map[string]bool, catalogSize int) string {
	switch {
	case !searchOps["fpr_1e2"]:
		return fmt.Sprintf("weak signal: below the search-level 1-in-100 operating point (%d game(s), catalog N=%d)", evidenceN, catalogSize)
	case evidenceN >= 3 && searchOps["fpr_1e3"]:
		return fmt.Sprintf("accusation-grade: %d games of evidence at the search-level 1-in-1,000 operating point (catalog N=%d)", evidenceN, catalogSize)
	case searchOps["fpr_1e3"]:
		return fmt.Sprintf("strong lead, not confirmation: %d game(s) of evidence (catalog N=%d)", evidenceN, catalogSize)
	default:
		return fmt.Sprintf("lead: clears search-level 1-in-100 but not 1-in-1,000 (%d game(s), catalog N=%d)", evidenceN, catalogSize)
	}
}

// interpretationPairwise returns the plain-language confidence line for a
// pairwise (1:1) Same comparison where no search correction is needed.
func interpretationPairwise(evidenceN int, ops map[string]bool) string {
	switch {
	case !ops["fpr_1e2"]:
		return fmt.Sprintf("weak signal: below the 1-in-100 operating point (%d game(s) of evidence)", evidenceN)
	case evidenceN >= 3 && ops["fpr_1e3"]:
		return fmt.Sprintf("accusation-grade: %d games of evidence at the 1-in-1,000 operating point", evidenceN)
	case ops["fpr_1e3"]:
		return fmt.Sprintf("strong lead, not confirmation: %d game(s) of evidence", evidenceN)
	default:
		return fmt.Sprintf("lead: clears 1-in-100 but not 1-in-1,000 (%d game(s) of evidence)", evidenceN)
	}
}

func opsCell(ops map[string]bool, key string) string {
	if ops[key] {
		return "yes"
	}
	return "no"
}

func cmdMatch(args []string) int {
	fs := flag.NewFlagSet("match", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory of replays for multi-game evidence")
	name := fs.String("name", "", "select the player by name")
	playerID := fs.Int("player", -1, "select the player by slot ID")
	minZ := fs.Float64("min-z", 2.0, "minimum calibrated z-score to report")
	minConfidence := fs.String("min-confidence", dataset.ConfidenceHigh, "minimum dataset confidence tier (confirmed/high/candidate)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	strict := fs.Bool("strict", false, "exit with error if the model is synthetic")
	positional, err := parseAll(fs, args)
	if err != nil {
		return exitError
	}

	db, err := dataset.NewDefaultDataset(nil, *minConfidence)
	if err != nil {
		return fail(err)
	}
	if code := warnIfSynthetic(db.Scorer(), *strict); code >= 0 {
		return code
	}
	if db.Len() == 0 {
		return fail(fmt.Errorf("built-in dataset is empty at confidence tier %q", *minConfidence))
	}
	lib, err := scfingerprint.NewDataset(db.Scorer())
	if err != nil {
		return fail(err)
	}
	for _, fp := range db.Fingerprints() {
		if err := lib.Add(fp); err != nil {
			return fail(err)
		}
	}

	paths, err := collectReplays(*dir, positional)
	if err != nil {
		return fail(err)
	}
	obs, err := extractAll(paths)
	if err != nil {
		return fail(err)
	}

	type playerReport struct {
		Player  string                      `json:"player"`
		Games   int                         `json:"games"`
		Matches []scfingerprint.MatchResult `json:"matches"`
		Notes   []string                    `json:"notes,omitempty"`
	}
	var reports []playerReport

	if *dir != "" || *name != "" || *playerID >= 0 {
		// One identity across many games.
		sel, err := selectObs(obs, *name, *playerID)
		if err != nil {
			return fail(err)
		}
		games := make([]scfingerprint.PlayerGame, len(sel))
		for i, o := range sel {
			games[i] = scfingerprint.PlayerGame{Vector: o.pf.Vector, Race: o.pf.Race}
		}
		results, err := scfingerprint.MatchMany(games, lib, scfingerprint.WithMinZ(*minZ))
		if err != nil {
			return fail(err)
		}
		label := *name
		if label == "" {
			label = sel[0].pf.Name
		}
		reports = append(reports, playerReport{Player: label, Games: len(sel), Matches: results})
	} else {
		// Every player of one replay, one game each.
		for _, o := range obs {
			results, err := scfingerprint.MatchMany(
				[]scfingerprint.PlayerGame{{Vector: o.pf.Vector, Race: o.pf.Race}},
				lib, scfingerprint.WithMinZ(*minZ))
			if err != nil {
				return fail(err)
			}
			reports = append(reports, playerReport{Player: o.pf.Name, Games: 1, Matches: results})
		}
	}

	anyMatch := false
	for _, r := range reports {
		if len(r.Matches) > 0 {
			anyMatch = true
		}
	}

	if *asJSON {
		out, _ := json.MarshalIndent(reports, "", " ")
		fmt.Println(string(out))
	} else {
		for _, r := range reports {
			fmt.Printf("Player: %s (%d game(s))\n", r.Player, r.Games)
			if len(r.Matches) == 0 {
				fmt.Println("  no matches above threshold")
				continue
			}
			w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "  LABEL\tZ\tCOSINE\tEVIDENCE\tSEARCH 1e-2\tSEARCH 1e-3\tSEARCH 1e-4")
			for _, m := range r.Matches {
				_, _ = fmt.Fprintf(w, "  %s\t%.2f\t%.3f\t%d\t%s\t%s\t%s\n",
					m.Label, m.Z, m.Cosine, m.EvidenceN,
					opsCell(m.SearchFPR, "fpr_1e2"), opsCell(m.SearchFPR, "fpr_1e3"), opsCell(m.SearchFPR, "fpr_1e4"))
			}
			_ = w.Flush()
			top := r.Matches[0]
			fmt.Printf("  → %s\n", interpretation(top.EvidenceN, top.SearchFPR, top.CatalogSize))
		}
	}
	if anyMatch {
		return exitOK
	}
	return exitNoMatch
}

func cmdSame(args []string) int {
	fs := flag.NewFlagSet("same", flag.ContinueOnError)
	a := fs.String("a", "", "directory or .rep file for side A")
	b := fs.String("b", "", "directory or .rep file for side B")
	nameA := fs.String("name-a", "", "select side A's player by name")
	nameB := fs.String("name-b", "", "select side B's player by name")
	asJSON := fs.Bool("json", false, "machine-readable output")
	strict := fs.Bool("strict", false, "exit with error if the model is synthetic")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *a == "" || *b == "" {
		return fail(fmt.Errorf("both --a and --b are required"))
	}

	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		return fail(err)
	}
	if code := warnIfSynthetic(scorer, *strict); code >= 0 {
		return code
	}

	sideGames := func(path, name string) ([]scfingerprint.PlayerGame, error) {
		dir, files := "", []string(nil)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			dir = path
		} else {
			files = []string{path}
		}
		paths, err := collectReplays(dir, files)
		if err != nil {
			return nil, err
		}
		obs, err := extractAll(paths)
		if err != nil {
			return nil, err
		}
		sel, err := selectObs(obs, name, -1)
		if err != nil {
			return nil, err
		}
		games := make([]scfingerprint.PlayerGame, len(sel))
		for i, o := range sel {
			games[i] = scfingerprint.PlayerGame{Vector: o.pf.Vector, Race: o.pf.Race}
		}
		return games, nil
	}

	gamesA, err := sideGames(*a, *nameA)
	if err != nil {
		return fail(fmt.Errorf("side A: %w", err))
	}
	gamesB, err := sideGames(*b, *nameB)
	if err != nil {
		return fail(fmt.Errorf("side B: %w", err))
	}

	v, err := scfingerprint.Same(gamesA, gamesB)
	if err != nil {
		return fail(err)
	}

	if *asJSON {
		out, _ := json.MarshalIndent(v, "", " ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("Z: %.2f  Cosine: %.3f  Evidence: %d games (%d + %d)\n", v.Z, v.Cosine, v.EvidenceN, len(gamesA), len(gamesB))
		fmt.Printf("→ %s\n", interpretationPairwise(v.EvidenceN, v.OperatingPoints))
	}
	if v.OperatingPoints["fpr_1e3"] {
		return exitOK
	}
	return exitNoMatch
}

func cmdEnroll(args []string) int {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	label := fs.String("label", "", "identity label for the fingerprint (required)")
	dir := fs.String("dir", "", "directory of replays")
	name := fs.String("name", "", "select the player by name (defaults to --label)")
	out := fs.String("o", "", "output file (default: <label>.fingerprint.json)")
	skipGate := fs.Bool("skip-gate", false, "skip the self-consistency gate (not recommended)")
	strict := fs.Bool("strict", false, "exit with error if the model is synthetic")
	positional, err := parseAll(fs, args)
	if err != nil {
		return exitError
	}
	if *label == "" {
		return fail(fmt.Errorf("--label is required"))
	}

	enrollScorer, err := scoring.NewFromEmbedded()
	if err != nil {
		return fail(err)
	}
	if code := warnIfSynthetic(enrollScorer, *strict); code >= 0 {
		return code
	}

	paths, err := collectReplays(*dir, positional)
	if err != nil {
		return fail(err)
	}
	obs, err := extractAll(paths)
	if err != nil {
		return fail(err)
	}
	selName := *name
	if selName == "" {
		selName = *label
	}
	sel, err := selectObs(obs, selName, -1)
	if err != nil {
		// Fall back: label may not equal any in-game name; require explicit --name.
		return fail(fmt.Errorf("%w (use --name to select the in-game player name)", err))
	}

	games := make([]scfingerprint.PlayerGame, len(sel))
	for i, o := range sel {
		games[i] = scfingerprint.PlayerGame{Vector: o.pf.Vector, Race: o.pf.Race}
	}
	fp, err := scfingerprint.Enroll(games, scfingerprint.Meta{Label: *label})
	if err != nil {
		return fail(err)
	}

	if !*skipGate {
		db, err := scfingerprint.NewDataset(nil)
		if err != nil {
			return fail(err)
		}
		score, err := hygiene.SelfConsistencyGate(fp, db.Scorer(), hygiene.DefaultThresholds())
		if err != nil {
			return fail(fmt.Errorf("%w (re-check the games belong to one person, or pass --skip-gate)", err))
		}
		fmt.Fprintf(os.Stderr, "self-consistency: %.3f (pass)\n", score)
	}

	blob, err := fp.MarshalString()
	if err != nil {
		return fail(err)
	}
	outPath := *out
	if outPath == "" {
		outPath = *label + ".fingerprint.json"
	}
	if err := os.WriteFile(outPath, []byte(blob+"\n"), 0o644); err != nil {
		return fail(err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d games)\n", outPath, fp.N())
	return exitOK
}

func cmdExtract(args []string) int {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	asJSON := fs.Bool("json", true, "machine-readable output (always on for extract)")
	positional, err := parseAll(fs, args)
	if err != nil {
		return exitError
	}
	_ = asJSON
	paths, err := collectReplays("", positional)
	if err != nil {
		return fail(err)
	}
	type extracted struct {
		File    string                    `json:"file"`
		Players []features.PlayerFeatures `json:"players"`
	}
	var out []extracted
	for _, path := range paths {
		pfs, err := features.ExtractFile(path)
		if err != nil {
			return fail(err)
		}
		out = append(out, extracted{File: path, Players: pfs})
	}
	data, _ := json.MarshalIndent(out, "", " ")
	fmt.Println(string(data))
	return exitOK
}

func cmdDatasetVerify(args []string) int {
	fs := flag.NewFlagSet("dataset verify", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return exitError
	}

	db, err := dataset.NewDefaultDataset(nil, dataset.ConfidenceCandidate)
	if err != nil {
		return fail(err)
	}
	var fps []*fingerprint.Fingerprint
	fps = append(fps, db.Fingerprints()...)

	findings, err := hygiene.VerifyCatalog(fps, db.Scorer(), hygiene.DefaultThresholds())
	if err != nil {
		return fail(err)
	}

	if *asJSON {
		if findings == nil {
			findings = []hygiene.Finding{}
		}
		out, _ := json.MarshalIndent(findings, "", " ")
		fmt.Println(string(out))
	} else {
		fmt.Printf("verified %d identities\n", db.Len())
		if len(findings) == 0 {
			fmt.Println("catalog is clean")
		}
		for _, f := range findings {
			score := ""
			if !math.IsNaN(f.Score) && f.Score != 0 {
				score = fmt.Sprintf(" (%.3f)", f.Score)
			}
			fmt.Printf("FINDING [%s] %v%s: %s\n", f.Kind, f.Labels, score, f.Message)
		}
	}
	if len(findings) > 0 {
		return exitNoMatch
	}
	return exitOK
}
