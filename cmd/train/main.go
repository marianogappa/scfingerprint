// Command train reads labeled feature CSVs, fits the fingerprint model,
// and writes the artifact JSON to disk.
//
// Usage:
//
//	go run ./cmd/train -csv data1.csv,data2.csv -out model/artifact.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/marianogappa/scfingerprint/training"
)

func main() {
	csvPaths := flag.String("csv", "", "comma-separated paths to labeled feature CSVs (required)")
	outPath := flag.String("out", "model/artifact.json", "output path for artifact JSON")
	k := flag.Int("k", 150, "feature selection K")
	shrinkage := flag.Float64("shrinkage", 0.15, "whitening shrinkage alpha")
	trainFrac := flag.Float64("train-frac", 0.7, "chronological train fraction")
	cohortSize := flag.Int("cohort-size", 30, "number of cohort players")
	minGames := flag.Int("min-games", 5, "minimum games per player")
	corpora := flag.String("corpora", "", "comma-separated corpus names for provenance")
	flag.Parse()

	if *csvPaths == "" {
		fmt.Fprintln(os.Stderr, "error: -csv is required")
		flag.Usage()
		os.Exit(1)
	}

	var allSamples []training.Sample
	for _, path := range strings.Split(*csvPaths, ",") {
		path = strings.TrimSpace(path)
		samples, err := training.ReadCSV(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "read %d samples from %s\n", len(samples), path)
		allSamples = append(allSamples, samples...)
	}
	fmt.Fprintf(os.Stderr, "total: %d samples\n", len(allSamples))

	cfg := training.Config{
		K:                 *k,
		Shrinkage:         *shrinkage,
		TrainFrac:         *trainFrac,
		CohortSize:        *cohortSize,
		MinGamesPerPlayer: *minGames,
	}
	if *corpora != "" {
		cfg.Corpora = strings.Split(*corpora, ",")
	}
	cfg.GitSHA = gitSHA()

	artifact, err := training.Fit(allSamples, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "training failed: %v\n", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(artifact, "", " ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal failed: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "writing %s: %v\n", *outPath, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d bytes, %d players, %d games)\n",
		*outPath, len(data), artifact.Provenance.NumPlayers, artifact.Provenance.NumGames)
}

func gitSHA() string {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
