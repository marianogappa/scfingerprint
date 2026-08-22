// Command eval runs the evaluation harness on labeled feature CSVs and
// enforces regression gates: it exits non-zero if any gated metric degrades
// beyond tolerance versus the baseline.
//
// Usage:
//
//	go run ./cmd/eval -csv corpus.csv [-artifact model.json] [-exclusions smurfs.json] [-gates gates.json]
//
// The gates file maps scenario names to metric bounds:
//
//	{"n1_all": {"eer_max": 0.0021, "tpr_at_fpr_1e3_min": 0.997}}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/marianogappa/scfingerprint/eval"
	"github.com/marianogappa/scfingerprint/model"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

type gate struct {
	EERMax         *float64 `json:"eer_max"`
	TPRAtFPR1e3Min *float64 `json:"tpr_at_fpr_1e3_min"`
	AccuracyMin    *float64 `json:"closed_set_accuracy_min"`
}

func main() {
	csvPaths := flag.String("csv", "", "comma-separated paths to labeled feature CSVs (required)")
	artifactPath := flag.String("artifact", "", "path to model artifact JSON (default: embedded)")
	exclusionsPath := flag.String("exclusions", "", "path to known-smurf exclusion manifest JSON")
	gatesPath := flag.String("gates", "", "path to regression gates JSON; failing a gate exits 1")
	enrollFrac := flag.Float64("enroll-frac", 0.5, "chronological fraction of games used for enrollment")
	minGames := flag.Int("min-games", 4, "minimum games per player")
	minLabelSC := flag.Float64("min-label-self-consistency", 0, "exclude labels below this race-aware self-consistency (0 = audit only)")
	splitByRace := flag.Bool("split-by-race", false, "evaluate each (label, race) stratum as its own identity")
	flag.Parse()

	if *csvPaths == "" {
		fmt.Fprintln(os.Stderr, "error: -csv is required")
		flag.Usage()
		os.Exit(1)
	}

	var samples []training.Sample
	for _, path := range strings.Split(*csvPaths, ",") {
		ss, err := training.ReadCSV(strings.TrimSpace(path))
		if err != nil {
			fatal(err)
		}
		samples = append(samples, ss...)
	}

	var scorer *scoring.Scorer
	var err error
	if *artifactPath != "" {
		data, err := os.ReadFile(*artifactPath)
		if err != nil {
			fatal(err)
		}
		a, err := model.Load(data)
		if err != nil {
			fatal(err)
		}
		scorer, err = scoring.New(a)
		if err != nil {
			fatal(err)
		}
	} else {
		scorer, err = scoring.NewFromEmbedded()
		if err != nil {
			fatal(err)
		}
	}

	opts := eval.DefaultOptions()
	opts.EnrollFrac = *enrollFrac
	opts.MinGamesPerPlayer = *minGames
	opts.MinLabelSelfConsistency = *minLabelSC
	opts.SplitByRace = *splitByRace
	if *exclusionsPath != "" {
		opts.Exclusions, err = eval.LoadExclusions(*exclusionsPath)
		if err != nil {
			fatal(err)
		}
	}

	report, err := eval.Evaluate(samples, scorer, opts)
	if err != nil {
		fatal(err)
	}

	for _, l := range report.ExcludedLabels {
		fmt.Fprintf(os.Stderr, "EXCLUDED: label %q fails race-aware self-consistency %.2f\n", l, *minLabelSC)
	}
	if *minLabelSC == 0 {
		for _, a := range report.LabelAudit {
			if a.RaceAware < 0.85 {
				fmt.Fprintf(os.Stderr, "WARNING: label %q race-aware self-consistency %.3f — ground truth may be contaminated (multi-human label or heavy drift); consider -min-label-self-consistency\n", a.Label, a.RaceAware)
			}
		}
	}

	out, _ := json.MarshalIndent(report, "", " ")
	fmt.Println(string(out))

	if *gatesPath == "" {
		return
	}
	gatesData, err := os.ReadFile(*gatesPath)
	if err != nil {
		fatal(err)
	}
	var gates map[string]gate
	if err := json.Unmarshal(gatesData, &gates); err != nil {
		fatal(fmt.Errorf("parsing gates %s: %w", *gatesPath, err))
	}

	failed := false
	for scenario, g := range gates {
		m, ok := report.Scenarios[scenario]
		if !ok {
			fmt.Fprintf(os.Stderr, "GATE FAIL: scenario %q not in report\n", scenario)
			failed = true
			continue
		}
		if g.EERMax != nil && m.EER > *g.EERMax {
			fmt.Fprintf(os.Stderr, "GATE FAIL: %s EER %.5f > max %.5f\n", scenario, m.EER, *g.EERMax)
			failed = true
		}
		if g.TPRAtFPR1e3Min != nil {
			if m.TPRAtFPR1e3 == nil {
				fmt.Fprintf(os.Stderr, "GATE SKIP: %s TPR@1e-3 unmeasurable (impostor pool %d too small)\n", scenario, m.NumImpostor)
			} else if *m.TPRAtFPR1e3 < *g.TPRAtFPR1e3Min {
				fmt.Fprintf(os.Stderr, "GATE FAIL: %s TPR@1e-3 %.4f < min %.4f\n", scenario, *m.TPRAtFPR1e3, *g.TPRAtFPR1e3Min)
				failed = true
			}
		}
		if g.AccuracyMin != nil && m.ClosedSetAccuracy < *g.AccuracyMin {
			fmt.Fprintf(os.Stderr, "GATE FAIL: %s closed-set accuracy %.4f < min %.4f\n", scenario, m.ClosedSetAccuracy, *g.AccuracyMin)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "all gates passed")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
