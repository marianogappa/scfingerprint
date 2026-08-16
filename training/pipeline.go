package training

import (
	"fmt"
	"sort"
	"time"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/model"
)

// Config controls the training pipeline.
type Config struct {
	K                 int      // feature selection K (default 150)
	Shrinkage         float64  // whitening shrinkage alpha (default 0.15)
	TrainFrac         float64  // chronological train/holdout split (default 0.7)
	CohortSize        int      // number of cohort players (default 30)
	MinGamesPerPlayer int      // minimum games to include a player (default 5)
	Corpora           []string // provenance: corpus names
	GitSHA            string   // provenance: git sha
}

// DefaultConfig returns a Config with the validated defaults.
func DefaultConfig() Config {
	return Config{
		K:                 150,
		Shrinkage:         0.15,
		TrainFrac:         0.7,
		CohortSize:        30,
		MinGamesPerPlayer: 5,
	}
}

// Fit runs the full training pipeline on the provided samples and returns
// a complete Artifact. The pipeline is deterministic: same inputs produce
// byte-identical JSON.
func Fit(samples []Sample, cfg Config) (*model.Artifact, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("training: no samples")
	}
	d := len(samples[0].Vector)
	if cfg.K > d {
		cfg.K = d
	}

	// Filter to players with enough games.
	samples = filterMinGames(samples, cfg.MinGamesPerPlayer)
	if len(samples) == 0 {
		return nil, fmt.Errorf("training: no players with >= %d games", cfg.MinGamesPerPlayer)
	}

	// Chronological split.
	train, heldOut := chronologicalSplit(samples, cfg.TrainFrac)
	if len(train) == 0 || len(heldOut) == 0 {
		return nil, fmt.Errorf("training: split produced empty train (%d) or held-out (%d)", len(train), len(heldOut))
	}

	// Deep-copy vectors so mutations don't affect the caller.
	train = deepCopySamples(train)
	heldOut = deepCopySamples(heldOut)

	// 1. Standardize.
	means, stds := FitStandardizer(train)
	ApplyStandardizer(train, means, stds)
	ApplyStandardizer(heldOut, means, stds)

	// 2. Feature selection.
	indices := FRatioSelect(train, cfg.K)

	ApplySelection(train, indices)
	ApplySelection(heldOut, indices)
	k := len(indices)

	// 3. Whitening.
	W, err := FitWhitening(train, cfg.Shrinkage)
	if err != nil {
		return nil, err
	}
	ApplyWhitening(train, W, k)
	ApplyWhitening(heldOut, W, k)

	// 4. Cohort normalization.
	cohort := BuildCohortNorm(heldOut, k, cfg.CohortSize)

	// 5. Per-n calibration tables.
	calTables := BuildCalibrationTables(heldOut, cohort, k)

	// 6. Operating-point thresholds.
	opPoints := ComputeThresholds(heldOut, cohort, k)

	// Count unique players.
	playerSet := map[string]bool{}
	for _, s := range samples {
		playerSet[s.Player] = true
	}

	return &model.Artifact{
		SchemaVersion:     1,
		FeatureVersion:    features.Version,
		Means:             means,
		Stds:              stds,
		SelectedIndices:   indices,
		K:                 k,
		WhiteningMatrix:   W,
		CohortNorm:        cohort,
		CalibrationTables: calTables,
		OperatingPoints:   opPoints,
		Provenance: model.Provenance{
			Corpora:    cfg.Corpora,
			TrainDate:  time.Now().UTC().Format("2006-01-02"),
			GitSHA:     cfg.GitSHA,
			NumGames:   len(samples),
			NumPlayers: len(playerSet),
		},
	}, nil
}

func filterMinGames(samples []Sample, minGames int) []Sample {
	counts := map[string]int{}
	for _, s := range samples {
		counts[s.Player]++
	}
	var out []Sample
	for _, s := range samples {
		if counts[s.Player] >= minGames {
			out = append(out, s)
		}
	}
	return out
}

// chronologicalSplit splits samples per-player by time: the first trainFrac
// of each player's sorted games go to train, the rest to held-out.
// Samples must already be sorted by (Player, StartTime).
func chronologicalSplit(samples []Sample, trainFrac float64) (train, heldOut []Sample) {
	byPlayer := map[string][]Sample{}
	for _, s := range samples {
		byPlayer[s.Player] = append(byPlayer[s.Player], s)
	}
	// Deterministic iteration order.
	players := sortedKeys(byPlayer)
	for _, p := range players {
		ss := byPlayer[p]
		// Already sorted by StartTime within player (from ReadCSV or test setup).
		sort.SliceStable(ss, func(i, j int) bool {
			return ss[i].StartTime.Before(ss[j].StartTime)
		})
		splitIdx := int(float64(len(ss)) * trainFrac)
		if splitIdx < 1 {
			splitIdx = 1
		}
		if splitIdx >= len(ss) {
			splitIdx = len(ss) - 1
		}
		train = append(train, ss[:splitIdx]...)
		heldOut = append(heldOut, ss[splitIdx:]...)
	}
	return train, heldOut
}

func deepCopySamples(samples []Sample) []Sample {
	out := make([]Sample, len(samples))
	for i, s := range samples {
		v := make([]float64, len(s.Vector))
		copy(v, s.Vector)
		out[i] = s
		out[i].Vector = v
	}
	return out
}
