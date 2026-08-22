# Reference re-run — 2026-08-22

First evaluation of the real (non-synthetic) embedded artifact against the
research spike's reference corpora. Model under test:
`v1/2026-08-22/d86de99` — `k=150`, trained on `cwal-harvest`
(7,780 games, 229 players).

## Headline

The pro 1v1 reference numbers **reproduce** when the transform is fit on the
corpus being evaluated, which is the methodology the spike used. The amateur
team-game reference number does **not** reproduce, and the cause is label
contamination in that corpus rather than a model regression.

| corpus | fit | scenario | EER | TPR@1e-3 | reference EER | reference TPR@1e-3 |
|---|---|---|---|---|---|---|
| pro 1v1, 24 ids | local | n1_all | **0.199%** | **0.997** | 0.21% | 0.997 |
| pro 1v1, 24 ids | local | n3_same_race | **0.000%** | **1.000** | 0.05% | 1.000 |
| pro 1v1, 24 ids | embedded | n1_all | 0.996% | 0.573 | — | — |
| pro 1v1, 24 ids | embedded | n3_same_race | 0.044% | 0.997 | — | — |
| amateur team, 14 ids (gated) | local | n1_all | 2.693% | 0.665 | 1.19% | 0.957 |
| amateur team, 14 ids (gated) | local | n3_all | 1.178% | 0.965 | — | — |
| amateur team, 14 ids (gated) | embedded | n1_all | 7.497% | 0.011 | — | — |
| cwal-harvest, 227 ids | embedded (in-domain) | n1_all | 1.220% | 0.916 | — | — |
| cwal-harvest, 227 ids | embedded (in-domain) | n3_all | 0.657% | 0.983 | — | — |

Gate outcomes against the committed baselines:

- `pro_1v1_gates.json`, locally-fit: **all gates passed**.
- `pro_1v1_gates.json`, embedded artifact: fails `n1_all` (EER 0.00996 > 0.0026;
  TPR@1e-3 0.573 < 0.992). Expected — see domain shift below.
- `amateur_team_gates.json`, locally-fit on the gated corpus: fails `n1_all`
  (EER 0.02693 > 0.0149; TPR@1e-3 0.665 < 0.952).

## Reproducing

The reference corpora are not in this repository (size and licensing). Both
were name-labelled, using the corpus-local rule that the same name is the same
human, via `cmd/extract-corpus -dir`:

```
extract-corpus -dir <pro-corpus> -only-1v1 -out pro_1v1.csv
extract-corpus -dir <amateur-corpus> -out amateur_team.csv

# locally-fit transform: train-frac 0.5 matches the eval enroll split, so
# probe games are unseen by the standardizer, selection and whitening.
train -csv pro_1v1.csv -out artifact_pro_local.json -k 150 -train-frac 0.5 -min-games 14
eval -csv pro_1v1.csv -artifact artifact_pro_local.json \
     -exclusions exclusions_pro.json -min-games 14 -gates eval/baselines/pro_1v1_gates.json
```

`min-games 14` on the pro corpus yields 24 identities from 3,974 player-games,
matching the spike's "23 players, 14–128 games each".

Exclusion manifest used for the pro corpus — the two confirmed alias pairs:

```json
[["58BoJi4485", "IllIllIlllIIIII"], ["MBU_Shine", "wG_Shine"]]
```

## Domain shift, measured again

The embedded artifact is fit on SC:R ladder 1v1 games. Evaluated out of
domain it degrades exactly as the spike warned:

```
amateur team games:  2.69% EER locally-fit  ->  7.50% EER with the embedded transform
```

The spike measured 1.19% -> 6.4% on the same kind of comparison. Heavy users on
a different domain should refit the transform on their own corpus rather than
rely on the shipped one.

## Why the amateur reference does not reproduce

`cmd/corpus-audit` over that corpus shows the labels, not the model, are the
problem. Self-consistency (first half of a label's games versus the second
half; genuine single-person labels score ~0.96):

| label | games | self-consistency |
|---|---|---|
| llIlIlIIIIIlII1 | 84 | 0.984 |
| lIlIIIIIIllllll | 84 | 0.977 |
| ReflectinG0d | 51 | 0.957 |
| Mariano | 202 | 0.859 |
| -=FallenAngel=- | 585 | 0.834 |
| oldie | 338 | 0.741 |
| chobo86 | 607 | 0.690 |
| chobo85 | 1122 | 0.677 |
| chobo85s | 53 | 0.322 |

