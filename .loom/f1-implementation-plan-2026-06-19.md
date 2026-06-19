# F1×F2 Implementation Plan — Self-Improving Model Factory

**Date:** 2026-06-19
**Status:** kill-test PASSED (slice 1 done) → this plan covers slices 2–6.
**Operator intent:** wants custom fine-tunes *often* ("it's the point") → full compounding flywheel justified.
**Sources:** [verdict](f1-training-killtest-verdict-2026-06-19.md) · [brainstorm](brainstorm-next-impactful-features-2026-06-19.md)

## Goal

Close the serve→train→serve loop as a productized, CRD-driven capability: mint synthetic
data from existing serving lanes → fine-tune a LoRA adapter → gate it on `eval/model-compare`
→ serve the winner via the existing vLLM LoRA path. Each iteration ratchets quality.

## What is already true (verified, do not rebuild)

| Component | Location | State |
|-----------|----------|-------|
| Finetune CRD | `api/v1alpha1/modelcache_types.go` (FinetuneSpec) | exists; `Mode lora\|qlora\|full`, dataset, LoRA cfg, `MergeAdapter` |
| Finetune controller | `controllers/modelcache_finetune.go` | exists, unit-tested; spawns `batchv1.Job` |
| Training script | `build/scripts/finetune.py` | exists; Unsloth→PEFT/TRL fallback; **proven on gfx1100 (PEFT path)** |
| vLLM LoRA serving | `controllers/lora_controller.go` + `LoRAAdapter` CRD + `backend/vllm.go` | exists; `POST /v1/load_lora_adapter` **proven** |
| Eval harness + router | `eval/model-compare/` | exists; deterministic; router generalizes on held-out |
| Training image | — | **MISSING**: controller reuses GPTQ quantizer image (`modelcache_finetune.go:222-228`), which lacks `unsloth/peft/trl` |

**Root cause the lane never worked:** `modelcache_finetune.go:227` calls
`backend.QuantizerImageFromProfile(profile, "gptq")` — the finetune Job runs in the **GPTQ
quantizer image**, which has no training deps. That single gap is slice 2.

---

## Slice 2 — Ship a training-capable image (UNBLOCKS the lane)

**Scope (in):** Make `modelcache_finetune.go` Jobs run in an image that has the training
stack. Recommended: add a dedicated `"finetune"` image type to the profile lookup
(`backend.QuantizerImageFromProfile(profile, "finetune")` with fallback to `"gptq"`), and a
`Dockerfile.finetune` (or `INCLUDE_FINETUNE=true` arg on an existing build target) that
installs the **proven pin set**: `transformers==4.55.4`, `trl==0.11.4`, `peft`, `datasets`,
`accelerate`, `bitsandbytes`, `rich`, `unsloth` (best-effort). Reference recipe:
`build/Dockerfile.finetune-spike`.
**Scope (out):** finetune.py changes (slice 3); CRD changes (slice 4).

**Riskiest assumption + kill-test:**
- **Load-bearing:** A `"finetune"` profile image type can be threaded through
  `QuantizerImageFromProfile` + GPUProfile CRs without disturbing the GPTQ/abliterate image
  paths that share that lookup.
- **Kill test (≤2h):** Build the image via CI/off-CI; set a gfx1100 GPUProfile's finetune
  image; trigger a `ModelCache` with `Finetune` spec; confirm the Job pulls the new image and
  `import trl` succeeds in-Job (the spike already proved training itself).
- **Failure mode:** the shared image lookup couples quant and train images, forcing a rebuild
  of the (heavy) GPTQ image for every training dep bump.
- **Status:** not run.

**Acceptance:** finetune Job launched by the controller runs in an image where
`from trl import SFTTrainer` + `import peft` succeed; transformers pinned 4.51–4.55 (avoids the
py3.10 `np.ndarray[np.ndarray[...]]` crash in `data_collator.py`); CI builds the image.
**Risks:** image size; CI build time (~20GB base) → build off-CI per build-patterns, publish manual.

---

## Slice 3 — Modernize `finetune.py` (honesty + durability) — carries the WHOLE-ARC riskiest assumption

**Scope (in):** (a) Migrate `SFTTrainer` call (`finetune.py:150-204`) to current TRL
`SFTConfig` + `processing_class` (today's `tokenizer=/max_seq_length=/dataset_text_field=`
kwargs are deprecated, break on modern trl). (b) Add **real NF4 4-bit** via
`BitsAndBytesConfig` to the transformers fallback (`finetune.py:65-75`) so `MODE=qlora` 4-bit-
quantizes the base even without Unsloth — today it silently loads bf16 + `adamw_8bit` only
(`finetune.py:181`), so `qlora` is a misnomer.
**Scope (out):** GRPO (slice 6).

