package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/marianogappa/scfingerprint"
)

// firstRegistryAccountWithToons picks a real shipped entry to drive the tests,
// so they exercise the embedded map rather than a fixture of it.
func firstRegistryAccountWithToons(t *testing.T) scfingerprint.RegistryAccount {
	t.Helper()
	reg, err := scfingerprint.BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range reg.Accounts() {
		if a.Name != "" && len(a.Toons) > 0 {
			return a
		}
	}
	t.Fatal("the shipped registry has no named account with toons")
	return scfingerprint.RegistryAccount{}
}

func TestRunWhoisArgErrors(t *testing.T) {
	if code := run([]string{"whois"}); code != exitError {
		t.Fatalf("whois with no argument: exit %d, want %d", code, exitError)
	}
	if code := run([]string{"whois", "a", "b"}); code != exitError {
		t.Fatalf("whois with two arguments: exit %d, want %d", code, exitError)
	}
}

func TestRunWhoisResolvesAllThreeWaysIn(t *testing.T) {
	acc := firstRegistryAccountWithToons(t)

	// A toon, a progamer name and an aurora id are all valid ways to ask, so
	// callers never have to know which kind of string they are holding.
	for _, q := range []string{
		acc.Toons[0].Toon,
		strings.ToLower(acc.Toons[0].Toon),
		acc.Name,
		strconv.FormatInt(acc.AuroraID, 10),
	} {
		if code := run([]string{"whois", q}); code != exitOK {
			t.Fatalf("whois %q: exit %d, want %d", q, code, exitOK)
		}
	}
}

func TestRunWhoisUnknownExitsOne(t *testing.T) {
	// Not found is a determination, not an error: exit 1, like a no-match.
	if code := run([]string{"whois", "definitely-not-a-progamer-toon"}); code != exitNoMatch {
		t.Fatalf("whois unknown: exit %d, want %d", code, exitNoMatch)
	}
}

func TestWhoisResolutionOrderPrefersTheMostSpecificQuestion(t *testing.T) {
	reg, err := scfingerprint.BuiltinRegistry()
	if err != nil {
		t.Fatal(err)
	}
	acc := firstRegistryAccountWithToons(t)

	if _, by := resolveWhois(reg, strconv.FormatInt(acc.AuroraID, 10)); by != "aurora_id" {
		t.Fatalf("aurora id resolved by %q", by)
	}
	if _, by := resolveWhois(reg, acc.Toons[0].Toon); by != "toon" {
		t.Fatalf("toon resolved by %q", by)
	}
	accs, by := resolveWhois(reg, acc.Name)
	if by != "name" || len(accs) == 0 {
		t.Fatalf("name resolved by %q into %d accounts", by, len(accs))
	}
	if got, by := resolveWhois(reg, "nobody-at-all"); by != "" || got != nil {
		t.Fatalf("unknown query resolved by %q into %v", by, got)
	}
}

func TestWhoisJSONCarriesProvenanceAndCatalogLink(t *testing.T) {
	acc := firstRegistryAccountWithToons(t)
	out := captureStdout(t, func() {
		if code := run([]string{"whois", "--json", acc.Toons[0].Toon}); code != exitOK {
			t.Fatalf("whois --json: exit %d", code)
		}
	})

	var report whoisReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("parsing whois JSON: %v\n%s", err, out)
	}
	if !report.Found || len(report.Accounts) != 1 {
		t.Fatalf("report = %+v", report)
	}
	got := report.Accounts[0]
	if got.AuroraID != acc.AuroraID {
		t.Fatalf("aurora id = %d, want %d", got.AuroraID, acc.AuroraID)
	}
	// Staleness has to be visible without a second call, or the caller has no
	// way to judge how much the answer is worth.
	if got.Source == "" || got.LastVerified == "" {
		t.Fatalf("account has no provenance: %+v", got)
	}
	if report.RegistryGenerated == "" || report.RegistryAccounts == 0 {
		t.Fatalf("report has no registry provenance: %+v", report)
	}
	// The catalog cross-reference says whether the claim is checkable at all.
	if got.InCatalog && got.CatalogID == "" {
		t.Fatalf("in-catalog account has no catalog id: %+v", got)
	}
}

