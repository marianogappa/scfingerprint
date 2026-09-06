# scfingerprint

**Recognise StarCraft: Brood War players by how they play, not by their name.**

Everyone has habits at the keyboard: which hotkeys you use, the little command
loops your fingers repeat without thinking, the rhythm of your clicking. Those
habits are surprisingly personal, and they stay with you. They survive renaming
your account, making a new one, and even switching race.

scfingerprint reads those habits out of a replay and turns them into a
"fingerprint" it can compare against others. So you can ask:

- **Who is this?** Point it at a replay; it tells you which known player it looks like.
- **Are these two accounts the same person?** Point it at two piles of replays.
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

### And where it doesn't

- **One game is a lead, not proof.** Three or more is where it gets confident.
- **Team games and custom maps are three to five times worse.** It was tuned on
  ladder 1v1.
- **It can be wrong.** It reports its own confidence so you don't have to guess,
  and it deliberately understates rather than overstates.

[docs/METHODOLOGY.md](docs/METHODOLOGY.md) spells out exactly what these numbers
mean, how they were measured, and what the tool cannot claim. Please don't
accuse anyone of anything on the strength of one number.

## Install

```bash
go install github.com/marianogappa/scfingerprint/cmd/scfingerprint@latest
```

That gives you a `scfingerprint` command. Everything below is a real run with
real output.

