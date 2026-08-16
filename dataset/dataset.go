// Package dataset provides the built-in VCS'd collection of player
// fingerprints — the product's core asset. Each identity is one JSON file
// under dataset/players/, embedded via go:embed, and loaded as the default
// [Dataset] for matching. The dataset is version-coupled to the feature
// schema: CI fails if they drift.
//
// Identities carry confidence tiers (confirmed / high / candidate), alias
// lists with per-alias evidence, and replay manifests for re-derivation on
// feature-version bumps.
package dataset

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/marianogappa/scfingerprint/features"
	"github.com/marianogappa/scfingerprint/fingerprint"
	"github.com/marianogappa/scfingerprint/scoring"
)

//go:embed players/*.json
var playersFS embed.FS

// Confidence tiers for dataset entries.
const (
	ConfidenceConfirmed = "confirmed"
	ConfidenceHigh      = "high"
	ConfidenceCandidate = "candidate"
)

// Identity is one dataset entry: a player's fingerprint plus provenance,
// aliases, confidence, and the replay manifest for re-derivation.
type Identity struct {
	// ID is the canonical identifier for this player (filename stem).
	ID string `json:"id"`

	// Fingerprint is the serialized fingerprint blob (from fingerprint.MarshalString).
	Fingerprint string `json:"fingerprint"`

	// Confidence is the curation tier: "confirmed", "high", or "candidate".
	Confidence string `json:"confidence"`

	// Aliases lists the known account names for this player, with evidence.
	Aliases []Alias `json:"aliases"`

	// ReplayManifest lists the replay files that contributed to this
	// enrollment (hashes or filenames, not the replay data), so vectors
	// can be regenerated on feature-version bumps.
	ReplayManifest []string `json:"replay_manifest,omitempty"`

	// Notes is free-text curation context.
	Notes string `json:"notes,omitempty"`
}

// Alias is one known account name for an identity.
type Alias struct {
	Name       string  `json:"name"`
	ZScore     float64 `json:"z_score,omitempty"`
	CoOccurred bool    `json:"co_occurred,omitempty"`
	Primary    bool    `json:"primary,omitempty"`
}

// LoadEmbedded reads all identity files from the embedded players/ directory,
// validates them against the current feature version, and returns the parsed
// identities and their fingerprints ready for use with NewDataset.
func LoadEmbedded() ([]Identity, []*fingerprint.Fingerprint, error) {
	entries, err := playersFS.ReadDir("players")
	if err != nil {
		return nil, nil, fmt.Errorf("dataset: reading embedded players: %w", err)
	}

	var ids []Identity
	var fps []*fingerprint.Fingerprint
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := playersFS.ReadFile(path.Join("players", e.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("dataset: reading %s: %w", e.Name(), err)
		}
		var id Identity
		if err := json.Unmarshal(data, &id); err != nil {
			return nil, nil, fmt.Errorf("dataset: parsing %s: %w", e.Name(), err)
		}
		fp, err := fingerprint.Parse(id.Fingerprint)
		if err != nil {
			return nil, nil, fmt.Errorf("dataset: %s fingerprint: %w", id.ID, err)
		}
		if fp.Version() != features.Version {
			return nil, nil, fmt.Errorf("dataset: %s has feature version %d, current is %d — re-derive from replays", id.ID, fp.Version(), features.Version)
		}
		ids = append(ids, id)
		fps = append(fps, fp)
	}

	// Sort both slices in lockstep by identity ID.
	type pair struct {
		id Identity
		fp *fingerprint.Fingerprint
	}
	pairs := make([]pair, len(ids))
	for i := range ids {
		pairs[i] = pair{ids[i], fps[i]}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].id.ID < pairs[j].id.ID })
	for i, p := range pairs {
		ids[i] = p.id
		fps[i] = p.fp
	}

	return ids, fps, nil
}

// NewDefaultDataset loads the embedded dataset and builds a Dataset with the
// given scorer (or the embedded model if nil). Only entries at or above
// minConfidence are included: "confirmed" includes only confirmed,
// "high" includes confirmed+high, "candidate" includes all.
func NewDefaultDataset(scorer *scoring.Scorer, minConfidence string) (*DatasetDB, error) {
	if scorer == nil {
		var err error
		scorer, err = scoring.NewFromEmbedded()
		if err != nil {
			return nil, err
		}
	}
	ids, fps, err := LoadEmbedded()
	if err != nil {
		return nil, err
	}
	db := &DatasetDB{
		scorer:     scorer,
		identities: make(map[string]Identity),
	}
	for i, id := range ids {
		if !meetsConfidence(id.Confidence, minConfidence) {
			continue
		}
		db.identities[id.ID] = id
		db.fps = append(db.fps, fps[i])
		proj, err := fps[i].Projected(scorer)
		if err != nil {
			return nil, fmt.Errorf("dataset: projecting %s: %w", id.ID, err)
		}
		db.projs = append(db.projs, proj)
	}
	return db, nil
}

// DatasetDB wraps the loaded identities and their projections for use with
// the top-level Match/MatchMany API via ToDataset.
type DatasetDB struct {
	scorer     *scoring.Scorer
	identities map[string]Identity
	fps        []*fingerprint.Fingerprint
	projs      [][]float64
}

// Len returns the number of identities in the dataset.
func (d *DatasetDB) Len() int { return len(d.fps) }

// Identities returns all loaded identity records.
func (d *DatasetDB) Identities() []Identity {
	out := make([]Identity, 0, len(d.identities))
	for _, id := range d.identities {
		out = append(out, id)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Fingerprints returns the parsed fingerprints, parallel to Identities order
// is not guaranteed — use LookupFingerprint for by-ID access.
func (d *DatasetDB) Fingerprints() []*fingerprint.Fingerprint { return d.fps }

// Scorer returns the underlying scorer.
func (d *DatasetDB) Scorer() *scoring.Scorer { return d.scorer }

func meetsConfidence(actual, minimum string) bool {
	rank := map[string]int{
		ConfidenceConfirmed: 3,
		ConfidenceHigh:      2,
		ConfidenceCandidate: 1,
	}
	return rank[actual] >= rank[minimum]
}
