package scfingerprint

import (
	"fmt"

	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/scoring"
)

// Dataset is a collection of known fingerprints that Match and MatchMany
// search against. Build one with NewDataset and populate it with Add.
type Dataset struct {
	scorer *scoring.Scorer
	fps    []*fingerprint.Fingerprint
	projs  [][]float64 // cached projected embeddings, parallel to fps
}

// NewDataset creates an empty dataset backed by the given scorer. Pass nil to
// use the compiled-in embedded model.
func NewDataset(scorer *scoring.Scorer) (*Dataset, error) {
	if scorer == nil {
		var err error
		scorer, err = scoring.NewFromEmbedded()
		if err != nil {
			return nil, fmt.Errorf("scfingerprint: loading embedded model: %w", err)
		}
	}
	return &Dataset{scorer: scorer}, nil
}

// Add registers a fingerprint in the dataset, pre-computing and caching its
// projected embedding for fast comparison.
func (d *Dataset) Add(fp *fingerprint.Fingerprint) error {
	proj, err := fp.Projected(d.scorer)
	if err != nil {
		return fmt.Errorf("scfingerprint: projecting %q: %w", fp.Meta.Label, err)
	}
	d.fps = append(d.fps, fp)
	d.projs = append(d.projs, proj)
	return nil
}

// Len returns the number of fingerprints in the dataset.
func (d *Dataset) Len() int { return len(d.fps) }

// Scorer returns the dataset's underlying scorer for callers that need direct
// access (e.g. Enroll's self-consistency check).
func (d *Dataset) Scorer() *scoring.Scorer { return d.scorer }