This needs a Go toolchain for now. Prebuilt binaries for Windows, macOS and
Linux, so you can just download and run, are tracked in
[#54](../../issues/54).

## What you can do with it

<details>
<summary><b>Who is in this replay?</b></summary>

The simplest thing it does. Give it one replay file; it reports every human
player in it and who each one looks like.

```bash
scfingerprint match game.rep
```

`game.rep` is any Brood War replay file. Nothing else is needed — the list of
known players is built into the binary.

Output (a real ladder replay, where player one is the pro Larva):

```
✓ LEAD: JSA_Larva looks like Larva, pending more games

  Confidence: lead (1 game of evidence)

  Why: Larva scored 5.04, which is 1.59 clear of the next candidate (Judge, 3.45).
       A stranger reaches this score about 1 in 15 times against a 68-player catalog.
       Worth following up with more games before treating it as an identification.

  Liquipedia: https://liquipedia.net/starcraft/Larva_(Player)

✗ NO MATCH: kroking is not anyone in the catalog

  Confidence: weak (1 game of evidence)

  Why: the best candidate (Skey, 3.00) is only 1.00 clear of the next one (Scan, 2.00),
       and a stranger reaches that score roughly 1 in 2 times against a 68-player catalog.
       That is coin-flip territory, not evidence of anything.

  Scale: none < weak < lead < strong
    strong  treat as an identification, then confirm by hand
    lead    promising; means little until confirmed with more games
    weak    the score a stranger gets; not evidence of anything
    none    nothing cleared the reporting threshold

  Re-run with -v for the full candidate table, -vv for run metadata, -q or -qq for less.
  Exit code 0 means the bar was met; set the bar with --min-verdict strong|lead|weak|any.
  JSON goes to stdout when piped or redirected; --jsonl streams one object per line.
```

Reading it:

- The **✓/✗ line is the call.** You don't have to weigh any numbers — the tool
  already did, and the exit code agrees with the symbol. The wording scales
  with the confidence: only a **strong** verdict says "X *is* Y"; a lead only
  "looks like".
- **Confidence** is one of four tiers, and the scale legend at the bottom of
  every run says what each one means.
- The **Why block** is the justification, so the call is auditable rather than
  magic.
- When the matched player has a verified **Liquipedia** page, the report links
  it (50 of the 70 built-in players do; the rest are ladder handles without a
  page). The link also rides along in the JSON as `liquipedia` on each match.

Later examples elide the trailing scale legend and flag hints for brevity.

The second player, `kroking`, is not a pro and is not in the catalog — so the
right answer for them is "no idea", and the tool says so.

Want the underlying numbers? `-v` adds the full candidate table, `-vv` adds the
model version and per-game breakdown. `-q` shows only the ✓/✗ lines, `-qq`
nothing at all.

Exit code is 0 when any player reaches the bar (a *lead* by default; pick your
own with `--min-verdict strong|lead|weak|any`), 1 otherwise.

</details>

<details>
<summary><b>Who is this player, using many games as evidence?</b></summary>

One game is a lead. Several games of the same person is real evidence. Put
their replays in a folder and name the player.

```bash
scfingerprint match --name JSA_Larva --dir larva-replays/
```

- `--dir` — a folder of `.rep` files. Searched recursively.
- `--name` — the player's in-game name, exactly as it appears in the replay.
  Needed because each replay has two players and it must know which one is
  yours.

Output (with `-v` to also show the candidate table):

```
✓ MATCH: JSA_Larva is Larva

  Confidence: strong (6 games of evidence)

  Why: Larva scored 5.25, which is 2.27 clear of the next candidate (Judge, 2.98).
       A stranger reaches this score about 1 in 15 times against a 68-player catalog.
       6 games agreeing while pulling 2.27 z clear of the field is what makes this strong. Still confirm by hand before acting on it.

  LABEL    Z     COSINE  GAMES  STRANGER SCORES THIS HIGH
  Larva    5.25  0.963   6      1 in 15
  Judge    2.98  0.551   6      1 in 2
  Soo      2.82  0.523   6      below any bar
  Soma     2.67  0.494   6      below any bar
  Stryker  2.38  0.441   6      below any bar
  Gunwook  2.08  0.387   6      below any bar
```

Six games instead of one pushes the cosine from 0.891 to **0.963** and widens
the gap over the runner-up from 1.59 to **2.27**. That widening gap is the real
signal — more games make the right answer pull away from the field, and that
decisive margin on 3+ games is exactly what upgrades a lead to **strong**.

**Don't know the in-game name?** Use the player slot instead. `--player 0` is
the first player, `--player 1` the second:

```bash
scfingerprint match --player 0 --dir larva-replays/
```

Or run `scfingerprint match` on a single replay first — it prints every player's
name.

</details>

<details>
<summary><b>Are these two accounts the same person?</b></summary>

The question the tool is really for. It needs nothing from the built-in player
list — it just compares two piles of replays against each other.

```bash
scfingerprint same \
  --a account-one/  --name-a JSA_Larva \
  --b account-two/  --name-b JSA_Larva
```

- `--a` / `--b` — a folder of replays, or a single `.rep` file, for each side.
- `--name-a` / `--name-b` — the in-game name to look at on each side. Skip them
  if each replay only has one candidate player; the tool will tell you if it
  needs them.

Two accounts that really are the same human:

```
✓ MATCH: the two sides are the same player

  Confidence: strong (12 games of evidence)

  Why: the pair scored z=4.93 (cosine 0.906) on 12 games (6 + 6).
       A stranger reaches this score about 1 in 1,000 times in a direct 1:1 comparison.
       12 games agreeing at that rate is what makes this strong rather than a lead. Still confirm by hand before acting on it.
```

The same command, with two genuinely different pros:

```
✗ NO MATCH: the evidence does not show the two sides are the same player

  Confidence: weak (12 games of evidence)

  Why: the pair scored z=-0.38 (cosine -0.059) on 12 games (6 + 6).
       That score clears no operating point; a stranger reaches it routinely.
       That is coin-flip territory, not evidence of anything.
```

Note how far apart those are — 4.93 versus -0.38. When it is the same person the
answer is usually not subtle.

Exit code is 0 for a confident match (`--min-verdict` picks the bar, as with
`match`), 1 otherwise, so you can use it in scripts.

</details>

<details>
<summary><b>Teach it a new player</b></summary>

The built-in list has 82 players. To add your own, build a fingerprint file from
their replays.

```bash
scfingerprint enroll --label "MyLarva" --name JSA_Larva --dir larva-replays/ -o mylarva.json
```

- `--label` — whatever you want to call this person. Required.
- `--name` — their in-game name, as with `match`.
- `--dir` — folder of their replays. You can also list `.rep` files directly
  instead.
- `-o` — where to write the fingerprint. Defaults to `<label>.fingerprint.json`.

```
self-consistency: 0.903 (pass)
wrote mylarva.json (6 games)
```

**That `self-consistency` number is a safety check, and it matters.** It splits
the games in half by date and asks whether the two halves look like the same
person. Around 0.96 is a healthy single human. A known case of two people
wrongly merged into one entry scored 0.44 — and that one bad entry ruined the
accuracy of every other comparison in the catalog. So enroll refuses to write a
file that fails:

```
error: hygiene: self-consistency unavailable: fingerprint: need at least 4 games across 2 chronological blocks, have 1 games in 1 blocks (re-check the games belong to one person, or pass --skip-gate)
```

Here it only had one game, which is too few to check at all. Either give it at
least four games, or override deliberately:

```bash
scfingerprint enroll --label "MyLarva" --name JSA_Larva game.rep -o mylarva.json --skip-gate
```

```
wrote mylarva.json (1 games)
```

The result is one JSON file holding the fingerprint as a single string, so it
fits in one database column:

```
{"v":3,"n":6,"races":{"z":6},"mean":"WBHKQ1dpgEMIibo+1XPQQ4//okORf2BDNRDlPaZjDzyHVQE7OMq/PE9RAD+9U5s+ ...
```

`v` is the format version, `n` the number of games, `races` the split by race.
It does not contain the replays or anything about them — just the habits.

</details>

<details>
<summary><b>Use it in scripts</b></summary>

The two output streams are separate by design: **stderr** carries the
human-readable report, **stdout** carries JSON and nothing else, ever. JSON is
emitted whenever stdout is piped or redirected — no flag needed — so every
command composes:

```bash
# Is anyone in this replay a known pro? (silent, exit code only)
if scfingerprint match game.rep --min-verdict strong -qq; then
  echo "pro detected"
fi

# Who does the tool think player one is?
scfingerprint match --player 0 game.rep 2>/dev/null | jq -r '.[0].matches[0].label'

# Scan a whole folder, keep only strong calls, as CSV
scfingerprint match replays/*.rep --jsonl 2>/dev/null \
  | jq -r 'select(.verdict=="strong") | [.file, .player, .matches[0].label] | @csv'

# Watch a human-readable run on the terminal while saving machine output
scfingerprint match replays/*.rep --jsonl > results.jsonl
```

`--jsonl` streams one compact object per player instead of one indented array,
for batch work. `--json` forces JSON onto a terminal too.

The JSON carries the determination explicitly, so scripts never have to
re-derive the call from raw numbers:

```bash
scfingerprint same --a account-one/ --name-a JSA_Larva --b account-two/ --name-b JSA_Larva 2>/dev/null
```

```json
{
 "verdict": "strong",
 "z": 4.932859790149249,
 "cosine": 0.9056169584593489,
 "evidence_n": 12,
 "operating_points": {
  "fpr_1e2": true,
  "fpr_1e3": true,
  "fpr_1e4": false
 },
 "fpr": 0.0010000000000000009,
 "model_is_synthetic": false
}
```

- `verdict` — the call: `strong`, `lead`, `weak` or `none`. Same tiers as the
  human output and the exit code.
- `operating_points` — which strictness bars this result cleared. `fpr_1e3: true`
  means "a stranger clears this bar only 1 time in 1,000".
- `fpr` — the strictest bar it cleared, as a number. `1.0` means none.
- `model_is_synthetic` — should always be `false`. If it is ever `true`, the
  scores are meaningless test data and the tool will shout at you.

For `match`, each report carries the `file` it came from plus per-candidate
`search_fpr` and `catalog_size`. `search_fpr` is the honest one to show a
person: it accounts for the fact that comparing against 68 players gives 68
chances to get lucky, so it is always worse than the per-comparison
`operating_points`.

</details>

<details>
<summary><b>Show only high-confidence results, or search a wider net</b></summary>

Two knobs on `match`.

**Hide weak guesses.** By default anything scoring below z=2 is hidden. Raise it
to see only strong candidates:

```bash
scfingerprint match --name JSA_Larva --dir larva-replays/ --min-z 4
```

```
✓ LEAD: JSA_Larva looks like Larva, pending more games

  Confidence: lead (6 games of evidence)

  Why: Larva scored 5.25, and no other candidate cleared the reporting threshold.
       A stranger reaches this score about 1 in 15 times against a 68-player catalog.
       Worth following up with more games before treating it as an identification.
```

**Search more of the built-in list.** Each known player is tagged with how sure
the curation is. By default only well-established entries are searched:

```bash
scfingerprint match --name JSA_Larva --dir larva-replays/ --min-confidence candidate
```

- `confirmed` — only the entries with the strongest evidence
- `high` — the default
- `candidate` — everything, including entries built from old archive replays

Widening to `candidate` adds more possible answers, so expect more noise along
with more coverage.

</details>

<details>
<summary><b>Who owns this account name? (no replay needed)</b></summary>

Sometimes you have a name and no game. `whois` answers from the built-in
identity map — pro name, Battle.net account, battle tag, and every alt that
account owns on every gateway:

```bash
scfingerprint whois Queennnnnn
```

```
✓ Queen (matched by toon)

  aurora 18242965  battle tag 김명운  (KOR)
    lllllIlIIIlIl    U.S. West
    wooni222         U.S. West
    IIllIIIIIIIl     U.S. East
    lllIIllIIIIllII  U.S. East
    lllIIllIIllIII   U.S. East  18 games last week
    wooni22222222    Europe
    Queennnnnn       Korea
    Queennnnnnnnn    Asia
    fingerprint: in the catalog as queen (confirmed, 60 games) — `scfingerprint match` can confirm this
    liquipedia: https://liquipedia.net/starcraft/ZerO
    source: bnet-webapi, last verified 2026-09-06

  This is a name lookup, not a fingerprint. Accounts get shared, sold, lent
  to a friend and renamed, so it is a second opinion and never evidence.
  Confirm from a replay: scfingerprint match <replay.rep>.
```

The name you looked up is on Korea; the account they are actually playing is on
US East.

It takes whichever kind of string you happen to have: an account name, a
progamer name (pros hold several accounts, and you get all of them), or an
aurora id.

Two things this is good for beyond curiosity. It tells you **which alt is
actually live**, which is the difference between a thin and a full set of
replays for a player — someone can look inactive on their main handle and have
a hundred games on another. And it labels a lobby full of barcodes instantly,
with no parsing at all.

`scfingerprint match` also reports what the map says, next to its own verdict:

```
✓ LEAD: f518df851df18df looks like Shinee, pending more games

  Why: Shinee scored 4.57, which is 2.17 clear of the next candidate (Byul, 2.40).
       A stranger reaches this score about 1 in 13 times against a 82-player catalog.
       Only 1 game of evidence keeps this a lead rather than strong; get 3+ games.

  Identity map: agrees. f518df851df18df is Shinee's account (last verified 2026-09-06).
  A name lookup, not evidence: it never affects the score above.
```

Two independent opinions agreeing costs you nothing extra. When they *disagree*
it says so loudly, because that is the interesting case — a shared or sold
account, or an entry that has gone stale — and it is a thing to go and look at
rather than something for the tool to quietly reconcile. Pass `--no-registry`
to turn the line off; with it or without it the score is identical.

**It is not evidence, and it is not meant to be.** It is a name lookup, and
names are the exact thing this tool exists to see past: accounts get shared,
sold, lent to a friend and renamed, and any shipped snapshot is stale the day
after it ships. Every entry carries where it came from and when it was last
checked so you can judge it. To actually identify someone, point
`scfingerprint match` at a replay.

</details>

<details>
<summary><b>Browse the built-in catalog</b></summary>

Who does it already know, and on how much evidence?

```bash
scfingerprint dataset list
```

```
82 players in the built-in catalog: 71 confirmed, 11 high, 0 candidate

  PLAYER   RACES                     CONFIDENCE  GAMES  REPLAYS  LIQUIPEDIA
  AAAA     Terran (60)               confirmed   60     60
  Action   Zerg (59)                 confirmed   59     59
  Alen     Zerg (60)                 confirmed   60     60       https://liquipedia.net/starcraft/Alen
  Ample    Terran (60)               confirmed   60     60       https://liquipedia.net/starcraft/Ample
  Artosis  Terran (59), Protoss (1)  confirmed   60     60       https://liquipedia.net/starcraft/Artosis
  ...
```

One player in detail, looked up by ID, label or any alias (case-insensitive):

```bash
scfingerprint dataset show Larva
```

```
Larva (id: larva)

  Confidence:  confirmed
  Races:       Zerg (49)
  Training:    49 games from 49 catalogued replays, feature version 3
  Source:      cwal-harvest
  Aliases:     Larva (primary)
  Liquipedia:  https://liquipedia.net/starcraft/Larva_(Player)
  Notes:       enrolled from cwal-harvest corpus
```

And the fingerprint itself, in the same single-string format `enroll` writes,
so you can store catalog entries in your own database:

```bash
scfingerprint dataset fingerprint Larva > larva.fingerprint.json
```

Both `list` and `show` follow the usual contract: human output on stderr, JSON
on stdout when piped (each entry carries `races`, `games`, `replays`,
`aliases`, `liquipedia` and more).

</details>

<details>
<summary><b>Check the built-in player list is healthy</b></summary>

```bash
scfingerprint dataset verify
```

```
verified 70 identities
catalog is clean
```

This re-runs the safety checks over every shipped entry: is each one internally
consistent, and does any pair look suspiciously like the same person (which
would mean a curation mistake). It also checks the identity map for two entries
that are really one Battle.net account. Exit code 1 if it finds anything.

In a checkout you can also check which account each entry's training replays
actually came from, which the behavioural checks cannot see — one account
enrolled twice looks perfectly consistent, because it is:

```bash
scfingerprint dataset verify --replay-metadata corpus/replays.jsonl
```

```
verified 70 identities
FINDING [enrollment_outliers] [ssak]: account 18372656 is behind 52 of 54 enrolment
replays; the other 2 came from a different account — an alt, or someone else's games
```

Which of those two it is needs a human; the point is that nothing surfaced it
before.

</details>

<details>
<summary><b>Dump the raw numbers</b></summary>

For debugging, or if you want to do your own maths.

```bash
scfingerprint extract game.rep
```

```json
[
 {
  "file": "game.rep",
  "players": [
   {
    "player_id": 0,
    "name": "JSA_Larva",
    "race": "Zerg",
    "vector": [
     432.10112551645534,
     289.6278387234649,
     0.32972209138012243,
     ...
```

`vector` is the fingerprint before any comparison: a few hundred numbers
describing this player's habits in this game. Also reported are `frames` (game
length) and `cmd_count` (how many actions they issued).

