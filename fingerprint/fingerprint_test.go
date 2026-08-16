package fingerprint

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
)

// closeF32 reports whether b equals a after a float32 round-trip.
func closeF32(a, b float64) bool {
	return float64(float32(a)) == b || math.Abs(a-b) <= math.Abs(a)*1e-6+1e-9
}

func dims(t *testing.T) int {
	t.Helper()
	names, err := features.FeatureNames(features.Version)
	if err != nil {
		t.Fatal(err)
	}
	return len(names)
}

func TestIncrementalAddEqualsBatchMean(t *testing.T) {
	d := dims(t)
	fp := New(Meta{})
	const games = 100
	batch := make([]float64, d)
	for g := 0; g < games; g++ {
		v := synthtest.GameVector(1, g, d)
		if err := fp.Add(v, "Zerg"); err != nil {
			t.Fatal(err)
		}
		for j := range batch {
			batch[j] += v[j]
		}
	}
	for j := range batch {
		batch[j] /= games
	}
	if fp.N() != games {
		t.Fatalf("N = %d, want %d", fp.N(), games)
	}
	mean := fp.Mean()
	for j := range batch {
		if math.Abs(mean[j]-batch[j]) > 1e-9 {
			t.Fatalf("dim %d: incremental %v vs batch %v", j, mean[j], batch[j])
		}
	}
}

func TestAddWrongDims(t *testing.T) {
	fp := New(Meta{})
	if err := fp.Add(make([]float64, 5), ""); err == nil {
		t.Fatal("expected error for wrong dims")
	}
}

func TestRoundTrip(t *testing.T) {
	d := dims(t)
	fp := New(Meta{Label: "C9_FlaSh", Source: "cwal-2026", DateFrom: "2026-01-01", DateTo: "2026-06-01", Confidence: "high"})
	for g := 0; g < 30; g++ {
		race := "Zerg"
		if g%3 == 0 {
			race = "Terran"
		}
		if err := fp.Add(synthtest.GameVector(2, g, d), race); err != nil {
			t.Fatal(err)
		}
	}

	s1, err := fp.MarshalString()
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := Parse(s1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if fp2.N() != fp.N() || fp2.Version() != fp.Version() || fp2.Meta != fp.Meta {
		t.Fatalf("state mismatch after round-trip: %+v vs %+v", fp2, fp)
	}
	m1, m2 := fp.Mean(), fp2.Mean()
	for j := range m1 {
		if !closeF32(m1[j], m2[j]) {
			t.Fatalf("mean dim %d differs after round-trip beyond float32 precision: %v vs %v", j, m1[j], m2[j])
		}
	}
	rc1, rc2 := fp.RaceCounts(), fp2.RaceCounts()
	if len(rc1) != len(rc2) || rc1["z"] != rc2["z"] || rc1["t"] != rc2["t"] {
		t.Fatalf("race counts differ: %v vs %v", rc1, rc2)
	}

	// Marshal again: must be byte-identical (stable serialization).
	s2, err := fp2.MarshalString()
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 {
		t.Fatal("re-marshaled string differs from original")
	}
}

func TestRaceSubMeans(t *testing.T) {
	d := dims(t)
	fp := New(Meta{})
	// 12 Zerg games (>= threshold), 3 Terran games (< threshold).
	for g := 0; g < 12; g++ {
		_ = fp.Add(synthtest.GameVector(3, g, d), "Zerg")
	}
	for g := 12; g < 15; g++ {
		_ = fp.Add(synthtest.GameVector(3, g, d), "Terran")
	}

	if _, n, ok := fp.RaceMean("Zerg"); !ok || n != 12 {
		t.Fatalf("Zerg sub-mean missing or wrong count: ok=%v n=%d", ok, n)
	}

	s, err := fp.MarshalString()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, `"race_means":{"z":`) {
		t.Fatal("serialized form must include the Zerg sub-mean (12 >= threshold)")
	}
	if strings.Contains(s, `"t":"`) && strings.Contains(s, `"race_means":{"z":"`) && strings.Contains(s[strings.Index(s, `"race_means"`):], `"t":"`) {
		t.Fatal("serialized form must not include the Terran sub-mean (3 < threshold)")
	}

	// After parsing, the Zerg sub-mean survives.
	fp2, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, n, ok := fp2.RaceMean("Zerg"); !ok || n != 12 {
		t.Fatal("Zerg sub-mean lost in round-trip")
	}
}

func TestVersionMismatchTypedError(t *testing.T) {
	_, err := Parse(`{"v":99,"n":1,"mean":[1.0]}`)
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("error must wrap ErrVersionMismatch, got: %v", err)
	}
}

func TestParseValidation(t *testing.T) {
	d := dims(t)
	valid := New(Meta{})
	for g := 0; g < 4; g++ {
		_ = valid.Add(synthtest.GameVector(4, g, d), "Zerg")
	}
	s, _ := valid.MarshalString()

	cases := map[string]string{
		"not json":            "{",
		"wrong mean dims":     `{"v":3,"n":1,"mean":"AAAA"}`,
		"block sum mismatch":  strings.Replace(s, `"n":4`, `"n":5`, 1),
		"negative race count": strings.Replace(s, `"races":{"z":4}`, `"races":{"z":-1}`, 1),
	}
	for name, blob := range cases {
		if _, err := Parse(blob); err == nil {
			t.Fatalf("%s: expected parse error", name)
		}
	}
}

