package scoring

import (
	"math"
	"testing"
)

// TestFingerprintPathParity settles whether the two fingerprint construction
// paths agree (issue #45, hypothesis 3):
//
//   - eval path:    Fingerprint(Transform(x1), ..., Transform(xn))  — mean of
//     whitened per-game vectors
//   - dataset path: Transform(mean(x1..xn))                         — raw mean
//     projected once (what fingerprint.Projected does)
//
// Transform is affine (standardize is affine, selection and whitening are
// linear), so the mean must commute through it. If this test fails, every
// shipped enrollment is constructed differently from what the eval harness
// measures.
func TestFingerprintPathParity(t *testing.T) {
	s, err := NewFromEmbedded()
	if err != nil {
		t.Fatal(err)
	}
	vecs := fixtureVectors(t)
	if len(vecs) < 2 {
		t.Fatal("need at least 2 fixture vectors")
	}

	var raws [][]float64
	for _, v := range vecs {
		raws = append(raws, v)
	}

	// Eval path: whiten each game, then average.
	var whitened [][]float64
	for _, r := range raws {
		w, err := s.Transform(r)
		if err != nil {
			t.Fatal(err)
		}
		whitened = append(whitened, w)
	}
	viaEval, err := s.Fingerprint(whitened...)
	if err != nil {
		t.Fatal(err)
	}

	// Dataset path: average raw vectors, then whiten once.
	d := len(raws[0])
	rawMean := make([]float64, d)
	for _, r := range raws {
		for j, v := range r {
			rawMean[j] += v
		}
	}
	for j := range rawMean {
		rawMean[j] /= float64(len(raws))
	}
	viaDataset, err := s.Transform(rawMean)
	if err != nil {
		t.Fatal(err)
	}

	if len(viaEval) != len(viaDataset) {
		t.Fatalf("dim mismatch: eval %d, dataset %d", len(viaEval), len(viaDataset))
	}
	for j := range viaEval {
		if diff := math.Abs(viaEval[j] - viaDataset[j]); diff > 1e-9 {
			t.Errorf("dim %d: eval-path %v vs dataset-path %v (diff %v)", j, viaEval[j], viaDataset[j], diff)
		}
	}
}
