// Command catalog-check measures 1:N top-1 identification against the shipped
// catalog, keyed on aurora ID rather than the unreliable proName registry
// labels (issue #45).
//
// For each catalogued identity it maps the replay manifest back to the aurora
// ID in the labeled CSV, splits that account's games chronologically, rebuilds
// a leakage-free enrollment from the first half, and probes 1:N with the
// second half. It reports top-1 accuracy against both the rebuilt catalog
// (honest, no leakage) and the shipped catalog (whose enrollments contain the
// probe games — noted as an upper bound).
//
// Usage:
//
//	go run ./cmd/catalog-check -csv corpus.csv
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"github.com/marianogappa/scfingerprint/dataset"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

const minZLead = 2.0 // api.go's default lead threshold

type enrollment struct {
	id       string // catalog identity id
	auroraID string
	proj     []float64
}

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV, player column = aurora ID (required)")
	probeN := flag.Int("n", 1, "games per probe (1 = single game)")
	flag.Parse()
	if *csvPath == "" {
		log.Fatal("-csv is required")
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	byPlayer := map[string][]training.Sample{}
	fileToPlayers := map[string][]string{}
	for _, s := range samples {
		byPlayer[s.Player] = append(byPlayer[s.Player], s)
		fileToPlayers[s.File] = append(fileToPlayers[s.File], s.Player)
	}

	ids, fps, err := dataset.LoadEmbedded()
	if err != nil {
		log.Fatal(err)
	}
	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		log.Fatal(err)
	}

	// Map each catalog identity to its aurora ID by majority vote over its
	// replay manifest (all manifest files belong to the enrolled account).
	identityAurora := map[string]string{}
	for _, id := range ids {
		votes := map[string]int{}
		for _, f := range id.ReplayManifest {
			for _, p := range fileToPlayers[f] {
				votes[p]++
			}
		}
		best, bestN := "", 0
		for p, n := range votes {
			if n > bestN {
				best, bestN = p, n
			}
		}
		if best == "" {
			log.Printf("WARN: %s: no manifest file found in CSV — skipping", id.ID)
			continue
		}
		if bestN < len(id.ReplayManifest)/2 {
			log.Printf("WARN: %s: majority aurora ID %s covers only %d/%d manifest files", id.ID, best, bestN, len(id.ReplayManifest))
		}
		identityAurora[id.ID] = best
	}
	log.Printf("catalog: %d identities, %d mapped to aurora IDs", len(ids), len(identityAurora))

	// Rebuilt catalog: first-half enrollments via the dataset path
	// (raw mean → Projected). Probes: second-half games.
	var rebuilt []enrollment
	var shipped []enrollment
	type probe struct {
		auroraID string
		whitened [][]float64
	}
	var probes []probe

	for i, id := range ids {
		aurora, ok := identityAurora[id.ID]
		if !ok {
			continue
		}
		games := byPlayer[aurora] // already sorted by (Player, StartTime)
		if len(games) < 4 {
			log.Printf("WARN: %s (%s): only %d games — skipping", id.ID, aurora, len(games))
			continue
		}
		half := len(games) / 2

		fp := fingerprint.New(fingerprint.Meta{Label: id.ID})
		for _, g := range games[:half] {
			if err := fp.Add(g.Vector, g.Race); err != nil {
				log.Fatal(err)
			}
		}
		proj, err := fp.Projected(scorer)
		if err != nil {
			log.Fatal(err)
		}
		rebuilt = append(rebuilt, enrollment{id: id.ID, auroraID: aurora, proj: proj})

		shippedProj, err := fps[i].Projected(scorer)
		if err != nil {
			log.Fatal(err)
		}
		shipped = append(shipped, enrollment{id: id.ID, auroraID: aurora, proj: shippedProj})

		heldOut := games[half:]
		for start := 0; start+*probeN <= len(heldOut); start += *probeN {
			var ws [][]float64
			for _, g := range heldOut[start : start+*probeN] {
				w, err := scorer.Transform(g.Vector)
				if err != nil {
					log.Fatal(err)
				}
				ws = append(ws, w)
			}
			probes = append(probes, probe{auroraID: aurora, whitened: ws})
		}
	}

	log.Printf("rebuilt %d enrollments, %d probes (n=%d)", len(rebuilt), len(probes), *probeN)

	measure := func(name string, catalog []enrollment) {
		var correct, wrongClears1e3, noLead int
		for _, pr := range probes {
			type hit struct {
				auroraID string
				z        float64
				clears   bool
			}
			var best *hit
			for _, e := range catalog {
				sc, err := scorer.Score(pr.whitened, e.proj)
				if err != nil {
					log.Fatal(err)
				}
				if best == nil || sc.Z > best.z {
					best = &hit{auroraID: e.auroraID, z: sc.Z, clears: sc.OperatingPoints["fpr_1e3"]}
				}
			}
			switch {
			case best == nil || best.z < minZLead:
				noLead++
			case best.auroraID == pr.auroraID:
				correct++
			default:
				if best.clears {
					wrongClears1e3++
				}
			}
		}
		n := len(probes)
		fmt.Printf("\n== %s catalog (%d identities, %d probes, n=%d) ==\n", name, len(catalog), n, *probeN)
		fmt.Printf("top-1 correct:            %d/%d = %.1f%%\n", correct, n, 100*float64(correct)/float64(n))
		fmt.Printf("wrong top-1 clears 1e-3:  %d/%d = %.1f%%\n", wrongClears1e3, n, 100*float64(wrongClears1e3)/float64(n))
		fmt.Printf("no result above lead z=%.1f: %d/%d = %.1f%%\n", minZLead, noLead, n, 100*float64(noLead)/float64(n))
	}

	sort.Slice(probes, func(i, j int) bool { return probes[i].auroraID < probes[j].auroraID })
	measure("rebuilt (leakage-free)", rebuilt)
	measure("shipped (probes inside enrollments — upper bound)", shipped)

	os.Exit(0)
}
