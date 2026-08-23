# screpdb integration contract

How screpdb integrates with `scfingerprint` for alias detection and
auto-fingerprinting.

Everything below uses only the root package. There is nothing else to import —
`internal/` is sealed, so this document *is* the API surface.

```go
import scf "github.com/marianogappa/scfingerprint"
```

## 1. Feature extraction at ingest

screpdb already parses replays into in-memory screp models. Pass the parsed
replay straight in — no re-parse:

```go
vectors, err := scf.Extract(replay)
for _, v := range vectors {
    // v.PlayerID, v.Name, v.Race, v.Vector, v.Frames, v.CmdCount
    // Store v.Vector in a (replay_hash, player_id, feature_version) table.
}
```

`PlayerGame` accepts either path, so cached vectors skip extraction entirely:

```go
game := scf.PlayerGame{Replay: rep, PlayerID: pid}          // extract on the fly
game := scf.PlayerGame{Vector: cachedVec, Race: "Zerg"}     // use the cache
```

## 2. Version-gated re-extraction

```go
scf.FeatureVersion()  // current feature schema version (int)
scf.ModelTag()        // "v1/2026-08-22/<sha>" — changes when the artifact is retrained
```

Store `FeatureVersion()` alongside every cached vector and re-extract when it
changes. `ModelTag()` identifies the model, so it is the right key for
invalidating anything derived from scores.

## 3. Auto-fingerprinting local players

When a local name crosses ~20 games, build and maintain a fingerprint:

```go
fp := scf.NewFingerprint(scf.Meta{Label: playerName, Source: "screpdb-local"})
for _, game := range playerGames {
    if err := fp.Add(game.Vector, game.Race); err != nil { /* version mismatch */ }
}
blob, err := fp.MarshalString()   // single JSON text → one DB column
```

`Add` never needs the raw history, so this is incremental. Read it back with
`scf.ParseFingerprint(blob)`. The format is versioned and carries per-race
sub-means.

## 4. Matching against the shipped catalog

```go
db, err := scf.BuiltinDataset(scf.ConfidenceHigh)

// Single-game lead:
results, err := scf.Match(replay, playerID, db)

// Multi-game evidence (3+ games for real confidence):
results, err := scf.MatchMany(games, db)
```

Confidence tiers are `scf.ConfidenceConfirmed`, `scf.ConfidenceHigh` and
`scf.ConfidenceCandidate`; each includes every entry at or above it.

Each `MatchResult` carries:

- `Z` — calibrated z-score, comparable across evidence counts
- `OperatingPoints` — per-comparison FPR thresholds cleared
- `SearchFPR` — Šidák-corrected search-level thresholds, accounting for catalog size N
- `CatalogSize` — the N used for the correction
- `EvidenceN` — number of games in the probe
- `ModelIsSynthetic` — true when scores carry no real-world meaning

**Use `SearchFPR`, not `OperatingPoints`, for anything shown to a user.**
`Match` is a 1:N sweep, and per-comparison FPRs overstate confidence at
catalog scale: α=0.001 across 68 identities is a ~6.6% search-level FPR.

To search your own fingerprints instead of the shipped catalog:

```go
db, err := scf.NewDataset()
for _, fp := range localFingerprints {
    err = db.Add(fp)
}
```

## 5. Local alias detection (pairwise)

```go
verdict, err := scf.Same(gamesA, gamesB)
// verdict.Z, verdict.OperatingPoints — no search correction needed (1:1)
```

## 6. Co-occurrence disproof

Two names in the same game cannot be the same human. Cheapest and most
decisive check available — run it before trusting any match. Pure function, no
database dependency:

```go
co := scf.NewCoOccurrence(map[string][]string{
    "replay1.rep": {"Alice", "Bob"},
    "replay2.rep": {"Alice", "Charlie"},
})
co.Disproved("Alice", "Bob")  // true — played in the same game
```

screpdb builds the manifest from its replay table and passes it in.

## 7. Self-consistency gate before merge

```go
score, err := fp.SelfConsistencyGate()
// err != nil → the enrollment looks contaminated, do not merge
```

`SelfConsistency()` returns the raw score without applying the threshold, for
display.

## Resolved open questions

### Auto-merge: suggest-only

Aliases are **suggest-only**, never auto-applied. The self-consistency gate and
co-occurrence disproof exist precisely because automatic merging risks catalog
poisoning: one wrong merge sets the operating-point thresholds and collapses
TPR@1e-3 for the entire corpus. screpdb should surface suggestions with
confidence tiers and let a human confirm.

Suggested UI tiers:

| Condition | Tier | UX |
|---|---|---|
| `SearchFPR["fpr_1e3"]` + 3+ games | accusation-grade | bold highlight |
| `SearchFPR["fpr_1e2"]` | strong lead | show prominently |
| `Z >= 2.0` only | lead | show, label as lead |
| `Z < 2.0` | noise | suppress |

### Scoring model: embedded artifact

Use the embedded artifact — it is what `BuiltinDataset` and `NewDataset` load,
and there is no public knob to replace it.

For corpora with heavy domain shift (team games, non-ladder maps) the shipped
transform degrades by roughly 3–5× on EER; refitting on a local corpus recovers
most of it, but that path runs through `internal/cmd/train` and is a
repo-maintainer operation, not a library call. If screpdb needs it, open an
issue and it can be exposed deliberately.

## Storage schema (recommended)

```sql
-- Cached per-game feature vectors
CREATE TABLE feature_vectors (
    replay_hash     TEXT NOT NULL,
    player_id       INTEGER NOT NULL,
    feature_version INTEGER NOT NULL,
    vector          BLOB NOT NULL,      -- raw float64 bytes or JSON array
    PRIMARY KEY (replay_hash, player_id, feature_version)
);

-- Incrementally maintained fingerprints
CREATE TABLE fingerprints (
    player_name   TEXT PRIMARY KEY,
    fingerprint   TEXT NOT NULL,       -- fp.MarshalString() output
    game_count    INTEGER NOT NULL,
    model_tag     TEXT NOT NULL,       -- scf.ModelTag() at last scoring
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
