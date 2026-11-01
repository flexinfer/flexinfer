# Next differentiating features for the gfx1100 and gfx906 lanes (round of 2026-11)

Date: 2026-10-31. Status: **design, awaiting owner review**. Implementers: Opus 5.5 agent sessions,
one per brief in §9.

Predecessors, read in this order:

1. `.loom/10-research-gfx1100-next-feature-2026-10-23.md` (R): the October serving round, S1–S6.
2. `.loom/10-research-serving-gaps-switching-caching-2026-10-27.md` (SG): switching and cache gaps R1–R12, C1–C11.
3. `.loom/10-research-voice-stack-and-gpu-placement-2026-10-30.md` (VS): voice audit, placement layout, gaps G1–G11, KT-ASR.

Evidence gathered for this doc on 2026-10-31: `git log` 2026-10-10..31 on `master`, `glab mr list`
(merged !1145–!1206, open !1203/!1204/!1207), live `Model`/`GPUProfile`/node state on k3s,
Prometheus (`flexinfer_gpu_healthy`, `flexinfer_proxy_requests_total` over 7 d), consumer code in
loom-core, jobsearch-app, imagegen-ui, psyche-simulation, mentatlab, platform/gitops (LiteLLM), and an
upstream check of vLLM 0.32 structured outputs, KV offloading, sleep mode and the OpenAI async API
shapes (§6.2, §6.4 cite the sources).

---

## 0. TL;DR

The October round made the busiest gfx1100 card fast and gave the proxy two routing primitives. The
November window (no earlier than **2026-11-06 00:15Z**, owner present) ships the placement layout
(!1203), the workhorse failover pair (!1204) and the whisper.cpp ASR lane (!1207). What stays thin is
everything a caller experiences around a lane that is **not Ready**:

- cold starts of 3–13 min that every consumer papers over with 300–1800 s timeouts, retry loops and
  response-body string matching (§3);
- creative renders and transcriptions that hold one HTTP connection for up to 30 min (§3);
- one Vega card carrying ~115K embedding requests a day, 50× the workhorse, with no twin, and a
  consumer that fled to the CPU lane because batches died mid-swap (§1.3, §3);
- a two-seat workhorse shared by interactive callers and Mills' background research with no
  caller-level fairness (§3).

Those are the places where a Kubernetes operator for consumer AMD GPUs can be differentiated and
nobody else is (§4). The pick:

| Wave | Id | Feature | Live impact at merge | Kill-test |
|---|---|---|---|---|
| 1 | **D1** | Instant flex card: in-pod switching on the XTX runtime with a measured swap floor | none until the window-gated runtime repin | KT-SWAP |
| 1 | **D2** | Scale-to-zero contract: typed activation errors with ETA, a cold capability descriptor, and an async job surface (`background: true`, `InferenceJob`) | none (new paths, new CRD) | KT-JOBS |
| 1 | **D3** | Caller QoS classes mapped onto vLLM priority scheduling and per-class admission | none until a lane opts in | KT-QOS |
| 1 | **D6** | Retrieval plane protection: swap-safe replay, an eviction guard for the gte lane, CPU twin failover via G1 | annotations only (owner paste) | KT-RET |
| 1 (small) | **D4** | Agent-grade outputs: structured-output certificate + tool-call conformance gate | none until certified | KT-SO |
| 2 | **D5** | Voice plane: diarization into the Model API through jobs, then a realtime loop on the Radeon VII | gfx906 runtime roll | KT-DIAR, KT-VOICE-RT |
| 2 | **D7** | Efficiency and evidence as product: tokens per joule, public certification matrix on flexinfer-site | none | — |

Every feature names its riskiest load-bearing assumption and a pre-registered pass rule (§6), per the
repo's rule that the decision is written before the data. Nothing in wave 1 changes a live lane at
merge time except D1's runtime repin, which is window-gated and owner-run.

---

## 1. What the October round delivered (2026-10-10 → 10-31)

### 1.1 gfx1100 serving (5930k workhorse, XTX warm lane)

