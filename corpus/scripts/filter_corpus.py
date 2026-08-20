#!/usr/bin/env python3
"""Filter the harvest corpus to players with ≥MIN_GAMES games, capped at
MAX_PER_PLAYER most-recent replays per player. Copies the selected .rep files
into corpus/replays/ and writes filtered manifests + a corpus-manifest.json
with per-file SHA-256 hashes.

Usage:
    python3 corpus/scripts/filter_corpus.py [HARVEST_DIR]

HARVEST_DIR defaults to ../screpharvest/harvest (relative to repo root).
"""

import hashlib
import json
import os
import shutil
import sys
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path

MIN_GAMES = 20
MAX_PER_PLAYER = 50

REPO_ROOT = Path(__file__).resolve().parents[2]
CORPUS_DIR = REPO_ROOT / "corpus"
REPLAYS_OUT = CORPUS_DIR / "replays"


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 16), b""):
            h.update(chunk)
    return h.hexdigest()


def main():
    harvest_dir = Path(sys.argv[1]) if len(sys.argv) > 1 else REPO_ROOT.parent / "screpharvest" / "harvest"
    harvest_dir = harvest_dir.resolve()

    replays_jsonl = harvest_dir / "replays.jsonl"
    identities_jsonl = harvest_dir / "identities.jsonl"
    pros_merged = harvest_dir.parent / "pros_merged.json"

    for p in [replays_jsonl, identities_jsonl, pros_merged]:
        if not p.exists():
            sys.exit(f"Missing: {p}")

    print(f"Reading {replays_jsonl} ...")
    with open(replays_jsonl) as f:
        all_replays = [json.loads(line) for line in f]
    print(f"  {len(all_replays)} total rows")

    by_player = defaultdict(list)
    for r in all_replays:
        by_player[r["auroraId"]].append(r)

    selected_rows = []
    selected_files = set()
    eligible_players = 0

    for aid, rows in sorted(by_player.items()):
        if aid == 0:
            continue
        if len(rows) < MIN_GAMES:
            continue
        eligible_players += 1
        rows.sort(key=lambda r: r.get("timestamp", 0), reverse=True)
        for r in rows[:MAX_PER_PLAYER]:
            fname = r["file"].replace("replays/", "")
            selected_rows.append(r)
            selected_files.add(fname)

    print(f"  {eligible_players} players with ≥{MIN_GAMES} games")
    print(f"  {len(selected_rows)} rows selected (cap {MAX_PER_PLAYER}/player)")
    print(f"  {len(selected_files)} unique replay files")

    REPLAYS_OUT.mkdir(parents=True, exist_ok=True)

    print("Copying replay files ...")
    copied = 0
    missing = 0
    for fname in sorted(selected_files):
        src = harvest_dir / "replays" / fname
        dst = REPLAYS_OUT / fname
        if src.exists():
            shutil.copy2(src, dst)
            copied += 1
        else:
            print(f"  WARNING: missing {src}")
            missing += 1
    print(f"  {copied} copied, {missing} missing")

    print("Writing filtered replays.jsonl ...")
    selected_rows.sort(key=lambda r: r.get("timestamp", 0))
    with open(CORPUS_DIR / "replays.jsonl", "w") as f:
        for r in selected_rows:
            f.write(json.dumps(r, ensure_ascii=False) + "\n")

    print("Copying identities.jsonl ...")
    shutil.copy2(identities_jsonl, CORPUS_DIR / "identities.jsonl")

    print("Copying pros_merged.json ...")
    shutil.copy2(pros_merged, CORPUS_DIR / "pros_merged.json")

    print("Computing SHA-256 hashes ...")
    file_hashes = {}
    total_bytes = 0
    for fname in sorted(selected_files):
        fp = REPLAYS_OUT / fname
        if fp.exists():
            file_hashes[fname] = sha256_file(fp)
            total_bytes += fp.stat().st_size

    max_plausible = 1_900_000_000_000  # ~year 2030, filter uint32 sentinels
    timestamps = [r.get("timestamp", 0) for r in selected_rows if r.get("timestamp") and r["timestamp"] < max_plausible]
    min_ts = min(timestamps) if timestamps else 0
    max_ts = max(timestamps) if timestamps else 0
    date_min = datetime.fromtimestamp(min_ts / 1000, tz=timezone.utc).strftime("%Y-%m-%d") if min_ts else "unknown"
    date_max = datetime.fromtimestamp(max_ts / 1000, tz=timezone.utc).strftime("%Y-%m-%d") if max_ts else "unknown"

    manifest = {
        "generated": datetime.now(tz=timezone.utc).strftime("%Y-%m-%d"),
        "filter": {
            "min_games": MIN_GAMES,
            "max_per_player": MAX_PER_PLAYER,
            "exclude_aurora_zero": True,
        },
        "stats": {
            "players": eligible_players,
            "replays": len(selected_files),
            "rows": len(selected_rows),
            "date_range": [date_min, date_max],
            "total_bytes": total_bytes,
        },
        "files": file_hashes,
    }

    manifest_path = CORPUS_DIR / "corpus-manifest.json"
    print(f"Writing {manifest_path} ...")
    with open(manifest_path, "w") as f:
        json.dump(manifest, f, indent=2, ensure_ascii=False)
        f.write("\n")

    print(f"\nDone. {eligible_players} players, {len(selected_files)} replays, {total_bytes / 1024 / 1024:.0f} MB")


if __name__ == "__main__":
    main()
