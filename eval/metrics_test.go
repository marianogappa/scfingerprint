package eval

import (
	"math"
	"testing"
)

func TestAUCPerfectSeparation(t *testing.T) {
	gen := []float64{2, 3, 4}
	imp := []float64{0, 1}
	if got := auc(gen, imp); got != 1.0 {
		t.Fatalf("AUC = %v, want 1.0", got)
	}
	// Reversed: all genuine below all impostors.
	if got := auc(imp, gen); got != 0.0 {
		t.Fatalf("AUC = %v, want 0.0", got)
	}
}

func TestAUCOverlap(t *testing.T) {
	// gen = {1, 3}, imp = {0, 2}: pairs (1>0)=1, (1<2)=0, (3>0)=1, (3>2)=1 → 3/4.
	gen := []float64{1, 3}
	imp := []float64{0, 2}
	if got := auc(gen, imp); math.Abs(got-0.75) > 1e-12 {
		t.Fatalf("AUC = %v, want 0.75", got)
	}
}

func TestAUCTies(t *testing.T) {
	// All equal: AUC must be 0.5.
	gen := []float64{1, 1}
	imp := []float64{1, 1}
	if got := auc(gen, imp); math.Abs(got-0.5) > 1e-12 {
		t.Fatalf("AUC = %v, want 0.5", got)
	}
}

func TestEERPerfectSeparation(t *testing.T) {
	gen := []float64{10, 11, 12}
	imp := []float64{0, 1, 2}
	if got := eer(gen, imp); got != 0.0 {
		t.Fatalf("EER = %v, want 0.0", got)
	}
}

func TestEERTotalOverlap(t *testing.T) {
	// Symmetric full overlap: EER should be ~0.5.
	gen := []float64{0, 1, 2, 3}
	imp := []float64{0, 1, 2, 3}
	got := eer(gen, imp)
	if math.Abs(got-0.5) > 0.15 {
		t.Fatalf("EER = %v, want ~0.5", got)
	}
}

func TestTPRAtFPR(t *testing.T) {
	// 100 impostors 0..99, genuine at various points.
	imp := make([]float64, 100)
	for i := range imp {
		imp[i] = float64(i)
	}
	gen := []float64{98.5, 99.5, 50}

	// fpr=0.01 allows 1 impostor >= t → t=99. Accepted: 99.5 → 1/3.
	if got := tprAtFPR(gen, imp, 0.01); math.Abs(got-1.0/3.0) > 1e-12 {
		t.Fatalf("TPR@1e-2 = %v, want 1/3", got)
	}
	// fpr=0.05 allows 5 → t=95. Accepted: 98.5, 99.5 → 2/3.
	if got := tprAtFPR(gen, imp, 0.05); math.Abs(got-2.0/3.0) > 1e-12 {
		t.Fatalf("TPR@5e-2 = %v, want 2/3", got)
	}
	// fpr=0.0001 allows 0 → t above all impostors... t=imp[99]=99 leaves 1
	// impostor >= t (fpr 0.01 > 0.0001), so threshold steps above the max →
	// no genuine below +inf... implementation returns 0 acceptance except
	// scores above the max impostor. 99.5 > 99 → but t must exceed 99;
	// stepping finds no next value → TPR 0.
	if got := tprAtFPR(gen, imp, 0.0001); got != 0 {
		t.Fatalf("TPR@1e-4 = %v, want 0", got)
	}
}

func TestExclusionSet(t *testing.T) {
	set := exclusionSet([][2]string{{"a", "b"}})
	if !set[[2]string{"a", "b"}] || !set[[2]string{"b", "a"}] {
		t.Fatal("exclusion set must be symmetric")
	}
	if set[[2]string{"a", "c"}] {
		t.Fatal("unrelated pair must not be excluded")
	}
}
