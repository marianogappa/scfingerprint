package training

import (
	"math"
	"sort"

	"github.com/marianogappa/scfingerprint/model"
)

// BuildCohortNorm selects a cohort of players from the held-out data, computes
// their whitened mean vectors (fingerprints), and computes z-norm stats
// (impostor score mean/std per cohort fingerprint).
func BuildCohortNorm(heldOut []Sample, k, cohortSize int) model.CohortNormStats {
	fps := playerFingerprints(heldOut, k)

	// Select the cohortSize most-represented players (most games), breaking
	// ties alphabetically for determinism.
	type playerCount struct {
		name  string
		count int
	}
	var pcs []playerCount
	counts := map[string]int{}
	for _, s := range heldOut {
		counts[s.Player]++
	}
	for _, name := range sortedKeys(counts) {
		pcs = append(pcs, playerCount{name, counts[name]})
	}
	sort.SliceStable(pcs, func(i, j int) bool {
		if pcs[i].count != pcs[j].count {
			return pcs[i].count > pcs[j].count
		}
		return pcs[i].name < pcs[j].name
	})
	if cohortSize > len(pcs) {
		cohortSize = len(pcs)
	}
	cohortNames := make([]string, cohortSize)
	for i := 0; i < cohortSize; i++ {
		cohortNames[i] = pcs[i].name
	}
	sort.Strings(cohortNames)

	cohortVecs := make([]float64, 0, cohortSize*k)
	for _, name := range cohortNames {
		cohortVecs = append(cohortVecs, fps[name]...)
	}

	// z-norm stats: for each cohort fingerprint, compute impostor score
	// mean/std against all other cohort fingerprints.
	znormMeans := make([]float64, cohortSize)
	znormStds := make([]float64, cohortSize)
	for i := 0; i < cohortSize; i++ {
		vi := cohortVecs[i*k : (i+1)*k]
		var scores []float64
		for j := 0; j < cohortSize; j++ {
			if j == i {
				continue
			}
			vj := cohortVecs[j*k : (j+1)*k]
			scores = append(scores, CosineSimilarity(vi, vj))
		}
		znormMeans[i], znormStds[i] = meanStd(scores)
	}

	return model.CohortNormStats{
		CohortVectors: cohortVecs,
		NumCohort:     cohortSize,
		ZNormMeans:    znormMeans,
		ZNormStds:     znormStds,
	}
}

// BuildCalibrationTables produces per-evidence-count calibration entries.
// For each bucket n in {1,2,3,5,8}, it averages n held-out games per player
// into a probe, scores against all fingerprints, and records genuine vs
// impostor score distributions.
func BuildCalibrationTables(
	heldOut []Sample,
	cohort model.CohortNormStats,
	k int,
) map[string]model.CalibrationEntry {
	fps := playerFingerprints(heldOut, k)

	// Group held-out samples by player.
	byPlayer := map[string][]int{}
	for i, s := range heldOut {
		byPlayer[s.Player] = append(byPlayer[s.Player], i)
	}
	players := sortedKeys(byPlayer)

	buckets := []struct {
		key string
		n   int
	}{
		{"1", 1}, {"2", 2}, {"3", 3}, {"5", 5}, {"8+", 8},
	}

	tables := map[string]model.CalibrationEntry{}
	for _, bucket := range buckets {
		var genuineScores, impostorScores []float64
		for _, player := range players {
			idxs := byPlayer[player]
			fp, ok := fps[player]
			if !ok {
				continue
			}
			n := bucket.n
			if len(idxs) < n {
				continue
			}
			// Use consecutive non-overlapping windows of size n.
			for start := 0; start+n <= len(idxs); start += n {
				probe := averageVectors(heldOut, idxs[start:start+n], k)
				rawCos := CosineSimilarity(probe, fp)
				genuineScores = append(genuineScores, ztNorm(rawCos, probe, cohort, k))

				for _, other := range players {
					if other == player {
						continue
					}
					if ofp, ok := fps[other]; ok {
						rawImp := CosineSimilarity(probe, ofp)
						impostorScores = append(impostorScores, ztNorm(rawImp, probe, cohort, k))
					}
				}
			}
		}
		gm, gs := meanStd(genuineScores)
		im, is := meanStd(impostorScores)
		tables[bucket.key] = model.CalibrationEntry{
			GenuineMean:  gm,
			GenuineStd:   gs,
			ImpostorMean: im,
			ImpostorStd:  is,
		}
	}
	return tables
}

