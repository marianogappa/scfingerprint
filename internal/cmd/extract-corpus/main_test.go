package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/marianogappa/scfingerprint/internal/features"
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

func TestMatchByToon(t *testing.T) {
	pfs := []features.PlayerFeatures{
		{Name: "Alice", Race: "Terran"},
		{Name: "Bob", Race: "Terran"},
	}

	tests := []struct {
		name     string
		toon     string
		wantName string
		wantOK   bool
	}{
		{"exact match", "Bob", "Bob", true},
		{"case insensitive", "bob", "Bob", true},
		{"no match", "Charlie", "", false},
		{"empty toon", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf, ok := matchByToon(tt.toon, pfs)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && pf.Name != tt.wantName {
				t.Errorf("name = %q, want %q", pf.Name, tt.wantName)
			}
		})
	}
}

func TestMatchByUniqueRace(t *testing.T) {
	nonMirror := []features.PlayerFeatures{
		{Name: "Alice", Race: "Terran"},
		{Name: "Bob", Race: "Zerg"},
	}
	mirror := []features.PlayerFeatures{
		{Name: "Alice", Race: "Terran"},
		{Name: "Bob", Race: "Terran"},
	}

	tests := []struct {
		name     string
		race     string
		pfs      []features.PlayerFeatures
		wantName string
		wantOK   bool
	}{
		{"non-mirror resolves", "T", nonMirror, "Alice", true},
		{"mirror is ambiguous", "T", mirror, "", false},
		{"unknown race letter", "X", nonMirror, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf, ok := matchByUniqueRace(tt.race, tt.pfs)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && pf.Name != tt.wantName {
				t.Errorf("name = %q, want %q", pf.Name, tt.wantName)
			}
		})
	}
}

func TestMatchByNameSet(t *testing.T) {
	pfs := []features.PlayerFeatures{
		{Name: "Alice", Race: "Terran"},
		{Name: "Bob", Race: "Terran"},
	}

	tests := []struct {
		name     string
		names    map[string]bool
		wantName string
		wantOK   bool
	}{
		{"single match", map[string]bool{"Bob": true}, "Bob", true},
		{"both match", map[string]bool{"Alice": true, "Bob": true}, "", false},
		{"no match", map[string]bool{"Charlie": true}, "", false},
		{"nil set", nil, "", false},
		{"empty set", map[string]bool{}, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pf, ok := matchByNameSet(tt.names, pfs)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && pf.Name != tt.wantName {
				t.Errorf("name = %q, want %q", pf.Name, tt.wantName)
			}
		})
	}
}

// A TvT replay where the account is in slot 1 must attribute to slot 1, not
// slot 0. The name set is learned from a non-mirror game in pass 1 and used to
// resolve the mirror in pass 2.
func TestResolveRowsMirrorSlot1(t *testing.T) {
	pfsNonMirror := []features.PlayerFeatures{
		{Name: "Opponent", Race: "Zerg", Vector: []float64{0}},
		{Name: "Soulkey", Race: "Terran", Vector: []float64{1}},
	}
	pfsMirror := []features.PlayerFeatures{
		{Name: "Flash", Race: "Terran", Vector: []float64{2}},
		{Name: "Soulkey", Race: "Terran", Vector: []float64{3}},
	}

	jobs := []fileJob{
		{file: "nonmirror.rep", rows: []replayRow{
			{File: "nonmirror.rep", AuroraID: 100, Race: "T", Matchup: "TvZ"},
		}},
		{file: "mirror.rep", rows: []replayRow{
			{File: "mirror.rep", AuroraID: 100, Race: "T", Matchup: "TvT"},
		}},
	}
	parsed := map[string]*parseResult{
		"nonmirror.rep": {pfs: pfsNonMirror},
		"mirror.rep":    {pfs: pfsMirror},
	}

	results, stats := resolveRows(jobs, parsed, nil)

	if stats.byRace != 1 {
		t.Errorf("byRace = %d, want 1", stats.byRace)
	}
	if stats.byName != 1 {
		t.Errorf("byName = %d, want 1", stats.byName)
	}
	if stats.noMatch != 0 {
		t.Errorf("noMatch = %d, want 0", stats.noMatch)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}

	for _, r := range results {
		if r.file == "mirror.rep" && r.vector[0] != 3 {
			t.Errorf("mirror game got vector %v, want [3] (slot 1 = Soulkey)", r.vector)
		}
	}
}

