package scfingerprint

import (
	"math"
	"testing"

	"github.com/marianogappa/scfingerprint/internal/features"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
)

func TestSearchFPR(t *testing.T) {
	all := map[string]bool{"fpr_1e2": true, "fpr_1e3": true, "fpr_1e4": true}

	// N=1: family-wise equals per-comparison, and the strictest cleared point
	// wins, so clearing everything reports 1e-4.
	if got := searchFPR(all, 1); math.Abs(got-0.0001) > 1e-9 {
		t.Errorf("N=1 all cleared: got %g, want 1e-4", got)
	}

	// N=68 (shipped catalog): clearing 1e-4 gives 1-(1-1e-4)^68 ≈ 0.678%.
	// This is the case the old boolean implementation reported as "not
	// cleared", because 1-(1-α)^N > α holds for every N>1 — so every result
	// against a real catalog looked like a weak signal.
	got68 := searchFPR(all, 68)
	if math.Abs(got68-0.006777) > 1e-5 {
		t.Errorf("N=68 all cleared: got %g, want ~0.006777", got68)
	}
	if got68 >= 1 {
		t.Error("N=68: a result clearing every operating point must not report FPR 1.0")
	}

	// Clearing only the loosest point is much weaker at catalog scale.
	loose := searchFPR(map[string]bool{"fpr_1e2": true}, 68)
	if math.Abs(loose-0.495110) > 1e-5 {
		t.Errorf("N=68 only 1e-2: got %g, want ~0.495110", loose)
	}
	if loose <= got68 {
		t.Error("clearing a stricter point must report a smaller family-wise FPR")
	}

	// Clearing nothing has no confidence to state.
	none := searchFPR(map[string]bool{"fpr_1e2": false, "fpr_1e3": false}, 68)
	if none != 1.0 {
		t.Errorf("nothing cleared: got %g, want 1.0", none)
	}

	// Family-wise rate grows with catalog size for a fixed hit.
	if searchFPR(all, 229) <= searchFPR(all, 68) {
		t.Error("a larger catalog must report a larger family-wise FPR")
	}

	// Unknown point names are ignored rather than trusted.
	if got := searchFPR(map[string]bool{"fpr_made_up": true}, 68); got != 1.0 {
		t.Errorf("unknown operating point: got %g, want 1.0", got)
	}
}

func TestMatchResultHasSearchFPR(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))

	db, err := newDatasetWithScorer(scorer)
	if err != nil {
		t.Fatal(err)
	}
	for p := 0; p < 10; p++ {
		fp := NewFingerprint(Meta{Label: synthtest.GameID(p, 0)})
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
	if top.SearchFPR <= 0 || top.SearchFPR > 1 {
		t.Fatalf("SearchFPR = %g, want a rate in (0,1]", top.SearchFPR)
	}
	// The self-match should clear at least one operating point, so it must
	// report a real rate rather than the "clears nothing" sentinel.
	if top.SearchFPR == 1.0 {
		t.Error("self-match reported no confidence at all")
	}
}
