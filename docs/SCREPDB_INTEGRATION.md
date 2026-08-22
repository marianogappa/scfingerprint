# screpdb integration contract

This document specifies how screpdb should integrate with `scfingerprint` for
automatic alias detection and auto-fingerprinting. All contract requirements
from issue #12 are satisfied by the existing library API.

## API surface for screpdb

### 1. Feature extraction at ingest

screpdb already parses replays into in-memory screp models. Pass the parsed
replay directly — no re-parse needed:

```go
import scfingerprint "github.com/marianogappa/scfingerprint"

// At ingest: extract + cache the feature vector for each player.
for _, p := range replay.Header.Players {
    pfs, _ := features.Extract(replay)
    // Store pf.Vector in a (replay_hash, player_id, feature_version) table.
}
```

The `PlayerGame` type accepts both paths:

```go
// From a live replay (extracts on the fly):
game := scfingerprint.PlayerGame{Replay: rep, PlayerID: pid}

// From a cached vector (skips extraction):
game := scfingerprint.PlayerGame{Vector: cachedVec, Race: "Zerg"}
```

### 2. Version-gated re-extraction

```go
features.Version  // current feature schema version (int)
scorer.FeatureVersion()  // the version the scorer expects
scorer.ModelTag()  // "v1/2026-08-22/<sha>" — changes when the artifact is retrained
```

screpdb should store `features.Version` alongside cached vectors and
re-extract when it changes. `ModelTag` identifies the model for invalidating
projected fingerprint caches (fingerprint.Projected tags its cache with this).

### 3. Auto-fingerprinting local players

When a local name crosses ~20 games, build and maintain a fingerprint:

```go
fp := fingerprint.New(fingerprint.Meta{Label: playerName, Source: "screpdb-local"})
for _, game := range playerGames {
    fp.Add(game.Vector, game.Race)
}
blob, _ := fp.MarshalString()  // single JSON text → one DB column
```

The fingerprint is incrementally updatable — `Add` never needs the raw
history. Serialize with `MarshalString()`, deserialize with
`fingerprint.Parse(blob)`. The format is versioned and carries race
sub-means.

### 4. Matching against the shipped catalog

```go
db, _ := scfingerprint.NewDataset(nil)  // nil → embedded model
// Populate with the shipped pro dataset:
for _, fp := range dataset.Fingerprints() {
    db.Add(fp)
}

// Single-game lead:
results, _ := scfingerprint.Match(replay, playerID, db)

// Multi-game accusation (3+ games for confidence):
results, _ := scfingerprint.MatchMany(games, db)
```

Each `MatchResult` carries:
- `Z` — calibrated z-score, comparable across evidence counts
- `OperatingPoints` — per-comparison FPR thresholds cleared
- `SearchFPR` — Šidák-corrected search-level thresholds (accounts for catalog size N)
- `CatalogSize` — the N used for the correction
- `EvidenceN` — number of games in the probe

### 5. Local alias detection (pairwise)

```go
verdict, _ := scfingerprint.Same(gamesA, gamesB)
// verdict.Z, verdict.OperatingPoints — no search correction needed (1:1)
```

### 6. Co-occurrence disproof

Pure function, no DB dependency:

```go
manifest := map[string][]string{
    "replay1.rep": {"Alice", "Bob"},
    "replay2.rep": {"Alice", "Charlie"},
}
co := hygiene.BuildCoOccurrence(manifest)
co.Disproved("Alice", "Bob")  // true — played in the same game
```

screpdb builds the manifest from its replay table and passes it in.

### 7. Self-consistency gate before merge

```go
score, err := hygiene.SelfConsistencyGate(fp, scorer, hygiene.DefaultThresholds())
// err != nil → enrollment is contaminated, do not merge
```

## Resolved open questions

### Auto-merge: suggest-only

Aliases are **suggest-only**, never auto-applied. The hygiene module's
self-consistency gate (issue #9, #40) and co-occurrence disproof exist
precisely because automatic merging risks catalog poisoning — one wrong
merge sets the operating-point thresholds and collapses TPR@1e-3 for
the entire corpus. screpdb should surface alias suggestions with
confidence tiers in its dashboard; the human confirms.

Confidence tiers for the UI:

| Condition | Tier | UX |
|---|---|---|
| `SearchFPR["fpr_1e3"]` + 3+ games | accusation-grade | bold highlight |
| `SearchFPR["fpr_1e2"]` | strong lead | show prominently |
| `Z >= 2.0` only | lead | show, label as lead |
| `Z < 2.0` | noise | suppress |

### Scoring model: embedded artifact, with refit option

screpdb should use the **embedded artifact** by default (`NewDataset(nil)`).
For corpora with heavy domain shift (team games, non-ladder maps), the
`RefitTransform` path (training a local artifact with `cmd/train`) is
available but requires enough local data (100+ games from 10+ players).
The RESULTS.md documents domain-shift degradation: ladder 1v1 → amateur
team games moves EER from ~1% to ~7%.

## Storage schema (recommended)

```sql
-- Cached per-game feature vectors
CREATE TABLE feature_vectors (
    replay_hash   TEXT NOT NULL,
    player_id     INTEGER NOT NULL,
    feature_version INTEGER NOT NULL,
    vector        BLOB NOT NULL,      -- raw float64 bytes or JSON array
    PRIMARY KEY (replay_hash, player_id, feature_version)
);

-- Incrementally maintained fingerprints
CREATE TABLE fingerprints (
    player_name   TEXT PRIMARY KEY,
    fingerprint   TEXT NOT NULL,      -- fingerprint.MarshalString() output
    game_count    INTEGER NOT NULL,
    model_tag     TEXT NOT NULL,       -- scorer.ModelTag() at last projection
    updated_at    TIMESTAMP NOT NULL
);

-- Alias suggestions (human-reviewed)
CREATE TABLE alias_suggestions (
    name_a        TEXT NOT NULL,
    name_b        TEXT NOT NULL,
    z_score       REAL NOT NULL,
    evidence_n    INTEGER NOT NULL,
    co_occurred   BOOLEAN NOT NULL DEFAULT FALSE,
    status        TEXT NOT NULL DEFAULT 'pending',  -- pending/confirmed/rejected
    PRIMARY KEY (name_a, name_b)
);
```
