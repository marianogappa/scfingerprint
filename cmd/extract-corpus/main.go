// Command extract-corpus reads the corpus metadata (replays.jsonl) and replay
// files, extracts feature vectors, and writes a labeled feature CSV suitable
// for cmd/train and cmd/eval.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marianogappa/scfingerprint/features"
)

type replayRow struct {
	File      string `json:"file"`
	Timestamp int64  `json:"timestamp"`
	Map       string `json:"map"`
	Matchup   string `json:"matchup"`
	Duration  int    `json:"duration"`
	AuroraID  int64  `json:"auroraId"`
	Toon      string `json:"toon"`
	Race      string `json:"race"`
}

type csvRow struct {
	file        string
	player      string
	race        string
	matchup     string
	mapName     string
	startTime   string
	durationMin float64
	numHumans   int
	vector      []float64
}

func main() {
	metadata := flag.String("metadata", "corpus/replays.jsonl", "path to replays.jsonl")
	replaysDir := flag.String("replays-dir", "corpus", "base directory containing replays/ subdirectory")
	out := flag.String("out", "", "output CSV path (default: stdout)")
	workers := flag.Int("workers", runtime.NumCPU(), "parallel extraction workers")
	flag.Parse()

	rows, err := readMetadata(*metadata)
	if err != nil {
		log.Fatalf("reading metadata: %v", err)
	}
	log.Printf("read %d metadata rows", len(rows))

	byFile := map[string][]replayRow{}
	for _, r := range rows {
		byFile[r.File] = append(byFile[r.File], r)
	}
	log.Printf("%d unique replay files", len(byFile))

	featNames, err := features.FeatureNames(features.Version)
	if err != nil {
		log.Fatal(err)
	}

	type job struct {
		file string
		rows []replayRow
	}
	jobs := make([]job, 0, len(byFile))
	for file, rs := range byFile {
		jobs = append(jobs, job{file: file, rows: rs})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].file < jobs[j].file })

	var (
		mu      sync.Mutex
		results []csvRow
		noMatch int
		errCnt  int
	)

	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup
	for idx, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, j job) {
			defer wg.Done()
			defer func() { <-sem }()

			path := filepath.Join(*replaysDir, j.file)
			pfs, extractErr := features.ExtractFile(path)
			if extractErr != nil {
				mu.Lock()
				errCnt++
				mu.Unlock()
				if (idx+1)%1000 == 0 || errCnt <= 10 {
					log.Printf("WARN: extract %s: %v", j.file, extractErr)
				}
				return
			}

			pfByName := map[string]features.PlayerFeatures{}
			for _, pf := range pfs {
				pfByName[pf.Name] = pf
			}

			var local []csvRow
			var localNoMatch int
			for _, row := range j.rows {
				pf, ok := pfByName[row.Toon]
				if !ok {
					for _, c := range pfs {
						if strings.EqualFold(c.Name, row.Toon) {
							pf = c
							ok = true
							break
						}
					}
				}
				if !ok {
					raceMap := map[string]string{"P": "Protoss", "T": "Terran", "Z": "Zerg"}
					want := raceMap[row.Race]
					for _, c := range pfs {
						if c.Race == want {
							pf = c
							ok = true
							break
						}
					}
				}
				if !ok {
					localNoMatch++
					continue
				}

				ts := time.Unix(row.Timestamp/1000, 0).UTC()
				local = append(local, csvRow{
					file:        row.File,
					player:      strconv.FormatInt(row.AuroraID, 10),
					race:        row.Race,
					matchup:     row.Matchup,
					mapName:     row.Map,
					startTime:   ts.Format("2006-01-02T15:04:05"),
					durationMin: float64(row.Duration) / 60.0,
					numHumans:   2,
					vector:      pf.Vector,
				})
			}

			mu.Lock()
			results = append(results, local...)
			noMatch += localNoMatch
			mu.Unlock()

			if (idx+1)%500 == 0 {
				log.Printf("progress: %d/%d files", idx+1, len(jobs))
			}
		}(idx, j)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		if results[i].player != results[j].player {
			return results[i].player < results[j].player
		}
		return results[i].startTime < results[j].startTime
	})

	log.Printf("extracted %d rows (%d no-match, %d extract-errors)", len(results), noMatch, errCnt)

	var w *csv.Writer
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		w = csv.NewWriter(f)
	} else {
		w = csv.NewWriter(os.Stdout)
	}

	header := append([]string{"file", "player", "race", "matchup", "map", "start_time", "duration_min", "num_humans"}, featNames...)
	if err := w.Write(header); err != nil {
		log.Fatal(err)
	}

	for _, row := range results {
		rec := make([]string, 0, 8+len(row.vector))
		rec = append(rec, row.file, row.player, row.race, row.matchup, row.mapName,
			row.startTime, fmt.Sprintf("%.5f", row.durationMin), strconv.Itoa(row.numHumans))
		for _, v := range row.vector {
			rec = append(rec, fmt.Sprintf("%.5f", v))
		}
		if err := w.Write(rec); err != nil {
			log.Fatal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		log.Fatal(err)
	}
	log.Printf("done")
}

func readMetadata(path string) ([]replayRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []replayRow
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var r replayRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("parsing line: %w", err)
		}
		rows = append(rows, r)
	}
	return rows, nil
}
