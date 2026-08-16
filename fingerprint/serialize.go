package fingerprint

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"github.com/marianogappa/scfingerprint/features"
)

// wire is the versioned JSON schema. Everything an enrollment is survives in
// one string: the durable raw mean (and per-race sub-means where a race has
// enough games), chronological block means for self-consistency, an optional
// model-tagged projection cache, and provenance metadata.
//
// Vector payloads are base64-encoded little-endian float32 arrays: the JSON
// keys keep the blob self-describing, but nobody reviews kilobytes of decimal
// floats, and float32's ~7 significant digits sit far above the noise floor
// of the features. Race keys are single-letter codes ("z", "t", "p", "r").
type wire struct {
	V         int               `json:"v"`
	N         int               `json:"n"`
	Races     map[string]int    `json:"races,omitempty"`
	Mean      string            `json:"mean"`
	RaceMeans map[string]string `json:"race_means,omitempty"`
	Blocks    []wireBlock       `json:"blocks,omitempty"`
	Proj      *wireProj         `json:"proj,omitempty"`
	Meta      *Meta             `json:"meta,omitempty"`
}

type wireBlock struct {
	N    int    `json:"n"`
	Mean string `json:"mean"`
}

type wireProj struct {
	Model string `json:"model"`
	Vec   string `json:"vec"`
}

// encodeVec packs a vector as base64 little-endian float32.
func encodeVec(xs []float64) string {
	buf := make([]byte, 4*len(xs))
	for i, x := range xs {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(float32(x)))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// decodeVec unpacks a base64 float32 vector, enforcing dimensionality and
// finiteness.
func decodeVec(name, s string, dims int) ([]float64, error) {
	buf, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("fingerprint: %s is not valid base64: %w", name, err)
	}
	if len(buf) != 4*dims {
		return nil, fmt.Errorf("fingerprint: %s has %d bytes, want %d (%d float32 dims)", name, len(buf), 4*dims, dims)
	}
	out := make([]float64, dims)
	for i := range out {
		v := math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("fingerprint: %s[%d] is %v", name, i, v)
		}
		out[i] = float64(v)
	}
	return out, nil
}

// MarshalString serializes the fingerprint to a single versioned JSON string,
// suitable for one DB text column. Per-race sub-means are included only for
// races with at least MinRaceSubMeanGames games.
func (fp *Fingerprint) MarshalString() (string, error) {
	w := wire{
		V:    fp.version,
		N:    fp.n,
		Mean: encodeVec(fp.mean),
	}
	if len(fp.raceCounts) > 0 {
		w.Races = fp.raceCounts
	}
	for race, count := range fp.raceCounts {
		if count >= MinRaceSubMeanGames {
			if w.RaceMeans == nil {
				w.RaceMeans = map[string]string{}
			}
			w.RaceMeans[race] = encodeVec(fp.raceMeans[race])
		}
	}
	for _, b := range fp.blocks {
		w.Blocks = append(w.Blocks, wireBlock{N: b.n, Mean: encodeVec(b.mean)})
	}
	if fp.proj != nil {
		w.Proj = &wireProj{Model: fp.proj.modelTag, Vec: encodeVec(fp.proj.vec)}
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
	// Check the version before decoding payloads strictly: a future-version
	// blob may change payload shapes and must report a version mismatch, not
	// a type error.
	var ver struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal([]byte(s), &ver); err != nil {
		return nil, fmt.Errorf("fingerprint: invalid JSON: %w", err)
	}
	names, err := features.FeatureNames(ver.V)
	if err != nil {
		return nil, fmt.Errorf("%w: blob has v%d", ErrVersionMismatch, ver.V)
	}
	dims := len(names)

	var w wire
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return nil, fmt.Errorf("fingerprint: invalid JSON: %w", err)
	}

	if w.N < 0 {
		return nil, fmt.Errorf("fingerprint: negative game count %d", w.N)
	}
	mean, err := decodeVec("mean", w.Mean, dims)
	if err != nil {
		return nil, err
	}

	fp := &Fingerprint{
		version:    w.V,
		dims:       dims,
		n:          w.N,
		mean:       mean,
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
	for race, encoded := range w.RaceMeans {
		if _, ok := fp.raceCounts[race]; !ok {
			return nil, fmt.Errorf("fingerprint: race_means has %q but races does not", race)
		}
		rm, err := decodeVec("race_means", encoded, dims)
		if err != nil {
			return nil, err
		}
		fp.raceMeans[race] = rm
	}

	blockTotal := 0
	for i, b := range w.Blocks {
		if b.N < 1 {
			return nil, fmt.Errorf("fingerprint: blocks[%d] has count %d", i, b.N)
		}
		bm, err := decodeVec("blocks", b.Mean, dims)
		if err != nil {
			return nil, err
		}
		fp.blocks = append(fp.blocks, block{n: b.N, mean: bm})
		if b.N > fp.blockCap {
			fp.blockCap = b.N
		}
		blockTotal += b.N
	}
	if len(w.Blocks) > 0 && blockTotal != w.N {
		return nil, fmt.Errorf("fingerprint: block counts sum to %d, n=%d", blockTotal, w.N)
	}

	if w.Proj != nil {
		// The projection's dimensionality is the model's K, unknown here;
		// derive it from the payload length and validate divisibility.
		buf, err := base64.StdEncoding.DecodeString(w.Proj.Vec)
		if err != nil || len(buf) == 0 || len(buf)%4 != 0 {
			return nil, fmt.Errorf("fingerprint: proj is not a valid float32 vector")
		}
		vec, err := decodeVec("proj", w.Proj.Vec, len(buf)/4)
		if err != nil {
			return nil, err
		}
		fp.proj = &projCache{modelTag: w.Proj.Model, vec: vec}
	}
	return fp, nil
}
