// Command era-probe scores groups of old-era games against the shipped 2026
// catalog to measure cross-era identification (issue #13). Each group is a
// confirmed identity's games from a historical corpus (e.g. YGOSU replays
// where the in-replay ID is unambiguous, like "HwaSeungOZ Jaedong"); the tool
// aggregates them into one n-aware probe, ranks all catalog entries, and
// reports where the true identity lands.
//
// Usage:
//
//	go run ./cmd/era-probe -csv ygosu.csv \
//	  -groups "jaedong=HwaSeungOZ Jaedong;best=Best[WHITE],SKTelecomT1Best"
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/marianogappa/scfingerprint/dataset"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

func main() {
	csvPath := flag.String("csv", "", "old-era labeled feature CSV, player column = in-replay name (required)")
	groupsFlag := flag.String("groups", "", "catalogID=name1,name2;catalogID2=... confirmed identity groups (required)")
	flag.Parse()
	if *csvPath == "" || *groupsFlag == "" {
		log.Fatal("-csv and -groups are required")
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		log.Fatal(err)
	}
	ids, fps, err := dataset.LoadEmbedded()
	if err != nil {
		log.Fatal(err)
	}

	type entry struct {
		id   string
		proj []float64
	}
	var catalog []entry
	for i, id := range ids {
		p, err := fps[i].Projected(scorer)
		if err != nil {
			log.Fatal(err)
		}
		catalog = append(catalog, entry{id.ID, p})
	}

	type group struct {
		pro   string
		names map[string]bool
	}
	var groups []group
	for _, seg := range strings.Split(*groupsFlag, ";") {
		parts := strings.SplitN(seg, "=", 2)
		if len(parts) != 2 {
			log.Fatalf("bad -groups segment %q", seg)
		}
		names := map[string]bool{}
		for _, n := range strings.Split(parts[1], ",") {
			names[strings.TrimSpace(n)] = true
		}
		groups = append(groups, group{pro: strings.TrimSpace(parts[0]), names: names})
	}

	fmt.Printf("%-10s %6s %5s %8s %8s  %s\n", "identity", "games", "rank", "z", "margin", "top3")
	for _, g := range groups {
		var whitened [][]float64
		for _, s := range samples {
			if g.names[s.Player] {
				w, err := scorer.Transform(s.Vector)
				if err != nil {
					log.Fatal(err)
				}
				whitened = append(whitened, w)
			}
		}
		if len(whitened) == 0 {
			fmt.Printf("%-10s no games found for %v\n", g.pro, g.names)
			continue
		}
		type hit struct {
			id string
			z  float64
		}
		var hits []hit
		for _, c := range catalog {
			sc, err := scorer.Score(whitened, c.proj)
			if err != nil {
				log.Fatal(err)
			}
			hits = append(hits, hit{c.id, sc.Z})
		}
		sort.Slice(hits, func(i, j int) bool { return hits[i].z > hits[j].z })
		rank, z := -1, 0.0
		for i, h := range hits {
			if h.id == g.pro {
				rank, z = i+1, h.z
			}
		}
		margin := 0.0
		if rank == 1 && len(hits) > 1 {
			margin = z - hits[1].z
		} else if rank > 1 {
			margin = z - hits[0].z
		}
		var top3 []string
		for _, h := range hits[:min(3, len(hits))] {
			top3 = append(top3, fmt.Sprintf("%s=%.2f", h.id, h.z))
		}
		fmt.Printf("%-10s %6d %5d %8.2f %8.2f  %s\n", g.pro, len(whitened), rank, z, margin, strings.Join(top3, ", "))
	}
}
