# Regression gates

Gate files for `internal/cmd/eval -gates`. A gate maps scenario names to
metric bounds and makes the command exit non-zero when a metric degrades
past them:

```json
{"n1_all": {"eer_max": 0.0021, "tpr_at_fpr_1e3_min": 0.997}}
```

`cwal_harvest_gates.json` is the only gate kept in-repo, because it is the
only one runnable from a clean checkout — it gates the committed `corpus/`:

```
go run ./internal/cmd/fetch-corpus
go run ./internal/cmd/extract-corpus -metadata corpus/replays.jsonl -replays-dir corpus -out /tmp/features.csv
go run ./internal/cmd/eval -csv /tmp/features.csv -gates internal/eval/baselines/cwal_harvest_gates.json
```

Gates for corpora that are not in the repo, and the write-ups behind these
numbers, live in the research folder outside this repository.

`TPR@1e-4` is intentionally not gated: at current impostor-pool sizes those
estimates rest on a handful of tail events.

CI runs the synthetic-corpus gates (`go test ./internal/eval/ -run
TestRegressionGates`) on every PR, which catch pipeline breakage without
needing any corpus.
