// Package hygiene is the catalog hygiene toolkit: gates and validators that
// keep a fingerprint catalog free of contaminated enrollments. The #1
// false-positive source in the research spike was catalog contamination —
// one wrongly-merged two-person enrollment poisoned an entire corpus's
// calibration — not the fingerprint math itself.
package hygiene

import (
	"fmt"
	"math"
	"sort"

	"github.com/marianogappa/scfingerprint/internal/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/scoring"
	"github.com/marianogappa/scfingerprint/internal/training"
)

// Thresholds are the gate operating points, in whitened-cosine space where
// genuine single-person comparisons sit near 0.96 and a wrong two-person
// merge scored 0.44 in the validated spike.
type Thresholds struct {
	// MinSelfConsistency is the floor for an enrollment's chronological
	// half-vs-half similarity.
	MinSelfConsistency float64
	// MinMergeCrossSim is the floor for the centroid cross-similarity of a
	// proposed identity merge.
	MinMergeCrossSim float64
	// DuplicateFloor flags catalog pairs at or above this similarity as
	// likely undeclared aliases; near-duplicates break t-norm normalization.
	DuplicateFloor float64
}

// DefaultThresholds returns the validated defaults, placed between the
// genuine (~0.96) and wrongly-merged (~0.44) regimes observed in the spike.
func DefaultThresholds() Thresholds {
	return Thresholds{
		MinSelfConsistency: 0.85,
		MinMergeCrossSim:   0.85,
		DuplicateFloor:     0.85,
	}
}

// SelfConsistencyGate enforces the self-consistency floor on one enrollment:
// it returns the score and a non-nil error when the enrollment must be
// rejected or flagged. Enforce on every Enroll and dataset build.
func SelfConsistencyGate(fp *fingerprint.Fingerprint, s *scoring.Scorer, th Thresholds) (float64, error) {
	score, err := fp.SelfConsistency(s)
	if err != nil {
		return 0, fmt.Errorf("hygiene: self-consistency unavailable: %w", err)
	}
	if score < th.MinSelfConsistency {
		return score, fmt.Errorf("hygiene: self-consistency %.3f below floor %.3f — likely a multi-person enrollment", score, th.MinSelfConsistency)
	}
	return score, nil
}

// SelfConsistencyGateRaceAware enforces the self-consistency floor on one
// enrollment, correcting for race mixing, and returns the full audit either
// way. Prefer it over SelfConsistencyGate wherever the enrollment's games are
// still to hand: a pro who splits a season between two races has two style
// clusters, so the mixed measure reads them as two humans and rejects a clean
// enrollment. The race-aware measure separates "several humans behind one
// name", which it still rejects, from "one human who plays several races",
// which the fingerprint format already stores as per-race sub-means.
//
// The correction applies only when at least two race strata are big enough to
// score. With one stratum the race-aware mean is not a correction — it is the
// mixed measure over a subset of the games, so it just weakens the evidence.
func SelfConsistencyGateRaceAware(ss []training.Sample, s *scoring.Scorer, th Thresholds) (LabelAudit, error) {
	a, err := AuditSamples(ss, s)
	if err != nil {
		return a, fmt.Errorf("hygiene: self-consistency unavailable: %w", err)
	}
	score, measure := a.Mixed, "self-consistency"
	if len(a.Strata) >= 2 {
		score, measure = a.RaceAware, "race-aware self-consistency"
	}
	if score < th.MinSelfConsistency {
		return a, fmt.Errorf("hygiene: %s %.3f below floor %.3f — likely a multi-person enrollment", measure, score, th.MinSelfConsistency)
	}
	return a, nil
}

// GatedSelfConsistency reports the measure SelfConsistencyGateRaceAware
// judged this audit on, so callers record the number the gate actually used.
func (a LabelAudit) GatedSelfConsistency() float64 {
	if len(a.Strata) >= 2 {
		return a.RaceAware
	}
	return a.Mixed
}

// MergeVerdict is the outcome of validating a proposed identity merge.
type MergeVerdict struct {
	OK     bool
	Reason string // empty when OK

	// CrossSimilarity is the whitened cosine between the two centroids.
	CrossSimilarity float64
	// MergedSelfConsistency is the self-consistency of the hypothetical
	// merged enrollment (a's games then b's).
	MergedSelfConsistency float64
	// CoOccurrenceDisproved is true when the two labels appeared in the same
	// replay, which disproves the merge outright.
	CoOccurrenceDisproved bool
}

// ValidateMerge decides whether identities a and b may be merged: the
// centroid cross-similarity must be in the genuine range AND the merged
// enrollment's self-consistency must stay high. If a co-occurrence index is
// provided and the two labels ever appeared in the same replay, the merge is
// disproved outright. Zero co-occurrence is supporting evidence only — in the
// spike it endorsed a merge the fingerprint correctly refuted — so absence of
// co-occurrence never overrides the similarity gates.
func ValidateMerge(a, b *fingerprint.Fingerprint, s *scoring.Scorer, co *CoOccurrence, th Thresholds) (MergeVerdict, error) {
	var v MergeVerdict

	if co != nil && co.Disproved(a.Meta.Label, b.Meta.Label) {
		v.CoOccurrenceDisproved = true
		v.Reason = fmt.Sprintf("labels %q and %q appeared in the same replay — cannot be the same person", a.Meta.Label, b.Meta.Label)
		return v, nil
	}

	pa, err := a.Projected(s)
	if err != nil {
		return v, err
	}
	pb, err := b.Projected(s)
	if err != nil {
		return v, err
	}
	v.CrossSimilarity = cosine(pa, pb)

	merged, err := fingerprint.Merge(a, b, fingerprint.Meta{})
	if err != nil {
		return v, err
	}
	v.MergedSelfConsistency, err = merged.SelfConsistency(s)
	if err != nil {
		return v, err
	}

	switch {
	case v.CrossSimilarity < th.MinMergeCrossSim:
		v.Reason = fmt.Sprintf("centroid cross-similarity %.3f below genuine floor %.3f", v.CrossSimilarity, th.MinMergeCrossSim)
	case v.MergedSelfConsistency < th.MinSelfConsistency:
		v.Reason = fmt.Sprintf("merged self-consistency %.3f below floor %.3f", v.MergedSelfConsistency, th.MinSelfConsistency)
	default:
		v.OK = true
	}
	return v, nil
}