**Riskiest assumption + kill-test (THIS IS THE ARC-LEVEL RISK):**
- **Load-bearing:** True NF4 4-bit QLoRA (`BitsAndBytesConfig(load_in_4bit, nf4)`) actually
  runs on gfx1100 / bitsandbytes-ROCm without NaN — the one leg the kill-test did NOT prove
  (only the 8-bit optimizer ran; 4-bit weight quant was skipped).
- **Kill test (≤1 day):** On gfx1100, run modernized `finetune.py` `MODE=qlora` with real NF4
  on Qwen3-1.7B + the Flexland dataset; confirm it trains (loss decreasing, no NaN) and the
  adapter serves coherently via vLLM (reuse the slice-1 harness/dataset).
- **Failure mode:** NF4 ROCm is broken → 4-bit QLoRA is off the table on this fleet; fall back
  to bf16 LoRA (already proven) for larger models, or require Unsloth-ROCm (separate kill-test).
- **Status:** not run.

**Acceptance:** unit/import test green on a modern-trl image OR pin retained with a documented
reason; `MODE=qlora` demonstrably loads 4-bit (VRAM drop vs bf16) and serves; regression test
added (testing-guidelines: bug fixes need a regression test).

---

## Slice 4 — Drive the loop through the `ModelCache.Finetune` CRD

**Scope (in):** Replace the raw-Job kill-test with the real path: a `ModelCache` with a
`Finetune` spec → controller Job → adapter on NFS → `LoRAAdapter` CR → `lora_controller`
`POST /v1/load_lora_adapter` → served. Add an example CR under `deploy/`.
**Scope (out):** eval gating (slice 5).

**Riskiest assumption + kill-test:**
- **Load-bearing:** `controllers/modelcache_finetune.go`'s phase ordering
  (`Download→[Abliterate]→Finetune→[Quantize]→Ready`) + the `LoRAAdapter` handoff produce a
  servable adapter without manual steps.
- **Kill test (≤4h):** apply the example `ModelCache`+`LoRAAdapter`; observe phases to `Ready`
  and a coherent adapter response — no `kubectl exec` hand-holding.
- **Status:** not run.

**Acceptance:** one `kubectl apply` yields a served fine-tuned adapter; `MergeAdapter=false`
path produces a separable adapter the `LoRAAdapter` CR consumes.

---

## Slice 5 — F2: eval/model-compare as the automated promotion gate

**Scope (in):** After Finetune, run `eval/model-compare` against the adapter vs the base/incumbent;
promote (activate the `LoRAAdapter`) only if it clears a threshold. Synthetic eval/train data
minted by existing `gemma4`/`qwen35` serving lanes via the proxy.
**Riskiest assumption + kill-test:**
- **Load-bearing:** `eval/model-compare` can score a LoRA-served model via the proxy
  `/model/{name}/v1/chat/completions` path and emit a pass/fail usable as a gate.
- **Kill test (≤4h):** run the harness against the slice-4 served adapter; confirm a
  deterministic verdict that distinguishes "learned the fact" from base.
- **Status:** not run.
**Acceptance:** a failing adapter is NOT promoted; a passing one is; verdict recorded.

---

## Slice 6 — GRPO reinforcement leg (later)

**Scope:** add GRPO (reward = eval harness) once SFT LoRA is productized end-to-end. Gated on
slices 2–5 + the NF4 verdict. Spec deferred until then.

---

## Sequence & dependencies

```
S2 (image) ──► S3 (finetune.py + NF4 kill-test) ──► S4 (CRD-driven) ──► S5 (eval gate) ──► S6 (GRPO)
```
S2 unblocks everything. S3 carries the arc-level residual risk (NF4-on-ROCm) — run its
kill-test before investing in S4+. S4/S5 are wiring of already-proven pieces.

## Cross-cutting notes

- **Build:** training images are heavy (~20GB base) → build off-CI on `7900xtx` via native
  `docker build` over ssh (the `docker --context build` buildx-hijack gotcha bites otherwise);
  publish manual. See verdict doc + memory `build-patterns`.
- **GPU contention:** training Jobs compete with serverless textgen lanes for the 2 gfx1100
  GPUs; raw Jobs bypass the shared-GPU scheduler. Consider a `gpu_priority`/preemption story
  for training vs serving (or schedule training when lanes are idle).
- **Eval backend caveat:** the loom agent-context embedding backend (`gte-qwen2` via
  `litellm.flexinfer.ai`) was timing out 2026-06-19 — unrelated to F1 but worth a glance if
  context-store writes fail during this work.
```
