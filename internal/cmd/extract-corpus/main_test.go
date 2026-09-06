package main

import (
	"sort"
	"testing"
)

// The harvest records a game twice when a player is picked up by more than one
// fetch pass. A duplicated row is a duplicated feature vector, so it
// double-weights that game in every mean computed downstream.
func TestDedupeKeepsOneRowPerReplayAndAccount(t *testing.T) {
	rows := []replayRow{
		{File: "replays/a.rep", AuroraID: 1, Toon: "first"},
		{File: "replays/a.rep", AuroraID: 2},
		{File: "replays/a.rep", AuroraID: 1, Toon: "duplicate"},
		{File: "replays/b.rep", AuroraID: 1},
	}

	out, dropped := dedupe(rows)

	if dropped != 1 {
		t.Errorf("dropped = %d, want 1", dropped)
	}
	if len(out) != 3 {
		t.Fatalf("kept %d rows, want 3", len(out))
	}
	if out[0].Toon != "first" {
		t.Errorf("kept the later duplicate (%q); the first occurrence should win", out[0].Toon)
	}
}

// Rows that tie on (player, start time) must still have a total order, or the
// chronological blocks a fingerprint serializes into vary run to run. Every
// non-ladder replay carries the same no-timestamp sentinel, so the ties are
// real and numerous.
func TestLessRowIsTotalOrderWhenTimestampsTie(t *testing.T) {
	const sentinel = "2106-02-07T06:28:15"
	tied := []csvRow{
		{player: "42", startTime: sentinel, file: "replays/localapi/c.rep"},
		{player: "42", startTime: sentinel, file: "replays/localapi/a.rep"},
		{player: "42", startTime: sentinel, file: "replays/localapi/b.rep"},
	}

	want := []string{"replays/localapi/a.rep", "replays/localapi/b.rep", "replays/localapi/c.rep"}
	for _, start := range [][]csvRow{tied, {tied[2], tied[0], tied[1]}} {
		rows := append([]csvRow{}, start...)
		sort.Slice(rows, func(i, j int) bool { return lessRow(rows[i], rows[j]) })
		for i, w := range want {
			if rows[i].file != w {
				t.Fatalf("position %d = %q, want %q — tied rows did not sort deterministically", i, rows[i].file, w)
			}
		}
	}
}

func TestLessRowOrdersByPlayerThenTime(t *testing.T) {
	rows := []csvRow{
		{player: "2", startTime: "2026-01-01T00:00:00", file: "b.rep"},
		{player: "1", startTime: "2026-02-01T00:00:00", file: "c.rep"},
		{player: "1", startTime: "2026-01-01T00:00:00", file: "a.rep"},
	}
	sort.Slice(rows, func(i, j int) bool { return lessRow(rows[i], rows[j]) })

	got := []string{rows[0].file, rows[1].file, rows[2].file}
	want := []string{"a.rep", "c.rep", "b.rep"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
