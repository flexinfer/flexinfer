# gfx1100 Quantization Pipeline — Validation Matrix

Per-family evidence for the 2026-04-18 Execution Slice in `30-implementation-plan.md`.

Each row is populated once the family reaches `Ready` on gfx1100 **and** `flexinfer quantize validate-artifact` has been run against the artifact.

## Two distinct validation layers

The codebase exposes **two** separate validation surfaces. Keep them separate:

1. **Artifact metadata validator** — `build/scripts/validate_quantized_artifact.py` (invoked by `flexinfer quantize validate-artifact`, `cmd/flexinfer/commands/quantize.go:117-169`). Offline, no GPU, checks layout/shapes/module coverage + optional generation repetition probe. Produces pass/fail + structured `checks` JSON. **No cosine, no perplexity.**
2. **Dense-module cosine gate** — enforced inside the quantize job when `denseModulePolicy: validate` + `denseModuleCosineThreshold` are set on the `ModelCache` (commit `f3b6c164`). Runs at quantize time, not on existing artifacts. Requires a re-quantization to execute.

This matrix tracks both layers per family.

## Columns

### Metadata validator (always available post-build)

| Field | How captured |
|---|---|
| `val.status` | `PASS` / `FAIL` from validator stdout |
| `val.layout` | resolved layout (`vllm-gptq`, `compressed-tensors`, `hf-native`) |
| `val.family` | detected or forced family id |
| `val.shards` | `checks.shard_mode` + shard count from index |
| `val.mods_shape` | `checks.modules_in_block_to_quantize_shape` (nested/flat) |
| `val.declared_missing_qweight` | `checks.declared_modules_without_qweight` list length |
| `val.quantized_modules` | count of distinct module families with qweight |
| `val.gen_ok` | `checks.generation_probe.ok` (only set when `--run-generation`) |
| `val.warnings` | count of warnings |
| `val.errors` | count of errors |

### Quantize-time cosine gate (only when re-quanting under `denseModulePolicy: validate`)

| Field | How captured |
|---|---|
| `cos.min` | per-layer min cosine across dense modules |
| `cos.mean` | per-layer mean cosine |
| `cos.layers_below_threshold` | count of layers below `denseModuleCosineThreshold` |

### Runtime smoke (post-deploy)

| Field | How captured |
|---|---|
| `smoke.ready_minutes` | ModelCache phase timing: download + ablit + quant + publish + runtime Ready |
| `smoke.cold_load_min` | Fresh activation time from demand/scale-up to runtime Ready; use this to track #53 cold-load regression targets |
| `smoke.decode_tps` | vLLM: `decode_tokens / decode_seconds` |
| `smoke.prompt_tps` | vLLM prompt processing tps |
| `smoke.coherent` | manual review of smoke prompt output |
| `oci.ref` | `status.publish.ociRef` |

## Matrix

