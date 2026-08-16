// Package scfingerprint identifies StarCraft: Brood War players by how they
// play — hotkey habits, muscle-memory command loops, action rhythm — rather
// than by what they're named. It operates on already-parsed screp in-memory
// replay models, so callers never pay for a re-parse.
//
// Core operations:
//
//   - [Match] / [MatchMany]: identify a player against a [Dataset] of known fingerprints.
//   - [Same]: pairwise "are these the same human?" without any dataset.
//   - [Enroll]: build a [Fingerprint] from observed games.
//
// Results always carry a calibrated z-score, evidence count, and operating
// points cleared — never bare booleans. Community trust depends on honest
// confidence reporting.
package scfingerprint

import (
	"github.com/icza/screp/rep"
	"github.com/marianogappa/scfingerprint/fingerprint"
)

// Replay is the parsed screp replay model that all scfingerprint APIs operate on.
type Replay = rep.Replay

// Fingerprint is a re-export of [fingerprint.Fingerprint] for caller convenience.
type Fingerprint = fingerprint.Fingerprint

// Meta is a re-export of [fingerprint.Meta] for caller convenience.
type Meta = fingerprint.Meta

// PlayerGame identifies one player's observation: either an already-parsed
// replay plus a player slot ID, or a pre-extracted feature vector (so callers
// like screpdb that cache vectors in their DB never re-extract).
type PlayerGame struct {
	// Replay + PlayerID: the screp in-memory model. Extract will be called
	// automatically. Ignored when Vector is set.
	Replay   *Replay
	PlayerID byte

	// Vector: a pre-extracted raw feature vector (360 dims for v3). When set,
	// Replay/PlayerID are ignored.
	Vector []float64

	// Race of this player in this game, for race-aware sub-fingerprint
	// matching. Optional; when empty, the global mean is used.
	Race string
}

// MatchResult is one candidate identity returned by Match or MatchMany.
type MatchResult struct {
	Label           string          // the fingerprint's label
	Z               float64         // calibrated z-score, comparable across evidence counts
	Cosine          float64         // raw cosine similarity
	EvidenceN       int             // number of games in the probe
	OperatingPoints map[string]bool // named thresholds cleared
}

// Verdict is the result of a pairwise Same comparison.
type Verdict struct {
	Z               float64         // calibrated z-score
	Cosine          float64         // raw cosine
	EvidenceN       int             // total games across both sides
	OperatingPoints map[string]bool // named thresholds cleared
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
