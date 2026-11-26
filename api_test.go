package scfingerprint

import (
	"math"
	"path/filepath"
	"testing"

	"github.com/icza/screp/repparser"
	"github.com/marianogappa/scfingerprint/internal/features"
	"github.com/marianogappa/scfingerprint/internal/scoring"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
)

func testScorer(t *testing.T) *scoring.Scorer {
	t.Helper()
	names, _ := features.FeatureNames(features.Version)
	return synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, len(names)))
}

func TestMatchWithReplay(t *testing.T) {
	scorer := testScorer(t)
	db, err := newDatasetWithScorer(scorer)
	if err != nil {
		t.Fatal(err)
	}

	// Enroll: build fingerprints from the fixture replay players.
	replays := []string{
		filepath.Join("internal", "features", "testdata", "01_zvt_zergling_rush.rep"),
		filepath.Join("internal", "features", "testdata", "03_zvp_progamer_soma.rep"),
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
	db, err := newDatasetWithScorer(scorer)
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
	if v.EvidenceN != 5 || v.GamesA != 5 || v.GamesB != 5 {
		t.Fatalf("EvidenceN/GamesA/GamesB = %d/%d/%d, want 5/5/5", v.EvidenceN, v.GamesA, v.GamesB)
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

func TestSameEvidenceIsTheSmallerSide(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)

	one := []PlayerGame{{Vector: synthtest.GameVector(200, 0, d)}}
	var many []PlayerGame
	for g := 1; g < 31; g++ {
		many = append(many, PlayerGame{Vector: synthtest.GameVector(200, g, d)})
	}

	oneVsMany, err := Same(one, many)
	if err != nil {
		t.Fatal(err)
	}
	if oneVsMany.EvidenceN != 1 || oneVsMany.GamesA != 1 || oneVsMany.GamesB != 30 {
		t.Fatalf("1 vs many: EvidenceN/GamesA/GamesB = %d/%d/%d, want 1/1/30", oneVsMany.EvidenceN, oneVsMany.GamesA, oneVsMany.GamesB)
	}

	manyVsOne, err := Same(many, one)
	if err != nil {
		t.Fatal(err)
	}
	if manyVsOne.EvidenceN != 1 || manyVsOne.GamesA != 30 || manyVsOne.GamesB != 1 {
		t.Fatalf("many vs 1: EvidenceN/GamesA/GamesB = %d/%d/%d, want 1/30/1", manyVsOne.EvidenceN, manyVsOne.GamesA, manyVsOne.GamesB)
	}
	if manyVsOne.Z != oneVsMany.Z || manyVsOne.Cosine != oneVsMany.Cosine {
		t.Fatalf("side order changed the score: z %.4f vs %.4f", manyVsOne.Z, oneVsMany.Z)
	}

	manyVsMany, err := Same(many[:20], many[20:])
	if err != nil {
		t.Fatal(err)
	}
	if manyVsMany.EvidenceN != 10 || manyVsMany.GamesA != 20 || manyVsMany.GamesB != 10 {
		t.Fatalf("many vs many: EvidenceN/GamesA/GamesB = %d/%d/%d, want 10/20/10", manyVsMany.EvidenceN, manyVsMany.GamesA, manyVsMany.GamesB)
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
	if fp.Meta().Label != "TestPlayer" {
		t.Fatalf("label = %q", fp.Meta().Label)
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
	db, _ := newDatasetWithScorer(scorer)
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
	db, _ := newDatasetWithScorer(scorer)
	fp := NewFingerprint(Meta{Label: "x"})
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
