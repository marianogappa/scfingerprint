# Methodology and honest limitations

scfingerprint identifies StarCraft: Brood War players by **how** they play,
not what they're named. This document explains how it works, what the numbers
mean, what it has demonstrably done, and — just as importantly — what it
cannot do. The audience is a skeptical community; if anything here reads as
an over-claim, [file an issue](../../../issues).

## How it works

### What a fingerprint measures

Every command a player issues in a replay is timestamped. From one player's
command stream, we extract a **360-dimensional feature vector** of behavioral
rates and timings — never map, position, or strategy content:

- **Hotkey habits**: which control groups (0–9) the player assigns and
  selects, how often, the ratio of selects to assigns, double-tap rates, and
  which groups they bind in their first five assignments of a game (early-game
  muscle memory).
- **Command loops**: a 10×10 matrix of "what command class follows what" —
  e.g. some players cycle select → hotkey → right-click in a tight loop,
  others interleave production checks. Also transitions between hotkey groups
  (does 1 → 2 → 3 or 1 → 1 → 2 dominate?).
- **Rhythm**: the distribution of gaps between consecutive commands — median,
  percentiles, a fine histogram, the modal gap, burst-run lengths, and gap
  medians conditioned on which command pair is being executed.
- **Micro-timings**: how long after assigning a hotkey the player first
  selects it; the gap inside a double-tap; selection sizes; how far apart
  consecutive positioned commands land.
- **Tempo**: APM, effective APM, redundancy, APM by game phase.

Example: two Zerg players can build the same units on the same map with the
same build order and still differ completely here — one binds hatcheries on
4/5/6 and army on 1/2 with a 190ms double-tap gap; the other binds army on
1/2/3, checks production by clicking, and has a distinctive 3-command burst
cadence. Those habits are muscle memory: they survive name changes, account
switches, and usually even race switches.

### How two players are compared

1. **Standardize** each feature using means/stds from a training corpus.
2. **Select** the 150 most identity-discriminative features (F-ratio:
   between-player variance over within-player variance).
3. **Whiten** with the within-player covariance (shrinkage-regularized), so
   that correlated habits don't double-count and every direction of remaining
   variation is equally informative.
4. **Cosine similarity** between the whitened vectors. A player's
   *fingerprint* is the mean of their whitened per-game vectors.
5. **Cohort normalization**: the raw cosine is z-normalized against a cohort
   of reference fingerprints (z-norm) and against the probe's own scores vs
   that cohort (t-norm), then averaged. This is what makes low
   false-positive-rate operating points usable — raw cosine alone loses ~40
   points of true-positive rate at FPR 10⁻³.
6. **Evidence-count calibration**: a probe averaged from n games has smaller
   variance than a single game, so scores are calibrated per evidence bucket
   (1, 2, 3, 5, 8+ games). Without this, one 84-game aggregate once out-ranked
   everything for the wrong reason.

## What the numbers mean

A result is always a **calibrated z-score plus a games-of-evidence count plus
named operating points** — never a bare yes/no.

- **Operating points** are false-positive-rate budgets measured on impostor
  pools: `fpr_1e2` (≈1-in-100), `fpr_1e3` (≈1-in-1,000), `fpr_1e4`
  (≈1-in-10,000). "Clears fpr_1e3" means: at this threshold, fewer than 1 in
  1,000 wrong-person comparisons would score this high.
- **Single game = strong lead, not confirmation.** Validated single-game
  performance on the pro 1v1 corpus: EER 0.21%, TPR 0.997 at FPR 10⁻³.
  Excellent — but a lead.
- **3+ games = high confidence.** At 3 games of evidence, same-race
  impostors: EER 0.05%, TPR 1.000 at FPR 10⁻³ in the validated spike.
- **Use-case guidance**:
  - *Alias identification* ("who is this smurf?") is a **1:N search** — every
    additional catalog identity is another chance at a false positive.
    Demand multi-game evidence and treat single-game hits as leads to gather
    more games for.
  - *Tournament ghosting / account verification* ("is this player X?") is
    **1:1 verification** — one hypothesis, much easier. Single-game evidence
    is often adequate for suspicion; a handful of games settles it.

## Validated results

From the research spike (~2,900 1v1 ladder replays across 23 identities, plus
~1,000 casual team-game replays):

- **Found real smurf pairs in the wild**: `MBU_Shine ≡ wG_Shine` and
  `58BoJi4485 ≡ IllIllIlllIIIII` surfaced as cross-identity matches and were
  confirmed.
- **Cross-corpus re-identification**: a pro (`HM_sSak`) enrolled from 1v1
  ladder games was identified playing casual team games in an unrelated
  replay collection at z = +7.6, where the null distribution's 99th
  percentile is +4.1.
- The catalog's worst failure was also instructive: one wrongly-merged
  two-person enrollment (self-consistency 0.44 vs the genuine ~0.96) produced
  an entire corpus's false-positive tail. That is why every enrollment now
  passes a self-consistency gate and the dataset ships with hygiene tooling.

## Honest limitations

- **Enrollment needs games.** Fingerprints stabilize around ~30+ enrollment
  games. Below that, expect noisier scores; the format tracks its own game
  count so you can judge.
- **Short games underperform.** Games under ~5 minutes carry less signal;
  sub-4-minute games are measurably worse (a rush that ends at 3:30 barely
  exercises anyone's habits).
- **Cross-era drift is untested.** We have not yet measured whether a 2020
  fingerprint still matches the same person in 2026. Temporal stability is
  tracked as future work.
- **Domain shift is real.** A model fit on 1v1 ladder degraded on 8-player
  team games (EER 1.19% locally-fit vs 6.4% cross-fit). Mixed-domain training
  and local refitting mitigate; treat cross-domain z-scores with extra
  skepticism.
- **FPR 10⁻⁴ is not certified.** Our impostor pools are large enough to
  measure 1-in-1,000 confidently; the 1-in-10,000 numbers rest on a handful
  of tail events and are reported as estimates only.
- **Deliberate mimicry is unstudied.** All results are against players being
  themselves. Someone consciously retraining their hotkey layout and rhythm
  to imitate another player — or to shed their own profile — has not been
  tested and should be assumed possible.
- **The catalog is the weakest link.** Most false positives in practice come
  from contaminated enrollments (two people merged under one identity), not
  from the matcher. Hygiene gates reduce this; they do not make it zero.

## Ethics and public claims

A match is a **probabilistic lead, never proof**. Before making any public
claim about a person, remember:

- Behavioral similarity has base rates. In a 1:N search over a large catalog,
  *someone* will look similar eventually; that is what operating points
  quantify and why evidence counts matter.
- Practice partners, siblings, and students of the same coach can share
  habits. Co-occurrence (two names in the same game) disproves identity;
  nothing in this tool *proves* it.
- Recommended language for public claims:
  - ✅ "Fingerprint analysis of N games matches X at the 1-in-1,000 operating
    point; treating as the same player pending confirmation."
  - ❌ "The tool proved X is Y."
- Accusations of ghosting/smurfing affect real people's reputations. Use
  multi-game evidence, check the alias against co-occurrence, prefer 1:1
  verification framing, and give the accused the numbers you'd want shown if
  it were you.

## Reproducibility

Everything above is re-derivable: replays are the source of truth, feature
extraction is versioned and append-only, the training pipeline is
deterministic (same inputs → byte-identical model artifact), and the
evaluation harness (`eval/`, with CI regression gates) recomputes EER/TPR
tables from any labeled corpus. See the [README](../README.md) for the
package map.
