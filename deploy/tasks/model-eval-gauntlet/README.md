# model-eval-gauntlet — offline benchmark automation (Sprint 2 / S2.3)

Weekly CronJob that runs `flexinfer-bench --gauntlet` against a configurable set
of warm text models. Each successful model records a throughput row to
**Postgres** (the `benchmarks` table in `flexinfer_benchmarks`), writes a
per-model results ConfigMap, and emits a structured PASS/FAIL verdict that
includes a small coherence probe.

This is the automation leg of **#27** (keep benchmark/scheduling inputs current),
riding on the **#34** Postgres storage backend, which was validated end-to-end
live on 2026-06-04 (see `.loom/60-validation-matrix.md` → "2026-06-04 S2.3").

## How it works

1. The `flexinfer-benchmarker` ServiceAccount (nodes `get` + configmaps RBAC)
   runs the published `flexinfer-bench:master` image.
2. A shell wrapper loops `MODELS` (space-separated `name=backend` entries) and runs
   the bench binary once per model, routing through `flexinfer-proxy` (models
   cold-start on demand).
3. Each gauntlet run stores the throughput artifact to Postgres
   (`POSTGRES_DSN`) and to `flexinfer-benchmarks-<model>` ConfigMap, then probes
   `/v1/completions` once for TTFT/token/coherence checks.
4. One cold/missing/coherence-failing model is logged (`GAUNTLET FAIL <name>`) but
   does not abort the gauntlet; the job exits non-zero only if **every** model
   failed.

## Configuration (env on the container)

| Env | Default | Purpose |
|-----|---------|---------|
| `MODELS` | `gemma4-26b-a4b-gptq=vllm gemma4-26b-a4b-gptq-5930k=vllm` | `name=backend` list |
| `ITERS` / `MIN_DURATION` / `BATCH_SIZE` | `3` / `30s` / `64` | bench knobs |
| `COLD_START_TIMEOUT` | `5m` | per-model activation wait |
| `GAUNTLET_ENABLED` | `1` | set `0` to run throughput-only compatibility mode |
| `GAUNTLET_MIN_TPS` / `GAUNTLET_MAX_TTFT` / `GAUNTLET_MIN_TOKENS` | `0` / `0s` / `1` | optional pass/fail gates |
| `GAUNTLET_PROMPT` / `GAUNTLET_EXPECT` / `GAUNTLET_EXPECT_MODE` | `What is 2 + 2?...` / `4` / `all` | coherence probe contract |
| `POSTGRES_DSN` | langgraph `flexinfer_benchmarks` | mirrors `values-k3s.yaml` |
| `FLEXINFER_PROXY_URL` | `http://flexinfer-proxy…:80` | proxy base |

Add models as artifacts land by editing `MODELS` (or a Flux patch).

## Prerequisites (#34)

The `flexinfer_benchmarks` database must exist on the Postgres host
(`langgraph-postgres-postgresql.ai.svc:5432`). The bench store auto-creates the
`benchmarks` **table** but not the **database**:

```bash
PGPASSWORD=changeme-app psql -h langgraph-postgres-postgresql.ai.svc -U langgraph \
  -d postgres -c "CREATE DATABASE flexinfer_benchmarks;"
```

(Already created in-cluster during the S2.3 validation.)

## Operations

```bash
# Manual one-shot run from the scheduled CronJob template:
kubectl -n flexinfer-system create job --from=cronjob/model-eval-gauntlet gauntlet-adhoc
kubectl -n flexinfer-system logs -f job/gauntlet-adhoc

# Inspect stored rows:
PGPASSWORD=changeme-app psql -h langgraph-postgres-postgresql.ai.svc -U langgraph \
  -d flexinfer_benchmarks -c \
  "SELECT model, backend, tokens_per_second, timestamp FROM benchmarks ORDER BY timestamp DESC LIMIT 10;"

# Pause:
kubectl -n flexinfer-system patch cronjob/model-eval-gauntlet -p '{"spec":{"suspend":true}}'
```

## CI/CD trigger

The `model_eval_gauntlet_trigger` GitLab job runs after the `publish` job on
`master` and creates a one-shot Kubernetes Job from this CronJob template. That
gives every newly published `flexinfer-bench:master` artifact an immediate
throughput + coherence pass while keeping the weekly CronJob as a drift detector.
The CI job skips only when the runner lacks `/etc/kubeconfig/k3s.yaml`; otherwise
it waits for the one-shot Job and fails the pipeline if every model fails.

## Retrieval-quality companion (F3 Slice 5)

This gauntlet measures **throughput plus model coherence**. The
**retrieval-quality** dimension — does
the retrieve→rerank→generate read path still answer repo-Q&A correctly after an
index / chunker / answer-model change — lives in
[`eval/f3-retrieval/`](../../../eval/f3-retrieval/). The `rqgate.py` kernel turns
the F3 kill-test's per-question rows into a two-axis gate (`ev_ratio` recall +
`judge_ratio` synthesis) and `f3eval.py` emits an `RQ_RESULT_JSON` score row when
`RQ_GATE=1` — the retrieval-quality sibling of the throughput row above.

Run it in-cluster with `eval/f3-retrieval/job.rq.example.yaml` (retrieval-only, no
NFS mount). Promoting it to a scheduled Flux CronJob alongside this one is a
documented fast-follow, gated on validating the thresholds against a first live run.

## Known limitation

`device_class` currently reflects the **bench runner pod's** node (often a CPU
node), not the model's serving GPU node — it reads `NODE_NAME`/node labels of the
pod, not the model endpoint's node. Tracked as a benchmarker follow-up; storage
and throughput numbers are correct.

## Reversibility

Additive: a CronJob writing to a dedicated DB + ConfigMaps. Remove by deleting this
directory from `deploy/kustomization.yaml` (Flux prunes it).
