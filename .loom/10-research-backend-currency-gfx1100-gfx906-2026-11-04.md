# Backend currency and gfx1100 vLLM gaps (round of 2026-11-04)

Date: 2026-11-04. Status: **research, awaiting owner review**. Nothing here changes a live lane.

Predecessors: `.loom/10-research-gfx1100-gfx906-next-differentiators-2026-10-31.md` (the
November design, "the design" below), `.loom/10-research-gfx1100-next-feature-2026-10-23.md` (R),
`.loom/10-research-serving-gaps-switching-caching-2026-10-27.md` (SG).

Evidence gathered for this doc on 2026-11-04: `git log --first-parent master` 2026-11-01..04
(through `435ded36`), `glab mr list` (27 open), live k3s state (`Model` phases, runtime and prewarm
DaemonSets, GPU node labels), `.loom/60-validation-matrix.md` rows 154–166, and four research agents
(three on upstream backends, one on vLLM's gfx1100 code paths) whose full reports are summarised in
§3–§5. Every upstream claim is marked **confirmed** (merged code or release note read), **claimed**
(issue, blog, forum) or **unknown**. The four full reports are local notes (not in git) under
`/Users/cblevins/workspace/.loom/local/flexinfer/backend-currency-2026-11-04/`.

---

## 0. TL;DR

- **Delivered and live.** The flex-card layout (KT-FLEX) is promoted after the XTX hardware fix, Wan
  renders cold in 252 s, the Radeon VII serves ASR through whisper.cpp, and D4, D5-a, D6 and D7 code
  is merged. The XTX runtime DaemonSet was still on vLLM 0.17; it moves to 0.32 today (owner override
  of the 11-06 window) after the stale gfx1100 GPUProfile vLLM defaults are fixed (§1).
- **Upstream moved.** vLLM 0.33.0 (11-02) ships a ROCm 10 default image and a like-for-like
  `-rocm72` one; classic ROCm ended at 7.2.4 and TheRock's 10.x took over; PyTorch 2.15 dropped
  gfx906; llama.cpp now cuts releases (v0.9.0 today); diffusers 0.42.1 is a security fix (§2, §3).
- **What 0.33 buys us**: fixes that the pending KT-QOS and KT-SO canaries depend on (priority
  self-preemption, structured outputs with MTP and the reasoning parser, Qwen3 tool-call streaming),
  lookahead prefix hashes for MTP, and **Octave KV**, a 3–4-bit KV cache built for exactly our
  Qwen3.8-27B attention shape on gfx1100. Taking it needs a plugin fix first (every MTP lane would
  TypeError) and three controller fixes (§4).
- **gfx1100 gaps we can close ourselves**, all as plugins on the upstream image: a pruned-vocabulary
  MTP draft head (the draft's 248K-vocab `lm_head` is an estimated 20–25 % of each decode step),
  Octave KV on 0.33 (replacing an fp8-segmented attempt), and input embeddings in pinned host memory
  (+2.4 GiB of KV). A one-window profiling run decides the order (§5).
- **gfx906**: keep vLLM on the VII, frozen; move llama.cpp b11335 → b11847 for the embedding lanes'
  LM-head skip; whisper.cpp waits for v1.9.6 (§4 D, §6).
- **Start now** (live-neutral): S1 controller currency and S2 the 0.33-ready plugin release (§6).

---

## 1. What landed and what is live (2026-11-01 → 11-04)

### 1.1 Merged since the design

| Feature | Merged | Matrix row and verdict |
|---|---|---|
| Placement layout (!1203) | 11-01 (`55a22375`); KT-FLEX **failed** 11-01 (XTX fell off the bus); owner hardware work 11-02 (link now Gen3 x8); burn passed 11-03; KT-FLEX rerun passed; promoted 11-04 (!1277) | `gfx1100-flex-card-layout-2026-11-03` **promote** |
| Wan VAE MIOpen bypass | !1268, live via !1275 (`b1f98ad0`): cold clip 252 s, VAE decode 7 s (was 370 s). fp32 VACE stays slow | inside KT-FLEX rerun |
| D1 prerequisites | creative-lane Python deps in the 0.32 serving image (!1269), `527e2714` prewarmed on the XTX (!1274) | `gfx1100-xtx-inpod-swap-2026-11-01` **pending** (window) |
| D4 tool-call conformance gate | !1233 (`eafe0204`) | `gfx1100-structured-outputs-2026-11-01` **pending** (owner canary) |
| D5-a diarization | pyannote backend + certificate gate (!1244), diarize job (!1245), KT-DIAR harness (!1246), pyannote layer fix (!1259) and prewarm of `cb4c8dc3` (!1260) | `gfx906-diarize-job-2026-11-xx` **pending** (window G2/G3: Drafts !1261, !1262) |
| D6 CPU twin failover | !1217 (11-02); controller non-unload bug fixed (!1256, refused fit releases the runtime) | `gfx906-retrieval-swap-safety-2026-10-31` **conditional**: K2 availability pass, park p95 8.5 s vs ≤ 2 s fail; Draft !1257 (twin 12 threads) and a K2 re-run pending |
| D7 power sampling | on the 5930k and the Radeon VII (!1231), sampler join-loop fix (!1272) | `gpu-power-sampling-1hz-2026-11-01` **pending** (72 h) |
| whisper.cpp ASR on the VII | !1206, !1207, !1250, !1252; ICC moved to it (gitops !1044) | `gfx906-whispercpp-asr-2026-11-02` **promote** |
| Proxy cold start | lane budget on cold start (!1264, rolled !1266), dial retry on an unprogrammed ClusterIP (!1270, rolled !1271) | — |

### 1.2 Live on k3s (read 2026-11-04 ~16Z)

| Card | Serving now | Engine |
|---|---|---|
| 5930k (gfx1100) | `qwen38-27b-autoround-workhorse` Ready (900, dedicated Deployment); six other lanes Preempted or Idle | vLLM 0.32.0 `b06b06fe`, segmented attention + fp16 KV + MTP K=2 at 32K |
| 7900 XTX (gfx1100) | `qwen38-27b-xtx-warm-canary` Ready (Deployment); Qwen-Image 2.1, Wan T2V, Wan VACE, Stable Audio Idle as Deployments | warm lane on `b06b06fe`; the **runtime DaemonSet is still `cfefae18`, the vLLM 0.17 persona** — D1's !1234 moves it to the 0.32 stack `527e2714` |
| Radeon VII (gfx906) | gte-Qwen2 1.5B, bge-large, bge-reranker, Orpheus, whisper-large-v3-turbo Ready; Qwen3-Embedding-4B, Qwen3-Reranker-0.6B, Humanizer Idle | runtime `6fc6fcbd` (llama.cpp b11335, whisper.cpp v1.9.5) |

All four GPU nodes report `flexinfer.ai/gpu.health=ok`.

### 1.3 Held for the owner window (≥ 2026-11-06 00:15Z)

| Draft | What it does |
|---|---|
| !1234 | XTX runtime DaemonSet and gfx1100 GPUProfile → `527e2714` (D1 step 2) |
| !1235 | XTX warm lane in-pod (D1 step 3, lane 1/5) |
| !1236–!1239 | Qwen-Image, Wan T2V, Wan VACE, Stable Audio in-pod. Their titles still say "BLOCKED on runtime image"; `527e2714` was built to carry those deps (!1269), so check the label before the window |
| !1221 / !1255 | proxy and controller with the `InferenceJob` API on (D2, KT-JOBS) |
| !1261 / !1262 | gfx906 runtime → pyannote layer, then the diarization lane (KT-DIAR) |
| !1257 | gte CPU twin headroom (D6 K2 re-run) |
| !1243 | pin `flexinfer-agent` by digest |

KT-QOS (D3) and KT-SO (D4) need an owner-applied canary and nothing else.

### 1.4 Hygiene found in the review

1. **The gfx1100 GPUProfile's vLLM block describes the 0.17/0.19 stack and still acts.**
   `deploy/gpuprofiles/gfx1100.yaml` `spec.backends.vllm`: `image` is `vllm@7ac257f3`,
   `defaults.enforceEager: true`, `v1Engine` "production target is 0.19.x", `piecewiseGraphs`
   experimental, `marlinINT4` "turboquant-vllm Tq4 is our INT4 path". `ApplyVLLMDefaultsFromProfile`
   (`backend/gpu_compat.go:232`) and `ResolveBackendImage` apply these to any vLLM Model that does not
   set them. Every promoted lane overrides both, so nothing is wrong today, but a new lane or an in-pod
   D1 lane that omits `enforceEager` boots in eager mode, and one that omits `image` gets the old
   engine; `whisper-large-v3-turbo` (Idle, XTX) is such a lane. Fix in a small MR: 0.32-era
   capability strings, `enforceEager: false`, image = the promoted 0.32 digest (or remove the image so
   lanes must pin). The gfx906 block (vLLM 0.6.3 V0) has the same shape and is only used by
   `openjev-0p8b-crossencoder-radeonvii`, which pins its own image.
2. **`flexinfer-agent` runs the mutable tag `20261104-160707`** and rolls on every GPU node, the XTX
   included, on each master build. Draft !1243 pins it; it should go in the same window as !1234.
3. **ROADMAP.md is 95 days old** (last updated 2026-08-01; the Tier-1 SLO is 90). Its "Now" names
   Qwen3.5 9B RP as the production text lane; the workhorse has been Qwen3.8-27B on vLLM 0.32 since
   10-25. `docs/IMPLEMENTATION_STATUS.md` (checked 11-01) still says D5 has no merged MR.
4. **Stale MRs**: !997 (superseded by !1196), !1087, !1062, !664, !237, !757, !227, !1011, the
   tech-debt pair !58/!59 and Renovate !47/!50/!51/!108/!109. Close or rebase; they hide the window
   Drafts in `glab mr list`.
5. **O7 (retire vLLM on gfx906)** is not quite true in the manifests: the OpenJEV NLI cross-encoder
   lane on the VII is a vLLM lane (Idle, own image, matrix row 2026-09-23). Owner decision
   2026-11-04: **do not retire vLLM on the Radeon VII** unless the evidence in §3 makes a clear case.

Owner decisions taken on 2026-11-04 after this section: the window is overridden for the vLLM 0.32
runtime repin, which lands today after item 1 is fixed (session `xtx-runtime-v032`), and the stale
MRs in item 4 are being triaged (session `stale-mr-cleanup`).

---

## 2. Backend currency at a glance

| Backend | Arch | We run | Upstream on 11-04 | Next step | Gate |
|---|---|---|---|---|---|
| vLLM | gfx1100 | 0.32.0 on ROCm 7.2.3 (Deployment lanes); runtime DaemonSet 0.17 → 0.32 `527e2714` today | 0.33.0 (11-02): `-rocm72` and a ROCm 10 default tag | `v0.33.0-rocm72` after the plugin fix | XTX warm-lane parity canary (§6) |
| vLLM | gfx906 | an old gfx906 image on one lane (OpenJEV cross-encoder) | not built for gfx906 | keep it, frozen; no currency path upstream | — |
| llama.cpp | gfx1100 | b11847, ROCm 7.2.3 | v0.9.0 = b12110 | hold; v0.8.0 (b11960) after a Glimmer prompt-processing A/B | draft-mtp prefill leg |
| llama.cpp | gfx906 | b11335, ROCm 6.2 builder | same | **b11847** on the ROCm 6.2 builder, ceiling b11878 | trial build, VRAM and HW-queue re-measure |
| whisper.cpp | gfx906 | v1.9.5 | v1.9.5 (FA padding fix on master) | v1.9.6 or master ≥ `e4acdfdf` | KT-ASR rerun |
| Ollama | sm_52 | v0.17.4 | drops gfx906, now a llama-server front end | stay | — |
| diffusers | gfx1100 (0.32 image) | 0.42.0 | **0.42.1 (security)** | 0.42.1 at the next image build | — |
| diffusers-video (Wan) | gfx1100 | ROCm 6.4.1, torch 2.6, diffusers 0.36 | — | consolidate onto the 0.32 base through D1's in-pod Wan lanes | Wan decode A/B on the 0.32 base |
| PyTorch | gfx906 | mixa3607 2.9.0 / ROCm 6.3.3 | mixa3607 2.14.1 / ROCm 10.1; PyTorch 2.15 dropped gfx906 | canary ladder 2.11.0-rocm-7.2.1 → 2.14.1-rocm-10.1 | 50 cold starts, SDXL soak, pyannote parity |
| ROCm | gfx1100 | 7.2.3 | 10.1.0 (TheRock); classic line ended at 7.2.4 | stay on 7.2.3; ROCm 10 canary on the 5930k later | disk headroom, MES behaviour |
| pyannote | gfx906 | 3.3.2 | 4.0.7 (needs torchcodec, unsupported on ROCm) | stay | — |
| bitsandbytes | gfx1100 | 0.50.2 | 0.50.2 | — | — |

## 3. Upstream findings by backend

### 3.1 vLLM and vllm-omni

Full agent report: `research-vllm.md` (local notes, see the header; code read from a blob-less clone at
v0.32.0, v0.33.0 and main, nothing run on gfx1100, so every performance number is the PR author's).

**Releases.** v0.33.0 is the only release after 0.32.0 (tag `4ba787ec`, GitHub release and images
2026-11-02); v0.32.1rc0 is a tag with no image; main is ~750 commits past 0.33.0. Image tags
(confirmed, Docker Hub manifests):

| Tag | ROCm | torch / Triton | Size | Note |
|---|---|---|---|---|
| `v0.32.0` (ours) | 7.2.3 | 2.13 / 3.8.0+git669b31ac | 11.6 GB | — |
| `v0.33.0` = `latest` | **10.0**, TheRock pip SDK on plain ubuntu:22.04 | 2.13.0+rocm10.0.0 / same Triton | 18.0 GB | new base layout; our overlay is unproven on it |
| `v0.33.0-rocm72` | 7.2.3 | same as 0.32.0 | 11.8 GB | the like-for-like bump |

**Findings.**

| Item | Source | gfx1100 | Rel. | What flexinfer needs |
|---|---|---|---|---|
| `ROCM_OCTAVE` 3–4 bit KV (`octave_k3v3`, `octave_k3v4`, `_compact`) | #59774, `docs/design/rocm_octave.md` | kernels target gfx1100 and gfx942 only (confirmed); author ran Qwen3.8-27B W4A16 + MTP + cudagraphs at TP4 (claimed); not in CI; whether the published image carries the ops is inferred, not inspected | **H** | 0.33 image; `kvCacheDtype: octave_*`, no `attentionBackend`, no backend inside `speculativeConfig`; KT-OCTAVE (§6) |
| Lookahead block hashes: MTP/EAGLE prefix hits keep their last block; on by default, off with any KV connector | #61589 | confirmed (NVIDIA-measured) | H | image bump; `cached_tokens` accounting changes |
| Qwen3 tool parser: incremental argument streaming (12 KB call 2.1 s → 11 ms), `<function=` recovery | #62027, #59928 | confirmed (pure Python) | H (D4, Mills) | image bump |
| Priority scheduling self-preemption fix | #61976 | confirmed; 0.33 only | H (D3) | image bump before KT-QOS is judged |
| Structured outputs with MTP + reasoning parser: masks from GPU logit counts, no 500 on implicit reasoning end; LM-Format-Enforcer deprecated | #52477, #61574, #63423 | confirmed | H (D4) | image bump; never certify LMFE |
| `--prefix-match-unit`, `--enable-mamba-shared-prefix-checkpoint` (already in 0.32.0) | `docs/features/automatic_prefix_caching.md` | config confirmed, effect unknown | M–H | two controller keys in `backend/vllm.go`, then a Mills-trace canary |
| Fused GDN MTP decode, Triton port for ROCm | #61855 | needs bf16 model dtype; our lanes are `dtype: half` so it falls back | M | `dtype: bfloat16` canary |
| TRITON_ATTN 32-bit addressing restored (+18 % on W7900) | #60084 | claimed; `ROCM_SEGMENTED_ATTN` never calls `set_triton_ptr_range`, so our backend gets no fix | M–H | image bump for TRITON lanes; plugin experiment on segmented (hypothesis) |
| KV offload `fs` tier: persists across restarts, content-hash shared, ROCm listed; 0.33 fixes MTP + mamba-align partial tails | `docs/features/kv_offloading_usage.md`, #61019 | docs + code confirmed, no gfx1100 evidence | M (C11) | controller has no `--kv-transfer-config` passthrough |
| Sleep mode: ROCm cuMem fix, `--sleep-preserve-parameter-names 'draft.*'` | #60367, #61046 | #52479 still open; gfx1100 unknown | M (#68) | new controller flag |
| gfx11 W4A16 GEMM upgrades | #57199, #60644 | gated to gfx115x | L | none |
| RDNA lm_head int4/int8 load-time quantization | #62708, #63402 (**open**) | measured on gfx1151 only | M–H if merged | watch v0.34 |
| vllm-omni | v0.32.0 (10-28), v0.33.0rc1 (11-04) | ROCm validated on gfx942 only; newest `vllm-omni-rocm` image v0.31.0rc1 | L | none |
| gfx906 | image arch lists | not built for gfx906 | — | verdict unchanged: no upstream vLLM for Vega20 |

**Plugin audit.** All plugin import targets exist unchanged at 0.32.0, 0.33.0 and main. Deletable
once no 0.23/0.26 runtime remains: `hybrid.py`, `mrope.py`, the `Qwen3_5ForCausalLM` registration,
`_patch_gptq_expert_loader` (inert since 0.26), and the seqcls label repair (upstream
`_resolve_num_labels`; keep the head-row assertions). Must keep: the FLA autotune pruning in
`rocm.py` (upstream guards only `chunk_delta_h`), the MTP head policy, the seqcls registration, and
the AutoRound `hfOverrides` GPTQ rewrite (INC still needs Marlin).

**Breaking changes on a bump to `v0.33.0-rocm72`.**

1. The text plugin's `hf_config_override` wrapper takes `(hf_config)`; upstream is now
   `(hf_config, transformers_mtp_overrides=None)` and is called through `functools.partial` when
   building the draft config, so **every MTP lane raises a TypeError** (confirmed by reading, not run).
   Upstream already maps `qwen3_5*_text`, so delete it or pass `*args, **kwargs` through.
2. `--mamba-cache-mode` is a deprecated no-op (removal in v0.34); four lanes set `mambaCacheMode`.
3. `VLLM_ROCM_USE_AITER_FP4_ASM_GEMM` is removed (#61278). `rocmAiterDisabledEnvVars()` still sets
   it, and the three `qwen35-35b-*` lanes combine `disableAiter: true` with
   `failOnEnvironValidation: true`, so they **hard-fail on any 0.33+ image**.
   `VLLM_ROCM_USE_AITER_PAGED_ATTN` and `VLLM_USE_TRITON_FLASH_ATTN` are already unknown in 0.32.0.
4. `--max-num-partial-prefills` / `--max-long-partial-prefills` exist in no version (0.32.0, 0.33.0,
   main); the controller can render them but no manifest sets them. Dead code that would abort
   argument parsing.
5. The default `v0.33.0` tag is the ROCm 10 image; take `-rocm72`.

Other: Qwen4 exists upstream only as `Qwen4Exp` (176B MoE, no 27B); the Responses API `background`
mode is in-memory and non-durable, so D2's `InferenceJob` design stands.

### 3.2 llama.cpp, whisper.cpp, Ollama

Full agent report: `research-llamacpp.md` (local notes, see the header; flags and code read at the named tags,
no build or run; issue numbers are the reporters').

**Versioning.** llama.cpp now cuts real releases: v0.7.0 = b11669 (10-15), v0.8.0 = b11960 (10-28),
v0.9.0 = b12110 (11-04). Our pins: gfx906 b11335 (10-01), gfx1100 b11847 (10-23). Ollama itself
now pins b11963 and is a llama-server front end.

| Item | Source | gfx1100 / gfx906 | Rel. | flexinfer change |
|---|---|---|---|---|
| LM-head and logits-buffer skip for pooled embeddings (b11597) | #30445 | in b11847, **absent from b11335** | **H** | bump gfx906; re-measure VRAM on gte/bge/Qwen3-Embedding and lower `vramEstimateMB` |
| MMQ grid-order change (b11879) slows MoE prompt processing | #31599, #31603 | gfx906 −15 to −50 % on MoE (claimed); gfx1100 unknown | M–H | gfx906 ceiling b11878; Glimmer prompt-processing A/B before any gfx1100 bump past it |
| draft-mtp cuts HIP prefill 74–80 % (open; reported on gfx1101) | #31495 | gfx1100 unknown | **H** | our `llamacpp-draft-mtp` certificate measured decode only; add a prefill-with-MTP leg |
| `flash_attn_ext_rows` rewrites the HIP FA kernels (b12059) | #29510 | both | M | avoid ≥ b12059 until someone measures it |
| Slot-file format bump (b11411) | #28498 | gfx906 | M | slot files from b11335 cannot be restored; the digest-bound slots and n-gram certificates go stale on the bump |
| ROCm 6.3.x compile break (b11710+) | #30755 | gfx906 builder | M | stay on the ROCm 6.2 builder (the code is compiled out below 6.3) |
| gfx906 intact in code (sdot4, dp4a, MMQ) but not in upstream CI or release targets; no plan to drop it | `.devops/rocm.Dockerfile`, `common.cuh` | gfx906 | M | none |
| Jinja `tojson` formatting changes (b11622+) | #30362 | gfx1100 tools lane | M | re-run the tool-calling gauntlet on any bump |
| Fused gate/up MMQ is NVIDIA-only; chunked GDN is RDNA3.5-only | #29948 | gfx1100 gets neither | L | none |
| Default port 8080 → 9931 (b11521); `--model`/`-hf` required (b11929) | #30159 | both | L | none: we pass both |
| whisper.cpp: no release after v1.9.5; the flash-attention padded K/V fix is master-only | whisper.cpp #4127 | gfx906 ASR | M–H | wait for v1.9.6, or pin master at/after `e4acdfdf`; re-run KT-ASR (its ggml sync carries the MMQ change) |
| whisper-server `words` are still per BPE token | `examples/server/server.cpp` | gfx906 | M | keep our sub-word merge |
| Ollama dropped gfx906 (ROCm 7); Maxwell only via `cuda_v12` PTX with driver ≥ 570 | ollama `docs/gpu.mdx` | gfx906 gone | L | stay on v0.17.4 for Maxwell and the VII |

**Safe on a bump:** no CLI flag was removed between b11335 and v0.9.0 (`--draft-max/--draft-min`
still abort, as the controller already handles); `/metrics` series are identical at b11335, b11847
and v0.9.0; the `patch-hipmemgetinfo.sh` anchor still exists at every tag checked in llama.cpp and
whisper.cpp. **Re-measure HW queues** on the first gfx906 canary: `cudaStreamPerThread` call sites grew
from 17 to 31, and the VII's admission is 24/3.

**Targets.** gfx1100: hold b11847; next candidate b11960 (v0.8.0) gated on a Glimmer
prompt-processing A/B, fallback b11878; b12058 only if a hybrid MTP lane is promoted (spec-checkpoint
fix b11996). gfx906: move to **b11847** on the existing ROCm 6.2 builder (whether it compiles there is
unknown; no upstream CI runs below ROCm 7.2), ceiling b11878, fallback v0.7.0 (b11669).

### 3.3 ROCm, PyTorch, diffusers and the audio stack

Full agent report: `research-rocm-diffusers.md` (local notes, see the header; clones and greps, nothing run).

**The platform moved under us.** Classic ROCm ended at 7.2.4 (2026-05-29); TheRock-built ROCm
followed (7.14 in July, 10.0.0 on 08-26, 10.1.0 on 10-05; 10.2 unreleased). PyTorch is at 2.15.0
(10-28), which ships wheels only for ROCm 7.14 and 10.0 and **drops gfx900/gfx906 kernels and the
ROCm 7.2 wheels** (#194919). The Wan lanes do not run the 0.32 stack: they run `diffusers-video`
(ROCm 6.4.1, torch 2.6, MIOpen 3.4, diffusers 0.36 pins) by `spec.imageDigest`. Stable Audio pins
its own `diffusers@d87f75f8`.

| Item | Source | gfx1100 / gfx906 | Rel. | flexinfer change |
|---|---|---|---|---|
| diffusers **0.42.1** security release: component-library resolution in `from_pretrained` (tied to a `trust_remote_code` bypass) | GHSA-4prv-5hj7-c2xr | both | **H** | bump `DIFFUSERS_VERSION` in `Dockerfile.runtime-serving-v032` at the next image build; gfx906's unpinned `diffusers>=0.30.0` would float to it and needs Python ≥ 3.11 |
| Conv3d still slow on ROCm 7.2 | InvokeAI #289 (W7900, torch 2.13+rocm7.2; claimed) | gfx1100 not fixed | H | **keep** the Wan VAE bypass (it lives in `build/server-diffusers.py`, so `527e2714` carries it for D1's in-pod Wan lanes) |
| MIOpen: Navi CK-WMMA 3D solver on develop (7.11+ line); naive conv "2–35×" faster "for 10.2.0" | rocm-libraries MIOpen CHANGELOG | gfx1100 effect unknown | M | Conv3d A/B on the rocm100 image; revisit the bypass after 10.2 |
| fp32 VACE decode: cast the decode half to bf16 and reuse the bypass | unsloth #12681 (gfx1151 580 s → ~178 s; claimed) | gfx1100 | M | VACE bf16-decode kill-test with a PSNR check |
| torch 2.12–2.13 RDNA fixes: wave32 `num_warps` no longer halved, wave32 persistent-kernel SIGSEGV, expandable segments on ROCm | PyTorch release notes | gfx1100 | M | the torch 2.6 Wan image lacks them; consolidate Wan onto the 0.32 base after a decode A/B |
| AOTriton 0.12b promotes gfx1100 out of experimental (torch 2.13 pins it) | AOTriton, `sdp_utils.cpp` | gfx1100 | L | `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL` probably removable; A/B first |
| `HSA_OVERRIDE_GFX_VERSION=11.0.0` redundant for a native gfx1100 | TheRock SUPPORTED_GPUS | gfx1100 | L | test removal on a canary; it may also stop the XTX's Raphael iGPU (gfx1036) being spoofed as gfx11 (inferred) |
| `TORCH_BLAS_PREFER_HIPBLASLT=1` | torch `CUDAHooks.cpp` | gfx1100 | L | keep on 7.2.x; the default flips to hipBLASLt at ROCm ≥ 7.13 |
| linux-firmware reverted GC 11.0.0 twice (09-18, 10-17); no confirmed SMU/MES fix for Navi31 drop-outs | linux-firmware commits | gfx1100 | M | keep the pinned firmware set; do not take newer GC blobs |
| bitsandbytes 0.50.2 still latest; ROCm stable since 0.50.0 (4-bit SIMT GEMM validated on gfx1100); ROCm 10.0 build included; no gfx906 in the default arch list | bnb releases, CMakeLists | gfx1100 ok | L | none; gfx906 keeps its source build |
| diffusers caching hooks (First Block Cache registered for `WanTransformerBlock` and `QwenImageTransformerBlock`; MagCache, TaylorSeer); SDNQ quantization (claims ROCm) | diffusers 0.42.1 source | gfx1100 unmeasured | M | optional denoise speed-up (Wan denoise ~137 s of the cold clip); quality-gated |
| Stable Audio 3 (0.40.0; gated, needs local conversion), Wan 2.2 (0.35.0), LTX-2.5, HunyuanVideo 1.5 | diffusers notes | gfx1100 unknown | M | candidate lanes, not platform work |
| pyannote 4.0.7 needs torch ≥ 2.8 and torchcodec ≥ 0.7; TheRock marks torchcodec unsupported on ROCm | pyannote CHANGELOG, TheRock #1490 | gfx906 unknown | M | keep 3.3.2 until tested |
| CTranslate2 4.8.5 ships ROCm 7.2 wheels as GitHub assets (gfx1100, no gfx906); faster-whisper still 1.2.1 | CT2 `ci.yml` | gfx1100 | L | only matters if the faster-whisper rollback stays |
| TTS | — | — | L | no gfx1100 benchmark found for Kokoro, Chatterbox or Orpheus; Qwen3-TTS has a community ROCm server on gfx1151 only |

**gfx906.** Not in any official ROCm matrix (maintenance mode since 5.7). TheRock builds it nightly
as "Build Passing" only, with no hardware CI and hipBLASLt/CK/rocWMMA excluded. PyTorch 2.15 dropped
its kernels. The community `mixa3607/ML-gfx906` is the only path: `pytorch-gfx906:v2.14.1-rocm-10.1`
(TheRock base, cp312) is the newest, `v2.11.0-rocm-7.2.1` the conservative step from our
`v2.9.0-rocm-6.3.3`; legacy (pre-TheRock) builds are unsupported from 2026-08-02. Open issue #27: a
non-deterministic GPU crash on ~12K-token self-attention on old and new builds. Triton 3.8.0 gained
a gfx906 target (v_dot2/v_dot4, no MFMA); AOTriton still has none. llama.cpp and whisper.cpp are
built separately and are not affected by this ladder.

**Recommended stacks.** gfx1100: stay on the ROCm 7.2.3 line (`v0.32.0` today, `v0.33.0-rocm72`
next), diffusers 0.42.1, bitsandbytes 0.50.2, every workaround kept; canary the ROCm 10 image on the
5930k only after that (SDK under site-packages instead of `/opt/rocm`, +6.4 GB on a disk-pressure
node, new MES behaviour on a card family with a drop-out history). gfx906: a canary ladder
`v2.11.0-rocm-7.2.1` then `v2.14.1-rocm-10.1` (50 fresh container starts, SDXL inpainting soak,
pyannote RTTM parity, diffusers 0.42.1 import), fp16 only, math SDPA, no `torch._int_mm`.

## 4. What flexinfer needs to change

Checked against the code on 2026-11-04 where marked (✓).

**A. Controller and manifests (no image change needed)**

| # | Change | Where | Why |
|---|---|---|---|
| A1 | gfx1100 GPUProfile vLLM block to 0.32 facts, lanes that lean on its defaults pinned first | `deploy/gpuprofiles/gfx1100.yaml` | §1.4 item 1; **in flight** (session `xtx-runtime-v032`, MR 1) |
| A2 | Stop rendering `--max-num-partial-prefills` / `--max-long-partial-prefills` | `backend/vllm.go:142,145` ✓ | the flags exist in no vLLM version (0.32.0, 0.33.0, main); a Model that set them would fail argument parsing. No manifest sets them ✓ |
| A3 | Version-gate `rocmAiterDisabledEnvVars()` | `backend/vllm.go:743` ✓ | it always sets `VLLM_ROCM_USE_AITER_PAGED_ATTN` (unknown since 0.32.0) and `VLLM_ROCM_USE_AITER_FP4_ASM_GEMM` (removed in 0.33.0). Five lanes combine `disableAiter: true` with `failOnEnvironValidation: true` ✓ (`qwen35-35b-clean-gptq-workhorse`, `-128k`, `qwen35-35b-clean-mtp-32k`, two debug canaries); all are dedicated Deployments on pre-0.32 images today, so they **hard-fail the first time anyone repins them** to 0.32 or later |
| A4 | Version-gate `--mamba-cache-mode` | `backend/vllm.go:100` ✓ | deprecated no-op in 0.33.0, removed in 0.34; four lanes set `mambaCacheMode` |
| A5 | New config keys: `prefixMatchUnit`, `enableMambaSharedPrefixCheckpoint`, `kvTransferConfig` (pass-through), `sleepPreserveParameterNames` | `backend/vllm.go`, `docs/specs/crds.md` | the first two already exist in 0.32.0 and target exactly the hybrid-GDN prefix-cache case Mills hits; the last two unblock C11 (KV offload `fs` tier) and the #68 sleep re-check |
| A6 | llama.cpp `draft-mtp` certificate: add a prefill leg | `deploy/gpuprofiles/gfx1100.yaml` certificate evidence, matrix row | #31495 reports draft-mtp cutting HIP prefill 74–80 %; our certificate measured decode only |
| A7 | D4: never certify LM-Format-Enforcer as a structured-output backend | D4 certificate gate | deprecated upstream in 0.33 |

**B. Images**

| # | Change | Where | Gate |
|---|---|---|---|
| B1 | diffusers 0.42.0 → 0.42.1 (security) | `build/Dockerfile.runtime-serving-v032` `DIFFUSERS_VERSION`; pin it in `Dockerfile.runtime-gfx906` too (it floats `>=0.30.0`) | next 0.32 image build; not today's repin |
| B2 | Text plugin: `hf_config_override` wrapper → `(hf_config, *args, **kwargs)` or delete it | `build/vllm-qwen35-text-plugin/src/` | prerequisite for any 0.33 image (every MTP lane TypeErrors otherwise) |
| B3 | vLLM `v0.33.0-rocm72` serving image (B1 + B2 in the overlay) | `build/Dockerfile.runtime-serving-v032` `BASE_IMAGE` | parity canary (§6 S1) |
| B4 | gfx906 llama.cpp b11335 → b11847 | `build/Dockerfile.runtime-gfx906` `LLAMACPP_VERSION` | trial build on the ROCm 6.2 builder; LM-head skip VRAM re-measure; HW-queue count; slot files discarded and the slots certificate re-issued |
| B5 | whisper.cpp v1.9.5 → v1.9.6 or a pinned master commit | same Dockerfile | KT-ASR rerun (34.7× real time at 40 CUs is the bar) |
| B6 | Delete dead plugin code (`hybrid.py`, `mrope.py`, the `Qwen3_5ForCausalLM` registration, `_patch_gptq_expert_loader`, the seqcls label repair minus its assertions) | `build/vllm-plugin-common`, `build/vllm-qwen35-*-plugin` | only once no 0.23/0.26 image is pinned by any lane |

**C. Workarounds to re-test, not remove blind**: `HSA_OVERRIDE_GFX_VERSION=11.0.0` (redundant on a
native gfx1100, may also stop the XTX iGPU being spoofed), `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL`
(AOTriton ≥ 0.12 promotes gfx1100), the Wan VAE bypass (keep until ROCm 10.2's MIOpen), the pinned
Navi31 firmware set (keep).

**D. gfx906 and vLLM (owner: keep vLLM unless it clearly makes sense to retire).** The research
does not make that case. Upstream vLLM has not built gfx906 for a long time and nothing on the ROCm
side will change that (PyTorch 2.15 dropped the arch; TheRock is build-only), so there is **no currency
path**. But the one vLLM lane on the VII (OpenJEV NLI cross-encoder) pins its own image, passed its
kill-test on 2026-09-23 and costs nothing while Idle. Recommendation: keep it **frozen** — confirm its image
(`vllm@sha256:e0aa1e2e…`) carries a `pinned-*` Harbor tag, leave the gfx906 GPUProfile vLLM block as
is — and
retire only if a llama.cpp or CPU path serves the cross-encoder equally, or the image is lost.

## 5. gfx1100 gaps in vLLM we could close ourselves

Full agent report: `research-vllm-gfx1100-gaps.md` (local notes, see the header; shallow clones of v0.32.0
`5df63a72` and main `101cfd10`, nothing executed). Every candidate stays inside the production
contract: a plugin, an env var or a tuned config on the upstream image, never a vLLM rebuild.

### 5.1 Where the headroom is

A decode forward pass of the 27B int4 workhorse reads ~15.4 GB, so 960 GB/s caps it at ~62 tok/s
without speculation. We measure 41.5 tok/s without MTP (~640 GB/s, 67 % of peak) and 68.5 tok/s
with MTP K=2 (~600 GB/s effective, 2.39 tokens per step, 2.80 on live traffic). So:

- **The largest single overhead is the draft's vocabulary head.** Each draft step runs the full
  248K × 5,120 fp16 `lm_head` (2.54 GB). Inferred from S3, the two draft calls take ~6.8–8.4 ms of a
  33.4 ms step, 20–25 %. Not yet profiled.
- Prefill is GEMM-bound at ~55 % of WMMA peak; decode is ≥ 80 % of the lane's GPU-busy time.
- Long context is not the problem (decode −4 % from 2K to 16K); concurrency 2 is rare (0.19 requests
  running on average); cold start is pod churn and caches, which D1 owns.
- **VRAM has a free lever**: the 2.54 GB fp16 input-embedding table stays in VRAM, but a decode step
  reads ~10 KB of it.

### 5.2 Inventory

| # | Gap (file:line at v0.32.0) | Lanes | Est. impact | How we close it | Effort |
|---|---|---|---|---|---|
| G1 | MTP draft runs the full 248K-vocab `lm_head` every draft step; the hook to restrict it exists (`use_local_argmax_reduction` → `get_top_tokens`, `speculator.py:450-451`, `interfaces.py:1531-1565`); no draft-vocab feature on main either | workhorse, any MTP lane | +6 % (JartX gfx1100 branch #57925, measured) to +25 % (bandwidth model) single-stream decode, lossless | text plugin | S–M |
| G2 | Embedding table pinned in VRAM: the UVA offloader only wraps decoder layers and towers, so `--cpu-offload-params` cannot reach `embed_tokens` (`offloader/uva.py:74-125`, `models/utils.py:911-916`) | 27B (2.54 GB), 9B (2.0 GB) | +2.37 GiB KV, ~+60–70 % pool on the 27B (estimate) | plugin-common, then upstream PR | M |
| G3 | fp8 KV gated to gfx12 in `ROCM_SEGMENTED_ATTN` (`rocm_segmented_attn.py:146-147, 182-185`) although the kernel converts fp8 → fp16 in Triton | 64K, workhorse | KV ×1.9; speed at risk (#56992: software fp8 2.2–2.8× slower in another gfx1100 kernel) | CUSTOM backend subclass in the plugin | S to try |
| G4 | RDNA3 W4A16 GEMM row tiling: M=3 (K=2 verify) runs as 2 row tiles, M=4 as 1 (`q_gemm_rdna3.cu:769-822`) | workhorse | 0–5 %; also makes K=3 cheaper than it looks | plugin pads rows 3→4, 6→8 | S |
| G5 | wvSplitK skinny GEMM (runs our `lm_head`) tuned for gfx1151 (and gfx1150 on main); gfx1100 keeps generic rows (`skinny_gemms.cu:1263-1294`) | all | unknown until microbenched | upstream PR with a gfx1100 sweep | M |
| G6 | Main routes large `lm_head` weights off wvSplitK on MI355X data (`VLLM_SKINNY_GEMM_MAX_WEIGHT_BYTES`) | workhorse, 64K | possible regression on our next bump | pre-registered A/B in the bump slice | S |
| G7 | Segmented attention compiles each shape on first use: block-table stride is `tl.constexpr`, autotune tables are built at a different width than the runtime table (`segmented_attention.py:62-107`, `rocm_segmented_attn_autotune_warmup.py:399-447`); unchanged on main | all segmented lanes | 0.5–4.7 s first-of-shape stalls after any cache invalidation | plugin patch to `_make_inputs`, or upstream PR | S |
| G8 | No fused-MoE Triton config for `AMD_Radeon_RX7900XTX` (72 Instinct files, 2 R9700, 1 8060S) | MoE lanes | unknown | tuned JSON via `VLLM_TUNED_CONFIG_FOLDER` | S |
| G9 | The RDNA3 HIP MoE kernel takes only compressed-tensors checkpoints; our GPTQ MoE falls back to Triton `moe_wna16` | MoE lanes | unknown | re-pack the MoE artifacts | M |
| G10 | Our own FLA pruning collapses all 11 GDN autotuners to one config | Qwen3.5/3.8 prefill | ≤ ~5 % TTFT | relax to dropping `num_stages ≥ 4` | S |
| G11–G12 | Upstream RDNA tuning (GDN chunked prefill, hybrid W4A16, prefill attention) targets gfx1151 / gfx12, not gfx1100 | indirect | structural | send gfx1100 sweep data upstream when we touch a kernel | — |
| G13–G15 | AITER (CDNA only), hardware fp8 (none on gfx11), sleep mode | — | — | none / D1 owns sleep | — |

Also found: `disableAiter` is a no-op on gfx1100 (fix the comment at `backend/vllm.go:407`), and
`build/scripts/patch_vllm_rocm_rms_norm_dtype.py` and `vllm_qwen35_patches.py` are 0.17/0.19-era
(0.32 runs RMSNorm through Inductor; the native-W4A16 Dockerfiles do not apply them).

**Correction to the agent's rejected list.** It rejected "Octave / INT8 per-token-head KV" as not in
tree. That is true of v0.32.0 only: `ROCM_OCTAVE` is in v0.33.0 and main (`csrc/attention/octave_*`,
`docs/design/rocm_octave.md`, checked on both clones). It is built for exactly our attention shape —
head size 256 with 64 rotary dims, the Qwen3-Next/3.5/3.6/3.8 family — with a WMMA decode tier on
gfx11 that treats MTP verification as a first-class case, and it is validated on gfx1100 and gfx942
only. Upstream's results are 4× XTX at TP4 with MTP k=3: capacity measured (`octave_k3v3` 1,679K
tokens, `octave_k4v4` 1,402K at 8 GB per GPU), **speed and quality still "TBD"**. Octave dominates
G3 for capacity if it holds speed, and competes with G2 for the same goal.

Rejected by the agent, with reasons in its report: custom paged attention (head size 128 only), AITER
on gfx11, FlashInfer sampling (CUDA-only; the Triton fallback is < 1 % of a step), Marlin, fp8/MXFP4
GEMMs, custom all-reduce, wave32 retuning of the dense hot path (no gap found), quantizing the target
`lm_head` (breaks the quality contract), n-gram speculation (falsified 2026-05-14), a prefill WMMA
rewrite (needs a compiled kernel).

### 5.3 Candidates, ranked

**Step 0 — measure the decode step (no code, one canary window).** On the S3b canary pattern
(`deploy/debug/qwen38-27b-v032-s3b-ttft-canary.yaml`, `--profiler-config`, `cudagraphMetrics`):
a torch-profiler trace of 20 decode steps at 2K with 1 and 2 requests; an in-pod microbench of read
bandwidth, `wvSplitK` on [n, 5120] × [248320, 5120] for n = 1–6, and `gptq_gemm_rdna3` on the seven
layer shapes at M = 1, 2, 3, 4, 6, 8. Pre-registered: G1 proceeds if the two draft `lm_head` calls are
≥ 12 % of step GPU time; G4's row padding ships if Σt(M=4) ≤ 0.95 × Σt(M=3); G5 goes upstream if
wvSplitK at n=1 reaches < 75 % of the read ceiling; inter-kernel gaps ≥ 10 % of the step get written
up as a fusion target.

**Candidate 1 — pruned-vocabulary MTP draft head (G1).** The text plugin wraps
`MTPSpeculator.load_draft_model`, gathers `W_sub = lm_head.weight[ids]` (335 MB at V′ = 32,768) and
overrides `Qwen3_5MTP.get_top_tokens`; the manifest adds `"use_local_argmax_reduction": true` to
`speculativeConfig`; `ids` = a frequency-ranked subset plus every special and added token. Output is
unchanged: the target still verifies every token. Riskiest assumption: mean acceptance stays within
3 % on our mix (code, JSON tool calls, prose, some non-English). Kill-test: stage A offline, token
coverage ≥ 98.5 % at V′ = 32,768 on ≥ 200 held-out greedy answers (else try 65,536, else stop);
stage B on a canary at the B′ envelope, pass only if acceptance length ≥ 0.97× and position-0 ≥ 0.78,
decode ≥ +8 % at 2K and ≥ +6 % at 16K (≥ 74.0 / 69.9 tok/s against S4's 68.5 / 65.9), 2-request soak
median ≥ +5 %, pool ≥ 1.5×, probes valid; kill if acceptance < 0.95× or the 2K gain < +5 %. Optional
K=3 arm after B (M = 4 is one row tile). Touches `build/vllm-qwen35-text-plugin` (0.10.0, new
`draft_vocab.py` and a token-id table), its tests, `verify_vllm_gfx1100_native_w4a16.py`, a canary
and an offline coverage script. Effort S–M; confidence medium.

**Candidate 2 — Octave KV on the 0.33 image (replaces the agent's G3).** `kvCacheDtype: octave_k3v3`
then `octave_k3v4`, no `attentionBackend`, no backend in `speculativeConfig`, on the
`v0.33.0-rocm72` canary once its parity run passes (§6 S3). Riskiest assumption: Octave keeps ≥ 90 %
of segmented + fp16 decode at 2K and 16K with hybrid GDN, prefix caching and MTP on one card — none
of which upstream has published numbers for. Kill-test (from the vLLM report): record pool tokens at
0.96, decode at 512/2K/16K and 32K/64K, MTP position-0 acceptance, greedy parity against fp16 on the
three fixed prompts, 3 × 128K needle, APC hit tokens on a 7.6K repeat; kill on decode < 0.90× at 2K
or any hybrid/APC correctness mismatch. Upside: a pool in the hundreds of thousands of tokens makes
64K × 2 sequences fit on one card and could retire the overflow reroute (estimate). If Octave fails,
fall back to the agent's G3 (fp8 segmented on gfx11, pass decode ≥ 0.95× at 2K/16K, TTFT ≤ 1.15×,
pool ≥ 1.8×).

**Candidate 3 — input embeddings in pinned host memory (G2).** plugin-common wraps `load_model`
inside MRV2's memory-profiling window, copies each `embed_tokens` weight into pinned host memory and
swaps in a UVA view (`get_accelerator_view_from_cpu_tensor`); the MTP draft shares the module. MRV2
already uses the same mechanism (`UvaBuffer`) in our pods. Riskiest assumption: row gathers over
PCIe (Gen3 x16 on the 5930k, Gen3 x8 on the XTX) add ≤ 1 % decode and ≤ 2 % TTFT. Pass:
`status.kvPool` ≥ 82,000 tokens at 32K (≥ 1.55× S4's 52,932), decode ≥ 0.99×, TTFT ≤ 1.02× at
256/1,124/4,096, parity unchanged, 0 HIP faults over a 30-min soak; kill on < 1.4× pool or > 2 %
regression. Stacks with Octave or stands alone if Octave fails; also worth 2.0 GB on the 9B lanes.
Effort M. Clean upstream PR afterwards.

**Quick wins gated by step 0:** Q1 row padding (G4); Q2 segmented autotune tables at runtime width
(G7; bar: zero `jit_monitor` lines in the first 100 live requests after a cold-cache boot); Q3 relax
our FLA pruning (G10; bar: 16K prefill ≥ 3 % faster, boot ≤ +60 s); Q4 a tuned MoE JSON when the 35B
lanes reach 0.32 (G8); Q5 the `VLLM_SKINNY_GEMM_MAX_WEIGHT_BYTES` A/B on the next bump (G6).

## 6. Recommended slices and owner decisions

### 6.1 Slices

| # | Slice | Live impact | Touches | Depends on |
|---|---|---|---|---|
| S0 | **In flight today**: A1 GPUProfile fix, then !1234 (XTX runtime → 0.32 `527e2714`) | XTX runtime pod restart | `deploy/gpuprofiles/gfx1100.yaml`, `deploy/system/values-k3s.yaml` | — |
| S1 | Controller currency: A2 (dead partial-prefill flags), A3 (version-gated AITER env list), A4 (`--mamba-cache-mode` gate), A5 (four new keys), the `disableAiter` comment | none until a lane opts in | `backend/vllm.go`, tests, `docs/specs/crds.md` | — (can start now) |
| S2 | Plugin 0.10.0, 0.33-ready: B2 `hf_config_override` passthrough; Q2 (G7) autotune tables at runtime width; Q3 (G10) relaxed FLA pruning behind an env flag | none until an image carries it | `build/vllm-*-plugin/` | — (can start now; one writer for the plugin files) |
| S3 | `v0.33.0-rocm72` serving image (S2's plugins + diffusers 0.42.1), prewarm, then a parity canary on the XTX warm lane at the S3/S4 envelope with only the image changed. Pass: greedy parity hash, decode 66–72 tok/s at 512/2K/16K, position-0 acceptance ≥ 0.75, plugin boot line clean; include the G6 skinny-GEMM A/B | canary only | `build/Dockerfile.runtime-serving-v032` (or a v033 sibling), prewarm list, a debug canary | S2 |
| S4 | Step 0 decode-step anatomy (§5.3) | canary window | a debug canary | — (0.32 or 0.33 image) |
| S5 | Candidate 1, pruned-vocabulary draft head | canary, then the workhorse | text plugin | S2 (same files), S4's ≥ 12 % rule |
| S6 | KT-OCTAVE (candidate 2) | canary | manifests only | S3 |
| S7 | Candidate 3, embeddings in host memory | canary | plugin-common | S4; decide after S6 |
| S8 | gfx906 llama.cpp b11335 → b11847 (B4) and whisper.cpp pin (B5) | VII runtime roll, window | `build/Dockerfile.runtime-gfx906`, gfx906 GPUProfile certificates | trial build on ROCm 6.2 |
| S9 | Creative: VACE bf16-decode kill-test; First Block Cache / TaylorSeer on Wan denoise (quality-gated); Wan decode A/B on the 0.32 base before D1's in-pod Wan lanes | XTX canary | `build/server-diffusers.py` | D1 in-pod Wan (!1237/!1238) |
| S10 | Env A/Bs: drop `HSA_OVERRIDE_GFX_VERSION` and `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL` on gfx1100 | canary | GPUProfile env | — |
| — | gfx906 PyTorch ladder (2.11.0-rocm-7.2.1 → 2.14.1-rocm-10.1) | VII canary | `build/Dockerfile.runtime-gfx906` base | only when a VII PyTorch lane needs it |

S1 and S2 are live-neutral and touch disjoint files, so they can run as parallel sessions now. S3 is
the only path to the upstream fixes the pending D3 and D4 kill-tests depend on (next section).

### 6.2 What this changes in the November design

- **KT-QOS (D3) and KT-SO (D4) should be judged on 0.33, or with the 0.32 bugs in view.** 0.33 fixes
  priority self-preemption (#61976) and structured outputs with MTP plus the reasoning parser
  (#52477, #61574, #63423: grammar masks from GPU logit counts, no HTTP 500 on implicit reasoning
  end), and streams Qwen3 tool-call arguments incrementally (#62027). A KT-QOS or KT-SO failure on
  0.32 could be one of those upstream bugs rather than our design. Recommendation: run both canaries
  on the S3 image once its parity run passes; if they run on 0.32 first, a failure that matches one
  of these issues triggers a rerun on 0.33 rather than a verdict.
- **D1** is unaffected: the repin target `527e2714` is 0.32.0 and carries the Wan VAE bypass.
  For the in-pod Wan lanes (!1237/!1238), the 0.32 base means torch 2.13 and ROCm 7.2.3's MIOpen
  instead of `diffusers-video`'s torch 2.6 and ROCm 6.4.1; run the decode A/B there before promoting.
- **C11 (KV offload `fs` tier)** needs A5's `kvTransferConfig` pass-through. Lookahead block hashes
  (0.33) are disabled whenever a KV connector is configured, so measure both.
- **#68 (sleep mode)**: #52479 is still open; `--sleep-preserve-parameter-names 'draft.*'` (#61046)
  is the new lever, behind A5.
- **O7** is answered: keep vLLM on the Radeon VII, frozen (§4 D).

### 6.3 Owner decisions

| Id | Decision | Recommendation |
|---|---|---|
| P1 | Next vLLM: `v0.33.0-rocm72` now, or wait for v0.34 | 0.33-rocm72 through S2 → S3; never the default ROCm 10 tag yet |
| P2 | Judge KT-QOS and KT-SO on 0.33 | yes, or treat a 0.32 failure matching #61976/#61574/#63423 as a rerun trigger |
| P3 | Order of the gfx1100 kernel candidates | S4 (measure) → S5 (draft head) and S6 (Octave) in parallel canaries → S7 only if the pool is still the bottleneck |
| P4 | ROCm 10 canary | defer until a ROCm 10.2 MIOpen or a vLLM feature needs it; the 5930k's disk headroom is the cost |
| P5 | whisper.cpp: pin master ≥ `e4acdfdf` or wait for v1.9.6 | wait two weeks, then pin master if nothing ships |
| P6 | Start S1 and S2 as sessions now | yes; both are live-neutral |
