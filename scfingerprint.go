// Package scfingerprint identifies StarCraft: Brood War players by how they
// play — hotkey habits, muscle-memory command loops, action rhythm — rather
// than by what they're named. It operates on already-parsed screp in-memory
// replay models, so callers never pay for a re-parse.
//
// Core operations:
//
//   - [Extract]: turn a parsed replay into per-player feature vectors.
//   - [Match] / [MatchMany]: identify a player against a [Dataset] of known fingerprints.
//   - [Same]: pairwise "are these the same human?" without any dataset.
//   - [Enroll]: build a [Fingerprint] from observed games.
//   - [BuiltinDataset]: the shipped catalog of known player fingerprints.
//
// Results always carry a calibrated z-score, evidence count, and operating
// points cleared — never bare booleans. Community trust depends on honest
// confidence reporting.
//
// This package is the entire supported API. Everything under internal/ is
// implementation and may change without notice.
package scfingerprint

import (
	"github.com/icza/screp/rep"
	"github.com/marianogappa/scfingerprint/internal/features"
	"github.com/marianogappa/scfingerprint/internal/scoring"
)

// Replay is the parsed screp replay model that all scfingerprint APIs operate on.
type Replay = rep.Replay

// PlayerGame identifies one player's observation: either an already-parsed
// replay plus a player slot ID, or a pre-extracted feature vector (so callers
// that cache vectors in their own store never re-extract).
type PlayerGame struct {
	// Replay + PlayerID: the screp in-memory model. Extract will be called
	// automatically. Ignored when Vector is set.
	Replay   *Replay
	PlayerID byte

	// Vector: a pre-extracted raw feature vector, as returned by Extract.
	// When set, Replay/PlayerID are ignored.
	Vector []float64

	// Race of this player in this game, for race-aware sub-fingerprint
	// matching. Optional; when empty, the global mean is used.
	Race string
}

// PlayerVector is one player's extracted feature vector plus the identity and
// volume metadata needed to interpret it.
type PlayerVector struct {
	PlayerID byte
	Name     string
	Race     string
	Vector   []float64 // raw feature vector for FeatureVersion()
	Frames   int       // game length in frames
	CmdCount int       // commands issued by this player
}

// MatchResult is one candidate identity returned by Match or MatchMany.
type MatchResult struct {
	Label            string          // the fingerprint's label
	Z                float64         // calibrated z-score, comparable across evidence counts
	Cosine           float64         // raw cosine similarity
	EvidenceN        int             // number of games in the probe
	OperatingPoints  map[string]bool // named per-comparison thresholds cleared
	SearchFPR        map[string]bool // Šidák-corrected thresholds at the search (1:N) level
	CatalogSize      int             // N used for the search-level correction
	ModelIsSynthetic bool            // true when the backing model was trained on synthetic data
}

// Verdict is the result of a pairwise Same comparison.
type Verdict struct {
	Z                float64         // calibrated z-score
	Cosine           float64         // raw cosine
	EvidenceN        int             // total games across both sides
	OperatingPoints  map[string]bool // named thresholds cleared
	ModelIsSynthetic bool            // true when the backing model was trained on synthetic data
}

// Option configures the top-level API functions.
type Option func(*options)

type options struct {
	minZ float64 // results below this calibrated z are suppressed
}

func defaultOptions() options {
	return options{minZ: 2.0}
}

// WithMinZ sets the minimum calibrated z-score for a result to be returned.
// The default (2.0) suppresses noise; set to math.Inf(-1) to see everything.
func WithMinZ(z float64) Option {
	return func(o *options) { o.minZ = z }
}

// Extract turns a parsed replay into one feature vector per human player.
// Callers should store the vectors alongside FeatureVersion() and re-extract
// when that version changes.
func Extract(r *Replay) ([]PlayerVector, error) {
	pfs, err := features.Extract(r)
	if err != nil {
		return nil, err
	}
	out := make([]PlayerVector, len(pfs))
	for i, pf := range pfs {
		out[i] = PlayerVector{
			PlayerID: pf.PlayerID,
			Name:     pf.Name,
			Race:     pf.Race,
			Vector:   pf.Vector,
			Frames:   pf.Frames,
			CmdCount: pf.CmdCount,
		}
	}
	return out, nil
}

// FeatureVersion is the feature schema version this build extracts. Vectors
// are only comparable within a version; store it with any cached vector.
func FeatureVersion() int { return features.Version }

// ModelTag identifies the embedded trained model, e.g. "v1/2026-08-22/d86de99".
// It changes when the artifact is retrained, so it is the right key for
// invalidating caches derived from scores.
func ModelTag() (string, error) {
	s, err := scoring.NewFromEmbedded()
	if err != nil {
		return "", err
	}
	return s.ModelTag(), nil
}
