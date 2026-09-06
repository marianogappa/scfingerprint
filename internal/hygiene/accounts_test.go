package hygiene

import (
	"strings"
	"testing"
)

func kinds(findings []Finding) map[string]int {
	out := map[string]int{}
	for _, f := range findings {
		out[f.Kind]++
	}
	return out
}

func TestVerifyAccountsCleanCatalog(t *testing.T) {
	// Two players, distinct accounts, each enrolled from its own account:
	// nothing to report. A pro owning several accounts by NAME is normal and
	// must not be flagged on its own.
	findings := VerifyAccounts([]IdentityAccounts{
		{Label: "Queen", ByName: []int64{1, 2}, ByReplays: []int64{1}, ReplayCount: 30, ReplaysOnAccount: 30},
		{Label: "Larva", ByName: []int64{3}, ByReplays: []int64{3}, ReplayCount: 30, ReplaysOnAccount: 30},
	})
	if len(findings) != 0 {
		t.Fatalf("clean catalog produced %d findings: %+v", len(findings), findings)
	}
}

func TestVerifyAccountsCatchesTwoEntriesOnOneAccount(t *testing.T) {
	// The bug the issue calls out: two catalog entries that are really the
	// same Battle.net account.
	findings := VerifyAccounts([]IdentityAccounts{
		{Label: "Queen", ByName: []int64{7}, ByReplays: []int64{7}, ReplayCount: 30, ReplaysOnAccount: 30},
		{Label: "QueenSmurf", ByName: []int64{7}, ByReplays: []int64{7}, ReplayCount: 30, ReplaysOnAccount: 30},
	})
	got := kinds(findings)
	if got["account_shared"] != 1 || got["enrollment_shared"] != 1 {
		t.Fatalf("kinds = %v, want one of each shared kind: %+v", got, findings)
	}
	for _, f := range findings {
		if len(f.Labels) != 2 || !strings.Contains(f.Message, "7") {
			t.Fatalf("finding does not name both entries and the account: %+v", f)
		}
	}
}

func TestVerifyAccountsCatchesReplaysFromAnotherAccount(t *testing.T) {
	// The other half of the issue's ask: an enrolment whose games did not all
	// come from one account. Reporting HOW MANY strays there are is the point
	// — two of forty is an alt, twenty of forty is two people.
	findings := VerifyAccounts([]IdentityAccounts{
		{Label: "Mixed", ByName: []int64{5}, ByReplays: []int64{5}, ReplayCount: 40, ReplaysOnAccount: 38},
	})
	got := kinds(findings)
	if got["enrollment_outliers"] != 1 {
		t.Fatalf("kinds = %v, want one enrollment_outliers: %+v", got, findings)
	}
	for _, want := range []string{"38 of 40", "other 2", "an alt"} {
		if !strings.Contains(findings[0].Message, want) {
			t.Fatalf("message is missing %q: %q", want, findings[0].Message)
		}
	}

	// A clean enrolment says nothing.
	if got := VerifyAccounts([]IdentityAccounts{
		{Label: "Clean", ByReplays: []int64{5}, ReplayCount: 40, ReplaysOnAccount: 40},
	}); len(got) != 0 {
		t.Fatalf("a clean enrolment produced findings: %+v", got)
	}

	// No resolvable metadata is not a finding — it is an absence of evidence.
	if got := VerifyAccounts([]IdentityAccounts{{Label: "Unknown", ByName: []int64{5}}}); len(got) != 0 {
		t.Fatalf("an entry with no replay metadata produced findings: %+v", got)
	}

	// A tie between two accounts is a different problem: we cannot tell the
	// entry's own account from its opponent's.
	got = kinds(VerifyAccounts([]IdentityAccounts{
		{Label: "Pair", ByReplays: []int64{5, 9}, ReplayCount: 3, ReplaysOnAccount: 3},
	}))
	if got["enrollment_ambiguous"] != 1 || got["enrollment_outliers"] != 0 {
		t.Fatalf("kinds = %v, want one enrollment_ambiguous", got)
	}
}

