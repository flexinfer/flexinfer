# Implementation Plan: Hardware-Utilization Arc (retrieval + throughput)

**Date**: 2026-06-03
**Arc chosen**: Utilization/throughput (operator pick, 2026-06-03) — F1 + F3 with
a thin F4 instrument slice first, folding in the near-free F2 SD-replication win.
**Brainstorm lineage**: `.loom/brainstorm-hardware-utilization-sprints-2026-06-03.md`
**Predecessor**: `.loom/brainstorm-rocm-fleet-unlocks-2026-05-25.md` (F2 embedding
appliance never built — this arc finishes it).

## Goal

Extract measurably more from the silicon we already own, in priority order of
leverage:
1. **Make utilization visible** (thin F4 instrument) so every later change is
   evidence-backed.
2. **Stand up the gfx906 HBM2 retrieval plane** (F1) — the clearest wasted
   silicon; broadest downstream leverage.
3. **Turn idle GPU-hours into work** (F3) riding on the retrieval plane.
4. **Replicate the proven 2× n-gram SD** to the rest of the textgen fleet (F2
   slice) — near-free throughput.

## Grounding facts (verified 2026-06-03)

- `deploy/models/bge-large-radeonvii.yaml` already defines bge-large-en-v1.5 on
  gfx906 via **llamacpp** (proven gfx906 substrate), `embedding: true`,
  `nGPULayers: 999` (full GPU offload), `batchSize: 2048`. **But**:
  `minReplicas: 0` (cold-start, not warm despite the comment), no reranker
  sibling, and the `embeddings` proxy alias still points at nomic.
- `deploy/models/embeddings.yaml` = `nomic-embed-text` on **GTX 980 Ti (ollama,
  nvidia)**, `minReplicas: 1` warm. This is the real current baseline — *not*
  CPU as the 2026-05-25 brainstorm assumed. Correction folded into the kill-test.
- In-process n-gram SD pays p50 1.95× / p95 2.17× on `gemma4-26b-a4b-gptq` (MR
  !512), conditional on graph-capture being enabled. Twin (`-5930k`) and qwen35
  not yet replicated.
- Both 26B lanes run `maxNumSeqs: 1` (single-stream). 42% timeouts at
  parallelism 10 (upstream queue saturation, not routing).
- GitLab issue map: #28 (GPUGroup metrics + Grafana) → Sprint 0/F4; #27 (automate
  benchmark gen in CI) + #34 (Postgres benchmark storage e2e) → Sprint 3 eval
  gauntlet; #26 (model pre-loading during deployment) → Sprint 2 warm policy.

---

## Sprint 0 (thin) — Fleet utilization instrument [F4 slice]

**Why first**: We're about to claim "10–40× embedding throughput" and "higher
fleet utilization." Those claims need a meter. Closes the long-open #28 as a
side effect. Small, mostly-existing surface.

**Slices**
- **S0.1** — Per-card utilization + KV-pressure metrics. Confirm/extend the
  scheduler + controller exports so each GPU (`cblevins-7900xtx`,
  `cblevins-5930k`, `cblevins-radeonvii`, `cblevins-gtx980ti`) reports
  utilization %, VRAM used/total, and idle-time. Reuse the existing
  `flexinfer_kvcache_pressure_*` family (MR !553) and scheduler scoring metrics.
- **S0.2** — Grafana "Fleet Utilization" dashboard: per-card util, tokens/sec,
  idle-hours/day, warm-vs-cold time. GitOps via `platform/gitops` monitoring
  overlay.
- **S0.3** — Capture a **baseline snapshot** (24 h) into `60-validation-matrix.md`:
  current per-card idle %, embeddings emb/s (nomic on 980 Ti), 26B tok/s. This
  is the before-picture every later sprint measures against.

**Acceptance**: dashboard live; baseline row in the validation matrix; #28 closed
with backlink.

---

## Sprint 1 — gfx906 HBM2 retrieval plane [F1] — KILL-TEST GATED

> **BLOCKED until the kill-test below passes** (spec-riskiest-assumption rule).
> The kill-test *is* slice S1.1.

**Riskiest assumption**: bge-large on gfx906 via llamacpp runs **GPU-resident**
and delivers materially higher batched-embedding throughput than the nomic/980 Ti
baseline — i.e. HBM2 bandwidth is reachable, not blocked by Vega20 fragility.

