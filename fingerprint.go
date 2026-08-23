package scfingerprint

import (
	"fmt"

	"github.com/marianogappa/scfingerprint/internal/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/hygiene"
	"github.com/marianogappa/scfingerprint/internal/scoring"
)

// Meta is curator-provided provenance for an enrollment.
type Meta struct {
	Label      string `json:"label,omitempty"`      // display identity, e.g. "C9_FlaSh"
	Source     string `json:"source,omitempty"`     // corpus the games came from
	DateFrom   string `json:"date_from,omitempty"`  // earliest game date (YYYY-MM-DD)
	DateTo     string `json:"date_to,omitempty"`    // latest game date (YYYY-MM-DD)
	Confidence string `json:"confidence,omitempty"` // curator confidence tier
}

// Fingerprint is the enrollable, storable identity object: a running aggregate
// over many games of one player's feature vectors.
//
// Add never needs the raw history, so a fingerprint can be maintained
// incrementally as new games arrive. MarshalString serializes it to a single
// versioned JSON string, suitable for one database text column.
//
// The zero value is not usable; construct with NewFingerprint, ParseFingerprint
// or Enroll.
type Fingerprint struct {
	inner *fingerprint.Fingerprint
}

// NewFingerprint creates an empty fingerprint for the current feature version.
func NewFingerprint(meta Meta) *Fingerprint {
	return &Fingerprint{inner: fingerprint.New(toInnerMeta(meta))}
}

// ParseFingerprint deserializes a fingerprint from its MarshalString form.
// A blob written by a build with an incompatible feature version returns an
// error: the blob is not corrupt, it needs recomputing from replays.
func ParseFingerprint(s string) (*Fingerprint, error) {
	inner, err := fingerprint.Parse(s)
	if err != nil {
		return nil, err
	}
	return &Fingerprint{inner: inner}, nil
}

// Add folds one game's raw feature vector into the fingerprint. race may be
// empty when unknown; full names and single-letter codes are both accepted.
// No raw vectors are retained.
func (f *Fingerprint) Add(vector []float64, race string) error {
	return f.inner.Add(vector, race)
}

// MarshalString serializes the fingerprint to a single versioned JSON string.
func (f *Fingerprint) MarshalString() (string, error) {
	return f.inner.MarshalString()
}

// Meta returns the fingerprint's provenance.
func (f *Fingerprint) Meta() Meta {
	m := f.inner.Meta
	return Meta{
		Label:      m.Label,
		Source:     m.Source,
		DateFrom:   m.DateFrom,
		DateTo:     m.DateTo,
		Confidence: m.Confidence,
	}
}

// N returns the number of games aggregated into this fingerprint.
func (f *Fingerprint) N() int { return f.inner.N() }

// RaceCounts returns the per-race game counts, keyed by single-letter race
// codes ("z", "t", "p", "r").
func (f *Fingerprint) RaceCounts() map[string]int { return f.inner.RaceCounts() }

// SelfConsistency splits the contributing games into chronological halves and
// returns the cosine similarity between the two half-centroids. Genuine
// single-person enrollments score around 0.96; a wrongly-merged two-person
// enrollment scored 0.44 and poisoned an entire corpus's calibration. Check
// this before trusting a merge.
func (f *Fingerprint) SelfConsistency() (float64, error) {
	s, err := scoring.NewFromEmbedded()
	if err != nil {
		return 0, err
	}
	return f.inner.SelfConsistency(s)
}

// SelfConsistencyGate runs SelfConsistency against the default catalog-hygiene
// threshold, returning the score and a non-nil error when the enrollment looks
// contaminated. Enrollments that fail this must not be merged into a catalog:
// one wrongly-merged two-person enrollment sets the operating-point thresholds
// and collapses true-positive rate for every other identity.
func (f *Fingerprint) SelfConsistencyGate() (float64, error) {
	s, err := scoring.NewFromEmbedded()
	if err != nil {
		return 0, err
	}
	return hygiene.SelfConsistencyGate(f.inner, s, hygiene.DefaultThresholds())
}

// Enroll builds a fingerprint from one or more observed games.
func Enroll(games []PlayerGame, meta Meta) (*Fingerprint, error) {
	if len(games) == 0 {
		return nil, fmt.Errorf("scfingerprint: no games to enroll")
	}
	fp := fingerprint.New(toInnerMeta(meta))
	for _, g := range games {
		vec, race, err := resolveVector(g)
		if err != nil {
			return nil, err
		}
		if err := fp.Add(vec, race); err != nil {
			return nil, err
		}
	}
	return &Fingerprint{inner: fp}, nil
}

func toInnerMeta(m Meta) fingerprint.Meta {
	return fingerprint.Meta{
		Label:      m.Label,
		Source:     m.Source,
		DateFrom:   m.DateFrom,
		DateTo:     m.DateTo,
		Confidence: m.Confidence,
	}
}
