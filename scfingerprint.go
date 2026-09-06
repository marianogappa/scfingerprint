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
//   - [BuiltinRegistry]: the shipped identity map, for name lookups.
//
// Results always carry a calibrated z-score, evidence count, and operating
// points cleared — never bare booleans. Community trust depends on honest
// confidence reporting.
//
// The one exception is [Registry], which is a name lookup rather than a
// measurement: it has no calibration and no error rate, never enters a score,
// and must never be quoted as confirming one.
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

	// Toon is the account name this game was played on. Optional, and only
	// read when a [Registry] is supplied via [WithRegistry]; when empty and
	// Replay is set, the name in the replay slot is used. It exists so a
	// caller passing cached Vectors can still get a registry opinion.
	Toon string
}

// PlayerVector is one player's extracted feature vector plus the identity and
// volume metadata needed to interpret it.
type PlayerVector struct {
	PlayerID byte      `json:"player_id"`
	Name     string    `json:"name"`
	Race     string    `json:"race"`
	Vector   []float64 `json:"vector"`    // raw feature vector for FeatureVersion()
	Frames   int       `json:"frames"`    // game length in frames
	CmdCount int       `json:"cmd_count"` // commands issued by this player
}

// MatchResult is one candidate identity returned by Match or MatchMany.
type MatchResult struct {
	Label            string          `json:"label"`                // the fingerprint's label
	Liquipedia       string          `json:"liquipedia,omitempty"` // the player's Liquipedia profile URL, when known
	Z                float64         `json:"z"`                    // calibrated z-score, comparable across evidence counts
	Cosine           float64         `json:"cosine"`               // raw cosine similarity
	EvidenceN        int             `json:"evidence_n"`           // number of games in the probe
	OperatingPoints  map[string]bool `json:"operating_points"`     // named per-comparison thresholds cleared
	SearchFPR        float64         `json:"search_fpr"`           // family-wise FPR across the whole catalog (see below)
	CatalogSize      int             `json:"catalog_size"`         // N used for the search-level correction
	ModelIsSynthetic bool            `json:"model_is_synthetic"`   // true when the backing model was trained on synthetic data

	// Registry is what the built-in identity map says about the observed
	// player, present only when [WithRegistry] was passed. It is a name
	// lookup, not evidence: it never influences Z, Cosine or SearchFPR, and
	// a Registry that disagrees with the top candidate is a finding to
	// surface rather than a score to adjust. See [RegistryOpinion].
	Registry *RegistryOpinion `json:"registry,omitempty"`
}

// Verdict is the result of a pairwise Same comparison.
type Verdict struct {
	Z                float64         `json:"z"`                  // calibrated z-score
	Cosine           float64         `json:"cosine"`             // raw cosine
	EvidenceN        int             `json:"evidence_n"`         // total games across both sides
	OperatingPoints  map[string]bool `json:"operating_points"`   // named thresholds cleared
	FPR              float64         `json:"fpr"`                // strictest false-positive rate this verdict clears; 1.0 = none
	ModelIsSynthetic bool            `json:"model_is_synthetic"` // true when the backing model was trained on synthetic data
}

// Option configures the top-level API functions.
type Option func(*options)

type options struct {
	minZ     float64   // results below this calibrated z are suppressed
	registry *Registry // when set, results carry a registry opinion
}

func defaultOptions() options {
	return options{minZ: 2.0}
}

// WithMinZ sets the minimum calibrated z-score for a result to be returned.
// The default (2.0) suppresses noise; set to math.Inf(-1) to see everything.
func WithMinZ(z float64) Option {
	return func(o *options) { o.minZ = z }
}

// WithRegistry attaches the identity map's opinion to every [MatchResult], as
// [MatchResult.Registry]. Opt-in, because it is a second and much weaker kind
// of answer and the default result should carry only the fingerprint's.
//
// The opinion is derived from the account name the games were played on
// ([PlayerGame.Toon], or the replay slot name), looked up in the registry. It
// changes nothing about the scoring: pass it or not, Z, Cosine, SearchFPR and
// the result ordering are identical.
func WithRegistry(r *Registry) Option {
	return func(o *options) { o.registry = r }
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
