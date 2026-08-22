package dataset

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
)

var regenerate = flag.Bool("regenerate-dataset", false, "regenerate synthetic seed dataset files under players/ (for testing only; production dataset is built by cmd/seed-dataset)")

// TestRegenerateDataset writes synthetic seed identity files for isolated
// testing. The production dataset is built by cmd/seed-dataset from the real
// corpus. Run with -regenerate-dataset to overwrite the committed files.
func TestRegenerateDataset(t *testing.T) {
	if !*regenerate {
		t.Skip("pass -regenerate-dataset to regenerate")
	}

	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	type seed struct {
		id         string
		player     int
		games      int
		race       string
		confidence string
		aliases    []Alias
		notes      string
	}

	seeds := []seed{
		{"flash", 200, 60, "t", ConfidenceConfirmed, []Alias{{Name: "C9_FlaSh", Primary: true}, {Name: "FlaSh", ZScore: 12.3}}, "synthetic seed"},
		{"jaedong", 201, 55, "z", ConfidenceConfirmed, []Alias{{Name: "Jaedong", Primary: true}}, "synthetic seed"},
		{"bisu", 202, 50, "p", ConfidenceConfirmed, []Alias{{Name: "Bisu", Primary: true}}, "synthetic seed"},
		{"stork", 203, 45, "p", ConfidenceHigh, []Alias{{Name: "Stork", Primary: true}}, "synthetic seed"},
		{"effort", 204, 40, "z", ConfidenceHigh, []Alias{{Name: "Effort", Primary: true}}, "synthetic seed"},
	}

	dir := filepath.Join("players")
	for _, s := range seeds {
		fp := fingerprint.New(fingerprint.Meta{
			Label:      s.aliases[0].Name,
			Source:     "synthetic-seed",
			Confidence: s.confidence,
		})
		for g := 0; g < s.games; g++ {
			vec := synthtest.GameVector(s.player, g, d)
			if err := fp.Add(vec, s.race); err != nil {
				t.Fatal(err)
			}
		}
		blob, err := fp.MarshalString()
		if err != nil {
			t.Fatal(err)
		}

		manifest := make([]string, s.games)
		for g := 0; g < s.games; g++ {
			manifest[g] = synthtest.GameID(s.player, g)
		}

		id := Identity{
			ID:             s.id,
			Fingerprint:    blob,
			Confidence:     s.confidence,
			Aliases:        s.aliases,
			ReplayManifest: manifest,
			Notes:          s.notes,
		}
		data, err := json.MarshalIndent(id, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, s.id+".json")
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(data))
	}
}
