# Research: Putting the idle flexinfer GPUs to work for loom-core and Mills (2026-09-13)

**Question**: How do we get the self-hosted flexinfer models doing more work, and useful
work, inside loom-core and the Mills operator? Second question, same session: which of the
newest open-weight models can be self-hosted on the 24 GB 7900 XTX (gfx1100) nodes?

**Verdict (TL;DR)**: The cards are idle because of *availability*, not model quality. The
two gfx1100 text lanes run scale-to-zero; every recorded cold start of the 27B workhorse
exceeded five minutes; so loom-core and Mills only send latency-tolerant side work to local
models and the vendor tiers took every inline stage. Fix availability first (slice 1, this
MR: keep the 5930k workhorse warm across Mills' inter-call gaps, retire two dead XTX lanes,
re-arm ModelBackfill), then flip the safe Mills stages by env (slice 2), then evaluate new
models behind a vLLM 0.23 → 0.28+ runtime bump (slice 3). A warm text lane *beside* the XTX
voice pod turned out to be structurally blocked (§7); that needs an operator decision.

Branch: `claude/flexinfer-utilization-loom-mills-e4c967`. Live numbers pulled 2026-09-13
~06:30–08:30Z from Prometheus (`flexinfer_*`, `vllm:*`, `litellm_*`, `mills_*`, `amdgpu_*`),
Loki, kubectl, and two read-only code surveys of loom-core.

---

## 1. What the numbers say

### 1a. Cards

| Card | GPU busy (30d avg) | VRAM in use | What is on it |
|---|---|---|---|
| cblevins-7900xtx | **0.0015%** | 3.2 / 24 GB | orpheus-3b-7900xtx warm-pinned, **0 requests in 30d**; snac/whisper/wan/stable-audio lanes 0; two gemma4 lanes Pending since 07-11 |
| cblevins-5930k | 1.17% | 25.5 GB when Ready | qwen38-27b-autoround-workhorse (scale-to-zero, idleTimeout 30m) + parked 27B/35B/9B/glimmer/video lanes |
| cblevins-radeonvii | 8.0% | 6.5 GB | embeddings + reranker + orpheus availability tier; gte-qwen2 **688K requests / 7d** (agent-context + codebase-memory) — the only busy card |

### 1b. The workhorse lane

| Signal | Value |
|---|---|
| Requests, 7d / 30d | 202 / ~253 |
| Generated tokens, 7d | ~80K (≈ 40 min of single-stream decode) |
| Hours with any traffic, last 168h | 2 |
| Phase at 4h samples, 09-06 → 09-13 | Idle 15, Ready 2 |
| Cold starts recorded (`flexinfer_model_cold_start_duration_seconds`) | **12 of 12 in the > 300 s bucket** |

The 5930k policy comment in gitops says Mills' proxy retries "cover" a 3–5 min cold start.
They do for council and audit calls. They are exactly why the judge, weaver, and every
pipeline stage were moved to vendor models: nothing inline can wait five minutes.

### 1c. Mills' model split (`mills_llm_prompt_tokens_total`, 30d)

| Component | Model | Prompt tokens |
|---|---|---|
| mills-judge | gpt-5.6-luna (LiteLLM → OpenAI) | 295.8K |
| mills-weaver (research stage) | kimi-k3 (LiteLLM → OpenRouter) | 197.2K |
| mills-flexinfer (audit pool ×2, spinning ring/open-end, merged-work) | qwen38 **local** | 102.0K |
| mills-judge tiebreaker | claude-sonnet-5 (Anthropic) | 58.3K |
| mills-council-reviewer security + tech-debt | qwen38 **local** | 40.5K |
| mills-council-editor | claude-fable-5-1 | 9.0K |
| mills-flexinfer (fallback hops) | gpt-5.6-luna | 7.6K |
| council user-impact / frontier lenses | deepseek-chat / kimi-k3 | 4.8K / 4.5K |
| mills-eval-judge | gpt-5.6-luna | 1.3K |

Local already carries ~20% of Mills' prompt tokens, all in out-of-band roles where a
five-minute cold start is tolerable.

### 1d. Where Mills' money and failures go (30d)