// DuplicatePair is a catalog pair whose similarity is above the duplicate
// floor — a likely undeclared alias.
type DuplicatePair struct {
	LabelA, LabelB string
	Similarity     float64
}

// ScanDuplicates computes pairwise projected similarity across the whole
// catalog and returns every pair at or above the duplicate floor, most
// similar first. Near-duplicate identities break t-norm score normalization
// and must be declared (merged or excluded) rather than left in the catalog.
func ScanDuplicates(fps []*fingerprint.Fingerprint, s *scoring.Scorer, th Thresholds) ([]DuplicatePair, error) {
	projs := make([][]float64, len(fps))
	for i, fp := range fps {
		p, err := fp.Projected(s)
		if err != nil {
			return nil, fmt.Errorf("hygiene: projecting %q: %w", fp.Meta.Label, err)
		}
		projs[i] = p
	}
	var dups []DuplicatePair
	for i := 0; i < len(fps); i++ {
		for j := i + 1; j < len(fps); j++ {
			sim := cosine(projs[i], projs[j])
			if sim >= th.DuplicateFloor {
				dups = append(dups, DuplicatePair{
					LabelA:     fps[i].Meta.Label,
					LabelB:     fps[j].Meta.Label,
					Similarity: sim,
				})
			}
		}
	}
	sort.SliceStable(dups, func(x, y int) bool { return dups[x].Similarity > dups[y].Similarity })
	return dups, nil
}

// scoredRaces lists the races an enrollment has enough games in for a
// per-race consistency measure to mean anything.
func scoredRaces(fp *fingerprint.Fingerprint) []string {
	var out []string
	for race, n := range fp.RaceCounts() {
		if n >= MinStratumGames {
			out = append(out, race)
		}
	}
	sort.Strings(out)
	return out
}

// multiRace reports whether an enrollment splits across races enough that its
// mixed self-consistency is depressed by race switching rather than by
// contamination.
func multiRace(fp *fingerprint.Fingerprint) bool {
	return len(scoredRaces(fp)) >= 2
}

// Finding is one catalog-hygiene violation.
type Finding struct {
	Kind    string   // "self_consistency", "self_consistency_race_mixed", "self_consistency_unavailable", "duplicate"
	Labels  []string // the enrollment(s) involved
	Score   float64  // the offending similarity/consistency value, when applicable
	Message string
}

// Blocking reports whether a finding should fail a catalog check. A
// race-mixed self-consistency shortfall is reported for review but does not
// block: the fingerprint format cannot settle it either way, so treating it
// as contamination would reject every multi-race pro in the catalog.
func (f Finding) Blocking() bool {
	return f.Kind != "self_consistency_race_mixed"
}

// VerifyCatalog runs every per-catalog gate — the self-consistency gate on
// each enrollment and the duplicate scan across all of them — and returns the
// findings, worst kinds first. An empty result means the catalog is clean.
// This is the engine behind `scfingerprint dataset verify` (#7).
func VerifyCatalog(fps []*fingerprint.Fingerprint, s *scoring.Scorer, th Thresholds) ([]Finding, error) {
	var findings []Finding
	for _, fp := range fps {
		score, err := fp.SelfConsistency(s)
		switch {
		case err != nil:
			findings = append(findings, Finding{
				Kind:    "self_consistency_unavailable",
				Labels:  []string{fp.Meta.Label},
				Message: err.Error(),
			})
		case score < th.MinSelfConsistency && multiRace(fp):
			// A fingerprint's chronological blocks are not race-tagged, so
			// this measure cannot be recomputed per race from the shipped
			// format — and for a player who splits a season across races it
			// reads low for that reason alone. Report it, but do not call it
			// contamination: the enrollment gate already judged this identity
			// on the race-aware measure, which needs the per-game races.
			findings = append(findings, Finding{
				Kind:    "self_consistency_race_mixed",
				Labels:  []string{fp.Meta.Label},
				Score:   score,
				Message: fmt.Sprintf("mixed self-consistency %.3f below floor %.3f, but the enrollment spans %d races — not decidable from the fingerprint alone; re-check with corpus-audit", score, th.MinSelfConsistency, len(scoredRaces(fp))),
			})
		case score < th.MinSelfConsistency:
			findings = append(findings, Finding{
				Kind:    "self_consistency",
				Labels:  []string{fp.Meta.Label},
				Score:   score,
				Message: fmt.Sprintf("self-consistency %.3f below floor %.3f — likely a multi-person enrollment", score, th.MinSelfConsistency),
			})
		}
	}
	dups, err := ScanDuplicates(fps, s, th)
	if err != nil {
		return nil, err
	}
	for _, d := range dups {
		findings = append(findings, Finding{
			Kind:    "duplicate",
			Labels:  []string{d.LabelA, d.LabelB},
			Score:   d.Similarity,
			Message: fmt.Sprintf("similarity %.3f at or above duplicate floor %.3f — likely undeclared alias", d.Similarity, th.DuplicateFloor),
		})
	}
	return findings, nil
}

func cosine(a, b []float64) float64 {
	dot, na, nb := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	denom := math.Sqrt(na) * math.Sqrt(nb)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
