package scfingerprint

import (
	"fmt"
	"sort"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/scoring"
)

// Match identifies a single observed player against every fingerprint in db.
// Results are returned sorted by z-score descending, filtered to those at or
// above the minimum z threshold (see WithMinZ; default 2.0).
//
// Accepts the in-memory screp model — callers like screpdb must not pay a
// re-parse. Single-game matches are "leads"; use MatchMany with 3+ games to
// reach accusation-grade confidence.
func Match(r *Replay, playerID byte, db *Dataset, opts ...Option) ([]MatchResult, error) {
	return MatchMany([]PlayerGame{{Replay: r, PlayerID: playerID}}, db, opts...)
}

// MatchMany identifies a player observed across several games against every
// fingerprint in db. More games → stronger evidence. Results sorted by
// z-score descending, filtered to the minimum z threshold.
//
// The spike showed: single-game EER 0.21%, 3-game same-race EER 0.05% with
// TPR@FPR=1e-3 = 1.000.
func MatchMany(games []PlayerGame, db *Dataset, opts ...Option) ([]MatchResult, error) {
	if len(games) == 0 {
		return nil, fmt.Errorf("scfingerprint: no games provided")
	}
	if db == nil || db.Len() == 0 {
		return nil, fmt.Errorf("scfingerprint: dataset is nil or empty")
	}

	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	whitened, err := extractAndTransform(games, db.scorer)
	if err != nil {
		return nil, err
	}

	var results []MatchResult
	for i, fp := range db.fps {
		target := db.projs[i]
		sc, err := db.scorer.Score(whitened, target)
		if err != nil {
			return nil, err
		}
		if sc.Z < o.minZ {
			continue
		}
		results = append(results, MatchResult{
			Label:           fp.Meta.Label,
			Z:               sc.Z,
			Cosine:          sc.Cosine,
			EvidenceN:       sc.EvidenceN,
			OperatingPoints: sc.OperatingPoints,
		})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Z > results[j].Z })
	return results, nil
}

// Same answers "are these two observed players the same human?" without
// requiring either to be in any dataset. Each side is one or more games;
// the probe is the average of side a, scored against the centroid of side b.
func Same(a, b []PlayerGame, opts ...Option) (Verdict, error) {
	if len(a) == 0 || len(b) == 0 {
		return Verdict{}, fmt.Errorf("scfingerprint: both sides must have at least one game")
	}

	scorer, err := scoring.NewFromEmbedded()
	if err != nil {
		return Verdict{}, err
	}

	wa, err := extractAndTransform(a, scorer)
	if err != nil {
		return Verdict{}, err
	}
	wb, err := extractAndTransform(b, scorer)
	if err != nil {
		return Verdict{}, err
	}

	fpB, err := scorer.Fingerprint(wb...)
	if err != nil {
		return Verdict{}, err
	}

	sc, err := scorer.Score(wa, fpB)
	if err != nil {
		return Verdict{}, err
	}
	return Verdict{
		Z:               sc.Z,
		Cosine:          sc.Cosine,
		EvidenceN:       len(a) + len(b),
		OperatingPoints: sc.OperatingPoints,
	}, nil
}

// Enroll builds a fingerprint from one or more observed games, running the
// self-consistency gate when there are enough games.
func Enroll(games []PlayerGame, meta Meta) (*Fingerprint, error) {
	if len(games) == 0 {
		return nil, fmt.Errorf("scfingerprint: no games to enroll")
	}
	fp := fingerprint.New(meta)
	for _, g := range games {
		vec, race, err := resolveVector(g)
		if err != nil {
			return nil, err
		}
		if err := fp.Add(vec, race); err != nil {
			return nil, err
		}
	}
	return fp, nil
}

// extractAndTransform resolves each PlayerGame to a raw vector, then
// transforms it through the scorer's pipeline into the whitened space.
func extractAndTransform(games []PlayerGame, scorer *scoring.Scorer) ([][]float64, error) {
	whitened := make([][]float64, len(games))
	for i, g := range games {
		vec, _, err := resolveVector(g)
		if err != nil {
			return nil, err
		}
		w, err := scorer.Transform(vec)
		if err != nil {
			return nil, err
		}
		whitened[i] = w
	}
	return whitened, nil
}

// resolveVector turns a PlayerGame into a raw feature vector and race,
// either from a pre-extracted vector or by extracting from the replay.
func resolveVector(g PlayerGame) ([]float64, string, error) {
	if g.Vector != nil {
		return g.Vector, g.Race, nil
	}
	if g.Replay == nil {
		return nil, "", fmt.Errorf("scfingerprint: PlayerGame has neither Vector nor Replay")
	}
	pfs, err := features.Extract(g.Replay)
	if err != nil {
		return nil, "", fmt.Errorf("scfingerprint: extracting features: %w", err)
	}
	for _, pf := range pfs {
		if pf.PlayerID == g.PlayerID {
			race := g.Race
			if race == "" {
				race = pf.Race
			}
			return pf.Vector, race, nil
		}
	}
	return nil, "", fmt.Errorf("scfingerprint: playerID %d not found in replay", g.PlayerID)
}
