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
| `corpus-manifest.json` | SHA-256 hash of every `.rep` file, plus aggregate stats | git |
| `replays/*.rep` | The actual replay files | **Git LFS** |
| `scripts/filter_corpus.py` | Script that built this filtered corpus from the full harvest | git |

## Filtering

The full harvest contains ~23,951 replays across ~2,139 players (1.8 GB). This
corpus is filtered to keep it within Git LFS's free tier:

- **Exclude** `auroraId == 0` (unidentified opponents, ~5,189 replays)
- **Require** ≥20 games per player
- **Cap** at 50 most-recent replays per player

Result: **231 players, 7,935 replays, ~640 MB**.

## Fetching replays

A regular `git clone` downloads only LFS pointer files (~140 bytes each). To
fetch the actual replay data:

```bash
git lfs pull
```

Or, to fetch only specific files:

```bash
git lfs pull --include="corpus/replays/MM-00008EA0-*.rep"
```

## Verifying integrity

Check that your local replay files match the reference hashes:

```bash
python3 -c "
import hashlib, json, pathlib, sys
manifest = json.load(open('corpus/corpus-manifest.json'))
bad = 0
for fname, expected in manifest['files'].items():
    p = pathlib.Path('corpus/replays') / fname
    if not p.exists():
        continue  # LFS pointer, not fetched
    actual = hashlib.sha256(p.read_bytes()).hexdigest()
    if actual != expected:
        print(f'MISMATCH: {fname}')
        bad += 1
print(f'Checked {len(manifest[\"files\"])} files, {bad} mismatches')
sys.exit(1 if bad else 0)
"
```

## Provenance

- **Source:** [CWAL.gg](https://cwal.gg) via the `screpharvest` tool
- **Method:** Per-player match history API + vault backfill at MMR ≥ 2300
  over ~100k recent games
- **Harvested:** 2025–2026
- **Why it can't be re-fetched:** CWAL's per-player API retains roughly one
  season; the vault archive shifts as new games arrive; Blizzard's replay S3
  expires at 30 days
