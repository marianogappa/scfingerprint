package scfingerprint

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/icza/screp/repparser"
	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
	"github.com/marianogappa/scfingerprint/scoring"
)

func testScorer(t *testing.T) *scoring.Scorer {
	t.Helper()
	names, _ := features.FeatureNames(features.Version)
	return synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, len(names)))
}

func TestMatchWithReplay(t *testing.T) {
	scorer := testScorer(t)
	db, err := NewDataset(scorer)
	if err != nil {
		t.Fatal(err)
	}

	// Enroll: build fingerprints from the fixture replay players.
	replays := []string{
		filepath.Join("features", "testdata", "01_zvt_zergling_rush.rep"),
		filepath.Join("features", "testdata", "03_zvp_progamer_soma.rep"),
	}
	type enrollment struct {
		label string
		games []PlayerGame
	}
	enrolled := map[string]*enrollment{}
	for _, path := range replays {
		r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
		if err != nil {
			t.Fatal(err)
		}
		pfs, err := features.Extract(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, pf := range pfs {
			e, ok := enrolled[pf.Name]
			if !ok {
				e = &enrollment{label: pf.Name}
				enrolled[pf.Name] = e
			}
			e.games = append(e.games, PlayerGame{Vector: pf.Vector, Race: pf.Race})
		}
	}
	for _, e := range enrolled {
		fp, err := Enroll(e.games, Meta{Label: e.label})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Add(fp); err != nil {
			t.Fatal(err)
		}
	}
	if db.Len() != 4 {
		t.Fatalf("dataset has %d entries, want 4", db.Len())
	}

	// Match: probe one fixture replay's player against the dataset.
	r, err := repparser.ParseFileConfig(replays[0], repparser.Config{Commands: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range r.Header.Players {
		if p.Observer || p.Type == nil || p.Type.Name != "Human" {
			continue
		}
		results, err := Match(r, p.ID, db, WithMinZ(math.Inf(-1)))
		if err != nil {
			t.Fatalf("Match(%s): %v", p.Name, err)
		}
		if len(results) == 0 {
			t.Fatalf("Match(%s): no results", p.Name)
		}
		if results[0].Label != p.Name {
			t.Fatalf("Match(%s): top match is %q, want self", p.Name, results[0].Label)
		}
		if results[0].EvidenceN != 1 {
			t.Fatalf("EvidenceN = %d, want 1", results[0].EvidenceN)
		}
		for _, op := range []string{"fpr_1e2", "fpr_1e3", "fpr_1e4"} {
			if _, ok := results[0].OperatingPoints[op]; !ok {
				t.Fatalf("missing operating point %q", op)
			}
		}
	}
}

func TestMatchManyWithVectors(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)
	scorer := testScorer(t)
	db, err := NewDataset(scorer)
	if err != nil {
		t.Fatal(err)
	}

	// Enroll player 100 with 30 games.
	var enrollGames []PlayerGame
	for g := 0; g < 30; g++ {
		enrollGames = append(enrollGames, PlayerGame{Vector: synthtest.GameVector(100, g, d), Race: "z"})
	}
	fp, err := Enroll(enrollGames, Meta{Label: "P100"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Add(fp); err != nil {
		t.Fatal(err)
	}

	// Enroll a different player so the dataset has >1 entry.
	var otherGames []PlayerGame
	for g := 0; g < 30; g++ {
		otherGames = append(otherGames, PlayerGame{Vector: synthtest.GameVector(101, g, d), Race: "t"})
	}
	fp2, err := Enroll(otherGames, Meta{Label: "P101"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Add(fp2); err != nil {
		t.Fatal(err)
	}

	// Probe: 3 new games from player 100.
	var probeGames []PlayerGame
	for g := 30; g < 33; g++ {
		probeGames = append(probeGames, PlayerGame{Vector: synthtest.GameVector(100, g, d)})
	}
	results, err := MatchMany(probeGames, db, WithMinZ(math.Inf(-1)))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}
	if results[0].Label != "P100" {
		t.Fatalf("top match is %q, want P100", results[0].Label)
	}
	if results[0].EvidenceN != 3 {
		t.Fatalf("EvidenceN = %d, want 3", results[0].EvidenceN)
	}
}

func TestSame(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	// Same person, different games.
	var a, b []PlayerGame
	for g := 0; g < 5; g++ {
		a = append(a, PlayerGame{Vector: synthtest.GameVector(200, g, d)})
	}
	for g := 5; g < 10; g++ {
		b = append(b, PlayerGame{Vector: synthtest.GameVector(200, g, d)})
	}
	v, err := Same(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if v.EvidenceN != 10 {
		t.Fatalf("EvidenceN = %d, want 10", v.EvidenceN)
	}

	// Different people.
	var c []PlayerGame
	for g := 0; g < 5; g++ {
		c = append(c, PlayerGame{Vector: synthtest.GameVector(201, g, d)})
	}
	v2, err := Same(a, c)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Z >= v.Z {
		t.Fatalf("different people scored higher (%.3f) than same person (%.3f)", v2.Z, v.Z)
	}
}

func TestEnroll(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	var games []PlayerGame
	for g := 0; g < 20; g++ {
		games = append(games, PlayerGame{Vector: synthtest.GameVector(300, g, d), Race: "Protoss"})
	}
	fp, err := Enroll(games, Meta{Label: "TestPlayer", Source: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if fp.N() != 20 {
		t.Fatalf("N = %d, want 20", fp.N())
	}
	if fp.Meta.Label != "TestPlayer" {
		t.Fatalf("label = %q", fp.Meta.Label)
	}
	rc := fp.RaceCounts()
	if rc["p"] != 20 {
		t.Fatalf("race counts = %v, want p:20", rc)
	}
}

func TestMatchErrors(t *testing.T) {
	if _, err := MatchMany(nil, nil); err == nil {
		t.Fatal("expected error for nil games")
	}
	scorer := testScorer(t)
	db, _ := NewDataset(scorer)
	if _, err := MatchMany([]PlayerGame{{Vector: make([]float64, 360)}}, db); err == nil {
		t.Fatal("expected error for empty dataset")
	}
	if _, err := Same(nil, []PlayerGame{{Vector: make([]float64, 360)}}); err == nil {
		t.Fatal("expected error for empty side")
	}
	if _, err := Enroll(nil, Meta{}); err == nil {
		t.Fatal("expected error for nil games")
	}
}

func TestPlayerGameWithoutVectorOrReplay(t *testing.T) {
	scorer := testScorer(t)
	db, _ := NewDataset(scorer)
	fp := fingerprint.New(Meta{Label: "x"})
	names, _ := features.FeatureNames(features.Version)
	for g := 0; g < 5; g++ {
		_ = fp.Add(synthtest.GameVector(0, g, len(names)), "")
	}
	_ = db.Add(fp)
	_, err := Match(nil, 0, db)
	if err == nil {
		t.Fatal("expected error for nil replay")
	}
}
