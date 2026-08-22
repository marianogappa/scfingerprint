package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
)

// TestRegressionGates is the CI regression gate: the full pipeline
// (train → score → evaluate) on the fixed synthetic corpus must stay at or
// above these metric floors. A change to features, training, or scoring that
// degrades verification quality fails here before it ships.
func TestRegressionGates(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	trainCorpus := synthtest.Corpus(0, 30, 60, len(names))
	scorer := synthtest.Scorer(t, trainCorpus)
	evalCorpus := synthtest.Corpus(30, 30, 60, len(names))

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
		if m.TPRAtFPR1e3 == nil {
			t.Errorf("GATE: %s TPR@1e-3 unmeasurable (impostor pool too small)", scenario)
		} else if *m.TPRAtFPR1e3 < g.tprMin {
			t.Errorf("GATE: %s TPR@1e-3 %.4f < min %.4f", scenario, *m.TPRAtFPR1e3, g.tprMin)
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
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, len(names)))
	samples := synthtest.Corpus(30, 30, 60, len(names))

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
	samples := synthtest.Corpus(0, 6, 10, len(names))
	scorer := synthtest.Scorer(t, samples)

	opts := DefaultOptions()
	opts.EnrollFrac = 1.5
	if _, err := Evaluate(samples, scorer, opts); err == nil {
		t.Fatal("expected error for EnrollFrac out of range")
	}
}
