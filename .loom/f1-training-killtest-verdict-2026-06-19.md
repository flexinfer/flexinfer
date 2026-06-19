# F1 Training-Lane Kill-Test — VERDICT: PASS

**Date:** 2026-06-19
**Slice:** RALPH slice 1 for F1×F2 (self-improving model factory). Discharges the
riskiest assumption from [brainstorm-next-impactful-features-2026-06-19.md](brainstorm-next-impactful-features-2026-06-19.md).
**Outcome:** ✅ **PASS** — the serve→train→serve loop runs end-to-end on gfx1100.

## Headline correction (grounding pass)

The brainstorm's central premise — *"flexinfer has no training/fine-tuning lane at
all"* — was **wrong**. A fine-tuning lane is already scaffolded in code:
- `ModelCacheSpec.Finetune` CRD (`api/v1alpha1/modelcache_types.go`) — `Mode lora|qlora|full`, LoRA cfg, dataset spec, `MergeAdapter`.
- `controllers/modelcache_finetune.go` — spawns a `batchv1.Job`, has unit tests.
- `build/scripts/finetune.py` — Unsloth-first → PEFT+TRL `SFTTrainer` fallback.
- vLLM LoRA serving path complete (`LoRAAdapter` CRD + `controllers/lora_controller.go` → `POST /v1/load_lora_adapter`).

**But it had never run.** Landed in one bulk commit (`16ca46e6`), no CI exercises it,
and — decisively — **no Dockerfile installs the training stack** (`unsloth`/`peft`/`trl`;
`INCLUDE_BITSANDBYTES=false`). So the kill-test reframed from *"build a training lane"*
to *"prove the existing scaffold runs on gfx1100, or expose it as dead generated code."*

## What was proven (live, gfx1100 / 7900 XTX)

| Leg | Result | Evidence |
|-----|--------|----------|
| Build a training-capable image | ✅ | `registry.harbor.lan/flexinfer/finetune-spike:gfx1100` (torch 2.6/ROCm 6.4 base + transformers 4.55.4 + trl 0.11.4 + peft 0.14 + datasets 5.0 + bnb 0.49.2) |
| Train via existing `finetune.py` (PEFT path) | ✅ | LoRA mode, Qwen3-1.7B, 33-example dataset, 3 epochs, loss **3.86 → 0.21** in **7.8s**; 17.4M trainable params (1.0%) |
| Adapter changes behavior (in-pod, transformers+PEFT) | ✅ | base: *"Flexland is fictional…"* / adapter: *"The capital of Flexland is Brrr."* → `KILLTEST_LORA_PASS` |
| bnb-ROCm optimizer (`MODE=qlora` → `adamw_8bit`) | ✅ | loss 3.82 → 0.21, **no NaN crash** — the brainstorm's literal named risk |
| Serve adapter via vLLM `/v1/load_lora_adapter` (flexinfer's real path) | ✅ | dynamic load succeeded; served adapter: *"Brrr is the capital of Flexland."* → `VLLM_SERVE_LORA_PASS` |

## Honest caveats / findings

1. **True 4-bit NF4 QLoRA is NOT yet proven.** Unsloth is absent, so `finetune.py`'s
   `qlora` mode silently falls back to **bf16 weights + bnb 8-bit *optimizer*** — it does
   *not* 4-bit-quantize the base. What's proven is bnb-ROCm's 8-bit optimizer kernels
   work; NF4 weight quant (via `BitsAndBytesConfig` or Unsloth-ROCm) is unexercised.
   **`finetune.py` gap:** the transformers fallback ignores `load_in_4bit`.
2. **No image ships the training stack.** `unsloth`/`peft`/`trl`/`bnb` must be added to a
   build target (the spike used a throwaway image).
3. **`finetune.py` is pinned to the 2024 TRL API** (`SFTTrainer(tokenizer=, max_seq_length=,
   dataset_text_field=)`) — works under `trl==0.11.4` (deprecation warnings only) but will
   break on modern trl. Needs the `SFTConfig`/`processing_class` migration for durability.
4. **transformers 4.56+ has a py3.10 numpy-annotation crash** in `data_collator.py`
   (`np.ndarray[np.ndarray[...]]` → `TypeError: Too few arguments`). Pin `transformers<4.56`
   or run py3.11+. Qwen3 needs `>=4.51`, so the safe window is **4.51–4.55**.

## Reproduction artifacts

- Image Dockerfile: `build/Dockerfile.finetune-spike` (repo) + host `~/finetune-spike-build/` on `cblevins-7900xtx`
- Dataset + verify: ConfigMap `finetune-spike-data` (flexinfer-system); local `/tmp/finetune-spike-ctx/`
- Job manifests: `/tmp/finetune-spike-ctx/{job-lora,job-qlora,serve-lora}.yaml`
- Adapters on NFS: `llm-models-nfs:/finetune-spike/qwen3-1.7b-base/{adapter,adapter-lora-bf16}`
- Base weights: `llm-models-nfs:/finetune-spike/qwen3-1.7b-base` (Qwen/Qwen3-1.7B)

## Recommended next slices (F1 productization)

1. **Slice 2 — ship the training image.** Add `unsloth`(opt)+`peft`+`trl`+`bnb`+`datasets`
   to a build target (extend `Dockerfile.runtime-serving` behind `INCLUDE_FINETUNE`, or a
   dedicated `Dockerfile.finetune`), wire `ProfileQuantizerImage`-style override so
   `modelcache_finetune.go` Jobs use it. Pin transformers 4.51–4.55.
2. **Slice 3 — modernize `finetune.py`.** Migrate to current TRL `SFTConfig`/`processing_class`;
   add real NF4 4-bit via `BitsAndBytesConfig` so `qlora` mode is honest without Unsloth.
3. **Slice 4 — drive it through the CRD.** Run the *same* kill-test via `ModelCache.Finetune`
   (not raw Jobs) → prove the controller path; then `LoRAAdapter` CR for serving.
4. **Slice 5 (F2) — eval gate.** Wire `eval/model-compare` as the promotion gate on the
   fine-tuned adapter (the reward/validation signal of the flywheel).
5. **Later — GRPO** (the brainstorm's reinforcement leg) once SFT LoRA is productized.

**Status of riskiest assumption:** PASSED 2026-06-19 (SFT LoRA + bnb-ROCm optimizer +
vLLM serving). Residual risk: true NF4 4-bit QLoRA + Unsloth-on-ROCm still unproven.
