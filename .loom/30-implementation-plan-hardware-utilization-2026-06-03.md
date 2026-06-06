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
  - **DONE 2026-06-03, MR !567 (squash b455d1f4).** Found the
    compute-utilization leg was collected-but-dead: the `flexinfer-agent`
    DaemonSet already populates `GPUMetrics.Utilization` (nvidia-smi
    `utilization.gpu` + rocm-smi `--showuse`) but only VRAM util was exported.
    Surfaced the per-card **compute (core busy) utilization** as a new gauge
    `flexinfer_gpu_compute_utilization_percent{gpu,node,vendor}` — near-zero is
    the fleet idle-time signal S0.2/S0.3 derive from. VRAM used/total/free/util%
    + temperature already existed (`flexinfer_gpu_vram_*`). Additive, all code CI
    green (the one red was a GitLab-502 git-clone flake in `proxy_test`, retried).
    Remaining S0.1 nicety: a dedicated KV-pressure-per-card label is still on the
    `flexinfer_kvcache_pressure_*` family from !553 (already shipped).
- **S0.2** — Grafana "Fleet Utilization" dashboard: per-card util, tokens/sec,
  idle-hours/day, warm-vs-cold time. GitOps via `platform/gitops` monitoring
  overlay.
  - **DONE + LIVE 2026-06-03, platform/gitops MR !219 (merge 93fb826f).** New
    dashboard `services-flexinfer-fleet-utilization` (folder Services) in
    `k3s/monitoring/dashboards/`. Kill-test first: confirmed live that the
    `flexinfer_gpu_*` agent family + `flexinfer_runtime_model_state` are scraped
    into the monitoring Prometheus (3 GPUs report). 10 panels — per-card compute
    util (`flexinfer_gpu_compute_utilization_percent`, S0.1) + VRAM util, VRAM
    bytes, temp, idle-cards + idle-hours/24h, tokens/sec, warm/cold states. All
    PromQL `max by (node,gpu,vendor)`-aggregated (collapses DaemonSet pod churn to
    one clean line/card). Flux-reconciled; ConfigMap verified in `monitoring` ns.
    VRAM/temp/warm-cold panels LIVE now; compute-util + idle panels populate once
    the flexinfer-agent image carrying S0.1 (MR !567) is rebuilt + rolled (ops
    follow-up). MR !227 (stale chart runtime-dashboard dedupe) untouched.
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
  - **Kill-test 2026-06-03 PASS**: `bge-reranker-v2-m3` GGUF (the llama.cpp-validated
    reranker, substituted for `bge-reranker-large`) GPU-resident on gfx906 via
    `llama-server --reranking`; `/v1/rerank` returns correct cohere-style scores.
  - **PIVOT 2026-06-03**: the gfx906 unified runtime is **single-slot**
    (`internal/runtime/manager.go` — one subprocess at a time), so bge embeddings
    (warm) and a GPU reranker can't co-reside; they'd swap-thrash. Operator chose
    "**multi-subprocess runtime first**". Second kill-test PASS: bge + reranker
    served **concurrently** on gfx906, 1591MB/16368MB VRAM (14.7GB free) — the
    blocker is software orchestration, not hardware. S1.3 re-scoped into R1–R5
    (`60-validation-matrix.md` "Multi-subprocess runtime — slice decomposition"):
    - **R1** kill-test — DONE PASS.
    - **S1.3 backend leg**: `--reranking` flag in `backend/llamacpp.go` — SHIPPED
      MR !562 (additive, inert until a reranker CR uses it).
    - **R2** Manager multi-subprocess core (flag-gated, default off) — SHIPPED MR
      !564 (`active` map, per-model ports, VRAM admission, multi-model status/metrics).
    - **R3** proxy per-model port routing from runtime status — TODO.
    - **R4** controller multi-Active leader election (VRAM-bounded set) — TODO.
    - **R5** reranker Model CR + flip `FLEXINFER_RUNTIME_MULTI_MODEL` on for
      radeonvii + rebuild/roll runtime image + live-verify `/v1/rerank` concurrent
      with `/v1/embeddings` — TODO (the original S1.3 payoff).
