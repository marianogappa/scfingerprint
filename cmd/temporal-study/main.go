// Command temporal-study measures fingerprint stability across multi-year
// time gaps within confirmed single-human identities (issue #13).
//
// It enrolls each identity on its earliest-year games (race-controlled, so
// time is the only variable), probes every later year, and reports the
// calibrated z per year gap for genuine probes alongside an impostor
// reference (other identities' same-race games against the same enrollment).
// It also reports per-feature-group drift between the enrollment era and the
// most recent era to test the hypothesis that hotkey habits persist while
// timing texture drifts.
//
// Usage:
//
//	go run ./cmd/temporal-study -csv corpus.csv -identity "me=chobo85,chobo86,oldie,chobo85s,hsjtykuliyyryjh"
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/model"
	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

func main() {
	csvPath := flag.String("csv", "", "labeled feature CSV (required)")
	identityFlags := flag.String("identity", "", "identity=label1,label2;identity2=... confirmed single-human label groups (required)")
	race := flag.String("race", "Z", "race to control for (single letter or name prefix)")
	mode := flag.String("mode", "any", "game mode filter: 1v1, team, or any")
	enrollN := flag.Int("enroll-n", 20, "games used for enrollment from the earliest year")
	flag.Parse()
	if *csvPath == "" || *identityFlags == "" {
		log.Fatal("-csv and -identity are required")
	}

	samples, err := training.ReadCSV(*csvPath)
	if err != nil {
		log.Fatal(err)
	}
	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		log.Fatal(err)
	}

	labelToIdentity := map[string]string{}
	for _, group := range strings.Split(*identityFlags, ";") {
		parts := strings.SplitN(group, "=", 2)
		if len(parts) != 2 {
			log.Fatalf("bad -identity segment %q", group)
		}
		for _, l := range strings.Split(parts[1], ",") {
			labelToIdentity[strings.TrimSpace(l)] = strings.TrimSpace(parts[0])
		}
	}

	// Group race-controlled samples per identity per year.
	byIdentity := map[string]map[int][]training.Sample{}
	for _, s := range samples {
		id, ok := labelToIdentity[s.Player]
		if !ok {
			continue
		}
		if !strings.HasPrefix(strings.ToUpper(s.Race), strings.ToUpper(*race)) {
			continue
		}
		if s.StartTime.IsZero() {
			continue
		}
		if !modeMatches(s, *mode) {
			continue
		}
		y := s.StartTime.Year()
		if byIdentity[id] == nil {
			byIdentity[id] = map[int][]training.Sample{}
		}
		byIdentity[id][y] = append(byIdentity[id][y], s)
	}

	// Impostor pool: all other labels' same-race games (not in any identity).
	var impostors []training.Sample
	for _, s := range samples {
		if _, ok := labelToIdentity[s.Player]; ok {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(s.Race), strings.ToUpper(*race)) && modeMatches(s, *mode) {
			impostors = append(impostors, s)
		}
	}

	names, _ := features.FeatureNames(features.Version)

	ids := make([]string, 0, len(byIdentity))
	for id := range byIdentity {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		years := byIdentity[id]
		var yearList []int
		for y := range years {
			yearList = append(yearList, y)
		}
		sort.Ints(yearList)
		if len(yearList) < 2 {
			fmt.Printf("\n== %s: only one year of data (%v), skipping\n", id, yearList)
			continue
		}
		enrollYear := yearList[0]
		enrollGames := years[enrollYear]
		if len(enrollGames) > *enrollN {
			enrollGames = enrollGames[:*enrollN]
		}
		if len(enrollGames) < 5 {
			fmt.Printf("\n== %s: only %d enrollment games in %d, skipping\n", id, len(enrollGames), enrollYear)
			continue
		}

		var enrollW [][]float64
		for _, s := range enrollGames {
			w, err := scorer.Transform(s.Vector)
			if err != nil {
				log.Fatal(err)
			}
			enrollW = append(enrollW, w)
		}
		fp, err := scorer.Fingerprint(enrollW...)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("\n== %s: enrolled on %d games from %d (race %s) ==\n", id, len(enrollGames), enrollYear, *race)
		fmt.Printf("%6s %5s %7s %9s %9s %9s\n", "year", "gap", "probes", "mean_z_n1", "mean_z_n3", "mean_cos")

		for _, y := range yearList {
			probes := years[y]
			if y == enrollYear {
				if len(probes) <= len(enrollGames) {
					continue
				}
				probes = probes[len(enrollGames):] // held-out same-year games
			}
			if len(probes) == 0 {
				continue
			}
			var zs1, zs3, coss []float64
			var window [][]float64
			for _, s := range probes {
				w, err := scorer.Transform(s.Vector)
				if err != nil {
					log.Fatal(err)
				}
				sc, err := scorer.Score([][]float64{w}, fp)
				if err != nil {
					log.Fatal(err)
				}
				zs1 = append(zs1, sc.Z)
				coss = append(coss, sc.Cosine)
				window = append(window, w)
				if len(window) == 3 {
					sc3, err := scorer.Score(window, fp)
					if err != nil {
						log.Fatal(err)
					}
					zs3 = append(zs3, sc3.Z)
					window = nil
				}
			}
			fmt.Printf("%6d %5d %7d %9.2f %9.2f %9.3f\n", y, y-enrollYear, len(probes), mean(zs1), mean(zs3), mean(coss))
		}

		// Impostor reference against this enrollment.
		var impZ []float64
		for i, s := range impostors {
			if i >= 500 {
				break
			}
			w, err := scorer.Transform(s.Vector)
			if err != nil {
				log.Fatal(err)
			}
			sc, err := scorer.Score([][]float64{w}, fp)
			if err != nil {
				log.Fatal(err)
			}
			impZ = append(impZ, sc.Z)
		}
		fmt.Printf("impostor reference (n=%d same-race single games): mean_z=%.2f max_z=%.2f\n", len(impZ), mean(impZ), maxOf(impZ))

		// Feature-group drift: standardized centroid cosine per group between
		// the enrollment year and the most recent year.
		lastYear := yearList[len(yearList)-1]
		if lastYear == enrollYear {
			continue
		}
		oldC := rawCentroid(years[enrollYear])
		newC := rawCentroid(years[lastYear])
		stdOld := standardize(oldC, scorer)
		stdNew := standardize(newC, scorer)

		art := mustArtifact()
		selected := map[int]bool{}
		for _, idx := range art.SelectedIndices {
			selected[idx] = true
		}
		groups := groupIndices(names)
		for g, idxs := range groups {
			var kept []int
			for _, i := range idxs {
				if selected[i] {
					kept = append(kept, i)
				}
			}
			groups[g] = kept
		}
		type groupSim struct {
			name string
			cos  float64
			dims int
		}
		var sims []groupSim
		for g, idxs := range groups {
			var a, b []float64
			for _, i := range idxs {
				a = append(a, stdOld[i])
				b = append(b, stdNew[i])
			}
			sims = append(sims, groupSim{g, cosine(a, b), len(idxs)})
		}
		sort.Slice(sims, func(i, j int) bool { return sims[i].cos > sims[j].cos })
		fmt.Printf("\nfeature-group drift %d → %d (standardized centroid cosine, selected dims only):\n", enrollYear, lastYear)
		for _, s := range sims {
			fmt.Printf("  %-22s %6.3f  (%d dims)\n", s.name, s.cos, s.dims)
		}
	}
}

