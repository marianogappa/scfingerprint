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
	if got := auc(imp, gen); got != 0.0 {
		t.Fatalf("AUC = %v, want 0.0", got)
	}
}

func TestAUCOverlap(t *testing.T) {
	gen := []float64{1, 3}
	imp := []float64{0, 2}
	if got := auc(gen, imp); math.Abs(got-0.75) > 1e-12 {
		t.Fatalf("AUC = %v, want 0.75", got)
	}
}

func TestAUCTies(t *testing.T) {
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
	gen := []float64{0, 1, 2, 3}
	imp := []float64{0, 1, 2, 3}
	got := eer(gen, imp)
	if math.Abs(got-0.5) > 0.15 {
		t.Fatalf("EER = %v, want ~0.5", got)
	}
}

func TestTPRAtFPR(t *testing.T) {
	imp := make([]float64, 100)
	for i := range imp {
		imp[i] = float64(i)
	}
	gen := []float64{98.5, 99.5, 50}

	got := tprAtFPR(gen, imp, 0.01)
	if got == nil {
		t.Fatal("TPR@1e-2 should be measurable with 100 impostors")
	}
	if math.Abs(*got-1.0/3.0) > 1e-12 {
		t.Fatalf("TPR@1e-2 = %v, want 1/3", *got)
	}

	got5 := tprAtFPR(gen, imp, 0.05)
	if got5 == nil {
		t.Fatal("TPR@5e-2 should be measurable")
	}
	if math.Abs(*got5-2.0/3.0) > 1e-12 {
		t.Fatalf("TPR@5e-2 = %v, want 2/3", *got5)
	}

	// 100 impostors cannot express FPR=0.0001 (needs 10,000).
	got4 := tprAtFPR(gen, imp, 0.0001)
	if got4 != nil {
		t.Fatalf("TPR@1e-4 should be nil (unmeasurable) with 100 impostors, got %v", *got4)
	}
}

func TestTPRAtFPRUnmeasurableSmallPool(t *testing.T) {
	gen := []float64{1, 2, 3}
	imp := []float64{0, 0.5}

	// 2 impostors cannot express FPR=0.001 (needs 1,000).
	if got := tprAtFPR(gen, imp, 0.001); got != nil {
		t.Fatalf("expected nil for unmeasurable FPR with 2 impostors, got %v", *got)
	}
	// But FPR=0.5 is expressible.
	if got := tprAtFPR(gen, imp, 0.5); got == nil {
		t.Fatal("FPR=0.5 should be measurable with 2 impostors")
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
