# scfingerprint

StarCraft: Brood War player fingerprinting — identify players by **how** they play, not what they're named.

Players have stable, measurable habits: which hotkey groups they use, their muscle-memory command loops, their action rhythm. These survive name changes, account switches, and even race switches. This library extracts those habits from replays into versioned fingerprint vectors and matches them with calibrated confidence scores.

Measured on ~2,900 ladder replays (23 players) and a 229-player harvest corpus:

- Single-game verification: EER 0.21%, 99.7% true-positive rate at a 1-in-1,000 false-positive threshold.
- 3-game evidence: EER 0.05%, TPR 1.000 at 1-in-1,000 — even against same-race impostors.
- 1:N identification against the shipped catalog: 98.9% top-1 on single games, 99.7% on 3-game probes, measured leakage-free.
- Fingerprints survive multi-decade gaps: old-era replays rank the true pro 1st of 68 across 14–20 year gaps.

See [docs/METHODOLOGY.md](docs/METHODOLOGY.md) for how the fingerprints work, what they can and cannot claim, and how these numbers were measured.

## Install

Library:

```bash
go get github.com/marianogappa/scfingerprint
```

CLI:

```bash
go install github.com/marianogappa/scfingerprint/cmd/scfingerprint@latest
```

## Library quickstart

The root package is the entire supported API. It operates on already-parsed [screp](https://github.com/icza/screp) in-memory models, so callers like [screpdb](https://github.com/marianogappa/screpdb) never pay a re-parse.

```go
import scf "github.com/marianogappa/scfingerprint"

// Who is this player? Search the shipped catalog of known players.
db, err := scf.BuiltinDataset(scf.ConfidenceHigh)
results, err := scf.Match(replay, playerID, db)

// Stronger evidence: several games of the same player.
results, err := scf.MatchMany(games, db)

// Are these two players the same human? No dataset needed.
verdict, err := scf.Same(gamesA, gamesB)

// Build a fingerprint you can store in one database column.
fp, err := scf.Enroll(games, scf.Meta{Label: "C9_FlaSh"})
blob, err := fp.MarshalString()
```

Every result carries a calibrated z-score, an evidence count, and the operating points it clears — never a bare boolean. Full API: [pkg.go.dev](https://pkg.go.dev/github.com/marianogappa/scfingerprint), and [docs/SCREPDB_INTEGRATION.md](docs/SCREPDB_INTEGRATION.md) for a worked integration.

## CLI quickstart

```bash
scfingerprint match game.rep                          # who is each player? vs built-in dataset
scfingerprint match --name FlaSh --dir replays/       # multi-game evidence for one identity
scfingerprint same --a dirA/ --b dirB/                # are these two players the same human?
scfingerprint enroll --label "C9_FlaSh" --dir reps/   # build a fingerprint file (gated)
scfingerprint extract game.rep                        # dump raw feature vectors (JSON)
scfingerprint dataset verify                          # hygiene checks over the built-in dataset
```

Human-readable tables by default, `--json` for machines. Exit codes: 0 = match found / success, 1 = no match / findings, 2 = error.

## Repository layout

| Path | Contents |
|---|---|
| `*.go` (root) | the public API — the only thing external callers import |
| `cmd/scfingerprint/` | the CLI |
| `internal/` | implementation: features, scoring, training, evaluation, hygiene, catalog |
| `internal/cmd/` | corpus and research tooling, not part of the public surface — see below |
| `internal/dataset/players/` | the built-in catalog: one JSON file per known player |
| `corpus/` | the labeled replay corpus every published number traces back to (Git LFS) |
| `docs/` | methodology and integration contract |

### internal/cmd

These are curation and research tools, deliberately not installable and not part of the API. They exist to regenerate the committed artifacts and to reproduce published results:

```bash
go run ./internal/cmd/extract-corpus   # replays → labeled feature CSV
go run ./internal/cmd/train            # CSV → internal/model/artifact.json
go run ./internal/cmd/eval             # CSV → metrics, with regression gates
go run ./internal/cmd/seed-dataset     # CSV → internal/dataset/players/
go run ./internal/cmd/corpus-audit     # label hygiene before enrollment
go run ./internal/cmd/catalog-check    # leakage-free 1:N top-1 accuracy
go run ./internal/cmd/alias-discovery  # open-set smurf/alias candidates
go run ./internal/cmd/era-probe        # cross-era identification
go run ./internal/cmd/cwal-resolve     # refresh the pro nickname → account registry
```

Regenerating the model and the catalog from the committed corpus:

```bash
git lfs pull
go run ./internal/cmd/extract-corpus -metadata corpus/replays.jsonl -replays-dir corpus -out /tmp/features.csv
go run ./internal/cmd/train -csv /tmp/features.csv -out internal/model/artifact.json
go run ./internal/cmd/eval -csv /tmp/features.csv -gates internal/eval/baselines/cwal_harvest_gates.json
```

## License

[MIT](LICENSE)
