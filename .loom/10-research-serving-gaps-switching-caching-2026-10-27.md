# Review: gfx1100 / gfx906 serving gaps, in-pod model switching, and the cache stack (2026-10-27)

**Question**: After the October program (vLLM 0.32, MTP, segmented attention, the
stacked 0.32 serving image, llama.cpp b11847, the gfx906 queue cap, the proxy overflow
reroute), what is still missing for serving on gfx1100 and gfx906, what keeps the
controller / runtime / proxy from switching models and backends quickly out of one
runtime image per GPU class, and where does the cache stack leak time or disk?

**Verdict (TL;DR)**: The serving work landed; the *switching* and *cache* work did not.
Every text lane on both gfx1100 cards is still a dedicated Deployment, so a lane switch
on the 5930k is pod churn (cold start mean 767 s over 30 days, p50 at or above the
histogram's 300 s ceiling) while the Radeon VII runtime swaps llama.cpp lanes in 3.5 to
9 s. The stacked 0.32 image that would fix this (`ef28b7b0`, S6a) is built but pinned
nowhere, the 5930k runtime profile is parked by a hand-patched node selector that git
does not carry, that profile lacks the multi-model and drain flags, and the runtime path
has no spec-change reload. On caches, the two marker fixes (!1137, !1138) are open and
mergeable, nothing evicts Local weights or compile caches, and several pins drifted
(the XTX 9B lane runs a Harbor-deleted digest; the two Qwen3.8 lanes run different
images). The ranked plan is in §8: merge the marker MRs, land a pin-hygiene MR, then
S6b with three controller changes done first so the runtime path can carry the
workhorse without regressing what the Deployment path already does.

Branch: `claude/gfx-inference-serving-gaps-9dc401`. Live numbers from Prometheus, the
flexinfer MCP and `kubectl` on 2026-10-27 ~18:45Z. Code references are to this
worktree at master `d9e5ada1e`.

---

## 1. What landed since 2026-10-10 (132 non-merge commits)

| Area | Shipped | Evidence |
|---|---|---|
| gfx1100 vLLM currency | 0.23 → 0.26 → **0.32** on the native-W4A16 profile; wheel-mode image, ISA asserted at build; plugins ported | S1 PASS, `b06b06fe` CI-built |
| MTP on Qwen3.8-27B | INT4 MTP head drafts at 0.80–0.82 pos-0 acceptance with `FLEXINFER_QWEN35_MTP_HEAD=checkpoint` | S2 PASS |
| Segmented attention | `ROCM_SEGMENTED_ATTN` + fp16 KV + MTP K=2 at 32K: decode +93–101 % at 16–28K; the "TTFT floor" was first-use Triton JIT | S3 / S3b |
| **Workhorse promotion (S4)** | 5930k workhorse on B; compile cache seeded; every gate passed | 24 h watch below |
| Proxy context overflow | over-length prompts replay to the XTX 64K lane under the target's budget and breaker; XTX budget 480 s | !1177, !1182/!1183; 3 reroutes in 7 d |
| Proxy request path | API calls off the request path, informer cache, direct targets follow port moves every 10 s; CPU limit 2000m | !1161/!1162, !1151 |
| Controller | per-Model `env` + vLLM `kernelConfig`; staged-cache readiness across config-only edits; prefetch bookkeeping; reconcile hot loop 8.6/s → 0.9/s; parked lanes at 30 s; digest pins `IfNotPresent`; GPU health sentinel; llama.cpp `draft-mtp` behind a GPUProfile certificate; `--flash-attn off` rendering; draft-window spelling per llama.cpp build | !1122–!1130, !1172/!1173, 37317c95 |
| Stacked 0.32 runtime image | `gfx1100-serving-v032` (0.32 + supervisor + diffusers 0.42 + snac), spawn contract verified at build, CI digest `ef28b7b0` | S6a PASS offline |
| gfx1100 llama.cpp | b11847 on ROCm 7.2.3 for the Glimmer lane; draft-mtp certified; Vulkan measured and rejected | matrix row `gfx1100-llamacpp-b11847-2026-10-24` |
| gfx906 | `GPU_MAX_HW_QUEUES=1` (24-queue runlist); Qwen3-Embedding-4B + Reranker-0.6B lanes; runtime repinned to the CI build `90648c35`; flash-attn A/B | !1140, !1146, !1118/!1119 |
| Fleet hygiene | Harbor `pinned-*` guard; prewarm lists repinned; 84 GiB + 3.4 GB of orphan caches removed by hand | gitops !971 |

**S4 24 h watch (the open follow-up) is passing.** Workhorse over the last 24 h:
2,208 requests, e2e p50 **4.4 s** (pre-S4 8.25 s), p95 26.4 s (29.4), TTFT p95 8.5 s,
mean acceptance length 2.80, zero preemptions, 403 generated tokens per request. The
XTX lane served 9 requests, p95 372 s, which is the overflow replays doing their job.

## 2. Live state (2026-10-27)

| Node | Runtime DaemonSet | Ready lanes | Idle / parked lanes | Prewarm holds |
|---|---|---|---|---|
| cblevins-5930k (gfx1100) | `gfx1100-5930k` **desired=0** on `cfefae18` (vLLM 0.17 persona) | workhorse (dedicated, `b06b06fe`) | 7 Preempted text lanes (all dedicated), stable-audio, 2 Wan | `ad93ae72`, `cd108c9e`, `b06b06fe`, vllm ×2, diffusers tag, diffusers-video |
| cblevins-7900xtx (gfx1100) | `gfx1100` on `cfefae18`, multi-model, **no drain**, 6 restarts (all 10-23/24 incidents) | xtx-warm-canary (dedicated, `cd108c9e`) | 9B RP (`2e9652ed`), 35B ×2, whisper, orpheus, snac | `2e9652ed`, `cfefae18`, `ad93ae72`, `cd108c9e`, vllm ×2, diffusers tag |
| cblevins-radeonvii (gfx906) | `gfx906` on `90648c35`, multi-model + drain | bge-large, bge-reranker, gte-qwen2, orpheus (all in-pod) | humanizer, gemma4-e4b, qwen3-embed/rerank, openjev | `8181b690` (retired overlay), `90648c35` |

Switch cost today, measured:

| Path | Metric | Value |
|---|---|---|
| Radeon VII in-pod (llama.cpp) | `flexinfer_runtime_model_swap_duration_seconds` p50 / p95, 30 d | 3.5–5.6 s / 9.2 s (16 swaps) |
| 5930k dedicated Deployment (27B vLLM) | `flexinfer_model_cold_start_duration_seconds` mean, 30 d | **767 s**; p50 and p90 both read 300 s because the top bucket is 300 s (`pkg/metrics/exporter.go:267`) |
| XTX dedicated Deployment | same, 30 d | mean 228 s, p50 180 s |
| 5930k lane switches in 7 d | `flexinfer_model_transitions_total` | workhorse Preempted→Loading/Ready 6 times (every canary window took the card) |

## 3. gfx1100 serving gaps

1. **No runtime-managed text lane on either gfx1100 card.** The XTX runtime pod hosts
   only snac; whisper is parked. All 11 5930k lanes and 5 XTX lanes are dedicated
   Deployments (`pkg/runtime/payload.go:110-141` eligibility; 4 of the 5930k lanes have
   no `dedicatedDeployment` opt-out and would silently move onto the 0.17 runtime image
   if the DaemonSet were ever un-parked on today's pin). The owner direction of
   2026-10-24 (one stacked image per GPU class, swaps inside the runtime pod) is S6b–S6e,
   none started.
2. **The 5930k runtime parking is not in git.** The live DaemonSet carries
   `flexinfer.ai/quantization-pause=false` in its nodeSelector; `values-k3s.yaml:758`
   only mentions it in a comment and !997 (open since 2026-08-24) is the stale attempt to
   codify it. Helm's 3-way merge preserves the hand patch, but a DaemonSet delete or a
   template change that touches `nodeSelector` would un-park it onto vLLM 0.17.
3. **S6a's image is unprotected.** `ef28b7b0` is in no prewarm list and no manifest, so
   the `pinned-*` guard (which tags only `deploy/` and `charts/` pins) does not cover it;
   Harbor retention (latest 30 pushed or pulled in 30 d) can delete it before S6b.
4. **The two Qwen3.8 lanes run different engines.** Workhorse `b06b06fe` (CI, text plugin
   ≥ 0.9.0), XTX `cd108c9e` (off-CI, older plugin, MTP must not run on it). The overflow
   route therefore changes engine build mid-request-class. `b06b06fe` is not in the XTX
   prewarm list; `ad93ae72` is still in the 5930k list although nothing uses it.
5. **9B RP primary pins a Harbor-deleted digest.** `qwen35-9b-ablit-rp` (XTX) still pins
   `2e9652ed`, alive only as the node snapshot the XTX prewarm holds; its 5930k twin runs
   `b06b06fe`. The drift gate has failed on this since 10-23.
6. **Capability claims in the gfx1100 GPUProfile are stale**: piecewise graphs and fp8 KV
   "experimental", "production target 0.19.x", eager default (`gfx1100.yaml:88-98`),
   while production runs graph mode, fp8/fp16 KV and MTP on 0.32. `maxGPUMemoryGB: 22`
   sits beside lanes estimating 23,200 MB.
7. **Fable decodes 13 % slower on 0.32 in its eager envelope** (7.6 vs 8.7 tok/s); graph
   mode is an unqualified separate slice.
8. **No gfx1100 embeddings / rerank / ASR lane.** Whisper is "un-servable on this card"
   per its manifest; the retrieval plane is gfx906-only (single point of failure for RAG).
9. **Vulkan is rejected** (2.7× slower TG in the measured image, no Mesa in the runtime
   image); the AMD-Ecosystem gfx11 ROCm nightlies were not tried.
10. Platform: XTX capped at 2100 MHz, BIOS 1811 open, kernel 7.0 above ROCm's listed
    ceiling; 5930k root cannot hold Longhorn replicas + caches + 300 GB of images.

## 4. gfx906 serving gaps

1. **vLLM is three different images and none is the certificate.** GPUProfile `2f3fe306`
   (vLLM 0.6.3, V0), Helm `vllm.gfx906Image` `2139c92b`, the openjev lane `e0aa1e2e`,
   certificate `034f0818`. Track C (revive or retire) from May was never closed; the
   profile comment still says the runtime is "paused (Track B)".
2. **llama.cpp certificates are on b8173; every lane runs b11335 in-pod.** The
   `stateful-slots` and `ngram-simple` certificates only apply to dedicated Deployments
   (`controllers/model_gpu.go:209-210`), so no runtime-managed lane can use speculation;
   there is no gfx906 `draft-mtp` certificate.
3. **The 24-queue budget is enforced by convention, not by the controller.** VRAM
   admission (`vramEstimateMB`) does not see HWS queues; every additive tenant (pyannote,
   Jobs, dedicated pods) still risks oversubscription. The count is only readable via
   `/sys/kernel/debug/kfd/mqds` on the host.
4. **Prewarm still holds the retired Humanizer overlay `8181b690`** (its
   `llama-embedding` cannot start). Nothing references it; it costs disk on a 127 G root.
5. **Reranker pairs over 1024 tokens return 400**; the 4B reranker does not fit beside the
   4B embedder; EmbeddingGemma-2 is still "conditional"; pyannote is a hand-written
   Deployment outside the Model API.
6. `HSA_OVERRIDE_GFX_VERSION` guidance disagrees across the GPUProfile, the unified
   Dockerfile, the gfx906 doc and the platform slice doc.

## 5. Switching: the two paths, and what blocks "one image, swap in pod"

### 5a. How a switch works today

- **Decision**: the controller's shared-group election (`controllers/model_shared_gpu.go:574-755`);
  demand comes only from the proxy's `lastActiveTime` patch or the activation
  annotation. The runtime ingress's own `activeRequests` / `lastRequestTime` is reported
  but read by nothing.
- **Runtime path**: unload = `DELETE /api/v1/models/A` (drain only where
  `FLEXINFER_RUNTIME_REQUEST_DRAIN` is set, i.e. gfx906 only), SIGTERM the process
  group, reap; load = `POST /api/v1/models/B/load` with VRAM admission (free ≥ estimate +
  1 GiB), 2 s health polls, backend startup timeout. Multi-model mode makes unload and
  load two independent reconciles; admission retries every 5 s are the only ordering.
- **Deployment path**: replicas 0 (no preStop, no drain) then replicas 1, `maxSurge 0`,
  image pull `IfNotPresent`, optional flash-loader init, then the KV-pool recheck relaunch
  for vLLM. Compile caches persist on the same hostPath for both paths
  (`<hostPath>/<ns>/<model>`).
- **Cross-backend in one pod** is only possible where the persona bundles both backends:
  vllm↔snac↔diffusers on the XTX, llamacpp↔diffusers↔ollama on the Radeon VII. vLLM↔
  llama.cpp on one card always means a Deployment (`gfx1100-serving` is built
  `llamacpp: false`; gfx906 bundles no vLLM).

### 5b. Blockers and defects in the runtime path (controller / runtime / proxy)

| # | Gap | Where | Why it matters for S6b |
|---|---|---|---|
| R1 | **No reload on spec change.** `Load` is idempotent on name + backend + state only; the Ready branch never compares generation | `internal/runtime/manager.go:465-476`, `controllers/model_runtime.go:272-301` | A `kernelConfig`, `env`, `speculativeConfig` or LoRA edit to a runtime-served lane applies only after a manual unload. The Deployment path rolls on every template change. |
| R2 | **5930k runtime profile lacks `FLEXINFER_RUNTIME_MULTI_MODEL` and `…_REQUEST_DRAIN`**; the XTX profile lacks drain | `deploy/system/values-k3s.yaml:777-795`, `:736` | Single-slot mode blocks inside the HTTP handler for unload + drain (up to 42 s) against 30 s client timeouts; no drain means SIGTERM mid-request. |
| R3 | **Command wrappers are dropped silently.** The load payload carries no command; `bash -lc 'export …'` wrappers (the two MTP lanes) would load the INT4 MTP head with the plain policy | `pkg/runtime/payload.go:18-26`; research §7e S6c | Silent-garbage mode. Must move to `spec.config.env` before any MTP lane goes runtime-managed. |
| R4 | **Runtime path has no KV-pool recheck and no loading substage.** Substage is always `Initializing`; the proxy's stalled-load fail-fast only fires on `LoadingWeights` | `controllers/model_helpers.go:472-489`, `model_runtime.go:451-457`, `internal/proxy/stalled_load.go:155-179` | The cold-compile short-pool relaunch that saved S1/S2/S4 boots does not exist in-pod; a hung load is only caught by the startup timeout. |
| R5 | **Spec image / digest ignored on the runtime path**; `GPUProfile.spec.runtime.image` is documentation only (#72 undecided) | `payload.go`, `gpuprofile_types.go:90-93` | Per-lane engine pins cannot coexist with the runtime path; every runtime-served lane runs the DaemonSet digest. Needs a decision, not more machinery. |
| R6 | **Proxy and controller build different load payloads** (proxy omits LoRA options, polls with the proxy-wide cold-start timeout) | `internal/proxy/queue.go:451-535`, `:584` | A proxy-won race loads a lane without its adapters; idempotency then makes the controller's correct load a no-op. |
| R7 | **No weights-in-RAM reuse on the runtime path.** No flash-loader, no page-cache management; the 27B lanes disable flash-loader anyway (5930k tmpfs ~7.7 GB) | `controllers/model_deployment.go:432-512` | Each in-pod swap re-reads ~19 GB from NVMe; fine for a few lanes, but the "swap in seconds" promise depends on kernel page cache the 48 Gi pod memory limit can evict. |
| R8 | **Sleep mode is passthrough only**; no runtime routes, kill-test crashed on ROCm (#68, deferred) | `internal/runtime/api.go:33-44` | Not a blocker; recorded so it is not re-proposed until vLLM #60367 is in the fleet image. |
| R9 | **Metrics split and capped.** Controller swap buckets stop at 120 s, cold-start at 300 s; `flexinfer_model_load_seconds` is really activation-to-Ready; runtime swap histogram exists but its Grafana dashboard is off | `pkg/metrics/exporter.go:267, 277`, `values-k3s.yaml:845` | S6b's acceptance ("swap time against pod churn") cannot be read from today's histograms. |
| R10 | **Idle lanes flap Idle→Pending→Idle ~twice a day.** `!cacheReady` writes Pending whenever a cache re-check Job is in flight; 11–16 transitions per Idle lane per week | `controllers/model_controller.go:437-441`; transitions metric | Noise in transitions, events and any consumer keyed on phase. |
| R11 | **Event noise**: `ExperimentalGPUSupport` has no cooldown (2,615 events in 21 h per lane) while `VRAMPressure` has one | `controllers/model_gpu.go:139` | Events are the swap forensics tool; the noise buries them. |
| R12 | `MaxConcurrentReconciles` is unset (single worker): one self-looping Model starves the rest | index 2026-10-23 follow-up | Latent; surfaced in the 10-23 hot loop. |

### 5c. What the Deployment path still does better

KV-pool recheck, image pins per lane, ephemeral-storage requests, priority class,
flash-loader, readiness-first drain on proxy rolls, and a loading substage. S6b should
not trade these away; R1, R2 and R4 are the three to close before the workhorse moves.

## 6. Cache stack: gaps that cost time or disk

| # | Layer | Gap | Where |
|---|---|---|---|
| C1 | Local HF marker | Marker = sha256(`spec.source`) only; a ggufFile / revision / pattern change re-runs the Job, which finds the old marker and skips the download. **Fix is !1137, open and mergeable since 10-24.** | `controllers/model_cache_jobs_local.go:227-291` |
| C2 | SharedPVC HF prefetch | Marker is a bare `.flexinfer_cached`, Job has no TTL, recreated only on a `spec.source` change. **Fix is !1138 (depends on !1137).** | `model_cache_jobs.go:84, 113` |
| C3 | "Ready" ≠ staged | Memory, None, Local-without-nodeSelector and SharedPVC-non-HF report `cache.ready=true` with no content check; the MCP shows them warm | `model_cache.go:614-620, 746-756` |
| C4 | No eviction, no cleanup | Local weights, compile caches, `/dev/shm/flexinfer` flash dirs and the runtime's shared compile dir are never reclaimed; `cleanupModel` removes only Deployment, Service and tmpfs; `idleTTL`/`PrefetchEvictable` are unused in `deploy/`; the v1alpha1 LRU is inert (size only set by quantization, access time only on Ready) | `model_helpers.go:258-282`, `modelcache_eviction.go` |
| C5 | Per-lane copies | Weights and compile caches keyed `<ns>/<model>`: every canary/probe gets a full copy (the 84 GiB orphan sweep). Compile-cache contents are keyed by GPU + toolchain + shapes, so a shared per-(image, GPU) dir would serve every lane; today seeding is a manual `cp -a` | research §7f step 1 |
| C6 | OCI caches | Two copies per node (local-path PVC + Local stage); a re-pull does not invalidate the Local copy; partial pulls count as cached; `ociPollInterval` would exec `oras` in a controller image that has none | `modelcache_shared_pvc.go:1065-1146` |
| C7 | Image cache | Prewarm pods have no priority class or ephemeral request (evicted under DiskPressure 10-24, then GC'd `2e9652ed`); `library/llamacpp@…` pins are outside the pin guard; `releaseGate` unset so tags are allowed; the Wan lanes pull a floating tag with `Always` while prewarm holds a digest the manifests do not use | `values-k3s.yaml:365-523` |
| C8 | Disk safety | Nothing checks free space before staging; node-disk alerts are opt-in and off; the scheduler's `cache` weight scores KV usage, not weight residency | `charts/flexinfer/values.yaml:612-620`, `scheduler/scheduler.go:459-522` |
| C9 | Observability | Cold-start histogram caps at 300 s; no label separates image / weights / compile cold vs warm; cache size unknown for Memory/None; CLI node-local TOTAL sums across nodes | `exporter.go:263-279` |
| C10 | Config drift inside the cache | Dedicated pods rewrite `config.json` in the cache dir in place; neither marker nor `stagedKey` sees it (runtime manager mode skips it) | `build/runtime-entrypoint.sh:256-289` |
| C11 | Prefix / KV | Lost on every swap on both paths; llama.cpp `slotSavePath` exists but no production lane uses it (and it must live under `/models`, which is the tmpfs when flash-loader is on) | `backend/llamacpp.go:179-184` |

## 7. Drift and hygiene (fix in one manifest MR)

- XTX: `qwen35-9b-ablit-rp` → `b06b06fe` (drop `calculateKvScales`; its twin's manifest
  is the template); `qwen38-27b-xtx-warm-canary` → `b06b06fe`; add `b06b06fe` to the XTX
  prewarm list; then drop `2e9652ed` from the XTX list and `ad93ae72` from the 5930k
  list once nothing references them.
- Pin `ef28b7b0` (S6a) in the 5930k prewarm list now, so the pin guard tags it.
- gfx906 prewarm: drop `8181b690`.
- Wan lanes: make `imageDigest` and the prewarm entry agree (`f03bfc2f` / `52ebc2b9`
  vs `83cda206`).
- `qwen36-35b-mtp-uncensored-5930k` pins a mutable tag; mtp-32k's `f467e202` is in no
  prewarm list.
- gfx906 GPUProfile: pick one vLLM image or mark vLLM unsupported (Track C); drop the
  "runtime paused" comment; both profiles still declare retired mlc-llm.
- gfx1100 GPUProfile: refresh the capability block (§3.6).
- Stale comments listed by the lane sweep: `kustomization.yaml:48/108/111/114`,
  `qwen35-35b-clean-mtp-32k.yaml:121-123`, `muse-glimmer-30b-tools.yaml:41-45`,
  `qwen35-9b-ablit-rp-5930k.yaml:12-14/28-30/105-110`, the openjev pair.
- Close or refresh !997 (superseded; the selector it codifies is still live-only).

## 8. Recommended sequence

| # | Slice | Content | Kill-test / acceptance |
|---|---|---|---|
| 1 | **Merge !1137 then !1138** (controller roll) | Option-aware cache markers with legacy adoption | On `muse-glimmer-30b-tools`: no stage Job, no re-download after the roll; next generation bump logs "Adopted legacy cache marker" |
| 2 | **Pin-hygiene MR** (§7) | Manifests + prewarm lists only | Drift gate green on master; XTX 9B lane boots `b06b06fe` (probe 3/3, pool ≥ 3x at 224K) |
| 3 | **Controller pre-work for S6b** (R1, R2, R4, R11, R9) | (a) runtime path compares `observedGeneration` and reloads on spec change (unload+load under `opMu`, respecting drain); (b) `FLEXINFER_RUNTIME_MULTI_MODEL=1` + `…_REQUEST_DRAIN=1` on both gfx1100 profiles; (c) runtime loads report a substage from the health loop and get the KV-pool recheck via the runtime's log tail or vLLM `/metrics`; (d) cooldown on `ExperimentalGPUSupport`; (e) cold-start buckets to 1800 s, swap to 900 s, plus a `cause` label (image/weights/compile) | Unit tests per piece; on the Radeon VII a `spec.config` edit to a Ready runtime lane reloads it within one reconcile and drain holds in-flight requests |
| 4 | **S6b** (GPU window, operator applies) | Repin `gfx1100-5930k` to `ef28b7b0` (prewarmed by #2), codify the park/un-park selector in git, un-park, let the workhorse move in-pod; MTP lanes switch wrappers to `spec.config.env` first (S6c) | Greedy hashes identical to the dedicated pod; decode within 3 % at 512/2K/16K; workhorse↔9B-RP swap time from `flexinfer_runtime_model_swap_duration_seconds` recorded against today's pod churn |
| 5 | **Shared compile cache** (C5) | One per-(image digest, GPU arch) compile dir for the runtime pod and the dedicated lanes; seed from the S4 cache; keep per-lane dirs only for canaries | A fresh lane on the same image boots with `autotune missing=0` and no `jit_monitor` storm |
| 6 | **Cache reclaim** (C4, C8) | `idleTTL` on the demand-only lanes; a controller sweep that deletes Local dirs and compile dirs for Models that no longer exist; free-space check before staging; enable the node-disk alerts for the serving nodes | 5930k root stays under the 60 % GC line through a canary window without a manual `rm` |
| 7 | gfx906 decisions | Track C (vLLM revive or retire, one image), re-certify llama.cpp speculation on b11335 (or b11847) for the runtime path, consider a `queueEstimate` admission field for the 24-queue budget | Profile shows one vLLM image or `unsupported`; a runtime-managed lane may use ngram-simple |
| 8 | gfx1100 retrieval redundancy | A gfx1100 embeddings + rerank pair (Qwen3-Embedding-4B / Reranker-0.6B GGUF on the Glimmer image, or vLLM pooling on 0.32) as a cold additive lane | RAG survives a Radeon VII outage |

Not recommended now: sleep mode (R8) until vLLM #60367 is in the fleet image and the
device-plugin deadlock is understood; Vulkan; any new gfx906 vLLM build before Track C
is decided.

## 9. Sources

- Code sweeps (this session, 2026-10-27): controllers/, internal/runtime/, internal/proxy/,
  pkg/runtime/payload.go, backend/, charts/flexinfer/templates/, deploy/system/values-k3s.yaml,
  deploy/gpuprofiles/, deploy/models/, build/runtime.yaml.
- Live: `flexinfer_list_models`, `flexinfer_cache_status`, `flexinfer_cache_capacity`,
  `kubectl get ds/pods/events -n flexinfer-system`, Prometheus
  (`flexinfer_model_*`, `flexinfer_runtime_*`, `flexinfer_proxy_*`, `vllm:*`,
  `kube_pod_container_status_restarts_total`).
- Program docs: `.loom/10-research-gfx1100-next-feature-2026-10-23.md` (§7a–§7f),
  `.loom/20-product-spec-runtime-image-ci-2026-09-13.md`, `.loom/00-index.md` Current Goal,
  `.loom/60-validation-matrix.md` rows dated 2026-10-2x, `docs/dev/gfx906-gpu-support.md`.
- GitLab: issues #66, #68, #69, #71, #72; MRs !997, !1087, !1137, !1138, !1165.
