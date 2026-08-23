package scfingerprint

import "github.com/marianogappa/scfingerprint/internal/hygiene"

// CoOccurrence disproves alias claims from replay attendance: two names that
// played in the same game cannot be the same human. It is the cheapest and
// most decisive check available, so run it before trusting any match.
type CoOccurrence struct {
	inner *hygiene.CoOccurrence
}

// NewCoOccurrence builds the index from a manifest of replay identifier to the
// names present in that replay. Callers build the manifest from their own
// replay store; no database access happens here.
func NewCoOccurrence(manifest map[string][]string) *CoOccurrence {
	return &CoOccurrence{inner: hygiene.BuildCoOccurrence(manifest)}
}

// Disproved reports whether a and b ever appeared in the same game, which
// rules them out as aliases of one another.
func (c *CoOccurrence) Disproved(a, b string) bool {
	return c.inner.Disproved(a, b)
}