Six of the nine labels with 50+ games are shared accounts: several humans
behind one name. Those labels' internal clusters score against each other as
impostors, which sets the low-FPR thresholds and collapses TPR@1e-3. Run
ungated, the corpus reports 15.08% EER; restricted to the 14 labels that clear
self-consistency 0.90 it reports 2.693%, with closed-set accuracy rising from
0.601 to 0.989.

The corpus has also roughly tripled since the spike measured it (3,341 replays
now versus ~1,000 then), so it is no longer the same benchmark. Treat the
1.19% figure as unverified rather than regressed.

Side finding: `lIlIIIIIIllllll` and `llIlIlIIIIIlII1` (84 games each, both
clean, never in a game together) score 0.783 against each other — a
previously unrecorded alias pair in that corpus.

## Caveat on the low-FPR columns

`TPR@1e-4` requires at least 10,000 impostor comparisons to be measurable at
all, and `TPR@1e-3` at least 1,000 for a single tail event. Below those pool
sizes the harness currently returns a hard `0`, which reads as total failure
rather than "not measurable" — the zeros in the same-race scenarios above are
that artifact, not a result. Only the `cwal-harvest` run (888,858 impostor
pairs) and the pro `n1_all` run (23,081) have pools large enough to state a
1e-4 number.

# 1:N top-1 against the shipped catalog — 2026-08-22 (issue #45)

An earlier spot check through the public API reported 10.9% top-1 against the
shipped 68-identity catalog, a 10x disagreement with the harness's 0.944
closed-set accuracy. Rebuilding the check keyed on **aurora ID** instead of
the registry's `proName` labels resolves the gap entirely: the low figure was
contaminated ground truth (hypothesis 1), not a broken catalog.

Measured with `cmd/catalog-check`: each catalogued account's games split
chronologically, first half rebuilt into a leakage-free enrollment via the
dataset path (raw mean → `Projected`), second half probed 1:N. Ground truth
is the probe's aurora ID.

| catalog | probe n | probes | top-1 correct | wrong top-1 clears 1e-3 | no lead (z<2) |
|---|---|---|---|---|---|
| rebuilt (leakage-free) | 1 | 1,230 | **98.9%** | 0.1% | 0.0% |
| shipped (upper bound)  | 1 | 1,230 | 99.2% | 0.0% | 0.0% |
| rebuilt (leakage-free) | 3 | 387 | **99.7%** | 0.0% | 0.0% |
| shipped (upper bound)  | 3 | 387 | 99.7% | 0.0% | 0.0% |

The "shipped" rows probe the committed catalog, whose enrollments contain the
probe games (each probe is ~1 of ~45 games in the enrollment mean); they are
an upper bound and agree with the leakage-free number to within half a point.

Hypothesis 3 (divergent fingerprint construction) is settled permanently by
`TestFingerprintPathParity` in `scoring/`: `Fingerprint(Transform(x_i)...)`
and `Transform(mean(x_i))` agree to 1e-9 — the transform is affine, so the
mean commutes through it. Both construction paths produce identical
embeddings.

Reproducing:

```
extract-corpus -metadata corpus/replays.jsonl -replays-dir corpus -out features.csv
catalog-check -csv features.csv        # n=1
catalog-check -csv features.csv -n 3   # 3-game probes
```

# Amateur baseline retirement + drift research — 2026-08-22 (issue #40)

