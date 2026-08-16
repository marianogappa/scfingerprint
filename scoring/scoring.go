// Package scoring turns (feature vector, fingerprint) pairs into calibrated
// scores using a trained model artifact. The pipeline is: standardize →
// select → whiten (Transform), then cosine similarity plus z-norm and t-norm
// against the artifact's cohort, calibrated per evidence count.
//
// Scoring is a couple of matrix multiplies — pure Go, no dependencies beyond
// the model package that carries the trained constants.
package scoring

import (
	"fmt"
	"math"

	"github.com/marianogappa/scfingerprint/model"
)

// Score is the result of scoring a probe against a fingerprint. Callers get
// the calibrated z-score, evidence count, and which named operating points
// it clears — never a bare boolean match.
type Score struct {
	Z               float64         // calibrated score, comparable across evidence counts
	Cosine          float64         // raw cosine similarity, for debugging
	EvidenceN       int             // number of games the probe was averaged from
	OperatingPoints map[string]bool // e.g. "fpr_1e2", "fpr_1e3", "fpr_1e4" thresholds crossed
}

// Scorer applies a trained model artifact to feature vectors.
type Scorer struct {
	artifact *model.Artifact

	// Calibrated operating-point thresholds: the artifact's raw thresholds
	// (derived from the n=1 impostor distribution) expressed in the
	// n-invariant calibrated space.
	calibratedOps map[string]float64
}

// New builds a Scorer from a validated model artifact.
func New(a *model.Artifact) (*Scorer, error) {
	if a == nil {
		return nil, fmt.Errorf("scoring: artifact is nil")
	}
	if err := a.Validate(); err != nil {
		return nil, fmt.Errorf("scoring: %w", err)
	}
	n1, ok := a.CalibrationTables["1"]
	if !ok {
		return nil, fmt.Errorf("scoring: artifact missing n=1 calibration table")
	}
	calibratedOps := make(map[string]float64, len(a.OperatingPoints))
	for name, threshold := range a.OperatingPoints {
		calibratedOps[name] = (threshold - n1.ImpostorMean) / clampStd(n1.ImpostorStd)
	}
	return &Scorer{artifact: a, calibratedOps: calibratedOps}, nil
}

// NewFromEmbedded builds a Scorer from the compiled-in model artifact.
func NewFromEmbedded() (*Scorer, error) {
	a, err := model.LoadEmbedded()
	if err != nil {
		return nil, err
	}
	return New(a)
}

// FeatureVersion returns the feature schema version this scorer expects.
func (s *Scorer) FeatureVersion() int { return s.artifact.FeatureVersion }

// K returns the dimensionality of the whitened space.
func (s *Scorer) K() int { return s.artifact.K }

// Transform maps a raw feature vector (as produced by the features package,
// with the version reported by FeatureVersion) into the whitened K-dim space
// where cosine similarity is meaningful: standardize → select → whiten.
func (s *Scorer) Transform(raw []float64) ([]float64, error) {
	a := s.artifact
	if len(raw) != len(a.Means) {
		return nil, fmt.Errorf("scoring: vector has %d dims, artifact expects %d", len(raw), len(a.Means))
	}
	// Standardize + select in one pass: only selected features are needed.
	sel := make([]float64, a.K)
	for i, idx := range a.SelectedIndices {
		sel[i] = (raw[idx] - a.Means[idx]) / a.Stds[idx]
	}
	// Whiten: w = W * sel.
	w := make([]float64, a.K)
	for i := 0; i < a.K; i++ {
		sum := 0.0
		off := i * a.K
		for j := 0; j < a.K; j++ {
			sum += a.WhiteningMatrix[off+j] * sel[j]
		}
		w[i] = sum
	}
	return w, nil
}