</details>

## Who it already knows

82 players, mostly Korean pros, built from a labelled corpus of about 9,500
ladder and non-ladder replays. Run `scfingerprint dataset list` to see them all. Each entry
records how confident the curation is, so you can ask for only the solid ones
(see `--min-confidence` above).

Alongside those fingerprints it ships an **identity map**: 129 pro names, 154
Battle.net accounts and 626 account names across all five gateways, with 77 of
those accounts active on more than one gateway. It is a name lookup and nothing
more — see `scfingerprint whois` above — but it covers far more players than
have fingerprints, which is what makes it useful for deciding whose replays to
go and get next.

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

The identity map is a separate API, because it is a separate and much weaker
kind of answer:

```go
reg, err := scf.BuiltinRegistry()

// Zero-replay lookup: who owns this account name, and what else do they own?
acc, ok := reg.LookupToon("Queennnnnn")
accs := reg.LookupName("Queen")        // pros hold several accounts
acc, ok = reg.LookupAurora(18242965)
live, _ := acc.PrimaryToon()           // the alt they are actually playing on
```

You can also opt a match into carrying the map's opinion alongside the
fingerprint's:

```go
results, err := scf.MatchMany(games, db, scf.WithRegistry(reg))
if op := results[0].Registry; op != nil && !op.Agrees {
    // The account name says one player, the way it was played says another.
}
```