- **S1.4** — Wire one real consumer end-to-end as proof: point
  `codebase-memory` (or agent-context recall) at the bge `/v1/embeddings` lane,
  re-embed one repo, confirm search quality parity + throughput gain. Matrix row.
  - **S1.4a (gateway route) — DONE + LIVE 2026-06-04, platform/gitops MR !220
    (merge c731f5c6).** Kill-test surfaced the real blocker: the litellm gateway
    (the only externally reachable entry — `flexinfer-proxy` is ClusterIP-only) had
    NO working bge route. Its `bge-large-embeddings` entry pointed at
    `bge-large-embeddings.flexinfer-system.svc:80`, a Service that does not exist,
    so `bge-large`/`text-embedding-bge-large` 404'd; consumers asking for
    `embeddings` got morph-embedding-v4 (paid cloud, 1536-dim). Re-pointed the route
    at `flexinfer-proxy.../v1` with `remoteModel: bge-large-radeonvii` (verified live
    from the litellm pod: that model name → HTTP 200, 1024-dim, bge@gfx906). Fixed in
    both `external-models.yaml` ConfigMap + the inline `STATIC_LOCAL_EMBEDDINGS`
    fallback. Flux-reconciled (`apps` ks); **gateway live-verified**: `bge-large` via
    `litellm.flexinfer.ai/v1/embeddings` → 1024-dim, served by
    `CompendiumLabs/bge-large-en-v1.5-gguf`. Additive/reversible (morph stays the
    default `embeddings` alias). Matrix: "2026-06-04 S1.4a".
  - **S1.4b (consumer migration) — DONE (proof-only) 2026-06-04. VERDICT: do NOT
    migrate the local codebase-memory.** Operator chose side-collection proof (no
    global flip). Measured emb/s + parity for the consumer's real path (local stdio
    → public litellm gateway). Result: **bge via gateway = 3.3 emb/s vs morph cloud =
    15.4 emb/s — a ~4.7× regression** for this consumer. Attribution: bge *serving* is
    fast (70.9 emb/s in-cluster via proxy, ≈ S0.3 baseline); the loss is WAN +
    Cloudflare per-batch round-trip, not GPU. Parity: morph nailed a paraphrase probe
    (`setJobFailed` top-1); bge ranked a sibling top-1 (suggestive, one probe). The
    proof **falsified** "migrate codebase-memory → utilization win." Morph left as the
    default (untouched). Matrix: "2026-06-04 S1.4b". **Redirect**: the real consumer to
    migrate is an **in-cluster batch** job hitting flexinfer-proxy directly (70.9
    emb/s, self-hosted) → see Sprint 2 **S2.2**.

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
  - **DONE 2026-06-04 (folded into S2.2).** Operator chose the standalone
    ConfigMap-script form, so the "nightly trigger" is a plain Flux-managed
    `CronJob` (`deploy/tasks/codebase-reembed/`, 04:00 ET, `concurrencyPolicy:
    Forbid`) rather than a new CRD/controller machinery — simplest thing that
    works, no Argo. Bounded by `MAX_FILES`/`MAX_CHUNKS`/`MAX_FILE_BYTES` clamps
    (the "admission filter + max_tokens" equivalent), and a per-input 512-token
    char cap forced by the bge model.