| Signal | Value |
|---|---|
| Pipeline spend | $111.2 — $107.5 on runs that ended *escalated*, $3.7 on runs that merged |
| Pipeline runs / autonomous merges | ~37 / 118 (7d: 19, 1d: 4) |
| Stage attempts: tests | 25 success / **69 error** |
| Stage attempts: ci_watch / implement / plan_slice / research | 2/11, 59/3, 49/1, 48/0 |
| Escalation classes | code 30, infra 12, config 11 |
| Judge calibration discrimination (all gates) | 0.148 over 331 graded runs (per-gate: pr_self_review 0.006, spec_conformance 0.117 — brainstorm 09-12) |

The 09-12 kill-test in loom-core showed 91% of tests-stage failures were sandbox substrate.
Local models cannot fix that yield problem; this program rides *behind* the C1
executable-verification plan (`services/loom-core/.loom/200-plan-executable-verification-2026-09-12.md`),
not against it.

## 2. Where local inference sits today (loom-core + Mills)

Everything routes through one client shape (`pkg/flexinfer.Client`, `pkg/mills/clients`)
and is local-or-vendor purely by `{base URL, model id}`. In production the HUD/weaver
`FLEXINFER_URL` is the **LiteLLM gateway**, which fronts local lanes under bare names and
vendors under `or/`, `oa/` prefixes (`platform/gitops/k3s/ai/litellm/`).

| Consumer | Backing today | Notes |
|---|---|---|
| Embeddings (codebase-memory, agent-context, pm, Mills merged-work) | local: gte-qwen2-1.5b / bge-large on radeonvii | the one busy plane |
| agent-context recall reranker | **off** (`WEAVER_RERANKER=off`, no manifest sets it) | bge-reranker lane is Ready; free win |
| HUD coordinator summarize/compress/triage/extract/plan + weaver router | wired, default-on, **6 proxy requests in 30d** | gateway URL with no key → client falls back to keyless proxy → target gemma4 alias on a scale-to-zero lane; effectively dormant |
| Mills council lenses ×2, audit pool ×2, spinning ring/open-end | local qwen38 (5930k) | policy `configmap-policy.yaml:66-67,442-445,796-797` |
| Mills rubric judge + council contradiction judge + overseer triage + foreman summaries | gpt-5.6-luna via LiteLLM (+ claude-sonnet-5 dissent tiebreaker) | moved OFF local 2026-07-25 after the gatekeeper bake-off proved the cheap fallback a rubber stamp (`k3s/mills/deployment.yaml:163-173`) |
| Mills research/weaver stage | kimi-k3 via LiteLLM | July's 44% research error rate was cold-start 503s, not quality |
| Mills item-memory consolidation | inert (`LOOM_MILLS_MEMORY_CONSOLIDATE` unset); empty `FLEXINFER_MEMORY_MODEL` silently inherits kimi | |
| Mills implement / plan_slice / pr_self_review | codex gpt-5.6-terra/sol, claude-code opus-5 (vendor CLIs) | closed harness vocabulary; no local runtime |
| morph-fast-apply MCP | vendor direct (api.morphllm.com) | LiteLLM already registers the route |
| All failure/incident classifiers | deterministic regex | no LLM |

## 3. Why the local lanes are idle

1. **Scale-to-zero + 5-minute cold starts** on the only lane strong enough for Mills (§1b).
2. **The XTX is reserved as the voice card**, ratified 2026-08-19/08-30, and the voice lane
   has served zero requests in 30 days. Structurally (§7) nothing else can share that card
   with the dedicated orpheus pod today.
3. **Every inline Mills stage needs sub-minute latency**; the judge/weaver flips in July were
   correct given (1). Fixing (1) reopens them.

## 4. Dormant or broken inventory found on the way