| Slice | What landed | MRs | Numbers |
|---|---|---|---|
| S1 | vLLM 0.32 native-W4A16 runtime on the gfx1100 profile; upstream wheel, contract gate forbids from-source rebuilds | !1155 (promote `cd108c9e`), later `b06b06fe` production pin (!1185) | 41.5 tok/s, pool 2.15× at 64K (R §7a) |
| S2 | MTP K=2 kill-test | canaries !1164/!1170 | pos-0 acceptance 0.80, MAL 2.35 (R §7c) |
| S3/S3b | Attention A/B: `ROCM_SEGMENTED_ATTN` + fp16 KV + MTP beats TRITON + fp8; the short-prompt TTFT floor was first-use Triton JIT | !1176 | decode +93–101 % (R §7d', §7d'') |
| S4 | Workhorse promoted to segmented + MTP at 32K; proxy reroutes over-length prompts to the XTX 64K lane under the target lane's budget | !1177/!1179, !1178, !1182/!1183 | decode 68.5 @2K, 65.9 @16K (was 40.6); TTFT 0.235/0.423/1.039 s at 256/512/1,124; E2E mean −30 % (R §7f) |
| S6a | Stacked gfx1100 serving persona image on 0.32 (`ef28b7b0`), verified offline | !1174 | pinned nowhere yet (SG §1) |
| — | Per-Model `env` and vLLM `kernelConfig` | !1172/!1173 | — |
| — | llama.cpp b11847 for the Glimmer lane; draft-MTP certified behind a digest-bound GPUProfile certificate | !1132, !1145 | 1.33–2.30× probe |
| — | Qwen-Image 2.1 served from the gfx1100 serving image (NF4, diffusers 0.41 shim), demand-based lane on the 5930k | !1165, !1175, !1191, !1192 | lane exists, V0–V6 GPU verification not recorded in the matrix |

### 1.2 Controller and proxy

| Area | What landed | MRs |
|---|---|---|
| Proxy request path | API calls off the request path, informer cache, direct targets resynced every 10 s; CPU limit 2000m | !1161/!1162, !1151 |
| Proxy lane breaker | probes non-chat lanes on `/health`; client cancellations no longer count as failures | !1193/!1194 |
| Controller stability | shared-group reconcile hot loop fixed; cache-status patch churn; parked-lane cadence; cache bookkeeping | !1122–!1130, !1157 |
| GPU health sentinel | node agent labels a dead card `gpu.health=degraded`; lanes park automatically | !1104/!1105/!1112 |
| S6b pre-work | reload on spec change (R1), loading substage + KV-pool recheck (R4), load-latency buckets with `serving_path` label (R9), event cooldown (R11) | !1186/!1187, rolled in !1189 |
| Round-2 runtime path | proxy and controller build one load payload (R6); launch overrides fail closed; cache re-check flap (R10); configurable reconcile concurrency (R12) | !1199, rolled in !1201 |
| Cache stack | compile cache shared per (image digest, GPU arch), opt-in (C5); cache markers keyed on download options (C1/C2) | !1200, !1137/!1138 |
| 5930k runtime park | `runtime.profiles[].parked` in git; four 5930k lanes stay `dedicatedDeployment` until S6b | !1196 |

### 1.3 gfx906 (Radeon VII)

| What landed | MRs | Numbers |
|---|---|---|
| Qwen3-Embedding-4B and Qwen3-Reranker-0.6B lanes | !1146 | canary re-index GO (matrix row `qwen3-embedding-gfx906-2026-10-24`) |
| HWS compute-queue admission for shared-GPU lanes (24 queues, 2 → 3 reserved), `GPU_MAX_HW_QUEUES=1` per llama-server, unified image without `HSA_OVERRIDE_GFX_VERSION` | !1140, !1198 | warm set measured 18 of 24 queues with pyannote (VS §4.1) |
| whisper.cpp backend for `/v1/audio/transcriptions` (word timestamps merged from sub-word tokens) | !1206 | KT-ASR: 41.5× real time unmasked, **34.7× at 40 of 60 CUs with gte p95 −0.2 %** (VS §4.1, !1205) |
| Voice stack audit; stable-audio digest pinned for retention | !1202 | — |

The Radeon VII is the busiest card by request count. Prometheus, 7-day average per day, 2026-10-31:

| Lane | Requests/day |
|---|---|
| `gte-qwen2-1p5b-radeonvii` | ~114,800 |
| `bge-large-radeonvii` | ~4,050 |
| `qwen38-27b-autoround-workhorse` | ~2,290 |
| `bge-reranker-radeonvii` | ~290 |
| `gte-qwen2-1p5b-cpu-5930k` | ~180 |
| `qwen38-27b-xtx-warm-canary` | ~75 |

### 1.4 Platform state that constrains the round

- **XTX**: degraded about 41 of the 168 h before 2026-10-30 (VS); healthy continuously since the
  `pcie_aspm.policy=performance` + Gen3 soak began 2026-10-30 00:15Z (Prometheus
  `flexinfer_gpu_healthy{node="cblevins-7900xtx"}` = 1 from 2026-10-30 00Z through 10-31 18Z).
  Verdict due 2026-11-06 00:15Z. The flex-card plan assumes the card holds.
- **5930k**: healthy for the whole window; root filesystem cannot hold Longhorn replicas, model caches
  and ~300 GB of images together (DiskPressure 2026-10-24; 84 GiB of orphan caches removed). Keep
  ≥ 200 GB free before any pull.
- **Harbor retention** deletes digests not pulled in 30 d unless tagged `pinned-*`; `ef28b7b0` is
  unprotected (SG §7).
- **Host RAM**: 64 GB on both gfx1100 nodes, 128 GB on the Radeon VII node (k3s node capacity).
- **docs/IMPLEMENTATION_STATUS.md** is dated 2026-03-05 and predates GPUProfiles, the runtime
  DaemonSets, vLLM 0.32, MTP, the sentinel and whisper.cpp. It should be rewritten or retired in
  this round (small docs slice, not a feature).

---

## 2. In flight and gated

| Item | State | Gate |
|---|---|---|
| !1203 placement layout: 5930k always-on workhorse (900, primary, `minReplicas 1`), XTX flex card (warm lane 300 ondemand `minReplicas 1`, creative lanes at 500) | Draft, tests green against the new manifests | window ≥ 2026-11-06 00:15Z, XTX soak verdict, owner present; KT-FLEX in-window |
| !1204 workhorse failover: proxy `unavailable-reroute-to` (G1), election `failover-for` (G2) | open, behaviour-neutral until annotated | merge any time; exercised in the window |
| platform/gitops!1018 LiteLLM shared-label routes (G3) | open | with !1203 |
| !1207 whisper.cpp ASR lane on the Radeon VII (`HSA_CU_MASK=0:0-39`, priority 111, first-byte budget 900 s) | Draft | controller + gfx906 runtime rolled to a build containing !1206 |
| S6b retargeted to the XTX profile (VS §7 D3): repin `cfefae18 → ef28b7b0`, warm and creative lanes in-pod | planned, **unowned** | after !1203 applies and KT-FLEX passes |
| Open SG items: cache reclaim (C4/C8), gfx906 Track C (vLLM revive or retire), gfx1100 retrieval pair (SG §8 #8), Fable graph-mode slice | open | — |
| Older drafts: !1062 Qwen3.8-27B TURBO Fable canary, !237 Gemma4 EGA, !1087 runtimes review, !997 (superseded by !1196) | stale | close or rebase during the round |

---

## 3. What the consumers do to live with flexinfer today

Every path below is evidence for D2, D3 or D6. File references are in the named repos under
`/Users/cblevins/workspace`.

| Consumer | How it calls | Workaround it carries | Source |
|---|---|---|---|
| loom-core Weaver/Mills | non-streaming chat on the two Qwen3.8 serving names, "No streaming or tools" | detects a cold start by matching "runtime pod is starting" / "no runtime endpoint found" in 4xx/5xx bodies; parks a lane 60 s on timeout, 20 s probe before reuse; 5-min default timeout because 30 s "escalated every research call"; walks a fallback chain on the parked-behind-primary 503 ("research 24×503 in 7d"); readiness probe before committing to a stale lane; retries once with 4× tokens when reasoning eats the budget | `services/loom-core/pkg/flexinfer/client.go:396-404`, `pkg/weaver/model_failover.go:54-89`, `pkg/mills/clients/flexinfer.go:100-104,319,489-494,584-588,811-813`; `platform/gitops/k3s/mills/deployment.yaml` ("wedged … while the proxy kept listing the lane as Ready") |
| jobsearch-app | workhorse by serving name; embeddings on the **CPU twin** | `LLM_REQUEST_TIMEOUT=300` ("Default 120s dies inside the workhorse's 3-5 min cold start"); embeddings pinned to `gte-qwen2-1p5b-cpu-5930k` because the Radeon VII "kept killing the embedder mid-batch"; inputs cut to 30K chars and shrunk on retry; 3 retries then OpenAI; **ASR bypasses flexinfer** (faster-whisper CPU, 600 s timeout); logs "Diarization not supported by the ASR backend" | `infrastructure/daemon/base/deployment.yaml:116-121`, `src/daemon/vector/embeddings.py:32-36`, `src/daemon/llm/config.py:113-143`, `src/daemon/routers/transcription.py:38-43,105-106` |
| imagegen-ui | images and edits straight to the proxy (LiteLLM strips diffusers params); **video synchronous** | `VIDEO_REQUEST_TIMEOUT_SECONDS=1800`, 4 retries 5 s apart on 502/503/504/524; copies the Wan 2.1 limits into the client "instead of waking a cold GPU lane just to get the same rejection" | `server/app.py:24-51,76-91` |
| psyche-simulation | streaming chat on the `workhorse` label, embeddings, images, video | `LLM_TIMEOUT=600`, `LLM_RETRY_ATTEMPTS=20` ("Hold demand through the workhorse lane's 3-6 minute scale-from-zero path"); an interrupted stream cannot be resumed; 64,512-token input guard | `k8s/configmap.yaml:33-43`, `llm/client.py:292-302`, `config/config.py:105+` |
| mentatlab | chat with 30 s timeout; activates through MCP then polls phase every 5 s for 120 s, exits code 3 for the scheduler to retry | `agents/flexinfer-adapter/main.py:30-36,161-177` |
| LiteLLM (gitops) | one route per alias, all to the proxy; 180 s default, 1800 s for the workhorse and 9B RP lanes; **no fallback for any flexinfer alias** (fallbacks exist only for cloud models); a cold ~226K prefill returned 408 before the first token | `platform/gitops/k3s/ai/litellm/litellm.yaml:117-135,118-123`, `external-models.yaml` |
| fi-fhir | `fast` and `workhorse` via LiteLLM, 120 s timeout "may be cold" | `platform/gitops/k3s/fi-fhir/fi-fhir-api.yaml:262-281` |
| flexinfer-site | advertises chat, completions, embeddings, images; n-gram speculative autotune; Federation; GamingSession. Never mentions ASR, TTS, rerank or video | `services/flexinfer-site/data/flexinfer-product.ts:35-112` |

What the proxy gives them today: a typed error envelope only from the lane breaker and the retry
path (`validation.WriteError`, type `service_unavailable_error`, code `lane_unresponsive`,
`Retry-After`); plain-text errors elsewhere; `X-FlexInfer-Context-Overflow`, `-MaxTokens-Clamped`,
`-Prefix-Cache-Hit-Rate`, `-Cached-Tokens`, `-Upstream-Ms` headers; `status.loadingSubstage`
(`ImagePulling`, `Initializing`, `LoadingWeights`, `Compiling`, `ProfilingKV`) is recorded on the
Model since !1187 but is not exposed to any caller.

---

## 4. Where the differentiation is, and where it is thin

What flexinfer does that the field (KServe/Knative, llm-d, the vLLM production stack, NVIDIA Dynamo,
Ollama/LocalAI, KAITO) does not:

1. **Consumer AMD, two generations, certified per arch.** RDNA3 and Vega20 with digest-bound
   GPUProfile backend-capability certificates (draft-MTP, queue budgets, HSA settings). The field is
   NVIDIA-first; none certifies ROCm features per card.
2. **Priority and demand election across modalities on one 24 GB card.** Text, image, video, audio
   lanes time-share a GPU with scale-to-zero, preemption, parked-behind-primary, swap cooldowns and
   HWS queue admission. Nobody else time-shares a single consumer card between an LLM and a diffusion
   lane by priority.
3. **Proxy intelligence that owns the lane**: context-overflow reroute, lane breaker, first-byte
   budgets, unavailable-reroute (!1204), KV-aware and least-loaded routing, admission by context
   window.
4. **Evidence-driven promotion**: kill-tests with pre-registered rules, the gauntlet, the validation
   matrix, promotion evidence on the Model itself.
5. **In-pod multi-model runtime** on the Radeon VII (3.5–9 s swaps) with drain.

Where it is thin, by the evidence in §3:

- The scale-to-zero story stops at "the lane will come up". Callers get no typed state, no ETA, no
  queue position, and must hold a connection for the whole wait.
- Long work (renders, transcriptions, batch embeddings) is synchronous. The first-byte budget
  (120 s default, 480–900 s on specific lanes) is the only tool, and it is a workaround for not
  having jobs.
- The gfx1100 cards still swap by pod churn (767 s mean cold start on the 5930k, SG §2) while the
  Radeon VII swaps in seconds. The flex-card layout (!1203) will make this visible every time a
  render preempts the warm lane.
- The highest-volume lane in the fleet is a single Vega card with no GPU twin and a consumer that
  left for the CPU lane.
- Fairness ends at the lane: the two-seat workhorse serves interactive and background callers FCFS.
- The product surface the site advertises (chat, completions, embeddings, images) is a subset of
  what the cluster serves (ASR, TTS, rerank, video, RAG).

---

## 5. Candidates considered

Ranked by: differentiation (is anyone else doing it on consumer AMD?), consumer evidence (§3),
feasibility for an agent in 2–4 weeks with the hardware available, and live risk.

| # | Candidate | Verdict | Why |
|---|---|---|---|
| 1 | In-pod switching on the XTX runtime with a measured swap floor (S6b-XTX + R7 weights residency) | **D1, wave 1** | the enabler for the flex card; planned in VS §7 D3 but unowned; pre-work and shared compile cache already landed |
| 2 | Typed activation errors + ETA, cold capability descriptor, async jobs | **D2, wave 1** | every consumer in §3 compensates for its absence; OpenAI shapes exist to copy |
| 3 | Caller QoS classes on vLLM priority scheduling | **D3, wave 1** | `schedulingPolicy` already rendered by the controller (`backend/vllm.go:151-155`); a gateway cannot do this, only the operator that owns the scheduler can |
| 4 | Retrieval plane protection (swap-safe replay, eviction guard, CPU twin failover) | **D6, wave 1** | 115K req/day on one card; jobsearch already fled; G1 (!1204) gives the failover primitive for free |
| 5 | Structured-output certificate + tool-call conformance gate | **D4, wave 1 (small)** | upstream support confirmed on ROCm and with speculative decoding (§6.4); mostly certification and a gauntlet gate |
| 6 | Voice plane: diarization in the Model API via jobs; realtime loop on the VII | **D5, wave 2** | depends on D2's job surface and on !1207 landing; jobsearch logs the missing diarization today |
| 7 | Tokens per joule telemetry; public certification matrix on the site | **D7, wave 2** | cheap, product-facing; the site under-advertises what the cluster serves |
| 8 | KV persistence across swaps (C11, vLLM `OffloadingConnector` `fs` tier) | **optional arm of D1** | ROCm supported upstream (§6.1); value is ≤ 1 s TTFT per request after a swap at p99 prompt 15.5K; measure inside KT-SWAP, do not build a slice on it |
| 9 | vLLM sleep mode in the election (#68) | **deferred, re-check** | upstream fixed the ROCm cuMem host mapping in 0.32 (PR #60367); level-2 wake still drops draft weights (#52479); the cluster-side device-plugin and readiness blockers from #68 stand |
| 10 | gfx906 vLLM revive or retire (Track C) | **decision, not a feature** | O7 in §10; nothing in wave 1 needs vLLM on gfx906 |
| 11 | gfx1100 GPU twin for embeddings | **rejected** | no VRAM beside a 27B lane at `gpuMemoryUtilization 0.96` on either card; the CPU twin plus G1 is the twin |
| 12 | Cross-card disaggregated prefill or KV migration | **rejected** | 2.5 GbE between nodes; the overflow reroute already covers the long-prompt case |
| 13 | New model candidates (Mistral Small 4, Qwen 4 27B, Bolt 74B-A9B, MXFP4 gpt-oss) | **watch** | R §5 said re-check in November; none is a platform feature |
| 14 | Namespace multi-tenancy with quotas (`docs/design/multi-tenancy.md`) | **after D3** | D3 is the first step that pays for itself |

---

## 6. Selected features

Each entry: why it differentiates, evidence, design, scope, non-goals, the riskiest load-bearing
assumption, the pre-registered kill-test with its matrix row, live impact and window, done criteria.

### 6.1 D1 — Instant flex card: in-pod switching on the XTX runtime with a measured swap floor

**Why.** Under !1203 the XTX serves the Mills warm lane at 300 and the creative lanes at 500. Every
render preempts the warm lane and every idle-out brings it back. Today each of those is a pod churn
(5930k cold start mean 767 s, SG §2) while the Radeon VII swaps in-pod in 3.5–9 s. A flex card that
swaps in about a minute is the product; one that swaps in 13 is a demo.

**Evidence.** SG §2, §3.1; R §7e; VS §7 D3. The XTX runtime profile already sets
`FLEXINFER_RUNTIME_MULTI_MODEL=1` and `FLEXINFER_RUNTIME_REQUEST_DRAIN=1`
(`deploy/system/values-k3s.yaml:825-831`); the 5930k runtime is `desired=0` with the same flags
(`:886-891`). The runtime exposes `POST /api/v1/models/{name}/load`, `DELETE`, `health`, `status`
(`internal/runtime/api.go:34-43`). Pre-work R1/R4/R6/R9/R10/R11 landed (!1186, !1187, !1199). The
shared compile cache (C5) landed (!1200). Load substages are recorded (`internal/runtime/load_progress.go`).

**Design.**

1. Protect `ef28b7b0` with a `pinned-ef28b7b0` Harbor tag and add it to the XTX prewarm list
   (live-neutral MR).
2. Repin the XTX runtime profile `cfefae18 → ef28b7b0` and the gfx1100 GPUProfile `runtime.image`
   (owner-run in the window; rolls the XTX runtime pod once, snac reloads).
3. Move lanes onto the runtime path one at a time (S6c), warm lane first, then Qwen-Image, Wan T2V,
   Wan VACE, Stable Audio: drop `dedicatedDeployment`, move MTP wrappers to `spec.config.env` (R3;
   the workhorse already carries `FLEXINFER_QWEN35_MTP_HEAD` as env).
4. Swap-floor profiler: a `flexinfer-bench swap` subcommand that drives load/unload through the
   runtime API, records per-substage durations (`LoadingWeights`, `Compiling`, `ProfilingKV`,
   `Ready`), decode and greedy hashes, and writes a matrix row. This is the instrument for KT-SWAP
   and for every later runtime change.
5. Weights residency (R7): after the profile says what dominates. If `LoadingWeights` dominates,
   keep the last-unloaded lane's weights resident in page cache (64 GB host RAM; the 27B INT4 is
   17 GB, Qwen-Image NF4 staged is ~33 GB, so at most the warm pair fits) and measure again. If
   `Compiling`/`ProfilingKV` dominate, residency buys nothing and the slice stops at step 4.
6. Optional arm, measured not built: vLLM `--kv-offloading-backend native` with a `fs` secondary
   tier on the compile-cache hostPath, to see whether a Mills prefix survives a swap (C11).

**Scope.** `deploy/system/values-k3s.yaml`, `deploy/gpuprofiles/gfx1100.yaml`, the five XTX lane
manifests, `cmd/flexinfer-bench`, `internal/runtime/load_progress.go`, `controllers/placement_layout_test.go`
(extend to the runtime path), README serving-path section, SG §3 closeout.

**Non-goals.** Un-parking the 5930k runtime (it stays `parked: true`; the 900 workhorse keeps its
dedicated Deployment this round). Sleep mode. Two lanes resident in VRAM at once.

**Riskiest assumption.** An in-pod vLLM 0.32 load of the 27B warm lane on gfx1100, with the shared
compile cache warm and the weights in page cache, completes in ≤ 120 s and is greedy-identical to the
Deployment lane. If `Compiling` or `ProfilingKV` dominate regardless of cache, the floor is ≥ 3 min,
the flex card stays a "wait" experience, and D2's async surface carries the UX while D1 reduces to the
repin and the profiler.

**Kill-test KT-SWAP** (matrix row `gfx1100-xtx-inpod-swap-<date>`). On the XTX runtime pod after
!1203 and the repin: warm lane ↔ Qwen-Image, 5 swaps each direction, nonce prompts. Pass when all of:
27B load p50 ≤ 120 s and p95 ≤ 180 s; diffusers load p50 ≤ 90 s; greedy hash identical to the
Deployment lane on the three fixed S3 prompts; decode within 3 % of the Deployment lane (R §7e rule);
0 SMU/amdgpu errors in the host log; the warm lane is back within the 5-min swap cooldown after each
render. Record the substage breakdown whatever the verdict.

**Live impact and window.** The repin and the first in-pod load need the owner present, after
KT-FLEX passes. Agent sessions cannot apply Model CRs; the brief hands the owner a paste sequence.
Rollback: repin to `cfefae18` (stays prewarmed) and restore `dedicatedDeployment: true`.

**Done.** XTX runtime on `ef28b7b0`, pinned and guarded; warm and creative lanes served in-pod;
KT-SWAP row recorded with the substage breakdown; the cold-start histogram shows the new floor by
`serving_path`; SG §3.1 and §5b marked closed or re-scoped; README serving truth updated.

### 6.2 D2 — Scale-to-zero contract: typed activation errors, a cold capability descriptor, async jobs

**Why.** Section 3 is the case. Seven consumers carry 300–1800 s timeouts, retry loops, string
matching and client-side copies of lane limits because the proxy says nothing legible about a lane
that is not Ready and offers no way to hand it long work. An operator whose scale-to-zero is legible
(what state, how long, what to do next) and whose long work is asynchronous removes every one of
those workarounds, and does it at the layer that owns the election. The shape to copy is OpenAI's
own: the Responses API `background: true` (status `queued | in_progress | completed | failed |
cancelled | incomplete`, `GET /v1/responses/{id}`, `POST …/cancel`, resume with
`?stream=true&starting_after=<n>`), the Batch API, and the Standard Webhooks envelope
(`webhook-id`, `webhook-timestamp`, `webhook-signature`). The OpenAI Videos API was deprecated
2026-03-24 and removed 2026-09-24, so **do not model the surface on `/v1/videos`**; imagegen-ui's
`/v1/videos/generations` is already non-standard and migrates to a job.

**Design.**

A. **Typed error envelope** on every non-2xx the proxy originates, extending the lane-breaker shape:

```json
{"error": {"type": "service_unavailable_error", "code": "model_cold_starting",
  "message": "…", "param": "qwen38-27b-autoround-workhorse",
  "flexinfer": {"phase": "Loading", "loading_substage": "Compiling",
    "eta_seconds": 95, "retry_after_seconds": 30, "reroute_to": null}}}
```

Codes: `model_cold_starting`, `model_parked`, `model_preempted`, `model_failed`,
`lane_unresponsive` (existing), `context_overflow`, `admission_rejected`, `queue_full`,
`stalled_load` (existing path). `Retry-After` on every 503. `X-FlexInfer-Activation:
<phase>/<substage>` on every response during a cold start, including the eventual 200. ETA from
`status.loadingSubstage` + `loadingProgressAt` and the lane's load-latency histogram p50 by
`serving_path` (metrics from !1186).

B. **Cold capability descriptor**: `GET /v1/models/{name}` answers from the Model CR without waking
the lane: modalities and served paths (`serve-paths`), `max_model_len`, image and video limits
(sizes, steps, duration, from `spec.config` or a new `spec.capabilities.limits` block), current phase,
substage and ETA, QoS classes accepted (D3). imagegen-ui's copied Wan limits and Mills' readiness
probes become reads of this. `GET /v1/models` keeps its OpenAI shape and gains the same block under
`flexinfer`.

C. **Async jobs.**

- CRD `InferenceJob` (`ai.flexinfer/v1alpha2`): `spec {model, path, request (inline JSON, or a
  ConfigMap/Secret ref for bodies over 256 KiB), inputRefs[], webhook {url, secretRef, events[]},
  ttlAfterFinished, qosClass (D3), idempotencyKey}`; `status {phase queued|in_progress|completed|
  failed|cancelled|incomplete, progress, queuedAt, startedAt, completedAt, expiresAt, output {uri,
  contentType, bytes, sha256}, error {code, message}, runner}`.
- Proxy endpoints: `background: true` on `/v1/images/generations`, `/v1/images/edits`,
  `/v1/audio/transcriptions`, `/v1/audio/speech`, `/v1/chat/completions` (returns the job object);
  `POST /v1/jobs` (generic); `GET /v1/jobs/{id}`, `GET /v1/jobs/{id}/content`, `POST
  /v1/jobs/{id}/cancel`, `GET /v1/jobs?status=&model=`. Job object mirrors the Responses background
  fields plus `flexinfer {model, lane, qos_class, queue_position, eta_seconds}`.
- Runner in the proxy (it owns `prepareLane`, budgets, the breaker and the reroutes): a bounded
  worker pool per Model (1 for diffusers lanes), claim by `resourceVersion` so more than one proxy
  replica is safe, execute under the lane's first-byte budget, stream the output to object storage
  (`daemon/minio` at 192.168.50.240 exists; a Longhorn `bulk-*` PVC is the fallback, owner decides
  O2) and write a served URL, fire webhooks with retries and dedupe ids. A queued job stamps demand
  on its Model (`lastActiveTime`, `activate-requested-at`) exactly as `handleColdStart` does, so a
  job is demand in the election; while it runs it keeps stamping.
- Manager side: TTL garbage collection, `flexinfer_jobs_*` metrics, status validation, printer
  columns.
- Later, same surface: `background: true` + `stream: true` with `starting_after` for chat, which is
  the resumable stream psyche cannot have today.

**Scope.** `api/v1alpha2/inferencejob_types.go`, `controllers/inferencejob_controller.go`,
`internal/proxy/jobs*.go`, the envelope as an extension of `pkg/validation/errors.go`
(`WriteError`, `WriteErrorWithParam` already exist; add the `flexinfer` block) plus a thin
`internal/proxy/errors.go`, `internal/proxy/models.go` (descriptor), `docs/specs/proxy-api.md`,
`docs/specs/crds.md`, chart RBAC for the proxy (InferenceJob read/write), a `flexinfer jobs` CLI verb.

**Non-goals.** Replacing LiteLLM; multi-cluster jobs; a UI.

**Riskiest assumption.** The shared-GPU election can hold a card for a queued job's lifetime with
nothing but the existing demand signal: a queued render on the flex card must take the card from the
300 warm lane only when it actually runs, keep it while it runs, and release it so the warm lane
returns within the swap cooldown, without starving the interactive tenant or flapping. If the
election needs a reservation primitive (a `GPULease`-like hold owned by the job), the brief grows by
one controller change and KT-JOBS re-runs.

**Kill-test KT-JOBS** (matrix row `proxy-async-jobs-<date>`). Submit 3 Qwen-Image jobs and 1 Wan
T2V job while the warm lane serves a 20 req/min chat replay. Pass when: all 4 complete with
retrievable outputs; no client connection held longer than 30 s; 0 × 504/524; chat p95 TTFT within
±20 % of baseline until the election flips; the warm lane is back ≤ 5 min after the last job; ≥ 3 of 4
webhooks delivered within 10 s; a cancelled job frees the card within the cooldown; the typed
envelope is returned on every 503 during the run and Mills' client, pointed at a dev proxy, resolves
the cold start from `code` instead of the body string.

**Live impact.** None at merge: new CRD, new paths, existing synchronous paths unchanged. Consumer
migrations are follow-ups in their repos: imagegen-ui video (1800 s → job), ICC and jobsearch ASR
(600 s → job), Mills cold-start detection (string → code), mentatlab activation polling
(→ descriptor ETA).

**Done.** Envelope on every proxy-originated error with tests per code; descriptor serves cold;
`InferenceJob` CRD, runner, GC and metrics; KT-JOBS row; proxy-api.md and crds.md updated; one
consumer (imagegen-ui video) migrated as the proof.

### 6.3 D3 — Caller QoS classes on vLLM priority scheduling

**Why.** The workhorse runs `maxNumSeqs: 2`. Interactive callers (jobsearch, psyche, the HUD,
fi-fhir) share those two seats with Mills' background research and shadow judging (5-min
timeouts, per-hunk chunking). vLLM's `--scheduling-policy priority` preempts lower-priority running
sequences; the controller already renders it from `spec.config.schedulingPolicy`
(`backend/vllm.go:151-155`), and the proxy already has caller identity (`caller_identity.go`,
logging only), context-window admission (`admission.go`), a per-model queue and a rate limiter. A
gateway cannot do fairness at the GPU; the operator that owns the scheduler can, and on a two-seat
consumer card it is the whole difference between "usable while Mills runs" and not.

**Design.**

- Class resolution, first match wins: request header `X-FlexInfer-QoS`; caller-identity map
  (ConfigMap `flexinfer-proxy-qos`: API key or caller name → class); LiteLLM alias suffix
  (`workhorse-background`); default `interactive`.
- Classes: `interactive` (vLLM priority 0; always admitted up to the lane's seats), `background`
  (priority 10; admitted when a seat is free or an interactive seat is idle; otherwise queued with
  `Retry-After` and the typed `queue_full`/position from D2), `batch` (priority 20; jobs only via D2;
  preemptible).
- The proxy sets the vLLM per-request `priority` field (confirm the 0.32 OpenAI protocol field name
  in the brief), orders its queue by class then FIFO, enforces per-class in-flight caps derived from
  `maxNumSeqs`, labels the usage log and metrics (`flexinfer_proxy_qos_decisions_total{class,lane,
  outcome}`, per-class TTFT and duration histograms).
- Lanes opt in with `spec.config.schedulingPolicy: priority`; a lane without it treats every class
  as `interactive` (behaviour-neutral).

**Scope.** `internal/proxy/qos.go` (new), `caller_identity.go`, `queue.go`, `routing.go`,
`metrics.go`, `usage_log.go`, chart ConfigMap and values, workhorse manifest (`schedulingPolicy`),
`docs/specs/proxy-api.md`, `docs/design/multi-tenancy.md` (D3 as M1 step 1).

**Non-goals.** Namespace quotas, billing, per-tenant lanes.

**Riskiest assumption.** vLLM 0.32 priority scheduling composes with MTP K=2, `ROCM_SEGMENTED_ATTN`
and chunked prefill on gfx1100 without throughput regression or preemption thrash on a two-seat
lane. If preemption of a background sequence costs more than the seat it frees, the design falls back
to proxy-side class queues only (no vLLM priority), which still removes the starvation but not the
latency hit.

**Kill-test KT-QOS** (matrix row `workhorse-qos-priority-<date>`). On a canary lane (the XTX warm
lane in-pod after D1, or a parked 5930k twin in a window) with `schedulingPolicy: priority`: two
background streams saturating both seats plus one interactive request every 10 s, 20 min. Pass when:
interactive TTFT p95 ≤ 1.3× the idle-lane baseline; background makes progress (no gap > 60 s);
aggregate decode within 5 % of the same mix under `fcfs`; greedy parity with `fcfs` on the fixed
prompts; MTP pos-0 acceptance unchanged (≥ 0.75).

**Live impact.** Enabling priority on the workhorse is a config change: an in-pod reload on the
runtime path, or a Deployment roll (~3 min) while it stays dedicated. Schedule with the owner in a
quiet hour; everything else is live-neutral.

**Done.** Classes resolved and enforced; metrics and usage log carry the class; KT-QOS row; the
workhorse runs `priority` with Mills mapped to `background`; proxy-api.md documents the header, the
classes and the typed queue responses.

### 6.4 D4 — Agent-grade outputs: structured-output certificate and tool-call conformance gate (small)

**Why.** Mills, mentatlab and jobsearch run agent loops against the workhorse; the repo ships an
agent-loop client (`internal/agentloop`) and tool parsers (`qwen3_coder`), but nothing in the
controller or proxy handles `response_format.json_schema` or vLLM's `structured_outputs`, and no gate
certifies that a lane's tool calls parse. Upstream check, 2026-10-31: vLLM 0.32 backends are
`auto | xgrammar | guidance | outlines | lm-format-enforcer` (LMFE deprecated from 0.33); the logits
mask is applied by a vLLM-owned Triton kernel, so **no ROCm limitation**; structured outputs compose
with speculative decoding since PR #14702 (bitmask rows allocated for `1 + num_speculative_tokens`,
drafts validated and rolled back), with the caveat that the parallel bitmask fill runs only when
spec tokens = 0, so the serial CPU path is used with MTP. The old `guided_*` request fields were
removed in v0.12; the request shape is `structured_outputs: {json|regex|choice|grammar|
structural_tag}` or `response_format.json_schema`.

**Design.** `spec.capabilities.structuredOutput` on the Model; the controller renders
`--structured-outputs-config` (backend `auto`) when set; a GPUProfile backend-capability certificate
`vllm/structured-outputs` (via `SetBackendCapabilityCertificate`, as llama.cpp draft-MTP) that the
controller requires before rendering the flag; a gauntlet gate `tool_call_conformance` driven by
`internal/agentloop` with 200 schema-constrained tool-call turns; the proxy passes
`response_format` and `structured_outputs` through unchanged and tags the usage log.

**Riskiest assumption.** The serial bitmask path with MTP K=2 does not eat the MTP gain: decode on
schema-constrained turns stays within 10 % of unconstrained and pos-0 acceptance stays ≥ 0.75.

**Kill-test KT-SO** (matrix row `gfx1100-structured-outputs-<date>`). 200 tool-call turns through
the agent loop against a canary lane: 100 % parse success, decode within 10 % of unguided, pos-0
≥ 0.75, greedy parity on unconstrained prompts unchanged.

**Live impact.** None until the certificate is set and a lane opts in. **Done.** Certificate,
capability, gate, matrix row; Mills' client can drop its 4× token retry for schema turns.

### 6.5 D5 — Voice plane (wave 2)

**Why.** !1206/!1207 put ASR in the Model API on the Radeon VII at 34.7× real time with gte
unaffected; Orpheus-3B TTS already serves in-pod on the same card; pyannote diarization runs as a
hand-written privileged Deployment outside the Model API (VS §3, §5); jobsearch logs "Diarization
not supported by the ASR backend"; jobsearch's "realtime" transcription is a WebSocket that buffers
and POSTs chunks (`routers/transcription.py` ~300). A private voice pipeline on a $150 Vega card,
behind one OpenAI-compatible proxy, is a differentiator the field does not have on any AMD card.

**Design.** (a) **Meeting pipeline**: backend `pyannote` in the gfx906 runtime (G8: image layer, HF
token, `hwQueueEstimate: 3`, admission); `/v1/audio/transcriptions` with `diarize=true` becomes a
D2 job that runs whisper.cpp then pyannote and merges `words` to speakers (`whispercpp_words.go`
already merges sub-word tokens); ICC and jobsearch repoint. (b) **Realtime loop**: a `/v1/realtime`
subset over WebSocket (session, input audio buffer append/commit, response audio deltas):
server-side VAD (Silero on CPU), 2–3 s chunked whisper.cpp on the VII, LLM `fast`
(`qwen3-1p7b-tools-radeonvii`) or `workhorse`, Orpheus tokens + CPU SNAC (G9) streamed as audio
deltas. `cmd/voice-loop` becomes the conformance client.

**Riskiest assumptions.** (a) pyannote fits the VII's queue and VRAM budget beside the warm set
(measured 18 of 24 queues with pyannote, VS §4.1). (b) whisper-server is batch-only on HIP, so
chunked 2–3 s utterances must keep WER within 10 % relative of whole-file and the chunk tail is the
latency floor.

**Kill-tests.** KT-DIAR (`gfx906-diarize-job-<date>`): a 30-min AMI meeting through the job path,
speaker-attributed words, DER within 5 points of the standalone pyannote Deployment, gte p95
unaffected. KT-VOICE-RT (`gfx906-realtime-loop-<date>`): end of speech → first audio byte ≤ 1.5 s p50
with `fast`, ≤ 2.5 s with `workhorse`, over 50 turns; gte p95 at baseline under the CU mask.

**Dependencies.** D2 (jobs), !1207 applied, G9 (CPU SNAC) from gitops.

### 6.6 D6 — Retrieval plane protection

**Why.** `gte-qwen2-1p5b-radeonvii` answers ~115K requests a day (§1.3), 50× the workhorse, on one
Vega card that also hosts bge, the reranker, Humanizer, Orpheus and soon ASR. Humanizer bursts evict
lower lanes (Orpheus 4× in 7 d, VS §3). jobsearch moved its embeddings to the CPU twin because the
card "kept killing the embedder mid-batch". SG §3.8 calls the VII the RAG single point of failure.
G1 (!1204) gives the proxy an `unavailable-reroute-to` primitive this slice can use for free.

**Design.**

- (a) **Swap-safe replay**: for idempotent paths (`/v1/embeddings`, `/v1/rerank`, non-streaming
  `/v1/audio/transcriptions`) a 5xx or connection reset during a runtime swap is replayed once on the
  same lane after Ready, or on the `unavailable-reroute-to` target, with `X-FlexInfer-Replayed`.
  Bound by the request's own deadline; never for streaming paths.
- (b) **Eviction guard**: `flexinfer.ai/eviction-grace: 20s` on the gte lane. The gfx906 runtime
  already drains accepted streams before releasing the GPU (`FLEXINFER_RUNTIME_REQUEST_DRAIN`); the
  guard adds the admission side: the election does not unload a lane with in-flight batches and
  non-empty queue until the grace expires, so a Humanizer burst waits one batch instead of killing
  it.
- (c) **Twin by failover**: annotate the gte lane `unavailable-reroute-to: gte-qwen2-1p5b-cpu-5930k`
  once !1204 merges, and size the CPU twin for the real rate. A GPU twin on gfx1100 is rejected
  (no VRAM beside a 27B lane at utilization 0.96 on either card).
- (d) Retrieval SLO dashboard: per-lane embedding p95, replay rate, eviction count, twin share.

**Scope.** `internal/proxy/routing_retry.go` (replay), `controllers/model_shared_gpu.go` (grace),
`deploy/models/gte-qwen2-1p5b-radeonvii.yaml` and the CPU twin manifest, Grafana dashboard in gitops.

**Non-goals.** A new embedding model; moving retrieval off the VII.

**Riskiest assumption.** The mid-batch kills are reproducible today, i.e. `REQUEST_DRAIN` on the
gfx906 runtime does not already prevent them. If the reproduction yields 0 client-visible errors,
(b) is dropped and (a), (c), (d) ship alone; the slice still removes the single point of failure.

**Kill-test KT-RET** (matrix row `gfx906-retrieval-swap-safety-<date>`). Replay 10 min of recorded
embedding traffic at 2 req/s with 64-input batches while forcing 6 Humanizer-triggered swaps;
then park the VII gte lane for 5 min. Pass when: 0 client-visible 5xx (replays allowed and counted);
p99 ≤ 2× baseline during swaps; the CPU twin absorbs the full rate for 5 min with p95 ≤ 2 s; no
Humanizer request waits longer than the grace plus one batch.

**Live impact.** Annotations on two Models (owner paste), a proxy roll. **Done.** Replay, grace,
annotations, dashboard, KT-RET row; jobsearch can move its embeddings back to the GPU lane.

### 6.7 D7 — Efficiency and evidence as product (wave 2)

**Why.** The node agent exports `flexinfer_gpu_temperature_celsius` but no power; the XTX's 294 W
floor and the 2100 MHz cap history are in the matrix but not in metrics; the site advertises a
subset of what the cluster serves and none of the evidence the matrix holds. "66 tok/s at N W on a
7900 XTX, certified" is a claim only this project can make with data behind it.

**Design.** (a) `flexinfer_gpu_power_watts` from `rocm-smi`/sysfs `power1_average` in the node
agent; `flexinfer_lane_energy_joules_total{model}` integrated while a lane is Active;
tokens-per-joule panel per lane. (b) `flexinfer certify render`: generate a JSON certification
matrix from GPUProfile backend-capability certificates, `promotion-evidence` annotations and the
`60-validation-matrix.md` rows with `support_level`, consumed by flexinfer-site as a public
"ROCm consumer GPU serving matrix" page; add ASR, TTS, rerank, video and RAG to the product copy.

**Riskiest assumption.** sysfs power readings on both ROCm 7.2 nodes are stable at 1 Hz without
touching the XTX's SMU (the card has wedged under load before). Read-only, sampled, and off if the
sentinel reports degraded.

---

## 7. Sequencing and dependencies

```
now ──► window (≥ 2026-11-06 00:15Z) ──► after KT-FLEX ──► wave 2
 │            │                             │
 │  !1204 merge (neutral)                   │  D1 repin + S6c moves + KT-SWAP
 │  gitops!1018                             │  D3 canary on the in-pod warm lane + KT-QOS
 │  D2-A/B envelope + descriptor (code)     │  D6 annotations + KT-RET
 │  D2-C jobs CRD + runner (code)           │  D4 certificate canary + KT-SO
 │  D3 proxy classes (code, opt-in)         │
 │  D6-a replay (code), D6-c sizing         └──► D5 (needs D2 + !1207), D7
 │  D1 profiler + pinned tag (code)
 └─ !1203 apply + KT-FLEX, !1207 after controller/runtime roll
```

- Live-neutral code can start now in parallel worktrees: D2 (all three parts), D3 (proxy side), D6-a,
  D1 steps 1 and 4. They touch disjoint files except `internal/proxy/routing.go` and
  `internal/proxy/metrics.go`, which D2, D3 and D6 all edit; land D2-A (the envelope) first and
  rebase the others on it.
- The window belongs to !1203 and KT-FLEX. D1's repin follows KT-FLEX, not the same evening.
- D3's canary wants D1 (an in-pod warm lane it can reload cheaply); without D1 it uses a parked
  5930k twin in a second window.
- D4 needs one canary window and nothing else.
- D5 needs D2's job surface and !1207 applied; D7 needs nothing.

---

## 8. Operating constraints for the implementing agents

Collected from the repo conventions and the operator's recorded lessons. Every brief in §9 inherits
them.

- **Live cluster writes.** Agent sessions cannot `kubectl apply`/`create` Models, GPUProfiles or Pods
  (admission denies them). Hand the owner a paste sequence; read results back with `kubectl get`/
  `logs`; the MCP `flexinfer_update_model` merge-patches `config`/`gpu.priority` after one operator
  paste. Never edit a Flux-owned object by hand without suspending `flexinfer-models` and saying so.
- **Windows.** Anything that rolls the XTX runtime pod, repins a runtime, or changes a lane's
  priority is window-gated with the owner present. The XTX soak verdict (2026-11-06 00:15Z) comes
  first; !1203 and KT-FLEX own that window.
- **Pins.** Digest pins only; any digest a manifest references gets a `pinned-<12>` Harbor tag or
  retention deletes it in 30 days; a digest pulled once into a node cannot be re-pushed from there.
  Never build on an unmerged upstream PR.
- **Decision before data.** Write the matrix row with the pass rule before running the kill-test;
  record the verdict in `.loom/60-validation-matrix.md` and the lane's `promotion-evidence`
  annotation; add a `changelog.d/<slug>.<kind>.md` fragment.
- **Worktrees and branches.** Work under `<repo>/.worktrees/<branch>`; `origin/*` moves under you,
  so merge `master` into the MR branch (no force-push) and check `git diff origin/master --stat`
  shows only your files before merging. One writer per set of files.
- **Builds and tests.** Go: `make test` via devbox (`devbox_exec_async`, waits ≤ 55 s; a fresh
  worktree's first sandbox build takes 15–30 min, start it first). Python runtime pieces (diffusers,
  whisper words) test inside the serving image on the `7900xtx` Docker context, never on the host.
  Contract tests pin the CI image bases (`bec9ad6a4`); extend them when a profile changes.
- **CI and MRs.** GitLab MR CI is not watched by the app: wait with one background `glab` loop,
  decide exits inside Python, quote `$IMG` in zsh (`$IMG:tag` hits the `:r` modifier). Merge via
  REST `auto_merge=true`; a 405 while the MR is preparing is transient.
- **Proxy facts.** The first-byte budget is 120 s by default and per-lane via
  `flexinfer.ai/lane-first-byte-budget`; the breaker probes `/health`; the proxy is off the API
  request path (informer cache, async demand writes). Time lanes at their Services for engine
  numbers and through the proxy for user numbers; use a nonce to defeat the prefix cache.
- **Node quirks.** gfx906 has 24 HWS compute queues (admission 24/3); the 7900xtx exposes its iGPU as
  a second `amd.com/gpu`; 5930k root needs ≥ 200 GB free before any pull; Radeon VII
  `init_user_pages -1` bursts during llama.cpp loads are harmless.
- **Docs.** README states current serving truth; `docs/specs/proxy-api.md`, `crds.md`,
  `labels-and-annotations.md`, `metrics.md` are the contract docs; `.loom/00-index.md` gets a one-line
  entry per plan and result.

---

## 9. Agent briefs (wave 1)

Each brief is self-contained for an Opus 5.5 session that has read this file. Start every session
with `agent_session_start(namespace="flexinfer/<brief-id>")`, allocate a worktree, and end with the
matrix row and a worklog entry.

### Brief D1 — Instant flex card

**Goal.** Serve the XTX warm lane and the four creative lanes in-pod on the stacked 0.32 runtime
(`ef28b7b0`), and publish the measured swap floor with a substage breakdown.

**Read first.** This doc §6.1; R §7e; SG §2, §3, §5b; VS §7, §10; `deploy/system/values-k3s.yaml`
runtime profiles; `internal/runtime/{api,manager,load_progress}.go`; `internal/proxy/queue.go`
(`tryDirectRuntimeLoad`, `DirectLoadPayload`); `controllers/placement_layout_test.go`.

**Deliverables, in MR order.**
1. MR (neutral): `pinned-ef28b7b0` tag request in the ops note, XTX prewarm entry, contract test for
   the gfx1100 GPUProfile `runtime.image`.
2. MR (neutral): `flexinfer-bench swap` profiler: load/unload through the runtime API, per-substage
   timings, decode and greedy hash on the three S3 prompts, JSON + matrix-row output.
3. MR (window): XTX profile repin `cfefae18 → ef28b7b0`, GPUProfile `runtime.image`, with the owner's
   apply note and rollback.
4. MRs (window, one lane each): drop `dedicatedDeployment` on the warm lane, then Qwen-Image, Wan
   T2V, Wan VACE, Stable Audio; MTP wrappers to `spec.config.env`; extend
   `placement_layout_test.go` to the runtime path.
5. KT-SWAP run and matrix row; optional C11 arm measured only.
6. Docs: README serving-path, SG §3.1/§5b closeout, index entry, changelog fragment.

**Kill-test.** KT-SWAP as written in §6.1. The pass rule goes into the matrix row before the run.

**Out of scope.** 5930k runtime un-park; sleep mode; the workhorse itself.

**Report back.** Substage breakdown table, verdict against each pass bar, what dominated the floor,
and whether weights residency (step 5 of §6.1) is worth a follow-up.

### Brief D2 — Scale-to-zero contract

**Goal.** Make every non-Ready state legible to callers and give long work an async home.

**Read first.** This doc §3, §6.2; `internal/proxy/{lane_breaker,stalled_load,queue,routing,
routing_retry,models,usage_log}.go`; `api/v1alpha2/model_types.go` (`ModelStatus`,
`LoadingSubstage`); `docs/specs/proxy-api.md`; the consumer call sites listed in §3 (read them, they
are the acceptance tests); the OpenAI Responses background and Batch guides for field names.

**Deliverables, in MR order.**
1. MR: typed error envelope (`internal/proxy/errors.go`), every proxy-originated non-2xx converted,
   `Retry-After` on all 503s, `X-FlexInfer-Activation`, ETA from substage + histogram. Tests per code.
2. MR: cold capability descriptor on `GET /v1/models/{name}` and the `flexinfer` block on
   `/v1/models`; `spec.capabilities.limits` if the lanes' limits are not derivable from `config`.
3. MR: `InferenceJob` CRD, deepcopy, CRD yaml in the chart, RBAC for the proxy, printer columns.
4. MR: proxy job API (`background: true`, `/v1/jobs*`), runner with per-Model pools, demand
   stamping, object-storage writer (interface with MinIO and PVC implementations; owner picks O2),
   webhooks with Standard Webhooks headers and retry.
5. MR: manager GC and metrics; `flexinfer jobs` CLI verb.
6. KT-JOBS run and matrix row; migrate imagegen-ui video to a job in its repo as the proof.
7. Docs: proxy-api.md, crds.md, labels-and-annotations.md, index, changelog.

**Kill-test.** KT-JOBS as written in §6.2.

**Out of scope.** Resumable chat streams (note the design, do not build); UI; LiteLLM changes.

**Report back.** Whether the election held the card for queued jobs with the existing demand signal
or needed a reservation; the envelope's final code list; which consumers can delete which
workaround.

### Brief D3 — Caller QoS classes

**Goal.** Three QoS classes, resolved at the proxy, enforced in the proxy queue and in vLLM's
scheduler, measured per class.

**Read first.** This doc §6.3; `internal/proxy/{caller_identity,admission,queue,routing,metrics,
usage_log}.go`; `backend/vllm.go:140-160`; vLLM 0.32 OpenAI protocol for the per-request priority
field; `docs/design/multi-tenancy.md`.

**Deliverables, in MR order.**
1. MR (neutral): class resolution, ConfigMap mapping, header, alias suffix; usage-log and metric
   labels; no enforcement yet.
2. MR (neutral): queue ordering by class, per-class in-flight caps, typed `queue_full` with position
   (depends on D2-A's envelope; rebase on it).
3. MR (neutral): vLLM per-request priority set from class when the lane has
   `schedulingPolicy: priority`.
4. KT-QOS on the canary lane; matrix row.
5. MR (window): workhorse `schedulingPolicy: priority`; Mills mapped to `background` in the
   ConfigMap; LiteLLM alias `workhorse-background` in gitops.
6. Docs: proxy-api.md, multi-tenancy.md M1 step 1, index, changelog.

**Kill-test.** KT-QOS as written in §6.3.

**Out of scope.** Quotas, billing, tenant namespaces.

**Report back.** The fcfs-vs-priority decode and TTFT table; whether vLLM preemption paid for itself
on two seats; the final class table.

### Brief D6 — Retrieval plane protection

**Goal.** No client-visible failure on the embedding and rerank lanes across runtime swaps and
Humanizer bursts, and a working twin.

**Read first.** This doc §1.3, §6.6; `internal/proxy/routing_retry.go`; `internal/proxy/
unavailable_reroute.go` (from !1204); `controllers/model_shared_gpu.go`; `internal/runtime/
manager.go` (drain); the gte and CPU-twin manifests; jobsearch `src/daemon/vector/embeddings.py`.

**Deliverables, in MR order.**
1. Reproduction first: a replay harness (recorded embedding traffic, forced swaps) that reports
   client-visible errors today. This decides whether (b) ships.
2. MR (neutral): idempotent replay in the proxy with `X-FlexInfer-Replayed` and metrics.
3. MR (neutral code, annotation in a window): `eviction-grace` in the election, only if step 1
   reproduces kills.
4. MR: gte lane `unavailable-reroute-to` the CPU twin; CPU twin resources sized for 2 req/s; Grafana
   retrieval dashboard in gitops.
5. KT-RET run and matrix row.
6. Docs and a note to jobsearch-app that the GPU lane is safe again.

**Kill-test.** KT-RET as written in §6.6.

**Out of scope.** New embedding models; moving retrieval off the Radeon VII.

**Report back.** Reproduction result; replay rate under swaps; CPU twin capacity curve.

### Brief D4 — Structured outputs certificate (small)

**Goal.** Certify structured outputs on the gfx1100 vLLM lanes and gate tool-call conformance.

**Read first.** This doc §6.4; `api/v1alpha2/gpuprofile_certificate.go`; `backend/vllm.go`;
`internal/agentloop`; `cmd/flexinfer-bench`; vLLM docs `features/structured_outputs.md`.

**Deliverables.** Capability field and flag rendering behind a certificate; gauntlet gate; KT-SO on
a canary lane; certificate set on the gfx1100 GPUProfile with evidence; proxy pass-through and
usage-log tag; docs and changelog.

**Kill-test.** KT-SO as written in §6.4.

**Report back.** Constrained vs unconstrained decode and acceptance; parse-failure taxonomy if any.

---

## 10. Owner decisions

| Id | Decision | Recommendation |
|---|---|---|
| O1 | Approve the wave-1 set D1, D2, D3, D6, D4 and the order in §7 | yes; start the live-neutral code now, keep the window for !1203 |
| O2 | Job output storage: `daemon/minio` (192.168.50.240) or a Longhorn `bulk-*` PVC served by the proxy | MinIO, with a PVC implementation behind the same interface |
| O3 | D3: enable `schedulingPolicy: priority` on the workhorse itself after KT-QOS, or canary only this round | workhorse, in a quiet hour, with Mills mapped to `background` |
| O4 | D6: eviction grace length for the gte lane (how long may a Humanizer burst wait) | 20 s, one batch |
| O5 | D1: keep the 5930k runtime parked through the round | yes; the 900 workhorse stays a dedicated Deployment until the XTX floor is known |
| O6 | D5: realtime protocol, OpenAI `/v1/realtime` subset over WebSocket or SSE | `/v1/realtime` subset, since clients exist |
| O7 | gfx906 Track C: revive vLLM on Vega20 or retire it | retire for this round; llama.cpp serves every VII lane that matters |
| O8 | Retire or rewrite `docs/IMPLEMENTATION_STATUS.md` | rewrite as a one-page pointer to README and this doc's §1 |
