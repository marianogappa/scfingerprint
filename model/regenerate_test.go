package model_test

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
	"github.com/marianogappa/scfingerprint/training"
)

var regenerate = flag.Bool("regenerate-artifact", false, "regenerate the embedded model artifact from the synthetic corpus")

// TestRegenerateArtifact rebuilds model/artifact.json with the validated
// synthetic-corpus configuration (K=60, cohort 10 — the same setup the eval
// regression gates prove separates out-of-training identities). Run with
// -regenerate-artifact after changing the training pipeline or the synthetic
// corpus generator, then re-pin dependent goldens:
//
//	go test ./model/ -run TestRegenerateArtifact -regenerate-artifact
//	go test ./scoring/ -run TestEmbeddedArtifactReferenceScores -update
func TestRegenerateArtifact(t *testing.T) {
	if !*regenerate {
		t.Skip("pass -regenerate-artifact to regenerate")
	}

	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	cfg := training.DefaultConfig()
	cfg.K = 60
	cfg.CohortSize = 10
	cfg.MinGamesPerPlayer = 5
	cfg.Corpora = []string{"synthetic"}
	cfg.GitSHA = "embedded-synthetic"

	art, err := training.Fit(synthtest.Corpus(0, 30, 60, d), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Fixed date so regeneration is reproducible.
	art.Provenance.TrainDate = "2026-08-16"

	data, err := json.MarshalIndent(art, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("artifact.json", append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote artifact.json (%d bytes, K=%d)", len(data), art.K)
}
