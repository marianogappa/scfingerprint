# scfingerprint

StarCraft: Brood War player fingerprinting — identify players by **how** they play, not what they're named.

Players have stable, measurable habits: which hotkey groups they use, their muscle-memory command loops, their action rhythm. These survive name changes, account switches, and even race switches. This library extracts those habits from replays into versioned fingerprint vectors and matches them with calibrated confidence scores.

Validated in a research spike on ~2,900 ladder replays (23 players) and ~1,000 casual team-game replays:

- Single-game verification: EER 0.21%, 99.7% true-positive rate at a 1-in-1,000 false-positive threshold.
- 3-game evidence: EER 0.05%, TPR 1.000 at 1-in-1,000 — even against same-race impostors.
- Found real smurf pairs in the wild (`MBU_Shine ≡ wG_Shine`, `58BoJi4485 ≡ IllIllIlllIIIII`) and re-identified a ladder player across two unrelated replay collections (z=+7.6 where the null's 99th percentile is +4.1).

See the [methodology and honest-limitations documentation](../../issues/11) for how the fingerprints work, what they can and cannot claim, and how the numbers above were measured.

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

Planned commands ([#7](../../issues/7)):

```bash
scfingerprint match game.rep            # who is each player in this replay?
scfingerprint same a.rep b.rep          # are these the same player?
scfingerprint enroll --player X *.rep   # build a fingerprint from known games
scfingerprint extract game.rep          # dump raw feature vectors
scfingerprint dataset verify            # check the built-in dataset's integrity
```

## Built-in dataset

Versioned player fingerprints (with provenance and confidence tiers) shipped in-repo ([#8](../../issues/8)).

## Status

Pre-implementation — the module, CI, and release automation exist; the fingerprinting packages land next. Design and roadmap live in the [issues](../../issues).

## License

[MIT](LICENSE)
