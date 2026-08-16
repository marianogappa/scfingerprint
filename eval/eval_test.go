package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

var evalRaces = []string{"Zerg", "Terran", "Protoss"}

// mix64 is a splitmix64-style finalizer mapping a seed to [0, 1).
func mix64(z uint64) float64 {
	z += 0x9E3779B97F4A7C15
	z ^= z >> 30
	z *= 0xBF58476D1CE4E5B9
	z ^= z >> 27
	z *= 0x94D049BB133111EB
	z ^= z >> 31
	return float64(z>>11) / float64(uint64(1)<<53)
}

// syntheticCorpus creates a deterministic corpus with per-player signal and
// round-robin races. The player offset makes disjoint corpora: training and
// evaluation must not share identities, or the artifact's cohort contaminates
// t-norm for players who are their own cohort entry.
func syntheticCorpus(offset, numPlayers, gamesPerPlayer, d int) []training.Sample {
	var samples []training.Sample
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for pi := 0; pi < numPlayers; pi++ {
		p := offset + pi
		playerName := fmt.Sprintf("P%03d", p)
		race := evalRaces[p%len(evalRaces)]
		for g := 0; g < gamesPerPlayer; g++ {
			vec := make([]float64, d)
			for j := 0; j < d; j++ {
				// Per-game noise, keyed by (player, game, feature). mix64
				// decorrelates nearby seeds — a bare LCG step leaves features
				// within a game near-perfectly correlated, which makes the
				// within-class covariance singular and whitening degenerate.
				noise := mix64(uint64(p*1000000 + g*1000 + j))
				// Stable per-player profile, keyed by (player, feature) only:
				// each player gets a distinct pattern across features, so
				// fingerprints are separable directions in feature space.
				signal := mix64(uint64(p*991 + j))
				vec[j] = signal + noise*0.15
			}
			samples = append(samples, training.Sample{
				File:      "synthetic.rep",
				Player:    playerName,
				Race:      race,
				StartTime: baseTime.Add(time.Duration(g) * time.Hour),
				Vector:    vec,
			})
		}
	}
	return samples
}

func syntheticScorer(t *testing.T, samples []training.Sample) *scoring.Scorer {
	t.Helper()
	cfg := training.DefaultConfig()
	cfg.K = 60
	cfg.CohortSize = 10
	cfg.MinGamesPerPlayer = 5
	cfg.GitSHA = "test-sha"
	art, err := training.Fit(samples, cfg)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}
	scorer, err := scoring.New(art)
	if err != nil {
		t.Fatalf("scoring.New: %v", err)
	}
	return scorer
}

// TestRegressionGates is the CI regression gate: the full pipeline
// (train → score → evaluate) on the fixed synthetic corpus must stay at or
// above these metric floors. A change to features, training, or scoring that
// degrades verification quality fails here before it ships.
func TestRegressionGates(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	trainCorpus := syntheticCorpus(0, 30, 60, len(names))
	scorer := syntheticScorer(t, trainCorpus)
	evalCorpus := syntheticCorpus(30, 30, 60, len(names))

	report, err := Evaluate(evalCorpus, scorer, DefaultOptions())
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	if report.NumPlayers != 30 {
		t.Fatalf("players = %d, want 30", report.NumPlayers)
	}

	gates := map[string]struct {
		eerMax, tprMin, accMin float64
	}{
		// The synthetic corpus is highly separable; anything below perfect
		// or near-perfect indicates a pipeline regression.
		"n1_all":       {eerMax: 0.01, tprMin: 0.99, accMin: 0.99},
		"n1_same_race": {eerMax: 0.01, tprMin: 0.99, accMin: 0.99},
		"n3_all":       {eerMax: 0.01, tprMin: 0.99, accMin: 0.99},
		"n3_same_race": {eerMax: 0.01, tprMin: 0.99, accMin: 0.99},
	}
	for scenario, g := range gates {
		m, ok := report.Scenarios[scenario]
		if !ok {
			t.Fatalf("missing scenario %q", scenario)
		}
		if m.NumGenuine == 0 || m.NumImpostor == 0 {
			t.Fatalf("%s: empty pools (genuine=%d impostor=%d)", scenario, m.NumGenuine, m.NumImpostor)
		}
		if m.EER > g.eerMax {
			t.Errorf("GATE: %s EER %.5f > max %.5f", scenario, m.EER, g.eerMax)
		}
		if m.TPRAtFPR1e3 < g.tprMin {
			t.Errorf("GATE: %s TPR@1e-3 %.4f < min %.4f", scenario, m.TPRAtFPR1e3, g.tprMin)
		}
		if m.ClosedSetAccuracy < g.accMin {
			t.Errorf("GATE: %s accuracy %.4f < min %.4f", scenario, m.ClosedSetAccuracy, g.accMin)
		}
	}

	// Same-race impostor pools must be strictly smaller than all-impostor pools.
	if report.Scenarios["n1_same_race"].NumImpostor >= report.Scenarios["n1_all"].NumImpostor {
		t.Fatal("same-race impostor pool should be smaller than all-impostor pool")
	}
}

// TestExclusionsShrinkImpostorPool verifies the smurf manifest removes the
// excluded pair's trials from impostor pools.
func TestExclusionsShrinkImpostorPool(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	scorer := syntheticScorer(t, syntheticCorpus(0, 30, 60, len(names)))
	samples := syntheticCorpus(30, 30, 60, len(names))

	base, err := Evaluate(samples, scorer, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	opts := DefaultOptions()
	opts.Exclusions = [][2]string{{"P030", "P031"}}
	excluded, err := Evaluate(samples, scorer, opts)
	if err != nil {
		t.Fatal(err)
	}
	if excluded.Scenarios["n1_all"].NumImpostor >= base.Scenarios["n1_all"].NumImpostor {
		t.Fatalf("exclusions did not shrink impostor pool: %d >= %d",
			excluded.Scenarios["n1_all"].NumImpostor, base.Scenarios["n1_all"].NumImpostor)
	}
}

func TestLoadExclusions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "smurfs.json")
	if err := os.WriteFile(path, []byte(`[["MBU_Shine","wG_Shine"],["a","b"]]`), 0o644); err != nil {
		t.Fatal(err)
	}
	pairs, err := LoadExclusions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 2 || pairs[0] != [2]string{"MBU_Shine", "wG_Shine"} {
		t.Fatalf("unexpected pairs: %v", pairs)
	}

	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte(`[["only-one"]]`), 0o644)
	if _, err := LoadExclusions(bad); err == nil {
		t.Fatal("expected error for malformed pair")
	}
}

func TestEvaluateOptionValidation(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	samples := syntheticCorpus(0, 6, 10, len(names))
	scorer := syntheticScorer(t, samples)

	opts := DefaultOptions()
	opts.EnrollFrac = 1.5
	if _, err := Evaluate(samples, scorer, opts); err == nil {
		t.Fatal("expected error for EnrollFrac out of range")
	}
}
