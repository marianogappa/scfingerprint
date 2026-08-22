// Command alias-discovery scans every account in a labeled feature CSV
// against the shipped pro catalog and reports likely aliases (issue #15).
//
// Each account's games are aggregated into one n-aware calibrated probe and
// scored 1:N against every catalog enrollment. Accounts are classified by
// their relationship to the ground-truth pro registry so the output doubles
// as an open-set evaluation:
//
//   - enrolled:    the account IS a catalog enrollment (leakage; sanity only)
//   - known-alias: pro-labeled, pro in catalog, different account — the true
//     open-set discovery test
//   - known-other: pro-labeled but the pro is not catalogued — should NOT
//     match anyone confidently
//   - unlabeled:   no pro label — novel discovery candidates
//
// Usage:
//
//	go run ./cmd/alias-discovery -csv corpus.csv -pros corpus/pros_merged.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/marianogappa/scfingerprint/dataset"
	"github.com/marianogappa/scfingerprint/hygiene"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

type row struct {
	AuroraID   string  `json:"aurora_id"`
	Class      string  `json:"class"`
	TruePro    string  `json:"true_pro,omitempty"`
	Games      int     `json:"games"`
	TopMatch   string  `json:"top_match"`
	Z          float64 `json:"z"`
	SecondZ    float64 `json:"second_z"`
	Clears1e3  bool    `json:"clears_fpr_1e3"`
	TopCorrect *bool   `json:"top_correct,omitempty"` // known-alias rows only
	Disproved  bool    `json:"disproved_by_co_occurrence,omitempty"`
}

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV, player column = aurora ID (required)")
	prosPath := flag.String("pros", "corpus/pros_merged.json", "pro name → aurora IDs ground truth")
	minGames := flag.Int("min-games", 3, "minimum games per probe account")
	asJSON := flag.Bool("json", false, "emit JSON rows instead of a table")
	flag.Parse()
	if *csvPath == "" {
		log.Fatal("-csv is required")
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	byAccount := map[string][]training.Sample{}
	fileToPlayers := map[string][]string{}
	for _, s := range samples {
		byAccount[s.Player] = append(byAccount[s.Player], s)
		fileToPlayers[s.File] = append(fileToPlayers[s.File], s.Player)
	}

	proByAurora, err := loadProMapping(*prosPath)
	if err != nil {
		log.Fatal(err)
	}

	ids, fps, err := dataset.LoadEmbedded()
	if err != nil {
		log.Fatal(err)
	}
	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		log.Fatal(err)
	}

	// Catalog entry → its enrolled aurora ID (manifest majority vote), and
	// each entry's projected embedding.
	type entry struct {
		id     string
		aurora string
		proj   []float64
	}
	var catalog []entry
	enrolledAurora := map[string]string{} // aurora → catalog id
	for i, id := range ids {
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
		proj, err := fps[i].Projected(scorer)
		if err != nil {
			log.Fatal(err)
		}
		catalog = append(catalog, entry{id: id.ID, aurora: best, proj: proj})
		if best != "" {
			enrolledAurora[best] = id.ID
		}
	}

	// Catalog ids are lowercased pro names; index for ground-truth checks.
	catalogHasPro := map[string]bool{}
	for _, e := range catalog {
		catalogHasPro[e.id] = true
	}

	co := hygiene.BuildCoOccurrence(hygiene.ManifestFromSamples(samples))

	accounts := make([]string, 0, len(byAccount))
	for a, ss := range byAccount {
		if len(ss) >= *minGames {
			accounts = append(accounts, a)
		}
	}
	sort.Strings(accounts)

	auroraOfEntry := map[string]string{}
	for _, e := range catalog {
		auroraOfEntry[e.id] = e.aurora
	}

	var rows []row
	for _, acct := range accounts {
		ss := byAccount[acct]
		var whitened [][]float64
		for _, s := range ss {
			w, err := scorer.Transform(s.Vector)
			if err != nil {
				log.Fatal(err)
			}
			whitened = append(whitened, w)
		}

		top, second := "", ""
		topZ, secondZ := -1e18, -1e18
		topClears := false
		for _, e := range catalog {
			sc, err := scorer.Score(whitened, e.proj)
			if err != nil {
				log.Fatal(err)
			}
			if sc.Z > topZ {
				second, secondZ = top, topZ
				top, topZ = e.id, sc.Z
				topClears = sc.OperatingPoints["fpr_1e3"]
			} else if sc.Z > secondZ {
				second, secondZ = e.id, sc.Z
			}
		}
		_ = second

		truePro := proByAurora[acct]
		class := "unlabeled"
		var topCorrect *bool
		switch {
		case enrolledAurora[acct] != "":
			class = "enrolled"
		case truePro != "" && catalogHasPro[lower(truePro)]:
			class = "known-alias"
			v := top == lower(truePro)
			topCorrect = &v
		case truePro != "":
			class = "known-other"
		}

		// Co-occurrence disproof: if this account and the matched pro's
		// enrolled account ever played in the same game, they cannot be
		// the same person.
		disproved := false
		if enrolled := auroraOfEntry[top]; enrolled != "" && enrolled != acct {
			disproved = co.Disproved(acct, enrolled)
		}

		rows = append(rows, row{
			AuroraID:   acct,
			Class:      class,
			TruePro:    truePro,
			Games:      len(ss),
			TopMatch:   top,
			Z:          topZ,
			SecondZ:    secondZ,
			Clears1e3:  topClears,
			TopCorrect: topCorrect,
			Disproved:  disproved,
		})
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].Z > rows[j].Z })

	if *asJSON {
		out, _ := json.MarshalIndent(rows, "", " ")
		fmt.Println(string(out))
		return
	}

	// Summary per class.
	classCount := map[string]int{}
	aliasCorrect, aliasTotal := 0, 0
	aliasCorrectClearing, aliasClearing := 0, 0
	falseAlarm := map[string]int{}
	for _, r := range rows {
		classCount[r.Class]++
		switch r.Class {
		case "known-alias":
			aliasTotal++
			if r.TopCorrect != nil && *r.TopCorrect {
				aliasCorrect++
			}
			if r.Clears1e3 {
				aliasClearing++
				if r.TopCorrect != nil && *r.TopCorrect {
					aliasCorrectClearing++
				}
			}
		case "known-other", "unlabeled":
			if r.Clears1e3 {
				falseAlarm[r.Class]++
			}
		}
	}

	fmt.Printf("catalog: %d enrollments   probe accounts: %d (min %d games)\n\n", len(catalog), len(rows), *minGames)
	fmt.Printf("class counts: %v\n\n", classCount)
	if aliasTotal > 0 {
		fmt.Printf("known-alias open-set test: top-1 correct %d/%d (%.1f%%)\n", aliasCorrect, aliasTotal, 100*float64(aliasCorrect)/float64(aliasTotal))
		fmt.Printf("  clearing fpr_1e3: %d, of which correct: %d\n\n", aliasClearing, aliasCorrectClearing)
	}
	fmt.Printf("false alarms clearing fpr_1e3: known-other=%d/%d unlabeled=%d/%d\n\n",
		falseAlarm["known-other"], classCount["known-other"], falseAlarm["unlabeled"], classCount["unlabeled"])

	fmt.Printf("%-12s %-12s %-16s %6s %-16s %8s %8s %6s %s\n", "aurora", "class", "true_pro", "games", "top_match", "z", "2nd_z", "1e-3", "notes")
	for _, r := range rows {
		var notes []string
		if r.TopCorrect != nil {
			if *r.TopCorrect {
				notes = append(notes, "✓")
			} else {
				notes = append(notes, "✗")
			}
		}
		if r.Disproved {
			notes = append(notes, "DISPROVED(co-occurrence)")
		}
		clears := ""
		if r.Clears1e3 {
			clears = "yes"
		}
		fmt.Printf("%-12s %-12s %-16s %6d %-16s %8.2f %8.2f %6s %s\n",
			r.AuroraID, r.Class, r.TruePro, r.Games, r.TopMatch, r.Z, r.SecondZ, clears, strings.Join(notes, " "))
	}
}

func lower(s string) string {
	out := []rune(s)
	for i, r := range out {
		if r >= 'A' && r <= 'Z' {
			out[i] = r + 32
		}
	}
	return string(out)
}

func loadProMapping(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string][]int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, ids := range raw {
		for _, id := range ids {
			out[fmt.Sprintf("%d", id)] = name
		}
	}
	return out, nil
}
