package features

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var spikeDir = flag.String("spike-dir", "../../screpdb/scratch_fingerprint", "path to the research-spike extractor directory")

type parityDeviation struct {
	replay, player, feature string
	goVal, spikeVal         float64
	absDiff                 float64
}

// spikeCSVTolerance accounts for the spike's fmt.Sprintf("%.5f", v) truncation.
// A value formatted to 5 decimal places has rounding error up to 5e-6. For
// features with magnitude > 1 (e.g. APM ≈ 300), the absolute error from
// truncation can be up to 5e-6. We use 6e-6 to leave a small margin.
const spikeCSVTolerance = 6e-6

func TestParityWithSpike(t *testing.T) {
	mainGo := resolveSpike(t)

	t.Run("golden_replays", func(t *testing.T) {
		replayDir := filepath.Join("testdata")
		replaySources := make(map[string]string, len(fixtureReplays))
		for _, name := range fixtureReplays {
			replaySources[name] = filepath.Join(replayDir, name)
		}
		runParityCheck(t, mainGo, replaySources)
	})

	t.Run("corpus_sample", func(t *testing.T) {
		corpusDir := filepath.Join("..", "corpus", "replays")
		entries, err := os.ReadDir(corpusDir)
		if err != nil {
			t.Skipf("corpus replays not available: %v", err)
		}
		// LFS pointer files are 130–140 bytes; skip if replays aren't fetched.
		if len(entries) > 0 {
			info, err := entries[0].Info()
			if err == nil && info.Size() < 200 {
				t.Skip("corpus replays are LFS pointers (run git lfs pull)")
			}
		}

		const sampleSize = 50
		replaySources := make(map[string]string)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".rep") {
				continue
			}
			replaySources[e.Name()] = filepath.Join(corpusDir, e.Name())
			if len(replaySources) >= sampleSize {
				break
			}
		}
		if len(replaySources) == 0 {
			t.Skip("no .rep files in corpus")
		}
		runParityCheck(t, mainGo, replaySources)
	})
}

func resolveSpike(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(*spikeDir)
	if err != nil {
		t.Fatalf("resolving spike-dir: %v", err)
	}
	mainGo := filepath.Join(abs, "main.go")
	if _, err := os.Stat(mainGo); os.IsNotExist(err) {
		t.Skipf("spike extractor not found at %s — skipping parity test", mainGo)
	}
	return mainGo
}

func runParityCheck(t *testing.T, mainGo string, replaySources map[string]string) {
	t.Helper()

	tmpDir := t.TempDir()
	var replays []string
	for name, src := range replaySources {
		dst := filepath.Join(tmpDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("reading %s: %v", src, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("writing %s: %v", dst, err)
		}
		replays = append(replays, name)
	}

	csvPath := filepath.Join(tmpDir, "spike_output.csv")
	cmd := exec.Command("go", "run", mainGo, tmpDir, csvPath)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("running spike extractor: %v", err)
	}

	spikeResults, err := parseSpikeCSV(csvPath)
	if err != nil {
		t.Fatalf("parsing spike CSV: %v", err)
	}

	names, _ := FeatureNames(Version)
	goResults := map[string]map[string][]float64{}
	for _, name := range replays {
		pfs, err := ExtractFile(replaySources[name])
		if err != nil {
			t.Fatalf("ExtractFile(%s): %v", name, err)
		}
		m := map[string][]float64{}
		for _, pf := range pfs {
			m[pf.Name] = pf.Vector
		}
		goResults[name] = m
	}

	var maxDevByGroup = map[string]parityDeviation{}
	var totalCompared, totalPlayers int

	for _, replay := range replays {
		spikePlayers, ok := spikeResults[replay]
		if !ok {
			continue
		}
		goPlayers, ok := goResults[replay]
		if !ok {
			t.Errorf("Go extractor produced no results for %s", replay)
			continue
		}

		for playerName, spikeVec := range spikePlayers {
			goVec, ok := goPlayers[playerName]
			if !ok {
				t.Errorf("%s: player %q in spike output but not in Go output", replay, playerName)
				continue
			}
			if len(spikeVec) != len(goVec) {
				t.Fatalf("%s/%s: spike has %d dims, Go has %d", replay, playerName, len(spikeVec), len(goVec))
			}
			totalPlayers++

			for d := range goVec {
				diff := math.Abs(goVec[d] - spikeVec[d])
				totalCompared++
				group := featureGroup(names[d])

				prev, exists := maxDevByGroup[group]
				if !exists || diff > prev.absDiff {
					maxDevByGroup[group] = parityDeviation{
						replay: replay, player: playerName, feature: names[d],
						goVal: goVec[d], spikeVal: spikeVec[d], absDiff: diff,
					}
				}

				if diff > spikeCSVTolerance {
					t.Errorf("REAL MISMATCH %s/%s dim %d (%s): go=%.15g spike=%.15g diff=%.15g (exceeds CSV tolerance)",
						replay, playerName, d, names[d], goVec[d], spikeVec[d], diff)
				}
			}
		}

		for playerName := range goPlayers {
			if _, ok := spikePlayers[playerName]; !ok {
				t.Errorf("%s: player %q in Go output but not in spike output", replay, playerName)
			}
		}
	}

	t.Logf("\n=== Parity Report ===")
	t.Logf("Compared %d dimension-values across %d replays (%d players)", totalCompared, len(replays), totalPlayers)
	t.Logf("")
	t.Logf("Max absolute deviation per feature group:")
	for _, group := range sortedKeys(maxDevByGroup) {
		d := maxDevByGroup[group]
		if d.absDiff == 0 {
			t.Logf("  %-25s exact match", group)
		} else {
			t.Logf("  %-25s %.2e  (feature=%s, replay=%s, player=%s)", group, d.absDiff, d.feature, d.replay, d.player)
		}
	}
	t.Logf("")
	t.Logf("All deviations are within %.0e (spike CSV fmt.Sprintf(\"%%.5f\") truncation).", spikeCSVTolerance)
}

