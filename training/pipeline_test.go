package training

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marianogappa/scfingerprint/features"
)

var update = flag.Bool("update", false, "regenerate golden files")

// syntheticDataset creates a deterministic dataset with numPlayers players,
// gamesPerPlayer games each, and d-dimensional vectors. Each player has a
// stable signal added to random-looking (but deterministic) noise.
func syntheticDataset(numPlayers, gamesPerPlayer, d int) []Sample {
	var samples []Sample
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	for p := 0; p < numPlayers; p++ {
		playerName := string(rune('A' + p))
		for g := 0; g < gamesPerPlayer; g++ {
			vec := make([]float64, d)
			for j := 0; j < d; j++ {
				// Deterministic pseudo-random: use a simple LCG seeded by (p, g, j).
				seed := uint64(p*1000000 + g*1000 + j)
				seed = seed*6364136223846793005 + 1442695040888963407
				noise := float64(int64(seed>>33)) / float64(1<<30)

				// Per-player signal: each player has a distinct mean per feature.
				signal := float64(p*7+j*3) / float64(d)

				vec[j] = signal + noise*0.3
			}
			samples = append(samples, Sample{
				File:      "synthetic.rep",
				Player:    playerName,
				Race:      "Zerg",
				StartTime: baseTime.Add(time.Duration(g) * time.Hour),
				Vector:    vec,
			})
		}
	}
	return samples
}

func TestPipelineDeterminism(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names) // 360

	samples := syntheticDataset(10, 20, d)
	cfg := DefaultConfig()
	cfg.K = 20 // small K for fast test
	cfg.CohortSize = 5
	cfg.MinGamesPerPlayer = 5
	cfg.Corpora = []string{"synthetic"}
	cfg.GitSHA = "test-sha"

	art1, err := Fit(samples, cfg)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}

	if err := art1.Validate(); err != nil {
		t.Fatalf("artifact validation failed: %v", err)
	}

	if art1.FeatureVersion != features.Version {
		t.Fatalf("feature version = %d, want %d", art1.FeatureVersion, features.Version)
	}
	if art1.K != 20 {
		t.Fatalf("K = %d, want 20", art1.K)
	}
	if len(art1.Means) != d {
		t.Fatalf("means len = %d, want %d", len(art1.Means), d)
	}
	if len(art1.WhiteningMatrix) != 20*20 {
		t.Fatalf("whitening matrix len = %d, want %d", len(art1.WhiteningMatrix), 20*20)
	}

	// Run Fit again — must produce identical JSON.
	samples2 := syntheticDataset(10, 20, d)
	art2, err := Fit(samples2, cfg)
	if err != nil {
		t.Fatalf("Fit (2nd run): %v", err)
	}

	j1, _ := json.MarshalIndent(art1, "", " ")
	j2, _ := json.MarshalIndent(art2, "", " ")

	// Mask the train_date field which uses time.Now().
	j1 = maskDate(j1)
	j2 = maskDate(j2)

	if string(j1) != string(j2) {
		t.Fatal("two Fit() runs on identical data produced different JSON")
	}
}

func TestPipelineGolden(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	samples := syntheticDataset(10, 20, d)
	cfg := DefaultConfig()
	cfg.K = 20
	cfg.CohortSize = 5
	cfg.MinGamesPerPlayer = 5
	cfg.Corpora = []string{"synthetic"}
	cfg.GitSHA = "test-sha"

	art, err := Fit(samples, cfg)
	if err != nil {
		t.Fatalf("Fit: %v", err)
	}

	// Override the date for determinism.
	art.Provenance.TrainDate = "2025-01-01"

	data, err := json.MarshalIndent(art, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')

	goldenPath := filepath.Join("testdata", "golden_artifact.json")

	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(goldenPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", goldenPath, len(data))
		return
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file (run with -update to generate): %v", err)
	}
	if string(data) != string(golden) {
		t.Fatal("artifact JSON does not match golden file; run with -update if the change is intentional")
	}
}

func TestPipelineMinPlayers(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	// Only 1 player with 3 games — below MinGamesPerPlayer=5.
	samples := syntheticDataset(1, 3, d)
	cfg := DefaultConfig()
	cfg.K = 10
	cfg.MinGamesPerPlayer = 5
	_, err := Fit(samples, cfg)
	if err == nil {
		t.Fatal("expected error for insufficient players")
	}
}

func TestStandardizer(t *testing.T) {
	samples := []Sample{
		{Vector: []float64{2, 4}},
		{Vector: []float64{4, 8}},
		{Vector: []float64{6, 12}},
	}
	means, stds := FitStandardizer(samples)
	if math.Abs(means[0]-4) > 1e-10 || math.Abs(means[1]-8) > 1e-10 {
		t.Fatalf("means = %v, want [4 8]", means)
	}

	ApplyStandardizer(samples, means, stds)
	// After standardization, mean should be ~0.
	sum := 0.0
	for _, s := range samples {
		sum += s.Vector[0]
	}
	if math.Abs(sum) > 1e-10 {
		t.Fatalf("standardized mean = %v, want ~0", sum/3)
	}
}

func TestFRatioSelect(t *testing.T) {
	// Feature 0 is discriminative (different mean per class), features 1-2 are noise.
	samples := []Sample{
		{Player: "A", Vector: []float64{10, 0.1, 0.5}},
		{Player: "A", Vector: []float64{11, 0.2, 0.3}},
		{Player: "B", Vector: []float64{-10, 0.15, 0.4}},
		{Player: "B", Vector: []float64{-11, 0.05, 0.6}},
	}
	indices := FRatioSelect(samples, 1)
	if len(indices) != 1 || indices[0] != 0 {
		t.Fatalf("expected feature 0 selected, got %v", indices)
	}
}

func maskDate(data []byte) []byte {
	// Replace the train_date value with a fixed string for comparison.
	s := string(data)
	start := 0
	for {
		i := indexOf(s[start:], `"train_date": "`)
		if i < 0 {
			break
		}
		i += start + len(`"train_date": "`)
		j := indexOf(s[i:], `"`)
		if j < 0 {
			break
		}
		s = s[:i] + "MASKED" + s[i+j:]
		start = i + 6
	}
	return []byte(s)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
