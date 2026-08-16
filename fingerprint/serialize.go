package fingerprint

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/marianogappa/scfingerprint/features"
)

// wire is the versioned JSON schema. Everything an enrollment is survives in
// one string: the durable raw mean (and per-race sub-means where a race has
// enough games), chronological block means for self-consistency, an optional
// model-tagged projection cache, and provenance metadata.
type wire struct {
	V         int                  `json:"v"`
	N         int                  `json:"n"`
	Races     map[string]int       `json:"races,omitempty"`
	Mean      []float64            `json:"mean"`
	RaceMeans map[string][]float64 `json:"race_means,omitempty"`
	Blocks    []wireBlock          `json:"blocks,omitempty"`
	Proj      *wireProj            `json:"proj,omitempty"`
	Meta      *Meta                `json:"meta,omitempty"`
}

type wireBlock struct {
	N    int       `json:"n"`
	Mean []float64 `json:"mean"`
}

type wireProj struct {
	Model string    `json:"model"`
	Vec   []float64 `json:"vec"`
}

// MarshalString serializes the fingerprint to a single versioned JSON string,
// suitable for one DB text column. Per-race sub-means are included only for
// races with at least MinRaceSubMeanGames games.
func (fp *Fingerprint) MarshalString() (string, error) {
	w := wire{
		V:    fp.version,
		N:    fp.n,
		Mean: fp.mean,
	}
	if len(fp.raceCounts) > 0 {
		w.Races = fp.raceCounts
	}
	for race, count := range fp.raceCounts {
		if count >= MinRaceSubMeanGames {
			if w.RaceMeans == nil {
				w.RaceMeans = map[string][]float64{}
			}
			w.RaceMeans[race] = fp.raceMeans[race]
		}
	}
	for _, b := range fp.blocks {
		w.Blocks = append(w.Blocks, wireBlock{N: b.n, Mean: b.mean})
	}
	if fp.proj != nil {
		w.Proj = &wireProj{Model: fp.proj.modelTag, Vec: fp.proj.vec}
	}
	if fp.Meta != (Meta{}) {
		m := fp.Meta
		w.Meta = &m
	}
	data, err := json.Marshal(w)
	if err != nil {
		return "", fmt.Errorf("fingerprint: marshaling: %w", err)
	}
	return string(data), nil
}

// Parse deserializes a fingerprint from its JSON string form. A blob with an
// unsupported feature version returns an error wrapping ErrVersionMismatch —
// the blob is not corrupt, it just needs code that speaks that version (or a
// recompute from replays).
func Parse(s string) (*Fingerprint, error) {
	var w wire
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return nil, fmt.Errorf("fingerprint: invalid JSON: %w", err)
	}
	names, err := features.FeatureNames(w.V)
	if err != nil {
		return nil, fmt.Errorf("%w: blob has v%d", ErrVersionMismatch, w.V)
	}
	dims := len(names)

	if w.N < 0 {
		return nil, fmt.Errorf("fingerprint: negative game count %d", w.N)
	}
	if len(w.Mean) != dims {
		return nil, fmt.Errorf("fingerprint: mean has %d dims, v%d expects %d", len(w.Mean), w.V, dims)
	}
	if err := checkFinite("mean", w.Mean); err != nil {
		return nil, err
	}

	fp := &Fingerprint{
		version:    w.V,
		dims:       dims,
		n:          w.N,
		mean:       w.Mean,
		raceCounts: map[string]int{},
		raceMeans:  map[string][]float64{},
		blockCap:   1,
	}
	if w.Meta != nil {
		fp.Meta = *w.Meta
	}

	raceTotal := 0
	for race, count := range w.Races {
		if count < 1 {
			return nil, fmt.Errorf("fingerprint: race %q has count %d", race, count)
		}
		fp.raceCounts[race] = count
		raceTotal += count
	}
	if raceTotal > w.N {
		return nil, fmt.Errorf("fingerprint: race counts sum to %d > n=%d", raceTotal, w.N)
	}
	for race, mean := range w.RaceMeans {
		if _, ok := fp.raceCounts[race]; !ok {
			return nil, fmt.Errorf("fingerprint: race_means has %q but races does not", race)
		}
		if len(mean) != dims {
			return nil, fmt.Errorf("fingerprint: race_means[%q] has %d dims, want %d", race, len(mean), dims)
		}
		if err := checkFinite("race_means", mean); err != nil {
			return nil, err
		}
		fp.raceMeans[race] = mean
	}

	blockTotal := 0
	for i, b := range w.Blocks {
		if b.N < 1 {
			return nil, fmt.Errorf("fingerprint: blocks[%d] has count %d", i, b.N)
		}
		if len(b.Mean) != dims {
			return nil, fmt.Errorf("fingerprint: blocks[%d] has %d dims, want %d", i, len(b.Mean), dims)
		}
		if err := checkFinite("blocks", b.Mean); err != nil {
			return nil, err
		}
		fp.blocks = append(fp.blocks, block{n: b.N, mean: b.Mean})
		if b.N > fp.blockCap {
			fp.blockCap = b.N
		}
		blockTotal += b.N
	}
	if len(w.Blocks) > 0 && blockTotal != w.N {
		return nil, fmt.Errorf("fingerprint: block counts sum to %d, n=%d", blockTotal, w.N)
	}

	if w.Proj != nil {
		if err := checkFinite("proj", w.Proj.Vec); err != nil {
			return nil, err
		}
		fp.proj = &projCache{modelTag: w.Proj.Model, vec: w.Proj.Vec}
	}
	return fp, nil
}

func checkFinite(name string, xs []float64) error {
	for i, v := range xs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("fingerprint: %s[%d] is %v", name, i, v)
		}
	}
	return nil
}
