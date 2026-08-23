package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marianogappa/scfingerprint/internal/fingerprint"
)

const fixtureRep = "../../internal/features/testdata/01_zvt_zergling_rush.rep"
const fixtureRep2 = "../../internal/features/testdata/03_zvp_progamer_soma.rep"

func TestRunUsageAndErrors(t *testing.T) {
	if code := run(nil); code != exitError {
		t.Fatalf("no args: exit %d, want %d", code, exitError)
	}
	if code := run([]string{"bogus"}); code != exitError {
		t.Fatalf("unknown command: exit %d, want %d", code, exitError)
	}
	if code := run([]string{"help"}); code != exitOK {
		t.Fatalf("help: exit %d, want %d", code, exitOK)
	}
	if code := run([]string{"dataset", "bogus"}); code != exitError {
		t.Fatalf("unknown dataset subcommand: exit %d, want %d", code, exitError)
	}
}

func TestRunExtract(t *testing.T) {
	if code := run([]string{"extract", fixtureRep}); code != exitOK {
		t.Fatalf("extract: exit %d, want %d", code, exitOK)
	}
	if code := run([]string{"extract", "nonexistent.rep"}); code != exitError {
		t.Fatalf("extract missing file: exit %d, want %d", code, exitError)
	}
}

func TestRunDatasetVerify(t *testing.T) {
	// The committed dataset must be clean against the embedded model.
	if code := run([]string{"dataset", "verify"}); code != exitOK {
		t.Fatalf("dataset verify: exit %d, want %d (catalog not clean?)", code, exitOK)
	}
	if code := run([]string{"dataset", "verify", "--json"}); code != exitOK {
		t.Fatalf("dataset verify --json: exit %d", code)
	}
}

func TestRunMatch(t *testing.T) {
	// Fixture players aren't in the built-in dataset: mechanically fine,
	// but no match is the correct outcome → exit 1.
	code := run([]string{"match", fixtureRep, "--min-z", "1e18"})
	if code != exitNoMatch {
		t.Fatalf("match with impossible threshold: exit %d, want %d", code, exitNoMatch)
	}
	if code := run([]string{"match", fixtureRep, "--json", "--min-z", "1e18"}); code != exitNoMatch {
		t.Fatalf("match --json: exit %d, want %d", code, exitNoMatch)
	}
	if code := run([]string{"match"}); code != exitError {
		t.Fatalf("match without replays: exit %d, want %d", code, exitError)
	}
	if code := run([]string{"match", fixtureRep, "--name", "NoSuchPlayer"}); code != exitError {
		t.Fatalf("match with unknown name: exit %d, want %d", code, exitError)
	}
}

func TestRunSame(t *testing.T) {
	code := run([]string{"same", "--a", fixtureRep, "--b", fixtureRep2, "--name-a", "Skins_", "--name-b", "LC_Tyson", "--json"})
	if code != exitOK && code != exitNoMatch {
		t.Fatalf("same: exit %d, want 0 or 1", code)
	}
	if code := run([]string{"same", "--a", fixtureRep}); code != exitError {
		t.Fatalf("same without --b: exit %d, want %d", code, exitError)
	}
	// Ambiguous side (two eligible players, no --name) must error.
	if code := run([]string{"same", "--a", fixtureRep, "--b", fixtureRep2, "--name-b", "LC_Tyson"}); code != exitError {
		t.Fatalf("same with ambiguous side: exit %d, want %d", code, exitError)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read captured stderr: %v", err)
	}
	os.Stderr = old
	return buf.String()
}

func TestNoSyntheticWarningOnMatch(t *testing.T) {
	stderr := captureStderr(t, func() {
		run([]string{"match", fixtureRep, "--min-z", "1e18"})
	})
	if strings.Contains(stderr, "SYNTHETIC") {
		t.Fatal("model is real — synthetic warning should not appear")
	}
}