`WithRegistry` is opt-in and changes nothing about the scoring: with it or
without it, `Z`, `Cosine`, `SearchFPR` and the result ordering are identical.
The opinion is derived from the account name (`PlayerGame.Toon`, or the replay
slot name) and reported in its own field. **Never merge it into a score.** A
registry hit is a name lookup; when it disagrees with the fingerprint, that
disagreement is the finding — a shared or sold account, or a stale entry — not
something to reconcile away.

Full reference: [pkg.go.dev](https://pkg.go.dev/github.com/marianogappa/scfingerprint).

</details>

<details>
<summary><b>For developers: repository layout and rebuilding the model</b></summary>

| Path | Contents |
|---|---|
| `*.go` (root) | the public API — the only thing external callers import |
| `cmd/scfingerprint/` | the CLI |
| `internal/` | implementation: features, scoring, training, evaluation, hygiene, catalog |
| `internal/cmd/` | tooling that rebuilds the committed artifacts; not part of the public surface |
| `internal/dataset/players/` | the built-in catalog: one JSON file per known player |
| `internal/registry/registry.json` | the built-in identity map: name ↔ aurora id ↔ battle tag ↔ toons |
| `internal/bnet/` | client for a running SC:R client's local web-api; used only by the refresh tool |
| `corpus/` | the labelled replay corpus every published number traces back to (fetched from release assets) |
| `docs/METHODOLOGY.md` | how it works, and what it cannot claim |

### internal/cmd

Deliberately not installable and not part of the API. Every one of these either
rebuilds a committed artifact or guards one:

| Command | Role |
|---|---|
| `fetch-corpus` | downloads `corpus/replays/` from its release assets; replaces `git lfs pull` |
| `publish-corpus` | maintainer-only: packages `corpus/replays/` and publishes it as a release asset |
| `extract-corpus` | replays → labelled feature CSV; the input to everything below |
| `train` | CSV → `internal/model/artifact.json` |
| `seed-dataset` | CSV → `internal/dataset/players/` |
| `eval` | metrics against regression gates; one gate runs in CI |
| `corpus-audit` | label hygiene, before a corpus is trusted for training or enrollment |
| `registry-refresh` | rebuilds `internal/registry/registry.json` (see below) |

Full rebuild from the committed corpus:

```bash
go run ./internal/cmd/fetch-corpus
go run ./internal/cmd/extract-corpus -metadata corpus/replays.jsonl -replays-dir corpus -out /tmp/features.csv
go run ./internal/cmd/corpus-audit -csv /tmp/features.csv
go run ./internal/cmd/train -csv /tmp/features.csv -out internal/model/artifact.json
go run ./internal/cmd/eval -csv /tmp/features.csv -gates internal/eval/baselines/cwal_harvest_gates.json
go run ./internal/cmd/seed-dataset -csv /tmp/features.csv -max-games 60
```

The last step rewrites `internal/dataset/players/` and should reproduce what is
committed byte for byte.

### Refreshing the identity map

Pros make new accounts constantly, so the map needs re-running rather than
curating once. `registry-refresh` has three modes:

```bash
# Rebuild from the corpus. Offline and deterministic, so the shipped map always
# has a reproducible floor.
go run ./internal/cmd/registry-refresh -seed

# Re-verify every account against a live client, picking up new alts.
go run ./internal/cmd/registry-refresh -refresh -v

# Find accounts the map does not know about at all.
go run ./internal/cmd/registry-refresh -discover -unknowns /tmp/unknowns.json
```

`-refresh` and `-discover` drive the local web-api that a running SC:R client
exposes on loopback, so they need the game **running and logged in to
Battle.net** on the same machine. The port changes every launch and is
discovered rather than configured. Neither mode is ever on the matching path.

The interesting one is `-discover`, and it exists because of a hard limit: the
profile route only expands alts *within* accounts you already know, and there
is no by-aurora-id route, so an account you have never seen is unreachable.
Private practice games are the way in — pros spar pros, so the unknown rival in
a 1v1 practice lobby is very likely another pro:

```
anchor on a known account
  → its non-ladder 1v1 games          (link is a bare number, not "MM-…")
  → the rival account name we do not know
  → download that game's .rep and fingerprint it against the catalog
  → confident match: one profile call onboards the whole new account,
                     with every alt it owns, on every gateway
  → no match:        queued in -unknowns for manual naming
```

The fingerprint is what does the naming, which is the right way round: the map
is built by identification, not trusted as a substitute for it. A single
practice game can only ever be a lead, so the gate is the same trade the CLI
makes when it upgrades a lead — a lead-grade family-wise rate plus a decisive
margin over the runner-up (`-max-fpr`, `-min-margin`) — and every write records
the anchor and game behind it so it can be audited or reverted.

A run over 25 anchors found 30 unknown rivals in private 1v1s, named one, and
queued the other 29 with their aurora id and best guess. The one it named is
the interesting case: `f518df851df18df` had been **renamed since the game**, so
no profile lookup could reach it — but each player uploads their own copy of a
game, and the upload's URL carries the uploader's account id, which recovered
aurora 15183299. The fingerprint said Shinee from that single game — z=4.57,
2.17 clear of the runner-up — and 15183299 is Shinee's account in
`corpus/identities.jsonl`. Neither half knew what the other was doing.

A low naming rate is the expected outcome and not a defect: most people a pro
practises against are not in the catalog at all, and the queue — not the
registry — is where they belong until a human looks.

Research harnesses that produced published one-off findings (cross-era probing,
open-set alias discovery, catalog accuracy measurement) are not kept here. They
live with the write-ups they produced, outside this repository.

</details>

## License

[MIT](LICENSE)
