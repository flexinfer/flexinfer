# Research: Next Most Impactful gfx1100 Feature (2026-10-23)

**Question**: With both 7900 XTX nodes back in service, what is the next most
impactful thing to build for gfx1100 in FlexInfer, given the current state of
ROCm, vLLM and llama.cpp, and are there new model candidates for a 24 GB card?

**Verdict (TL;DR)**: **Turn on MTP speculative decoding for the Qwen3.8-27B
production lanes, carried by a vLLM 0.32 native-W4A16 runtime.** The 5930k
workhorse is now the busiest card in the fleet (12,048 requests and 4.29 M
generated tokens in 7 days) and every request is decode-bound at ~40 tok/s
(p50 8.25 s for a ~356-token answer). MTP is the only lever that roughly
halves that without a new card, the artifact already carries the head, the
35B-A3B lane already proved the gfx1100 MTP machinery (80.18 % acceptance),
and the two upstream GDN-speculation fixes the fleet lacks landed in 0.28 and
0.29. The same runtime bump brings the new RDNA segmented-attention backend
(0.32), the ROCm sleep-mode crash fix, and ROCm 10.1's official kernel 7.0
support line. Secondary: refresh the gfx1100 llama.cpp image (b8173 →
b11808) so the GGUF lanes get `draft-mtp` and the Nemotron 3.5 Bolt 74B-A9B
canary becomes possible. No September–October model release beats
Qwen3.8-27B on a single 24 GB card for coding; Bolt is a speed/agentic lane,
not a quality upgrade.

Branch: `claude/gfx1100-flexinfer-support-904425`. Live numbers from
Prometheus (`vllm:*`, `amdgpu_*`, `flexinfer_*`) and the flexinfer MCP on
2026-10-23 ~20:30Z; upstream state from the vLLM/ROCm/llama.cpp release pages
and PRs cited in §8.

---

## 1. Where the two cards are today

| Card | Lane (Ready) | Runtime | Envelope | 7d requests | 7d gen tokens | p50 / p95 e2e | GPU busy 7d |
|---|---|---|---|---|---|---|---|
| cblevins-5930k | `qwen38-27b-autoround-workhorse` (workhorse / reasoning / loom-workhorse) | vLLM 0.26 `ad93ae72` (native RDNA3 W4A16) | 64K, fp8 KV, 2 seqs, TRITON_ATTN, graph, GDN APC | **12,048** | **4.29 M** (17.45 M prompt) | **8.25 s / 29.4 s** | 19.6 % |
| cblevins-7900xtx | `qwen38-27b-xtx-warm-canary` (mills-warm / mills-council) | same digest | same, 2100 MHz clock cap | 336 (parked until 19:11Z today) | 93 K | 9.6 s / 14.5 s | 46.7 % (soaks/burns) |

What the shape says:

- Mean request on the workhorse is ~1,450 prompt / ~356 generated tokens. At
  40.6 tok/s single-stream the generation alone is ~8.8 s, which is the p50.
  Average running requests is 0.19, so this is **per-request decode latency,
  not queueing**. Prefix caching already absorbs the prompt side.
- All Mills traffic landed on the 5930k while the XTX was parked (10-10 →
  10-23). The XTX lane is back under a 2100 MHz overdrive cap (~6 % below
  uncapped: 40.3 vs 43.0 tok/s); the card has never survived sustained load
  above ~2140 MHz on either kernel and BIOS 1811 → 3854/3881 is still open
  (matrix rows `xtx-vllm-soak-2026-10-23`, `gfx1100-native-w4a16-2026-10-22`).
- The idle-card problem of September is solved (slice 1 of
  `10-research-flexinfer-utilization-loom-mills-2026-09-13.md`); the new
  problem is that the card that is used is slow per token.

## 2. What moved upstream since the fleet's vLLM 0.26 (2026-07-27)