The committed amateur gate (1.19% EER / 0.957 TPR@1e-3, 8 ids) is retired:
it never reproduced (15.08% ungated, 2.693% mixed-gated in the earlier
re-run; 6.4% mixed-gated with today's tooling), and the corpus is a live
replay folder that keeps growing, so any frozen number rots.

## Ground truth from the corpus owner

The labels flagged "shared accounts" by the mixed self-consistency audit are
in fact single humans: **Mariano, oldie, chobo86, chobo85, chobo85s are one
person** (the corpus owner); **-=FallenAngel=-** and **ReflectinG0d** are one
person each. The audit's verdict wording was wrong — the failures are
intra-person drift, not label contamination.

## What drives the drift (measured with cmd/drift-analysis)

Whitened-centroid cosine between strata of the same human, embedded artifact:

| comparison | cosine | reading |
|---|---|---|
| chobo85/P vs chobo85/T | 0.984 | P and T are one style |
| chobo86/P vs chobo86/T | 0.988 | same |
| chobo85/P vs chobo85/Z | 0.495 | Zerg is a different style |
| chobo86/P vs chobo86/Z | 0.584 | same |
| chobo85/Z vs oldie/Z | 0.962 | same human, different account, race-controlled |
| chobo85/Z vs chobo86/Z | 0.911 | same, 7 years apart |
| chobo85/2024-26 vs chobo86/2024-26 | 0.992 | same era, different account |
| chobo85/2017-18 vs chobo85/2024-26 | 0.693 | 9-year drift is real but secondary |
| Mariano/Z vs chobo85/Z | 0.411 | Mariano is 97% vs-AI games — unrepresentative |

**Race dominates; identity holds per race.** Controlling for race, the same
human matches across accounts and years in the genuine range. Zerg vs
Protoss/Terran within one human reads as impostor. vs-AI games form their own
cluster and should not be enrolled or benchmarked.

## Consequences shipped

- `hygiene.AuditLabels`: race-aware label self-consistency (per-race
  half-vs-half, game-weighted). Rescues random-race players from false
  "CONTAMINATED" verdicts (chobo86: 0.870 mixed → 0.941 race-aware; oldie:
  0.892 → 0.930). Limitation: cannot catch two humans on one account playing
  different races.
- `eval.Evaluate` always records the label audit in the report;
  `MinLabelSelfConsistency` excludes failing labels before metrics
  (`-min-label-self-consistency` in cmd/eval).
- `eval.Options.SplitByRace` (`-split-by-race`): identity = (label, race),
  same-label strata excluded from each other's impostor pools — mirrors the
  fingerprint package's per-race sub-means.
- `cmd/corpus-audit` reports mixed and race-aware columns, corrected verdict
  wording, and `-clean-csv` writes the gated corpus.
- `amateur_team_audit.json` is the committed audit record: 16 of 24 labels
  (>= 20 games) pass race-aware self-consistency >= 0.90 against the
  embedded artifact.

## Why no new amateur gate

Even gated and race-split, today's numbers (n1_all EER 6.4–8.4%) reflect a
casual, multi-mode, decade-spanning domain that is much harder than ladder
1v1 — and the corpus mutates as its owner plays. A regression gate needs a
frozen benchmark; freezing a snapshot of this folder is tracked as future
work. Until then the pro 1v1 and cwal-harvest gates carry regression duty.

# Open-set alias discovery — 2026-08-22 (issue #15)

`cmd/alias-discovery` scans every corpus account (aggregated n-aware probe of
all its games) 1:N against the 68-enrollment catalog, classifies each account
by its relationship to the pro registry, and applies co-occurrence disproof
to every candidate pair. Run over cwal-harvest (229 accounts, min 3 games):

| class | count | outcome |
|---|---|---|
| enrolled (leakage sanity) | 68 | all 68 match themselves, z 6.8–18.9 |
| known-other (pro not catalogued) | 7 | **0 false alarms** clearing fpr_1e3 — clean open-set rejection |
| known-alias (2nd account of a catalogued pro) | 2 | both "wrong" — and both are registry errors, see below |
| unlabeled | 152 | 7 clear fpr_1e3 → discovery candidates |

## The two "failed" known-alias probes are registry errors, not misses

- **Byul (786567149)** fingerprints as **shinee** (z=4.76, clears 1e-3).
  The research spike independently flagged the registry's "Byul" entries as
  covering multiple humans, one fingerprinting as Shinee — reproduced here
  from scratch. Hygiene had already refused merging Byul's two accounts
  (cross-sim 0.610).
- **Sai (21027124)** matches nothing confidently (top z=2.11). Hygiene had
  refused merging Sai's accounts at cross-sim 0.015 — the registry's "Sai"
  is two different humans, and the catalog holds the other one.

## Discovery candidates (unlabeled accounts clearing fpr_1e3)

None are disproved by co-occurrence. battleTag/rank from identities.jsonl:

| aurora | matches | z | 2nd z | corroboration |
|---|---|---|---|---|
| 1506882456 | timeisgold | 6.70 | 1.81 | barcode handle, clean margin — strong |
| 1424114028 | jaedong | 5.84 | 2.75 | barcode-ish, ladder rank 4 — strong |
| 702617558 | midas | 5.35 | 3.97 | named handle, rank 398 — review |
| 17456700 | sharp | 5.19 | 2.63 | barcode + named handle, rank 295 — review |
| 717050343 | bliss | 5.16 | 5.13 | ambiguous margin — weak |
| 823491599 | ample | 5.06 | 2.29 | barcode handles, rank 36 — strong |
| 707085483 | nada | 4.72 | 3.03 | named handles, rank 264 — review |

Per-comparison expectation at fpr_1e3 across 68 comparisons × 152 accounts is
~10 chance clears, so the *count* alone is not evidence — the z magnitudes
and margins are. Candidates above z≈5 with a 2+ margin over the runner-up are
far outside the impostor tail; the ambiguous-margin row is likely chance.

Suggest-only per the integration contract: these are leads for manual triage,
never auto-merged.
