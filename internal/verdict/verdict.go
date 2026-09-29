// Package verdict turns calibrated scores into the opinionated confidence
// tiers every front end reports, so the CLI and the web page can never make
// different calls on the same numbers.
package verdict

import scfingerprint "github.com/marianogappa/scfingerprint"

// Verdict tiers, from weakest to strongest.
const (
	None   = "none"
	Weak   = "weak"
	Lead   = "lead"
	Strong = "strong"
)

// Confidence tiers, keyed on the false-positive rate a result achieves. For a
// 1:N search the rate is family-wise (Šidák-corrected for catalog size) and a
// decisive z margin over the runner-up can substitute for a strong rate — a
// real identification pulls away from the field. For a 1:1 comparison the
// rate is per-comparison, so the bars are one notch stricter.
const (
	FPRStrong    = 0.01 // 1:N, at or below: the evidence is strong
	FPRLead      = 0.10 // 1:N, at or below: worth following up
	MarginStrong = 1.5  // 1:N, z gap to the runner-up that makes a lead-grade rate strong

	FPRStrong1v1 = 0.001 // 1:1, at or below: the evidence is strong
	FPRLead1v1   = 0.01  // 1:1, at or below: worth following up

	MinGamesStrong = 3 // fewer games than this can never be strong
)

// Match makes the call for one player's result list, or None when nothing
// survived the minZ filter. Strong needs 3+ games and either a strong rate or
// a lead-grade rate with a decisive margin over the runner-up (when no
// runner-up survived the filter, the filter threshold is the margin's floor).
func Match(matches []scfingerprint.MatchResult, minZ float64) string {
	if len(matches) == 0 {
		return None
	}
	top := matches[0]
	margin := top.Z - minZ
	if len(matches) > 1 {
		margin = top.Z - matches[1].Z
	}
	switch {
	case top.SearchFPR > FPRLead:
		return Weak
	case top.EvidenceN < MinGamesStrong:
		return Lead
	// A player whose style is crowded scores respectably against strangers,
	// so a score that does not clear their own measured bar is a lead at
	// best however good the family-wise FPR looks.
	case !top.ClearsIdentityBar:
		return Lead
	case top.SearchFPR <= FPRStrong || margin >= MarginStrong:
		return Strong
	default:
		return Lead
	}
}

// Same makes the call for a 1:1 comparison, on the stricter per-comparison
// bars: strong needs both a 1 in 1,000 grade rate and 3+ games.
func Same(fpr float64, evidenceN int) string {
	switch {
	case fpr > FPRLead1v1:
		return Weak
	case evidenceN < MinGamesStrong:
		return Lead
	case fpr <= FPRStrong1v1:
		return Strong
	default:
		return Lead
	}
}