// groupIndices buckets feature dimensions into interpretable groups by name.
func groupIndices(names []string) map[string][]int {
	groups := map[string][]int{}
	for i, n := range names {
		g := "other"
		switch {
		case strings.HasPrefix(n, "hk_") || strings.HasPrefix(n, "hktrans_") || strings.HasPrefix(n, "firstassign_"):
			g = "hotkey habits"
		case strings.HasPrefix(n, "ici_") || strings.HasPrefix(n, "preici_") || strings.HasPrefix(n, "bici_") ||
			strings.HasPrefix(n, "burst_") || strings.HasPrefix(n, "a2s_") || strings.HasPrefix(n, "dbltap_") || strings.HasPrefix(n, "dblgap_"):
			g = "timing texture"
		case strings.HasPrefix(n, "apm") || n == "eapm" || n == "redundancy":
			g = "apm/tempo"
		case strings.HasPrefix(n, "frac_") || strings.HasPrefix(n, "bigram_") || n == "queued_frac":
			g = "command mix"
		case strings.HasPrefix(n, "sel_size") || strings.HasPrefix(n, "dist_") || strings.HasPrefix(n, "pings") || strings.HasPrefix(n, "chats"):
			g = "selection/space"
		}
		groups[g] = append(groups[g], i)
	}
	return groups
}

func rawCentroid(ss []training.Sample) []float64 {
	sum := make([]float64, len(ss[0].Vector))
	for _, s := range ss {
		for j, v := range s.Vector {
			sum[j] += v
		}
	}
	for j := range sum {
		sum[j] /= float64(len(ss))
	}
	return sum
}

// standardize z-scores a raw centroid with the artifact's means/stds so
// per-group cosines are comparable across differently-scaled features.
func standardize(raw []float64, s *scoring.Scorer) []float64 {
	// The scorer doesn't export means/stds; reconstruct standardization by
	// transforming won't work per-dim (selection+whitening mix dims). Load
	// the artifact directly.
	a := mustArtifact()
	out := make([]float64, len(raw))
	for j := range raw {
		out[j] = (raw[j] - a.Means[j]) / a.Stds[j]
	}
	return out
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func maxOf(xs []float64) float64 {
	m := math.Inf(-1)
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

func cosine(a, b []float64) float64 {
	dot, na, nb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	d := math.Sqrt(na) * math.Sqrt(nb)
	if d == 0 {
		return 0
	}
	return dot / d
}

func mustArtifact() *model.Artifact {
	a, err := model.LoadEmbedded()
	if err != nil {
		log.Fatal(err)
	}
	return a
}

// modeMatches filters samples by game mode: 1v1 (2 humans), team (3+), any.
func modeMatches(s training.Sample, mode string) bool {
	switch mode {
	case "1v1":
		return s.NumHumans == 2
	case "team":
		return s.NumHumans >= 3
	default:
		return true
	}
}