**Slices**
- **S1.1 (KILL-TEST) — DONE 2026-06-03, feasibility leg PASS.** Ran
  `llama-embedding` for bge-large + nomic GGUFs directly in
  `flexinfer-runtime-gfx906-p4kng`. Both GPU-resident (25/25 & 13/13 layers,
  peak GPU 100%, no segfault), bge-large 8,952 tok/s / nomic 23,719 tok/s
  compute. Matrix row "2026-06-03 S1.1 kill-test". The Vega20 fragility risk is
  retired → **the rest of Sprint 1 is unblocked.** The ≥5×-vs-incumbent
  *throughput* confirmation rolls into S0.3 (nomic@980Ti baseline couldn't be
  activated — 980 Ti shared-group queue). Net: feasibility proven; quantitative
  ratio is a measurement, not a risk.
- **S1.2 (deploy) — DONE 2026-06-03, MR !555.** Added `bge-large-radeonvii` to
  the deployed kustomization as an additive, reversible lane (priority 100 <
  live tool router 120; minReplicas 0; serviceLabel `embeddings-hbm2`). Flux
  applied; cache prefetched; ConfigValid + Schedulable. **But live-activation
  revealed a blocker (see S1.2b): bge is Idle/Queued, can't get the GPU slot.**
- **S1.2b (blocker) — CPU model on the single-slot GPU runtime. CODE FIX MERGED
  2026-06-03 (MR !556, #62).** Corrected root cause: NOT VRAM budget — the gfx906
  unified runtime serves ONE backend subprocess at a time
  (`internal/runtime/manager.go:70`) and `chooseSharedGroupLeader` elects one
  leader per group; the CPU-pinned tool router held that single slot. Operator
  picked option (A) "dedicated pod for tool router". Fix: `DirectRuntimeLoadEligibility`
  excludes explicit `gpu.vendor: cpu` models → they get a dedicated Deployment
  (CPU pod) instead of the GPU runtime; tool router reclassified to `vendor: cpu`
  (schema-forces it out of the GPU group). Unit tests added; CI green; auto-merged.
  - **DONE + LIVE VERIFIED 2026-06-03.** Controller rebuilt (Dockerfile.manager,
    digest sha256:5932e77d) + rolled out (scale 0→1). Four follow-up fixes landed
    via live verification: !557 bge priority 100→120 (out-ranked by gemma4-e4b
    110); !558 pin tool-router image (vendor:cpu default 404→ErrImagePull); !559
    bge cache SharedPVC→Local (runtime sees node-local /models only). Result:
    tool router = own CPU Deployment 1/1 Ready (weaver dep up); bge = Active
    leader, runtime-served GPU-resident; `POST /v1/embeddings` via proxy → **HTTP
    200, 1024-dim** vector; pyannote undisturbed. A CPU + GPU model now serve the
    same node concurrently. Matrix: "S1.2b LIVE VERIFIED".
  - Follow-up (latent, non-blocking): fix `backend/llamacpp.go`
    `DEFAULT_LLAMA_CPP_IMAGE_CPU` (ggerganov→ggml-org) so future vendor:cpu
    llamacpp models don't need an explicit image.
- **S1.2c — DONE + LIVE VERIFIED 2026-06-03 (MR !560).** bge `minReplicas:1` (warm),
  took `embeddings`+`text-embedding-3-small` aliases + `embeddings`/`semantic-search`/
  `rag` serviceLabels; nomic demoted to cold fallback (distinct names). Live: default
  `embeddings` + `text-embedding-3-small` aliases → HTTP 200 served by bge@gfx906
  (1024-dim). **The gfx906 HBM2 retrieval plane is the live default embeddings lane.**
  S0.3 verdict: incumbent nomic@980Ti was stuck Queued for weeks (same #62 pathology
  on gtx980ti-models) → cutover is a strict availability win; bge served ~91 emb/s
  batched. Matrix: "S0.3 baseline", "S1.2c LIVE VERIFIED".
- **S1.3** — Add the **reranker**: `bge-reranker-large` GGUF on the same card via
  llamacpp `--reranking`, expose proxy `/v1/rerank`. New `rerank` service label +
  alias. (Reranker was the predecessor brainstorm's "free win" — never built.)
- **S1.4** — Wire one real consumer end-to-end as proof: point
  `codebase-memory` (or agent-context recall) at the bge `/v1/embeddings` lane,
  re-embed one repo, confirm search quality parity + throughput gain. Matrix row.

**Acceptance**: kill-test PASS row; bge warm + default; `/v1/rerank` live; one
consumer migrated with before/after emb/s in the matrix.

---

## Sprint 2 — Idle-time batch appliance [F3] (rides on Sprint 1)

**Why after F1**: embedding is the heaviest cost of the batch loop; with the
HBM2 plane live, nightly re-embed/index is 10–40× cheaper.

**Slices**
- **S2.1** — Minimal job-queue + nightly trigger (reuse existing controller Job
  machinery + `scheduled-tasks`; do **not** reinvent Argo). One CRD or ConfigMap
  job-spec, bounded by admission filter + max_tokens clamp.
- **S2.2** — First job: nightly codebase re-embed/index for the repos in
  `codebase-memory`, writing to the existing Qdrant collection. Uses the Sprint 1
  bge lane.
- **S2.3** — Second job (demand-driven): offline model-eval gauntlet for any new
  model artifact (ties to #27 automate-benchmark-gen + #34 Postgres benchmark
  storage). Produces a coherence/throughput row automatically.
- **S2.4** — Model pre-loading / prefix-cache prewarm during the idle window
  (ties to #26): precompute likely first-prompt prefixes so the morning's first
  request is warm.

**Acceptance**: nightly re-embed runs unattended + logged; eval gauntlet emits a
matrix/Postgres row for a test artifact; #26/#27/#34 advanced with backlinks.

---

## Sprint 3 — Fleet throughput replication [F2 slice] (near-free)

**Why last but cheap**: pure config/replication; bundled here so the arc ends
with the whole textgen fleet at the throughput the primary already proved. Can be
pulled forward if concurrent demand materializes (the runner-up trigger).

**Slices**
- **S3.1** — Replicate in-process n-gram SD (`speculativeConfig` passthrough,
  already wired at `backend/vllm.go:212-216`) to `gemma4-26b-a4b-gptq-5930k` and
  qwen35 (once it serves coherently — see #51/#52). Verify graph-capture is on
  (the win is conditional on it).
- **S3.2** — Tune `num_speculative_tokens` 5→7 / `prompt_lookup_max` 4→6 on the
  primary (per-position acceptance still 68% at position 5 → headroom). Guard:
  n-gram SD is **workload-conditional** (long-form gen regresses; see
  `ngram-sd-workload-conditional.md`) — measure on the real workload shape, gate
  per-route.
- **S3.3** — Controlled `maxNumSeqs` raise on the 26B lanes with the Sprint 0
  dashboard watching KV pressure; find the batch-depth knee at current context
  before latency regresses. Matrix row with the throughput/latency tradeoff curve.

**Acceptance**: SD replicated to twin + qwen35 with per-lane verdict; tuned params
or documented null result; `maxNumSeqs` knee captured with the tradeoff curve.

---

## Sequencing & dependencies

```
Sprint 0 (instrument) ──► Sprint 1 (F1, kill-test gated) ──► Sprint 2 (F3)
        │                          │
        └──────────────────────────┴──► Sprint 3 (F2, parallelizable, pull-forward if demand)
```

- Sprint 0 is a hard prerequisite for trustworthy claims in 1/2/3.
- Sprint 1 S1.1 kill-test gates the rest of Sprint 1 and all of Sprint 2.
- Sprint 3 is independent of 1/2 and can be pulled forward if concurrent load
  appears (the runner-up trigger from the brainstorm).

## Out of scope (explicitly deferred)
- F5 heterogeneous 70B TP (capability-ceiling arc, not this one).
- F6 unified multimodal endpoint (breadth arc).
- F8 daily-driver product packaging (product-polish arc).
- F4-prefix-cache-flip canary promotion (row 193 conditional) — separate F4
  thread, tracked in `00-index.md`.

## Handoff
- Per-sprint execution → `feature-dev` / `roadmap-spec-ralph-loop`, one slice per MR.
- Sprint 1 S1.1 → run as a standalone kill-test before opening the sprint.
- Evidence target for every slice → `.loom/60-validation-matrix.md`.
