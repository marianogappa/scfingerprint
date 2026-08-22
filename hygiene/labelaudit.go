package hygiene

import (
	"fmt"
	"sort"

	"github.com/marianogappa/scfingerprint/scoring"
	"github.com/marianogappa/scfingerprint/training"
)

// MinStratumGames is the minimum games a (label, race) stratum needs for its
// half-vs-half similarity to be meaningful.
const MinStratumGames = 8

// LabelAudit is one corpus label's ground-truth health check. Mixed is the
// chronological half-vs-half similarity over all the label's games; RaceAware
// recomputes it per race and takes the game-weighted mean, which separates
// "several humans behind one name" from "one human who plays several races".
// A random-race player fragments the mixed metric (first half Zerg-heavy,
// second half Protoss-heavy reads as two people) while staying high per race.
//
// Limitation: RaceAware cannot catch two humans sharing an account who each
// play a different race — every race stratum is pure, so both metrics read
// clean. Co-occurrence disproof and cross-label duplicate scans remain the
// backstop for that shape.
type LabelAudit struct {
	Label     string             `json:"label"`
	Games     int                `json:"games"`
	Mixed     float64            `json:"mixed_self_consistency"`
	RaceAware float64            `json:"race_aware_self_consistency"`
	Strata    map[string]float64 `json:"strata,omitempty"` // race → half-vs-half
}

// AuditLabels computes per-label self-consistency over a labeled corpus,
// both mixed (all games chronologically halved) and race-aware. Samples must
// be sorted by (Player, StartTime), which training.ReadCSV guarantees. Labels
// with fewer than minGames games are skipped. When no race stratum reaches
// MinStratumGames, RaceAware falls back to Mixed.
func AuditLabels(samples []training.Sample, s *scoring.Scorer, minGames int) ([]LabelAudit, error) {
	byLabel := map[string][]training.Sample{}
	for _, smp := range samples {
		byLabel[smp.Player] = append(byLabel[smp.Player], smp)
	}

	labels := make([]string, 0, len(byLabel))
	for l, ss := range byLabel {
		if len(ss) >= minGames {
			labels = append(labels, l)
		}
	}
	sort.Strings(labels)

	halves := func(ss []training.Sample) (float64, error) {
		mid := len(ss) / 2
		c1, err := centroidOf(ss[:mid], s)
		if err != nil {
			return 0, err
		}
		c2, err := centroidOf(ss[mid:], s)
		if err != nil {
			return 0, err
		}
		return cosine(c1, c2), nil
	}

	audits := make([]LabelAudit, 0, len(labels))
	for _, l := range labels {
		ss := byLabel[l]
		mixed, err := halves(ss)
		if err != nil {
			return nil, fmt.Errorf("hygiene: auditing %q: %w", l, err)
		}

		byRace := map[string][]training.Sample{}
		for _, smp := range ss {
			byRace[smp.Race] = append(byRace[smp.Race], smp)
		}
		strata := map[string]float64{}
		weightedSum, totalWeight := 0.0, 0
		for race, g := range byRace {
			if len(g) < MinStratumGames {
				continue
			}
			sc, err := halves(g)
			if err != nil {
				return nil, fmt.Errorf("hygiene: auditing %q race %q: %w", l, race, err)
			}
			strata[race] = sc
			weightedSum += sc * float64(len(g))
			totalWeight += len(g)
		}

		raceAware := mixed
		if totalWeight > 0 {
			raceAware = weightedSum / float64(totalWeight)
		}
		audits = append(audits, LabelAudit{
			Label:     l,
			Games:     len(ss),
			Mixed:     mixed,
			RaceAware: raceAware,
			Strata:    strata,
		})
	}
	return audits, nil
}

func centroidOf(ss []training.Sample, s *scoring.Scorer) ([]float64, error) {
	if len(ss) == 0 {
		return nil, fmt.Errorf("empty sample group")
	}
	var sum []float64
	for _, smp := range ss {
		w, err := s.Transform(smp.Vector)
		if err != nil {
			return nil, err
		}
		if sum == nil {
			sum = make([]float64, len(w))
		}
		for j, v := range w {
			sum[j] += v
		}
	}
	for j := range sum {
		sum[j] /= float64(len(ss))
	}
	return sum, nil
}
