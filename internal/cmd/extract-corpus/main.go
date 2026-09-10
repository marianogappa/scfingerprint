// Command extract-corpus extracts feature vectors from a replay corpus and
// writes a labeled feature CSV suitable for internal/cmd/train and internal/cmd/eval.
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
	"github.com/marianogappa/scfingerprint/internal/features"
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

type fileJob struct {
	file string
	rows []replayRow
}

type parseResult struct {
	pfs []features.PlayerFeatures
}

type pendingRow struct {
	row replayRow
	pfs []features.PlayerFeatures
}

type resolveStats struct {
	byToon  int
	byRace  int
	byName  int
	noMatch int
}

func main() {
	dir := flag.String("dir", "", "directory-mode: walk this tree of .rep files and label rows by in-replay player name")
	minGameMin := flag.Float64("min-game-min", 0, "directory-mode: skip games shorter than this many minutes")
	only1v1 := flag.Bool("only-1v1", false, "directory-mode: keep only games with exactly 2 eligible human players")
	metadata := flag.String("metadata", "corpus/replays.jsonl", "path to replays.jsonl")
	identities := flag.String("identities", "corpus/identities.jsonl", "path to identities.jsonl; curated handles seed the per-account name sets that resolve mirror games (empty to disable)")
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

	rows, dropped := dedupe(rows)
	if dropped > 0 {
		log.Printf("dropped %d duplicate (replay, account) rows", dropped)
	}

	byFile := map[string][]replayRow{}
	for _, r := range rows {
		byFile[r.File] = append(byFile[r.File], r)
	}
	log.Printf("%d unique replay files", len(byFile))

	featNames := featNamesTop

	jobs := make([]fileJob, 0, len(byFile))
	for file, rs := range byFile {
		jobs = append(jobs, fileJob{file: file, rows: rs})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].file < jobs[j].file })

	parsed := make(map[string]*parseResult, len(jobs))
	var (
		mu     sync.Mutex
		errCnt int
	)

	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup
	for idx, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, j fileJob) {
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
			mu.Lock()
			parsed[j.file] = &parseResult{pfs: pfs}
			mu.Unlock()

			if (idx+1)%500 == 0 {
				log.Printf("progress: %d/%d files", idx+1, len(jobs))
			}
		}(idx, j)
	}
	wg.Wait()

	var nameSet map[int64]map[string]bool
	if *identities != "" {
		nameSet, err = identityHandles(*identities)
		if err != nil {
			log.Fatalf("reading identities: %v", err)
		}
		log.Printf("seeded name sets for %d accounts from %s", len(nameSet), *identities)
	}

	results, stats := resolveRows(jobs, parsed, nameSet)

	sort.Slice(results, func(i, j int) bool { return lessRow(results[i], results[j]) })

	log.Printf("extracted %d rows (%d by-toon, %d by-race, %d by-name-set, %d unresolvable, %d extract-errors)",
		len(results), stats.byToon, stats.byRace, stats.byName, stats.noMatch, errCnt)

	writeCSV(*out, results, featNames)
	log.Printf("done")
}

// resolveRows resolves each metadata row to the correct in-replay player in two
// passes. Pass 1 resolves rows where the toon matches or the account's race is
// unique in the game (non-mirror), and learns each account's in-replay name set
// on top of the seeded curated handles. Pass 2 resolves mirror games by
// checking whether exactly one player's name appears in the account's name set.
// Rows that remain ambiguous are dropped rather than guessed.
func resolveRows(jobs []fileJob, parsed map[string]*parseResult, nameSet map[int64]map[string]bool) ([]csvRow, resolveStats) {
	if nameSet == nil {
		nameSet = map[int64]map[string]bool{}
	}
	var results []csvRow
	var pending []pendingRow
	var stats resolveStats

	for _, j := range jobs {
		pr := parsed[j.file]
		if pr == nil {
			continue
		}
		for _, row := range j.rows {
			if pf, ok := matchByToon(row.Toon, pr.pfs); ok {
				addToNameSet(nameSet, row.AuroraID, pf.Name)
				results = append(results, makeCSVRow(row, pf))
				stats.byToon++
				continue
			}
			if pf, ok := matchByUniqueRace(row.Race, pr.pfs); ok {
				addToNameSet(nameSet, row.AuroraID, pf.Name)
				results = append(results, makeCSVRow(row, pf))
				stats.byRace++
				continue
			}
			pending = append(pending, pendingRow{row: row, pfs: pr.pfs})
		}
	}

	for _, p := range pending {
		if pf, ok := matchByNameSet(nameSet[p.row.AuroraID], p.pfs); ok {
			results = append(results, makeCSVRow(p.row, pf))
			stats.byName++
			continue
		}
		stats.noMatch++
	}

	return results, stats
}

