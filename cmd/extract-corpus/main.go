// Command extract-corpus extracts feature vectors from a replay corpus and
// writes a labeled feature CSV suitable for cmd/train and cmd/eval.
//
// Two labelling modes:
//
//   - metadata mode (default): reads corpus metadata (replays.jsonl) and
//     labels each row with the replay's aurora account ID.
//   - directory mode (-dir): walks a directory tree of .rep files and labels
//     each row with the in-replay player name. Within a single curated corpus
//     the same name is the same human, which is the labelling the research
//     spike's reference corpora rely on.
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

	"github.com/icza/screp/repparser"
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
	dir := flag.String("dir", "", "directory-mode: walk this tree of .rep files and label rows by in-replay player name")
	minGameMin := flag.Float64("min-game-min", 0, "directory-mode: skip games shorter than this many minutes")
	only1v1 := flag.Bool("only-1v1", false, "directory-mode: keep only games with exactly 2 eligible human players")
	metadata := flag.String("metadata", "corpus/replays.jsonl", "path to replays.jsonl")
	replaysDir := flag.String("replays-dir", "corpus", "base directory containing replays/ subdirectory")
	out := flag.String("out", "", "output CSV path (default: stdout)")
	workers := flag.Int("workers", runtime.NumCPU(), "parallel extraction workers")
	flag.Parse()

	featNamesTop, err := features.FeatureNames(features.Version)
	if err != nil {
		log.Fatal(err)
	}

	if *dir != "" {
		rows := extractDir(*dir, *workers, *minGameMin, *only1v1)
		writeCSV(*out, rows, featNamesTop)
		log.Printf("done")
		return
	}

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

	featNames := featNamesTop

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

	writeCSV(*out, results, featNames)
	log.Printf("done")
}

// writeCSV writes the metadata header plus one row per player-game.
func writeCSV(out string, results []csvRow, featNames []string) {
	var w *csv.Writer
	if out != "" {
		f, err := os.Create(out)
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

// extractDir walks a directory tree of .rep files and extracts one row per
// eligible human player, labelled by in-replay player name. Rows are sorted
// by (player, start_time) so downstream chronological splits are stable.
func extractDir(root string, workers int, minGameMin float64, only1v1 bool) []csvRow {
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".rep") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(paths)
	log.Printf("found %d replay files under %s", len(paths), root)

	var (
		mu      sync.Mutex
		results []csvRow
		errCnt  int
		skipped int
		sem     = make(chan struct{}, workers)
		wg      sync.WaitGroup
	)

	for idx, path := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, path string) {
			defer wg.Done()
			defer func() { <-sem }()

			r, parseErr := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
			if parseErr != nil {
				mu.Lock()
				errCnt++
				if errCnt <= 10 {
					log.Printf("WARN: parse %s: %v", path, parseErr)
				}
				mu.Unlock()
				return
			}
			pfs, extractErr := features.Extract(r)
			if extractErr != nil {
				mu.Lock()
				errCnt++
				mu.Unlock()
				return
			}
			durationMin := r.Header.Duration().Minutes()
			if durationMin < minGameMin || (only1v1 && len(pfs) != 2) {
				mu.Lock()
				skipped++
				mu.Unlock()
				return
			}

			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			startTime := r.Header.StartTime.UTC().Format("2006-01-02T15:04:05")
			matchup := r.Header.Matchup()

			local := make([]csvRow, 0, len(pfs))
			for _, pf := range pfs {
				local = append(local, csvRow{
					file:        rel,
					player:      pf.Name,
					race:        raceLetter(pf.Race),
					matchup:     matchup,
					mapName:     r.Header.Map,
					startTime:   startTime,
					durationMin: durationMin,
					numHumans:   len(pfs),
					vector:      pf.Vector,
				})
			}

			mu.Lock()
			results = append(results, local...)
			mu.Unlock()

			if (idx+1)%500 == 0 {
				log.Printf("progress: %d/%d files", idx+1, len(paths))
			}
		}(idx, path)
	}
	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
		if results[i].player != results[j].player {
			return results[i].player < results[j].player
		}
		if results[i].startTime != results[j].startTime {
			return results[i].startTime < results[j].startTime
		}
		return results[i].file < results[j].file
	})

	log.Printf("extracted %d rows from %d files (%d skipped, %d errors)", len(results), len(paths), skipped, errCnt)
	return results
}

// raceLetter normalises screp's race names to the single-letter codes the
// metadata-mode CSVs use, so both modes produce comparable race columns.
func raceLetter(race string) string {
	if race == "" {
		return ""
	}
	switch race[0] {
	case 'Z', 'T', 'P':
		return race[:1]
	}
	return race
}
