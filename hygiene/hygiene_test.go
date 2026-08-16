package hygiene

import (
	"math"
	"testing"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
	"github.com/marianogappa/scfingerprint/scoring"
)

// enroll builds a fingerprint from a synthetic player's games [from, to).
func enroll(t *testing.T, label string, player, from, to, d int) *fingerprint.Fingerprint {
	t.Helper()
	fp := fingerprint.New(fingerprint.Meta{Label: label})
	for g := from; g < to; g++ {
		if err := fp.Add(synthtest.GameVector(player, g, d), "Zerg"); err != nil {
			t.Fatal(err)
		}
	}
	return fp
}

func testScorer(t *testing.T) (*scoring.Scorer, int) {
	t.Helper()
	names, _ := features.FeatureNames(features.Version)
	d := len(names)
	return synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d)), d
}

func TestSelfConsistencyGate(t *testing.T) {
	scorer, d := testScorer(t)
	th := DefaultThresholds()

	genuine := enroll(t, "genuine", 100, 0, 40, d)
	score, err := SelfConsistencyGate(genuine, scorer, th)
	if err != nil {
		t.Fatalf("genuine enrollment rejected (score %.3f): %v", score, err)
	}

	merged := enroll(t, "merged", 100, 0, 20, d)
	for g := 0; g < 20; g++ {
		_ = merged.Add(synthtest.GameVector(101, g, d), "Zerg")
	}
	if _, err := SelfConsistencyGate(merged, scorer, th); err == nil {
		t.Fatal("two-person enrollment must fail the gate")
	}

	tiny := enroll(t, "tiny", 100, 0, 1, d)
	if _, err := SelfConsistencyGate(tiny, scorer, th); err == nil {
		t.Fatal("unauditable enrollment must fail the gate")
	}
}

func TestValidateMergeAcceptsSamePerson(t *testing.T) {
	scorer, d := testScorer(t)
	a := enroll(t, "acct1", 100, 0, 20, d)
	b := enroll(t, "acct2", 100, 20, 40, d)

	v, err := ValidateMerge(a, b, scorer, nil, DefaultThresholds())
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK {
		t.Fatalf("same-person merge rejected: %s (cross %.3f, merged SC %.3f)", v.Reason, v.CrossSimilarity, v.MergedSelfConsistency)
	}
}

func TestValidateMergeRejectsDifferentPeople(t *testing.T) {
	scorer, d := testScorer(t)
	a := enroll(t, "personA", 100, 0, 20, d)
	b := enroll(t, "personB", 101, 0, 20, d)

	v, err := ValidateMerge(a, b, scorer, nil, DefaultThresholds())
	if err != nil {
		t.Fatal(err)
	}
	if v.OK {
		t.Fatalf("different-person merge accepted (cross %.3f, merged SC %.3f)", v.CrossSimilarity, v.MergedSelfConsistency)
	}
}

func TestValidateMergeCoOccurrenceDisproof(t *testing.T) {
	scorer, d := testScorer(t)
	// Same synthetic person (so similarity gates would pass), but the labels
	// appear in one replay together — disproof wins.
	a := enroll(t, "acct1", 100, 0, 20, d)
	b := enroll(t, "acct2", 100, 20, 40, d)
	co := BuildCoOccurrence(map[string][]string{
		"game1.rep": {"acct1", "acct2", "someone_else"},
	})

	v, err := ValidateMerge(a, b, scorer, co, DefaultThresholds())
	if err != nil {
		t.Fatal(err)
	}
	if v.OK || !v.CoOccurrenceDisproved {
		t.Fatalf("co-occurring labels must disprove the merge: %+v", v)
	}
}

func TestCoOccurrence(t *testing.T) {
	co := BuildCoOccurrence(map[string][]string{
		"a.rep": {"x", "y"},
		"b.rep": {"y", "z"},
	})
	if !co.Disproved("x", "y") || !co.Disproved("y", "x") {
		t.Fatal("co-occurring pair must be disproved, symmetrically")
	}
	if co.Disproved("x", "z") {
		t.Fatal("non-co-occurring pair must not be disproved")
	}
	if co.Disproved("", "y") {
		t.Fatal("empty names must never disprove")
	}
	got := co.DisprovedPairs([][2]string{{"x", "y"}, {"x", "z"}})
	if len(got) != 1 || got[0] != [2]string{"x", "y"} {
		t.Fatalf("DisprovedPairs = %v", got)
	}
}

