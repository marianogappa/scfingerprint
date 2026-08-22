# Reference regression gates

Gate files for `cmd/eval -gates`, encoding the validated reference numbers
(v3 features, top-150 selection, z+t-norm) with tolerance — EER may not
degrade more than 25% relative, TPR@1e-3 not more than 0.005 absolute:

| corpus | scenario | reference EER | reference TPR@1e-3 |
|---|---|---|---|
| pro 1v1 (23 ids) | n1_all | 0.21% | 0.997 |
| pro 1v1 (23 ids) | n3_same_race | 0.05% | 1.000 |
| amateur team-games (8 ids) | n1_all | 1.19% | 0.957 |
| cwal-harvest (229 ids) | n1_all | 1.22% | 0.917 |
| cwal-harvest (229 ids) | n3_all | 0.66% | 0.984 |

The labeled replay corpus is now committed under [`corpus/`](../../corpus/)
(Git LFS); run `git lfs pull` to fetch the replay files. Extract features
and run the gates:

```
go run ./cmd/extract-corpus -metadata corpus/replays.jsonl -replays-dir corpus -out /tmp/features.csv
go run ./cmd/eval -csv /tmp/features.csv -gates eval/baselines/cwal_harvest_gates.json
```

TPR@1e-4 is intentionally not gated: at current impostor-pool sizes those
estimates rest on a handful of tail events. Certifying that regime needs
more corpora (tracked in dataset growth, #8).

CI runs the synthetic-corpus regression gates (`go test ./eval/ -run
TestRegressionGates`) on every PR — those catch pipeline breakage without
needing the private corpora.
