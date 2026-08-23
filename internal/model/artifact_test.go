package model

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	a, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if a.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", a.SchemaVersion)
	}
	if a.FeatureVersion != 3 {
		t.Fatalf("feature version = %d, want 3", a.FeatureVersion)
	}
	if len(a.Means) != 360 {
		t.Fatalf("means len = %d, want 360", len(a.Means))
	}
	if a.K < 1 {
		t.Fatalf("k = %d, want >= 1", a.K)
	}
}

func TestValidateGood(t *testing.T) {
	a := minimalValidArtifact(3, 2)
	if err := a.Validate(); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidateBadSchema(t *testing.T) {
	a := minimalValidArtifact(3, 2)
	a.SchemaVersion = 99
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for bad schema version")
	}
}

func TestValidateMissingCalibration(t *testing.T) {
	a := minimalValidArtifact(3, 2)
	delete(a.CalibrationTables, "1")
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for missing calibration key")
	}
}

func TestValidateMissingOperatingPoint(t *testing.T) {
	a := minimalValidArtifact(3, 2)
	delete(a.OperatingPoints, "fpr_1e3")
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for missing operating point")
	}
}

func TestValidateNaN(t *testing.T) {
	a := minimalValidArtifact(3, 2)
	a.Means[0] = nan()
	if err := a.Validate(); err == nil {
		t.Fatal("expected error for NaN in means")
	}
}

func TestRoundTrip(t *testing.T) {
	a := minimalValidArtifact(3, 2)
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	data2, _ := json.Marshal(b)
	if string(data) != string(data2) {
		t.Fatal("round-trip marshal/unmarshal changed artifact")
	}
}

func minimalValidArtifact(d, k int) *Artifact {
	means := make([]float64, d)
	stds := make([]float64, d)
	for i := range stds {
		stds[i] = 1.0
	}
	indices := make([]int, k)
	for i := range indices {
		indices[i] = i
	}
	wm := make([]float64, k*k)
	for i := 0; i < k; i++ {
		wm[i*k+i] = 1.0
	}
	cv := make([]float64, k)
	for i := range cv {
		cv[i] = 0.1
	}

	return &Artifact{
		SchemaVersion:   1,
		FeatureVersion:  3,
		Means:           means,
		Stds:            stds,
		SelectedIndices: indices,
		K:               k,
		WhiteningMatrix: wm,
		CohortNorm: CohortNormStats{
			CohortVectors: cv,
			NumCohort:     1,
			ZNormMeans:    []float64{0},
			ZNormStds:     []float64{1},
		},
		CalibrationTables: map[string]CalibrationEntry{
			"1":  {0, 1, 0, 1},
			"2":  {0, 1, 0, 1},
			"3":  {0, 1, 0, 1},
			"5":  {0, 1, 0, 1},
			"8+": {0, 1, 0, 1},
		},
		OperatingPoints: map[string]float64{
			"fpr_1e2": 1.0,
			"fpr_1e3": 2.0,
			"fpr_1e4": 3.0,
		},
		Provenance: Provenance{
			Corpora:    []string{"test"},
			TrainDate:  "2025-01-01",
			GitSHA:     "abc123",
			NumGames:   10,
			NumPlayers: 2,
		},
	}
}

func TestIsSynthetic(t *testing.T) {
	a := minimalValidArtifact(3, 2)
	a.Provenance.Corpora = []string{"synthetic"}
	a.Provenance.GitSHA = "embedded-synthetic"
	if !a.IsSynthetic() {
		t.Fatal("expected IsSynthetic()=true for synthetic corpus and git_sha")
	}

	a.Provenance.Corpora = []string{"real-corpus"}
	a.Provenance.GitSHA = "abc123"
	if a.IsSynthetic() {
		t.Fatal("expected IsSynthetic()=false for non-synthetic artifact")
	}

	a.Provenance.GitSHA = "embedded-synthetic"
	if !a.IsSynthetic() {
		t.Fatal("expected IsSynthetic()=true for embedded-synthetic git_sha alone")
	}

	a.Provenance.GitSHA = "abc123"
	a.Provenance.Corpora = []string{"ladder", "synthetic"}
	if !a.IsSynthetic() {
		t.Fatal("expected IsSynthetic()=true when corpora includes synthetic")
	}
}

func TestEmbeddedArtifactIsNotSynthetic(t *testing.T) {
	if os.Getenv("SCFINGERPRINT_RELEASE_GATE") == "" {
		t.Skip("skipped outside release-gate CI (set SCFINGERPRINT_RELEASE_GATE=1 to run)")
	}
	a, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if a.IsSynthetic() {
		t.Fatal("embedded artifact is synthetic — release build blocked; train on real data before tagging")
	}
}

func TestEmbeddedArtifactIsNotSyntheticNow(t *testing.T) {
	a, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if a.IsSynthetic() {
		t.Fatal("embedded artifact should NOT be synthetic — it was trained on real corpus data")
	}
}

func nan() float64 {
	return math.NaN()
}
