package training

import "sort"

// FRatioSelect computes the F-ratio (between-class / within-class variance)
// for each feature and returns the indices of the top k features, sorted
// ascending. Input vectors must already be standardized.
func FRatioSelect(samples []Sample, k int) (indices []int) {
	if len(samples) == 0 || k <= 0 {
		return nil
	}
	d := len(samples[0].Vector)
	if k > d {
		k = d
	}

	// Group samples by player.
	groups := map[string][]int{}
	for i, s := range samples {
		groups[s.Player] = append(groups[s.Player], i)
	}

	n := float64(len(samples))
	fratios := make([]float64, d)
	for j := 0; j < d; j++ {
		globalMean := 0.0
		for _, s := range samples {
			globalMean += s.Vector[j]
		}
		globalMean /= n

		between, within := 0.0, 0.0
		for _, p := range sortedKeys(groups) {
			idxs := groups[p]
			nc := float64(len(idxs))
			classMean := 0.0
			for _, i := range idxs {
				classMean += samples[i].Vector[j]
			}
			classMean /= nc
			diff := classMean - globalMean
			between += nc * diff * diff
			for _, i := range idxs {
				d := samples[i].Vector[j] - classMean
				within += d * d
			}
		}
		if within < 1e-30 {
			within = 1e-30
		}
		fratios[j] = between / within
	}

	// Rank features by F-ratio descending, take top k.
	ranked := make([]int, d)
	for i := range ranked {
		ranked[i] = i
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		return fratios[ranked[a]] > fratios[ranked[b]]
	})
	indices = ranked[:k]
	sort.Ints(indices)
	return indices
}

// ApplySelection reduces each sample's vector to the selected feature indices.
func ApplySelection(samples []Sample, indices []int) {
	for i := range samples {
		orig := samples[i].Vector
		sel := make([]float64, len(indices))
		for j, idx := range indices {
			sel[j] = orig[idx]
		}
		samples[i].Vector = sel
	}
}
