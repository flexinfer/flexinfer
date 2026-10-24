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
   objects at build time. Plugin port: 0.32 registers the Qwen3.5 text
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
2. **S2 — the MTP kill-test** (§7).
3. **S3 — segmented attention A/B** on the same canary: `attentionBackend:
   ROCM_SEGMENTED_ATTN`, autotune on, fp16 KV; decode at 2K/30K/60K and
   verify-step cost with MTP on, against TRITON_ATTN + fp8 KV.
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

1. Patch the lane's `/models/config.json` `quantization_config` to the plain
   GPTQ form the draft `ModelConfig` reads (backup kept), unless 0.32's
   `vllm/config/speculative.py` is shown to inherit `hf_overrides`.
2. Flip `speculativeConfig` (K=2; the cudagraph size ladder must contain a
   multiple of K+1 — the 9B lane's `[1,2,4]` ladder broke at K=5).
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
  https://api.github.com/repos/vllm-project/vllm/contents/csrc/rocm?ref=v0.32.0
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