| Release | Date | gfx1100-relevant | Breaking for us |
|---|---|---|---|
| 0.28 | 2026-08-26 | Qwen3.8 enabled on ROCm (#50068); **GDN gates aligned with speculative tokens in mixed batches (#51812, merged 08-11)**; fused post-conv MTP decode kernel for Qwen3.5 GDN (#51674); Muse Glimmer (#51655); prefix caching default-on for Mamba (#50991); DeepSeek-V4 on gfx11; Torch 2.12 / Triton 3.7 | `--calculate-kv-scales` removed (#49389); bitsandbytes moved out of tree (#43529); transformers 5.15 |
| 0.29 | 2026-09-09 | **Fused GDN MTP for all Qwen head ratios (#52539)**; Mamba prefix-cache internal prefill checkpoints, 9–25 % TTFT (#52789); dense retention restored for hybrid MTP (#55760); per-request acceptance stats (#48915); Qwen3.8-Flash-Next | MRV1 deprecated, removal targeted 0.32 |
| 0.30 | 2026-09-22 | MTP/EAGLE under PP on MRV2; adaptive verification for draft speculators | `mamba_cache_mode=all` deprecated (we use `align`) |
| 0.31 | 2026-10-05 | Draft-model speculation on MRV2 (#43091); LiLiCorr drafter (#57934); Granite 4.2 thinking parser | — |
| 0.32 | 2026-10-18 | **`ROCM_SEGMENTED_ATTN` for RDNA3/3.5/4 (#59132, merged 10-06)**: on a 7900 XTX 1-stream 10.15 → 47.38 tok/s vs ROCM_ATTN, native multi-token verify (spec qlen > 1), head 64/128/256, GQA 1–16, **fp8 KV gfx12-only**; gfx11 W4A16 row-stride tuning (#56301, gated to gfx1151); **MRV2 default on ROCm, MRV1 exceptions removed (#56360)**; sleep-mode segfault fix on ROCm 7.2+ (#60367, HIP VMM host access); GPU n-gram speculator with variable-length drafts (#40704); `ultraquant_4bit` KV (gfx950 only) | `mamba_cache_mode=all` removed (#58997) |
| main after 0.32 | — | RDNA3 W4A16 MoE N-first compressed-tensors layout fix (#60388, 10-08, selects `Rdna3WNA16Experts`); open: native HIP MXFP4 dense+MoE for RDNA3 (#46676, open since 06-25: gpt-oss-20b 8.5 → 48.8 tok/s at C=1, 382 tok/s at C=32 on one XTX), Octave 3-bit KV (#59774), qwen4_exp (#60650/#60657), W4A16 GEMM for spec-decode row sizes and INT8/INT4 attention for gfx1100 (no PRs yet; tracking branch #57925) | — |

The RDNA3 W4A16 kernels the fleet depends on are still in the v0.32.0 tree
(`csrc/rocm/q_gemm_rdna3.cu`, `q_gemm_rdna3_wmma.cu`, `moe_q_gemm_rdna3.cu`,
`qdq_4_rdna3.cuh`). Official images exist for the bump:
`vllm/vllm-openai-rocm:v0.32.0` (10-18) plus `nightly-rocm72` /
`nightly-rocm100` variants.

**ROCm 10.1.0 (2026-10-05)**: Ubuntu 24.04.5 HWE **kernel 7.0** is now in the
support matrix (closes the "kernel 7.0 sits above ROCm's 6.17 ceiling" item
in the 10-21 Current Goal); vLLM 0.29, PyTorch 2.14 and SGLang 0.5.18 are
listed on gfx1100; Composable Kernel 1.3.0 retuned FMHA forward on gfx11 for
head dim 128.

**llama.cpp**: upstream b11808 (10-22). The gfx1100 images are b8173
(`build/runtime.yaml`) and b10453 (the Glimmer lane); gfx906 already moved to
b11335/b11457. Qwen3.8-27B GGUFs ship the MTP head and run it with
`--spec-type draft-mtp` (bartowski keeps the MTP layers; community reports
73.6 → 125.5 tok/s at n=2 on unspecified NVIDIA hardware, 18.4 → 38 tok/s on
SYCL). ROCm vs Vulkan on gfx1100 (issue #20934, b10809, Qwen3.8-27B): Vulkan
+8.5 % token generation (39.07 vs 36.02 tok/s), ROCm +5 % prompt processing;
the issue is closed with no upstream fix (wave32 vs wave64; +16 % needs an
unsupported flag). AMD-Ecosystem/llama.cpp now publishes gfx11 ROCm nightlies
(gfx1100–gfx1153 multiarch, TheRock ROCm 10.1, 10-21 build at `9ab1b6a`).

## 3. Why MTP is the pick (and why it failed before)

What the fleet already knows:

- `qwen35-35b-clean-mtp-32k` certified native MTP-1 on gfx1100: 80.18 % draft
  acceptance, 1.230x decode, exact greedy parity (GPUProfile certificate,
  2026-07-16). The gfx1100 MTP machinery works when the draft is right.
- The 27B attempt (research doc 2026-08-15 §8b, vLLM 0.23, Intel 3.6
  AutoRound with an FP16 `mtp.fc`) measured **~33 % pos-0 acceptance** with
  both `mtp` and `qwen3_next_mtp` → net zero (35.0 vs 39.0 tok/s). Coherence
  was unaffected (6/6). Two blockers were recorded: the draft `ModelConfig`
  re-reads `config.json` and ignores `hfOverrides` (needs the on-disk
  `quantization_config` patch), and the acceptance itself.

What changed upstream that bears on the 33 %:

- #51812 (0.28): in a mixed batch the GDN kernel received the **unsorted gate
  tensors** while Q/K/V were reordered by `spec_token_indx`, so gates could
  belong to other tokens; the fix gathers `a`/`b` with the same indices.
  Reproduced on Qwen3.5-2B with two MTP draft tokens. Our 0.23 runs mixed
  prefill chunks and decode (chunked prefill on, APC on), which is exactly the
  mixed case.
- #52539 (0.29): fused GDN MTP path for all Qwen head ratios; #55760: dense
  prefix-cache retention restored for hybrid MTP models.
- The vLLM Qwen3.8-27B recipe reports the in-checkpoint MTP head working in
  every precision tested, acceptance 0.754–0.897 (NVIDIA, `method: mtp`,
  `num_speculative_tokens: 3`), and says to read
  `vllm:spec_decode_num_{accepted,draft}_tokens_total` rather than trust
  throughput.
- A downstream fork (weicj PR #258) found a silent 0 %-acceptance mode: a
  whole-head `mtp` quantization **exclusion** never reaches the nested MTP
  modules (exact-prefix matching), so the head is built with the wrong quant
  config and loads garbage without an error. Our `hfOverrides` builds the
  INT4 module list by exact census, so an `mtp.*` mismatch of the same family
  is plausible on the 3.8 artifact (whose MTP block is INT4, unlike Intel's
  3.6).
- JartX's RDNA3 stack serves Qwen3.8-27B W4A16 G32 with MTP k=3 at 113–119
  tok/s decode at both short and 167K context on 4x 7900 XTX (TP4) — MTP pays
  on this architecture when the draft is correct.

Expected effect here: at ~0.75 pos-0 acceptance and k=2 the mean accepted
length is ~2.3 tokens per step; the verify step costs more on the W4A16 GEMM
(no M=2–4 tuning yet, per the tracking branch) and on the attention 2D path
(research doc §8f: TRITON 2D at qlen 2 is 2.6–2.8x cheaper than ROCM but
~12x the 3D qlen-1 cost per layer at 8K). A realistic single-card target is
**1.5–1.8x effective decode (60–73 tok/s)** at working-set contexts, which
takes the workhorse p50 from ~8.3 s to ~5 s. `ROCM_SEGMENTED_ATTN` (0.32)
supports multi-token verify natively and may close the 2D penalty, but its
fp8-KV support is gfx12-only, so on gfx1100 it has to be measured with fp16
KV (64K/1 seq or 32K/2 seqs).

## 4. Ranked candidates

| # | Candidate | Impact | Effort | Risk | Call |
|---|---|---|---|---|---|
| 1 | **MTP on the Qwen3.8-27B lanes, on a vLLM 0.32 native-W4A16 runtime** | Highest: the busiest lane, every request decode-bound; both 27B lanes and the 9B RP lanes inherit; also carries #51812/#52539, segmented attention, sleep-mode fix, Mamba prefill checkpoints (9–25 % TTFT) | Medium: Dockerfile ARGs (`v0.26.0` → `v0.32.0`), plugin re-port to 0.32's FLA layout (819 LOC across three plugins; the two 27B lanes do not load `flexinfer_qwen35_text`), CI job, canary ladder with the existing `deploy/debug/qwen38-27b-v026-canary.yaml` + `scripts/verify-vllm-canary.py` + the soak script | Acceptance on an INT4 MTP head on ROCm (riskiest); MRV2-default behaviour for hybrid GDN on ROCm; draft config ignoring `hfOverrides`; cudagraph ladder (sizes must be multiples of K+1); mixed decode+spec batches (#36917 closed as not reproducible since 0.19) | **Do first** |
| 2 | `ROCM_SEGMENTED_ATTN` A/B (rides on #1's image) | High if it beats TRITON_ATTN: upstream replacement for the !967 workaround, native qlen > 1 verify | Low once #1's image exists | fp8 KV unsupported on gfx11 → pool halves; autotune off by default (`--kernel-config.enable_rocm_segmented_attn_autotune=True`) | Measure inside #1 |
| 3 | gfx1100 llama.cpp currency b8173/b10453 → b11808, ROCm 6.4.4 base → 7.x or the AMD-Ecosystem gfx11 build, plus a Vulkan A/B | Medium: `draft-mtp` for GGUF Qwen3.8 (a vLLM-independent MTP path and a fallback if #1's acceptance stays low), DFlash on Glimmer, prerequisite for any new GGUF-only model | Low–medium: same Dockerfile family the gfx906 image already moved; the Glimmer lane is the vehicle | Vulkan in-container needs Mesa/RADV in the image; ROCm wins prefill | Second slice |
| 4a | Nemotron 3.5 Bolt 74B-A9B-24GB canary (10-15, OpenMDW-1.1; IQ2_XXS experts + NVFP4 Mamba + Q8 embed; LiLiCorr drafter; vision) | Medium: a 9B-active agentic/vision lane, likely 2–3x the dense 27B's decode; **below Qwen3.8-27B on Terminal-Bench 2.1 (50.98 vs 73.0)**, so a speed lane, not the workhorse | Low after #3 | AMD untested (card lists Ada/Blackwell); NVFP4 tensors in a GGUF on HIP unproven; 24 GB "at batch 1 / 128K" leaves no co-tenant | Canary after #3 |
| 4b | gpt-oss-20b MXFP4 fan-out lane via #46676 | High concurrency (382 tok/s at C=32 on one XTX) for audit pools | Needs a patched build; PR open since June | Do not build on an unmerged PR | Watch |
| 5 | XTX platform: BIOS 1811 → 3854/3881, card-swap diagnosis, drop the 2100 MHz cap when a soak says so | Removes the 6 % cap and the drop risk on the second card | Ops | — | Keep open, not a feature |
| 6 | Sleep-mode election (#68) | Large UX if it worked | Blocked: #60367 fixes the VMM crash class, but the device-plugin deadlock and readiness blockers stand; only the runtime-managed path can use it | — | Defer |
| 7 | `ultraquant_4bit` KV, Octave 3-bit KV | Pool density | gfx950 only / open PR | — | Not applicable |

## 5. Model candidates for a 24 GB gfx1100 (Sep–Oct 2026 census)

| Model | Released | Shape | Fits 24 GB | Role | Verdict |
|---|---|---|---|---|---|
| Qwen3.8-27B (incumbent) | 08-14 | dense 27B, hybrid GDN 3:1, MTP head, 262K | 16.8 GiB INT4 | workhorse | Still the strongest single-card coder (TB 2.1 73.0, SWE-Pro 61.7); nothing since beats it |
| Nemotron 3.5 Bolt 74B-A9B-24GB | 10-15 | Mamba-2/Transformer LatentMoE, 9B active, vision | ~22–23 GB IQ2 at batch 1 | fast agentic / browsing / vision lane | Canary via llama.cpp after the image refresh; AMD unproven |
| Mistral Small 4 | weights due end of Oct | unknown | unknown | watch | Re-check in November |
| Qwen 4 27B | in training (Apsara 09-22), no date | successor of 3.8-27B, open weights promised | — | future workhorse | Watch; qwen4_exp PRs show the vLLM path is being built on gfx1100 |
| Mellum2.1 Thinking (JetBrains) | 10-08 | 12B-A2.5B MoE, code, Apache-2.0 | ~6.7 GB | cheap code-completion lane | Only if a consumer needs sub-second code completion |
| LFM2.5-24B-A2B (Liquid) | 10-13 | 24B-A2.45B | ~13 GB | triage | Revenue-capped licence; skip |
| gpt-oss-20b MXFP4 | — | 21B-A3.6B | ~13 GB | fan-out | Blocked on #46676 |
| Qwen3.8-Flash-Next | 09 | 125B-A6B + 51B n-gram table | **no** (JartX needed 4 cards even pruned) | — | Reject |
| Reflection Beam 501B-A23B, GLM-5.3-Flash 320B, DeepSeek-V4.x | 10-05 / 08 / 09 | > 300B | **no** | — | Reject |

## 6. Recommended slice plan

1. **S1 — runtime currency, no behaviour change.** `build/runtime.yaml`
   profile `gfx1100-native-w4a16` and `build/Dockerfile.runtime-serving-
   native-w4a16`: base `vllm/vllm-openai-rocm:v0.32.0`, `VLLM_REF v0.32.0`.
   **Finding (2026-10-23, S1 build)**: the official 0.32.0 ROCm image
   (ROCm 7.2.3, torch 2.13, Python 3.12) already ships `_rocm_C` with
   `gptq_gemm_rdna3` and 12 `amdgcn-amd-amdhsa--gfx1100` code objects
   (upstream's `docker/Dockerfile.rocm_base` lists gfx1100 in
   `PYTORCH_ROCM_ARCH`), so the from-source rebuild the 0.26 image needed is
   unnecessary; the Dockerfile now probes the base (`VLLM_REBUILD=auto`) and
   skips it, and the verify script asserts both the op and the gfx1100 code
   objects at build time. (Superseded the same day on master by `build/vllm-v032-native-w4a16`,
   commits 85b670996 / 3fc9c37fe, which drops the rebuild path entirely —
   wheel mode, `verify_isa_contract` over `_C`/`_moe_C`/`_rocm_C`; this branch
   merged that version, so the S1/S2 canaries ran on the probe-built digest
   `8ff8d14a` while CI publishes the wheel-mode image from master.) Plugin port: 0.32 registers the Qwen3.5 text
   classes itself with `IsHybrid`, `SupportsMRoPE` and the mamba helpers on
   `Qwen3_5ForCausalLMBase`, and its `SpeculativeConfig.hf_config_override`
   maps `qwen3_5_text`, so `flexinfer_vllm_common` 0.2.0 copies only helpers
   a class does not already resolve, `flexinfer_vllm_qwen35_text` 0.9.0 skips
   upstream-registered architectures (the #66 duplicate-registration check)
   and gains `FLEXINFER_QWEN35_MTP_HEAD=plain|checkpoint` (S2 needs
   `checkpoint`: the plain policy forces every `mtp.*` linear to the
   unquantized method, which silently mis-loads an INT4 MTP block), and
   `flexinfer_vllm_qwen35_seqcls` 0.2.0 keeps upstream's M-RoPE. Removed
   flags other than kv-scales: `--max-num-partial-prefills`,
   `--max-long-partial-prefills` (no production lane sets them; only
   `examples/serverless-multi-backend.yaml`) and `--swap-space` (v1alpha1
   `kvCache.swapSpace` only). **Manifest blocker**: 12 lane manifests
   set `calculateKvScales: false`, and `appendVLLMBooleanOptionalArg`
   (`backend/vllm.go:227`) renders that as `--no-calculate-kv-scales`; 0.28
   removed the option (#49389), so every such lane would fail argument
   parsing on the new image. Either drop the key from the manifests that move
   or make the backend skip the flag when the runtime is ≥ 0.28 (a
   GPUProfile/image-aware guard is the durable fix). Boot
   `qwen38-27b-v026-canary` (renamed) on the
   5930k at the production envelope with MTP off: pass = 3/3 probes
   identical, ≥ 40 tok/s, pool ≥ 1.8x, record which model runner booted.
2. **S2 — the MTP kill-test** (§7; prepared 2026-10-24, §7b:
   `deploy/debug/qwen38-27b-v032-mtp-canary.yaml` + `scripts/verify-vllm-mtp.py`,
   waiting for the 5930k window).
3. **S3 — segmented attention A/B** on the same canary: `attentionBackend:
   ROCM_SEGMENTED_ATTN`, autotune on, fp16 KV; decode at 2K/30K/60K and
   verify-step cost with MTP on, against TRITON_ATTN + fp8 KV. (Prepared
   2026-10-25, §7d: `deploy/debug/qwen38-27b-v032-s3-attn-canary.yaml`, five
   arms at 32K, sweep points re-chosen from production traffic.)
4. **S4 — promotion**: XTX lane first (canary vehicle, soak under the cap),
   then the workhorse (`!1110` pattern), prewarm both nodes, drift gate, and
   the `pinned-*` Harbor tag so retention cannot delete the digest again.
5. **S5 — llama.cpp gfx1100 refresh** (`build/Dockerfile.llamacpp-rocm-
   gfx1100`, `llamacpp_version` b8173 → b11808 or the AMD-Ecosystem gfx11
   build): Glimmer lane re-qualification, `draft-mtp` probe on a
   Qwen3.8-27B GGUF, Vulkan A/B, then the Bolt canary.

## 7. Riskiest assumption and kill-test (S2)

**Assumption**: `biMEMO/Qwen3.8-27B-int4-AutoRound` (INT4 MTP block) reaches
≥ 70 % position-0 draft acceptance with `{"method":"mtp",
"num_speculative_tokens":2}` on a gfx1100 vLLM 0.32 native-W4A16 build, with
greedy outputs identical to the no-MTP baseline.

**Kill-test** (one debug Model on the 5930k, after S1 passes):

1. ~~Patch the lane's `/models/config.json` `quantization_config`~~ —
   resolved 2026-10-24 (§7b): not needed on 0.32. The draft `ModelConfig`
   still re-reads `config.json` (dict `hf_overrides` stay target-only), but
   the draft *model* is built from the target's `VllmConfig` and therefore
   the target's AutoGPTQ config; the draft config's own quantization verify
   settles on `inc`, which ROCm lists in 0.32 (0.23 rejected it).
2. Flip `speculativeConfig` (K=2, draft `attention_backend` pinned to
   `TRITON_ATTN`; 0.32 derives the cudagraph ladder from K+1 itself — the 9B
   lane's `[1,2,4]` ladder breaking at K=5 was a 0.2x-era manual ladder).
3. Run the 6-probe gauntlet + the 100-request concurrency-2 soak
   (`vllm_soak.py`, 400-token outputs) and read
   `vllm:spec_decode_num_accepted_tokens_total /
   vllm:spec_decode_num_draft_tokens_total` and the per-position series.
4. **Pass** = pos-0 ≥ 0.70, mean accepted length ≥ 1.6, single-stream
   ≥ 60 tok/s at 2K context, greedy token-identical to baseline, 0 engine
   deaths across the soak and one 60K needle. **Fail at < 0.50** = try an
   FP16-MTP artifact (dequantize `mtp.*` back to BF16 the way Lorbus did for
   3.6; the in-house AutoRound pipeline can re-pack) before concluding the
   runtime is at fault; an engine death with `num_decodes/num_spec_decodes`
   in the assert = #36917 class → `maxNumSeqs: 1` is not acceptable, stop.
5. Rollback = remove `speculativeConfig`; S1's digest stays if it passed on
   its own.

If S2 fails, S1 and S3 still ship on their own merits (segmented attention,
the sleep-mode fix, the 0.28/0.29 GDN fixes, ROCm 10.1 alignment) and the
GGUF `draft-mtp` path in S5 becomes the MTP fallback.

## 7a. S1 RESULT (2026-10-24): **PASS — vLLM 0.32.0 serves the production envelope at parity**

Matrix row `gfx1100-native-w4a16-v032-2026-10-24`. Image
`registry.harbor.lan/flexinfer/runtime@sha256:8ff8d14aa9ec…` (tags
`rocm-gfx1100-v032-native-w4a16`, `-s1-20261023`, `pinned-8ff8d14aa9ec`),
built off-CI in minutes because the official 0.32 ROCm image already carries
the gfx1100 build of `_rocm_C` (§6 S1 finding). Canary
`deploy/debug/qwen38-27b-v032-canary.yaml` on the 5930k, applied by the
operator (the agent session is denied `kubectl apply` and the in-cluster MCP
identities lack Model RBAC).

| Gate | 0.32 canary | 0.26 workhorse (reference) |
|---|---|---|
| Boot | V2 model runner, `RDNA3W4A16LinearKernel`, Triton/FLA GDN prefill, `TRITON_ATTN` honoured; plugins `upstream_registered=Qwen3_5ForCausalLM,Qwen3_5MoeForCausalLM`, `hybrid_methods_copied=0`, `fla_autotuners=11`, seqcls `mrope=upstream` | V1 runner; plugin registers the text classes itself |
| KV pool @ 64K / 0.96 | first cold-compile boot 86,388 tok (1.32x) → `KVPoolRecheck` relaunch **140,753 tok / 2.15x** | 128,837 / 1.97x |
| Warm relaunch | weights 15.3 s, torch.compile 0.47 s (hostPath cache), Ready in ~1 min | ~3 min |
| Probes | `42` / Birch JSON / `MAPLE-4827` identical; recall TTFT 2.31 s @ 2,337 tok | identical answers; 2.41 s |
| Decode (novel, 256 tok) | **41.50 tok/s median** | 40.6 clean |
| Prefix cache | 7,583-tok prompt 10.74 s → 1.02 s / 0.81 s, `cached_tokens` 7200 | same recipe, 12.96 → 1.3 s on the XTX |
| Concurrency | two different cold ~6–7K prefills at once, 15.2 / 15.8 s, both correct, 0 preemptions | — |
| Faults | 0 restarts, 0 engine errors; **one** `HIPCachingAllocator … OOM … (free: 0)` warning during the first 7.5K cold prefill, recovered, not repeated | 0 allocator lines in 24 h |

Reading of the one warning: 0.32's memory profiler equates 0.96 to 0.9475
without cudagraph accounting (0.26: 0.9232), so it hands ~0.4 GiB more to KV
and leaves less transient room for a 4,096-token prefill chunk. Promotion
should use `gpuMemoryUtilization: 0.95` on 0.32 (pool still ≈ 2.0x), or keep
0.96 with the allocator line on the watch list. Cosmetic 0.32 warnings:
`--model` is deprecated (the controller still passes it) and
`VLLM_ROCM_USE_AITER_PAGED_ATTN` is unknown to 0.32.

Not covered by S1 (by design): the 64K needle, a tool-call round trip, a
≥ 100-request soak, the XTX lane, MTP (S2) and `ROCM_SEGMENTED_ATTN` (S3).
Promotion (S4) additionally needs this branch's backend change deployed —
the current controller renders `--no-calculate-kv-scales` for the twelve
lanes that set the key, which 0.32 rejects — or those keys removed from the
manifests that move.

## 7b. S2 PREPARATION (2026-10-24): what the v0.32.0 tree settles before the card is free

Prepared while the 5930k is inside the S5 llama.cpp window
(`muse-glimmer-30b-canary` at 520 / forcePromotion); the kill-test runs when
that session deletes its canaries. Deliverables:
`deploy/debug/qwen38-27b-v032-mtp-canary.yaml` (the S1 canary plus the MTP
delta) and `scripts/verify-vllm-mtp.py` (+ `scripts/test_verify_vllm_mtp.py`,
10 tests). Findings, each against the v0.32.0 source:

- **No on-disk `config.json` patch.** `SpeculativeConfig.__post_init__`
  (`vllm/config/speculative.py` L1147–1163, L1283–1313) builds the draft
  `ModelConfig` from the target's model path with `quantization` inherited
  and `hf_overrides = compose_draft_hf_overrides(target.hf_overrides)`, which
  for a dict override returns only `hf_config_override` ("Dict overrides are
  target-specific key patches and are not applied to the draft", L1093). So
  the draft config carries the artifact's on-disk `quant_method: auto-round`
  (`packing_format auto_round:auto_gptq`, `block_name_to_quantize
  [model.language_model.layers, mtp.layers]`, `extra_config` marking
  `mtp.fc` as bits 16); its `_verify_quantization` (`vllm/config/model.py`
  L1370–1457) walks the override list, `auto_gptq` declines (quant_method ≠
  gptq), `inc` claims auto-round (`inc/inc.py` L361–368) and
  `platforms/rocm.py` L614–626 lists `inc`, so nothing raises (0.23 did not
  list it: that ValueError is what forced the patch). The draft **model**
  never uses that config's quantization: `LLMBaseProposer._get_model`
  (`vllm/v1/spec_decode/llm_base_proposer.py` L1304–1317) calls
  `get_model(vllm_config=_create_draft_vllm_config(), model_config=draft)`;
  `_create_draft_vllm_config` (L1266–1302) replaces only the kernel,
  attention and cache configs, and `initialize_model`
  (`model_loader/utils.py` L74–97) resolves the class from the draft config
  (`Qwen3_5MTP`) but constructs it with the target's `quant_config` and
  `model_config`. `Qwen3_5MultiTokenPredictor` (`qwen3_5_mtp.py` L66–126)
  reads `vllm_config.model_config.hf_text_config` / `.hf_config.
  quantization_config` — the manifest's gptq dict, which has no `dynamic`
  `-:mtp` entry, so the MTP layers stay quantized.
- **The gptq dict already encodes the MTP census.** `is_layer_gptq_quantized`
  (`utils/gptq_utils.py` L120–169) matches `modules_in_block_to_quantize`
  as substrings: `mtp.fc` → unquantized (the artifact ships `mtp.fc.weight`
  fp16), `mtp.layers.0.self_attn.{qkv,o}_proj` and `mlp.{gate_up,down}_proj`
  → quantized (the artifact ships qweight/qzeros/scales for all seven),
  `lm_head` unquantized (`lm_head: false`). The biMEMO
  `model.safetensors.index.json` @ `2d7425f7` has exactly 29 `mtp.*`
  tensors: 7 × (qweight, qzeros, scales) + 8 fp16 weights (fc and 7 norms).
- **Plugin policy must be `checkpoint`.** With `plain` the text plugin
  returns `UnquantizedLinearMethod` for every `mtp.*` `LinearBase`
  (`_patch_qwen35_mtp_gptq`), so the seven INT4 tensors would be loaded into
  fp16 parameters — the silent-garbage mode of the downstream fork's PR #258.
  `checkpoint` leaves `AutoGPTQConfig.get_quant_method` untouched. The
  plugin's `_patch_qwen35_text_mtp_config` only intercepts `qwen3_5_text` /
  `qwen3_5_moe_text`; the on-disk type is `qwen3_5`, which upstream maps to
  `qwen3_5_mtp` / `Qwen3_5MTP` with `n_predict = mtp_num_hidden_layers = 1`
  (`speculative.py` L917–937). The env reaches the pod through the
  `spec.config.command` wrapper (`controllers/model_deployment.go`
  L270–281; the vLLM backend sets no command of its own — the workhorse
  Deployment runs the image entrypoint `vllm serve` with args
  `--host 0.0.0.0 --port 8000 …`), the same pattern as
  `deploy/models/qwen35-35b-clean-mtp-32k.yaml`.
- **Memory.** `Qwen3_5MTP.load_weights` keeps the `embed_tokens` and
  `lm_head` names, so the draft first loads its own copies (vocab 248,320 ×
  5120 × 2 B ≈ 2.5 GB each, `tie_word_embeddings: false`); the proposer then
  `torch.equal`-compares them with the target's and shares them
  (`_maybe_share_embeddings` / `_maybe_share_lm_head`, L1413–1545). Transient
  +5 GB at load, steady state ≈ +0.3 GB (one INT4 attention+MLP layer plus
  the fp16 fc) plus one more full-attention KV layer (~+6 % per token).
  Expect the 64K pool to profile ≈ 1.9x against S1's 2.15x; the manifest
  keeps 0.96 so MTP is the only variable.
- **CUDA graphs.** `VllmConfig.__post_init__` (`vllm/config/vllm.py`
  L2341–2499) appends uniform decode sizes `n × (K+1)` for
  `n ∈ request_counts(max_num_seqs)` and caps `max_cudagraph_capture_size`
  at `min(max_num_seqs × (K+1) × 2, 512) = 12`: expected ladder
  `[1, 2, 3, 4, 6, 8]`, no `cudagraphCaptureSizes` override (the 35B MTP
  lane's `"2"` was a 0.2x-era necessity).
- **Draft attention backend.** `_create_draft_vllm_config` "never inherit[s]
  the attention backend from base … always independently autoselect unless
  explicitly specified in the speculative config", so the manifest sets
  `speculativeConfig.attention_backend: TRITON_ATTN`
  (`_parse_attention_backend`, L1802–1808, accepts the enum name) to keep the
  draft on the RDNA3 long-context fix path next to the target.
- **Hybrid guards.** `mamba_cache_mode: align` has no speculative-decoding
  guard in 0.32 (`vllm.py` L3277–3283; only the ReplaySSM / RecoverSSM
  paths constrain it) and #55760 restored dense prefix-cache retention for
  hybrid MTP, so the production prefix-cache recipe stays on.
- **Metrics** (`vllm/v1/spec_decode/metrics.py` L176–262):
  `vllm:spec_decode_num_{drafts,draft_tokens,accepted_tokens}_total` and
  `vllm:spec_decode_num_accepted_tokens_per_pos_total{position="0|1"}`.
  pos-0 acceptance = `per_pos[0] / drafts`; mean acceptance length =
  `1 + accepted / drafts` (vLLM's convention — the §3 "~2.3 tokens per
  step" and the §7 "≥ 1.6" bar are read in it). The probe reads the counter
  deltas around its whole workload rather than trusting throughput.
- **Probe design** (`scripts/verify-vllm-mtp.py`): the three S1 coherence
  probes, three 256-token greedy parity generations hashed for `--compare`
  against an MTP-off rerun on the same digest (`kubectl patch … --type json
  -p '[{"op":"remove","path":"/spec/config/speculativeConfig"}]'`, then
  `--phase baseline`), single-stream decode at short and ~2K context, a
  50-pair concurrency-2 soak, then the spec-decode verdict (pos-0 ≥ 0.70,
  mean acceptance length ≥ 1.6, decode ≥ 60 tok/s). It fails closed when
  the counters are missing, go backwards (engine restart) or record no
  drafts.
- **Field correction (2026-10-24):** `kubectl get/logs/exec/patch/delete`
  all work from the agent session; only `apply`/`create` are denied. The
  MTP-off baseline patch and the canary deletion were run here directly.
- **Offline image probe PASS** (no GPU, `docker --context 7900xtx run` of
  the S1 digest with `FLEXINFER_QWEN35_MTP_HEAD=checkpoint`,
  `s2_image_probe.py`): the plugin line reads `mtp_head=checkpoint,
  upstream_registered=Qwen3_5ForCausalLM,Qwen3_5MoeForCausalLM,
  fla_autotuners=11` and `AutoGPTQConfig` is left unpatched;
  `SpeculativeConfig._parse_attention_backend("TRITON_ATTN")` →
  `AttentionBackendEnum.TRITON_ATTN`; the manifest's quantization dict
  overrides to `auto_gptq` and decides `mtp.fc` False, `mtp.layers.0.
  self_attn.qkv_proj` / `o_proj` / `mlp.gate_up_proj` / `mlp.down_proj`
  True, `linear_attn.in_proj_a` False, `lm_head_quantized` False;
  `hf_config_override` maps the on-disk `qwen3_5` config to `qwen3_5_mtp` /
  `['Qwen3_5MTP']`, `n_predict 1`; `Qwen3_5MTP` is in the registry.

## 7c. S2 RESULT (2026-10-24): **PASS on the riskiest assumption — the INT4 MTP head drafts at 0.80 pos-0 acceptance; the gain is context-dependent**

Matrix row `gfx1100-native-w4a16-v032-mtp-2026-10-24`. Same digest as S1,
`deploy/debug/qwen38-27b-v032-mtp-canary.yaml` on the 5930k, applied by the
operator 13:16Z, MTP-on probe 13:59–14:04Z, MTP-off baseline on the same pod
after a `kubectl patch` removed `speculativeConfig` (14:05Z), deleted 14:18Z.

| Gate (§7) | MTP on (K=2, draft TRITON_ATTN) | MTP off (same digest) | Read |
|---|---|---|---|
| pos-0 acceptance ≥ 0.70 | **0.8018** (pos-1 0.5473, overall 0.6746; 11,669 drafts / 23,338 draft tokens / 15,743 accepted) | — | **met** |
| mean acceptance length ≥ 1.6 | **2.349** (vLLM's periodic log 2.32–2.37) | — | **met**; matches the §3 "~2.3 tokens per step" expectation |
| single-stream decode ≥ 60 tok/s, short | **65.14** median (65.14 / 65.66 / 57.32) | 41.31 (41.05 / 41.31 / 41.35; S1 41.50) | **met, 1.58x** |
| single-stream decode ≥ 60 tok/s @ 2K | 48.41 @ 2,063 tok | 40.00 | **not met, 1.21x** |
| concurrency-2 soak | 100/100, median per request 55.24 (min 32.0), engine aggregate 101 tok/s | 40/40, 39.59 (min 39.44) | clean, 1.40x per request |
| probes identical | `42` / Birch JSON / `MAPLE-4827`; recall TTFT 2.32 s | identical, 2.32 s | met |
| greedy token-identical | rain prompt ×3 byte-identical to the warm baseline; `parity_code` byte-identical to the baseline rerun (932 chars) while the baseline's own cold vs warm runs diverge at 685; `parity_explain` / `parity_list` diverge at 854/1336 and 577/1403 chars from a baseline stable across reruns | cold vs warm prefix-cache runs already diverge | **not a usable bar**: the stack is not bit-reproducible without MTP; MTP adds late (~150–200 token) near-tie argmax flips from the multi-token verify path, coherent either way |
| KV pool @ 64K | 101,544 tok / **1.55x** (KVPoolShort threshold 1.50) | 140,753 / 2.15x | −28 %: the draft's KV layer, its graphs and the profiler's margin |
| boot | fresh compile cache: engine dies once (`2.45 GiB KV cache needed, 1.77 GiB available`) and the container restarts; warm cache: Ready in 2 min; model 17.08 GiB (+0.28 over S1, so embed/lm_head are shared) | S1 pattern (1.32x → KVPoolRecheck relaunch) | promotion must prewarm the compile cache or accept the first-boot restart |
| faults | 0 restarts after the cold one, 0 HIP/HSA/OOM/NaN lines in 52 min | same | met |

What this settles: the artifact's INT4 MTP block, loaded through the target's
AutoGPTQ config with `FLEXINFER_QWEN35_MTP_HEAD=checkpoint`, is a correct
draft on gfx1100 — the 2026-08-15 33 % result was the 0.23 runtime (and the
`plain` head policy's unquantized-load path), not the head. What it does not
settle: whether MTP pays at the workhorse's real working-set contexts. The
verify step's 2D attention cost on TRITON_ATTN eats the gain as context grows
(1.58x at 30 tokens, 1.21x at 2K; §3 predicted this), the pool drops to 1.55x
(two 64K sequences no longer fit), and K=2 on a single MTP layer gives pos-1
only 0.55 (vLLM warns about this at boot).

Next, in order: **S3** measures the verify cost with `ROCM_SEGMENTED_ATTN`
(fp16 KV, so 64K/1 seq or 32K/2 seqs) at 2K/30K/60K with MTP on, against
TRITON_ATTN; a **K=1 vs K=2 vs K=3** A/B on the same canary (K=1 drops the
pos-1 forward and the pool cost); then **S4** decides MTP per lane from its
context distribution (short-answer lanes such as the 9B RP and the
workhorse's p50 356-token answers gain the most) and turns the 0.32 base
into the runtime persona rather than two dedicated Deployments (owner
direction 2026-10-24: one stacked image per GPU class, backend swaps inside
the runtime pod, no pod churn).

## 7d. S3 PREPARATION (2026-10-25): the attention A/B, sized by real traffic

State on 2026-10-25: another session promoted the 5930k workhorse to the 0.32
wheel-mode image `cd108c9e` (!1155, Ready 14:50Z, pool 140,753 / 2.15x at 64K,
MTP off) after canarying the XTX lane on it (!1144); both lanes logged zero
allocator/HIP/engine-failure lines in 24 h. That image's text plugin predates
`FLEXINFER_QWEN35_MTP_HEAD`, so MTP must not run on it: the MTP-capable 0.32
build is still the S1/S2 digest `8ff8d14a` (pinned in Harbor) until !1139
merges and CI publishes the native-W4A16 tag from master.

**Production prompt sizes** (vLLM `vllm:request_prompt_tokens`, 7 days to
2026-10-25 15:10Z):

| Lane | Requests | Prompt p50 / p90 / p99 | Share <=1K / <=2K / <=5K / <=20K | Generation p50 / p90 |
|---|---:|---|---|---|
| 5930k workhorse | 12,625 | 466 / 4,260 / 15,563 | 61.1 / 76.0 / 94.6 / 99.6 % (none > 50K) | 297 / 587 |
| XTX Mills lane | 366 | 1,488 / 1,957 / 7,388 | — | 281 / 467 |

So S2's numbers already bracket most traffic (1.58x at ~30 tokens, 1.21x at
2K), and the open question is the 2K-16K band, not 60K. A 32K lane would
still serve > 99.6 % of requests, which makes the fp16-KV segmented backend
a realistic production shape if it wins.

**Segmented attention on gfx1100** (v0.32.0
`vllm/v1/attention/backends/rocm_segmented_attn.py`,
`vllm/v1/attention/ops/segmented_attention.py`): RDNA only (`on_gfx1x`), fp8
KV only on gfx12 (`supports_combination` returns "FP8 segmented attention
requires gfx12"), head size 64/128/256 (Qwen3.8 full attention: 256), GQA
1-16 (6 here), decoder only, query length up to 8192 (`MAX_QUERY_LEN`), so
4,096-token prefill chunks and the K+1 verify both run in it; it shares a
Triton cache between target and draft and autotunes only when
`kernel_config.enable_rocm_segmented_attn_autotune` is set (the vLLM backend
has no kernel-config key, so the canary appends `--kernel-config` in its
command wrapper). fp16 KV roughly halves the pool: S2's 101,544 fp8 tokens
with the draft resident become ~50K, under one 64K sequence, so every arm
runs at 32K.

**Image (updated 2026-10-26 00:00Z):** `b06b06fe`, CI's build of master
`dc73879c` (pipeline 34797, job 414103, published 22:38Z), the digest the three
parked 5930k lanes run since !1166 and the 5930k prewarm list holds. Same
Dockerfile lineage as production `cd108c9e` plus the MTP head policy; the
offline MTP-contract probe passed in it at 23:56Z. The interim off-CI build
`b5afdb20` (`pinned-b5afdb208c46`, built while job 414103 waited behind a
unified build holding the `buildkit-rocm-images` group) passed the same probe
and remains a fallback.

**Canary** `deploy/debug/qwen38-27b-v032-s3-attn-canary.yaml`: five arms on
one Model, sequenced with `kubectl patch` (which the agent session can run;
only `apply`/`create` need the operator): B segmented + fp16 + MTP K=2, C
segmented + fp16, D TRITON + fp8, A TRITON + fp8 + MTP K=2, optional A1
TRITON + fp8 + MTP K=1. Probe `scripts/verify-vllm-mtp.py --phase measure
--contexts 512,2048,4096,16384,28672` (new: a single-stream sweep with a
unique prefix per point, so each TTFT is a cold prefill, and per-point
acceptance from the counter deltas; `measure` enforces coherence only). The
decision rule is written in the manifest before the data: MTP goes to the
workhorse if its traffic-weighted decode speedup on the production backend
is >= 1.25 with pos-0 acceptance >= 0.70; segmented replaces TRITON_ATTN only
if it beats it by >= 10 % at 16K/28K without > 20 % worse TTFT.

## 7d'. S3 RESULT (2026-10-26): **segmented attention + MTP wins decode and long-context prefill, but fails the pre-registered TTFT bar on short prompts**

The window ran 03:37–04:47Z on the 5930k on `b06b06fe`. The canary took the card from the workhorse at 03:38:51 (priority 510 vs 500). Every arm ran at 32K with `maxNumSeqs 2`, using `scripts/verify-vllm-mtp.py --phase measure --contexts 512,2048,4096,16384,28672 --soak-pairs 10`. Arms were switched with `flexinfer_update_model` merge patches.

**Per arm.** Single-stream decode in tok/s, TTFT in s, 256 generated tokens per point:

| Arm | Pool at 32K | 512 | 2K | 4K | 16K | 28K | Soak c=2 (median per request) |
|---|---|---|---|---|---|---|---|
| B segmented, fp16, MTP K=2 | 52,932 (1.62x) | 71.2 / 2.0 | 68.6 / 1.8 | 66.6 / 5.3 | 66.1 / 15.1 | 61.5 / 27.1 | 59.7 |
| C segmented, fp16 | 70,749 (2.16x) | 41.6 / 1.9 | 41.3 / 2.3 | 40.7 / 5.2 | 38.2 / 14.4 | 36.0 / 26.4 | 39.9 |
| D TRITON, fp8 (production attention) | 80,874 (2.47x) | 40.8 / 0.5 | 40.2 / 2.1 | 39.3 / 4.6 | 34.3 / 32.0 | 30.6 / 79.4 | 39.7 |
| A TRITON, fp8, MTP K=2 | 88,851 (2.71x) | 61.6 / 0.5 | 47.5 / 2.0 | 36.9 / 4.8 | 15.3 / 32.9 | 9.9 / 82.3 | 55.8 |

- **Acceptance.** Position-0 acceptance was 0.79–0.86 at every point in both MTP arms. Over B's whole workload it was 0.817, with position 1 at 0.566 and a mean acceptance length of 2.38.
- **Coherence.** The three coherence probes were identical in every arm. The greedy `parity_code` hash (`8174a49e…`) matched across B, C and D; A's differed.
- **Faults.** No HIP, HSA, OOM, EngineDead or Traceback lines in any arm.
- **Boots.**
  - B's cold first boot died once at KV profiling (1.96 GiB available, 2.45 GiB needed: the S2 pattern), then came up warm.
  - The segmented autotune ran a 74-workload plan once, about 10 min, covering decode (Q=1, 2) and prefill buckets (Q=4 to 2048). Later segmented boots hit the cache (`missing=0`).
  - C's boot also took one controller "recheck a short KV cache pool" relaunch.
- **B2 short-prompt check.** Back on arm B's config, warm pod, `--contexts 256,512,1024,2048`: TTFT 1.71 / 1.79 / 1.05 / 1.78 s, decode 72.1 / 72.0 / 68.9 / 69.2. A 1,124-token prompt returns its first token faster than a 253-token one. The ~1.7 s floor is per request, not a first-call compile, and C shows it without MTP. TRITON returns the first token in 0.5 s at these sizes.

**Decision rule, as written before the data:**

1. **MTP on the production backend (TRITON_ATTN + fp8).** It pays (≥ 1.15x with position-0 acceptance ≥ 0.70) only up to 2K: 1.51x at 512 and 1.18x at 2K. Past that it loses: 0.94x at 4K, 0.45x at 16K, 0.32x at 28K. The verify step's attention cost on TRITON grows with context, which S2's 1.58x → 1.21x was the start of.
   - The traffic-weighted speedup is 1.30 as a weighted mean of ratios, and 1.17 time-weighted (harmonic, the number a user's decode time follows).
   - **Verdict: MTP does not go to the workhorse on TRITON_ATTN.** One engine cannot switch MTP per request, and about 24 % of requests (the 4K-and-up bands) would decode slower.
2. **Segmented replaces TRITON on a lane.**
   - **Decode bar: passes.** Best segmented arm vs best TRITON arm is +93 % at 16K (66.1 vs 34.3) and +101 % at 28K (61.5 vs 30.6).
   - **32K bar: passes.** The lane can live at 32K: 52,932 fp16 tokens with the draft resident is 1.62x of 32K, and 99.6 % of requests are ≤ 20K.
   - **TTFT bar ("no more than 20 % worse"): fails on short prompts.** It is 3.3x worse at ≤ 512 tokens (1.7–1.8 s vs 0.5 s), and that band is 61 % of requests. From 2K up it is equal or better: 15.1 vs 32.0 s at 16K, 27.1 vs 79.4 s at 28K.
   - **Verdict: segmented does not replace TRITON yet.**
3. **End to end, B is still the best arm overall.** Taking TTFT plus the 297-token median generation over the traffic bands, the mean request latency is 10.8 s on D (production), 9.9 s on A and **7.6 s on B (−30 %)**.
   - At 512 tokens B's median request still beats production (6.2 vs 7.8 s): it saves 3.1 s of decode and adds 1.5 s of TTFT. A is the fastest arm in that band (5.3 s), but it is the slowest from 4K up.
   - Decode is 1.74x production time-weighted, and 1.50x per request under concurrency 2.

**Riskiest open question, and the next slice (S3b):** why does `ROCM_SEGMENTED_ATTN` put a ~1.7 s floor under prompts of 512 tokens or fewer on gfx1100 when 1,124 tokens take 1.05 s? Prefill shapes were autotuned, so it is not an untuned bucket. Candidates:

- the cudagraph and mixed-batch path for small prefills;
- per-request segment planning on the host;
- the chunked-prefill scheduler with segmented metadata.

Kill-test: one segmented boot (cache warm). Read the vLLM TTFT, queue and prefill-time histograms around a run of 256-token requests; take a stack sample during one (py-spy is not in `b06b06fe`, so install it in the pod for the probe); and sweep 256 / 384 / 512 / 768 / 1024 tokens (the probe's floor is 256). If the floor goes away, B (segmented + fp16 + MTP K=2 at 32K) passes every bar and goes to S4. If it does not, choosing B anyway trades short-prompt TTFT for the −30 % end-to-end latency, and that is the owner's call. The pool is 52.9K fp16 tokens at 32K against production's 140.8K at 64K, so requests over 32K need an overflow route (the 35B 128K lane). **Resolved in §7d'':** the floor was first-use Triton JIT of the segmented kernels, which one-sample probing read as a floor. Warm, B meets the TTFT bar and goes to S4.

## 7d''. S3b (2026-10-26): **the "floor" was first-use Triton JIT of the segmented kernels, not a floor; B passes every bar**

**RESULT.** The window ran 13:15–13:32Z on the 5930k (17 min). The operator applied the canary, and the agent activated it, since an Idle lane does not take the card on priority alone. Arms ran in this order: S1 (segmented + fp16, no MTP, profiler on), then B' (segmented + fp16 + MTP K=2). S2 and the fix arms were skipped, because S1 attributed the cost directly (below).

- **S1 has no floor in steady state.**
  - Median server TTFT over three reps of exact-size cold prefills was 0.183 / 0.236 / 0.319 / 0.561 / 0.715 / 0.927 / 1.011 / 1.776 / 3.490 s at 64 / 256 / 384 / 512 / 768 / 1,024 / 1,124 / 2,048 / 4,096 tokens.
  - The chat path matched within 10 ms.
  - The 256-token profiler trace held 214 ms of GPU work in a 309 ms span; the GPTQ RDNA3 GEMM was 86 % of the GPU time.
  - py-spy over 21 back-to-back 256-token requests (all 0.236–0.240 s) showed the EngineCore mostly waiting in `torch.cuda` `synchronize`, and the API server idle.
- **The outliers are compiles.** Five requests paid an extra 0.5–1.5 s, each the first of its shape:
  - 256 rep 0 (1.76 s), 801 rep 0 (2.08 s), 1,024 rep 0 (1.78 s), 4,096 rep 0 (3.97 s), and a 1,225-token prompt in a re-run of S3's own probe (2.03 s).
  - Each lines up one-to-one with a new `_segmented_attention_stage` / `_segmented_attention_reduce` entry in the persisted Triton cache (`/cache/compile/triton`).
  - vLLM's `jit_monitor` logs "Triton kernel JIT compilation during inference" for both kernels.
  - Every later request of the same shape is fast.
  - S3 sampled each point once, so it caught these one-time compiles at 256 and 512 and read them as a floor. B2's 253 and ~509-token prompts were new specializations for that process.
- **Why the boot warmup misses them.**
  - `_segmented_attention_stage` takes every stride as a `tl.constexpr` (`Q0..T1`, `PAGE`). The block-table stride `T0` is `ceil(32768 / block)` at runtime: 82 for fp16 (block 400), 79 with MTP (block 416).
  - The autotuner's synthetic table is `ceil(seq_len / page)` wide per workload (`_make_inputs`), so its 1,261 compiled variants mostly do not match live launches.
  - The boot warmup only loads the tuning table (`loaded=74 missing=0`). It never launches with the runtime block table, so each (query bucket, chosen config) compiles on its first live request.
- **The disk cache carries compiles across processes.**
  - B' booted as a new process on the same hostPath. Its whole sweep added zero new Triton cache entries: the shapes came from S3's B and B2 processes.
  - The one `jit_monitor` line was a 35 ms disk load.
  - Its decode check added exactly one real compile (13:30:21, the 16K point, about +1 s).
- **B' (the S3 winner) passes every pre-registered S3b bar.**
  - Median server TTFT, against production's 0.237 / 0.436 / 1.088 s at the same sizes:
    - 0.234 s at 256 (bar ≤ 0.28);
    - 0.419 s at 512 (bar ≤ 0.52);
    - 1.026 s at 1,124 (bar ≤ 1.31).
  - Other points: 0.225 / 0.402 / 0.501 / 0.698 / 0.935 / 1.114 / 1.750 / 3.391 s at 64 / 415–417 / 513 / 801 / 1,024 / 1,225 / 2,048 / 4,096 tokens.
  - Chat path: 0.238 / 0.431 / 1.035 s.
  - S3's probe at 512 / 2K / 16K: decode 71.8 / 66.5 / 66.1 tok/s (bars ≥ 65.2 at 2K and ≥ 62.8 at 16K); TTFT 0.44 / 1.78 / 16.2 s.
  - Position-0 acceptance 0.84 / 0.79 / 0.80, 0.814 overall, mean acceptance length 2.38.
  - Coherent probes, `parity_code` `8174a49e` (as in S3), 0 faults.
- **One open oddity.** S1 without MTP measured 0.561 s at 512 where B' measured 0.419 s, while 256 and 1,124 agree. It does not affect the candidate. The two configs use different autotune tables (`cd8d86c0…` for fp16 without the draft), so S1's tuned record for the 257–512 bucket is the likely suspect.

**Revised S3 verdict.** Under S3's own rule, segmented + fp16 + MTP K=2 at 32K now passes all of it:
- decode +93 % / +101 % at 16K / 28K;
- the lane lives at 32K;
- TTFT within 20 % of production at every size.

B goes to S4, with three requirements this slice adds:

1. **Warm the segmented specializations before routing traffic.** The cost is one-time per (image, config, block size, cache path).
   - Either carry this canary's compile-cache hostPath, or run `scripts/probe-vllm-ttft-floor.py` once after the first boot as a warm sweep: every power-of-two query bucket and the 1+2^k edges, concurrency-2 pairs, and one long chunked prefill.
   - The upstream fix is for the segmented warmup to launch with the runtime block table; vLLM's own warning asks for exactly that.
2. **An overflow route for requests over 32K** (the 35B 128K lane), as S3 noted.
3. **The MTP head policy.** It needs `FLEXINFER_QWEN35_MTP_HEAD=checkpoint` (now `spec.config.env`) on an image with text plugin ≥ 0.9.0 (`b06b06fe` or its successor).

**What is known before the window (the plan as prepared).**

- **Production baseline, measured with the new probe.** `scripts/probe-vllm-ttft-floor.py` ran against the live workhorse (TRITON_ATTN, fp8, 64K, `cd108c9e`) over a port-forward at 13:04Z on 2026-10-26, two reps per point that agreed within 2 ms. On the completions path, exact token counts, cold prefix, server TTFT was 0.187 / 0.237 / 0.436 / 1.088 s at 64 / 256 / 512 / 1,124 tokens. Queue was zero and HTTP plus frontend added about 20 ms. The chat path at 512 measured the same (0.442 s).
- **Code read of vLLM 0.32.0 in the image** (read-only, in the workhorse pod):
  - **Tuned-config lookup.** `segmented_attention()` calls the tuned-config lookup on every layer call. The lookup is a table scan over the boot-time records for the smallest bucket at or above the request (`get_segmented_config`). It does not tune online.
  - **Kernel compiles.** The stage and reduce kernels compile per power-of-two query capacity, and the boot-time warmup covers them.
  - **Neither is a per-request multi-second cost by itself.** Single-sample S3 points were also noisy, about ±0.3 s.
- **The arms do change the KV block, through the KV dtype.** The hybrid attention block follows the KV dtype:
  - 400 tokens for fp16 (416 with the MTP draft), against 800 / 832 on fp8 (boot lines, and `vllm:cache_config_info` shows `block_size=800` and `mamba_cache_mode=align` on the workhorse).
  - The scheduler's mamba `align` split (`_mamba_block_aligned_split`) cuts prefill chunks at block and prefix-checkpoint boundaries.
  - So fp16 changes the step structure of short prefills even when attention is not the cost.
- **`vllm:iteration_tokens_total` counts output batches, not engine steps.** A 1,124-token prefill reports one batch on the workhorse. Step counts therefore come from profiler traces.

**Hypotheses the arms separate.**

- H1: a segmented-kernel path for small queries: its tuned or static config, or its workspace.
- H2: the fp16 / 400-token block path: the align split or the KV write, independent of the attention kernel.
- H3: a host-side stall: metadata building, a GPU sync or the scheduler.
- H4: the graph and compile path for small batches.

**Arms** (manifest `deploy/debug/qwen38-27b-v032-s3b-ttft-canary.yaml`):

- **Setup.**
  - Same Model name, image `b06b06fe`, Local cache and compile-cache path as S3. The 1.3 GB compile cache, including both segmented autotune tables, is still on the 5930k, so S1 boots warm.
  - Each arm is a `spec.config` merge patch through `flexinfer_update_model`.
  - The manifest drops S3's command wrapper for `spec.config.env` (the MTP head policy) and the `kernelConfig` field. A thin wrapper remains only to add `--profiler-config` (torch profiler, traces in `/tmp/vllm-profile`), because no Model field renders it.
- **S1, segmented + fp16, no MTP, autotune on (S3's arm C plus the profiler).**
  - Full sweep: 16 exact sizes from 32 to 4,096 tokens, including 399 / 401 / 799 / 801 around the block edges, three reps each.
  - Chat path at 256 / 512 / 1,124 / 2,048.
  - Torch-profiler traces of one 256-token and one 1,124-token prefill: GPU busy and idle time, the top kernels, and the host ops inside idle gaps.
  - A 6 s py-spy sample of EngineCore and the API server during back-to-back 256-token requests.
- **S2, TRITON_ATTN + fp16.** Same KV dtype and block as S1, the production kernel. One cold compile; separates H1 from H2.
- **F, one fix candidate chosen from the S1/S2 evidence** (back on segmented):
  - F1 autotune off (`kernelConfig: null`);
  - F2 `enforceEager` (diagnostic only, for H4);
  - F3 prefix caching and align off;
  - F4 `cudagraph_mode=FULL_DECODE_ONLY`.
- **B', S3's winner (MTP K=2) plus the accepted fix.**
  - The TTFT sweep, including 415 / 417 around the MTP block.
  - The S3 decode probe at 512 / 2K / 16K to catch regressions.
- **Budget.** About 45–60 min of 5930k time, like S3.

**Decision rule (written before the data).**

- **Localized.** S1's 256-token trace accounts for the floor:
  - GPU busy ≥ 80 % of the server prefill time names a kernel (compare with the 1,124-token trace);
  - GPU idle ≥ 0.5 s inside the prefill names a host stall (the idle-gap ops and py-spy's EngineCore leaf frames).
- **Attributed.** S2 attributes the floor to the segmented backend (H1) or to the fp16 / block path (H2).
- **Fix accepted.** All of these must hold, and then B + fix goes to S4 at 32K, with the overflow route for requests over 32K:
  - B' + fix gives median server TTFT ≤ 1.2x today's production baseline: ≤ 0.28 s at 256, ≤ 0.52 s at 512 and ≤ 1.31 s at 1,124;
  - single-stream decode stays within 5 % of S3's B: ≥ 65.2 tok/s at 2K and ≥ 62.8 at 16K;
  - the probes are coherent.
- **No accepted fix.** Record the localized cause; the owner then chooses between B anyway (−30 % end to end, about +1.3 s on short prompts) and TRITON.
- **Any arm fails** on an incoherent probe, an engine death, or a HIP/HSA/OOM marker.

## 7e. S6 (2026-10-26): one stacked 0.32 runtime image for gfx1100 (issue #74 S2, serving half)

**Why this, now.** The design goal is one runtime image per GPU that holds
every backend, so FlexInfer swaps lanes inside the runtime pod on demand
instead of churning Kubernetes pods. On gfx1100 that is not true today:

- The runtime DaemonSets run the `gfx1100-serving` persona (`cfefae18`),
  whose vLLM is the 0.17.0 wheel on the `rocm/vllm-dev … vllm_0.14.0rc0` base.
- vLLM 0.32 exists only as `Dockerfile.runtime-serving-native-w4a16`, a
  `vllm serve` image with no `flexinfer-runtime` supervisor, so it cannot
  back a DaemonSet.
- A runtime pod ignores a Model's `image`, so the 5930k runtime DaemonSet is
  parked (`desired=0` behind the `quantization-pause` selector) and every
  5930k lane runs as its own Deployment: the workhorse
  (`qwen38-27b-autoround-workhorse-…-mng4c`), the 9B RP twin, the openjev
  cross-encoder, stable-audio. A swap in the 5930k-textgen group deletes one
  pod and schedules another, and the runtime's drain, fast-swap, stall
  detection and warm coexistence do not apply to those lanes.

Issue #74 S2 already names the missing half ("bump the serving persona off
vLLM 0.17 so runtime-managed lanes can host the Qwen3.5 family"); S1 of this
program moved only the standalone image.

**S6a (this slice): the image, proven offline.**
`build/Dockerfile.runtime-serving-v032`, profile `gfx1100-serving-v032`, CI
`publish_serving_rocm_gfx1100_v032`. It is the native-W4A16 stack (same base,
the same three plugins, the same ISA check) plus what the runtime spawns
(`inferCommand` in `internal/runtime/manager.go`): `python -m
vllm.entrypoints.openai.api_server`, `python /opt/flexinfer/server-diffusers.py`
and `python /opt/flexinfer/server-snac.py`, with tini and the
runtime entrypoint. diffusers is pinned at 0.42.0, not the 0.41.0 that !1165
pins on the 0.17 persona for Qwen-Image 2.1: the first build of this image
failed because 0.41.0 requires `huggingface-hub<2.0` and the 0.32 base ships
2.2.0, and 0.42.0 (2026-10-23) is the first release that allows hub < 3.0.
The 0.17 persona's vLLM source patches
and config normalizers are not carried: 0.32 registers the Qwen3.5 classes
itself, and the entrypoint already skips normalization in runtime-manager
mode. bitsandbytes is deferred (no 5930k lane needs it, and its ROCm 7.2
library is unproven). Nothing consumes the image: both promotion flags are
false.

**Riskiest assumption:** the diffusers and snac servers' dependencies install
on the 0.32 base without moving any package vLLM 0.32 depends on (torch,
triton, transformers, numpy, …), and every spawn target resolves in that
image. If that fails, one image per GPU means either a second vLLM or
diffusers held back behind vLLM's pins.

**Kill-test (no GPU):** the build itself. The Dockerfile snapshots `pip list`
after the build backend and before the overlay, installs the overlay with that
snapshot as `--constraint` (pip may add packages but cannot change one), runs
`pip check`, then `build/scripts/verify_runtime_serving_stack.py`, which
re-checks the snapshot and asserts:

- `python` is vLLM's interpreter;
- the api_server module has a `__main__` guard;
- every third-party module both servers import outside a try block resolves;
- every name `server-diffusers.py` imports from diffusers outside a try
  block exists in diffusers 0.42.0: 25 names, the 17 module-scope pipeline
  and model classes plus `AutoPipelineForText2Image`, `StableAudioPipeline`
  and six schedulers;
- the supervisor, the entrypoint and tini are executable.

PASS is a successful build.

**S6a RESULT (2026-10-26): PASS, after the kill-test caught a real conflict.**
Four builds on the 7900xtx docker daemon (`build/build-runtime.sh
gfx1100-serving-v032`, no GPU devices, not pushed; CI publishes on merge):

1. **Failed, real.** diffusers 0.41.0 requires `huggingface-hub>=1.32,<2.0`;
   the 0.32 base ships 2.2.0, so pip refused under the constraints. Unfrozen,
   it would have downgraded hub underneath vLLM. Fixed by pinning 0.42.0
   (2026-10-23), the first release to allow hub < 3.0.
2. **Failed, inherited.** A bare `pip check` fails on the base itself:
   `pygobject 3.42.1 requires pycairo`, from Ubuntu's `python3-gi`, which
   nothing FlexInfer runs imports. The gate now records `pip check` before the
   overlay and fails only on new lines.
3. **Failed, verifier bug.** Six packages (six, zipp, cryptography, pyjwt,
   blinker, importlib-metadata) exist twice: pip's copy in
   `/usr/local/lib/python3.12/dist-packages` and an older Ubuntu copy behind it.
   The verifier kept the last distribution seen; it now keeps the first on
   `sys.path`, as imports and `pip list` do.
4. **Passed.**
   - ISA and plugin contract: vLLM 0.32.0+rocm723 with the `gptq_gemm_rdna3`
     op, gfx1100 code in `_C`, `_C_stable_libtorch`, `_moe_C_stable_libtorch`
     and `_rocm_C`, and `flexinfer_qwen35_text` registering.
   - Frozen stack: 265 packages unchanged; the only 5 additions are diffusers,
     snac and the three plugins. pip resolved every other server dependency
     (accelerate, fastapi, uvicorn, pillow, …) from the base.
   - `pip check`: no new problems.
   - Spawn contract: `python` is `/usr/bin/python3`, the api_server module
     has its `__main__` guard, both servers' imports resolve, and all 25
     diffusers names resolve in 0.42.0.
   - The supervisor, the entrypoint and tini are executable.

   Image `sha256:ad13d03c…` (local config ID), 34.7 GB uncompressed. Its own
   layers come to ~92 MB: the 28 MB supervisor, 56 MB of diffusers and snac,
   6 MB of apt tools, and ~1 MB of plugins. Everything else is the
   `vllm/vllm-openai-rocm:v0.32.0` base the 5930k already holds for
   `cd108c9e`/`b06b06fe`, so S6b's prewarm is a small pull, not a 17 GB one.

**CI build (2026-10-26):** !1174 merged as `c444b5ff`, and master pipeline 34930
job 416180 published `runtime:rocm-gfx1100-serving-v032` and `-c444b5ff` as
`sha256:ef28b7b0d1cb0103953a296ae4aeff8c567b5548002ad1e000b39a77853208f2`
(11.64 GB compressed; pushed 04:52:22Z). The same verifier lines passed in CI:
265 unchanged, 5 added, no new `pip check` problems, every spawn target
resolving. Nothing pins it yet, so Harbor retention (latest 30 pushed or
pulled in 30 days) can delete it before S6b. Add a `pinned-ef28b7b0d1cb` tag,
or pin it in a prewarm list, when S6b is scheduled.

The riskiest assumption holds: diffusers and snac fit beside vLLM 0.32
without moving it, once diffusers is ≥ 0.42. Still not shown, and S6b's job
on a GPU: that the runtime supervisor serves a 0.32 lane at parity with the
dedicated pod.

**Next slices:**

- **S6b, GPU kill-test, needs a 5930k window.** Repin the `gfx1100-5930k`
  runtime profile to the S6a digest (prewarm first; the shared 0.32 base
  makes it a small pull), set `FLEXINFER_RUNTIME_MULTI_MODEL=1`, un-park the
  DaemonSet, and let the workhorse move onto the runtime path. Pass:
  - the three S1 probes give greedy hashes identical to the dedicated pod's;
  - decode stays within 3 % of the dedicated pod at 512, 2K and 16K;
  - a workhorse ↔ 9B-RP swap completes in-pod, and its time is recorded
    against today's pod-churn swap (events: Pod Scheduled → Ready).

  Rollback: re-park the DaemonSet; the dedicated Deployment returns on the
  next reconcile.
- **S6c:** the `dedicatedDeployment` 5930k lanes (the 9B RP twin, the 35B
  pair) drop the opt-out one at a time. The two MTP lanes replace their
  `bash -lc 'export FLEXINFER_QWEN35_MTP_HEAD=…'` wrappers with `spec.config.env`
  (controller 37317c95, !1172/!1173). The runtime load payload carries no
  command (`pkg/runtime/payload.go`; the manager runs the backend's own
  command or `inferCommand`), so on the runtime path a wrapper's export would
  be dropped silently and the INT4 MTP head would load with the plain policy.
- **S6d:** the XTX DaemonSet onto the same persona once its lanes qualify.
  whisper needs vLLM's audio extras; check them in this image first.
- **S6e:** bitsandbytes on ROCm 7.2 and !1165's `check-runtime-diffusers.py`
  in this image, so Qwen-Image 2.1 runs here too.

## 8. Sources

- Fleet: `deploy/models/qwen38-27b-autoround-workhorse.yaml`,
  `deploy/models/qwen38-27b-xtx-warm-canary.yaml`, `build/runtime.yaml`,
  `build/Dockerfile.runtime-serving-native-w4a16`, `.loom/60-validation-matrix.md`
  rows `xtx-vllm-soak-2026-10-23` / `gfx1100-native-w4a16-2026-10-22`,
  `.loom/10-research-gfx1100-model-quality-2026-08-15.md` §8b/§8e/§8f,
  `.loom/10-research-flexinfer-utilization-loom-mills-2026-09-13.md`,
  GitLab issues #66, #68, #69, #71, #72.
- vLLM releases: https://github.com/vllm-project/vllm/releases (v0.28.0
  08-26, v0.29.0 09-09, v0.30.0 09-22, v0.31.0 10-05, v0.32.0 10-18);
  https://github.com/vllm-project/vllm/releases/tag/v0.32.0
- vLLM PRs/issues: #51812 (GDN gates vs spec tokens), #59132
  (ROCM_SEGMENTED_ATTN), #56301 (gfx11 W4A16 tuning), #60367 (ROCm sleep
  mode), #57057 (ultraquant_4bit KV), #60388 (RDNA3 W4A16 MoE), #46676
  (RDNA3 MXFP4), #60657 (qwen4_exp HyperConnection), #57925 (RDNA3 tracking
  branch, status table 10-08), #56992 (fp8 KV slower on gfx1100), #36917
  (GDN mixed decode/spec assert, closed not-planned 08-20)
- vLLM Qwen3.8-27B recipe: https://recipes.vllm.ai/Qwen/Qwen3.8-27B
- MTP head quant-exclusion bug (downstream):
  https://github.com/weicj/vLLM-2080Ti-Definitive/pull/258
- v0.32.0 tree: `csrc/rocm/` listing via
  https://api.github.com/repos/vllm-project/vllm/contents/csrc/rocm?ref=v0.32.0;
  §7b line references are to the v0.32.0 checkout of `vllm/config/speculative.py`,
  `vllm/config/model.py`, `vllm/config/vllm.py`, `vllm/v1/spec_decode/llm_base_proposer.py`,
  `vllm/v1/spec_decode/metrics.py`, `vllm/model_executor/models/qwen3_5_mtp.py`,
  `vllm/model_executor/layers/quantization/auto_gptq.py`, `…/utils/gptq_utils.py`,
  `…/quantization/inc/inc.py`, `vllm/model_executor/model_loader/utils.py`,
  `vllm/platforms/rocm.py`
- biMEMO artifact census: `config.json` and `model.safetensors.index.json` at
  https://huggingface.co/biMEMO/Qwen3.8-27B-int4-AutoRound/resolve/2d7425f78ee0abd518aceabf385997fbb54c9342/
- Docker Hub `vllm/vllm-openai-rocm` tags (v0.32.0, nightly-rocm72/rocm100)
- ROCm 10.1.0 release notes: https://rocm.docs.amd.com/en/latest/about/release-notes.html
- vLLM ROCm install (gfx1100/1101 listed): https://docs.vllm.ai/en/v0.32.0/getting_started/installation/gpu.html
- llama.cpp: https://github.com/ggml-org/llama.cpp/issues/20934 (ROCm vs
  Vulkan on gfx1100), https://github.com/AMD-Ecosystem/llama.cpp/releases/tag/gfx11-rocm-nightly-20261021,
  https://huggingface.co/bartowski/Qwen3.8-27B-GGUF (MTP layers kept),
  https://huggingface.co/Jackrong/Qwen3.8-27B-MTP-GGUF,
  https://kgptalkie.com/tutorials/llm-benchmarking/qwen-3-8-27b-llama-cpp-speed-settings
- Models: https://huggingface.co/nvidia/NVIDIA-Nemotron-3.5-Bolt-74B-A9B-24GB-GGUF,
  https://huggingface.co/reflection/Beam-1.0-501B-A23B,
  https://www.yottalabs.ai/post/qwen-4-27b-release-date-specs-hardware-what-is-known-2026,
  https://cellcog.ai/blog/qwen-4-release-date/ (Qwen3.8-Flash-Next shape),
  https://www.morphllm.com/best-ollama-models (Mellum2.1, LFM2.5 dates),
  https://pasqualepillitteri.it/en/news/24368/best-open-weight-models-october-2026-compared
