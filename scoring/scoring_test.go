package scoring

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "regenerate golden files")

func TestNewFromEmbedded(t *testing.T) {
	s, err := NewFromEmbedded()
	if err != nil {
		t.Fatalf("NewFromEmbedded: %v", err)
	}
	if s.FeatureVersion() != 3 {
		t.Fatalf("feature version = %d, want 3", s.FeatureVersion())
	}
	if s.K() < 1 {
		t.Fatalf("k = %d, want >= 1", s.K())
	}
}

func TestTransformDimsError(t *testing.T) {
	s, err := NewFromEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Transform(make([]float64, 7)); err == nil {
		t.Fatal("expected error for wrong input dims")
	}
}

func TestScoreErrors(t *testing.T) {
	s, err := NewFromEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	fp := make([]float64, s.K())
	if _, err := s.Score(nil, fp); err == nil {
		t.Fatal("expected error for empty probe")
	}
	if _, err := s.Score([][]float64{make([]float64, s.K())}, make([]float64, 3)); err == nil {
		t.Fatal("expected error for wrong fingerprint dims")
	}
	if _, err := s.Fingerprint(); err == nil {
		t.Fatal("expected error for empty fingerprint input")
	}
}

func TestBucketFor(t *testing.T) {
	cases := map[int]string{
		1: "1", 2: "2", 3: "3", 4: "3", 5: "5", 6: "5", 7: "5", 8: "8+", 20: "8+",
	}
	for n, want := range cases {
		if got := bucketFor(n); got != want {
			t.Fatalf("bucketFor(%d) = %q, want %q", n, got, want)
		}
	}
}

// fixtureVectors loads the 4 known player vectors from the features package
// golden file.
func fixtureVectors(t *testing.T) map[string][]float64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "features", "testdata", "golden_v3.json"))
	if err != nil {
		t.Fatalf("reading features golden file: %v", err)
	}
	var golden map[string][]struct {
		Name   string    `json:"name"`
		Vector []float64 `json:"vector"`
	}
	if err := json.Unmarshal(data, &golden); err != nil {
		t.Fatal(err)
	}
	out := map[string][]float64{}
	for file, players := range golden {
		for _, p := range players {
			out[file+"/"+p.Name] = p.Vector
		}
	}
	return out
}

// TestEmbeddedArtifactReferenceScores loads the embedded artifact, transforms
// the fixture vectors, and reproduces reference scores within tolerance.
// This is the acceptance test from issue #5 living where the scoring math is.
func TestEmbeddedArtifactReferenceScores(t *testing.T) {
	s, err := NewFromEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	vecs := fixtureVectors(t)

	keys := make([]string, 0, len(vecs))
	for k := range vecs {
		keys = append(keys, k)
	}
	// Deterministic ordering.
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}

	type refScore struct {
		Probe       string  `json:"probe"`
		Fingerprint string  `json:"fingerprint"`
		Z           float64 `json:"z"`
		Cosine      float64 `json:"cosine"`
	}
	var got []refScore

	whitened := map[string][]float64{}
	for _, k := range keys {
		w, err := s.Transform(vecs[k])
		if err != nil {
			t.Fatalf("Transform(%s): %v", k, err)
		}
		for i, v := range w {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("%s: whitened[%d] = %v", k, i, v)
			}
		}
		whitened[k] = w
	}

	for _, pk := range keys {
		for _, fk := range keys {
			if pk == fk {
				continue
			}
			fp, _ := s.Fingerprint(whitened[fk])
			sc, err := s.Score([][]float64{whitened[pk]}, fp)
			if err != nil {
				t.Fatalf("Score(%s vs %s): %v", pk, fk, err)
			}
			if math.IsNaN(sc.Z) || math.IsInf(sc.Z, 0) {
				t.Fatalf("Z is %v for %s vs %s", sc.Z, pk, fk)
			}
			if sc.Cosine < -1.0000001 || sc.Cosine > 1.0000001 {
				t.Fatalf("cosine %v out of range for %s vs %s", sc.Cosine, pk, fk)
			}
			if sc.EvidenceN != 1 {
				t.Fatalf("EvidenceN = %d, want 1", sc.EvidenceN)
			}
			for _, op := range []string{"fpr_1e2", "fpr_1e3", "fpr_1e4"} {
				if _, ok := sc.OperatingPoints[op]; !ok {
					t.Fatalf("missing operating point %q", op)
				}
			}
			got = append(got, refScore{Probe: pk, Fingerprint: fk, Z: sc.Z, Cosine: sc.Cosine})
		}
	}

	goldenPath := filepath.Join("testdata", "reference_scores.json")
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		data, _ := json.MarshalIndent(got, "", " ")
		if err := os.WriteFile(goldenPath, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s", goldenPath)
		return
	}

	data, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading reference scores (run with -update to generate): %v", err)
	}
	var want []refScore
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d scores, golden has %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Probe != want[i].Probe || got[i].Fingerprint != want[i].Fingerprint {
			t.Fatalf("pair %d mismatch: got %s/%s want %s/%s", i, got[i].Probe, got[i].Fingerprint, want[i].Probe, want[i].Fingerprint)
		}
		if math.Abs(got[i].Z-want[i].Z) > 1e-9 {
			t.Errorf("%s vs %s: Z = %v, golden %v", got[i].Probe, got[i].Fingerprint, got[i].Z, want[i].Z)
		}
		if math.Abs(got[i].Cosine-want[i].Cosine) > 1e-9 {
			t.Errorf("%s vs %s: cosine = %v, golden %v", got[i].Probe, got[i].Fingerprint, got[i].Cosine, want[i].Cosine)
		}
	}
}

// TestMultiGameEvidence verifies n-aware behavior: multi-game probes pick the
// right bucket and produce finite calibrated scores.
func TestMultiGameEvidence(t *testing.T) {
	s, err := NewFromEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	vecs := fixtureVectors(t)
	var whitened [][]float64
	for _, v := range vecs {
		w, err := s.Transform(v)
		if err != nil {
			t.Fatal(err)
		}
		whitened = append(whitened, w)
	}
	fp, _ := s.Fingerprint(whitened[0])

	for _, n := range []int{1, 2, 3, 4} {
		sc, err := s.Score(whitened[:n], fp)
		if err != nil {
			t.Fatalf("Score with n=%d: %v", n, err)
		}
		if sc.EvidenceN != n {
			t.Fatalf("EvidenceN = %d, want %d", sc.EvidenceN, n)
		}
		if math.IsNaN(sc.Z) || math.IsInf(sc.Z, 0) {
			t.Fatalf("n=%d: Z is %v", n, sc.Z)
		}
	}
}
