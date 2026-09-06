package hygiene

import (
	"fmt"
	"slices"
)

// IdentityAccounts is the account-level evidence for one catalog entry: which
// Battle.net accounts the identity map attributes to its name, and which
// account its enrolment replays actually came from.
//
// Both feed gates that no behavioural check can see. Self-consistency and the
// duplicate scan compare how people play; these compare who the games belonged
// to, which catches a class of enrolment bug that scores perfectly consistent
// because it genuinely is one account's games under two names, or another
// account's games slipped in under one name.
type IdentityAccounts struct {
	// Label is the catalog entry this evidence belongs to.
	Label string

	// ByName are the aurora ids the identity map attributes to Label. A pro
	// legitimately owns several accounts, so more than one is normal here and
	// is never a finding on its own.
	ByName []int64

	// ByReplays is the account the enrolment was built from: the one
	// appearing in the most of its replays. More than one means a tie, which
	// the metadata cannot resolve.
	//
	// Per-replay metadata names both players and does not say which side the
	// entry played, so no single replay identifies the account — only the
	// fact that a player is in all of their own games does.
	ByReplays []int64

	// ReplayCount is how many manifest replays had resolvable metadata, and
	// ReplaysOnAccount how many of those ByReplays appears in. They are equal
	// in a clean enrolment; a gap is a replay that came from somewhere else.
	ReplayCount      int
	ReplaysOnAccount int
}

// VerifyAccounts reports account-level enrolment problems across the catalog.
//
// Four kinds, none of them visible to the behavioural gates:
//
//   - account_shared: two entries whose names the identity map resolves to the
//     same Battle.net account. Either one player is enrolled twice under two
//     names, or the map has attributed one account to two people.
//   - enrollment_shared: two entries built from the same account's games.
//     Stronger than the above, because the enrolment data says so rather than
//     a name lookup.
//   - enrollment_outliers: some of an entry's replays are not its account's.
//     Legitimate when a player used an alt for a few games, contamination
//     otherwise, so the count is reported rather than a conclusion.
//   - enrollment_ambiguous: two accounts appear in equally many of the entry's
//     replays, so its own account cannot be told from its most frequent
//     opponent's. Usually a very small manifest.
//
// The identity map is a name lookup and settles nothing on its own, so every
// message says what to go and check instead of asserting a conclusion.
func VerifyAccounts(entries []IdentityAccounts) []Finding {
	var findings []Finding

	findings = append(findings, sharedAccountFindings(entries,
		func(e IdentityAccounts) []int64 { return e.ByName },
		"account_shared",
		"the identity map resolves both names to Battle.net account %d — one player enrolled twice, or one account attributed to two people")...)

	findings = append(findings, sharedAccountFindings(entries,
		func(e IdentityAccounts) []int64 { return e.ByReplays },
		"enrollment_shared",
		"both enrolments were built from Battle.net account %d — the same account's games under two names")...)

	for _, e := range entries {
		if e.ReplayCount == 0 {
			continue // no metadata to judge it with
		}
		ids := distinct(e.ByReplays)
		switch {
		case len(ids) > 1:
			findings = append(findings, Finding{
				Kind:   "enrollment_ambiguous",
				Labels: []string{e.Label},
				Message: fmt.Sprintf("accounts %v each appear in %d of %d enrolment replays — cannot tell the entry's own account from its opponent's",
					ids, e.ReplaysOnAccount, e.ReplayCount),
			})
		case len(ids) == 1 && e.ReplaysOnAccount < e.ReplayCount:
			findings = append(findings, Finding{
				Kind:   "enrollment_outliers",
				Labels: []string{e.Label},
				Message: fmt.Sprintf("account %d is behind %d of %d enrolment replays; the other %d came from a different account — an alt, or someone else's games",
					ids[0], e.ReplaysOnAccount, e.ReplayCount, e.ReplayCount-e.ReplaysOnAccount),
			})
		}
	}
	return findings
}

// sharedAccountFindings reports every pair of entries that share an aurora id
// under the given accessor, one finding per (pair, shared account).
func sharedAccountFindings(entries []IdentityAccounts, accounts func(IdentityAccounts) []int64, kind, msg string) []Finding {
	// aurora id → the labels claiming it, in input order.
	claims := map[int64][]string{}
	for _, e := range entries {
		for _, id := range distinct(accounts(e)) {
			claims[id] = append(claims[id], e.Label)
		}
	}

	ids := make([]int64, 0, len(claims))
	for id, labels := range claims {
		if len(labels) > 1 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	var findings []Finding
	for _, id := range ids {
		labels := claims[id]
		for i := range labels {
			for j := i + 1; j < len(labels); j++ {
				findings = append(findings, Finding{
					Kind:    kind,
					Labels:  []string{labels[i], labels[j]},
					Message: fmt.Sprintf(msg, id),
				})
			}
		}
	}
	return findings
}

// distinct returns the unique ids, sorted, dropping zeros — an aurora id of 0
// is the web-api's "no such account", never a real one.
func distinct(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// DominantAccount picks the account behind an enrolment out of its replays'
// both-player account sets: the one appearing in the most of them, or every
// account tied for the most when there is no clear winner.
//
// It also reports how many replays carried usable metadata, so the gap between
// that and the winner's count is visible as the outlier count it is.
func DominantAccount(perReplay [][]int64) (dominant []int64, replays, onAccount int) {
	counts := map[int64]int{}
	for _, accounts := range perReplay {
		ids := distinct(accounts)
		if len(ids) == 0 {
			continue
		}
		replays++
		for _, id := range ids {
			counts[id]++
		}
	}
	for id, n := range counts {
		switch {
		case n > onAccount:
			onAccount, dominant = n, []int64{id}
		case n == onAccount:
			dominant = append(dominant, id)
		}
	}
	slices.Sort(dominant)
	return dominant, replays, onAccount
}
