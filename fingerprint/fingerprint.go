// Package fingerprint defines the enrollable, storable identity object: an
// aggregate over many games of one player's feature vectors. Fingerprints
// update incrementally (Add never needs the raw history), serialize to a
// single versioned JSON string for one-column persistence, and can audit
// themselves for wrongly-merged identities via SelfConsistency.
//
// Feature semantics are version-append-only: a future feature version may add
// features but never redefines existing ones, so old blobs stay interpretable
// against the manifest reported by features.FeatureNames. Replays remain the
// source of truth — a fingerprint can always be recomputed to the newest
// version by re-extracting its games.
package fingerprint

import (
	"errors"
	"fmt"
	"math"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/scoring"
)

// ErrVersionMismatch is returned (wrapped, with detail) when a fingerprint's
// feature version is not usable by the running code or a given scorer.
var ErrVersionMismatch = errors.New("fingerprint: feature version mismatch")

const (
	// maxBlocks bounds the chronological block means kept for
	// SelfConsistency; when exceeded, adjacent blocks merge pairwise.
	maxBlocks = 8

	// MinRaceSubMeanGames is the minimum number of games of a race before
	// its per-race sub-mean is included in the serialized form.
	MinRaceSubMeanGames = 10

	// MinSelfConsistencyGames is the minimum game count for a meaningful
	// self-consistency estimate.
	MinSelfConsistencyGames = 4
)

// Meta is curator-provided provenance for an enrollment.
type Meta struct {
	Label      string `json:"label,omitempty"`      // display identity, e.g. "C9_FlaSh"
	Source     string `json:"source,omitempty"`     // corpus the games came from
	DateFrom   string `json:"date_from,omitempty"`  // earliest game date (YYYY-MM-DD)
	DateTo     string `json:"date_to,omitempty"`    // latest game date (YYYY-MM-DD)
	Confidence string `json:"confidence,omitempty"` // curator confidence tier
}

// block is a running mean over a consecutive run of contributed games.
type block struct {
	n    int
	mean []float64
}

// projCache is a projected embedding (post standardize/select/whiten) of the
// raw mean, tagged with the model that produced it. A model bump invalidates
// only this cache, never the durable raw mean.
type projCache struct {
	modelTag string
	vec      []float64
}

// Fingerprint is a running aggregate of one identity's per-game feature
// vectors. The zero value is not usable; construct with New or Parse.
type Fingerprint struct {
	Meta Meta

	version int
	dims    int
	n       int
	mean    []float64

	raceCounts map[string]int
	raceMeans  map[string][]float64

	blocks   []block
	blockCap int

	proj *projCache
}

// New creates an empty fingerprint for the current feature version.
func New(meta Meta) *Fingerprint {
	names, _ := features.FeatureNames(features.Version)
	return &Fingerprint{
		Meta:       meta,
		version:    features.Version,
		dims:       len(names),
		mean:       make([]float64, len(names)),
		raceCounts: map[string]int{},
		raceMeans:  map[string][]float64{},
		blockCap:   1,
	}
}

// Add folds one game's raw feature vector into the fingerprint. race may be
// empty when unknown. No raw vectors are retained. Any cached projection is
// invalidated.
func (fp *Fingerprint) Add(vector []float64, race string) error {
	if len(vector) != fp.dims {
		return fmt.Errorf("fingerprint: vector has %d dims, version %d expects %d", len(vector), fp.version, fp.dims)
	}
	fp.n++
	updateMean(fp.mean, vector, fp.n)

	if race != "" {
		fp.raceCounts[race]++
		rm, ok := fp.raceMeans[race]
		if !ok {
			rm = make([]float64, fp.dims)
			fp.raceMeans[race] = rm
		}
		updateMean(rm, vector, fp.raceCounts[race])
	}

	fp.addToBlocks(vector)
	fp.proj = nil
	return nil
}

// updateMean folds x into a running mean that now covers n samples.
func updateMean(mean, x []float64, n int) {
	inv := 1 / float64(n)
	for j := range mean {
		mean[j] += (x[j] - mean[j]) * inv
	}
}

// addToBlocks appends the vector to the newest chronological block, growing
// and compacting the block list so it never exceeds maxBlocks. Blocks retain
// exact means over consecutive game runs, which is what SelfConsistency
// splits into early/late halves.
func (fp *Fingerprint) addToBlocks(vector []float64) {
	if len(fp.blocks) == 0 || fp.blocks[len(fp.blocks)-1].n >= fp.blockCap {
		if len(fp.blocks) == maxBlocks {
			// Merge adjacent pairs: 8 blocks -> 4, doubling capacity.
			merged := make([]block, 0, maxBlocks/2)
			for i := 0; i < len(fp.blocks); i += 2 {
				merged = append(merged, mergeBlocks(fp.blocks[i], fp.blocks[i+1]))
			}
			fp.blocks = merged
			fp.blockCap *= 2
		}
		fp.blocks = append(fp.blocks, block{mean: make([]float64, fp.dims)})
	}
	b := &fp.blocks[len(fp.blocks)-1]
	b.n++
	updateMean(b.mean, vector, b.n)
}

