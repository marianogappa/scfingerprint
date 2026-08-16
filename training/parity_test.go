package training

import (
	"math"
	"testing"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/scoring"
)

// TestScoringParity asserts that the scoring package reproduces the exact
// z/t-norm and transform math used at calibration time. If these diverge,
// the artifact's calibration tables and thresholds no longer apply to the
// scores the scorer produces.
func TestScoringParity(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	samples := syntheticDataset(10, 20, d)
	cfg := DefaultConfig()
	cfg.K = 20
	cfg.CohortSize = 5
	cfg.MinGamesPerPlayer = 5
	cfg.GitSHA = "test-sha"

	art, err := Fit(samples, cfg)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	scorer, err := scoring.New(art)
	if err != nil {
		t.Fatalf("scoring.New: %v", err)
	}

	// Transform parity: scoring.Transform must equal the training-side
	// Apply chain (standardize -> select -> whiten) on raw vectors.
	raw := syntheticDataset(2, 6, d)
	viaTraining := deepCopySamples(raw)
	ApplyStandardizer(viaTraining, art.Means, art.Stds)
	ApplySelection(viaTraining, art.SelectedIndices)
	ApplyWhitening(viaTraining, art.WhiteningMatrix, art.K)

	for i, s := range raw {
		w, err := scorer.Transform(s.Vector)
		if err != nil {
			t.Fatalf("Transform: %v", err)
		}
		for j := range w {
			if math.Abs(w[j]-viaTraining[i].Vector[j]) > 1e-9 {
				t.Fatalf("transform mismatch sample %d dim %d: scoring %v vs training %v", i, j, w[j], viaTraining[i].Vector[j])
			}
		}
	}

	// zt-norm parity: recover the pre-calibration zt score from Score.Z
	// (Z = (zt - impostorMean_1) / impostorStd_1 for single-game probes)
	// and compare against the training-side ztNorm.
	n1 := art.CalibrationTables["1"]
	for i := 0; i < len(viaTraining)-1; i++ {
		probe := viaTraining[i].Vector
		fp := viaTraining[i+1].Vector
		sc, err := scorer.Score([][]float64{probe}, fp)
		if err != nil {
			t.Fatalf("Score: %v", err)
		}
		ztFromScore := sc.Z*n1.ImpostorStd + n1.ImpostorMean
		ztTraining := ztNorm(CosineSimilarity(probe, fp), probe, art.CohortNorm, art.K)
		if math.Abs(ztFromScore-ztTraining) > 1e-9 {
			t.Fatalf("zt mismatch pair %d: scoring %v vs training %v", i, ztFromScore, ztTraining)
		}
		if math.Abs(sc.Cosine-CosineSimilarity(probe, fp)) > 1e-12 {
			t.Fatalf("cosine mismatch pair %d", i)
		}
	}
}
