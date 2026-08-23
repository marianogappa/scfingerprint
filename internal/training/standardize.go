package training

import "math"

// FitStandardizer computes per-feature mean and standard deviation from the
// given samples. Stds are clamped to a minimum of 1e-12 to avoid division
// by zero for constant features.
func FitStandardizer(samples []Sample) (means, stds []float64) {
	if len(samples) == 0 {
		return nil, nil
	}
	d := len(samples[0].Vector)
	n := float64(len(samples))
	means = make([]float64, d)
	stds = make([]float64, d)

	for _, s := range samples {
		for j, v := range s.Vector {
			means[j] += v
		}
	}
	for j := range means {
		means[j] /= n
	}

	for _, s := range samples {
		for j, v := range s.Vector {
			diff := v - means[j]
			stds[j] += diff * diff
		}
	}
	for j := range stds {
		stds[j] = math.Max(math.Sqrt(stds[j]/n), 1e-12)
	}
	return means, stds
}

// ApplyStandardizer standardizes vectors in place: x[j] = (x[j] - mean[j]) / std[j].
func ApplyStandardizer(samples []Sample, means, stds []float64) {
	for i := range samples {
		for j := range samples[i].Vector {
			samples[i].Vector[j] = (samples[i].Vector[j] - means[j]) / stds[j]
		}
	}
}