func TestManifestFromSamples(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	samples := synthtest.Corpus(0, 4, 2, len(names))
	manifest := ManifestFromSamples(samples)
	// All synthetic samples share one file name, so all players co-occur.
	if len(manifest["synthetic.rep"]) != 8 {
		t.Fatalf("manifest has %d names, want 8", len(manifest["synthetic.rep"]))
	}
}

func TestScanDuplicates(t *testing.T) {
	scorer, d := testScorer(t)
	fps := []*fingerprint.Fingerprint{
		enroll(t, "alias1", 100, 0, 20, d),
		enroll(t, "alias2", 100, 20, 40, d), // same person, different label
		enroll(t, "distinct", 101, 0, 20, d),
	}
	dups, err := ScanDuplicates(fps, scorer, DefaultThresholds())
	if err != nil {
		t.Fatal(err)
	}
	if len(dups) != 1 {
		t.Fatalf("want exactly the alias pair flagged, got %v", dups)
	}
	if dups[0].LabelA != "alias1" || dups[0].LabelB != "alias2" {
		t.Fatalf("wrong pair flagged: %+v", dups[0])
	}
}

func TestVerifyCatalog(t *testing.T) {
	scorer, d := testScorer(t)
	th := DefaultThresholds()

	clean := []*fingerprint.Fingerprint{
		enroll(t, "p100", 100, 0, 40, d),
		enroll(t, "p101", 101, 0, 40, d),
		enroll(t, "p102", 102, 0, 40, d),
	}
	findings, err := VerifyCatalog(clean, scorer, th)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("clean catalog must have no findings, got %v", findings)
	}

	badMerge := enroll(t, "poisoned", 103, 0, 20, d)
	for g := 0; g < 20; g++ {
		_ = badMerge.Add(synthtest.GameVector(104, g, d), "Zerg")
	}
	poisoned := append(clean,
		badMerge,
		enroll(t, "p100_alias", 100, 40, 60, d),
	)
	findings, err = VerifyCatalog(poisoned, scorer, th)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, f := range findings {
		kinds[f.Kind]++
	}
	if kinds["self_consistency"] != 1 {
		t.Fatalf("want 1 self_consistency finding, got %v", findings)
	}
	if kinds["duplicate"] != 1 {
		t.Fatalf("want 1 duplicate finding, got %v", findings)
	}
}

func TestMergeStateCorrectness(t *testing.T) {
	_, d := testScorer(t)
	a := enroll(t, "a", 100, 0, 12, d)
	b := enroll(t, "b", 100, 12, 30, d)

	merged, err := fingerprint.Merge(a, b, fingerprint.Meta{Label: "ab"})
	if err != nil {
		t.Fatal(err)
	}
	if merged.N() != 30 {
		t.Fatalf("merged N = %d, want 30", merged.N())
	}

	// Merged mean must equal the batch mean over all 30 games.
	batch := fingerprint.New(fingerprint.Meta{})
	for g := 0; g < 30; g++ {
		_ = batch.Add(synthtest.GameVector(100, g, d), "Zerg")
	}
	bm, mm := batch.Mean(), merged.Mean()
	for j := range bm {
		if math.Abs(bm[j]-mm[j]) > 1e-9 {
			t.Fatalf("dim %d: merged %v vs batch %v", j, mm[j], bm[j])
		}
	}
	if merged.RaceCounts()["z"] != 30 {
		t.Fatalf("merged race counts = %v", merged.RaceCounts())
	}

	// Round-trips like any fingerprint.
	s, err := merged.MarshalString()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fingerprint.Parse(s); err != nil {
		t.Fatal(err)
	}
}

func TestMergeVersionAndEmptyErrors(t *testing.T) {
	_, d := testScorer(t)
	a := enroll(t, "a", 100, 0, 5, d)
	empty := fingerprint.New(fingerprint.Meta{})
	if _, err := fingerprint.Merge(a, empty, fingerprint.Meta{}); err == nil {
		t.Fatal("merging with an empty fingerprint must error")
	}
}
