package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadExclusions reads a known-smurf exclusion manifest: a JSON array of
// two-element player-name arrays, e.g. [["MBU_Shine","wG_Shine"]]. Pairs
// listed here are the same person under different names, so they are removed
// from impostor pools (a high cross-score between them is a true positive,
// not a false one).
func LoadExclusions(path string) ([][2]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: reading exclusion manifest %s: %w", path, err)
	}
	var raw [][]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("eval: parsing exclusion manifest %s: %w", path, err)
	}
	pairs := make([][2]string, 0, len(raw))
	for i, p := range raw {
		if len(p) != 2 || p[0] == "" || p[1] == "" {
			return nil, fmt.Errorf("eval: exclusion manifest %s entry %d: want exactly two non-empty names, got %v", path, i, p)
		}
		pairs = append(pairs, [2]string{p[0], p[1]})
	}
	return pairs, nil
}