func parseSpikeCSV(path string) (map[string]map[string][]float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	records, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("CSV has only %d rows (need header + data)", len(records))
	}

	header := records[0]
	// metadata columns: file, player, race, matchup, map, start_time, duration_min, num_humans
	const metaCols = 8
	if len(header) < metaCols+1 {
		return nil, fmt.Errorf("CSV has only %d columns", len(header))
	}

	fileIdx, playerIdx := -1, -1
	for i, h := range header[:metaCols] {
		switch h {
		case "file":
			fileIdx = i
		case "player":
			playerIdx = i
		}
	}
	if fileIdx < 0 || playerIdx < 0 {
		return nil, fmt.Errorf("CSV missing 'file' or 'player' column in header")
	}

	result := map[string]map[string][]float64{}
	for _, row := range records[1:] {
		replayFile := filepath.Base(row[fileIdx])
		playerName := row[playerIdx]

		vec := make([]float64, len(row)-metaCols)
		for i := metaCols; i < len(row); i++ {
			v, err := strconv.ParseFloat(row[i], 64)
			if err != nil {
				return nil, fmt.Errorf("row %s/%s col %d (%s): %w", replayFile, playerName, i, header[i], err)
			}
			vec[i-metaCols] = v
		}

		if result[replayFile] == nil {
			result[replayFile] = map[string][]float64{}
		}
		result[replayFile][playerName] = vec
	}
	return result, nil
}

func featureGroup(name string) string {
	groups := []struct {
		prefix string
		group  string
	}{
		{"apm", "apm_tempo"},
		{"eapm", "apm_tempo"},
		{"redundancy", "apm_tempo"},
		{"apm_early", "apm_tempo"},
		{"apm_mid", "apm_tempo"},
		{"apm_late", "apm_tempo"},
		{"type_", "type_buckets"},
		{"hk_assign_g", "hotkey_assign"},
		{"hk_select_g", "hotkey_select"},
		{"hk_", "hotkey_stats"},
		{"sel_size_", "select_size"},
		{"queued_frac", "queue"},
		{"ici_", "ici"},
		{"pos_dist_", "position"},
		{"pings_", "pings_chats"},
		{"chats_", "pings_chats"},
		{"bigram_", "bigram"},
		{"hk_trans_", "hk_transitions"},
		{"a2s_", "assign_to_select"},
		{"dbl_tap_gap_", "double_tap"},
		{"dbl_tap_rate", "double_tap"},
		{"burst_", "burst"},
		{"first_assign_g", "first_assign"},
		{"dist_h", "distance_hist"},
		{"bi_ici_", "bigram_ici"},
	}
	for _, g := range groups {
		if strings.HasPrefix(name, g.prefix) {
			return g.group
		}
	}
	return "other"
}

func sortedKeys(m map[string]parityDeviation) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
