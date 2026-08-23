package features

import (
	"encoding/json"
	"flag"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/icza/screp/repparser"
)

var update = flag.Bool("update", false, "regenerate golden files")

var fixtureReplays = []string{
	"01_zvt_zergling_rush.rep",
	"03_zvp_progamer_soma.rep",
}

func TestFeatureNames(t *testing.T) {
	names, err := FeatureNames(Version)
	if err != nil {
		t.Fatalf("FeatureNames(%d): %v", Version, err)
	}
	if len(names) != 360 {
		t.Fatalf("v3 must have 360 dims, got %d", len(names))
	}
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			t.Fatalf("duplicate feature name %q", n)
		}
		seen[n] = true
	}
	if names[0] != "apm" || names[len(names)-1] != "dist_h6" {
		t.Fatalf("unexpected boundary names: first=%q last=%q", names[0], names[len(names)-1])
	}

	if _, err := FeatureNames(99); err == nil {
		t.Fatal("FeatureNames(99) must error on unknown version")
	}
}

func TestExtractGolden(t *testing.T) {
	type goldenPlayer struct {
		PlayerID byte      `json:"player_id"`
		Name     string    `json:"name"`
		Race     string    `json:"race"`
		Version  int       `json:"version"`
		Frames   int       `json:"frames"`
		CmdCount int       `json:"cmd_count"`
		Vector   []float64 `json:"vector"`
	}

	golden := map[string][]goldenPlayer{}
	goldenPath := filepath.Join("testdata", "golden_v3.json")
	if !*update {
		data, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("reading golden file (run with -update to generate): %v", err)
		}
		if err := json.Unmarshal(data, &golden); err != nil {
			t.Fatalf("parsing golden file: %v", err)
		}
	}

	names, _ := FeatureNames(Version)
	got := map[string][]goldenPlayer{}
	for _, fixture := range fixtureReplays {
		pfs, err := ExtractFile(filepath.Join("testdata", fixture))
		if err != nil {
			t.Fatalf("ExtractFile(%s): %v", fixture, err)
		}
		if len(pfs) == 0 {
			t.Fatalf("%s: no eligible players extracted", fixture)
		}
		for _, pf := range pfs {
			if len(pf.Vector) != len(names) {
				t.Fatalf("%s/%s: vector has %d dims, want %d", fixture, pf.Name, len(pf.Vector), len(names))
			}
			for i, v := range pf.Vector {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("%s/%s: feature %s is %v", fixture, pf.Name, names[i], v)
				}
			}
			got[fixture] = append(got[fixture], goldenPlayer{
				PlayerID: pf.PlayerID, Name: pf.Name, Race: pf.Race,
				Version: pf.Version, Frames: pf.Frames, CmdCount: pf.CmdCount,
				Vector: pf.Vector,
			})
		}
	}

	if *update {
		data, err := json.MarshalIndent(got, "", " ")
		if err != nil {
			t.Fatalf("marshaling golden file: %v", err)
		}
		if err := os.WriteFile(goldenPath, append(data, '\n'), 0o644); err != nil {
			t.Fatalf("writing golden file: %v", err)
		}
		t.Logf("wrote %s", goldenPath)
		return
	}

	for _, fixture := range fixtureReplays {
		want, gotPlayers := golden[fixture], got[fixture]
		if len(gotPlayers) != len(want) {
			t.Fatalf("%s: got %d players, golden has %d", fixture, len(gotPlayers), len(want))
		}
		for i, g := range gotPlayers {
			w := want[i]
			if g.PlayerID != w.PlayerID || g.Name != w.Name || g.Race != w.Race ||
				g.Version != w.Version || g.Frames != w.Frames || g.CmdCount != w.CmdCount {
				t.Errorf("%s player %d metadata mismatch: got %+v want %+v", fixture, i,
					goldenPlayer{PlayerID: g.PlayerID, Name: g.Name, Race: g.Race, Version: g.Version, Frames: g.Frames, CmdCount: g.CmdCount},
					goldenPlayer{PlayerID: w.PlayerID, Name: w.Name, Race: w.Race, Version: w.Version, Frames: w.Frames, CmdCount: w.CmdCount})
				continue
			}
			for d := range g.Vector {
				if math.Abs(g.Vector[d]-w.Vector[d]) > 1e-9 {
					t.Errorf("%s/%s: feature %s = %v, golden %v", fixture, g.Name, names[d], g.Vector[d], w.Vector[d])
				}
			}
		}
	}
}

func TestExtractNilAndNoCommands(t *testing.T) {
	if _, err := Extract(nil); err == nil {
		t.Fatal("Extract(nil) must error")
	}
	r, err := repparser.ParseFileConfig(filepath.Join("testdata", fixtureReplays[0]), repparser.Config{})
	if err != nil {
		t.Fatalf("parsing without commands: %v", err)
	}
	if _, err := Extract(r); err == nil {
		t.Fatal("Extract on a replay parsed without commands must error")
	}
}

func TestExtractOptions(t *testing.T) {
	path := filepath.Join("testdata", fixtureReplays[0])

	pfs, err := ExtractFile(path, WithMinCommands(1<<30))
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if len(pfs) != 0 {
		t.Fatalf("with impossible MinCommands, want 0 players, got %d", len(pfs))
	}

	pfs, err = ExtractFile(path, WithMinGameFrames(1<<30))
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if len(pfs) != 0 {
		t.Fatalf("with impossible MinGameFrames, want 0 players, got %d", len(pfs))
	}
}

func TestExtractIdempotentOnComputedReplay(t *testing.T) {
	// screpdb calls Compute before handing us the replay; extraction must not
	// depend on being the one to compute.
	path := filepath.Join("testdata", fixtureReplays[0])
	r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	r.Compute()
	precomputed, err := Extract(r)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	fresh, err := ExtractFile(path)
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	if len(precomputed) != len(fresh) {
		t.Fatalf("player count differs: %d vs %d", len(precomputed), len(fresh))
	}
	for i := range fresh {
		for d := range fresh[i].Vector {
			if fresh[i].Vector[d] != precomputed[i].Vector[d] {
				t.Fatalf("player %d dim %d differs: %v vs %v", i, d, fresh[i].Vector[d], precomputed[i].Vector[d])
			}
		}
	}
}

func BenchmarkParse(b *testing.B) {
	path := filepath.Join("testdata", fixtureReplays[1])
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExtract measures the screpdb integration path: the caller already
// parsed AND computed the replay, so this is the pure feature-extraction cost.
func BenchmarkExtract(b *testing.B) {
	path := filepath.Join("testdata", fixtureReplays[1])
	r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
	if err != nil {
		b.Fatal(err)
	}
	r.Compute()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Extract(r); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkExtractUncomputed includes the r.Compute() call Extract performs
// when the caller only parsed (per-command IneffKind is filled in by Compute).
func BenchmarkExtractUncomputed(b *testing.B) {
	path := filepath.Join("testdata", fixtureReplays[1])
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := Extract(r); err != nil {
			b.Fatal(err)
		}
	}
}
