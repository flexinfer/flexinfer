# Research: Model Quality + Context Uplift for the Two gfx1100 Nodes (2026-08-15)

**Question**: What model/quant/serving combo gives a genuinely reliable coding + general-purpose
lane on the two 7900 XTX (gfx1100, 24 GB) nodes — good enough to slot into loom-core Mills,
Council, and general agent scenarios — replacing the current Qwen3.5-35B-A3B GPTQ workhorse,
which has known quality/quant issues?

**Verdict (TL;DR)**: **Qwen3.6-27B (dense, clean) at INT4 AutoRound (GPTQ-format packing)** is
the target. It is the strongest open model that fits a single 24 GB card as of Aug 2026 —
77.2 SWE-bench Verified, beating Qwen's own previous 397B flagship on every coding benchmark —
and it is the *same modeling code* our vLLM 0.23 gfx1100 runtime already serves. Ready-made
quality INT4 artifacts exist (Intel AutoRound), so the kill-test is a serve-and-probe, not a
12-hour self-quant. Secondary: canary **Muse Glimmer 30B** (Meta, Aug 10) via the llamacpp
backend as a Mills tool-orchestration/judge specialist. Reject: Qwen3-Coder-Next PP=2,
GLM-4.7-Flash as primary, all FP8/AWQ/W8A16 artifacts.

---

## 1. Why the current workhorse feels weak (diagnosis)

Current primary: `qwen35-35b-clean-gptq-workhorse{,-128k}` — Qwen3.5-35B-A3B, self-quant
GPTQ W4 g128 (GDN-preserving), 128K qualified, ~79 tok/s graph mode.

Two compounding causes:

1. **Capacity ceiling**: A3B = ~3B active params/token. Qwen's own benchmark table shows the
   gap: Qwen3.6-35B-A3B scores 73.4 SWE-bench Verified / 28.7 SkillsBench, vs 77.2 / 48.2 for
   the dense 27B. And we run the *previous generation* (3.5) of the A3B.