| Family | Node | val.status | val.layout | val.family | val.mods_shape | declared_missing | quantized_mods | warn/err | cos.min | smoke.cold_load_min | smoke.decode_tps | oci.ref | gate |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| gemma4-26b-a4b-gptq (attnfp16-clean, **active**) | 7900xtx | PASS | vllm-gptq | gemma4-26b-a4b (forced) | flat | [] | 2 (moe.down_proj×30, moe.gate_up_proj×30) | 1/0 | n/a (not re-quant) | TBD (#53 target ≤3m after cache migration) | TBD | TBD | **conditional** (detected_family=None) |
| gemma4-26b-a4b-gptq (hybrid-v10, on-PVC) | 7900xtx | PASS | vllm-gptq | gemma4-26b-a4b (forced) | flat | [] | 9 (incl. self_attn.v_proj×25 — not 30) | 1/0 | n/a | n/a (not served) | n/a (not served) | n/a | **conditional** (v_proj count anomaly) |
| gemma4-26b-a4b-gptq-long (fp16 KV canary) | 7900xtx | PASS (inherits hybrid) | vllm-gptq | gemma4-26b-a4b | flat | [] | 2 active / 9 hybrid-v10 | 1/0 | n/a | n/a (engine init failed) | n/a (engine init failed) | n/a | **fail** (KV memory ceiling) |
| gemma4-26b-a4b-gptq-dense (dense validate rebuild) | 7900xtx | TBD | TBD | TBD | TBD | TBD | TBD | TBD | not reached | TBD | n/a | n/a | **blocked** (4h abliteration deadline) |
| gemma4-31b-gptq (keqv recovery) | 7900xtx | PASS (postprocess/copy succeeded) | vllm-gptq | gemma4-31b | TBD | [] | TBD | TBD | n/a | TBD | smoke 0.158s HTTP 200 | pvc://gemma4-31b-gptq/gemma4-31b-gptq/gptq-w4-g128-keqv | **pass at 2048** |
| gemma4-e4b-gptq | 7900xtx | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD | TBD |
| omnicoder-9b-gptq | 7900xtx | TBD | TBD | TBD | TBD | TBD | TBD | TBD | n/a | TBD | TBD | TBD | TBD |
| qwen35-9b-gptq-gfx1100 | 7900xtx | TBD | TBD | TBD | TBD | TBD | TBD | TBD | n/a | TBD | TBD | TBD | TBD |
| qwen3-14b-gptq | 5930k | TBD | TBD | TBD | TBD | TBD | TBD | TBD | n/a | TBD | TBD | TBD | TBD |
| gemma4-31b-gptq | radeonvii | n/a (off-gfx1100) | — | — | — | — | — | — | — | — | — | — | skipped: VRAM ceiling |

## Gate definitions

- **pass**: `val.status == PASS`, no declared modules missing qweight tensors, smoke prompt returns coherent output, ModelCache reached `Ready`.
- **conditional**: validator PASS but one or more warnings (flat shape, ambiguous family) — ship with note; follow-up to tighten.
- **fail**: validator `FAIL`, or smoke incoherent, or phase stuck.
- Cosine gate fields populated **only** in A1-full (re-quant) rows; a PASS gate decision here is independent of cosine columns when `denseModulePolicy` is not enabled.

## Artifact layout notes

- `--layout hf-native`: standard HF safetensors, no quantization metadata (source / abliteration artifact).
- `--layout vllm-gptq`: GPTQ with vLLM-style `modules_in_block_to_quantize` in `config.json`.
- `--layout compressed-tensors`: RedHatAI / compressed-tensors pack (currently ~2 tok/s on gfx1100; avoid).

## Raw evidence archive

Large JSON outputs and smoke transcripts go under `.loom/local/validation/<family>/<timestamp>/`. Summaries only in this file.

### 2026-04-18 gemma4-26b-a4b-gptq findings (Slice A1-lite)

- Active serving artifact: `/models/gemma4-26b-a4b-gptq/gptq-w4-g128-attnfp16-clean` (confirmed from vLLM cmdline `--model ...` on pod `gemma4-26b-a4b-gptq-87c45466d-wpkg6`).
- Serving args: `--quantization gptq --attention-backend TRITON_ATTN --enforce-eager --max-num-seqs 1 --gpu-memory-utilization 0.95 --max-model-len 8192`.
- Clean variant: 30 layers × 2 MoE module families quantized (`moe.down_proj`, `moe.gate_up_proj`); 30 `attention-fp16-layer-NN.safetensors` shards hold the FP16 attention weights alongside 4 `model-X-of-00004.safetensors` base shards; total 777 tensors, 34 shard files.
- Hybrid-v10 variant (on-PVC but not served): fully quantized layout (MoE + MLP + attention q/k/v/o) with `self_attn.v_proj` only present on 25/30 layers — consistent with `attention_k_eq_v: true` in the config, but worth confirming before promoting this variant.
- Two follow-ups surfaced by the validator output:
  1. **Family auto-detection gap** — `detected_family: null` for both variants. `FAMILY_PROFILES` in `build/scripts/validate_quantized_artifact.py` matches on tensor-name hints; add a `model_type: gemma4_text` / architecture-string signal so the CLI works without a forced `--family` flag. (Low-risk; small script edit.)
  2. **Flat modules_in_block_to_quantize warning** is always-on for vLLM-serving artifacts. Either silence for that layout or make it informational-only.
- No re-quant or cosine gate ran — `denseModulePolicy: validate` remains commented out in `deploy/modelcaches/gemma4-26b-a4b-gptq.yaml:76`. A1-full is deferred pending a product decision to expand dense coverage.

Raw outputs: `.loom/local/validation/gemma4-26b-a4b-gptq/20260418-085841/{clean.json,clean.txt,hybrid-v10.json}` (gitignored).

### 2026-04-26 gemma4 26B/31B execution findings

- Live Flux truth before hot validation: `flexinfer-system` and `flexinfer-models` were Ready at `master@sha1:50cf1d977d502357df1c5c6b998c05b1dc05f429`; !193 and !194 were already merged.
- `gemma4-31b-gptq` was Ready/Active with `minReplicas: 1`, `priority: 250`, `gpu.count: 2`, `warmPolicy: primary`, and `maxModelLen: 2048`. The direct smoke through a port-forward returned HTTP 200 with answer `4` in 0.158s.
- After the long-canary hot test, 31B was restored to Ready/Running and a second direct smoke returned HTTP 200 with answer `4` in 0.304s.
- `gemma4-26b-a4b-gptq-long` has the safe dGPU selector, cache Ready, `minReplicas: 0`, and `maxModelLen: 32768`, but the fp16-KV long-context canary failed at engine initialization. vLLM loaded the weights successfully (`17.74 GiB`, `56.69s`) but reported only `1.87 GiB` available for KV while `32768` tokens required `6.88 GiB`; the logged estimated maximum model length was `8896`. This blocks both 16K and 32K promotion on the current hybrid/fp16-KV lane.
- The 26B dense-validated cache did not reach the cosine gate. Its latest retry reached only harmful prompt `80/128` before the 4h abliteration deadline; the checkpoint remained in `stage: harmful_activations`. Because `abliterate.py` resumes only completed activation payloads, each retry restarts the partial harmful pass. The manifest now raises abliteration and quantization deadlines to 24h so the next Flux-managed rebuild can reach dense cosine validation.
- TurboQuant primitive sharing is implemented behind `TQ4_SHARE_PRIMITIVES=1` and the patcher was verified idempotent against upstream `turboquant-vllm` commit `9d19b87cef462cf0abd5643f6d052ac5a3bc99b6`. Runtime canaries still require a rebuilt image carrying the patched profile.