func mergeBlocks(a, b block) block {
	out := block{n: a.n + b.n, mean: make([]float64, len(a.mean))}
	wa, wb := float64(a.n)/float64(out.n), float64(b.n)/float64(out.n)
	for j := range out.mean {
		out.mean[j] = a.mean[j]*wa + b.mean[j]*wb
	}
	return out
}

// N returns the number of games aggregated into this fingerprint.
func (fp *Fingerprint) N() int { return fp.n }

// Version returns the feature schema version of the aggregated vectors.
func (fp *Fingerprint) Version() int { return fp.version }

// Mean returns a copy of the durable raw feature mean.
func (fp *Fingerprint) Mean() []float64 {
	out := make([]float64, len(fp.mean))
	copy(out, fp.mean)
	return out
}

// RaceCounts returns a copy of the per-race game counts.
func (fp *Fingerprint) RaceCounts() map[string]int {
	out := make(map[string]int, len(fp.raceCounts))
	for r, c := range fp.raceCounts {
		out[r] = c
	}
	return out
}

// RaceMean returns a copy of the per-race sub-mean and its game count, or
// ok=false if the race has no games.
func (fp *Fingerprint) RaceMean(race string) (mean []float64, n int, ok bool) {
	rm, exists := fp.raceMeans[race]
	if !exists {
		return nil, 0, false
	}
	out := make([]float64, len(rm))
	copy(out, rm)
	return out, fp.raceCounts[race], true
}

// Projected returns the fingerprint's embedding in the scorer's whitened
// space — the representation hot-path comparison uses (cosine over these is
// a couple of dot products). The result is cached and tagged with the model;
// a different model or a subsequent Add recomputes it.
func (fp *Fingerprint) Projected(s *scoring.Scorer) ([]float64, error) {
	if err := fp.checkScorer(s); err != nil {
		return nil, err
	}
	if fp.n == 0 {
		return nil, fmt.Errorf("fingerprint: empty fingerprint has no projection")
	}
	tag := s.ModelTag()
	if fp.proj == nil || fp.proj.modelTag != tag {
		vec, err := s.Transform(fp.mean)
		if err != nil {
			return nil, err
		}
		fp.proj = &projCache{modelTag: tag, vec: vec}
	}
	out := make([]float64, len(fp.proj.vec))
	copy(out, fp.proj.vec)
	return out, nil
}

// SelfConsistency splits the contributing games into chronological halves,
// projects both half-centroids into the scorer's whitened space, and returns
// their cosine similarity. Genuine single-person enrollments score high
// (~0.96 in the validated spike); a wrongly-merged two-person enrollment
// scored 0.44 and produced an entire false-positive tail. Enrollments below
// a threshold should be flagged or rejected (catalog hygiene, #9).
func (fp *Fingerprint) SelfConsistency(s *scoring.Scorer) (float64, error) {
	if err := fp.checkScorer(s); err != nil {
		return 0, err
	}
	if fp.n < MinSelfConsistencyGames || len(fp.blocks) < 2 {
		return 0, fmt.Errorf("fingerprint: need at least %d games across 2 chronological blocks, have %d games in %d blocks", MinSelfConsistencyGames, fp.n, len(fp.blocks))
	}

	// Split blocks where the cumulative game count first reaches half.
	split, cum := 0, 0
	for i, b := range fp.blocks {
		cum += b.n
		if cum*2 >= fp.n {
			split = i + 1
			break
		}
	}
	if split >= len(fp.blocks) {
		split = len(fp.blocks) - 1
	}

	early := centroid(fp.blocks[:split], fp.dims)
	late := centroid(fp.blocks[split:], fp.dims)

	we, err := s.Transform(early)
	if err != nil {
		return 0, err
	}
	wl, err := s.Transform(late)
	if err != nil {
		return 0, err
	}
	return cosine(we, wl), nil
}

func (fp *Fingerprint) checkScorer(s *scoring.Scorer) error {
	if s.FeatureVersion() != fp.version {
		return fmt.Errorf("%w: fingerprint v%d, scorer expects v%d", ErrVersionMismatch, fp.version, s.FeatureVersion())
	}
	return nil
}

func centroid(blocks []block, dims int) []float64 {
	out := make([]float64, dims)
	total := 0
	for _, b := range blocks {
		total += b.n
	}
	for _, b := range blocks {
		w := float64(b.n) / float64(total)
		for j := range out {
			out[j] += b.mean[j] * w
		}
	}
	return out
}

func cosine(a, b []float64) float64 {
	dot, na, nb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
