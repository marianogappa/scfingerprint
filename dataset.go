package scfingerprint

import (
	"fmt"

	"github.com/marianogappa/scfingerprint/internal/dataset"
	"github.com/marianogappa/scfingerprint/internal/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/scoring"
)

// Confidence tiers for built-in dataset entries, from strictest to loosest.
// BuiltinDataset includes every entry at or above the tier it is given.
const (
	ConfidenceConfirmed = dataset.ConfidenceConfirmed
	ConfidenceHigh      = dataset.ConfidenceHigh
	ConfidenceCandidate = dataset.ConfidenceCandidate
)

// Dataset is a collection of known fingerprints that Match and MatchMany
// search against. Build an empty one with NewDataset and populate it with Add,
// or load the shipped catalog with BuiltinDataset.
type Dataset struct {
	scorer *scoring.Scorer
	fps    []*fingerprint.Fingerprint
	projs  [][]float64 // cached projected embeddings, parallel to fps
}

// NewDataset creates an empty dataset backed by the embedded model.
func NewDataset() (*Dataset, error) {
	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		return nil, fmt.Errorf("scfingerprint: loading embedded model: %w", err)
	}
	return &Dataset{scorer: scorer}, nil
}

// BuiltinDataset loads the shipped catalog of known player fingerprints,
// including every entry at or above minConfidence (see ConfidenceConfirmed,
// ConfidenceHigh, ConfidenceCandidate).
func BuiltinDataset(minConfidence string) (*Dataset, error) {
	db, err := dataset.NewDefaultDataset(nil, minConfidence)
	if err != nil {
		return nil, fmt.Errorf("scfingerprint: loading built-in dataset: %w", err)
	}
	d := &Dataset{scorer: db.Scorer()}
	for _, fp := range db.Fingerprints() {
		if err := d.add(fp); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// Add registers a fingerprint in the dataset, pre-computing and caching its
// projected embedding for fast comparison.
func (d *Dataset) Add(fp *Fingerprint) error {
	if fp == nil || fp.inner == nil {
		return fmt.Errorf("scfingerprint: nil fingerprint")
	}
	return d.add(fp.inner)
}

// Len returns the number of fingerprints in the dataset.
func (d *Dataset) Len() int { return len(d.fps) }

// Labels returns the label of every fingerprint in the dataset, in the order
// Match results are computed.
func (d *Dataset) Labels() []string {
	out := make([]string, len(d.fps))
	for i, fp := range d.fps {
		out[i] = fp.Meta.Label
	}
	return out
}

func (d *Dataset) add(fp *fingerprint.Fingerprint) error {
	proj, err := fp.Projected(d.scorer)
	if err != nil {
		return fmt.Errorf("scfingerprint: projecting %q: %w", fp.Meta.Label, err)
	}
	d.fps = append(d.fps, fp)
	d.projs = append(d.projs, proj)
	return nil
}

// newDatasetWithScorer builds an empty dataset on a caller-supplied scorer.
// Used by tests and by internal tooling that trains its own artifact.
func newDatasetWithScorer(scorer *scoring.Scorer) (*Dataset, error) {
	if scorer == nil {
		return NewDataset()
	}
	return &Dataset{scorer: scorer}, nil
}

// ModelIsSynthetic reports whether the model backing this dataset was trained
// on synthetic data. Scores from a synthetic model carry no real-world meaning
// and must not be presented as evidence.
func (d *Dataset) ModelIsSynthetic() bool { return d.scorer.IsSynthetic() }
