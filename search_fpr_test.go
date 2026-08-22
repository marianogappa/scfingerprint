package scfingerprint

import (
	"math"
	"testing"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
)

func TestSearchCorrectedOps(t *testing.T) {
	// N=1: search-level = per-comparison, so all cleared ops stay cleared.
	ops := map[string]bool{"fpr_1e2": true, "fpr_1e3": true, "fpr_1e4": true}
	corrected := searchCorrectedOps(ops, 1)
	for name, want := range ops {
		if corrected[name] != want {
			t.Errorf("N=1: %s = %v, want %v", name, corrected[name], want)
		}
	}

	// N=68 (shipped catalog): 1e-3 per-comparison → ~6.6% search-level,
	// which exceeds 0.1%, so fpr_1e3 should NOT clear at search level.
	corrected68 := searchCorrectedOps(ops, 68)
	if corrected68["fpr_1e3"] {
		searchFPR := 1 - math.Pow(1-0.001, 68)
		t.Errorf("N=68: fpr_1e3 should not clear at search level (search FPR=%.4f)", searchFPR)
	}
	// fpr_1e4 at N=68 → ~0.68% search-level, still exceeds 0.01%.
	if corrected68["fpr_1e4"] {
		t.Error("N=68: fpr_1e4 should not clear at search level")
	}

	// Uncleared ops stay uncleared.
	notCleared := map[string]bool{"fpr_1e2": false, "fpr_1e3": false}
	correctedNot := searchCorrectedOps(notCleared, 68)
	for name := range notCleared {
		if correctedNot[name] {
			t.Errorf("uncleared %s became cleared after correction", name)
		}
	}
}

func TestMatchResultHasSearchFPR(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))

	db, err := NewDataset(scorer)
	if err != nil {
		t.Fatal(err)
	}
	for p := 0; p < 10; p++ {
		fp := fingerprint.New(Meta{Label: synthtest.GameID(p, 0)})
		for g := 0; g < 30; g++ {
			_ = fp.Add(synthtest.GameVector(p, g, d), "")
		}
		if err := db.Add(fp); err != nil {
			t.Fatal(err)
		}
	}

	probe := []PlayerGame{{Vector: synthtest.GameVector(0, 50, d)}}
	results, err := MatchMany(probe, db, WithMinZ(math.Inf(-1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	top := results[0]
	if top.CatalogSize != 10 {
		t.Fatalf("CatalogSize = %d, want 10", top.CatalogSize)
	}
	if top.SearchFPR == nil {
		t.Fatal("SearchFPR is nil")
	}
	for _, name := range []string{"fpr_1e2", "fpr_1e3", "fpr_1e4"} {
		if _, ok := top.SearchFPR[name]; !ok {
			t.Errorf("SearchFPR missing key %q", name)
		}
	}
}