func TestDominantAccountFindsTheEnrolmentsOwnAccount(t *testing.T) {
	// Every game names two players, so the entry's own account is the one
	// that keeps recurring while the opponents change.
	dominant, replays, on := DominantAccount([][]int64{
		{7, 100}, {7, 101}, {7, 102}, {7, 103},
	})
	if len(dominant) != 1 || dominant[0] != 7 || replays != 4 || on != 4 {
		t.Fatalf("DominantAccount = %v, %d, %d; want [7], 4, 4", dominant, replays, on)
	}

	// One stray replay must not hide the account — it must show up as the
	// outlier count instead.
	dominant, replays, on = DominantAccount([][]int64{
		{7, 100}, {7, 101}, {7, 102}, {55, 56},
	})
	if len(dominant) != 1 || dominant[0] != 7 || replays != 4 || on != 3 {
		t.Fatalf("DominantAccount = %v, %d, %d; want [7], 4, 3", dominant, replays, on)
	}

	// Two players who only ever played each other are a genuine tie.
	dominant, _, _ = DominantAccount([][]int64{{7, 8}, {8, 7}})
	if len(dominant) != 2 {
		t.Fatalf("DominantAccount = %v, want a two-way tie", dominant)
	}

	// Aurora 0 is "no such account" and replays with no usable metadata do
	// not count towards the total.
	dominant, replays, on = DominantAccount([][]int64{{0, 0}, nil, {7, 0}})
	if len(dominant) != 1 || dominant[0] != 7 || replays != 1 || on != 1 {
		t.Fatalf("DominantAccount = %v, %d, %d; want [7], 1, 1", dominant, replays, on)
	}

	if dominant, replays, on := DominantAccount(nil); dominant != nil || replays != 0 || on != 0 {
		t.Fatalf("DominantAccount(nil) = %v, %d, %d", dominant, replays, on)
	}
}

func TestVerifyAccountsIgnoresAuroraZeroAndDuplicates(t *testing.T) {
	// Aurora 0 is the web-api's "no such account", so two entries both
	// carrying 0 are not two entries sharing an account.
	findings := VerifyAccounts([]IdentityAccounts{
		{Label: "A", ByName: []int64{0}, ByReplays: []int64{0, 4}, ReplayCount: 30, ReplaysOnAccount: 30},
		{Label: "B", ByName: []int64{0}, ByReplays: []int64{0, 5}, ReplayCount: 30, ReplaysOnAccount: 30},
	})
	if len(findings) != 0 {
		t.Fatalf("aurora 0 was treated as a real account: %+v", findings)
	}

	// A repeated id within one entry is one account, not two.
	findings = VerifyAccounts([]IdentityAccounts{{Label: "A", ByReplays: []int64{4, 4, 4}, ReplayCount: 30, ReplaysOnAccount: 30}})
	if len(findings) != 0 {
		t.Fatalf("a repeated aurora id read as more than one account: %+v", findings)
	}
}

func TestVerifyAccountsReportsEveryPairOnAThreeWayCollision(t *testing.T) {
	// Three entries on one account is three problems to fix, not one.
	findings := VerifyAccounts([]IdentityAccounts{
		{Label: "A", ByName: []int64{1}},
		{Label: "B", ByName: []int64{1}},
		{Label: "C", ByName: []int64{1}},
	})
	if n := kinds(findings)["account_shared"]; n != 3 {
		t.Fatalf("three-way collision produced %d pairs, want 3: %+v", n, findings)
	}
}

func TestVerifyAccountsIsDeterministic(t *testing.T) {
	// Findings are read by CI and by humans diffing runs, so the order must
	// not depend on map iteration.
	entries := []IdentityAccounts{
		{Label: "A", ByName: []int64{9, 3}, ByReplays: []int64{9}, ReplayCount: 20, ReplaysOnAccount: 20},
		{Label: "B", ByName: []int64{3}, ByReplays: []int64{9}, ReplayCount: 20, ReplaysOnAccount: 20},
		{Label: "C", ByName: []int64{9}},
	}
	first := VerifyAccounts(entries)
	for range 20 {
		again := VerifyAccounts(entries)
		if len(again) != len(first) {
			t.Fatalf("finding count varies: %d vs %d", len(again), len(first))
		}
		for i := range first {
			if again[i].Kind != first[i].Kind || again[i].Message != first[i].Message ||
				again[i].Labels[0] != first[i].Labels[0] {
				t.Fatalf("finding %d varies between runs: %+v vs %+v", i, again[i], first[i])
			}
		}
	}
}
