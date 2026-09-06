package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"text/tabwriter"

	scfingerprint "github.com/marianogappa/scfingerprint"
)

// The output contract: stdout carries structured output (JSON) and nothing
// else, ever; stderr carries the human-readable report. Both are emitted on
// every run, except that JSON is suppressed when stdout is a terminal (unless
// forced with --json/--jsonl) so interactive runs read as one report, and
// `scfingerprint match game.rep > out.json` still leaves clean JSON in the
// file while the terminal shows the human run.

// Verdict tiers, ordered. The exit code and the human ✓/✗ determination both
// key on the same tier, so they can never disagree.
const (
	verdictNone   = "none"
	verdictWeak   = "weak"
	verdictLead   = "lead"
	verdictStrong = "strong"
)

var verdictRank = map[string]int{
	verdictNone:   0,
	verdictWeak:   1,
	verdictLead:   2,
	verdictStrong: 3,
}

// sameVerdict makes the opinionated call for a 1:1 comparison, on the
// stricter per-comparison bars: strong needs both a 1 in 1,000 grade rate and
// 3+ games.
func sameVerdict(fpr float64, evidenceN int) string {
	switch {
	case fpr > fprLead1v1:
		return verdictWeak
	case evidenceN < 3:
		return verdictLead
	case fpr <= fprStrong1v1:
		return verdictStrong
	default:
		return verdictLead
	}
}

// parseMinVerdict validates a --min-verdict value. "any" is the loosest bar:
// any result that survived the --min-z filter at all.
func parseMinVerdict(s string) (string, error) {
	if s == "any" {
		return verdictWeak, nil
	}
	if _, ok := verdictRank[s]; !ok || s == verdictNone {
		return "", fmt.Errorf("invalid --min-verdict %q (want: strong, lead, weak or any)", s)
	}
	return s, nil
}

// outputOpts are the presentation flags shared by the commands that report
// verdicts.
type outputOpts struct {
	jsonForce  bool
	jsonl      bool
	noColor    bool
	minVerdict string
	quiet      bool
	silent     bool
	verbose    bool
	debug      bool
}

func addOutputFlags(fs *flag.FlagSet, o *outputOpts, withJSONL bool) {
	fs.BoolVar(&o.jsonForce, "json", false, "emit JSON on stdout even when it is a terminal")
	if withJSONL {
		fs.BoolVar(&o.jsonl, "jsonl", false, "emit one compact JSON object per line instead of an array")
	}
	fs.BoolVar(&o.noColor, "no-color", false, "disable colour on stderr")
	fs.StringVar(&o.minVerdict, "min-verdict", verdictLead, "verdict tier required to exit 0 (strong/lead/weak/any)")
	fs.BoolVar(&o.quiet, "q", false, "only the ✓/✗ determination line on stderr")
	fs.BoolVar(&o.silent, "qq", false, "nothing on stderr; exit code and stdout only")
	fs.BoolVar(&o.verbose, "v", false, "add the full candidate table")
	fs.BoolVar(&o.debug, "vv", false, "add model tag, feature version and per-game breakdown")
}

// verbosity resolves the four flags into one level: -2 silent, -1 quiet,
// 0 default, 1 verbose, 2 debug.
func (o outputOpts) verbosity() int {
	switch {
	case o.silent:
		return -2
	case o.quiet:
		return -1
	case o.debug:
		return 2
	case o.verbose:
		return 1
	default:
		return 0
	}
}

// palette holds ANSI codes, all empty when colour is off.
type palette struct{ green, red, yellow, cyan, bold, dim, reset string }

func newPalette(enabled bool) palette {
	if !enabled {
		return palette{}
	}
	return palette{
		green:  "\x1b[32m",
		red:    "\x1b[31m",
		yellow: "\x1b[33m",
		cyan:   "\x1b[36m",
		bold:   "\x1b[1m",
		dim:    "\x1b[2m",
		reset:  "\x1b[0m",
	}
}

// tier returns the colour that carries a verdict tier's meaning.
func (pal palette) tier(t string) string {
	switch t {
	case verdictStrong:
		return pal.green
	case verdictLead:
		return pal.yellow
	default:
		return pal.red
	}
}

// name renders an identity (a player or a catalog label) in colour.
func (pal palette) name(s string) string {
	return pal.cyan + s + pal.reset
}

