// Package model defines the trained model artifact and provides loading
// and validation. The artifact carries everything the scoring package needs
// to transform a raw 360-dim feature vector into a calibrated score.
//
// A default artifact is embedded into the binary via go:embed; callers that
// train on their own corpus can load a custom artifact from JSON bytes.
package model

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
)

//go:embed artifact.json
var embeddedArtifact []byte

// LoadEmbedded returns the compiled-in model artifact.
func LoadEmbedded() (*Artifact, error) {
	return Load(embeddedArtifact)
}

// Load parses and validates a model artifact from JSON bytes.
func Load(data []byte) (*Artifact, error) {
	var a Artifact
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("model: invalid artifact JSON: %w", err)
	}
	if err := a.Validate(); err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	return &a, nil
}

// Artifact is the trained model artifact.
type Artifact struct {
	SchemaVersion  int `json:"schema_version"`
	FeatureVersion int `json:"feature_version"`

	Means []float64 `json:"means"`
	Stds  []float64 `json:"stds"`

	SelectedIndices []int     `json:"selected_indices"`
	K               int       `json:"k"`
	WhiteningMatrix []float64 `json:"whitening_matrix"`

	CohortNorm        CohortNormStats             `json:"cohort_norm"`
	CalibrationTables map[string]CalibrationEntry `json:"calibration_tables"`
	OperatingPoints   map[string]float64          `json:"operating_points"`

	Provenance Provenance `json:"provenance"`
}

// CohortNormStats holds the cohort fingerprints and per-fingerprint
// impostor score statistics used for z-norm and t-norm.
type CohortNormStats struct {
	CohortVectors []float64 `json:"cohort_vectors"`
	NumCohort     int       `json:"num_cohort"`
	ZNormMeans    []float64 `json:"znorm_means"`
	ZNormStds     []float64 `json:"znorm_stds"`
}

// CalibrationEntry holds genuine and impostor score distribution stats
// for a single evidence-count bucket.
type CalibrationEntry struct {
	GenuineMean  float64 `json:"genuine_mean"`
	GenuineStd   float64 `json:"genuine_std"`
	ImpostorMean float64 `json:"impostor_mean"`
	ImpostorStd  float64 `json:"impostor_std"`
}

// Provenance records training metadata for reproducibility.
type Provenance struct {
	Corpora    []string `json:"corpora"`
	TrainDate  string   `json:"train_date"`
	GitSHA     string   `json:"git_sha"`
	NumGames   int      `json:"num_games"`
	NumPlayers int      `json:"num_players"`
}

var requiredCalibrationKeys = []string{"1", "2", "3", "5", "8+"}
var requiredOperatingPoints = []string{"fpr_1e2", "fpr_1e3", "fpr_1e4"}

// Validate checks structural invariants of the artifact.
func (a *Artifact) Validate() error {
	if a.SchemaVersion != 1 {
		return fmt.Errorf("unsupported schema version %d (want 1)", a.SchemaVersion)
	}
	if a.FeatureVersion < 1 {
		return fmt.Errorf("invalid feature version %d", a.FeatureVersion)
	}
	d := len(a.Means)
	if d == 0 {
		return fmt.Errorf("means is empty")
	}
	if len(a.Stds) != d {
		return fmt.Errorf("stds length %d != means length %d", len(a.Stds), d)
	}
	if a.K < 1 || a.K > d {
		return fmt.Errorf("k=%d out of range [1, %d]", a.K, d)
	}
	if len(a.SelectedIndices) != a.K {
		return fmt.Errorf("selected_indices length %d != k=%d", len(a.SelectedIndices), a.K)
	}
	for i, idx := range a.SelectedIndices {
		if idx < 0 || idx >= d {
			return fmt.Errorf("selected_indices[%d]=%d out of range [0, %d)", i, idx, d)
		}
	}
	if len(a.WhiteningMatrix) != a.K*a.K {
		return fmt.Errorf("whitening_matrix length %d != k*k=%d", len(a.WhiteningMatrix), a.K*a.K)
	}
	if err := checkFinite("means", a.Means); err != nil {
		return err
	}
	if err := checkFinite("stds", a.Stds); err != nil {
		return err
	}
	if err := checkFinite("whitening_matrix", a.WhiteningMatrix); err != nil {
		return err
	}

	cn := a.CohortNorm
	if cn.NumCohort < 1 {
		return fmt.Errorf("cohort_norm.num_cohort=%d < 1", cn.NumCohort)
	}
	if len(cn.CohortVectors) != cn.NumCohort*a.K {
		return fmt.Errorf("cohort_vectors length %d != num_cohort*k=%d", len(cn.CohortVectors), cn.NumCohort*a.K)
	}
	if len(cn.ZNormMeans) != cn.NumCohort {
		return fmt.Errorf("znorm_means length %d != num_cohort=%d", len(cn.ZNormMeans), cn.NumCohort)
	}
	if len(cn.ZNormStds) != cn.NumCohort {
		return fmt.Errorf("znorm_stds length %d != num_cohort=%d", len(cn.ZNormStds), cn.NumCohort)
	}

	for _, key := range requiredCalibrationKeys {
		if _, ok := a.CalibrationTables[key]; !ok {
			return fmt.Errorf("missing calibration table key %q", key)
		}
	}
	for _, key := range requiredOperatingPoints {
		if _, ok := a.OperatingPoints[key]; !ok {
			return fmt.Errorf("missing operating point %q", key)
		}
	}
	return nil
}

func checkFinite(name string, xs []float64) error {
	for i, v := range xs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%s[%d] is %v", name, i, v)
		}
	}
	return nil
}