func TestNoSyntheticWarningOnSame(t *testing.T) {
	stderr := captureStderr(t, func() {
		run([]string{"same", "--a", fixtureRep, "--b", fixtureRep2, "--name-a", "Skins_", "--name-b", "LC_Tyson"})
	})
	if strings.Contains(stderr, "SYNTHETIC") {
		t.Fatal("model is real — synthetic warning should not appear")
	}
}

func TestStrictAcceptsRealModel(t *testing.T) {
	code := run([]string{"match", fixtureRep, "--strict", "--min-z", "1e18"})
	if code == exitError {
		t.Fatal("match --strict should not reject a real (non-synthetic) model")
	}
}

func TestRunEnroll(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "skins.fingerprint.json")

	// One game can't pass the self-consistency gate; must fail without --skip-gate.
	if code := run([]string{"enroll", "--label", "Skins_", fixtureRep, "-o", out}); code != exitError {
		t.Fatalf("enroll below gate: exit %d, want %d", code, exitError)
	}
	if code := run([]string{"enroll", "--label", "Skins_", fixtureRep, "-o", out, "--skip-gate"}); code != exitOK {
		t.Fatalf("enroll --skip-gate: exit %d, want %d", code, exitOK)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("fingerprint file not written: %v", err)
	}
	if code := run([]string{"enroll", fixtureRep}); code != exitError {
		t.Fatalf("enroll without --label: exit %d, want %d", code, exitError)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}
	os.Stdout = old
	return buf.String()
}

// stdout carries only JSON (with an explicit verdict), stderr the human report.
func TestMatchStreamsAreSeparated(t *testing.T) {
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() {
			run([]string{"match", fixtureRep2})
		})
	})

	var reports []playerReport
	if err := json.Unmarshal([]byte(stdout), &reports); err != nil {
		t.Fatalf("stdout is not a JSON array of reports: %v\n%s", err, stdout)
	}
	if len(reports) == 0 {
		t.Fatal("no reports on stdout")
	}
	for _, r := range reports {
		if _, ok := verdictRank[r.Verdict]; !ok {
			t.Fatalf("report for %q has invalid verdict %q", r.Player, r.Verdict)
		}
		if r.File == "" {
			t.Fatalf("report for %q is missing the file it came from", r.Player)
		}
	}
	if !strings.Contains(stderr, "MATCH") {
		t.Fatalf("stderr has no determination line:\n%s", stderr)
	}
	if strings.Contains(stderr, "\x1b[") {
		t.Fatalf("stderr has colour codes despite not being a terminal:\n%s", stderr)
	}
	if strings.Contains(stdout, "NO MATCH:") {
		t.Fatalf("human output leaked into stdout:\n%s", stdout)
	}
}

func TestMatchJSONLOneObjectPerLine(t *testing.T) {
	stdout := captureStdout(t, func() {
		run([]string{"match", fixtureRep2, "--jsonl", "-qq"})
	})
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected one line per player, got %d:\n%s", len(lines), stdout)
	}
	for _, line := range lines {
		var r playerReport
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("line is not a JSON object: %v\n%s", err, line)
		}
	}
}

func TestMatchQuietSilencesStderr(t *testing.T) {
	stderr := captureStderr(t, func() {
		_ = captureStdout(t, func() {
			run([]string{"match", fixtureRep2, "-qq"})
		})
	})
	if stderr != "" {
		t.Fatalf("-qq must leave stderr empty, got:\n%s", stderr)
	}
}

