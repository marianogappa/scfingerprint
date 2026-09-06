package main

import (
	"testing"

	"github.com/marianogappa/scfingerprint"
)

func TestRivalToonPicksTheOneAccountWeDoNotOwn(t *testing.T) {
	own := map[string]bool{"queennnnnn": true, "wooni222": true}

	// The normal case: a 1v1 practice game against somebody else.
	if got := rivalToon([]string{"kimsabuho", "Queennnnnn"}, own); got != "kimsabuho" {
		t.Fatalf("rivalToon = %q, want kimsabuho", got)
	}
	// Slot order must not matter, and matching is case-insensitive because
	// the roster string preserves whatever case the player typed.
	if got := rivalToon([]string{"QUEENNNNNN", "kimsabuho"}, own); got != "kimsabuho" {
		t.Fatalf("rivalToon = %q, want kimsabuho", got)
	}

	// A pro practising against their own alt yields nothing to discover.
	if got := rivalToon([]string{"Queennnnnn", "wooni222"}, own); got != "" {
		t.Fatalf("rivalToon on two owned toons = %q, want empty", got)
	}
	// A game the anchor is not in at all is not ours to reason about: we
	// cannot tell which of the two strangers is which.
	if got := rivalToon([]string{"strangerA", "strangerB"}, own); got != "" {
		t.Fatalf("rivalToon with no owned slot = %q, want empty", got)
	}
	// Anything that is not a clean two-slot roster is skipped rather than
	// guessed at — a wrong guess writes a wrong name into shipped data.
	for _, names := range [][]string{nil, {"Queennnnnn"}, {"Queennnnnn", "a", "b"}} {
		if got := rivalToon(names, own); got != "" {
			t.Fatalf("rivalToon(%v) = %q, want empty", names, got)
		}
	}
}

func TestConfidentNeedsBothALeadGradeRateAndAMargin(t *testing.T) {
	cfg := config{maxFPR: discoverMaxFPR, minMargin: discoverMinMargin}

	// Nothing survived the match: nothing to attribute.
	if confident(nil, cfg) {
		t.Fatal("confident with no results")
	}
	// A hit that clears no operating point tightly enough is a lead at best,
	// and this decision writes into shipped data.
	if confident([]scfingerprint.MatchResult{{Z: 9, SearchFPR: 0.5}}, cfg) {
		t.Fatal("confident on a loose family-wise FPR")
	}
	// A tight FPR with no runner-up to compare against is enough.
	if !confident([]scfingerprint.MatchResult{{Z: 9, SearchFPR: 0.001}}, cfg) {
		t.Fatal("not confident on a lone tight hit")
	}
	// A tight FPR whose runner-up is right behind it is ambiguous: two
	// catalog players look alike here, so pick neither.
	crowded := []scfingerprint.MatchResult{{Z: 9, SearchFPR: 0.001}, {Z: 8.5}}
	if confident(crowded, cfg) {
		t.Fatal("confident despite a 0.5 margin over the runner-up")
	}
	// The loop only ever has one game, so it cannot reach the 3-game evidence
	// the CLI needs to call something strong; the gate is the lead-grade rate
	// plus a decisive margin, which is the same trade the CLI makes.
	if discoverMaxFPR != 0.10 || discoverMinMargin != 1.5 {
		t.Fatalf("discovery gate drifted from the CLI's lead bar: fpr %v, margin %v", discoverMaxFPR, discoverMinMargin)
	}
	if !confident([]scfingerprint.MatchResult{{Z: 9, SearchFPR: 0.07}, {Z: 4}}, cfg) {
		t.Fatal("a lead-grade rate with a decisive margin should be enough")
	}
	decisive := []scfingerprint.MatchResult{{Z: 9, SearchFPR: 0.001}, {Z: 4}}
	if !confident(decisive, cfg) {
		t.Fatal("not confident despite a 5.0 margin over the runner-up")
	}
}

func TestSanitizeFilenameKeepsGameKeysUsable(t *testing.T) {
	// Ladder keys and numeric private-game ids are both already safe; the
	// point is that nothing else can escape into a path.
	if got := sanitizeFilename("MM-00096C84-8EB6-11F1"); got != "MM-00096C84-8EB6-11F1" {
		t.Fatalf("sanitizeFilename = %q", got)
	}
	if got := sanitizeFilename("1713934189"); got != "1713934189" {
		t.Fatalf("sanitizeFilename = %q", got)
	}
	if got := sanitizeFilename("../../etc/passwd"); got != "______etc_passwd" {
		t.Fatalf("sanitizeFilename = %q, want the separators neutralised", got)
	}
	if got := sanitizeFilename(""); got == "" {
		t.Fatal("sanitizeFilename returned an empty filename")
	}
}

func TestOtherAuroraPicksTheRival(t *testing.T) {
	// In a 1v1 the account that is not the anchor's is the rival's.
	if got := otherAurora([]int64{14926205, 18242965}, 18242965); got != 14926205 {
		t.Fatalf("otherAurora = %d, want 14926205", got)
	}
	if got := otherAurora([]int64{18242965, 14926205}, 18242965); got != 14926205 {
		t.Fatalf("otherAurora = %d, want 14926205", got)
	}
	// Only our own upload survived, so the game names no rival account.
	if got := otherAurora([]int64{18242965}, 18242965); got != 0 {
		t.Fatalf("otherAurora with only our own id = %d, want 0", got)
	}
	if got := otherAurora(nil, 18242965); got != 0 {
		t.Fatalf("otherAurora(nil) = %d, want 0", got)
	}
	// Zero is "no account", never a rival.
	if got := otherAurora([]int64{0}, 18242965); got != 0 {
		t.Fatalf("otherAurora treated 0 as a rival: %d", got)
	}
}
