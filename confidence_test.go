package scfingerprint

import (
	"testing"

	"github.com/marianogappa/scfingerprint/internal/dataset"
)

// The public tier names are spelled out rather than aliased so godoc never
// names an internal package; this keeps them honest.
func TestConfidenceTiersMatchDataset(t *testing.T) {
	for _, c := range []struct{ public, internal string }{
		{ConfidenceConfirmed, dataset.ConfidenceConfirmed},
		{ConfidenceHigh, dataset.ConfidenceHigh},
		{ConfidenceCandidate, dataset.ConfidenceCandidate},
	} {
		if c.public != c.internal {
			t.Errorf("tier drift: public %q != dataset %q", c.public, c.internal)
		}
	}
}
