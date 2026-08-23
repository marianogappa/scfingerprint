package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/icza/screp/repparser"
	scfingerprint "github.com/marianogappa/scfingerprint"
	"github.com/marianogappa/scfingerprint/internal/dataset"
	"github.com/marianogappa/scfingerprint/internal/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/hygiene"
	"github.com/marianogappa/scfingerprint/internal/scoring"
)

const syntheticBanner = `
╔══════════════════════════════════════════════════════════════════╗
║  WARNING: model trained on SYNTHETIC data; scores are not       ║
║  meaningful. See docs/METHODOLOGY.md for details.               ║
╚══════════════════════════════════════════════════════════════════╝
`

// warnIfSynthetic prints the banner (suppressed below the default verbosity;
// the JSON carries model_is_synthetic regardless) and enforces --strict.
func warnIfSynthetic(isSynthetic, strict bool, verbosity int) int {
	if !isSynthetic {
		return -1
	}
	if verbosity >= 0 {
		fmt.Fprint(os.Stderr, syntheticBanner)
	}
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

// Confidence tiers, keyed on the false-positive rate a result achieves. For a
// 1:N search the rate is family-wise (Šidák-corrected for catalog size) and a
// decisive z margin over the runner-up can substitute for a strong rate — a
// real identification pulls away from the field. For a 1:1 comparison the
// rate is per-comparison, so the bars are one notch stricter.
const (
	fprStrong    = 0.01 // 1:N, at or below: the evidence is strong
	fprLead      = 0.10 // 1:N, at or below: worth following up
	marginStrong = 1.5  // 1:N, z gap to the runner-up that makes a lead-grade rate strong

	fprStrong1v1 = 0.001 // 1:1, at or below: the evidence is strong
	fprLead1v1   = 0.01  // 1:1, at or below: worth following up
)

func cmdMatch(args []string) int {
	fs := flag.NewFlagSet("match", flag.ContinueOnError)
	dir := fs.String("dir", "", "directory of replays for multi-game evidence")
	name := fs.String("name", "", "select the player by name")
	playerID := fs.Int("player", -1, "select the player by slot ID")
	minZ := fs.Float64("min-z", 2.0, "minimum calibrated z-score to report")
	minConfidence := fs.String("min-confidence", dataset.ConfidenceHigh, "minimum dataset confidence tier (confirmed/high/candidate)")
	strict := fs.Bool("strict", false, "exit with error if the model is synthetic")
	var out outputOpts
	addOutputFlags(fs, &out, true)
	positional, err := parseAll(fs, args)
	if err != nil {
		return exitError
	}
	bar, err := parseMinVerdict(out.minVerdict)
	if err != nil {
		return fail(err)
	}
	verb := out.verbosity()
	pal := newPalette(colorEnabled(out.noColor))

	lib, err := scfingerprint.BuiltinDataset(*minConfidence)
	if err != nil {
		return fail(err)
	}
	if code := warnIfSynthetic(lib.ModelIsSynthetic(), *strict, verb); code >= 0 {
		return code
	}
	if lib.Len() == 0 {
		return fail(fmt.Errorf("built-in dataset is empty at confidence tier %q", *minConfidence))
	}

	paths, err := collectReplays(*dir, positional)
	if err != nil {
		return fail(err)
	}
	obs, err := extractAll(paths)
	if err != nil {
		return fail(err)
	}

	var reports []playerReport

	if *dir != "" || *name != "" || *playerID >= 0 {
		// One identity across many games.
		sel, err := selectObs(obs, *name, *playerID)
		if err != nil {
			return fail(err)
		}
		games := make([]scfingerprint.PlayerGame, len(sel))
		files := make([]string, len(sel))
		for i, o := range sel {
			games[i] = scfingerprint.PlayerGame{Vector: o.pf.Vector, Race: o.pf.Race}
			files[i] = o.file
		}
		results, err := scfingerprint.MatchMany(games, lib, scfingerprint.WithMinZ(*minZ))
		if err != nil {
			return fail(err)
		}
		label := *name
		if label == "" {
			label = sel[0].pf.Name
		}
		r := playerReport{Player: label, Games: len(sel), Verdict: reportVerdict(results, *minZ), Matches: results}
		r.setFiles(files)
		reports = append(reports, r)
	} else {
		// Every player of one replay, one game each.
		for _, o := range obs {
			results, err := scfingerprint.MatchMany(
				[]scfingerprint.PlayerGame{{Vector: o.pf.Vector, Race: o.pf.Race}},
				lib, scfingerprint.WithMinZ(*minZ))
			if err != nil {
				return fail(err)
			}
			r := playerReport{Player: o.pf.Name, Games: 1, Verdict: reportVerdict(results, *minZ), Matches: results}
			r.setFiles([]string{o.file})
			reports = append(reports, r)
		}
	}

	for _, r := range reports {
		renderMatchReport(os.Stderr, pal, verb, bar, r)
	}
	if verb >= 2 {
		renderRunMeta(os.Stderr, pal, fmt.Sprintf("catalog size: %d (min confidence: %s)", lib.Len(), *minConfidence))
		fmt.Fprintln(os.Stderr)
	}
	if verb >= 0 {
		renderScale(os.Stderr, pal)
		renderHints(os.Stderr, pal, "the full candidate table", true)
	}

	if stdoutWantsJSON(out.jsonForce || out.jsonl) {
		if out.jsonl {
			printJSONLines(reports)
		} else {
			printJSON(reports)
		}
	}

	for _, r := range reports {
		if verdictRank[r.Verdict] >= verdictRank[bar] {
			return exitOK
		}
	}
	return exitNoMatch
}

// reportVerdict makes the call for one player's result list, or none when
// nothing survived the --min-z filter. Strong needs 3+ games and either a
// strong rate or a lead-grade rate with a decisive margin over the runner-up
// (when no runner-up survived the filter, the filter threshold is the
// margin's floor).
func reportVerdict(matches []scfingerprint.MatchResult, minZ float64) string {
	if len(matches) == 0 {
		return verdictNone
	}
	top := matches[0]
	margin := top.Z - minZ
	if len(matches) > 1 {
		margin = top.Z - matches[1].Z
	}
	switch {
	case top.SearchFPR > fprLead:
		return verdictWeak
	case top.EvidenceN < 3:
		return verdictLead
	case top.SearchFPR <= fprStrong || margin >= marginStrong:
		return verdictStrong
	default:
		return verdictLead
	}
}

func cmdSame(args []string) int {
	fs := flag.NewFlagSet("same", flag.ContinueOnError)
	a := fs.String("a", "", "directory or .rep file for side A")
	b := fs.String("b", "", "directory or .rep file for side B")
	nameA := fs.String("name-a", "", "select side A's player by name")
	nameB := fs.String("name-b", "", "select side B's player by name")
	strict := fs.Bool("strict", false, "exit with error if the model is synthetic")
	var out outputOpts
	addOutputFlags(fs, &out, false)
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if *a == "" || *b == "" {
		return fail(fmt.Errorf("both --a and --b are required"))
	}
	bar, err := parseMinVerdict(out.minVerdict)
	if err != nil {
		return fail(err)
	}
	verb := out.verbosity()
	pal := newPalette(colorEnabled(out.noColor))

	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		return fail(err)
	}
	if code := warnIfSynthetic(scorer.IsSynthetic(), *strict, verb); code >= 0 {
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
	report := sameReport{Tier: sameVerdict(v.FPR, v.EvidenceN), Verdict: v}

	renderSameReport(os.Stderr, pal, verb, bar, report, len(gamesA), len(gamesB))
	if verb >= 0 {
		renderScale(os.Stderr, pal)
		renderHints(os.Stderr, pal, "the raw scores and operating points", false)
	}
	if stdoutWantsJSON(out.jsonForce) {
		printJSON(report)
	}

	if verdictRank[report.Tier] >= verdictRank[bar] {
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
	if code := warnIfSynthetic(enrollScorer.IsSynthetic(), *strict, 0); code >= 0 {
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
		score, err := fp.SelfConsistencyGate()
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
		File    string                       `json:"file"`
		Players []scfingerprint.PlayerVector `json:"players"`
	}
	var out []extracted
	for _, path := range paths {
		r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
		if err != nil {
			return fail(err)
		}
		pvs, err := scfingerprint.Extract(r)
		if err != nil {
			return fail(err)
		}
		out = append(out, extracted{File: path, Players: pvs})
	}
	data, _ := json.MarshalIndent(out, "", " ")
	fmt.Println(string(data))
	return exitOK
}

func cmdDatasetVerify(args []string) int {
	fs := flag.NewFlagSet("dataset verify", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON on stdout even when it is a terminal")
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

	fmt.Fprintf(os.Stderr, "verified %d identities\n", db.Len())
	if len(findings) == 0 {
		fmt.Fprintln(os.Stderr, "catalog is clean")
	}
	for _, f := range findings {
		score := ""
		if !math.IsNaN(f.Score) && f.Score != 0 {
			score = fmt.Sprintf(" (%.3f)", f.Score)
		}
		fmt.Fprintf(os.Stderr, "FINDING [%s] %v%s: %s\n", f.Kind, f.Labels, score, f.Message)
	}
	if stdoutWantsJSON(*asJSON) {
		if findings == nil {
			findings = []hygiene.Finding{}
		}
		printJSON(findings)
	}
	if len(findings) > 0 {
		return exitNoMatch
	}
	return exitOK
}
