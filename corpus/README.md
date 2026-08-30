# Corpus

Labeled StarCraft: Brood War replays used to train and evaluate the
fingerprinting model. Every number this project publishes traces back to this
corpus.

## What's here

| Path | Contents | Storage |
|---|---|---|
| `replays.jsonl` | Per-replay metadata: match ID, player aurora IDs, race, MMR, map, timestamp | git |
| `identities.jsonl` | Player identity records (369 entries): aurora ID → battle tag, rank, handles | git |
| `pros_merged.json` | Pro-player ID mapping from CWAL.gg | git |
| `pro_aliases.json` | Curated ring names that never appear as a toon (Organ ↔ PianO) | git |
| `pro_exclusions.json` | Aurora IDs CWAL maps to a pro that must not be enrolled under it, with evidence | git |
| `cwal_default_list.json` | Raw CWAL.gg Player Tracker snapshot (128 nicknames → 152 accounts, 2026-08-08, curated by WorsT21/Impact44 + DudeNerd) — the provenance behind `pros_merged.json` | git |
| `corpus-manifest.json` | SHA-256 of every `.rep` file, plus aggregate stats | git |
| `corpus-source.json` | Which release assets hold the replays for this commit | git |
| `replays/**/*.rep` | The actual replay files | **GitHub release asset** |
| `scripts/filter_corpus.py` | Script that selected the ladder subset from the full harvest | git |

## Fetching replays

A clone contains **no replay data** — not even pointer files. Download it with:

```bash
go run ./internal/cmd/fetch-corpus
```

That reads `corpus-source.json`, downloads the release asset it names, checks
the archive against its recorded SHA-256 *before* unpacking, extracts into
`replays/`, and then verifies every file against `corpus-manifest.json`. It is
idempotent — a second run downloads nothing.

The replay directory is gitignored and entirely owned by that command.

### Why not Git LFS

The corpus was on Git LFS until it outgrew the free tier: 1 GB of storage and
1 GB of bandwidth per month, against a ~771 MB corpus where a single
`git lfs pull` costs three quarters of the monthly budget and LFS storage is
never reclaimed when files are deleted. Release assets have neither limit.

⚠️ Commits before the migration still reference LFS objects. `git lfs pull`
works there and nowhere after.

## Composition

**9,416 replays, ~771 MB**, from three sources:

| Count | Source |
|---|---|
| 7,935 | Ladder replays selected by `scripts/filter_corpus.py` from the full harvest |
| 343 | Non-ladder replays collected via the local Battle.net web API |
| 1,138 | Harvest replays backfilled so every catalog fingerprint is re-derivable |

The full harvest contains ~23,951 replays across ~2,139 players (1.8 GB). The
ladder subset applies:

- **Exclude** `auroraId == 0` (unidentified opponents)
- **Require** ≥20 games per player
- **Cap** at 50 most-recent replays per player

That filter, originally chosen to fit inside Git LFS's free tier, produced 231
players and 7,935 replays.

The 1,138 backfilled replays close the gap that made this corpus insufficient
on its own: `internal/dataset/players/*.json` `replay_manifest` fields name
every replay behind each enrollment, so a feature-version bump can re-derive
the fingerprints. 1,138 of those names had no file in the repository, and only
existed in the external harvest. `publish-corpus -backfill-from` copies them in
and reports coverage, so this cannot silently regress.

## Verifying integrity

```bash
go run ./internal/cmd/fetch-corpus -verify-only
```

Hashes every replay on disk against `corpus-manifest.json` and reports missing,
corrupt and extra files. No network access. Add `-prune` to delete replays that
are not in the manifest.

To check that the release assets are still reachable without downloading them
(this also runs weekly in CI):

```bash
go run ./internal/cmd/fetch-corpus -check-assets
```

## Publishing a new corpus version

Maintainer-only, and it cannot run in CI — the replays are not in git, so only
a machine that already holds the corpus can build the archive.

```bash
go run ./internal/cmd/publish-corpus -tag corpus-v2 \
    -backfill-from ../screpharvest/harvest
```

That backfills any dataset-referenced replay that is missing, regenerates
`corpus-manifest.json` from the replay tree, packs a deterministic tar+zstd,
creates the `corpus-v2` release (flagged as a prerelease so it never occupies
the repository's "latest release" slot), uploads the asset, and rewrites
`corpus-source.json`. Commit both
JSON files afterwards — they are what ties the commit to the release.

Add `-dry-run` to build and hash the archive locally without touching GitHub.

Corpus releases are versioned independently of software releases (`vX.Y.Z`), so
publishing the software never re-uploads the corpus, and each commit reproduces
its own corpus via the tag in `corpus-source.json`.

### About the archive

Individual `.rep` files are already internally compressed and shrink by ~0.3%
on their own. The corpus nonetheless packs to **38% of raw** (771 MB → 293 MB)
because replays share map and unit data, and a solid archive with a 128 MB zstd
window finds those matches across files. Per-file compression would gain
nothing.

The tar is built deterministically — sorted paths, zeroed mtimes and ownership
— so packing the same replays twice yields byte-identical output, and the
SHA-256 in `corpus-source.json` is reproducible by anyone holding the corpus.

## Provenance

- **Source:** [CWAL.gg](https://cwal.gg) via the `screpharvest` tool
- **Method:** Per-player match history API + vault backfill at MMR ≥ 2300
  over ~100k recent games
- **Harvested:** 2025–2026
- **Why it can't be re-fetched:** CWAL's per-player API retains roughly one
  season; the vault archive shifts as new games arrive; Blizzard's replay S3
  expires at 30 days