- **S2.2** — First job: nightly codebase re-embed/index for the repos in
  `codebase-memory`, writing to the existing Qdrant collection. Uses the Sprint 1
  bge lane. **NOTE (from S1.4b)**: run this as an **in-cluster k8s Job hitting
  `flexinfer-proxy` directly** (70.9 emb/s, self-hosted) — NOT via the public
  Cloudflare gateway (3.3 emb/s, WAN-bound). This is the correct home for the
  "wire a real bge consumer" payoff: a batch job is latency-insensitive and stays
  in-cluster, so the HBM2 throughput actually lands. New 1024-dim collection
  (`codebase_memory_bge_v1`); morph collection preserved.
  - **DONE + LIVE-VERIFIED 2026-06-04** (`deploy/tasks/codebase-reembed/`).
    Standalone ConfigMap `reembed.py` (pure-stdlib) + nightly CronJob. Source =
    inline read-only NFS mount of the shared devbox workspace (no git/creds;
    flexinfer not on the NFS, so target = `services/loom-core`, the codebase-memory
    consumer's own repo + S1.4b corpus). Live: 500-chunk bounded run → collection
    `codebase_memory_bge_v1` (1024-dim, 500 pts, green) on the **canonical** Qdrant
    `192.168.50.176` (NOT the orphan `daemon/qdrant`), beside morph
    `codebase_memory_v1`; junk-free relevant search. Three bugs caught+fixed in
    Prove: 512-token per-input cap, AppleDouble `._*` dotfile skip, wrong-Qdrant
    target + api-key secret. Throughput 4–10 emb/s wall-clock (cold-start + upsert
    bound; warm bge serving is the 70.9 from S1.4b) — fine for an idle batch.
    Matrix: "2026-06-04 S2.2". **S2.3/S2.4 remain.**
- **S2.3** — Second job (demand-driven): offline model-eval gauntlet for any new
  model artifact (ties to #27 automate-benchmark-gen + #34 Postgres benchmark
  storage). Produces a coherence/throughput row automatically.
  - **DONE + LIVE-VERIFIED 2026-06-04** (`deploy/tasks/model-eval-gauntlet/`).
    **#34** validated e2e: tracker drift (store is `agents/benchmarker/postgres_store.go`,
    not `pkg/benchmarkconfig/...`); the configured DSN's `flexinfer_benchmarks`
    **database did not exist** (store auto-creates the table, not the DB) → created
    it; `flexinfer-bench` → 1 Postgres row (gemma4-26b 113 tps). **#27** automated:
    weekly gauntlet CronJob loops a `MODELS` list → Postgres + per-model ConfigMap;
    live one-shot ok=2 fail=0, 3 rows total (gemma4-26b 113→156 tps, 5930k twin 141).
    Follow-up: `device_class` empty (reads runner node, not model node — benchmarker
    code change); true on-artifact-creation trigger (vs weekly schedule). Matrix:
    "2026-06-04 S2.3". **S2.4 (prefix prewarm #26) remains.**
- **S2.4** — Model pre-loading / prefix-cache prewarm during the idle window
  (ties to #26): precompute likely first-prompt prefixes so the morning's first
  request is warm.
  - **PREMISE PIVOT + SHIPPED 2026-06-04 (flexinfer MR — controller feature).**
    The prefix-prewarm framing was **falsified for the current fleet**: the
    daily-driver gemma4-26b is warm-pinned (no cold-start) with
    `enablePrefixCaching: false` (APC infeasible at 32K/FP8-KV per the F4 canary →
    no prefix cache to seed), and all scale-to-zero text lanes share gfx906 with the
    LIVE bge+reranker plane (prewarm would evict the S1.4 win). Operator chose #26's
    controller pre-loading. Shipped opt-in `config.preloadOnDeploy: true`: warms a
    **non-shared serverless** model (1 replica) from deploy until first request,
    then normal idle scale-to-zero resumes (distinct from minReplicas:1). Pure calc
    in `desiredReplicasForContext`; shared-GPU members excluded (cannot evict a
    leader); gauge `flexinfer_model_preload_active`. Default-off, unit-tested, all
    controllers/metrics suites green. Matrix: "2026-06-04 S2.4". **Sprint 2 COMPLETE
    (S2.1–S2.4).**

**Acceptance**: nightly re-embed runs unattended + logged; eval gauntlet emits a
matrix/Postgres row for a test artifact; #26/#27/#34 advanced with backlinks.

---

## Sprint 3 — Fleet throughput replication [F2 slice] (near-free)

**Why last but cheap**: pure config/replication; bundled here so the arc ends
with the whole textgen fleet at the throughput the primary already proved. Can be
pulled forward if concurrent demand materializes (the runner-up trigger).

> **STATUS RECONCILIATION (2026-06-05)**: grounding found the plan's Sprint 3 is
> largely already-applied, blocked, or live-risky:
> - **S3.1 twin SD** — *already live*: `deploy/models/gemma4-26b-a4b-gptq-5930k.yaml`
>   has `speculativeConfig {method:ngram, num_speculative_tokens:5, prompt_lookup_max:4}`
>   + `enforceEager:false` (graph-capture on), deployed (`kustomization.yaml:64`).
> - **S3.1 qwen35 SD** — *blocked*: qwen35 models intentionally disabled
>   (`deploy/models/kustomization.yaml:6`); not serving coherently (#51/#52).
> - **S3.2 primary tuning** — *already applied*: primary at `{7,6}`
>   (`gemma4-26b-a4b-gptq.yaml:163`).
> - The remaining real moves (twin bump `{5,4}→{7,6}`; `maxNumSeqs` knee) all push
>   **more blanket SD / heavier batching with no traffic-mix evidence and no
>   per-request SD bypass** — the exact mistake `ngram-sd-workload-conditional`
>   warns against. **Operator decision (2026-06-05): measure traffic mix first.**

**Slices**
- **S3.0 (measure traffic mix) — KILL-TEST FAILED → instrument shipped 2026-06-05.**
  Goal: per-lane joint prompt/completion-token distribution to decide blanket SD
  per route. The prescribed source (proxy `event=request_usage` logs) is
  **unreachable** — proxy pinned to the un-scraped `gtx980ti` control-plane node,
  drowned by `v1 Endpoints is deprecated` spam; Prometheus had no token metric.
  Shipped two proxy histograms `flexinfer_proxy_request_{prompt,completion}_tokens{model}`
  (scrape-reliable path; non-streaming only) so the measurement can land once data
  accumulates. Verdict deferred to a follow-up. Matrix: "2026-06-05 S3.0".
  - **S3.0 #3 (streaming usage-chunk capture) — DONE 2026-06-05, MR !579 (merge 2af1c733).**
    Closed the histograms' streaming blind spot: `usageSniffingBody` transparently sniffs the
    terminal SSE `usage` chunk (when the client set `stream_options.include_usage`) and records
    the shape histograms, so the eventual SD verdict is trustworthy regardless of traffic mix.
    Additive/default-safe; full `./internal/proxy/` suite green under `-race`. Matrix:
    "2026-06-05 S3.0 #3". Remaining S3.0 dependency: roll S3.0b `completions_total` live + let
    ≥1 day of real traffic accumulate before the blanket-SD verdict.
  - **S3.0b + #3 LIVE-ROLL VERIFIED — DONE 2026-06-05.** Confirmed the merged coverage counter
    (`c0815345`) and streaming usage capture (`2af1c733`) are live on the rolled proxy pod
    (`:master` → `sha256:3094248f`, started 20:47Z). End-to-end via port-forward: non-streaming
    completion populated `completions_total{stream="false"}` + the token-shape histograms; a
    streaming completion with `stream_options.include_usage` populated `completions_total{stream="true"}`
    **and advanced the histogram `_count` 1→2** (proving the #3 streaming path records shape). All
    three series confirmed centrally scraped in loom Prometheus (`job="flexinfer-proxy"`). The
    **"roll S3.0b live" dependency is CLOSED**; the blanket-SD verdict is now **purely time-gated**
    on ≥1 day of real per-lane traffic. Matrix: "2026-06-05 S3.0b + S3.0 #3 — LIVE-ROLL VERIFIED".
  - **S3.0 first data-clock read — DONE 2026-06-05 (canary effective).** The 2026-06-05
    traffic-source canaries (news-analyzer !7, storyboard !2, jobsearch !93 — all merged)
    moved the lanes from ~0 real completions/day to **primary ≈ 45/day + twin ≈ 5/day**
    (24 h `increase()`, reset-repaired). First token-shape read: **primary is long-form**
    (prompt p50 ≈ 741, completion p50 ≈ 495 / p90 ≈ 958 tok), **twin is short** (completion
    p50 ≈ 13 tok), **stream coverage = 0 %** (all consumers non-streaming → histograms catch
    100 %). Preliminary lean (sample still <1 day): the lanes have **inverted shapes**, so
    the verdict is **per-lane not blanket** — primary's long-form median sits in the
    SD-hostile regime (regresses −53…−75 % per `ngram-sd-workload-conditional`), twin's
    short traffic is the lane SD would help. Records the read, does **not** act on it.
    Matrix: "2026-06-05 S3.0 — first per-lane data-clock read".
- **S3.1** — Replicate in-process n-gram SD (`speculativeConfig` passthrough,
  already wired at `backend/vllm.go:212-216`) to qwen35 (once it serves coherently
  — see #51/#52). **Twin already done** (see reconciliation above). Verify
  graph-capture is on (the win is conditional on it).
- **S3.2** — Tune `num_speculative_tokens` 5→7 / `prompt_lookup_max` 4→6 on the
  **twin** to match the already-tuned primary (per-position acceptance still 68% at
  position 5 → headroom). **GATED on S3.0 data**: n-gram SD is
  **workload-conditional** (long-form gen regresses; see
  `ngram-sd-workload-conditional.md`) — read the twin's completion-token
  distribution before bumping.
- **S3.3** — Controlled `maxNumSeqs` raise on the 26B lanes with the Sprint 0
  dashboard watching KV pressure; find the batch-depth knee at current context
  before latency regresses. **High blast radius** (live warm-pinned daily-driver
  primary at 32K/FP8-KV ~5GB KV) — canary the twin first. Matrix row with the
  throughput/latency tradeoff curve.

**Acceptance**: S3.0 instrument live + ≥1 day of per-lane token-shape data → blanket
SD verdict per lane; twin tuned (or documented null) on that evidence; `maxNumSeqs`
knee captured with the tradeoff curve.

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
