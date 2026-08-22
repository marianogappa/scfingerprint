// Command drift-analysis measures which factor (race, game mode, time) drives
// within-label fingerprint drift on a labeled corpus. For each label it
// computes whitened centroids per stratum and reports cross-stratum cosine
// similarities, controlling one factor at a time (issue #40 research).
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV (required)")
	labels := flag.String("labels", "", "comma-separated labels to analyze (required)")
	minGames := flag.Int("min-stratum", 8, "minimum games per stratum")
	flag.Parse()
	if *csvPath == "" || *labels == "" {
		log.Fatal("-csv and -labels are required")
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		log.Fatal(err)
	}

	want := map[string]bool{}
	for _, l := range strings.Split(*labels, ",") {
		want[strings.TrimSpace(l)] = true
	}

	byLabel := map[string][]training.Sample{}
	for _, s := range samples {
		if want[s.Player] {
			byLabel[s.Player] = append(byLabel[s.Player], s)
		}
	}

	centroid := func(ss []training.Sample) []float64 {
		if len(ss) == 0 {
			return nil
		}
		var sum []float64
		for _, s := range ss {
			w, err := scorer.Transform(s.Vector)
			if err != nil {
				log.Fatal(err)
			}
			if sum == nil {
				sum = make([]float64, len(w))
			}
			for j, v := range w {
				sum[j] += v
			}
		}
		for j := range sum {
			sum[j] /= float64(len(ss))
		}
		return sum
	}

	cos := func(a, b []float64) float64 {
		dot, na, nb := 0.0, 0.0, 0.0
		for i := range a {
			dot += a[i] * b[i]
			na += a[i] * a[i]
			nb += b[i] * b[i]
		}
		d := math.Sqrt(na) * math.Sqrt(nb)
		if d == 0 {
			return 0
		}
		return dot / d
	}

	mode := func(s training.Sample) string {
		if s.NumHumans <= 2 {
			return "1v1"
		}
		return "team"
	}

	type stratum struct {
		key string
		ss  []training.Sample
	}
	strata := map[string][]stratum{}
	addStrata := func(dim string, keyFn func(training.Sample) string) {
		for label, ss := range byLabel {
			groups := map[string][]training.Sample{}
			for _, s := range ss {
				groups[keyFn(s)] = append(groups[keyFn(s)], s)
			}
			for k, g := range groups {
				if len(g) >= *minGames {
					strata[dim] = append(strata[dim], stratum{key: label + "/" + k, ss: g})
				}
			}
		}
	}

	addStrata("race", func(s training.Sample) string { return s.Race })
	addStrata("mode", mode)
	addStrata("race+mode", func(s training.Sample) string { return s.Race + "-" + mode(s) })
	addStrata("era", func(s training.Sample) string {
		if s.StartTime.IsZero() {
			return "unknown"
		}
		y := s.StartTime.Year()
		switch {
		case y <= 2018:
			return "2017-18"
		case y <= 2023:
			return "2019-23"
		default:
			return "2024-26"
		}
	})
	addStrata("race+mode+era", func(s training.Sample) string {
		era := "old"
		if !s.StartTime.IsZero() && s.StartTime.Year() >= 2024 {
			era = "new"
		}
		return s.Race + "-" + mode(s) + "-" + era
	})

	for _, dim := range []string{"race", "mode", "race+mode", "era", "race+mode+era"} {
		sts := strata[dim]
		sort.Slice(sts, func(i, j int) bool { return sts[i].key < sts[j].key })
		fmt.Printf("\n===== stratified by %s (min %d games) =====\n", dim, *minGames)
		cents := make([][]float64, len(sts))
		for i, st := range sts {
			cents[i] = centroid(st.ss)
		}
		for i := 0; i < len(sts); i++ {
			for j := i + 1; j < len(sts); j++ {
				fmt.Printf("  %-28s vs %-28s  %.3f  (n=%d,%d)\n",
					sts[i].key, sts[j].key, cos(cents[i], cents[j]), len(sts[i].ss), len(sts[j].ss))
			}
		}
	}
}