func TestWhoisHumanOutputRefusesToBeEvidence(t *testing.T) {
	// The registry is the one part of this tool that is a name lookup, so the
	// human output has to say so every time.
	acc := firstRegistryAccountWithToons(t)
	report := whoisReport{
		Query: acc.Toons[0].Toon, MatchedBy: "toon", Found: true,
		RegistryGenerated: "2026-09-06", RegistryAccounts: 1,
		Accounts: []whoisAccount{annotateWithCatalog(acc)},
	}
	out := renderToString(t, func(w io.Writer) { renderWhois(w, newPalette(false), report) })
	for _, want := range []string{"not a fingerprint", "second opinion", "scfingerprint match", acc.Toons[0].Toon} {
		if !strings.Contains(out, want) {
			t.Fatalf("whois output is missing %q:\n%s", want, out)
		}
	}

	miss := whoisReport{Query: "ghost", RegistryAccounts: 154, RegistryGenerated: "2026-09-06"}
	out = renderToString(t, func(w io.Writer) { renderWhois(w, newPalette(false), miss) })
	// A miss must not read as "this is not a pro" — most accounts are absent.
	if !strings.Contains(out, "says nothing about who the player is") {
		t.Fatalf("whois miss output does not disclaim itself:\n%s", out)
	}
}

// renderToString runs a renderer into a buffer and returns what it wrote.
func renderToString(t *testing.T, render func(io.Writer)) string {
	t.Helper()
	var buf bytes.Buffer
	render(&buf)
	return buf.String()
}

func TestRegistryLineReportsAgreementAndDisagreement(t *testing.T) {
	opinion := func(name string, agrees bool) *scfingerprint.RegistryOpinion {
		return &scfingerprint.RegistryOpinion{
			Name: name, Toon: "JSA_Larva", AuroraID: 7,
			Source: "bnet-webapi", LastVerified: "2026-09-06", Agrees: agrees,
		}
	}
	lineFor := func(r playerReport) string {
		return renderToString(t, func(w io.Writer) { renderRegistryLine(w, newPalette(false), 0, r) })
	}

	agree := lineFor(playerReport{Player: "JSA_Larva", Matches: []scfingerprint.MatchResult{
		{Label: "Larva", Registry: opinion("Larva", true)},
	}})
	if !strings.Contains(agree, "agrees") || !strings.Contains(agree, "last verified 2026-09-06") {
		t.Fatalf("agreement line = %q", agree)
	}

	// Disagreement is the finding the issue asks for: it must name both
	// opinions and say why the reader should care.
	disagree := lineFor(playerReport{Player: "JSA_Larva", Matches: []scfingerprint.MatchResult{
		{Label: "Queen", Registry: opinion("Larva", false)},
	}})
	for _, want := range []string{"DISAGREES", "Larva", "Queen", "shared or sold"} {
		if !strings.Contains(disagree, want) {
			t.Fatalf("disagreement line is missing %q: %q", want, disagree)
		}
	}

	// Either way it must disclaim itself, so nobody reads it as a score.
	for _, line := range []string{agree, disagree} {
		if !strings.Contains(line, "never affects the score") {
			t.Fatalf("registry line does not disclaim itself: %q", line)
		}
	}

	// An account the map does not know stays silent at default verbosity —
	// most accounts are not progamer accounts and saying so every time is noise.
	quiet := lineFor(playerReport{Player: "stranger", Matches: []scfingerprint.MatchResult{{Label: "Queen"}}})
	if quiet != "" {
		t.Fatalf("unregistered account produced output at default verbosity: %q", quiet)
	}
	verbose := renderToString(t, func(w io.Writer) {
		renderRegistryLine(w, newPalette(false), 1, playerReport{
			Player: "stranger", Matches: []scfingerprint.MatchResult{{Label: "Queen"}},
		})
	})
	if !strings.Contains(verbose, "no entry") {
		t.Fatalf("verbose line for an unregistered account = %q", verbose)
	}

	// With no surviving candidate there is nothing to agree or disagree with.
	if got := lineFor(playerReport{Player: "x"}); got != "" {
		t.Fatalf("registry line rendered with no candidates: %q", got)
	}
}

