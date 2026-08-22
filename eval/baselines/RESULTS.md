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
