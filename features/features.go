// Package features turns one player's command stream from a parsed StarCraft:
// Brood War replay into a fixed-length feature vector. The vector captures how
// a player plays — hotkey habits, command loops, action rhythm — and is the
// input to fingerprint building and matching.
//
// Vectors are only comparable within a schema version; FeatureNames reports
// the stable, ordered dimension names for each version.
package features

import (
	"fmt"

	"github.com/icza/screp/rep"
	"github.com/icza/screp/repparser"
)

// Version is the current feature schema version. Vectors from different
// versions are not comparable.
const Version = 3

const (
	framesPerMin = 1428.6

	// DefaultMinCommands is the minimum command count for a player to be
	// eligible for extraction; below it there isn't enough signal for a
	// stable vector.
	DefaultMinCommands = 150

	// DefaultMinGameFrames is the minimum game length for extraction:
	// 2 in-game minutes (2*framesPerMin, rounded up).
	DefaultMinGameFrames = 2858
)

// PlayerFeatures is one player's extracted feature vector plus the identity
// and volume metadata needed to interpret it.
type PlayerFeatures struct {
	PlayerID byte
	Name     string
	Race     string
	Vector   []float64 // len == len(FeatureNames(Version))
	Version  int       // feature schema version
	Frames   int       // game length in frames
	CmdCount int       // commands issued by this player
}

// Option customizes extraction eligibility thresholds.
type Option func(*options)

type options struct {
	minCommands   int
	minGameFrames int
}

// WithMinCommands overrides the minimum per-player command count required for
// a player to be included in the result.
func WithMinCommands(n int) Option {
	return func(o *options) { o.minCommands = n }
}

// WithMinGameFrames overrides the minimum game length (in frames) required
// for any player to be included in the result.
func WithMinGameFrames(n int) Option {
	return func(o *options) { o.minGameFrames = n }
}

// Extract computes a feature vector for every eligible human player in an
// already-parsed screp replay. The replay must have been parsed with commands
// (repparser.Config{Commands: true}); callers that already paid for parsing
// are never forced to re-parse. Observers and computer players are excluded,
// as are players below the minimum command count and games below the minimum
// length (see DefaultMinCommands and DefaultMinGameFrames).
//
// Players are returned in header order. A replay where no player is eligible
// yields an empty slice and no error.
func Extract(r *rep.Replay, opts ...Option) ([]PlayerFeatures, error) {
	if r == nil {
		return nil, fmt.Errorf("features: replay is nil")
	}
	if r.Commands == nil {
		return nil, fmt.Errorf("features: replay has no commands; parse with repparser.Config{Commands: true}")
	}

	o := options{minCommands: DefaultMinCommands, minGameFrames: DefaultMinGameFrames}
	for _, opt := range opts {
		opt(&o)
	}

	// Compute fills in per-command IneffKind (needed for eAPM/redundancy).
	// It is idempotent, so callers that already computed pay nothing.
	r.Compute()

	accs := map[byte]*accumulator{}
	var order []byte
	for _, p := range r.Header.Players {
		if p.Observer || p.Type == nil || p.Type.Name != "Human" {
			continue
		}
		cmdCount := 0
		if pd := r.Computed.PIDPlayerDescs[p.ID]; pd != nil {
			cmdCount = int(pd.CmdCount)
		}
		a := newAccumulator(cmdCount)
		a.name = p.Name
		if p.Race != nil {
			a.race = p.Race.Name
		}
		accs[p.ID] = a
		order = append(order, p.ID)
	}

	for _, cmd := range r.Commands.Cmds {
		if a, ok := accs[cmd.BaseCmd().PlayerID]; ok {
			a.add(cmd)
		}
	}

	gameFrames := int(r.Header.Frames)
	var out []PlayerFeatures
	for _, id := range order {
		a := accs[id]
		if int(a.total) < o.minCommands || gameFrames < o.minGameFrames {
			continue
		}
		out = append(out, PlayerFeatures{
			PlayerID: id,
			Name:     a.name,
			Race:     a.race,
			Vector:   a.features(),
			Version:  Version,
			Frames:   gameFrames,
			CmdCount: int(a.total),
		})
	}
	return out, nil
}

// ExtractFile parses a replay file and extracts feature vectors from it.
// It is a convenience wrapper around Extract for CLI / standalone use;
// library callers holding a parsed *rep.Replay should call Extract directly.
func ExtractFile(path string, opts ...Option) ([]PlayerFeatures, error) {
	r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
	if err != nil {
		return nil, fmt.Errorf("features: parsing %s: %w", path, err)
	}
	return Extract(r, opts...)
}

// FeatureNames returns the stable, ordered names of every dimension in the
// given schema version's vector. It errors on unknown versions.
func FeatureNames(version int) ([]string, error) {
	if version != Version {
		return nil, fmt.Errorf("features: unknown feature schema version %d (supported: %d)", version, Version)
	}
	return featureNamesV3(), nil
}