func TestMatchCrossReferencesTheRegistryAndCanBeTurnedOff(t *testing.T) {
	// The fixture players are not progamers, so the cross-reference is silent;
	// what matters is that enabling it changes no verdict and that --no-registry
	// is accepted.
	var withReg, without string
	withReg = captureStdout(t, func() { run([]string{"match", "--json", fixtureRep}) })
	without = captureStdout(t, func() { run([]string{"match", "--json", "--no-registry", fixtureRep}) })

	var a, b []playerReport
	if err := json.Unmarshal([]byte(withReg), &a); err != nil {
		t.Fatalf("parsing match JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(without), &b); err != nil {
		t.Fatalf("parsing --no-registry match JSON: %v", err)
	}
	if len(a) != len(b) {
		t.Fatalf("report counts differ: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Verdict != b[i].Verdict || len(a[i].Matches) != len(b[i].Matches) {
			t.Fatalf("the registry changed report %d: %+v vs %+v", i, a[i], b[i])
		}
		for j := range a[i].Matches {
			if a[i].Matches[j].Z != b[i].Matches[j].Z {
				t.Fatalf("the registry changed a z-score: %v vs %v", a[i].Matches[j].Z, b[i].Matches[j].Z)
			}
			if b[i].Matches[j].Registry != nil {
				t.Fatal("--no-registry still attached an opinion")
			}
		}
	}
}

func TestDatasetVerifyAccountGates(t *testing.T) {
	// The shipped catalog must be clean on the account gates that need no
	// corpus, since those run for every installed binary.
	if code := run([]string{"dataset", "verify"}); code != exitOK {
		t.Fatalf("dataset verify: exit %d, want %d — two catalog entries on one Battle.net account?", code, exitOK)
	}

	// With corpus metadata the replay-level gate also runs. It is expected to
	// have findings today (a handful of entries carry a stray replay), so the
	// assertion is that the gate produces the shape we can act on, not that
	// it is silent.
	out := captureStdout(t, func() {
		run([]string{"dataset", "verify", "--json", "--replay-metadata", "../../corpus/replays.jsonl"})
	})
	var findings []struct {
		Kind    string   `json:"Kind"`
		Labels  []string `json:"Labels"`
		Message string   `json:"Message"`
	}
	if err := json.Unmarshal([]byte(out), &findings); err != nil {
		t.Fatalf("parsing verify JSON: %v\n%s", err, out)
	}
	for _, f := range findings {
		if len(f.Labels) == 0 || f.Message == "" {
			t.Fatalf("finding names no entry or gives no message: %+v", f)
		}
		// An account-level finding must always name the account, or there is
		// nothing to go and check.
		switch f.Kind {
		case "account_shared", "enrollment_shared", "enrollment_outliers", "enrollment_ambiguous":
			if !strings.ContainsAny(f.Message, "0123456789") {
				t.Fatalf("account finding does not name the account: %+v", f)
			}
		}
	}

	// A bad metadata path must fail loudly rather than silently skipping the
	// gate and reporting a clean catalog.
	if code := run([]string{"dataset", "verify", "--replay-metadata", "nonexistent.jsonl"}); code != exitError {
		t.Fatalf("dataset verify with a missing metadata file: exit %d, want %d", code, exitError)
	}
}

func TestCollectAccountEvidenceResolvesTheShippedCatalog(t *testing.T) {
	ids, _, err := loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := collectAccountEvidence(ids, "../../corpus/replays.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != len(ids) {
		t.Fatalf("evidence for %d entries, want %d", len(evidence), len(ids))
	}

	// Every catalog label must resolve to a registry account, or a match
	// verdict and a whois answer would disagree about who a name refers to.
	manifestSize := make(map[string]int, len(ids))
	for _, id := range ids {
		manifestSize[id.ID] = len(id.ReplayManifest)
	}
	for _, e := range evidence {
		if len(e.ByName) == 0 {
			t.Errorf("catalog entry %q resolves to no registry account", e.Label)
		}
		// And every manifest replay must have a metadata row. A partial
		// join is the dangerous case: the gate would still report, but on
		// evidence it only half has, and it would look identical to a
		// clean result.
		if want := manifestSize[e.Label]; e.ReplayCount != want {
			t.Errorf("%s: resolved %d of %d manifest replays — the account gates would judge partial evidence",
				e.Label, e.ReplayCount, want)
		}
		if e.ReplayCount > 0 && len(e.ByReplays) == 0 {
			t.Errorf("%s: %d replays resolved but no account behind them", e.Label, e.ReplayCount)
		}
	}
}