// A mirror row whose account plays under a name its own metadata rows never
// teach is still resolvable when the curated identities file lists that name as
// one of the account's handles.
func TestResolveRowsSeededHandleResolvesMirror(t *testing.T) {
	pfsMirror := []features.PlayerFeatures{
		{Name: "Opponent", Race: "Protoss", Vector: []float64{0}},
		{Name: "Stork", Race: "Protoss", Vector: []float64{1}},
	}

	jobs := []fileJob{
		{file: "pvp.rep", rows: []replayRow{
			{File: "pvp.rep", AuroraID: 500, Race: "P", Matchup: "PvP"},
		}},
	}
	parsed := map[string]*parseResult{
		"pvp.rep": {pfs: pfsMirror},
	}
	seeded := map[int64]map[string]bool{500: {"Stork": true}}

	results, stats := resolveRows(jobs, parsed, seeded)

	if stats.byName != 1 || stats.noMatch != 0 {
		t.Fatalf("stats = %+v, want byName 1, noMatch 0", stats)
	}
	if len(results) != 1 || results[0].vector[0] != 1 {
		t.Fatalf("results = %+v, want the seeded handle's player (vector [1])", results)
	}
}

func TestIdentityHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identities.jsonl")
	lines := `{"auroraId":10267282,"battleTag":"x","handles":[{"toon":"llIIIIllIIlIlI"},{"toon":"Stork"}]}
{"auroraId":12355047,"battleTag":"","handles":[]}
{"auroraId":42,"handles":[{"toon":""}]}`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}

	ns, err := identityHandles(path)
	if err != nil {
		t.Fatal(err)
	}

	if !ns[10267282]["Stork"] || !ns[10267282]["llIIIIllIIlIlI"] {
		t.Errorf("handles for 10267282 = %v, want both toons", ns[10267282])
	}
	if len(ns[12355047]) != 0 {
		t.Errorf("account with no handles should have no names, got %v", ns[12355047])
	}
	if len(ns[42]) != 0 {
		t.Errorf("empty toon must not be seeded, got %v", ns[42])
	}
}

// A mirror game where no name can be learned (no toon, all games are mirrors)
// must be dropped rather than guessed.
func TestResolveRowsMirrorNoLearnableNameIsDropped(t *testing.T) {
	pfsMirror := []features.PlayerFeatures{
		{Name: "Alpha", Race: "Zerg", Vector: []float64{0}},
		{Name: "Beta", Race: "Zerg", Vector: []float64{1}},
	}

	jobs := []fileJob{
		{file: "zvz.rep", rows: []replayRow{
			{File: "zvz.rep", AuroraID: 200, Race: "Z", Matchup: "ZvZ"},
		}},
	}
	parsed := map[string]*parseResult{
		"zvz.rep": {pfs: pfsMirror},
	}

	results, stats := resolveRows(jobs, parsed, nil)

	if len(results) != 0 {
		t.Fatalf("expected 0 results for unlearnable mirror, got %d", len(results))
	}
	if stats.noMatch != 1 {
		t.Errorf("noMatch = %d, want 1", stats.noMatch)
	}
}

// Toon resolution takes priority and feeds the name set that resolves mirrors.
func TestResolveRowsToonFeedsNameSet(t *testing.T) {
	pfsToon := []features.PlayerFeatures{
		{Name: "Flash", Race: "Terran", Vector: []float64{0}},
		{Name: "Stork", Race: "Protoss", Vector: []float64{1}},
	}
	pfsMirror := []features.PlayerFeatures{
		{Name: "Flash", Race: "Terran", Vector: []float64{2}},
		{Name: "Light", Race: "Terran", Vector: []float64{3}},
	}

	jobs := []fileJob{
		{file: "tvp.rep", rows: []replayRow{
			{File: "tvp.rep", AuroraID: 300, Toon: "Flash", Race: "T", Matchup: "TvP"},
		}},
		{file: "tvt.rep", rows: []replayRow{
			{File: "tvt.rep", AuroraID: 300, Race: "T", Matchup: "TvT"},
		}},
	}
	parsed := map[string]*parseResult{
		"tvp.rep": {pfs: pfsToon},
		"tvt.rep": {pfs: pfsMirror},
	}

	results, stats := resolveRows(jobs, parsed, nil)

	if stats.byToon != 1 {
		t.Errorf("byToon = %d, want 1", stats.byToon)
	}
	if stats.byName != 1 {
		t.Errorf("byName = %d, want 1", stats.byName)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}

	for _, r := range results {
		if r.file == "tvt.rep" && r.vector[0] != 2 {
			t.Errorf("mirror game got vector %v, want [2] (Flash)", r.vector)
		}
	}
}
