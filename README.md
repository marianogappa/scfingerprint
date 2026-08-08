# scfingerprint

StarCraft: Brood War player fingerprinting — identify players by **how** they play, not what they're named.

Players have stable, measurable habits: which hotkey groups they use, their muscle-memory command loops, their action rhythm. These survive name changes, account switches, and even race switches. This library extracts those habits from replays into versioned fingerprint vectors and matches them with calibrated confidence scores.

Validated in a research spike on ~2,900 ladder replays (23 players) and ~1,000 casual team-game replays:

- Single-game verification: EER 0.21%, 99.7% true-positive rate at a 1-in-1,000 false-positive threshold.
- 3-game evidence: EER 0.05%, TPR 1.000 at 1-in-1,000 — even against same-race impostors.
- Found real smurf pairs in the wild (`MBU_Shine ≡ wG_Shine`, `58BoJi4485 ≡ IllIllIlllIIIII`) and identified a pro playing casual team games cross-corpus.

## Planned shape

- **Importable Go library**: operates on already-parsed [screp](https://github.com/icza/screp) in-memory models (no forced re-parse), so tools like [screpdb](https://github.com/marianogappa/screpdb) can integrate cheaply.
- **CLI**: `match`, `same`, `enroll`, `extract`, `dataset verify`.
- **Built-in dataset**: versioned player fingerprints (with provenance and confidence tiers) shipped in-repo.

Design and roadmap live in the [issues](../../issues). Status: pre-implementation.
