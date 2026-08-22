package eval

import (
	"testing"
	"time"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/hygiene"
	"github.com/marianogappa/scfingerprint/internal/synthtest"
	"github.com/marianogappa/scfingerprint/training"
)

// contaminatedCorpus returns an eval corpus where the label "shared" covers
// two different synthetic humans (players 40 and 41) in sequence — the
// account was handed from one to the other, the classic shared-account
// shape chronological-half self-consistency catches.
func contaminatedCorpus(d int) []training.Sample {
	samples := synthtest.Corpus(30, 10, 40, d)
	extra := synthtest.Corpus(40, 2, 40, d)
	for i := range extra {
		if extra[i].Player == "P041" {
			extra[i].StartTime = extra[i].StartTime.Add(1000 * time.Hour)
		}
		extra[i].Player = "shared"
		// Same race for both humans: race-aware self-consistency only
		// catches contamination within a race stratum.
		extra[i].Race = "Terran"
	}
	return append(samples, extra...)
}

func TestEvaluateAuditsLabels(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))
	samples := contaminatedCorpus(d)

	report, err := Evaluate(samples, scorer, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(report.LabelAudit) == 0 {
		t.Fatal("report has no label audit")
	}
	var shared *hygiene.LabelAudit
	for i := range report.LabelAudit {
		if report.LabelAudit[i].Label == "shared" {
			shared = &report.LabelAudit[i]
		}
	}
	if shared == nil {
		t.Fatal("shared label missing from audit")
	}
	if shared.RaceAware >= 0.85 {
		t.Fatalf("two-human label scored %.3f — audit failed to flag contamination", shared.RaceAware)
	}
	if len(report.ExcludedLabels) != 0 {
		t.Fatalf("no exclusion requested but got %v", report.ExcludedLabels)
	}
}

func TestEvaluateExcludesContaminatedLabels(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))
	samples := contaminatedCorpus(d)

	opts := DefaultOptions()
	opts.MinLabelSelfConsistency = 0.85
	report, err := Evaluate(samples, scorer, opts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range report.ExcludedLabels {
		if l == "shared" {
			found = true
		}
	}
	if !found {
		t.Fatalf("shared label not excluded; excluded = %v", report.ExcludedLabels)
	}
	if report.NumPlayers != 10 {
		t.Fatalf("players = %d, want 10 (shared excluded)", report.NumPlayers)
	}
}

func TestEvaluateSplitByRace(t *testing.T) {
	names, _ := features.FeatureNames(features.Version)
	d := len(names)
	scorer := synthtest.Scorer(t, synthtest.Corpus(0, 30, 60, d))

	// 10 mono-race players; one of them relabeled into a two-race player.
	samples := synthtest.Corpus(30, 10, 40, d)
	count := 0
	for i := range samples {
		if samples[i].Player == "P030" {
			if count%2 == 0 {
				samples[i].Race = "z"
			} else {
				samples[i].Race = "p"
			}
			count++
		}
	}

	opts := DefaultOptions()
	opts.SplitByRace = true
	report, err := Evaluate(samples, scorer, opts)
	if err != nil {
		t.Fatal(err)
	}
	// P030 splits into two identities; the other 9 players keep one each.
	if report.NumPlayers != 11 {
		t.Fatalf("players = %d, want 11 (P030 split into 2 race strata)", report.NumPlayers)
	}
	// Same-label strata are excluded from each other's impostor pools, so a
	// split must not add impostor comparisons between P030/z and P030/p.
	// (They are the same synthetic human; scoring them as impostors would
	// poison the pools.) Just assert the run completes and pools are sane.
	if report.Scenarios["n1_all"].NumImpostor == 0 {
		t.Fatal("empty impostor pool")
	}
}

func TestSplitByRaceExclusions(t *testing.T) {
	samples := []training.Sample{
		{Player: "a", Race: "z", Vector: []float64{1}},
		{Player: "a", Race: "p", Vector: []float64{1}},
		{Player: "b", Race: "t", Vector: []float64{1}},
	}
	split, exclusions := splitByRace(samples, [][2]string{{"a", "b"}})
	if split[0].Player != "a/z" || split[1].Player != "a/p" || split[2].Player != "b/t" {
		t.Fatalf("unexpected relabeling: %v %v %v", split[0].Player, split[1].Player, split[2].Player)
	}
	if samples[0].Player != "a" {
		t.Fatal("splitByRace mutated the caller's samples")
	}
	set := map[[2]string]bool{}
	for _, p := range exclusions {
		set[p] = true
	}
	// Caller exclusion (a, b) expands to strata pairs; same-label strata
	// also excluded.
	if !set[[2]string{"a/z", "b/t"}] || !set[[2]string{"a/p", "b/t"}] {
		t.Fatalf("caller exclusions not expanded: %v", exclusions)
	}
	if !set[[2]string{"a/z", "a/p"}] {
		t.Fatalf("same-label strata not excluded: %v", exclusions)
	}
}
