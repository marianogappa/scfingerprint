package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/icza/screp/repparser"
	"github.com/marianogappa/scfingerprint/features"
)

// gameObs is one observed player-game: the extracted features plus where it
// came from.
type gameObs struct {
	file string
	pf   features.PlayerFeatures
}

// collectReplays resolves a --dir flag and/or positional .rep arguments into
// a sorted list of replay file paths.
func collectReplays(dir string, files []string) ([]string, error) {
	var paths []string
	if dir != "" {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(strings.ToLower(path), ".rep") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walking %s: %w", dir, err)
		}
	}
	paths = append(paths, files...)
	if len(paths) == 0 {
		return nil, fmt.Errorf("no replay files found")
	}
	sort.Strings(paths)
	return paths, nil
}

// extractAll parses every replay and extracts features for all eligible
// human players.
func extractAll(paths []string) ([]gameObs, error) {
	var out []gameObs
	for _, path := range paths {
		r, err := repparser.ParseFileConfig(path, repparser.Config{Commands: true})
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		pfs, err := features.Extract(r)
		if err != nil {
			return nil, fmt.Errorf("extracting %s: %w", path, err)
		}
		for _, pf := range pfs {
			out = append(out, gameObs{file: path, pf: pf})
		}
	}
	return out, nil
}

// selectObs filters observations to one identity: by player name when name
// is set, by slot ID when playerID >= 0, or — when neither is given — only
// if every replay has exactly one eligible player.
func selectObs(obs []gameObs, name string, playerID int) ([]gameObs, error) {
	if name != "" {
		var out []gameObs
		for _, o := range obs {
			if o.pf.Name == name {
				out = append(out, o)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("player %q not found in any replay", name)
		}
		return out, nil
	}
	if playerID >= 0 {
		var out []gameObs
		for _, o := range obs {
			if int(o.pf.PlayerID) == playerID {
				out = append(out, o)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("player slot %d not found in any replay", playerID)
		}
		return out, nil
	}
	byFile := map[string]int{}
	for _, o := range obs {
		byFile[o.file]++
	}
	for file, n := range byFile {
		if n > 1 {
			return nil, fmt.Errorf("%s has %d eligible players; select one with --name or --player", file, n)
		}
	}
	return obs, nil
}