2. **Vanilla-GPTQ quant loss**: measured on Qwen3.6-27B by btbtyler09: vanilla GPTQ W4 g128 =
   **+8.45% perplexity** vs BF16; AutoRound/FOEM-class W4 = **+1.95%**
   (https://huggingface.co/btbtyler09/Qwen3.6-27B-GPTQ-4bit). Our in-house pipeline is vanilla
   GPTQ with default calibration (128 samples) — we are paying roughly the +8% tier on top of
   the A3B ceiling. This is the concrete mechanism behind "works but quality/quant issues."

Context is NOT the problem — the 128K lane passed 5/5 needles at 129,919 tokens. Quality is.

## 2. The Aug-2026 candidate field (24 GB-class)

| Model | Arch | SWE-V | Terminal-Bench 2.0 | SkillsBench | MCP Atlas | Ctx (native) | Fits 24 GB @W4 | vLLM ROCm today |
|---|---|---|---|---|---|---|---|---|
| **Qwen3.6-27B** (Apr 2026) | dense 27B, hybrid GDN 3:1, multimodal, MTP | **77.2** | **59.3** (= Claude 4.5 Opus) | **48.2** | 62.5 | **262,144** | yes (~14-15 GB LM-only) | **yes — same Qwen3.5 class our 0.23 runtime serves** |
| Qwen3.6-35B-A3B (Jun 2026) | MoE A3B hybrid GDN | 73.4 | 51.5 | 28.7 | — | 262,144 | yes (~20.5 GB) | yes (proven: mtp-uncensored lane) |
| Muse Glimmer 30B (Meta, **Aug 10 2026**) | dense 30B, SWA 3:1 local:global, multimodal, DFlash drafter | 65.9 | 60.7* | — | **75.5** | 131,072 (→262K) | yes (official 17 GB K-quant GGUF) | not yet (PR in flight; ROCm path = AITER/CDNA). llama.cpp b10353+ works, ROCm supported |
| GLM-4.7-Flash (Jan 2026) | MoE 30B-A3B (glm4_moe_lite) | mid | — | — | — | 128K | yes | day-0 vLLM |
| Qwen3-Coder-Next (80B-A3B) | MoE hybrid GDN, coding-specialized | 70.6 | — | — | — | 262,144 | **no — needs ~40 GB → PP=2 both nodes** | arch supported; cross-host Ray needed |
| Qwen3.5-35B-A3B (current) | MoE A3B hybrid GDN | (below 73.4) | 41.6-class | 27-class | — | 262,144 | yes | live today |

\* Muse Glimmer's Terminal-Bench number is from Meta's comparison (Qwen3.6-27B leads at 60.7 in
their harness); Qwen's own harness reports 59.3 for the 27B. Cross-vendor numbers are
directionally consistent: **Qwen3.6-27B wins coding; Glimmer wins MCP/tool orchestration.**

Sources: https://qwen.ai/blog?id=qwen3.6-27b (full table, Apr 21 2026),
https://huggingface.co/Qwen/Qwen3.6-27B,
https://huggingface.co/meta-models/Muse-Glimmer-30B,
https://www.marktechpost.com/2026/08/10/meta-ai-releases-muse-glimmer,
https://www.marktechpost.com/2026/01/20/zhipu-ai-releases-glm-4-7-flash-a-30b-a3b-moe-model-for-efficient-local-coding-and-agents.

Key headline from Qwen: the dense 27B **beats Qwen3.5-397B-A17B (397B MoE) on every major
coding benchmark** — SWE-V 77.2 vs 76.2, SWE-Pro 53.5 vs 50.9, Terminal-Bench 59.3 vs 52.5,
SkillsBench 48.2 vs 30.0. Dense at 27B is the new local-coding quality frontier, and it's the
"most widely deployed open-source scale" release the community asked for.

## 3. Why Qwen3.6-27B is the right fit for THIS fleet (not just benchmarks)

- **Runtime already proven**: Qwen3.6 IS Qwen3.5 modeling code (verified in-repo:
  `deploy/models/qwen36-27b-gptq.yaml` header). Our source-built **vLLM 0.23 gfx1100 runtime
  with the native RDNA3 W4A16 HIP operator** already served a Qwen3.6-27B GPTQ coherently —
  the Fable Fusion lane (`qwen36-27b-fable-gptq`, qualified 2026-07-21). Same
  `hfOverrides: Qwen3_5ForCausalLM` + `partial_rotary_factor 0.25` recipe applies.
- **The two prior 27B failures don't indict the clean model**:
  - `qwen36-27b-gptq` (garbage output) was **abliterated** — known GDN-abliteration failure
    mode (refusal-direction edits break the delta-rule recurrence; documented in memory).
  - `qwen36-27b-fable-gptq` (coherent, 8.7 tok/s) is a community merge, served **eager-only**
    at an 8K envelope — perf and context were simply never qualified, not disproven.
- **Hybrid GDN = cheap long context**: only 1 in 4 layers carries a KV cache. 262K context
  fits a 24 GB card "with room to spare" (community W8A16 card measurements). Our 35B lane
  holds 196K tokens in 1.94 GiB KV at fp8_e4m3 — the 27B has similar full-attn layer counts.
  Native 262,144 ctx means **no RoPE-extrapolation cliff** anywhere in our usable range
  (unlike the 32K-trained uncensored 35B that collapsed at ~73K).
- **VRAM math**: W4 language-model-only weights ~14-15 GB (`languageModelOnly: true` skips the
  vision tower) → ~8-9 GB for KV/graphs on 24 GB. Comfortable at 128K, plausible at 262K.
- **Tool calling**: `toolCallParser: qwen3_coder` already proven in-fleet; Qwen recommends
  thinking mode + `preserve_thinking` for agentic use; `--reasoning-parser qwen3`. Qwen3.6
  fixed the Qwen3.5-era tool-calling template breakage the community complained about.
- **MTP is native**: the 27B ships a multi-token-prediction head; vLLM speculative config
  `{"method":"qwen3_next_mtp","num_speculative_tokens":2-3}`. Community evidence (RTX 3090,
  same 936-960 GB/s bandwidth class as 7900 XTX): **85 tok/s decode, 125K ctx, vision on, with
  AutoRound INT4 + MTP accept-length 3.4-3.8**
  (https://medium.com/@fzbcwvv/an-overnight-stack-for-qwen3-6-27b-85-tps-125k-context-vision-on-one-rtx-3090-0d95c6291914).
  Fleet already has MTP experience (qwen36-35b mtp-draftq artifact).

## 4. Quant selection (the quality fix)

**Do NOT self-quant vanilla GPTQ this time.** Ranked options:

1. **Intel/Qwen3.6-27B-int4-AutoRound** (https://huggingface.co/Intel/Qwen3.6-27B-int4-AutoRound)
   — official Intel AutoRound, g128. First choice to pull and kill-test.
2. **Lorbus/Qwen3.6-27B-int4-AutoRound** (https://huggingface.co/Lorbus/Qwen3.6-27B-int4-AutoRound)
   — W4A16, g128, **sym=true**, packing **`auto_round:auto_gptq`** (GPTQ kernel path →
   ExllamaLinearKernel / native RDNA3 W4A16 op), leaves `linear_attn.in_proj_a/b`, `mtp.fc`,
   norms, and router gates unquantized (GDN-safe), and **explicitly fixes the mtp.fc INT4
   packing bug** that gives 0% MTP acceptance (vLLM name mismatch `fc.qweight` vs `fc.weight`).
   Preferred if we want MTP working out of the box; check Intel's artifact for the same fix.
3. **btbtyler09/Qwen3.6-27B-GPTQ-4bit** (FOEM) — PPL evidence source; GPTQ-format fallback.
4. **Self-quant AutoRound** (in-house) — only if HF artifacts fail on our runtime. AutoRound
   provenance on ROCm Exllama is already proven in-fleet (kaitchup Llama-3.3-70B AutoRound
   GPTQ, Track A kill-test PASS 2026-06-11). Export `format=auto_gptq`, sym=true, g128; keep
   GDN in_proj + mtp.fc + gates in BF16 (mirror Lorbus's exclusion list).

**Avoid on gfx1100** (all verified this cycle or in memory):
- Official **FP8** (~28 GB, and gfx1100 has no FP8 compute) — hardware-blocked.
- **AWQ-INT4** community quants (drawais/cyankiwi) — Triton AWQ kernel = 9 tok/s class on RDNA3.
- **W8A16 compressed-tensors** (88plug, ~27 GB) — doesn't fit 24 GB.
- **NVFP4** (nvidia/Qwen3.6-27B-NVFP4) — Blackwell-only.

Known small bug to expect: vLLM ≤0.19.x `Qwen3_5TextConfig.ignore_keys_at_rope_validation`
is a list (TypeError at config parse); patch documented on the btbtyler09 card. Our 0.23
build may already carry the fix.

## 5. Recommended plan (slices)

1. **Kill-test (riskiest assumption first)**: Intel (or Lorbus) AutoRound INT4 serves
   coherently on the pinned vLLM 0.23 gfx1100 runtime.
   - Clone the fable lane Model CR as `qwen36-27b-clean-*` canary: `languageModelOnly: true`,
     hfOverrides per fable lane, `maxModelLen: 8192` start, eager, fp8 KV off initially
     (fable used `kvCacheDtype: auto`), minReplicas 0, on whichever card is free.
   - Pass = deterministic greedy coherence + 5-prompt smoke (the fable gauntlet).
   - Watch: `auto_round:auto_gptq` packing detection on our build (`quantization: gptq` vs
     `auto_round`); unquantized GDN `in_proj_a/b` mixed-precision load (analog of the old
     patch-16 .weight/.qweight fixup — fable lane proves dense mixed load works).
2. **Graph-mode + throughput qualification**: `enforceEager: false` (FULL_AND_PIECEWISE with
   GDN in splitting_ops — proven on the 35B hybrid). Expect ~30-45 tok/s decode; the 35B went
   9.5 → 74.6 tok/s from graphs alone. Then **MTP speculative** (`qwen3_next_mtp`, 2-3 tokens)
   targeting 60-85+ effective tok/s per the 3090 datapoint.
3. **Context ladder**: 32K → 64K → 128K with `kvCacheDtype: fp8_e4m3` + hybrid KV manager
   (do NOT set disableHybridKVCacheManager), needle-bench each rung with
   `scripts/context-needle-bench.py` (depth grid + multi-needle). Native 262K = no cliff
   expected; qualify 128K first (Mills envelope), stretch later.
4. **Promotion**: take over `workhorse` / `loom-workhorse` / `mills-council` (+`-128k`)
   aliases; thinking mode on, `preserve_thinking` for Mills/Council agents; run both nodes
   like today's pair (one warm primary, one on-demand 128K sister). Keep the 35B-A3B lanes
   demoted-but-servable as fallback until the 27B soaks; then retire.
5. **Parallel canary (cheap)**: Muse Glimmer 30B via **llamacpp backend** — official
   `meta-models/Muse-Glimmer-30B-GGUF` K-Quant-17GB (+ optional `dflash-kquant.gguf` drafter,
   `--spec-type draft-dflash`), needs llama.cpp **b10353+** (arch merged 2026-08-10, PR
   #26841, includes ATEM tool-call parser; ROCm is a supported backend). Our llamacpp image
   will need a rebuild to b10353+. Value: MCP Atlas 75.5 vs Qwen's 62.5 → Mills MCP
   orchestration + LLM-as-judge lane, plus vision. Revisit vLLM serving when the Glimmer PR
   merges and community GPTQ/AWQ quants appear (the documented ROCm path uses AITER =
   CDNA-oriented; gfx1100 needs the non-AITER attention path we always use).

## 6. Rejected options (and why)

- **Qwen3-Coder-Next 80B-A3B on PP=2 across both nodes**: 70.6 SWE-V is *below* the
  single-card 27B's 77.2; costs both cards, cross-host Ray/RCCL fragility, displaces video +
  psyche lanes. Coding-specialist branding doesn't beat the dense generalist here. Skip.
- **GLM-4.7-Flash 30B-A3B**: well-liked, day-0 vLLM, but it's another 3B-active MoE — the
  same capacity class we're leaving. Possible future *speed* lane, not the quality fix.
- **Requant Qwen3.6-35B-A3B (clean)**: lowest-effort incremental (+SWE-V 73.4, proven serving
  path via the mtp-uncensored artifact), but dominated by the 27B on every benchmark at
  similar VRAM. Only do it if the 27B fails throughput needs for batch/council fan-out
  (A3B decodes ~2x faster than dense at C=1).
- **Kimi K2.x / GLM-5.2 / DeepSeek V4 / MiniMax M3**: all >40 GB at W4 — out of envelope.
- **Muse Glimmer as PRIMARY**: loses coding head-to-head (SWE-V 65.9 vs 77.2); 5 days old;
  vLLM ROCm path not landed. Canary only.

## 7. Open questions / risks

1. `auto_round:auto_gptq` packing on our 0.23 build — does the native RDNA3 W4A16 op engage?
   (Mitigation: artifact config declares gptq-compatible checkpoint format; fallback
   `--quantization gptq`, then FOEM GPTQ artifact, then self-quant AutoRound w/ auto_gptq export.)
2. Graph capture on *dense* GDN hybrid (35B MoE proven; dense should be the easier case).
3. MTP + cudagraph + ROCm interaction — qualify separately after graphs (Intel card shows MTP
   flags on plain vLLM; Lorbus tested on 0.19.1 nightlies).
4. Council concurrency: dense 27B at maxNumSeqs 2-4 vs A3B's cheap batching — measure agg
   tok/s at C=2/4 during slice 2; keep the A3B for high-fan-out if needed.
5. Vision (multimodal) intentionally deferred: `languageModelOnly: true` first; a vision-on
   variant is a later bonus lane (the 3090 stack runs vision + 125K on 24 GB, so it fits).

## 8. Slice-1 kill-test RESULT (2026-08-16): **PASS — riskiest assumption dead**

`deploy/debug/qwen36-27b-clean-autoround-canary.yaml` served
Intel/Qwen3.6-27B-int4-AutoRound coherently on the pinned vLLM 0.23 gfx1100
runtime on cblevins-5930k: **6/6 greedy gauntlet PASS** (exact " Paris.",
correct Fibonacci code, valid JSON extraction, 14-apples arithmetic, clean
summaries; zero degenerate output). Weights loaded in 43.7 s with no skipped
tensors; KV pool 46,811 tokens at the 8K/fp16 envelope; **eager single-stream
8.3 tok/s** — the same eager floor as the fable lane (8.7), as predicted;
throughput comes from slice 2 (graph mode + MTP).

Blockers hit and solved (all encoded in the canary YAML):
1. **auto-round → inc rejection**: vLLM 0.23 resolves `quant_method:
   "auto-round"` to `inc` (Intel Neural Compressor) *before* honoring
   `--quantization`, and ROCm rejects inc. `--quantization gptq` alone does
   NOT fix it. FIX: `hfOverrides` rewrites `quantization_config` wholesale to
   plain GPTQ form (bits 4, g128, sym, desc_act false) +
   `modules_in_block_to_quantize` = exact INT4 census from the safetensors
   index (attn q/k/v/o, mlp gate/up/down, linear_attn in_proj_qkv/in_proj_z/
   out_proj; in_proj_a/b + conv1d + norms + lm_head are FP16). vLLM then logs
   `Using RDNA3W4A16LinearKernel for AutoGPTQLinearMethod` — the native fast
   kernel — plus the Triton/FLA GDN prefill kernel.
2. **Flash-loader tmpfs**: 18 GB of shards vs 7.7 GB tmpfs budget on the 63Gi
   node → `flashLoader.enabled: false` (wan21 posture).
3. **Demand-window vs cold load**: the ~4-6 min cold load outlives the 2-min
   demand window → the warm primary (gemma4-5930k) preempts an on-demand
   start mid-load, and `minReplicas: 1` alone does not win the election back.
   Qualification windows need `minReplicas: 1` + `gpu.forcePromotion: true`,
   reverted afterward. Also: `litellm.enabled: false` parks a lane entirely
   (never cold-started by name) — demand-only canaries need litellm enabled
   with the unique alias.
4. Serve the artifact's stock multimodal wrapper class with
   `languageModelOnly: true` — do NOT force the text class (that was for
   text-layout artifacts like fable's).

Next: slice 2 — `enforceEager: false` graph qualification (expect ~30-45
 tok/s), then MTP speculative (`qwen3_next_mtp`); slice 3 — context ladder
32K→128K with fp8 KV + needle bench.

## 8b. Slice-2 RESULT (2026-08-16): **graph mode QUALIFIED at 39 tok/s; MTP tested and disabled**

Graph mode (`enforceEager: false` + persistent compilationCache):
- CompilationMode VLLM_COMPILE, cudagraph FULL_AND_PIECEWISE, with
  `vllm::qwen_gdn_attention_core` in splitting_ops (GDN runs eager inside the
  piecewise graph — the hybrid-safe path, same shape as the 35B win).
- torch.compile 124.9 s fresh, AOT saved to
  `/var/lib/flexinfer/compile-cache-qwen36-27b-autoround-5930k`; subsequent
  boots reuse it (~3 min cold start incl. 43 s weight load).
- **6/6 gauntlet coherent; single-stream greedy decode 39.0 tok/s = 4.7×
  eager (8.3)** — mid-prediction band. Council-concurrency (C=2/4 agg) still
  to measure in slice 3/4.

MTP speculative — tested, currently a no-win, DISABLED for promotion:
- Blocker found: the DRAFT ModelConfig re-reads config.json from disk and
  does NOT inherit hf_overrides → the auto-round→inc ROCm rejection returns
  inside SpeculativeConfig. Fixed by patching quantization_config in the
  PVC's `/models/config.json` to the plain-GPTQ form (backup kept:
  `config.json.bak-autoround`). A promoted lane must bake this into the
  published artifact or an entrypoint normalize — the PVC edit is lost if the
  cache re-stages.
- `qwen3_next_mtp` is deprecated on 0.23 (maps to `mtp`).
- Acceptance is the real blocker: pos-0 ≈33% with either method (2-token:
  33.7%/3.3% per-pos, net 35.0 tok/s; 1-token: 33.0%, net 39.4 tok/s vs 39.0
  baseline). Reference stacks report ~97% pos-0 on 0.19-nightly forks, so the
  draft head is likely mishandled by this runtime/artifact combo (candidates:
  INT4-quantized mtp.layers.0, overlay MTP weight path). Coherence unaffected
  (6/6 with MTP on). Revisit on a newer runtime or with an artifact that
  keeps the MTP block FP16.

Ops note repeated from slice 1: qualification windows need `minReplicas: 1` +
`gpu.forcePromotion: true` (reverted after; lane is back to demand-only).

Next: slice 3 — context ladder 32K→64K→128K with `kvCacheDtype: fp8_e4m3`,
needle-bench each rung (`scripts/context-needle-bench.py`), then promotion
(aliases, both nodes) as slice 4.

## 8c. Slice-3 RESULT (2026-08-16): **coherent to 64K; decode-at-context is the real fleet ceiling**

Coherence: **PASS.** Needle recall 1/1 at 32K and 64K (fp8-KV 128K envelope);
128K + fp8 KV boots cleanly with a 137,098-token KV pool (1.05× concurrency at
131,072). No RoPE cliff — native-262K arch. Two bench-harness gotchas for this
model: thinking mode needs `--max-tokens 2048` (the answer follows a long
`<think>`), and the dense 27B needs `--timeout 900` (the bench's 240s default
was tuned on the A3B).

The real finding — **decode speed collapses linearly with context length** on
the unified Triton attention path (gfx1100, vLLM 0.23):

| context | decode fp8 KV | decode fp16 KV |
|---|---|---|
| ~0 | 39 tok/s | 39-42 tok/s |
| 2K | 17.2 | ~25 |
| 30K | 1.9 | 4.0 |
| 60K | — | 2.0 |

Cost model: ~25.6 ms + 16.6 µs/ctx-token (fp8) or ~7.5 µs (fp16) per decoded
token — the fp8 dequant path is ~2× worse *per byte*, and both are far off
bandwidth (serialized KV walk, no split-K/flash-decoding). Prefill is fine
(946-1049 tok/s ≤14K, 591 @30K — normal quadratic decay).

A/B eliminations: `rocmCustomPagedAttn: true` = no-op for this hybrid arch;
`attentionBackend: FLASH_ATTN` + `FLASH_ATTENTION_TRITON_AMD_ENABLE=TRUE` =
"FlashAttention version not detected" (not compiled into this runtime image).

**Fleet context**: the incumbent lanes share this ceiling — back-computing the
35B workhorse-128k qualification (129,919-token prompt, 297s total, ~245s
prefill) puts its needle-answer decode at ~2-3 tok/s. Long-context decode was
never separately measured before; the 27B is not a regression, the fleet has
been living on short-context decode + long-context *recall*.

**Parked posture**: 64K, fp16 KV, graph mode, no MTP, demand-only.

**Slice-4 options** (promotion + the decode fix):
1. Promote the 27B at 64K/fp16 now for QUALITY (it beats the 35B everywhere on
   benchmarks; decode at working-set contexts 2-16K is 12-25 tok/s — usable
   for Mills turns), keep the 35B-128k lane for >64K recall jobs.
2. Runtime work (the real unlock, benefits every lane): RDNA3 decode-attention
   split-K/flash-decoding in the unified Triton kernel, or build the Triton-FA
   package into the runtime image; plus the fp8-KV dequant fix. File as a
   runtime-image backlog item.
3. Re-check newer vLLM (0.24/0.25) ROCm attention for RDNA3 improvements
   before writing custom kernels.

## 8d. Slice-4 RESULT (2026-08-16): **PROMOTED — shipped via MR !965, routed kill-test PASS**

`qwen36-27b-autoround-workhorse` (cblevins-5930k, 64K/fp16-KV/graph, no MTP)
now owns the base `workhorse` / `loom-workhorse` / `mills-council` /
`reasoning` aliases; the 35B lanes keep only `*-128k` + identity aliases for
>64K recall. Merged to master as MR !965 and rolled by Flux.

**Ops discoveries during promotion:**
1. **Flux `flexinfer-models` is ACTIVE again** (unsuspended, prune=true,
   reconciling ./deploy from master) — the June "suspended + direct-apply"
   posture is DEAD. Direct kubectl edits to Model CRs get reverted within a
   reconcile cycle; promotions must go through git. (This is why the first
   alias flip silently un-applied.)
2. The plain `workhorse` alias had been quietly 503ing ("parked behind a
   higher-priority primary") since the 2026-07-18 psyche layout — the 35B
   base lane at priority 300 was statically parked behind gemma4's 385 warm
   primary. The 27B lane's priority 500 borrow-idle posture fixes the role
   route as a side effect.
3. The retrying-client demand pattern (poke every ≤60s) carries a ~10-min
   cold start (19 GB prefetch + boot) through the 2-min demand window —
   validated end-to-end: `workhorse` routed, cold-started, and returned
   ROUTE_OK through the proxy; `mills-council` answered the apples probe
   correctly with clean content (reasoning parser strips <think>; the proxy
   does not forward reasoning_content — content is clean either way).

**Full program status**: slices 1-4 complete. Remaining follow-ups:
(a) runtime RDNA3 decode-attention split-K work — spawned as its own task;
(b) optional 7900xtx sister lane + 262K stretch after the decode fix;
(c) MTP revisit (needs FP16-draft artifact or runtime fix + PVC config patch);
(d) Muse Glimmer llamacpp canary (needs llama.cpp b10353+ image) — untouched
    this cycle.

## 8e. Decode-ceiling RESOLUTION (2026-08-16): **backend misattribution — fix is one config line, 9-16x**

The §8c ceiling was attributed to the unified Triton attention kernel. It is
not that kernel. The deployed lanes never ran it: on Radeon, vLLM's backend
priority selects **ROCM_ATTN** by default (`rocm.py`:
`Overriding with ROCM_ATTN out of potential backends: ['ROCM_ATTN',
'TRITON_ATTN']` — visible in every canary boot log), and ROCM_ATTN's decode
path has **no KV-length parallelism** at all. The actual unified Triton kernel
(TRITON_ATTN backend) has carried a 3D split-KV decode path (flash-decoding
style, `NUM_PAR_SOFTMAX_SEGMENTS=16` + `reduce_segments`) since before v0.23,
and its gate (pure-decode batch, `num_seqs <= 128//num_kv_heads`) passes for
our single-stream decode shapes.

**Kernel microbench** (`deploy/debug/gfx1100-unified-attn-decode-probe.yaml`,
deployed v0.23 digest, 27B full-attn geometry 24q/4kv/hd256, 1-seq decode,
per-layer):

| ctx | 2D serialized | 3D split-KV (16 seg) | KV read |
|---|---|---|---|
| 30720 fp16 | 4.63 ms | 0.506 ms | 27 → 248 GB/s |
| 61440 fp16 | — | 0.516 ms | 487 GB/s |
| 30720 fp8 | 12.7 ms | 0.508 ms | 4.9 → 124 GB/s |
| 61440 fp8 | — | 0.956 ms | 132 GB/s |

16 segments already saturates at 60K (32/64 segments: no further gain). The
fp8 dequant penalty is hidden by the split path to ~30K and costs 1.85x at
60K — livable; no custom dequant work needed for current envelopes.

**End-to-end canary validation** (`attentionBackend: TRITON_ATTN` on
qwen36-27b-clean-autoround-canary, parked 64K/fp16/graph posture, 128-token
greedy decode-at-context probe):

| ctx | decode before | decode after | prefill after |
|---|---|---|---|
| 2K | ~25 tok/s | **41.7** | 1134 tok/s |
| 30K | 4.0 | **36.3** (9.1x) | 543 |
| 60K | 2.0 | **32.2** (16x) | 316 |

Slope collapsed from 7.5 µs/ctx-token to ~0.12 µs (~60x). Boot log confirms
`Using TRITON_ATTN backend (selected via --attention-backend)`.

**Fleet rollout**: `config.attentionBackend: TRITON_ATTN` added to the six
v0.23 gfx1100 qwen lanes (27B autoround workhorse, 27B fable, 9B RP x2,
35B workhorse x2). Excluded: MTP/spec-decode lanes (verify-step qlen > 1
fails the 3D gate — they'd fall back to the 2D unified path; separate
follow-up), gemma4 lanes (different arch family, existing explicit
overrides), gfx906 lanes (different image).

**Upstream check (closes 8d follow-up (a) and slice-4 option 3)**: v0.24.0/v0.25.0/v0.26.0 diff of
`triton_unified_attention.py` + `triton_attn.py` + `rocm.py` shows no RDNA3
decode changes and the same ROCM_ATTN-first default — the already-built
v0.26 image (`rocm-gfx1100-v026-native-w4a16`, harbor 2026-08-01) needs the
same per-lane override when adopted. Triton-FA package (§8c option 2) is
moot for decode: TRITON_ATTN suffices.

## 9. Source index

- Qwen3.6-27B release blog + full benchmark table: https://qwen.ai/blog?id=qwen3.6-27b
- Model card: https://huggingface.co/Qwen/Qwen3.6-27B (262K native, YaRN beyond; vLLM ≥0.19)
- Quants: https://huggingface.co/Intel/Qwen3.6-27B-int4-AutoRound ·
  https://huggingface.co/Lorbus/Qwen3.6-27B-int4-AutoRound ·
  https://huggingface.co/btbtyler09/Qwen3.6-27B-GPTQ-4bit (PPL: vanilla GPTQ +8.45% vs FOEM +1.95%) ·
  https://huggingface.co/88plug/Qwen3.6-27B-W8A16 (262K-on-24GB KV note)
- 85 tok/s @125K on 3090 (AutoRound + MTP recipe + mtp.fc gotcha):
  https://medium.com/@fzbcwvv/an-overnight-stack-for-qwen3-6-27b-85-tps-125k-context-vision-on-one-rtx-3090-0d95c6291914
- Muse Glimmer: https://huggingface.co/meta-models/Muse-Glimmer-30B ·
  https://huggingface.co/meta-models/Muse-Glimmer-30B-GGUF (llama.cpp b10353+, PR #26841) ·
  https://unsloth.ai/docs/models/muse-glimmer · https://recipes.vllm.ai/meta-models/Muse-Glimmer-30B
  (vLLM PR-gated; ROCm = AITER path) ·
  https://www.amd.com/en/blogs/2026/run-meta-muse-glimmer-30b-on-amd-ryzen-ai-max-and-radeon-gpus.html
- Qwen3-Coder-Next: https://huggingface.co/Qwen/Qwen3-Coder-Next (80B-A3B, 256K, 70.6 SWE-V)
- GLM-4.7-Flash: https://unsloth.ai/docs/models/tutorials/glm-4.7-flash
- Landscape surveys (Jun-Aug 2026): https://pinggy.io/blog/best_open_source_self_hosted_llms_for_coding ·
  https://atomic.chat/blog/guides/best-local-llms-for-coding
- In-repo evidence: `deploy/models/qwen36-27b-fable-gptq.yaml` (0.23 runtime + native W4A16 op,
  dense 27B GPTQ qualified), `deploy/models/qwen36-27b-gptq.yaml` (abliterated failure — not
  arch), `deploy/models/qwen35-35b-clean-gptq-workhorse*.yaml` (current envelope + graph/fp8-KV
  recipe), `scripts/context-needle-bench.py` (context qualification tool).