// colorEnabled auto-detects: colour only on a stderr terminal, and never when
// NO_COLOR is set or --no-color passed.
func colorEnabled(noColorFlag bool) bool {
	if noColorFlag || os.Getenv("NO_COLOR") != "" {
		return false
	}
	return isTerminal(os.Stderr)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// stdoutWantsJSON decides whether structured output is emitted: always when
// stdout is piped or redirected, on a terminal only when forced.
func stdoutWantsJSON(forced bool) bool {
	return forced || !isTerminal(os.Stdout)
}

func printJSON(v any) {
	out, _ := json.MarshalIndent(v, "", " ")
	fmt.Println(string(out))
}

func printJSONLines[T any](items []T) {
	for _, it := range items {
		out, _ := json.Marshal(it)
		fmt.Println(string(out))
	}
}

// playerReport is one player's result: the machine shape on stdout and the
// input to the human rendering on stderr. Verdict carries the determination
// explicitly so consumers never re-derive the call from raw numbers.
type playerReport struct {
	File    string                      `json:"file,omitempty"`
	Files   []string                    `json:"files,omitempty"`
	Player  string                      `json:"player"`
	Games   int                         `json:"games"`
	Verdict string                      `json:"verdict"`
	Matches []scfingerprint.MatchResult `json:"matches"`
	Notes   []string                    `json:"notes,omitempty"`
}

// setFiles records provenance: `file` for the common one-replay case (the
// shape scripts select on), `files` for multi-game evidence.
func (r *playerReport) setFiles(files []string) {
	if len(files) == 1 {
		r.File = files[0]
		return
	}
	r.Files = files
}

// renderMatchReport writes one player's human-readable report. bar is the
// --min-verdict tier; the ✓/✗ call is "did this player meet the bar", so it
// always agrees with the exit code.
func renderMatchReport(w io.Writer, pal palette, verb int, bar string, r playerReport) {
	if verb <= -2 {
		return
	}
	pass := verdictRank[r.Verdict] >= verdictRank[bar]

	// The wording scales with the confidence: only a strong verdict earns
	// "is"; a lead only "looks like", per docs/METHODOLOGY.md.
	switch {
	case pass && r.Verdict == verdictStrong:
		_, _ = fmt.Fprintf(w, "%s%s✓ MATCH:%s %s is %s\n",
			pal.bold, pal.green, pal.reset, pal.name(r.Player), pal.name(r.Matches[0].Label))
	case pass && r.Verdict == verdictLead:
		_, _ = fmt.Fprintf(w, "%s%s✓ LEAD:%s %s looks like %s, pending more games\n",
			pal.bold, pal.yellow, pal.reset, pal.name(r.Player), pal.name(r.Matches[0].Label))
	case pass:
		_, _ = fmt.Fprintf(w, "%s%s✓ WEAK SIGNAL:%s %s has only noise level candidates (best: %s)\n",
			pal.bold, pal.yellow, pal.reset, pal.name(r.Player), pal.name(r.Matches[0].Label))
	case verdictRank[r.Verdict] >= verdictRank[verdictLead]:
		_, _ = fmt.Fprintf(w, "%s%s✗ NO MATCH:%s %s has only a %s (%s), below the %s bar\n",
			pal.bold, pal.red, pal.reset, pal.name(r.Player), r.Verdict, pal.name(r.Matches[0].Label), bar)
	default:
		_, _ = fmt.Fprintf(w, "%s%s✗ NO MATCH:%s %s is not anyone in the catalog\n",
			pal.bold, pal.red, pal.reset, pal.name(r.Player))
	}
	if verb <= -1 {
		return
	}

	_, _ = fmt.Fprintln(w)
	if r.Verdict != verdictNone {
		_, _ = fmt.Fprintf(w, "  Confidence: %s%s%s%s (%s)\n\n",
			pal.bold, pal.tier(r.Verdict), r.Verdict, pal.reset, gamesPhrase(r.Matches[0].EvidenceN))
	}
	writeBlock(w, pal, "Why:", matchWhyLines(r))
	renderRegistryLine(w, pal, verb, r)
	if pass && r.Matches[0].Liquipedia != "" {
		_, _ = fmt.Fprintf(w, "\n  Liquipedia: %s%s%s\n", pal.cyan, r.Matches[0].Liquipedia, pal.reset)
	}

	if verb >= 1 && len(r.Matches) > 0 {
		_, _ = fmt.Fprintln(w)
		tw := tabwriter.NewWriter(w, 2, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  LABEL\tZ\tCOSINE\tGAMES\tSTRANGER SCORES THIS HIGH")
		for _, m := range r.Matches {
			_, _ = fmt.Fprintf(tw, "  %s\t%.2f\t%.3f\t%d\t%s\n", m.Label, m.Z, m.Cosine, m.EvidenceN, fprCell(m.SearchFPR))
		}
		_ = tw.Flush()
	}
	if verb >= 2 {
		files := r.Files
		if r.File != "" {
			files = []string{r.File}
		}
		if len(files) > 0 {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintf(w, "  %sEvidence files:%s\n", pal.dim, pal.reset)
			for _, f := range files {
				_, _ = fmt.Fprintf(w, "  %s  %s%s\n", pal.dim, f, pal.reset)
			}
		}
	}
	_, _ = fmt.Fprintln(w)
}

// renderRegistryLine reports what the identity map says about this account
// name, next to the fingerprint's verdict and never mixed into it.
//
// Agreement is worth one quiet line. Disagreement is the finding: the account
// name says one player and the way the game was played says another, which
// points at a shared or sold account or a stale registry entry. Silence when
// the account is simply not in the map — most accounts are not.
func renderRegistryLine(w io.Writer, pal palette, verb int, r playerReport) {
	if len(r.Matches) == 0 {
		return
	}
	op := r.Matches[0].Registry
	if op == nil {
		if verb >= 1 {
			_, _ = fmt.Fprintf(w, "\n  %sIdentity map: no entry for the account name %q.%s\n", pal.dim, r.Player, pal.reset)
		}
		return
	}

	stale := fmt.Sprintf("%s, last verified %s", op.Source, op.LastVerified)
	switch {
	case op.Agrees:
		_, _ = fmt.Fprintf(w, "\n  %sIdentity map: agrees.%s %s is %s's account (%s).\n",
			pal.green, pal.reset, op.Toon, pal.name(op.Name), stale)
	default:
		_, _ = fmt.Fprintf(w, "\n  %s%sIdentity map: DISAGREES.%s %s is registered to %s, not %s (%s).\n",
			pal.bold, pal.yellow, pal.reset, op.Toon, pal.name(op.Name), pal.name(r.Matches[0].Label), stale)
		_, _ = fmt.Fprintf(w, "  %sA shared or sold account, or a stale entry — worth a look either way.%s\n", pal.dim, pal.reset)
	}
	if op.Ambiguous {
		_, _ = fmt.Fprintf(w, "  %sThese games span more than one registered account; only one is shown.%s\n", pal.dim, pal.reset)
	}
	_, _ = fmt.Fprintf(w, "  %sA name lookup, not evidence: it never affects the score above.%s\n", pal.dim, pal.reset)
}

// matchWhyLines justifies the call so the opinion is auditable rather than
// magic.
func matchWhyLines(r playerReport) []string {
	if len(r.Matches) == 0 {
		return []string{"no candidate scored above the reporting threshold (--min-z)."}
	}
	top := r.Matches[0]
	var lines []string

	switch {
	case len(r.Matches) > 1 && r.Verdict == verdictWeak:
		next := r.Matches[1]
		lines = append(lines, fmt.Sprintf("the best candidate (%s, %.2f) is only %.2f clear of the next one (%s, %.2f),",
			top.Label, top.Z, top.Z-next.Z, next.Label, next.Z))
	case len(r.Matches) > 1:
		next := r.Matches[1]
		lines = append(lines, fmt.Sprintf("%s scored %.2f, which is %.2f clear of the next candidate (%s, %.2f).",
			top.Label, top.Z, top.Z-next.Z, next.Label, next.Z))
	default:
		lines = append(lines, fmt.Sprintf("%s scored %.2f, and no other candidate cleared the reporting threshold.", top.Label, top.Z))
	}
	continuesClause := r.Verdict == verdictWeak && len(r.Matches) > 1
	lines = append(lines, strangerLine(top.SearchFPR, fmt.Sprintf("against a %d-player catalog", top.CatalogSize), continuesClause))
	if line := matchTierLine(r); line != "" {
		lines = append(lines, line)
	}
	return lines
}

// matchTierLine explains what pins a search result to its tier.
func matchTierLine(r playerReport) string {
	top := r.Matches[0]
	switch r.Verdict {
	case verdictStrong:
		switch {
		case top.SearchFPR <= fprStrong:
			return fmt.Sprintf("%d games agreeing at that rate is what makes this strong. Still confirm by hand before acting on it.", top.EvidenceN)
		case len(r.Matches) > 1:
			return fmt.Sprintf("%d games agreeing while pulling %.2f z clear of the field is what makes this strong. Still confirm by hand before acting on it.",
				top.EvidenceN, top.Z-r.Matches[1].Z)
		default:
			return fmt.Sprintf("%d games agreeing with no rival candidate in sight is what makes this strong. Still confirm by hand before acting on it.", top.EvidenceN)
		}
	case verdictLead:
		if top.EvidenceN < 3 {
			return fmt.Sprintf("Only %s keeps this a lead rather than strong; get 3+ games.", gamesPhrase(top.EvidenceN))
		}
		if len(r.Matches) > 1 && top.Z-r.Matches[1].Z < marginStrong {
			return "The thin margin over the runner-up keeps this a lead; a real identification pulls away from the field."
		}
		return "Worth following up with more games before treating it as an identification."
	case verdictWeak:
		return "That is coin-flip territory, not evidence of anything."
	}
	return ""
}

// strangerLine phrases the false-positive rate. asClause continues a weak
// sentence ("...is only 1.00 clear of the next one, and a stranger...").
func strangerLine(fpr float64, scope string, asClause bool) string {
	switch {
	case fpr >= 1:
		if asClause {
			return "and that score clears no operating point; a stranger reaches it routinely."
		}
		return "That score clears no operating point; a stranger reaches it routinely."
	case fpr <= 0:
		return fmt.Sprintf("A stranger essentially never reaches this score (better than 1 in 100,000) %s.", scope)
	case asClause:
		return fmt.Sprintf("and a stranger reaches that score roughly %s times %s.", oddsPhrase(fpr), scope)
	default:
		return fmt.Sprintf("A stranger reaches this score about %s times %s.", oddsPhrase(fpr), scope)
	}
}

// sameTierLine explains what pins a 1:1 verdict to its tier.
func sameTierLine(verdict string, evidenceN int) string {
	switch verdict {
	case verdictStrong:
		return fmt.Sprintf("%d games agreeing at that rate is what makes this strong rather than a lead. Still confirm by hand before acting on it.", evidenceN)
	case verdictLead:
		if evidenceN < 3 {
			return fmt.Sprintf("Only %s keeps this a lead rather than strong; get 3+ games.", gamesPhrase(evidenceN))
		}
		return "For a direct comparison the strong bar is 1 in 1,000; this is a lead worth following up."
	case verdictWeak:
		return "That is coin-flip territory, not evidence of anything."
	default:
		return ""
	}
}

func gamesPhrase(n int) string {
	if n == 1 {
		return "1 game of evidence"
	}
	return fmt.Sprintf("%d games of evidence", n)
}

// writeBlock prints an indented, label-aligned block:
//
//	Why: first line
//	     second line
func writeBlock(w io.Writer, pal palette, label string, lines []string) {
	indent := fmt.Sprintf("%*s", len(label)+3, "")
	for i, line := range lines {
		if i == 0 {
			_, _ = fmt.Fprintf(w, "  %s%s%s %s\n", pal.bold, label, pal.reset, line)
			continue
		}
		_, _ = fmt.Fprintf(w, "%s%s\n", indent, line)
	}
}

// renderScale explains the confidence tiers, so a first-time reader knows
// where a verdict sits without consulting the docs. Printed once per run.
func renderScale(w io.Writer, pal palette) {
	_, _ = fmt.Fprintf(w, "  %sScale:%s %snone%s < %sweak%s < %slead%s < %sstrong%s\n",
		pal.bold, pal.reset,
		pal.red, pal.reset, pal.red, pal.reset, pal.yellow, pal.reset, pal.green, pal.reset)
	_, _ = fmt.Fprintf(w, "    %sstrong%s  treat as an identification, then confirm by hand\n", pal.green, pal.reset)
	_, _ = fmt.Fprintf(w, "    %slead%s    promising; means little until confirmed with more games\n", pal.yellow, pal.reset)
	_, _ = fmt.Fprintf(w, "    %sweak%s    the score a stranger gets; not evidence of anything\n", pal.red, pal.reset)
	_, _ = fmt.Fprintf(w, "    %snone%s    nothing cleared the reporting threshold\n", pal.red, pal.reset)
	_, _ = fmt.Fprintln(w)
}

// renderHints tells the reader how to re-run for more or less output. Printed
// once per run, after the scale.
func renderHints(w io.Writer, pal palette, vShows string, withJSONL bool) {
	_, _ = fmt.Fprintf(w, "%s  Re-run with -v for %s, -vv for run metadata, -q or -qq for less.%s\n", pal.dim, vShows, pal.reset)
	_, _ = fmt.Fprintf(w, "%s  Exit code 0 means the bar was met; set the bar with --min-verdict strong|lead|weak|any.%s\n", pal.dim, pal.reset)
	if withJSONL {
		_, _ = fmt.Fprintf(w, "%s  JSON goes to stdout when piped or redirected; --jsonl streams one object per line.%s\n", pal.dim, pal.reset)
	} else {
		_, _ = fmt.Fprintf(w, "%s  JSON goes to stdout when piped or redirected.%s\n", pal.dim, pal.reset)
	}
}

// renderRunMeta prints the -vv run metadata shared by match and same.
func renderRunMeta(w io.Writer, pal palette, extra ...string) {
	if tag, err := scfingerprint.ModelTag(); err == nil {
		_, _ = fmt.Fprintf(w, "%s  model: %s%s\n", pal.dim, tag, pal.reset)
	}
	_, _ = fmt.Fprintf(w, "%s  feature version: %d%s\n", pal.dim, scfingerprint.FeatureVersion(), pal.reset)
	for _, line := range extra {
		_, _ = fmt.Fprintf(w, "%s  %s%s\n", pal.dim, line, pal.reset)
	}
}

// sameReport is the machine shape of a `same` run: the pairwise verdict plus
// the explicit determination tier.
type sameReport struct {
	Tier string `json:"verdict"`
	scfingerprint.Verdict
}

// renderSameReport writes the human-readable report for a pairwise
// comparison.
func renderSameReport(w io.Writer, pal palette, verb int, bar string, r sameReport, gamesA, gamesB int) {
	if verb <= -2 {
		return
	}
	pass := verdictRank[r.Tier] >= verdictRank[bar]
	switch {
	case pass && r.Tier == verdictStrong:
		_, _ = fmt.Fprintf(w, "%s%s✓ MATCH:%s the two sides are the same player\n", pal.bold, pal.green, pal.reset)
	case pass && r.Tier == verdictLead:
		_, _ = fmt.Fprintf(w, "%s%s✓ LEAD:%s the two sides look like the same player, pending more games\n", pal.bold, pal.yellow, pal.reset)
	case pass:
		_, _ = fmt.Fprintf(w, "%s%s✓ WEAK SIGNAL:%s only a weak signal that the two sides are the same player\n", pal.bold, pal.yellow, pal.reset)
	default:
		_, _ = fmt.Fprintf(w, "%s%s✗ NO MATCH:%s the evidence does not show the two sides are the same player\n", pal.bold, pal.red, pal.reset)
	}
	if verb <= -1 {
		return
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintf(w, "  Confidence: %s%s%s%s (%s)\n\n",
		pal.bold, pal.tier(r.Tier), r.Tier, pal.reset, gamesPhrase(r.EvidenceN))
	lines := []string{
		fmt.Sprintf("the pair scored z=%.2f (cosine %.3f) on %d games (%d + %d).", r.Z, r.Cosine, r.EvidenceN, gamesA, gamesB),
		strangerLine(r.FPR, "in a direct 1:1 comparison", false),
	}
	if line := sameTierLine(r.Tier, r.EvidenceN); line != "" {
		lines = append(lines, line)
	}
	writeBlock(w, pal, "Why:", lines)

	if verb >= 1 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintf(w, "  Z: %.2f  Cosine: %.3f  Evidence: %d games (%d + %d)\n", r.Z, r.Cosine, r.EvidenceN, gamesA, gamesB)
		_, _ = fmt.Fprint(w, "  Operating points:")
		for _, name := range []string{"fpr_1e2", "fpr_1e3", "fpr_1e4"} {
			mark := "✗"
			if r.OperatingPoints[name] {
				mark = "✓"
			}
			_, _ = fmt.Fprintf(w, "  %s %s", name, mark)
		}
		_, _ = fmt.Fprintln(w)
	}
	if verb >= 2 {
		renderRunMeta(w, pal)
	}
	_, _ = fmt.Fprintln(w)
}

// fprCell renders a result's false-positive rate for the results table.
func fprCell(fpr float64) string {
	if fpr >= 1 {
		return "below any bar"
	}
	return oddsPhrase(fpr)
}

// oddsPhrase renders a false-positive rate as "1 in N", which reads better than
// a percentage at the small rates that matter here.
func oddsPhrase(fpr float64) string {
	if fpr <= 0 {
		return "far better than 1 in 100,000"
	}
	// Round before bucketing: 1/(1-(1-1e-3)) lands on 999.999... in float,
	// which would otherwise print as "1 in 1000" rather than "1 in 1,000".
	n := math.Round(1 / fpr)
	if n >= 1000 {
		return fmt.Sprintf("1 in %.0f,000", math.Round(n/1000))
	}
	return fmt.Sprintf("1 in %.0f", n)
}
