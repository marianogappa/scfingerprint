package training

import (
	"fmt"
	"math"
)

// CholeskyDecompose computes L such that A = L*Lᵀ for a symmetric
// positive-definite matrix A of size n×n (flat row-major).
// Returns L as a lower-triangular n×n flat row-major slice.
func CholeskyDecompose(A []float64, n int) ([]float64, error) {
	L := make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j <= i; j++ {
			sum := 0.0
			for k := 0; k < j; k++ {
				sum += L[i*n+k] * L[j*n+k]
			}
			if i == j {
				val := A[i*n+i] - sum
				if val <= 0 {
					return nil, fmt.Errorf("linalg: matrix is not positive definite (diag[%d] = %v)", i, val)
				}
				L[i*n+j] = math.Sqrt(val)
			} else {
				L[i*n+j] = (A[i*n+j] - sum) / L[j*n+j]
			}
		}
	}
	return L, nil
}

// InvertLowerTriangular computes L⁻¹ for a lower-triangular matrix L
// of size n×n (flat row-major). Returns the inverse (also lower-triangular).
func InvertLowerTriangular(L []float64, n int) []float64 {
	inv := make([]float64, n*n)
	for i := 0; i < n; i++ {
		inv[i*n+i] = 1.0 / L[i*n+i]
		for j := 0; j < i; j++ {
			sum := 0.0
			for k := j; k < i; k++ {
				sum += L[i*n+k] * inv[k*n+j]
			}
			inv[i*n+j] = -sum / L[i*n+i]
		}
	}
	return inv
}

// MatVecMul computes y = M*x where M is m×n (flat row-major).
func MatVecMul(M []float64, x []float64, m, n int) []float64 {
	y := make([]float64, m)
	for i := 0; i < m; i++ {
		sum := 0.0
		off := i * n
		for j := 0; j < n; j++ {
			sum += M[off+j] * x[j]
		}
		y[i] = sum
	}
	return y
}

// CosineSimilarity computes the cosine of the angle between a and b.
func CosineSimilarity(a, b []float64) float64 {
	dot, na, nb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// Dot computes the dot product of two vectors.
func Dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// VecNorm returns the L2 norm of a vector.
func VecNorm(a []float64) float64 {
	return math.Sqrt(Dot(a, a))
}
