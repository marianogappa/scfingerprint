package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureRep = "../../features/testdata/01_zvt_zergling_rush.rep"
const fixtureRep2 = "../../features/testdata/03_zvp_progamer_soma.rep"

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

func TestSyntheticWarningOnMatch(t *testing.T) {
	stderr := captureStderr(t, func() {
		run([]string{"match", fixtureRep, "--min-z", "1e18"})
	})
	if !strings.Contains(stderr, "SYNTHETIC") {
		t.Fatalf("expected synthetic warning on stderr, got: %s", stderr)
	}
}

func TestSyntheticWarningOnSame(t *testing.T) {
	stderr := captureStderr(t, func() {
		run([]string{"same", "--a", fixtureRep, "--b", fixtureRep2, "--name-a", "Skins_", "--name-b", "LC_Tyson"})
	})
	if !strings.Contains(stderr, "SYNTHETIC") {
		t.Fatalf("expected synthetic warning on stderr, got: %s", stderr)
	}
}

func TestStrictRejectsOnSyntheticModel(t *testing.T) {
	code := run([]string{"match", fixtureRep, "--strict"})
	if code != exitError {
		t.Fatalf("match --strict with synthetic model: exit %d, want %d", code, exitError)
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
