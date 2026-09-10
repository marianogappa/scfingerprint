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

	"github.com/marianogappa/scfingerprint/internal/features"
	"github.com/marianogappa/scfingerprint/internal/fingerprint"
	"github.com/marianogappa/scfingerprint/internal/scoring"
)

//go:embed players/*.json
var playersFS embed.FS

// liquipedia.json maps identity IDs to their Liquipedia profile URL. It lives
// in its own file rather than in players/*.json so seed-dataset regeneration
// never wipes it. Only verified pages belong here: existence on the wiki plus
// a race match against the fingerprint.
//
//go:embed liquipedia.json
var liquipediaJSON []byte

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

	// NullP95 is the 95th percentile of this fingerprint's score against
	// accounts that are NOT this player, measured over the corpus at
	// enrollment. It says how crowded the player's corner of style space is:
	// a distinctive player sits near 1.8, while one whose style is modal for
	// their race runs higher and will score respectably against strangers.
	// Claims are judged against this, not against a single global bar — see
	// [IdentityBar]. Zero means "not measured", and the global bar applies.
	NullP95 float64 `json:"null_p95,omitempty"`

	// Notes is free-text curation context.
	Notes string `json:"notes,omitempty"`

	// Liquipedia is the player's profile URL, filled from the embedded
	// liquipedia.json at load time (never stored in players/*.json).
	// Empty when the player has no verified page.
	Liquipedia string `json:"liquipedia,omitempty"`
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
	var links map[string]string
	if err := json.Unmarshal(liquipediaJSON, &links); err != nil {
		return nil, nil, fmt.Errorf("dataset: parsing liquipedia.json: %w", err)
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
		id.Liquipedia = links[id.ID]
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

// NullMargin is how far above an identity's own null distribution a score must
// sit before it counts as that player rather than as someone who merely plays
// like them. Calibrated on the corpus: it puts a distinctive identity's bar at
// about 4.3 — below the flat operating point, so nothing distinctive gets less
// sensitive — while a crowded one such as Shuttle lands near 5.2, which is
// where its false claims sat.
const NullMargin = 2.5

// defaultNullP95 stands in for identities enrolled before NullP95 was
// measured. It is the corpus median, so an unmeasured identity behaves like a
// typical one instead of silently claiming everything.
const defaultNullP95 = 1.82

// IdentityBar is the z an observation must clear to be claimed as this
// identity. It is the identity's own null plus [NullMargin], so the bar rises
// only for players whose style is genuinely crowded.
func (i Identity) IdentityBar() float64 {
	p95 := i.NullP95
	if p95 <= 0 {
		p95 = defaultNullP95
	}
	return p95 + NullMargin
}
