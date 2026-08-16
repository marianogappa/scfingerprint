# scfingerprint

StarCraft: Brood War player fingerprinting — identify players by **how** they play, not what they're named.

Players have stable, measurable habits: which hotkey groups they use, their muscle-memory command loops, their action rhythm. These survive name changes, account switches, and even race switches. This library extracts those habits from replays into versioned fingerprint vectors and matches them with calibrated confidence scores.

Validated in a research spike on ~2,900 ladder replays (23 players) and ~1,000 casual team-game replays:

- Single-game verification: EER 0.21%, 99.7% true-positive rate at a 1-in-1,000 false-positive threshold.
- 3-game evidence: EER 0.05%, TPR 1.000 at 1-in-1,000 — even against same-race impostors.
- Found real smurf pairs in the wild (`MBU_Shine ≡ wG_Shine`, `58BoJi4485 ≡ IllIllIlllIIIII`) and re-identified a ladder player across two unrelated replay collections (z=+7.6 where the null's 99th percentile is +4.1).

See the [methodology and honest-limitations documentation](docs/METHODOLOGY.md) for how the fingerprints work, what they can and cannot claim, and how the numbers above were measured.

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

The library operates on already-parsed [screp](https://github.com/icza/screp) in-memory models (no forced re-parse), so tools like [screpdb](https://github.com/marianogappa/screpdb) can integrate cheaply. Planned API ([#6](../../issues/6)):

```go
import "github.com/marianogappa/scfingerprint"

// replay is an already-parsed *rep.Replay from screp.
matches, err := scfingerprint.Match(replay)
```

## CLI quickstart

```bash
scfingerprint match game.rep                          # who is each player? vs built-in dataset
scfingerprint match --name FlaSh --dir replays/       # multi-game evidence for one identity
scfingerprint same --a dirA/ --b dirB/                # are these two players the same human?
scfingerprint enroll --label "C9_FlaSh" --dir reps/   # build a fingerprint file (gated)
scfingerprint extract game.rep                        # dump raw feature vectors (JSON)
scfingerprint dataset verify                          # hygiene checks over the built-in dataset
```

Human-readable tables by default, `--json` for machines. Exit codes: 0 = match
found / success, 1 = no match / findings, 2 = error.

## Built-in dataset

Versioned player fingerprints (with provenance and confidence tiers) shipped in-repo ([#8](../../issues/8)).

## Status

The full pipeline is implemented: feature extraction, offline training, calibrated scoring, fingerprints, catalog hygiene, evaluation harness with CI regression gates, library API, and CLI. The embedded model artifact and built-in dataset are currently synthetic placeholders — real-corpus training and enrollment are the next step. Roadmap lives in the [issues](../../issues).

## License

[MIT](LICENSE)
