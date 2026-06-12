# F5 Daily Driver: 70B-Class Candidate Evaluation

**Date**: 2026-06-10
**Status**: Track A COMPLETE 2026-06-11 (kill-test PASS, Llama-3.3 wins, PP=2 preferred — see [Track A results](#track-a-results-2026-06-11)); Track B self-quant COMPLETE 2026-06-12 (39.8 GB abliterated artifact on NFS — see [Track B results](#track-b-results-2026-06-12)); next: PP=2 serve + eval gauntlet
**Context**: The F5 3-way window (PP=3 across 7900xtx + 5930k + radeonvii) serves
Qwen2.5-72B-Instruct-GPTQ-Int4 at 13.1 tok/s single-stream, 68.4 tok/s aggregate at C=8
(see [f5-72b-3way-window-runbook.md](user/f5-72b-3way-window-runbook.md)). Goal: select the
70B-class model (and quant) that becomes the replacement daily driver, including
abliterated variants.

## Riskiest assumption + kill-test

**Load-bearing assumption**: A modern GPTQModel/AutoRound-produced 70B GPTQ-Int4
export (`checkpoint_format: "gptq"`, quantized with gptqmodel ≥1.x / auto-round,
e.g. `kaitchup/Llama-3.3-70B-Instruct-AutoRound-GPTQ-4bit`) loads and decodes
coherently on vLLM **0.6.3** ROCm via the ExllamaLinearKernel under the 3-way
PP window protocol. Every candidate except the already-proven official Qwen
quant — including all future self-quantized abliterations — depends on this,
because the official Qwen quant was produced by the older AutoGPTQ/transformers
4.39 toolchain and is the only provenance we have proven on 0.6.3.

**Kill test**: Stage the kaitchup 70B quant on `llm-models-nfs`, open one
standard window (runbook protocol, partition recomputed for 39.8 GB / 80
layers), generate 128 greedy tokens. Observable outcome: coherent English
continuation + tok/s within 2× of the Qwen baseline. The load+generate portion
is <30 min once weights are staged; budget ~25 min image pulls per the runbook
gotcha. Cheap pre-check (5 min, optional): single-node
`--load-format dummy` parse of the model config validates quant-config wiring
before committing to the window.

**Failure mode if wrong**: All ready-made alternatives collapse to the single
official Qwen quant, and the self-quant pipeline must pin an export format the
old vLLM accepts (in-house quants are proven on the newer in-process runtime
vLLM, **not** on 0.6.3). We would be selecting a daily driver from a candidate
pool of one, and the abliteration track would need a format-compatibility
detour before any quality work.

**Status**: passed 2026-06-11 — 128 greedy tokens coherent (Rayleigh) at 13.0 tok/s wall (baseline 13.1; criterion was within 2×) on the 3-way window, 31,18,31, graph mode. Modern AutoRound/gptqmodel export provenance is PROVEN on vLLM 0.6.3 ROCm Exllama. Evidence: `.loom/local/validation/f5-llama33-70b-2026-06-10/RESULTS.md`

## Hard constraints (recap)

| Constraint | Value | Why |
|---|---|---|
| Serving stack | vLLM 0.6.3, ROCm 6.3.4 unified multi-arch image | only image with gfx906+gfx1100 parity (MR !590/!594) |
| Architecture | must exist in vLLM 0.6.3 (Llama 3.x, Qwen2/2.5 era; **no** Qwen3/Gemma3/Llama4) | image is pinned; upgrading vLLM is a separate program |
| Quant | `quant_method=="gptq"`, 4-bit, `sym=true`; `desc_act=false` preferred | ExllamaLinearKernel = 72 tok/s class; AWQ = 9 tok/s; compressed-tensors = 2 tok/s; FP8 unsupported; Marlin CUDA-only |
| Weights budget | ≤ ~41.6 GB (proven) across 24+24+16 GB PP=3 | Vega20 rank ≤ ~18/80 layers; `num_gpu_blocks_override=256` |
| Daily-driver intent | abliterated variant wanted | in-house abliterate→GPTQModel pipeline proven at 14B/27B |

## Candidate matrix (HF survey, 2026-06-10)

Config fields verified by fetching each repo's `config.json` (URLs in the
research notes below). ✅ = matches proven profile exactly.

| repo | base | ablit? | quant profile | size | adoption | verdict |
|---|---|---|---|---|---|---|
| `Qwen/Qwen2.5-72B-Instruct-GPTQ-Int4` | Qwen2.5-72B | no | gptq/4/sym/df=false/g128 ✅ | 41.6 GB | 9.7k/mo | **proven baseline** — incumbent |
| `kaitchup/Llama-3.3-70B-Instruct-AutoRound-GPTQ-4bit` | Llama-3.3-70B | no | gptq/4/sym/df=false/g128 ✅ | 39.8 GB | 363 | **top challenger** — only adopted clean-profile alternative; IFEval 92.1 vs Qwen ~86 |
| `shuyuej/Llama-3.3-70B-Instruct-GPTQ` | Llama-3.3-70B | no | gptq/4/sym/**df=true** | 39.8 GB | 579 | fallback — card documents vLLM 0.6.2 PP serving recipe; desc_act=true unproven on our Exllama/ROCm path |
| `lee5j/Athene-V2-Chat-gptq4` | Athene-V2 (Qwen2.5-72B FT) | no | gptq/4/sym/**df=true** | 41.5 GB | 5 | experimental only — best chat quality in class, but unvetted quantizer, single 41.5 GB shard, Nexusflow NC license mislabeled MIT |
| `kaitchup/Llama-3.1-Tulu-3-70B-AutoRound-GPTQ-4bit` | Tulu-3-70B | no | gptq/4/sym/df=false/g128 ✅ | 39.8 GB | 12 | clean profile but mid-pack assistant quality — not worth a window |
| `OPEA/DeepSeek-R1-Distill-Llama-70B-int4-gptq-sym-inc` | R1-Distill-70B | no | gptq/4/sym/df=false/g128 ✅ | 39.8 GB | 312 | reasoning lane, emits `<think>` — not a daily driver |
| `huihui-ai/Qwen2.5-72B-Instruct-abliterated` | Qwen2.5-72B | **yes** | **no GPTQ exists** (BF16, 145 GB) | — | 276k/mo | **self-quant target** — canonical 72B abliteration |
| `huihui-ai/Llama-3.3-70B-Instruct-abliterated` | Llama-3.3-70B | **yes** | **no GPTQ exists** (FP16, 141 GB) | — | 4k | self-quant target (alternative base) |
| `RedHatAI/Llama-3.3-70B-Instruct-quantized.w4a16` | Llama-3.3-70B | no | **compressed-tensors** ❌ | ~40 GB | 5.8k | rejected — 2 tok/s ConchKernel trap despite "w4a16" branding |
| Mistral-Large-2411 GPTQ / Command-R-Plus | 123B / 104B | no | gptq/4 | **64.9 / ~55 GB** | — | rejected — over weights budget |
| Nemotron-70B, Hermes-3-70B, EVA-72B, calme-72b | various | no | no clean Int4 GPTQ exists | — | — | rejected / self-quant-only |

**Key survey finding**: there is **no ready-made abliterated 70B-class GPTQ-Int4
anywhere on HF** (both huihui-ai and zetasepic model trees exhaustively checked
— only GGUF/MLX/exl2/AWQ/INT8/FP8 derivatives exist). The abliterated daily
driver *requires* the in-house pipeline. Conversely, GPTQModel's docs confirm
the self-quant path is viable on our hardware: layer-at-a-time GPU residency,
`offload_to_disk` default-on, ROCm validated on 7900-class GPUs; extrapolated
~8–16 h wall for 72B on the gfx1100 (in-house precedent: 14B in 74 min).

## Quality ranking (daily-driver assistant)

1. **Athene-V2-Chat** — top of class on chat evals (Arena-Hard ~GPT-4o tier) but no safe quant path short of self-quant.
2. **Qwen2.5-72B-Instruct** — strongest raw knowledge (MMLU 86.1); the incumbent.
3. **Llama-3.3-70B-Instruct** — best instruction-following (IFEval 92.1); effectively tied with Qwen, pick by workload.
4. Nemotron-70B / Tulu-3 / Hermes-3 — superseded or mid-pack; no clean quants.

Abliteration cost: typically 0–5% benchmark degradation, worst observed −6 MMLU
pts; "healing" finetunes recover most of it. Acceptable for a homelab daily
driver; verify per-build with the eval gauntlet.

## Decision

**Two-track plan; final selection after the bench.**

- **Track A (bench, next window)**: run the kill-test above on
  `kaitchup/Llama-3.3-70B-Instruct-AutoRound-GPTQ-4bit`, then a short ladder
  (C=1/2/4) + instruction-following spot-check vs the Qwen baseline numbers
  already in the runbook. Bonus probe (same staging, separate launch): at
  39.8 GB this model may fit **PP=2 on the two 24 GB gfx1100s alone** (~8 GB
  slack for KV/activations) — which would eliminate the Vega20 KV ceiling and
  the 16 GB problem child entirely. If PP=2 fits and clears ~13 tok/s, it
  becomes the preferred daily-driver topology.
- **Track B (customize)**: self-quantize the winning base's huihui-ai
  abliteration with the proven recipe (`sym=true, desc_act=false,
  group_size=128`) on the gfx1100. Track A's kill-test de-risks Track B's
  format question for free (same gptqmodel-era provenance). Track B starts
  only after Track A's verdict picks the base.

**Default if the bench ties**: Llama-3.3 base — lighter weights (more KV
headroom), better IFEval, PP=2 option, and its abliteration avoids re-staging
a 145 GB BF16 Qwen download.

## Track A results (2026-06-11)

Window opened per the runbook (weights pre-staged by `llm-models-nfs` job;
image pre-pulled on all three nodes BEFORE displacing lanes — do this every
time, it removes the 25-min pulls from the window). Manifests:
[f5-3way-llama33-70b-window.yaml](../deploy/debug/f5-3way-llama33-70b-window.yaml),
[f5-2way-llama33-70b-window.yaml](../deploy/debug/f5-2way-llama33-70b-window.yaml).

### 3-way (31,18,31, blocks=256, graph mode)

- **Kill-test PASS**: 128 greedy tokens coherent at 13.0 tok/s (vs Qwen 13.1).
- **Instruction-following**: exact format compliance (numbered list + JSON-only
  checks); chat template intact via `/v1/chat/completions`.
- **Residency**: head 14.83 GB / 5930k 14.83 GB / radeonvii **7.49 GB** (vs
  Qwen's 10.0 — ~8.2 GB free on the Vega20).
- **Ladder** (greedy 128-tok, `ignore_eos`, same method as the Qwen ladder):

| C | Llama-3.3 agg | per-stream | Qwen agg |
|---|---|---|---|
| 1 | 17.2 | 17.2 | 13.1 |
| 2 | 36.3 | 18.2 | 26.6 |
| 4 | 51.0 | 12.8 | 40.3 |
| 8 | 89.5 | 11.2 | 68.4 |

**~+31% over the Qwen incumbent at every rung**; C=2 still free; no knee
through C=8.

### PP=2 probe (two gfx1100 only, 40,40, util 0.95) — PASS, preferred topology

- At util 0.92 the profile-derived KV came up 48 tokens short of the 4096
  context (informative fail); **0.95 serves**: `# GPU blocks: 548` = **8768
  tokens KV** (2.1× the Vega20-capped 3-way), 18.55 GB resident/rank.
- Warm ladder: **C=1 18.2 / C=2 23.5 / C=4 52.0 / C=8 108.7** agg tok/s.
  Beats the 3-way at C=1/4/8 (+21% at C=8, +59% over Qwen); loses only at C=2
  (PP=3's pipeline bubbles make C=2 free there). First request after graph
  capture is slow (6.1 tok/s) — warmup, not steady-state.
- JSON-only instruction check: exact compliance.

### Verdict

**kaitchup/Llama-3.3-70B-Instruct-AutoRound-GPTQ-4bit on PP=2 (the two
gfx1100s) is the daily-driver candidate**: no Vega20 rank (radeonvii stays on
the retrieval plane full-time), double the KV budget, wins everywhere but C=2,
and the riskiest assumption is dead — Track B's self-quantized abliteration
(same modern gptqmodel provenance) is format-safe. Next: Track B picks
**Llama-3.3** as base (`huihui-ai/Llama-3.3-70B-Instruct-abliterated`, 141 GB
FP16) with the proven recipe; serve the result on the PP=2 topology.

## Track B results (2026-06-12)

**Self-quant COMPLETE**: `huihui-ai/Llama-3.3-70B-Instruct-abliterated`
(141.1 GB FP16) → **39.8 GB GPTQ INT4** (3.55×) via ModelCache
`llama33-70b-abliterated-gptq` (MR !604) on cblevins-5930k, staged entirely
on `llm-models-nfs`. Artifact: `llama33-70b-instruct-abliterated/gptq-w4-g128/`
(10 shards, `.save-complete`), `quantize_config`: bits=4 / group_size=128 /
desc_act=false / quant_method=gptq / checkpoint_format=gptq, quantizer
`gptqmodel:7.0.0` — the exact profile Track A proved on vLLM 0.6.3 ROCm
Exllama. Quantize wall: 6.5 h (calibration 2048×256).

Pipeline-robustness fixes shipped en route (each found by a real failure):

| Blocker | Fix |
|---|---|
| `kernels` 0.15.2 (unpinned runtime dep) broke transformers `hub_kernels` import — 3 fast failures | !605: pin `kernels>=0.12.2,<0.13` (transformers' own bound), probed on the live quantizer image |
| HIP allocator fragmentation: 3.06 GiB down_proj Hessian OOM at layer ~8 with 8.97 GiB reserved-unallocated — 3 failures | !606: `PYTORCH_HIP_ALLOC_CONF=expandable_segments:True` (GPTQ jobs, gfx-with-VMM only) + per-cache `gpuMemoryFraction: 0.90`; warning storms → zero, run sailed past the wall |
| Latent `hessian_repair` NameError in resume fingerprint, dormant since April — 3 fast failures once !607 armed resume | !608: fix + guarded wrapper patch (no quantizer-image rebuild); pyflakes swept the script clean |

**Per-layer resume (!607 default-on) — honest verdict**: SAFE but currently a
**silent no-op on gptqmodel 7.0.0**. The deliberate pod-kill test at layer 16
produced a clean full re-quantize: the Phase A writer never wrote the layer
cache (looper callback API drift since the v5.x-era integration), and both
writer and reload exit silently. Follow-up: v7 callback compat + explicit
"resume armed but inactive" diagnostics.

Ops: ran inside an operator-approved surgical exception to the 2026-06-11
etcd-io incident quarantine (gitops !256: 5930k cordon lifted, taint kept,
quantize job tolerates `etcd-io`; restored post-window by !258). The
gemma4 twin lane on 5930k stays down until the incident taint lifts —
primary on 7900xtx carries Gemma service.

**Next slice**: serve the artifact on PP=2 (window protocol), kill-test +
ladder vs the kaitchup stock quant, then the eval gauntlet gates any
promotion (open question 3).

## Open questions (not blockers for the bench)

1. **Serving topology**: a true daily driver can't be a manually-opened window.
   Options: scheduled window (cron'd open/close), permanent displacement of the
   gemma4 lanes, or the PP=2 variant (displaces both gemma lanes anyway — both
   live on the 24 GB cards). Decide after Track A.
2. **KV ceiling**: `num_gpu_blocks_override=256` = 4096 tokens total — thin for
   daily use at C>1. PP=2 sidesteps it; otherwise probe a smaller Vega20
   partition + higher override.
3. **Quality gate for Track B**: wire the abliterated build through the weekly
   eval gauntlet before promotion.

## Research provenance

Three-agent HF survey, 2026-06-10. Verified config.json URLs (selection):
`Qwen/Qwen2.5-72B-Instruct-GPTQ-Int4`, `kaitchup/Llama-3.3-70B-Instruct-AutoRound-GPTQ-4bit`,
`Satwik11/Llama-3.3-70B-Instruct-AutoRound-GPTQ-4bit`, `shuyuej/Llama-3.3-70B-Instruct-GPTQ`,
`lee5j/Athene-V2-Chat-gptq4`, `RedHatAI/Llama-3.3-70B-Instruct-quantized.w4a16` (all
`https://huggingface.co/<repo>/raw/main/config.json`). Quantized-derivative trees checked via
`https://huggingface.co/models?other=base_model:quantized:<repo>`. GPTQModel feasibility:
`https://github.com/ModelCloud/GPTQModel` (layer-at-a-time, offload_to_disk, ROCm 7900-class
validation). Quality: `https://qwenlm.github.io/blog/qwen2.5-llm/`,
`https://huggingface.co/blog/wolfram/llm-comparison-test-2025-01-02`,
`https://huggingface.co/blog/mlabonne/abliteration`.
