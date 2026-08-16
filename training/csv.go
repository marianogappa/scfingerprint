package training

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/marianogappa/scfingerprint/features"
)

// Sample holds one player-game row from a labeled feature CSV.
type Sample struct {
	File        string
	Player      string
	Race        string
	Matchup     string
	Map         string
	StartTime   time.Time
	DurationMin float64
	NumHumans   int
	Vector      []float64
}

// ReadCSV reads a labeled feature CSV and returns samples sorted by
// (Player, StartTime) for chronological splitting. The header must
// contain all feature names from features.FeatureNames(3) in the
// canonical order after the metadata columns.
func ReadCSV(path string) ([]Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("training: opening %s: %w", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("training: reading header from %s: %w", path, err)
	}

	names, _ := features.FeatureNames(features.Version)
	featStart, err := findFeatureStart(header, names)
	if err != nil {
		return nil, fmt.Errorf("training: %s: %w", path, err)
	}

	colIdx := headerIndex(header)

	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("training: reading %s: %w", path, err)
	}

	samples := make([]Sample, 0, len(records))
	for lineNo, rec := range records {
		if len(rec) < featStart+len(names) {
			return nil, fmt.Errorf("training: %s line %d: expected %d columns, got %d", path, lineNo+2, featStart+len(names), len(rec))
		}
		vec := make([]float64, len(names))
		for i := range names {
			v, err := strconv.ParseFloat(rec[featStart+i], 64)
			if err != nil {
				return nil, fmt.Errorf("training: %s line %d col %d: %w", path, lineNo+2, featStart+i, err)
			}
			vec[i] = v
		}
		s := Sample{
			File:   getCol(rec, colIdx, "file"),
			Player: getCol(rec, colIdx, "player"),
			Race:   getCol(rec, colIdx, "race"),
			Vector: vec,
		}
		if idx, ok := colIdx["matchup"]; ok {
			s.Matchup = rec[idx]
		}
		if idx, ok := colIdx["map"]; ok {
			s.Map = rec[idx]
		}
		if idx, ok := colIdx["start_time"]; ok {
			t, err := time.Parse("2006-01-02T15:04:05", rec[idx])
			if err == nil {
				s.StartTime = t
			}
		}
		if idx, ok := colIdx["duration_min"]; ok {
			v, _ := strconv.ParseFloat(rec[idx], 64)
			s.DurationMin = v
		}
		if idx, ok := colIdx["num_humans"]; ok {
			v, _ := strconv.Atoi(rec[idx])
			s.NumHumans = v
		}
		samples = append(samples, s)
	}

	sort.SliceStable(samples, func(i, j int) bool {
		if samples[i].Player != samples[j].Player {
			return samples[i].Player < samples[j].Player
		}
		return samples[i].StartTime.Before(samples[j].StartTime)
	})
	return samples, nil
}

func findFeatureStart(header, names []string) (int, error) {
	for start := 0; start+len(names) <= len(header); start++ {
		if header[start] == names[0] {
			match := true
			for i, n := range names {
				if header[start+i] != n {
					match = false
					break
				}
			}
			if match {
				return start, nil
			}
		}
	}
	return 0, fmt.Errorf("header does not contain the %d v%d feature columns starting with %q", len(names), features.Version, names[0])
}

func headerIndex(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[h] = i
	}
	return m
}

func getCol(rec []string, idx map[string]int, name string) string {
	if i, ok := idx[name]; ok && i < len(rec) {
		return rec[i]
	}
	return ""
}
