// Package synthtest generates deterministic synthetic corpora and trained
// scorers for tests. The data has a stable per-player signal across features
// plus per-game noise, so identities are separable the way real players are.
package synthtest

import (
	"fmt"
	"testing"
	"time"

	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

// Races are assigned round-robin to synthetic players.
var Races = []string{"Zerg", "Terran", "Protoss"}

// Mix64 is a splitmix64-style finalizer mapping a seed to [0, 1). A bare LCG
// step leaves features within a game near-perfectly correlated, which makes
// the within-class covariance singular and whitening degenerate.
func Mix64(z uint64) float64 {
	z += 0x9E3779B97F4A7C15
	z ^= z >> 30
	z *= 0xBF58476D1CE4E5B9
	z ^= z >> 27
	z *= 0x94D049BB133111EB
	z ^= z >> 31
	return float64(z>>11) / float64(uint64(1)<<53)
}

// Corpus creates a deterministic corpus. The player offset makes disjoint
// corpora: training and evaluation must not share identities, or the
// artifact's cohort contaminates t-norm for players who are their own
// cohort entry.
func Corpus(offset, numPlayers, gamesPerPlayer, d int) []training.Sample {
	var samples []training.Sample
	baseTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for pi := 0; pi < numPlayers; pi++ {
		p := offset + pi
		playerName := fmt.Sprintf("P%03d", p)
		race := Races[p%len(Races)]
		for g := 0; g < gamesPerPlayer; g++ {
			samples = append(samples, training.Sample{
				File:      "synthetic.rep",
				Player:    playerName,
				Race:      race,
				StartTime: baseTime.Add(time.Duration(g) * time.Hour),
				Vector:    GameVector(p, g, d),
			})
		}
	}
	return samples
}

// GameVector generates one game's feature vector for a synthetic player:
// a stable per-(player, feature) profile plus per-(player, game, feature)
// noise.
func GameVector(p, g, d int) []float64 {
	vec := make([]float64, d)
	for j := 0; j < d; j++ {
		noise := Mix64(uint64(p*1000000 + g*1000 + j))
		signal := Mix64(uint64(p*991 + j))
		vec[j] = signal + noise*0.15
	}
	return vec
}

// GameID returns a stable replay identifier for a synthetic game, suitable
// for replay manifests.
func GameID(player, game int) string {
	return fmt.Sprintf("synth-p%d-g%d.rep", player, game)
}

// Scorer trains a small model on the given corpus and wraps it in a Scorer.
func Scorer(t testing.TB, samples []training.Sample) *scoring.Scorer {
	t.Helper()
	cfg := training.DefaultConfig()
	cfg.K = 60
	cfg.CohortSize = 10
	cfg.MinGamesPerPlayer = 5
	cfg.GitSHA = "test-sha"
	art, err := training.Fit(samples, cfg)
	if err != nil {
		t.Fatalf("synthtest: Fit: %v", err)
	}
	scorer, err := scoring.New(art)
	if err != nil {
		t.Fatalf("synthtest: scoring.New: %v", err)
	}
	return scorer
}