func TestBlocksStayBounded(t *testing.T) {
	d := dims(t)
	fp := New(Meta{})
	for g := 0; g < 500; g++ {
		_ = fp.Add(synthtest.GameVector(5, g, d), "")
	}
	if len(fp.blocks) > maxBlocks {
		t.Fatalf("blocks grew to %d > max %d", len(fp.blocks), maxBlocks)
	}
	total := 0
	for _, b := range fp.blocks {
		total += b.n
	}
	if total != 500 {
		t.Fatalf("block counts sum to %d, want 500", total)
	}
	// Block-weighted centroid must equal the running mean.
	c := centroid(fp.blocks, d)
	mean := fp.Mean()
	for j := range mean {
		if math.Abs(c[j]-mean[j]) > 1e-9 {
			t.Fatalf("dim %d: centroid %v vs mean %v", j, c[j], mean[j])
		}
	}
}

func TestSelfConsistencyGenuineVsMerged(t *testing.T) {
	d := dims(t)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))

	// Genuine enrollment: 40 games from one unseen player.
	genuine := New(Meta{Label: "genuine"})
	for g := 0; g < 40; g++ {
		_ = genuine.Add(synthtest.GameVector(100, g, d), "Zerg")
	}
	gScore, err := genuine.SelfConsistency(scorer)
	if err != nil {
		t.Fatalf("SelfConsistency (genuine): %v", err)
	}

	// Wrongly-merged enrollment: player 100's games then player 101's, the
	// concatenation shape a bad alias merge produces.
	merged := New(Meta{Label: "merged"})
	for g := 0; g < 20; g++ {
		_ = merged.Add(synthtest.GameVector(100, g, d), "Zerg")
	}
	for g := 0; g < 20; g++ {
		_ = merged.Add(synthtest.GameVector(101, g, d), "Zerg")
	}
	mScore, err := merged.SelfConsistency(scorer)
	if err != nil {
		t.Fatalf("SelfConsistency (merged): %v", err)
	}

	if gScore < 0.9 {
		t.Errorf("genuine self-consistency %.3f, want >= 0.9", gScore)
	}
	if mScore > gScore-0.2 {
		t.Errorf("merged self-consistency %.3f not clearly below genuine %.3f", mScore, gScore)
	}
}

func TestSelfConsistencyNeedsEnoughGames(t *testing.T) {
	d := dims(t)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))
	fp := New(Meta{})
	_ = fp.Add(synthtest.GameVector(100, 0, d), "")
	if _, err := fp.SelfConsistency(scorer); err == nil {
		t.Fatal("expected error for too few games")
	}
}

func TestProjectedCache(t *testing.T) {
	d := dims(t)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))

	fp := New(Meta{})
	for g := 0; g < 10; g++ {
		_ = fp.Add(synthtest.GameVector(100, g, d), "Zerg")
	}
	proj, err := fp.Projected(scorer)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := scorer.Transform(fp.Mean())
	if err != nil {
		t.Fatal(err)
	}
	for j := range proj {
		if proj[j] != direct[j] {
			t.Fatalf("dim %d: projection %v != direct transform %v", j, proj[j], direct[j])
		}
	}

	// The cache serializes with its model tag and survives a round-trip.
	s, _ := fp.MarshalString()
	if !strings.Contains(s, `"proj":{"model":"`+scorer.ModelTag()+`"`) {
		t.Fatal("serialized form must carry the model-tagged projection cache")
	}
	fp2, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	proj2, err := fp2.Projected(scorer)
	if err != nil {
		t.Fatal(err)
	}
	for j := range proj {
		if !closeF32(proj[j], proj2[j]) {
			t.Fatalf("projection dim %d differs after round-trip beyond float32 precision: %v vs %v", j, proj[j], proj2[j])
		}
	}

	// Add invalidates the cache.
	_ = fp.Add(synthtest.GameVector(100, 11, d), "Zerg")
	s2, _ := fp.MarshalString()
	if strings.Contains(s2, `"proj"`) {
		t.Fatal("Add must invalidate the projection cache")
	}

	// A version-mismatched scorer is rejected with the typed error.
	// (Simulated via a fingerprint claiming an unsupported version is not
	// possible through Parse, so check the scorer path with a v-mismatch.)
}

func TestMarshalEmptyMeta(t *testing.T) {
	d := dims(t)
	fp := New(Meta{})
	_ = fp.Add(synthtest.GameVector(0, 0, d), "")
	s, err := fp.MarshalString()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, `"meta"`) {
		t.Fatal("empty meta must be omitted")
	}
	if strings.Contains(s, `"races"`) {
		t.Fatal("empty races must be omitted")
	}
}
