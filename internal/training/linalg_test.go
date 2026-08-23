package training

import (
	"math"
	"testing"
)

func TestCholeskyDecompose(t *testing.T) {
	// 3×3 SPD matrix:
	//  4  12 -16
	// 12  37 -43
	//-16 -43  98
	// Expected L:
	//  2  0  0
	//  6  1  0
	// -8  5  3
	A := []float64{4, 12, -16, 12, 37, -43, -16, -43, 98}
	L, err := CholeskyDecompose(A, 3)
	if err != nil {
		t.Fatal(err)
	}
	expectedL := []float64{2, 0, 0, 6, 1, 0, -8, 5, 3}
	for i, v := range L {
		if math.Abs(v-expectedL[i]) > 1e-10 {
			t.Fatalf("L[%d] = %v, want %v", i, v, expectedL[i])
		}
	}

	// Verify L*Lᵀ = A.
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			sum := 0.0
			for k := 0; k < 3; k++ {
				sum += L[i*3+k] * L[j*3+k]
			}
			if math.Abs(sum-A[i*3+j]) > 1e-10 {
				t.Fatalf("L*Lᵀ[%d,%d] = %v, want %v", i, j, sum, A[i*3+j])
			}
		}
	}
}

func TestCholeskyNotPD(t *testing.T) {
	// Not positive definite.
	A := []float64{-1, 0, 0, 1}
	_, err := CholeskyDecompose(A, 2)
	if err == nil {
		t.Fatal("expected error for non-PD matrix")
	}
}

func TestInvertLowerTriangular(t *testing.T) {
	L := []float64{2, 0, 0, 6, 1, 0, -8, 5, 3}
	inv := InvertLowerTriangular(L, 3)

	// Verify inv * L = I.
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			sum := 0.0
			for k := 0; k < 3; k++ {
				sum += inv[i*3+k] * L[k*3+j]
			}
			expected := 0.0
			if i == j {
				expected = 1.0
			}
			if math.Abs(sum-expected) > 1e-10 {
				t.Fatalf("(L⁻¹*L)[%d,%d] = %v, want %v", i, j, sum, expected)
			}
		}
	}
}

func TestMatVecMul(t *testing.T) {
	M := []float64{1, 2, 3, 4, 5, 6}
	x := []float64{1, 2, 3}
	y := MatVecMul(M, x, 2, 3)
	if y[0] != 14 || y[1] != 32 {
		t.Fatalf("MatVecMul: got %v, want [14 32]", y)
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float64{1, 0, 0}
	b := []float64{0, 1, 0}
	if math.Abs(CosineSimilarity(a, b)) > 1e-10 {
		t.Fatalf("orthogonal vectors should have cosine 0, got %v", CosineSimilarity(a, b))
	}
	c := CosineSimilarity(a, a)
	if math.Abs(c-1) > 1e-10 {
		t.Fatalf("identical vectors should have cosine 1, got %v", c)
	}
}
