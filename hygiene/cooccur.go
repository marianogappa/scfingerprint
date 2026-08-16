package hygiene

import "github.com/marianogappa/scfingerprint/training"

// CoOccurrence indexes which player names appeared together in a replay.
// Two names playing in the same game CANNOT be the same person — a cheap,
// definitive disproof. It only ever disproves: absence of co-occurrence is
// weak supporting evidence, never proof of identity.
type CoOccurrence struct {
	together map[[2]string]bool
}

// BuildCoOccurrence indexes a replay manifest: replay identifier → the player
// names that appeared in it.
func BuildCoOccurrence(manifest map[string][]string) *CoOccurrence {
	c := &CoOccurrence{together: map[[2]string]bool{}}
	for _, names := range manifest {
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				if names[i] == names[j] {
					continue
				}
				c.together[[2]string{names[i], names[j]}] = true
				c.together[[2]string{names[j], names[i]}] = true
			}
		}
	}
	return c
}

// ManifestFromSamples builds a replay manifest from labeled corpus samples.
func ManifestFromSamples(samples []training.Sample) map[string][]string {
	manifest := map[string][]string{}
	for _, s := range samples {
		manifest[s.File] = append(manifest[s.File], s.Player)
	}
	return manifest
}

// Disproved reports whether the two names ever played in the same replay,
// which disproves them being the same person. Empty names never disprove.
func (c *CoOccurrence) Disproved(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return c.together[[2]string{a, b}]
}

// DisprovedPairs filters proposed same-person pairs down to those the replay
// manifest disproves.
func (c *CoOccurrence) DisprovedPairs(pairs [][2]string) [][2]string {
	var out [][2]string
	for _, p := range pairs {
		if c.Disproved(p[0], p[1]) {
			out = append(out, p)
		}
	}
	return out
}
