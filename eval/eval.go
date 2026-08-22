// Package eval is the evaluation harness: every model/feature change must
// prove itself on fixed benchmarks before shipping. Given a labeled corpus
// of feature vectors it computes closed-set accuracy, verification AUC, EER,
// and TPR at fixed FPR operating points, for single-game and 3-game probes,
// against all-impostor and same-race-impostor pools.
//
// Splits are chronological per player — random splits leak same-session
// effects and overstate accuracy. Known-smurf pairs are excluded from
// impostor pools via an exclusion manifest so real aliases don't count as
// false positives.
package eval

import (
	"fmt"
	"sort"

	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

// Options controls an evaluation run.
type Options struct {
	// EnrollFrac is the chronological fraction of each player's games used
	// to enroll their fingerprint; the remainder become probes. Default 0.5.
	EnrollFrac float64
	// MinGamesPerPlayer excludes players with fewer games. Default 4
	// (enough for at least one enrollment game and one probe).
	MinGamesPerPlayer int
	// Exclusions lists known-smurf player pairs to remove from impostor
	// pools (both directions).
	Exclusions [][2]string
}

// DefaultOptions returns the default evaluation options.
func DefaultOptions() Options {
	return Options{EnrollFrac: 0.5, MinGamesPerPlayer: 4}
}

// Metrics is one scenario's verification and identification results.
// TPR fields are nil when the impostor pool is too small to express
// the requested FPR (fewer than 1/fpr impostors).
type Metrics struct {
	ClosedSetAccuracy float64  `json:"closed_set_accuracy"`
	AUC               float64  `json:"auc"`
	EER               float64  `json:"eer"`
	TPRAtFPR1e2       *float64 `json:"tpr_at_fpr_1e2"`
	TPRAtFPR1e3       *float64 `json:"tpr_at_fpr_1e3"`
	TPRAtFPR1e4       *float64 `json:"tpr_at_fpr_1e4"`
	NumGenuine        int      `json:"num_genuine"`
	NumImpostor       int      `json:"num_impostor"`
}

// Report holds metrics for every evaluated scenario, keyed by scenario name:
// "n1_all", "n1_same_race", "n3_all", "n3_same_race".
type Report struct {
	NumPlayers int                `json:"num_players"`
	NumProbes  int                `json:"num_probes"`
	Scenarios  map[string]Metrics `json:"scenarios"`
}

// probe is one evaluation trial: an averaged whitened vector with its truth.
type probe struct {
	player string
	race   string
	games  [][]float64
}

// Evaluate runs the full benchmark for a labeled corpus against a scorer.
func Evaluate(samples []training.Sample, scorer *scoring.Scorer, opts Options) (*Report, error) {
	if opts.EnrollFrac <= 0 || opts.EnrollFrac >= 1 {
		return nil, fmt.Errorf("eval: EnrollFrac must be in (0,1), got %v", opts.EnrollFrac)
	}
	if opts.MinGamesPerPlayer < 2 {
		opts.MinGamesPerPlayer = 2
	}
	samples = filterMinGames(samples, opts.MinGamesPerPlayer)
	if len(samples) == 0 {
		return nil, fmt.Errorf("eval: no players with >= %d games", opts.MinGamesPerPlayer)
	}

	enroll, probeSamples := training.ChronologicalSplit(samples, opts.EnrollFrac)

	// Whiten everything once.
	enrollW, err := transformAll(enroll, scorer)
	if err != nil {
		return nil, err
	}
	probeW, err := transformAll(probeSamples, scorer)
	if err != nil {
		return nil, err
	}

	// Enroll fingerprints: mean whitened vector per player, plus modal race.
	players := sortedPlayers(enroll)
	fingerprints := map[string][]float64{}
	races := map[string]string{}
	for _, p := range players {
		var vecs [][]float64
		for i, s := range enroll {
			if s.Player == p {
				vecs = append(vecs, enrollW[i])
			}
		}
		fp, err := scorer.Fingerprint(vecs...)
		if err != nil {
			return nil, err
		}
		fingerprints[p] = fp
		races[p] = modalRace(enroll, p)
	}

	// Build probes: n=1 (each probe game alone) and n=3 (consecutive windows).
	probesN1 := buildProbes(probeSamples, probeW, races, 1)
	probesN3 := buildProbes(probeSamples, probeW, races, 3)

	excluded := exclusionSet(opts.Exclusions)

	report := &Report{
		NumPlayers: len(players),
		NumProbes:  len(probesN1),
		Scenarios:  map[string]Metrics{},
	}
	for _, sc := range []struct {
		name     string
		probes   []probe
		sameRace bool
	}{
		{"n1_all", probesN1, false},
		{"n1_same_race", probesN1, true},
		{"n3_all", probesN3, false},
		{"n3_same_race", probesN3, true},
	} {
		m, err := runScenario(sc.probes, players, fingerprints, races, scorer, sc.sameRace, excluded)
		if err != nil {
			return nil, fmt.Errorf("eval: scenario %s: %w", sc.name, err)
		}
		report.Scenarios[sc.name] = m
	}
	return report, nil
}

func runScenario(
	probes []probe,
	players []string,
	fingerprints map[string][]float64,
	races map[string]string,
	scorer *scoring.Scorer,
	sameRaceOnly bool,
	excluded map[[2]string]bool,
) (Metrics, error) {
	var genuine, impostor []float64
	correct, total := 0, 0

	for _, pr := range probes {
		ownFP, ok := fingerprints[pr.player]
		if !ok {
			continue
		}
		ownScore, err := scorer.Score(pr.games, ownFP)
		if err != nil {
			return Metrics{}, err
		}
		genuine = append(genuine, ownScore.Z)

		best, bestPlayer := ownScore.Z, pr.player
		for _, other := range players {
			if other == pr.player {
				continue
			}
			if excluded[[2]string{pr.player, other}] {
				continue
			}
			if sameRaceOnly && races[other] != pr.race {
				continue
			}
			sc, err := scorer.Score(pr.games, fingerprints[other])
			if err != nil {
				return Metrics{}, err
			}
			impostor = append(impostor, sc.Z)
			if sc.Z > best {
				best, bestPlayer = sc.Z, other
			}
		}
		total++
		if bestPlayer == pr.player {
			correct++
		}
	}

	if len(genuine) == 0 || len(impostor) == 0 {
		return Metrics{}, fmt.Errorf("empty genuine (%d) or impostor (%d) pool", len(genuine), len(impostor))
	}

	acc := float64(correct) / float64(total)
	return Metrics{
		ClosedSetAccuracy: acc,
		AUC:               auc(genuine, impostor),
		EER:               eer(genuine, impostor),
		TPRAtFPR1e2:       tprAtFPR(genuine, impostor, 0.01),
		TPRAtFPR1e3:       tprAtFPR(genuine, impostor, 0.001),
		TPRAtFPR1e4:       tprAtFPR(genuine, impostor, 0.0001),
		NumGenuine:        len(genuine),
		NumImpostor:       len(impostor),
	}, nil
}

func transformAll(samples []training.Sample, scorer *scoring.Scorer) ([][]float64, error) {
	out := make([][]float64, len(samples))
	for i, s := range samples {
		w, err := scorer.Transform(s.Vector)
		if err != nil {
			return nil, fmt.Errorf("eval: transforming sample %d (%s): %w", i, s.Player, err)
		}
		out[i] = w
	}
	return out, nil
}

// buildProbes groups per-player probe games into consecutive non-overlapping
// windows of size n (chronological order is preserved from the split).
func buildProbes(samples []training.Sample, whitened [][]float64, races map[string]string, n int) []probe {
	byPlayer := map[string][]int{}
	for i, s := range samples {
		byPlayer[s.Player] = append(byPlayer[s.Player], i)
	}
	var probes []probe
	for _, p := range sortedPlayers(samples) {
		idxs := byPlayer[p]
		for start := 0; start+n <= len(idxs); start += n {
			games := make([][]float64, n)
			for j := 0; j < n; j++ {
				games[j] = whitened[idxs[start+j]]
			}
			probes = append(probes, probe{player: p, race: races[p], games: games})
		}
	}
	return probes
}

func filterMinGames(samples []training.Sample, minGames int) []training.Sample {
	counts := map[string]int{}
	for _, s := range samples {
		counts[s.Player]++
	}
	var out []training.Sample
	for _, s := range samples {
		if counts[s.Player] >= minGames {
			out = append(out, s)
		}
	}
	return out
}

func sortedPlayers(samples []training.Sample) []string {
	set := map[string]bool{}
	for _, s := range samples {
		set[s.Player] = true
	}
	players := make([]string, 0, len(set))
	for p := range set {
		players = append(players, p)
	}
	sort.Strings(players)
	return players
}

// modalRace returns the most frequent race among a player's games, breaking
// ties alphabetically.
func modalRace(samples []training.Sample, player string) string {
	counts := map[string]int{}
	for _, s := range samples {
		if s.Player == player {
			counts[s.Race]++
		}
	}
	best, bestCount := "", -1
	races := make([]string, 0, len(counts))
	for r := range counts {
		races = append(races, r)
	}
	sort.Strings(races)
	for _, r := range races {
		if counts[r] > bestCount {
			best, bestCount = r, counts[r]
		}
	}
	return best
}

// exclusionSet expands smurf pairs into a symmetric lookup set.
func exclusionSet(pairs [][2]string) map[[2]string]bool {
	set := map[[2]string]bool{}
	for _, p := range pairs {
		set[[2]string{p[0], p[1]}] = true
		set[[2]string{p[1], p[0]}] = true
	}
	return set
}
