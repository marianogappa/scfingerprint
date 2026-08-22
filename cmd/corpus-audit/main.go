// Command corpus-audit applies the catalog-hygiene gates to a labeled feature
// CSV before it is used as a benchmark or as enrollment material.
//
// A benchmark corpus is only as good as its labels. A label covering two
// humans — a shared account, or a name reused across people — splits into two
// clusters that score as impostors against each other, which sets the
// operating-point thresholds and collapses TPR at low FPR. That looks
// identical to a model regression, so it must be ruled out first.
//
// For each label with enough games it reports the self-consistency score
// (first half of the games versus the second half, chronologically) and then
// every label-pair centroid similarity, annotated with co-occurrence
// disproof. Genuine single-person labels score around 0.96; anything much
// lower is contaminated.
//
// Usage:
//
//	go run ./cmd/corpus-audit -csv corpus.csv [-artifact model.json] [-min-games 20] [-min-self-consistency 0.9]
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"

	"github.com/marianogappa/scfingerprint/hygiene"
	"github.com/marianogappa/scfingerprint/model"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

type labelReport struct {
	Label           string             `json:"label"`
	Games           int                `json:"games"`
	SelfConsistency float64            `json:"self_consistency"`
	RaceAware       float64            `json:"race_aware_self_consistency"`
	Strata          map[string]float64 `json:"strata,omitempty"`
	Passes          bool               `json:"passes"`
}

type pairReport struct {
	A         string  `json:"a"`
	B         string  `json:"b"`
	Cosine    float64 `json:"cosine"`
	Disproved bool    `json:"disproved_by_co_occurrence"`
}

type auditReport struct {
	Labels []labelReport `json:"labels"`
	Pairs  []pairReport  `json:"pairs"`
	Clean  []string      `json:"clean_labels"`
}

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV (required)")
	artifactPath := flag.String("artifact", "", "model artifact JSON (default: embedded)")
	minGames := flag.Int("min-games", 20, "minimum games for a label to be audited")
	minSelf := flag.Float64("min-self-consistency", 0.9, "self-consistency below this marks a label contaminated")
	topPairs := flag.Int("top-pairs", 20, "how many highest-scoring label pairs to report")
	asJSON := flag.Bool("json", false, "emit JSON instead of a table")
	cleanCSV := flag.String("clean-csv", "", "write a copy of the input CSV filtered to passing labels")
	flag.Parse()

	if *csvPath == "" {
		fmt.Fprintln(os.Stderr, "error: -csv is required")
		flag.Usage()
		os.Exit(1)
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	scorer, err := loadScorer(*artifactPath)
	if err != nil {
		log.Fatal(err)
	}

	byLabel := map[string][][]float64{}
	for _, s := range samples {
		w, err := scorer.Transform(s.Vector)
		if err != nil {
			log.Fatal(err)
		}
		byLabel[s.Player] = append(byLabel[s.Player], w)
	}

	labels := make([]string, 0, len(byLabel))
	for l, games := range byLabel {
		if len(games) >= *minGames {
			labels = append(labels, l)
		}
	}
	sort.Strings(labels)
	if len(labels) == 0 {
		log.Fatalf("no label has >= %d games", *minGames)
	}

	co := hygiene.BuildCoOccurrence(hygiene.ManifestFromSamples(samples))

	audits, err := hygiene.AuditLabels(samples, scorer, *minGames)
	if err != nil {
		log.Fatal(err)
	}

	rep := auditReport{}
	centroids := map[string][]float64{}
	for _, l := range labels {
		all, err := scorer.Fingerprint(byLabel[l]...)
		if err != nil {
			log.Fatal(err)
		}
		centroids[l] = all
	}
	for _, a := range audits {
		// The verdict is on the race-aware score: a random-race player reads
		// as multiple people under the mixed metric while staying high per
		// race (issue #40).
		lr := labelReport{
			Label:           a.Label,
			Games:           a.Games,
			SelfConsistency: a.Mixed,
			RaceAware:       a.RaceAware,
			Strata:          a.Strata,
			Passes:          a.RaceAware >= *minSelf,
		}
		rep.Labels = append(rep.Labels, lr)
		if lr.Passes {
			rep.Clean = append(rep.Clean, a.Label)
		}
	}

	for i := 0; i < len(labels); i++ {
		for j := i + 1; j < len(labels); j++ {
			a, b := labels[i], labels[j]
			rep.Pairs = append(rep.Pairs, pairReport{
				A: a, B: b,
				Cosine:    cosine(centroids[a], centroids[b]),
				Disproved: co.Disproved(a, b),
			})
		}
	}
	sort.Slice(rep.Pairs, func(i, j int) bool { return rep.Pairs[i].Cosine > rep.Pairs[j].Cosine })

	if *cleanCSV != "" {
		if err := writeCleanCSV(*csvPath, *cleanCSV, rep.Clean); err != nil {
			log.Fatal(err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d clean labels)\n", *cleanCSV, len(rep.Clean))
	}

	if *asJSON {
		out, _ := json.MarshalIndent(rep, "", " ")
		fmt.Println(string(out))
		return
	}

	fmt.Printf("model: %s\n", scorer.ModelTag())
	if scorer.IsSynthetic() {
		fmt.Println("WARNING: the model artifact is synthetic; these scores carry no evidential weight")
	}
	fmt.Printf("\n%d labels with >= %d games (%d pass self-consistency >= %.2f)\n\n", len(labels), *minGames, len(rep.Clean), *minSelf)
	fmt.Printf("%-24s %6s %7s %10s  %s\n", "label", "games", "mixed", "race-aware", "verdict")
	for _, l := range rep.Labels {
		verdict := "clean"
		if !l.Passes {
			verdict = "CONTAMINATED — multi-human label, or one human with heavy mode/time drift"
		}
		fmt.Printf("%-24s %6d %7.3f %10.3f  %s\n", l.Label, l.Games, l.SelfConsistency, l.RaceAware, verdict)
	}

	n := *topPairs
	if n > len(rep.Pairs) {
		n = len(rep.Pairs)
	}
	fmt.Printf("\ntop %d label pairs by centroid similarity\n\n", n)
	fmt.Printf("%-24s %-24s %7s  %s\n", "label A", "label B", "cos", "co-occurrence")
	for _, p := range rep.Pairs[:n] {
		verdict := "never played each other"
		if p.Disproved {
			verdict = "DISPROVED — played each other, cannot be one person"
		}
		fmt.Printf("%-24s %-24s %7.3f  %s\n", p.A, p.B, p.Cosine, verdict)
	}
}

func loadScorer(path string) (*scoring.Scorer, error) {
	if path == "" {
		return scoring.NewFromEmbedded()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	a, err := model.Load(data)
	if err != nil {
		return nil, err
	}
	return scoring.New(a)
}

func cosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// writeCleanCSV copies the input CSV keeping only rows whose player column is
// in the clean label set.
func writeCleanCSV(inPath, outPath string, clean []string) error {
	keep := map[string]bool{}
	for _, l := range clean {
		keep[l] = true
	}
	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	r := csv.NewReader(in)
	header, err := r.Read()
	if err != nil {
		return err
	}
	playerIdx := -1
	for i, h := range header {
		if h == "player" {
			playerIdx = i
			break
		}
	}
	if playerIdx < 0 {
		return fmt.Errorf("no player column in %s", inPath)
	}

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	w := csv.NewWriter(out)
	if err := w.Write(header); err != nil {
		return err
	}
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if keep[rec[playerIdx]] {
			if err := w.Write(rec); err != nil {
				return err
			}
		}
	}
	w.Flush()
	return w.Error()
}