// Fingerprint averages one or more whitened vectors into a fingerprint.
// This is the enrollment primitive: a player's fingerprint is the mean of
// their whitened per-game vectors.
func (s *Scorer) Fingerprint(whitened ...[]float64) ([]float64, error) {
	if len(whitened) == 0 {
		return nil, fmt.Errorf("scoring: no vectors to fingerprint")
	}
	k := s.artifact.K
	fp := make([]float64, k)
	for _, v := range whitened {
		if len(v) != k {
			return nil, fmt.Errorf("scoring: vector has %d dims, want k=%d", len(v), k)
		}
		for j := 0; j < k; j++ {
			fp[j] += v[j]
		}
	}
	n := float64(len(whitened))
	for j := range fp {
		fp[j] /= n
	}
	return fp, nil
}

// Score scores a probe built from n whitened per-game vectors against a
// fingerprint. The probe games are averaged, scored with cosine similarity,
// normalized with z-norm and t-norm against the artifact's cohort, and
// calibrated with the evidence-count bucket matching n.
func (s *Scorer) Score(probeGames [][]float64, fingerprint []float64) (Score, error) {
	a := s.artifact
	if len(probeGames) == 0 {
		return Score{}, fmt.Errorf("scoring: no probe games")
	}
	if len(fingerprint) != a.K {
		return Score{}, fmt.Errorf("scoring: fingerprint has %d dims, want k=%d", len(fingerprint), a.K)
	}
	probe, err := s.Fingerprint(probeGames...)
	if err != nil {
		return Score{}, err
	}
	n := len(probeGames)

	rawCos := cosine(probe, fingerprint)
	zt := s.ztNorm(rawCos, probe)

	entry := a.CalibrationTables[bucketFor(n)]
	z := (zt - entry.ImpostorMean) / clampStd(entry.ImpostorStd)

	ops := make(map[string]bool, len(s.calibratedOps))
	for name, threshold := range s.calibratedOps {
		ops[name] = z >= threshold
	}

	return Score{
		Z:               z,
		Cosine:          rawCos,
		EvidenceN:       n,
		OperatingPoints: ops,
	}, nil
}

// bucketFor maps an evidence count to its calibration table key. Counts
// between bucket boundaries use the nearest lower bucket, whose variance
// is a conservative (wider) estimate.
func bucketFor(n int) string {
	switch {
	case n <= 1:
		return "1"
	case n == 2:
		return "2"
	case n < 5:
		return "3"
	case n < 8:
		return "5"
	default:
		return "8+"
	}
}

// ztNorm applies z-norm and t-norm using the artifact's cohort and averages
// them. This must mirror the calibration-time math in the training package —
// the artifact's calibration tables and thresholds are only valid for scores
// normalized exactly this way.
func (s *Scorer) ztNorm(rawCosine float64, probe []float64) float64 {
	a := s.artifact
	cohort := a.CohortNorm
	k := a.K

	zSum := 0.0
	for i := 0; i < cohort.NumCohort; i++ {
		zSum += (rawCosine - cohort.ZNormMeans[i]) / clampStd(cohort.ZNormStds[i])
	}
	zNormed := zSum / float64(cohort.NumCohort)

	tScores := make([]float64, cohort.NumCohort)
	for i := 0; i < cohort.NumCohort; i++ {
		cv := cohort.CohortVectors[i*k : (i+1)*k]
		tScores[i] = cosine(probe, cv)
	}
	tMean, tStd := meanStd(tScores)
	tNormed := (rawCosine - tMean) / clampStd(tStd)

	return (zNormed + tNormed) / 2
}

func cosine(a, b []float64) float64 {
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

func meanStd(xs []float64) (float64, float64) {
	if len(xs) == 0 {
		return 0, 1
	}
	m := 0.0
	for _, x := range xs {
		m += x
	}
	m /= float64(len(xs))
	v := 0.0
	for _, x := range xs {
		d := x - m
		v += d * d
	}
	v /= float64(len(xs))
	sd := 0.0
	if v > 0 {
		sd = math.Sqrt(v)
	}
	return m, sd
}

func clampStd(s float64) float64 {
	if s < 1e-12 {
		return 1e-12
	}
	return s
}