// ComputeThresholds finds the calibrated z-score thresholds at specific
// false positive rates from the full impostor score distribution.
func ComputeThresholds(heldOut []Sample, cohort model.CohortNormStats, k int) map[string]float64 {
	fps := playerFingerprints(heldOut, k)
	byPlayer := map[string][]int{}
	for i, s := range heldOut {
		byPlayer[s.Player] = append(byPlayer[s.Player], i)
	}
	players := sortedKeys(byPlayer)

	var impostorScores []float64
	for _, player := range players {
		idxs := byPlayer[player]
		for _, i := range idxs {
			probe := heldOut[i].Vector
			for _, other := range players {
				if other == player {
					continue
				}
				if ofp, ok := fps[other]; ok {
					raw := CosineSimilarity(probe, ofp)
					impostorScores = append(impostorScores, ztNorm(raw, probe, cohort, k))
				}
			}
		}
	}

	sort.Float64s(impostorScores)
	n := len(impostorScores)

	quantile := func(fpr float64) float64 {
		if n == 0 {
			return 100.0
		}
		idx := n - int(fpr*float64(n)) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= n {
			idx = n - 1
		}
		return impostorScores[idx]
	}

	return map[string]float64{
		"fpr_1e2": quantile(0.01),
		"fpr_1e3": quantile(0.001),
		"fpr_1e4": quantile(0.0001),
	}
}

// ztNorm applies z-norm and t-norm using the cohort and averages them.
func ztNorm(rawCosine float64, probe []float64, cohort model.CohortNormStats, k int) float64 {
	// z-norm: average across cohort fingerprints.
	zScores := make([]float64, cohort.NumCohort)
	for i := 0; i < cohort.NumCohort; i++ {
		zm, zs := cohort.ZNormMeans[i], cohort.ZNormStds[i]
		if zs < 1e-12 {
			zs = 1e-12
		}
		zScores[i] = (rawCosine - zm) / zs
	}
	zNormed := mean(zScores)

	// t-norm: score probe against all cohort fingerprints, normalize.
	tScores := make([]float64, cohort.NumCohort)
	for i := 0; i < cohort.NumCohort; i++ {
		cv := cohort.CohortVectors[i*k : (i+1)*k]
		tScores[i] = CosineSimilarity(probe, cv)
	}
	tMean, tStd := meanStd(tScores)
	if tStd < 1e-12 {
		tStd = 1e-12
	}
	tNormed := (rawCosine - tMean) / tStd

	return (zNormed + tNormed) / 2
}

func playerFingerprints(samples []Sample, k int) map[string][]float64 {
	byPlayer := map[string][][]float64{}
	for _, s := range samples {
		byPlayer[s.Player] = append(byPlayer[s.Player], s.Vector)
	}
	fps := map[string][]float64{}
	for _, player := range sortedKeys(byPlayer) {
		vecs := byPlayer[player]
		fp := make([]float64, k)
		for _, v := range vecs {
			for j := 0; j < k; j++ {
				fp[j] += v[j]
			}
		}
		n := float64(len(vecs))
		for j := range fp {
			fp[j] /= n
		}
		fps[player] = fp
	}
	return fps
}

func averageVectors(samples []Sample, idxs []int, k int) []float64 {
	avg := make([]float64, k)
	for _, i := range idxs {
		for j := 0; j < k; j++ {
			avg[j] += samples[i].Vector[j]
		}
	}
	n := float64(len(idxs))
	for j := range avg {
		avg[j] /= n
	}
	return avg
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func meanStd(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 1
	}
	m := mean(xs)
	v := 0.0
	for _, x := range xs {
		d := x - m
		v += d * d
	}
	v /= float64(len(xs))
	s := 0.0
	if v > 0 {
		s = math.Sqrt(v)
	}
	if s < 1e-12 {
		s = 1e-12
	}
	return m, s
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
