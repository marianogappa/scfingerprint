package training

import "fmt"

// FitWhitening computes the whitening matrix W (k×k, flat row-major) from
// the within-class covariance of the given samples. The covariance is
// regularized via shrinkage: S_reg = (1-α)*S_w + α*diag(S_w).
// Input vectors must already be standardized and feature-selected (dim k).
func FitWhitening(samples []Sample, shrinkage float64) (W []float64, err error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("training: no samples for whitening")
	}
	k := len(samples[0].Vector)

	groups := map[string][]int{}
	for i, s := range samples {
		groups[s.Player] = append(groups[s.Player], i)
	}
	numClasses := len(groups)
	n := len(samples)
	if n <= numClasses {
		return nil, fmt.Errorf("training: need more samples (%d) than classes (%d)", n, numClasses)
	}

	// Within-class scatter matrix.
	sw := make([]float64, k*k)
	diff := make([]float64, k)
	for _, p := range sortedKeys(groups) {
		idxs := groups[p]
		classMean := make([]float64, k)
		for _, i := range idxs {
			for j := 0; j < k; j++ {
				classMean[j] += samples[i].Vector[j]
			}
		}
		nc := float64(len(idxs))
		for j := range classMean {
			classMean[j] /= nc
		}
		for _, i := range idxs {
			for j := 0; j < k; j++ {
				diff[j] = samples[i].Vector[j] - classMean[j]
			}
			// Rank-1 update: sw += diff * diff^T
			for r := 0; r < k; r++ {
				for c := 0; c < k; c++ {
					sw[r*k+c] += diff[r] * diff[c]
				}
			}
		}
	}
	denom := float64(n - numClasses)
	for i := range sw {
		sw[i] /= denom
	}

	// Shrinkage: S_reg = (1-α)*S_w + α*diag(S_w)
	for r := 0; r < k; r++ {
		diagVal := sw[r*k+r]
		for c := 0; c < k; c++ {
			if r == c {
				sw[r*k+c] = (1-shrinkage)*sw[r*k+c] + shrinkage*diagVal
			} else {
				sw[r*k+c] = (1 - shrinkage) * sw[r*k+c]
			}
		}
	}

	L, err := CholeskyDecompose(sw, k)
	if err != nil {
		return nil, fmt.Errorf("training: whitening cholesky: %w", err)
	}
	W = InvertLowerTriangular(L, k)
	return W, nil
}

// ApplyWhitening transforms each sample's vector: x = W*x.
func ApplyWhitening(samples []Sample, W []float64, k int) {
	for i := range samples {
		samples[i].Vector = MatVecMul(W, samples[i].Vector, k, k)
	}
}
