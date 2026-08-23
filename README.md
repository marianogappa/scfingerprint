# scfingerprint

**Recognise StarCraft: Brood War players by how they play, not by their name.**

Everyone has habits at the keyboard: which hotkeys you use, the little command
loops your fingers repeat without thinking, the rhythm of your clicking. Those
habits are surprisingly personal, and they stay with you. They survive renaming
your account, making a new one, and even switching race.

scfingerprint reads those habits out of a replay and turns them into a
"fingerprint" it can compare against others.

So you can ask questions like:

- **Who is this?** Point it at a replay and it tells you which known player it looks like.
- **Are these two accounts the same person?** Point it at two piles of replays and it tells you.
- **Is this new account a smurf?** Same question, asked of the ladder.

## How well does it work?

| Question | Answer |
|---|---|
| Told apart from a stranger, from one game | right ~99.8% of the time |
| Told apart from a stranger, from three games | right ~99.95% of the time |
| Picked out of 68 known players, from one game | right 98.9% of the time |
| Recognised from replays 14–20 years apart | still ranked 1st of 68 |

That last one is the surprising part: someone's habits are recognisable two
decades later.

There are real limits, and they matter. **One game is a lead, not proof.**
Accuracy also drops on kinds of games the tool wasn't tuned for — team games
and custom maps, roughly three to five times worse. And it can be wrong.
[docs/METHODOLOGY.md](docs/METHODOLOGY.md) spells out exactly what these
numbers mean, how they were measured, and what the tool cannot claim.

Please don't use this to accuse someone of something on the strength of one
number. It reports its own confidence honestly so you don't have to.

## Try it

Install:

```bash
go install github.com/marianogappa/scfingerprint/cmd/scfingerprint@latest
```

Ask who is in a replay:

```bash
scfingerprint match game.rep
```

You get a table of the players it thinks are most likely, most likely first,
each with a confidence score and a plain-English verdict on the bottom line.

Ask whether two accounts are the same person:

```bash
scfingerprint same --a folder-of-replays/ --b other-folder/
```

More games means a better answer. Three or more per side is where it gets
genuinely confident.

Everything the CLI can do:

```bash
scfingerprint match game.rep                          # who is each player in this replay?
scfingerprint match --name FlaSh --dir replays/       # who is this, using many games as evidence
scfingerprint same --a dirA/ --b dirB/                # are these two the same person?
scfingerprint enroll --label "C9_FlaSh" --dir reps/   # teach it a new player
scfingerprint extract game.rep                        # dump the raw numbers
scfingerprint dataset verify                          # sanity-check the built-in player list
```

Add `--json` to any of them for machine-readable output. Exit codes: 0 = found
something, 1 = found nothing, 2 = something went wrong.

## Who it already knows

The repo ships fingerprints for 70 players, mostly Korean pros, built from a
labelled corpus of ~7,900 ladder replays. Each one records how confident the
curation is, so you can ask for only the solid ones.

---

<details>
<summary><b>For developers: using it as a Go library</b></summary>

The root package is the entire supported API. `internal/` is sealed and may
change without notice.

It works on already-parsed [screp](https://github.com/icza/screp) in-memory
models, so callers that already parsed the replay never pay for a re-parse.

```go
import scf "github.com/marianogappa/scfingerprint"

// Who is this player? Search the shipped catalog.
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

`PlayerGame` takes either a parsed replay plus a player slot, or a feature
vector you cached earlier:

```go
scf.PlayerGame{Replay: rep, PlayerID: pid}       // extract now
scf.PlayerGame{Vector: cached, Race: "Zerg"}     // reuse a cached vector
```

Store `scf.FeatureVersion()` next to any cached vector and re-extract when it
changes. `scf.ModelTag()` identifies the trained model, for invalidating
anything derived from scores.

Every result carries a calibrated z-score, an evidence count, and the operating
points it clears — never a bare boolean. **For anything shown to a user, read
`SearchFPR`, not `OperatingPoints`:** `Match` is a 1-against-many sweep, and
per-comparison error rates overstate confidence at catalog scale.

Before trusting that two accounts are one person, check
`fp.SelfConsistencyGate()` (does this enrollment look like a single human?) and
`scf.NewCoOccurrence(manifest).Disproved(a, b)` (did they ever play each other?
then they are not the same person).

Full reference: [pkg.go.dev](https://pkg.go.dev/github.com/marianogappa/scfingerprint).

</details>

<details>
<summary><b>For developers: repository layout and rebuilding the model</b></summary>

| Path | Contents |
|---|---|
| `*.go` (root) | the public API — the only thing external callers import |
| `cmd/scfingerprint/` | the CLI |
| `internal/` | implementation: features, scoring, training, evaluation, hygiene, catalog |
| `internal/cmd/` | curation and research tooling, not part of the public surface |
| `internal/dataset/players/` | the built-in catalog: one JSON file per known player |
| `corpus/` | the labelled replay corpus every published number traces back to (Git LFS) |
| `docs/METHODOLOGY.md` | how it works, and what it cannot claim |

### internal/cmd

Deliberately not installable and not part of the API. They regenerate the
committed artifacts and reproduce published results.

Rebuilding the committed artifacts:

| Command | Produces |
|---|---|
| `extract-corpus` | labelled feature CSV — the input to everything below |
| `train` | `internal/model/artifact.json` |
| `seed-dataset` | `internal/dataset/players/` |

Protecting them:

| Command | Checks |
|---|---|
| `eval` | EER/TPR metrics against regression gates (one gate runs in CI) |
| `catalog-check` | leakage-free 1-against-many top-1 accuracy of the shipped catalog |
| `corpus-audit` | label hygiene, before a corpus is trusted for enrollment |

Research and curation:

| Command | Does |
|---|---|
| `alias-discovery` | scans a corpus for smurf/alias candidates against the catalog |
| `era-probe` | identifies players across eras; how old-era pros get enrolled |
| `cwal-resolve` | refreshes the pro nickname → account registry from cwal.gg |

Full rebuild from the committed corpus:

```bash
git lfs pull
go run ./internal/cmd/extract-corpus -metadata corpus/replays.jsonl -replays-dir corpus -out /tmp/features.csv
go run ./internal/cmd/train -csv /tmp/features.csv -out internal/model/artifact.json
go run ./internal/cmd/eval -csv /tmp/features.csv -gates internal/eval/baselines/cwal_harvest_gates.json
```

</details>

## License

[MIT](LICENSE)