// The exit code carries the determination, not the --min-z filter: candidates
// that never rise above "weak" must not exit 0 at the default bar.
func TestMatchExitCodeFollowsVerdict(t *testing.T) {
	var reports []playerReport
	code := 0
	stdout := captureStdout(t, func() {
		code = run([]string{"match", fixtureRep2, "-qq"})
	})
	if err := json.Unmarshal([]byte(stdout), &reports); err != nil {
		t.Fatalf("stdout: %v", err)
	}
	best := verdictNone
	for _, r := range reports {
		if verdictRank[r.Verdict] > verdictRank[best] {
			best = r.Verdict
		}
	}
	wantOK := verdictRank[best] >= verdictRank[verdictLead]
	if wantOK != (code == exitOK) {
		t.Fatalf("best verdict %q but exit %d", best, code)
	}

	// A stricter bar can only lower the outcome, never raise it.
	strictCode := 0
	_ = captureStdout(t, func() {
		strictCode = run([]string{"match", fixtureRep2, "-qq", "--min-verdict", "strong"})
	})
	if code == exitNoMatch && strictCode == exitOK {
		t.Fatalf("strong bar exits 0 where lead bar exits 1")
	}
	if best != verdictStrong && strictCode != exitNoMatch {
		t.Fatalf("no strong verdict but --min-verdict strong exits %d", strictCode)
	}
}

func TestMatchMinVerdictValidation(t *testing.T) {
	if code := run([]string{"match", fixtureRep2, "--min-verdict", "bogus"}); code != exitError {
		t.Fatalf("bogus --min-verdict: exit %d, want %d", code, exitError)
	}
}

func TestSameStdoutCarriesVerdict(t *testing.T) {
	stdout := captureStdout(t, func() {
		run([]string{"same", "--a", fixtureRep, "--b", fixtureRep2, "--name-a", "Skins_", "--name-b", "LC_Tyson", "-qq"})
	})
	var r sameReport
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("stdout is not a JSON report: %v\n%s", err, stdout)
	}
	if _, ok := verdictRank[r.Tier]; !ok || r.Tier == verdictNone {
		t.Fatalf("invalid verdict %q", r.Tier)
	}
}

func TestDatasetList(t *testing.T) {
	stdout := captureStdout(t, func() {
		if code := run([]string{"dataset", "list"}); code != exitOK {
			t.Fatalf("dataset list: exit %d, want %d", code, exitOK)
		}
	})
	var entries []catalogEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("stdout is not a JSON array of catalog entries: %v", err)
	}
	if len(entries) < 60 {
		t.Fatalf("expected the full catalog, got %d entries", len(entries))
	}
	for _, e := range entries {
		if e.ID == "" || e.Label == "" || e.Games <= 0 {
			t.Fatalf("incomplete entry: %+v", e)
		}
	}
	if code := run([]string{"dataset", "list", "--min-confidence", "bogus"}); code != exitError {
		t.Fatalf("bogus --min-confidence: exit %d, want %d", code, exitError)
	}
}

func TestDatasetShow(t *testing.T) {
	stdout := captureStdout(t, func() {
		// Lookup is case-insensitive and accepts label or alias.
		if code := run([]string{"dataset", "show", "LARVA"}); code != exitOK {
			t.Fatalf("dataset show: exit %d, want %d", code, exitOK)
		}
	})
	var e catalogEntry
	if err := json.Unmarshal([]byte(stdout), &e); err != nil {
		t.Fatalf("stdout is not a JSON catalog entry: %v", err)
	}
	if e.ID != "larva" {
		t.Fatalf("resolved %q, want larva", e.ID)
	}
	if code := run([]string{"dataset", "show", "nobody"}); code != exitError {
		t.Fatalf("unknown player: exit %d, want %d", code, exitError)
	}
}

func TestDatasetFingerprint(t *testing.T) {
	stdout := captureStdout(t, func() {
		if code := run([]string{"dataset", "fingerprint", "Larva"}); code != exitOK {
			t.Fatalf("dataset fingerprint: exit %d, want %d", code, exitOK)
		}
	})
	fp, err := fingerprint.Parse(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("exported blob does not parse: %v", err)
	}
	if fp.N() <= 0 {
		t.Fatalf("parsed fingerprint has no games")
	}
	if code := run([]string{"dataset", "fingerprint", "nobody"}); code != exitError {
		t.Fatalf("unknown player: exit %d, want %d", code, exitError)
	}
}
