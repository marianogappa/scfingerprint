package eval

import "sort"

// auc computes the verification ROC AUC via the rank-sum (Mann-Whitney U)
// statistic: the probability a random genuine score exceeds a random
// impostor score, with ties counted as half.
func auc(genuine, impostor []float64) float64 {
	imp := append([]float64(nil), impostor...)
	sort.Float64s(imp)
	n := float64(len(genuine)) * float64(len(imp))
	sum := 0.0
	for _, g := range genuine {
		lo := sort.SearchFloat64s(imp, g)                                   // impostors < g
		hi := sort.Search(len(imp), func(i int) bool { return imp[i] > g }) // impostors <= g
		sum += float64(lo) + float64(hi-lo)/2
	}
	return sum / n
}

// eer computes the equal error rate: the point where the false positive rate
// equals the false negative rate, swept over all candidate thresholds.
func eer(genuine, impostor []float64) float64 {
	gen := append([]float64(nil), genuine...)
	imp := append([]float64(nil), impostor...)
	sort.Float64s(gen)
	sort.Float64s(imp)
	ng, ni := float64(len(gen)), float64(len(imp))

	// Candidate thresholds: all scores. At threshold t (accept if score >= t):
	// FPR = frac(imp >= t), FNR = frac(gen < t).
	all := append(append([]float64(nil), gen...), imp...)
	sort.Float64s(all)

	bestDiff, bestEER := 2.0, 1.0
	for _, t := range all {
		fpr := float64(len(imp)-sort.SearchFloat64s(imp, t)) / ni
		fnr := float64(sort.SearchFloat64s(gen, t)) / ng
		diff := fpr - fnr
		if diff < 0 {
			diff = -diff
		}
		if diff < bestDiff {
			bestDiff = diff
			bestEER = (fpr + fnr) / 2
		}
	}
	return bestEER
}

// tprAtFPR computes the true positive rate at the threshold where the
// impostor distribution's false positive rate is at most fpr. Returns nil
// when the impostor pool is too small to express the requested FPR
// (fewer than 1/fpr impostors), since the metric is structurally unmeasurable.
func tprAtFPR(genuine, impostor []float64, fpr float64) *float64 {
	imp := append([]float64(nil), impostor...)
	sort.Float64s(imp)
	ni := len(imp)

	if float64(ni) < 1/fpr {
		return nil
	}

	// Highest threshold t such that frac(imp >= t) <= fpr: allow at most
	// floor(fpr*ni) impostors at or above t.
	allowed := int(fpr * float64(ni))
	idx := ni - allowed // t = imp[idx]: exactly `allowed` impostors >= t...
	if idx >= ni {
		idx = ni - 1
	}
	// imp[idx] would leave (ni-idx) impostors >= t only if values are unique;
	// with ties, step the threshold up until the constraint holds.
	t := imp[idx]
	for float64(ni-sort.SearchFloat64s(imp, t))/float64(ni) > fpr {
		// Find the next distinct impostor value above t; if none, use +inf
		// equivalent (no genuine can be accepted at a threshold above max).
		next := sort.Search(ni, func(i int) bool { return imp[i] > t })
		if next >= ni {
			v := 0.0
			return &v
		}
		t = imp[next]
	}

	accepted := 0
	for _, g := range genuine {
		if g >= t {
			accepted++
		}
	}
	v := float64(accepted) / float64(len(genuine))
	return &v
}