// identityHandles seeds per-account name sets from the curated identities file:
// each handle's toon is a name the account is known to play under, which
// resolves mirror games the account's own metadata rows cannot (a game recorded
// without a toon, played under an alt the rest of the corpus never labels).
func identityHandles(path string) (map[int64]map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	type handle struct {
		Toon string `json:"toon"`
	}
	type identity struct {
		AuroraID int64    `json:"auroraId"`
		Handles  []handle `json:"handles"`
	}
	ns := map[int64]map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var id identity
		if err := json.Unmarshal([]byte(line), &id); err != nil {
			return nil, fmt.Errorf("parsing identities line: %w", err)
		}
		for _, h := range id.Handles {
			if h.Toon != "" {
				addToNameSet(ns, id.AuroraID, h.Toon)
			}
		}
	}
	return ns, nil
}

func addToNameSet(ns map[int64]map[string]bool, id int64, name string) {
	if ns[id] == nil {
		ns[id] = map[string]bool{}
	}
	ns[id][name] = true
}

func matchByToon(toon string, pfs []features.PlayerFeatures) (features.PlayerFeatures, bool) {
	if toon == "" {
		return features.PlayerFeatures{}, false
	}
	for _, pf := range pfs {
		if pf.Name == toon {
			return pf, true
		}
	}
	for _, pf := range pfs {
		if strings.EqualFold(pf.Name, toon) {
			return pf, true
		}
	}
	return features.PlayerFeatures{}, false
}

func matchByUniqueRace(race string, pfs []features.PlayerFeatures) (features.PlayerFeatures, bool) {
	raceMap := map[string]string{"P": "Protoss", "T": "Terran", "Z": "Zerg"}
	want, ok := raceMap[race]
	if !ok {
		return features.PlayerFeatures{}, false
	}
	var matched features.PlayerFeatures
	var count int
	for _, pf := range pfs {
		if pf.Race == want {
			matched = pf
			count++
		}
	}
	if count == 1 {
		return matched, true
	}
	return features.PlayerFeatures{}, false
}

func matchByNameSet(names map[string]bool, pfs []features.PlayerFeatures) (features.PlayerFeatures, bool) {
	if len(names) == 0 {
		return features.PlayerFeatures{}, false
	}
	var matched features.PlayerFeatures
	var count int
	for _, pf := range pfs {
		if names[pf.Name] {
			matched = pf
			count++
		}
	}
	if count == 1 {
		return matched, true
	}
	return features.PlayerFeatures{}, false
}

func makeCSVRow(row replayRow, pf features.PlayerFeatures) csvRow {
	ts := time.Unix(row.Timestamp/1000, 0).UTC()
	return csvRow{
		file:        row.File,
		player:      strconv.FormatInt(row.AuroraID, 10),
		race:        row.Race,
		matchup:     row.Matchup,
		mapName:     row.Map,
		startTime:   ts.Format("2006-01-02T15:04:05"),
		durationMin: float64(row.Duration) / 60.0,
		numHumans:   2,
		vector:      pf.Vector,
	}
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

// lessRow orders rows by (player, start time, file). The file is the tiebreak
// and not decoration: without it the order of same-timestamp rows varies run to
// run, and with it the chronological blocks a fingerprint serializes into. The
// 343 non-ladder replays all carry the uint32 no-timestamp sentinel, which puts
// 154 of them in ties.
func lessRow(a, b csvRow) bool {
	if a.player != b.player {
		return a.player < b.player
	}
	if a.startTime != b.startTime {
		return a.startTime < b.startTime
	}
	return a.file < b.file
}

// dedupe keeps one row per (replay, account). The upstream harvest records the
// same game twice when a player is picked up by more than one fetch pass, and
// a duplicated row is a duplicated feature vector: it double-weights that game
// in every mean computed downstream and inflates enrollment counts.
func dedupe(rows []replayRow) ([]replayRow, int) {
	type key struct {
		file     string
		auroraID int64
	}
	seen := make(map[key]bool, len(rows))
	out := make([]replayRow, 0, len(rows))
	for _, r := range rows {
		k := key{r.File, r.AuroraID}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out, len(rows) - len(out)
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

	sort.Slice(results, func(i, j int) bool { return lessRow(results[i], results[j]) })

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