- Both `ModelBackfill` declarations terminal **Failed**: `model-eval-gemma4-5930k` on 08-12
  (attempt 24; target retired to scale-to-zero on 06-17 and superseded on the card by the 27B on
  08-16; inherited the CronJob's default MODELS list that names the Pending XTX lane),
  `model-eval-qwen3-radeonvii` on 09-03 (attempt 57; target Ready; Job logs expired under the
  24h TTL). Failed phases only reset on a spec generation change. **Post-merge (07:35Z): the
  re-armed radeonvii declaration ran attempt 59 immediately and PASSED 3/3 gauntlet checks
  (`GAUNTLET OK`, phase Succeeded, next run 2026-09-14T07:36Z)** — the primitive works; the
  operational gap is that one transient failure parks a declaration forever. Follow-up: a bounded
  retry (or an alert on `phase=Failed`) in the ModelBackfill controller.
- `gemma4-26b-a4b-gptq` + `-apc-canary` on the XTX: Pending since 07-11. Their ModelCache PVC
  `pvc-444d505e…` (Longhorn `nvme-1r-gpu`, single replica on the XTX) faulted 2026-09-05T21:31Z;
  the replica data directory is gone from `/mnt/nvme/longhorn/replicas/`; auto-salvage is on and
  could not help. Cache-copy pods sat in ContainerCreating for 8 days. Unrecoverable → retire.
- LiteLLM noise, 7d: three `storyboard-generator` pods poll `/v1/models` unauthenticated every
  ~3 s (**169K 401s**); `gemini-3-flash-preview` 404s ×472; `text-embedding-3-small`
  UnsupportedParams ×130 (its alias points at a 1024-dim bge lane); `oa/gpt-5.6-sol` 400 ×10.
- `loom-mills-squads` ConfigMap names `llama-4-70b-instruct` / `qwen-3-32b`, which do not exist
  (squads are parsed but unenforced — harmless, misleading).
- Docs drift: `services/loom-core/docs/MILLS.md:837` and `docs/weaver.md:33` still say the
  judge is local.

## 5. Program

### Slice 1 — a lane that is actually there (this MR)

1. `deploy/models/qwen38-27b-autoround-workhorse.yaml`: `idleTimeout` 30m → **12h**. Keeps
   the 5930k card warm across Mills' gaps (council cron every 6h, pipelines, audit sweeps)
   while `warmPolicy: ondemand` still lets wan21 VACE (600) / T2V (500) / glimmer (500)
   demand-swap once the lane is idle > 2 min. `minReplicas: 1` was rejected: a warm-pinned
   leader never idles and would block video demand (whisper / !563 lesson).
2. Retire the two XTX gemma4 lanes (kustomization; manifests kept on disk). Drop the retired
   lane from the weekly gauntlet's default MODELS.
3. ModelBackfill: new `model-eval-qwen38-5930k` targeting the workhorse with an explicit
   single-model gauntlet override; `model-eval-qwen3-radeonvii` re-armed by a spec touch so
   the next run's logs can be read within 24h.
4. Prepared, not applied: `deploy/debug/qwen38-27b-xtx-warm-canary.yaml` (§7).

### Slice 2 — flip the safe Mills stages (gitops env, after slice 1 soaks)

| Change | Where | Why it is safe |
|---|---|---|
| research/weaver → local | `MILLS_WEAVER_BACKEND=flexinfer`, `FLEXINFER_WEAVER_MODEL=qwen38-27b-autoround-workhorse` (`k3s/mills/deployment.yaml:161,176`) | 0 stage errors in 14d, p50 1 min; 197K tokens/mo off kimi; keep `FLEXINFER_WEAVER_MAX_TOKENS ≥ 4096` |
| overseer triage + foreman summaries → local | judge-client override in `cmd/loom-mills-operator/main.go:1279-1286` (needs a separate model knob; code change) | ≤ 512-token JSON verdicts, fail-safe on unavailable |
| item-memory consolidation → local | `FLEXINFER_MEMORY_MODEL` | today silently inherits kimi |
| council user-impact lens, spinning `mule` → local | `configmap-policy.yaml:68,795` | siblings already local |
| **Shadow judge**, not a flip | new non-blocking annotation on `post_review_gate` exporting its own `mills_judge_calibration_*` series | S4 in the loom-core plan is waiting on a fortnight of discrimination measurement; the idle GPU produces it for free |
| Enable agent-context reranker | `WEAVER_RERANKER` in `k8s/base/servers/agent-context` | bge-reranker-radeonvii Ready |
| HUD coordinator: point at the warm lane (`COORDINATOR_*_MODEL` → workhorse) or turn it off | `k8s/base/servers/mobile-hud` | 6 calls in 30d today |

### Slice 2b — a job local models do not have yet: intake curation

436 of 1030 Mills backlog items are emitter-minted, 36 cite files that do not exist, 31 of 42
escalated items are emitter items. A duplicate-theme brake keyed on the grading corpus plus a
fabricated-slice pre-screen at enqueue is F2 of the 09-12 brainstorm and the deferred LLM ranker
slice 1c from `.loom/43` (May). No incumbent cost; pure local.

### Slice 3 — new models behind a runtime bump (§6)

## 6. September-2026 model survey for 24 GB gfx1100

Web survey (past the assistant's training data); every benchmark is vendor-reported under a
vendor scaffold. Verify with the gauntlet and a replay of graded Mills runs.

**Runtime prerequisite.** The fleet runs vLLM 0.23; upstream is 0.29. 0.28 added Muse
Glimmer, Qwen3.8 ROCm enablement and gfx11 enablement. Post-0.29 PR #55522 added an RDNA3
W4A16 MoE backend validated on two 7900 XTX (~385 tok/s at 8 seq) — compressed-tensors
symmetric only; check whether the 35B-A3B lane logs `Rdna3WNA16Experts` or sits on the slow
Triton path. PR #54706 fixes nondeterministic bf16 split-K on RDNA3. The July currency canary
(`deploy/debug/qwen36-currency-canary-model.yaml`) is the tool.

| Model | Fits 24 GB how | Role | Why | Risk |
|---|---|---|---|---|
| Laguna XS 2.1 (Poolside, 2026-07-02, 33B-A3B, OpenMDW) | ~17–19 GB after self-requant to compressed-tensors W4A16 sym g128 | workhorse A/B | SWE-V 70.9, TB2 37.5 at 3B active; expect ~70 tok/s class | official INT4 is Marlin-only; llama.cpp HIP reports flaky |
| Muse Glimmer 30B → vLLM | self-GPTQ sym g128, ~18 GB | tools / judge | dense GQA gets the fast W4A16 path vs 30 tok/s on llama.cpp today | needs vLLM 0.28 |
| Smaug-Mini (Abacus, 2026-09-10) | LoRA on Qwen3.8-27B; rerun the biMEMO AutoRound recipe | workhorse A/B | agentic-loop tuned (JobBench 50.5 vs 33.4) | no SWE/TB numbers published |
| Granite 4.2-30B (IBM, 2026-08-25) | ~17 GB INT4, plain dense | MCP / tool lane | BFCL v4 61.4, τ³ 62.0 | weaker coder |
| K2-Horizon-7B (IFM, 2026-09-03) | 5 GB Q4 | triage / judge | self-reported SWE-V 70.6 at 7B | unverified; runtime fork-only; report due end of Sept |
| Gemma 4 26B-A4B July-15 refresh | same lane | action item | tool-calling/template fixes | re-pull + re-quant |
| Qwen3-Embedding-4B/8B + Qwen3-Reranker | trivial | RAG plane | leads code-retrieval benches over gte-qwen2-1.5b | Qdrant re-index (16+ collections) |

Not in the window or not a fit: no small Qwen 3.9/4, no Gemma 5, no Llama 5 (contradictory SEO
claims; treat as unreleased), no small GLM-5 / DeepSeek V4 / Kimi, no Phi-5, no new gpt-oss or
Mistral small. Nemotron 3.5 Lightning is 25 GB at Q4_K_M with no ROCm evidence. jina-reranker-v3.5
is non-commercial. llama.cpp: MTP merged in May (~1.9× decode); Vulkan decodes 15–25% faster than
ROCm on the 7900 XTX — try on the Glimmer lane before moving it.

Suggested order: Smaug-Mini first (zero infra change) → vLLM 0.28 canary → Glimmer on vLLM +
Laguna requant → Granite for tools → embeddings.

### 6b. Video lane candidate: LTX-2.5 (requested 2026-09-13)

Current video lanes: `wan21-t2v-1p3b-gfx1100` (text2video, 480p) and `wan21-vace-1p3b-i2v-gfx1100`
(keyframe/i2v) on the 5930k diffusers backend (`diffusers-video:rocm-gfx1100`, diffusers 0.36.0,
bf16 + cpuOffload). Web survey of https://huggingface.co/Lightricks/LTX-2.5 (2026-09-13; vendor
numbers, unverified):

**What it is.** Released 2026-08-11. One size only: a ~22B asymmetric dual-stream DiT (video
4096-wide + audio 2048-wide; diffusers `transformer/` is 38 GB bf16 ≈ 19B, plus ~3B connectors)
with a version-locked **Gemma 4 12B** text encoder (24 GB bf16). Variants `dev` (30 steps, CFG)
and `distilled` (8 fixed sigmas, guidance 1.0). Modes: T2V, I2V, first/last-frame, arbitrary
keyframe conditioning, video extension, retake, multishot, joint audio, duration head, 4K via a
two-stage/diffusion decoder. `num_frames % 8 == 1`, 24 fps base, up to 20 s. License: LTX-2.x
Community License, royalty-free incl. commercial under $10M annual revenue; **the HF repo is gated**
(license acceptance + token; an operator action).

**Fit on a 24 GB gfx1100 through diffusers.**

| Variant | DiT footprint | Verdict |
|---|---|---|
| dev / distilled bf16 | 38 GB | no (sequential CPU offload only; impractical) |
| official int8-convrot | 21.5 GB | not via diffusers (ComfyUI format); on RDNA3 in ComfyUI it loads but runs 2–3× slower than fp8 |
| NVFP4 | 18.7 GB | no (Blackwell) |
| community GGUF Q4_K_M / Q5_K_M / Q6_K | 15.7 / 18.1 / 18.6 GB | plausible with the text encoder offloaded + tiled VAE; LTX-2.5 GGUF loading in diffusers `from_single_file` unverified |
| bitsandbytes NF4 on the fly (DiT + connectors + Gemma 4) | ≈ 10–11 GB DiT + ~1.7 GB + ~7–8 GB (estimates) | most likely fit and ROCm-compatible; **no published datapoint** |

**Software.** diffusers **0.40.0** (2026-08-20) adds LTX-2.5 on the existing `LTX2Pipeline` /
`LTX2ImageToVideoPipeline` / `LTX2ConditionPipeline` (FLF2V, keyframes, extension) classes;
transformers **≥ 5.5.0** for the Gemma 4 encoder; bitsandbytes ≥ 0.49.2 on ROCm. The fleet image
pins diffusers 0.36.0 → image rebuild. The 2.5 diffusion decoder needs flex/NATTEN attention
(CUDA); on ROCm use the conv VAE with `enable_tiling()` and skip it. `server-diffusers.py` has no
LTX2 pipeline mode (LTX2 returns video + audio and takes distilled `sigmas`), so this is a new
pipeline family in the server, not a manifest change.

**Speed and quality.** 24 GB NVIDIA reports (ComfyUI int8): RTX 4090 22.7 GiB resident, text
encoder must be freed before sampling; RTX 3090 ~2.5 min per 5 s distilled clip. AMD: **zero
LTX-2.5 datapoints**; LTX-2.3 on a 7900 XTX needed ComfyUI offload flags to load and int8 ran
2–3× slower than fp8. Arena: LTX-2.5 Fast Elo 1216 T2V; community says 3–4× faster than Wan 2.2
on NVIDIA but Wan still renders more photorealistic humans; known I2V identity drift and small-face
failures, which bear directly on the VACE keyframe use case. Baseline here: Wan2.1-1.3B ≈ 4 min
per 5 s 480p clip on a 4090 unoptimized, so on gfx1100 with NF4 dequant + SDPA-only attention a
regression versus the 1.3B lanes is plausible.

**Verdict.** Augment, not replace: it brings audio, FLF/keyframes, extension and far stronger
prompt following, but only as a canary behind an image bump. Prerequisites in order: (1) operator
accepts the gated license and stages the weights (exclude `transformer_full/`); (2) diffusers 0.40
/ transformers 5.5 / bnb image (`build/requirements-diffusers-rocm.txt`) — check the Wan/Flux/SDXL
paths against transformers 5.5; (3) an `ltx2` pipeline mode in `server-diffusers.py` (T2V + I2V +
condition; NF4 loaders; tiled VAE; no diffusion decoder). Placement tension: video lives on the
5930k card that slice 1 now keeps warm for Mills; a video canary demand-swaps the workhorse
(equal priority 500, allowed once idle > 2 min) and the next Mills call then pays a 5-minute cold
start — schedule LTX canary windows deliberately, or run video on the XTX under option 7(a).

**Riskiest assumption + kill-test.** Assumption: NF4 LTX-2.5-distilled DiT + NF4 Gemma 4 12B
encoder produce a coherent 5 s, 768×512, 121-frame clip with audio on one gfx1100 inside 24 GB in
under 10 minutes. Kill-test (one operator, one canary Model on the 5930k or XTX, diffusers 0.40
image): generate the clip; pass only if peak VRAM < 23 GiB, no OOM/restart, the MP4 decodes at
121 frames with a coherent subject, and wall time < 10 min; then run the VACE keyframe prompt from
`.loom/30-implementation-plan-keyframe-video-pipeline-2026-07-15.md` through
`LTX2ConditionPipeline` and compare first-frame preservation. Fail → stay on Wan2.1-1.3B and
revisit with a GGUF Q5_K_M DiT or a vLLM/ComfyUI-side runtime.

## 7. The XTX co-residency blocker (needs an operator decision)

The proposal "warm-pin a 27B beside the 3.2 GB orpheus pod" does not survive contact with the
node:

1. **Device-plugin slots.** cblevins-7900xtx exposes `amd.com/gpu: 2` (dGPU + Raphael iGPU).
   The gfx1100 GPUProfile sets `deviceCount: 2, usableDeviceIndices: ["0"]`; a dedicated vLLM
   pod must claim `count: 2` so it never draws the iGPU (the 2026-08-23 snac kill-test drew the
   iGPU with `count: 1`). `orpheus-3b-7900xtx` is a dedicated llamacpp pod holding one slot, so
   any dedicated text lane stays Pending while it runs. llamacpp is not a runtime-bundled
   backend, so Glimmer has the same problem.
2. **The runtime-managed channel is too old.** The persistent runtime pod
   (`flexinfer-runtime-gfx1100`, bundled backends vllm/diffusers/snac) is the only co-residency
   path with a dedicated pod, and it runs **vLLM 0.17.0** with no `RDNA3W4A16` kernel — it cannot
   load the Qwen3.5-family hybrid architecture at all. It is also single-model until
   `FLEXINFER_RUNTIME_MULTI_MODEL=1` lands ("pending Helm re-adoption", per the profile).
3. **VRAM budget** (GPUProfile `vramMB: 24576`): orpheus 3300 + a 27B at 64K (≈ 20 600) fits,
   but adding snac 1500 does not; a 32K envelope (≈ 19 200) fits all three on paper.

Options, in order of cost:

- **(a)** Accept voice on the warm-pinned Radeon VII tier only (RTF ~0.75 vs 1.6 on the XTX;
  zero voice requests in 30 days). Scale `orpheus-3b-7900xtx` to `minReplicas: 0`, apply
  `deploy/debug/qwen38-27b-xtx-warm-canary.yaml`, run the kill-test below. Reverses the
  2026-08-19/08-30 posture; operator call. **CHOSEN 2026-09-13**: orpheus-3b-7900xtx parked
  (minReplicas 0, priority 460) by MR; canary applied by hand at priority 470 with whole-card
  `vramEstimateMB: 23200` so snac/whisper stay parked (tts-lane falls back to CPU decode).
  Outcome recorded in §7a below.
- **(b)** Rebuild the gfx1100 runtime bundle on the certified vLLM 0.23 digest (or the 0.28
  currency candidate) and enable multi-model; then a runtime-managed 27B at 32K co-resides with
  orpheus and snac. Real image work; pairs naturally with slice 3.
- **(c)** Leave the XTX as the idle voice card. Slice 1's 5930k change still removes most Mills
  cold starts.

### 7a. Outcome of option (a) — applied 2026-09-13 14:38Z

Orpheus XTX parked by !1021 (Flux applied in ~60 s; pod gone, device slots free). Canary applied by
the operator; Ready at **+6 min**: cache staged to the node model store in 3 min (~140 MB/s; the
controller used `/var/lib/flexinfer/models/...`, not a Longhorn PVC), weights 42 s, engine init
69 s of which 51 s was the first graph compile (AOT-cached from now on), `RDNA3W4A16LinearKernel`
engaged, KV pool **69,905 tokens fp8 = 1.07× a 64K request**, pod on cblevins-7900xtx with
`amd.com/gpu: 2`.

| Probe (through flexinfer-proxy, alias `qwen38-27b-xtx-warm-canary`) | Result |
|---|---|
| "Reply with exactly ROUTE_OK" | `ROUTE_OK`, 3.97 s (first request after boot) |
| "17 × 23, just the number" | `391`, 0.29 s |
| one-sentence Pending-pod explanation, 36 tokens | coherent, 1.06 s (≈ 34 tok/s) |
| 2,317-token prompt, 2 output tokens (≈ TTFT) | `DONE`, **2.57 s** (target was < 2 s; prefill-bound, ~900 tok/s) |
| Voice stack | orpheus-3b-radeonvii Ready; orpheus-3b-7900xtx / snac / whisper Idle; RP lane Idle |
| `orpheus-tts` route probe (`/v1/completions`, 6 tokens) | served by the VII twin in 0.72 s (audio tokens returned; VII `lastActiveTime` advanced) |
| Card | 22.6 GB VRAM in use (util 0.94), 2.5% busy over the probe window |
| `flexinfer_model_cold_start_duration_seconds_count{model="qwen38-27b-xtx-warm-canary"}` | 1 (the initial boot, 367 s) — the 24h kill-test is "still 1 tomorrow" |

Verdict: the warm lane is there and answers in well under a second for short work; long prompts
pay prefill, not cold start. Remaining gates before a promotion MR moves Mills' role aliases:
the 24h zero-cold-start soak and a Ready ≥ 90% sample rate; then decide which aliases move
(proposal: `mills-council`, a new `mills-warm` for triage/consolidation/lenses; `workhorse` and
`reasoning` stay on the 5930k 131K lane).

### 7b. Slice 2 shipped the same day (soak waived by the operator)

- flexinfer !1024 promoted the lane into Flux under its existing name (`deploy/models/
  qwen38-27b-xtx-warm-canary.yaml`) with labels `mills-warm` / `mills-council` / `xtx-warm`;
  `mills-council` left the 5930k workhorse. Observed: Flux adoption recreated the pod inside the
  same ReplicaSet (no template change); back to Ready in ~2.5 min from the cached model store and
  AOT cache. A `mills-warm` probe issued during that window waited 72 s and answered
  `WARM_ROUTE_OK`.
- gitops !696 pointed Mills at the local lanes: council lenses security / tech-debt / user-impact,
  audit pool ×2, spinning ring / open-end → `mills-warm`; research/weaver stage → flexinfer backend
  on `qwen38-27b-autoround-workhorse` (131K) with `mills-warm` fallback; `FLEXINFER_MEMORY_MODEL=
  mills-warm`; judge and tiebreaker unchanged. The operator rolled at ~15:2xZ with the env live.
  **Correction (gitops !699)**: `mills_audit_bulk_reviewers_available` fell 2 → 0 on that roll —
  the flexinfer reviewer client resolves model ids against the proxy's `/v1/models` list, so a
  bare label 404s (the 2026-08-23 lesson already in the audit-pool comment). Every Mills
  reference now uses the serving name `qwen38-27b-xtx-warm-canary`; the `mills-warm` label stays
  for chat-completions callers, which the proxy does route by label.
- flexinfer !1025 repinned the three dead quantization/abliteration digests and prewarms
  `2e9652ed` + `522d0fe2` (serving persona, S1 target) on both gfx1100 nodes.
- **Verified on the 18:00Z council (2026-09-13)** — Mills counters since the operator restart at 17:33Z:
  `mills-council-reviewer` × `qwen38-27b-xtx-warm-canary` 15,064 prompt / 1,152 completion tokens (the three
  local lenses, 384 max tokens each), `mills-flexinfer` × the XTX lane 9,805 / 1,429 (audit pool ×2,
  `mills_audit_bulk_reviewers_available` = 2), `mills-weaver` × `qwen38-27b-autoround-workhorse` 14,342 / 530
  (research stage); the frontier lens (kimi), editor (claude-fable-5-1) and judge (gpt-5.6-luna) unchanged.
  Council cost $0.98 frontier / $0.003 local. Caveat: the `mills_llm_*` counters reset on every operator
  restart, and proxy/vLLM log greps were unreliable for this question — use the counters.

### 7c. Runtime-image program, same day (issue #74; spec `20-product-spec-runtime-image-ci-2026-09-13.md`)

| Slice | Landed | Evidence |
|---|---|---|
| S0 drift gate + trigger parity | !1022, !1029, !1030 | `runtime_pin_drift` on master; live-manifest filter → 0 FAIL after the repins |
| repins + prewarm | !1025, !1031 | three dead quantization/abliteration digests and the dead `diffusers-video` prewarm pin replaced; serving persona prewarmed on both gfx1100 nodes |
| S1 gfx1100 serving persona | !1027 | `ds/flexinfer-runtime-gfx1100` → `522d0fe2`; in-pod vLLM 0.17.0 + diffusers 0.39.0, snac/diffusers servers present, `FLEXINFER_RUNTIME_MULTI_MODEL=1`; both dedicated text lanes untouched. The 5930k DaemonSet is desired=0 behind its `quantization-pause` selector, so only the XTX pod rolled |
| S3 gfx906 | !1031, !1028 | `ds/flexinfer-runtime-gfx906` → `25fce977` (08-25 build); embeddings back at +30 s, rerank at +45 s; Orpheus VII Ready at +4 min after the controller "deferred runtime load while active peer is protected" (retrieval lanes load first) — voice was unreachable for those four minutes; Go default `-v3` → `-v4` |
| S4/S5 first half | !1032 (blocked) | nightly Trivy over the live GPU pins + master rules for the standalone vLLM job; blocked by a CI runner node in DiskPressure and the `trivy_scan_deps` gate on grpc CVE-2026-84445 (bump in !1033) |

Observed on the way: a Model CR spec change (labels via Flux adoption) or a HelmRelease upgrade
recreates the model pod (scale 0→1 inside the same ReplicaSet), so every values/lane MR costs the
warm text lanes a 3–5 minute reload — batch such changes. loom-core follow-ups filed:
overseer triage model knob (loom-core #754) and the shadow local judge with a per-role calibration
series (loom-core #755); the agent-context reranker is enabled in loom-core !1945.

## 8. Riskiest assumption + kill-test (slice 1)

**Assumption**: a 12h `idleTimeout` on the workhorse keeps it Ready across Mills' inter-call
gaps without starving the 5930k video/audio lanes of demand-swaps.

**Kill-test** (48h after merge, read-only):
`increase(flexinfer_model_cold_start_duration_seconds_count{model="qwen38-27b-autoround-workhorse"}[48h])`
must be 0 outside a deliberate video activation; `flexinfer_model_phase{model=…,phase="Ready"}`
must hold at ≥ 90% of 4h samples; and one manual `video-i2v` request must still activate
`wan21-vace-1p3b-i2v-gfx1100` (then the workhorse must re-activate on the next Mills call).
Fail on the video half → revert to 30m and move the warm lane to option 7(a)/(b).

**Kill-test for 7(a), if chosen**: canary Ready on the XTX with orpheus at 0; 24h with
`increase(...cold_start...[24h]) = 0`; TTFT p50 < 2 s from the loom-mills namespace on a
2K-token prompt; 6/6 gauntlet coherent; `mills-tools`/`judge` probes unchanged on the 5930k.

## 9. Follow-ups filed by this note

- [x] Orphaned ModelCache `gemma4-26b-a4b-gptq` + faulted 96Gi PVC on the XTX — operator yes
  2026-09-14; !1053 removed the manifest, Flux pruned the CR and the PVC, PV and faulted Longhorn
  volume were gone within 30 s (no manual Longhorn step needed). The artifact remains in Harbor
  only (the 5930k OCI cache was deleted the same night, !1049).
- storyboard-generator: give it a LiteLLM key or stop polling `/v1/models`.
- LiteLLM aliases for `gemini-3-flash-preview` and `text-embedding-3-small` callers.
- loom-core docs drift (MILLS.md, weaver.md) and the squads ConfigMap model names.
- Read `model-eval-qwen3-radeonvii-58-*` logs within 24h of its next run.
- [x] Controller default `ephemeral-storage` request + serving PriorityClass on every dedicated
  serving Deployment — shipped 2026-09-14 (chart `controller.runtime.servingDefaults`, manager env
  `DEFAULT_SERVING_PRIORITY_CLASS` / `DEFAULT_SERVING_EPHEMERAL_STORAGE_REQUEST`; annotation and
  explicit requests still win, an empty annotation opts out). Goes live on the next controller
  roll; each Ready lane without both guards reloads once.
- [x] 5930k root headroom — operator yes 2026-09-13; !1049 deleted `qwen36-27b-oci` (25 G),
  `qwen36-27b-fable-oci` (17 G) and both gemma4 caches (32 G) at 01:2xZ on 09-14 (the PVCs
  needed their Completed cache-copy/stage pods deleted before `pvc-protection` let go);
  `qwen36-35b-mtp-uncensored` (40 G) stays parked on purpose. Root now 693 G used / 237 G free.
