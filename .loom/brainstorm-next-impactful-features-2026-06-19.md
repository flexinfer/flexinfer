# Brainstorm — The next most impactful features for flexinfer

**Date:** 2026-06-19
**Decision in one sentence:** Given a mature serving + quant control plane on an AMD-heavy fleet, where should the next feature effort go to maximize impact for home inference and the quant/training framework?

## Grounding (two research passes)

**Capability inventory (codebase):** flexinfer is a strong, multi-arch *serving + quantization* control plane — Model/GPUProfile/LoRAAdapter/ModelCatalog/FederatedModel CRDs, serverless proxy with scale-to-zero, shared-GPU priority scheduling, vLLM/llama.cpp/ollama/diffusers backends, GPTQ self-quant (incl. MoE attention-only), spec decoding, prefix caching, TP windowing, embeddings + reranking, voice stack (Whisper + Kokoro), a task-complexity router, and a deterministic `eval/model-compare` harness.

**Conspicuous gaps:** **no training/fine-tuning lane at all** (LoRA *serving* only); no vision-in (voice only); eval + routing are manual scripts, not automated serving properties; no experiment CRD; cost/power instrumented but unused; quant posture is GPTQ-only (gfx906 stranded with no good vLLM quant path).

**Emerging-trends pass (AMD-fit filtered):** The single most important fact — **the entire 2026 quant *speed* frontier (FP8, FP4/NVFP4/MXFP4-native) is hardware-locked out of the consumer Radeon fleet** (needs Blackwell or Instinct MI300/MI350). For gfx1100/gfx906 the real frontier is software/orchestration: AWQ + GGUF-imatrix quant, LMCache KV offload, XGrammar structured decoding, vLLM Semantic Router, OTel GenAI observability, home fine-tuning (Unsloth + GRPO, now feasible on gfx1100 via bitsandbytes ROCm *preview*), and new architectures reachable only via llama.cpp (diffusion, hybrid/SSM, VLM). gfx906 (Radeon VII) is excluded from the modern quantized-training stack; its only paths are llama.cpp GGUF + community-torch FP16 LoRA.

## Riskiest assumption + kill-test

**Load-bearing assumption:** The operator wants *custom* models (fine-tune / distill) often enough that a train→eval→serve flywheel pays for itself — **and** Unsloth QLoRA/GRPO actually runs on gfx1100 (bitsandbytes ROCm is preview-only; gfx906 is excluded entirely).

**Kill test (≤1 day):** On a 7900 XTX (gfx1100), run Unsloth QLoRA to fine-tune a small model (e.g. Qwen3-1.7B) on a tiny dataset; produce an adapter; load it into the existing vLLM LoRA serving path; confirm coherent generation reflecting the fine-tune. Unambiguous: the adapter either trains without the known bnb-ROCm NaN crash and serves coherently, or it doesn't.

**Failure mode if the assumption is wrong:** We build a training lane on a flaky preview bnb-ROCm stack that crashes on real jobs, or one used twice a year — sinking weeks into a flywheel that never spins.

**Status:** PASSED 2026-06-19 — see [f1-training-killtest-verdict-2026-06-19.md](f1-training-killtest-verdict-2026-06-19.md). Live on gfx1100 (7900 XTX): existing `finetune.py` (PEFT path) trained a Qwen3-1.7B LoRA adapter (loss 3.86→0.21, 7.8s) that demonstrably changed behavior (learned a fictional fact); bnb-ROCm `adamw_8bit` ran without NaN; vLLM dynamically loaded the adapter via `/v1/load_lora_adapter` and served it coherently. Residual risk: true NF4 4-bit QLoRA + Unsloth-on-ROCm still unproven (the transformers fallback silently skips 4-bit). Grounding pass also CORRECTED the premise below. The "no training lane" claim below is **wrong**: a fine-tuning lane is already scaffolded in code (`ModelCacheSpec.Finetune` CRD + `controllers/modelcache_finetune.go` + `build/scripts/finetune.py` using Unsloth→PEFT+TRL). BUT it was never exercised on hardware — no CI job builds/tests it, and **no Dockerfile installs `unsloth`/`peft`/`trl`** (`INCLUDE_BITSANDBYTES=false` default), so no runnable training image exists today. The kill-test is therefore reframed from "build a training lane" to **"prove the existing scaffold runs on gfx1100, or expose it as dead generated code"** — cheaper, and unchanged in spirit. Execution staged: minimal training image off existing torch+ROCm base → LoRA (bf16, robust) first → then QLoRA (4-bit bnb = the actual flaky-ROCm risk) → adapter → vLLM `/v1/load_lora_adapter` → `eval/model-compare` gate.

---

## Phase 1 — Diverge

### F1 · Close the serve→train→serve loop (home fine-tuning lane)
Add an Unsloth-on-gfx1100 training pod (QLoRA + GRPO) that mints LoRA adapters the *existing* LoRA-serving already consumes. Synthetic data minted by your own gemma4/qwen35 lanes; `eval/model-compare` as reward/validation signal.
- **Bet:** the platform already owns every piece of a self-improvement flywheel *except* the training step — adding one node completes something no cloud-API user can replicate.
- **Risk:** training is a different operational class (long jobs, checkpoints, data mgmt); gfx906 excluded from the modern quantized-training stack; maintenance sink if fine-tuning is rare.

### F2 · Make quality a measured, automated property (eval-driven serving)
Promote `eval/model-compare` + the task-router from manual scripts to an always-on CI regression gate + online traffic sampler + auto-routing. Models can't silently regress.
- **Bet:** the hard part is already built (deterministic eval harness + a router that *generalized* on held-out tests); the gap is pure wiring — hardware-agnostic, low-risk.
- **Risk:** value scales with model churn; stable lineup → automation rarely fires. Online eval adds proxy surface.

### F3 · Lean into the orchestration layer where AMD doesn't gate you
Productize the control plane: vLLM Semantic Router (folds in fast/reasoning routing + PII/jailbreak/tool-selection), LMCache CPU-KV offload, XGrammar-2 structured decoding, OTel GenAI tracing correlated to Prometheus GPU metrics, prefix-aware multi-replica routing.
- **Bet:** highest-ROI wins are hardware-agnostic orchestration; flexinfer is already a control plane — this deepens the core competency.
- **Risk:** incrementalism — a bag of medium features, none individually transformative. Polish, not capability.

### F4 · Unlock model classes the fleet can actually run (new architectures)
Kill-test-gated lanes for what's absent: hybrid/SSM (Granite-4, Falcon-H1), diffusion LLMs (LLaDA/Dream via `llama-diffusion-cli`), via the llama.cpp ROCm path vLLM can't match on gfx1100.
- **Bet:** architectural breadth makes a personal platform feel like a frontier lab; llama.cpp gives an AMD path vLLM doesn't.
- **Risk:** each arch is a ROCm serving-maturity swamp (vLLM hybrid kernels broken on gfx1100); breadth without depth → models that "run" but nobody uses.

### F5 · Productize the experiment platform (the prior A+B bet)
ModelExperiment CRD + controller: declare a trial (quant variant, arch, router config), run build→serve→gauntlet→promote automatically. The F5-window pattern, productized.
- **Bet:** the operator already does this by hand on every quant cycle and lane bringup; a CRD captures repeatability + replay, amortizing the 11-cycle monkey-patch pain.
- **Risk:** meta-tooling for an N-of-1 operator; the abstraction may not survive the next novel bringup, which always needs hand-patching.

### F6 · Multimodal-in as the headline capability
Voice in/out exists (Whisper + Kokoro); add vision-in (VLM lane via llama.cpp + mmproj) → a true omni-modal private assistant backend (screenshot/document/photo Q&A) wired into agent-loop.
- **Bet:** the differentiated value of a *home* platform is private vision over your own photos/docs; the voice stack proves the pattern lands.
- **Risk:** VLM serving on vLLM-ROCm is weakly tested; image pipeline is real proxy work; thin demand for a solo operator.

### F7 · Cost / power / utilization intelligence
Turn existing instrumentation (`flexinfer_gpu_compute_utilization_percent`, VRAM) into action: power-aware scheduling, cost-per-token attribution, predictive/scheduled autoscaling, brown-out handling.
- **Bet:** a 24/7 multi-GPU homelab has real power/heat/noise/$ costs; pure ops value from data already collected.
- **Risk:** optimizing a constraint that may not bind for this operator; complex control loops for marginal kWh.

### F8 · Quant modernization — AWQ + GGUF-imatrix
Diversify beyond GPTQ: add an AWQ vLLM-ROCm lane (most reliable RDNA3 path) and imatrix GGUF self-quant (the *only* quality-preserving sub-4-bit route on gfx906, which has no good vLLM quant path).
- **Bet:** GPTQ-on-ROCm is hit-and-miss and gfx906 is stranded; AWQ + imatrix are proven AMD-fit and low-effort.
- **Risk:** improvement to an existing strength, not new capability — arguably tech-debt, not a headline feature.

---

## Phase 2 — Cross-Pollinate

### Combination A — F1 × F2 = a self-improving model factory
Training lane + eval-driven serving is more than the sum: serve mints synthetic data → Unsloth/GRPO fine-tunes → the eval harness is *both* the GRPO reward signal *and* the promotion gate → router auto-routes the winner. The one thing a cloud-API user fundamentally **cannot** do: private data → private fine-tune → private serve, with a measured quality ratchet. Each loop iteration improves the next — it *compounds*, where a model-zoo never does.

### Combination B — F5 as the chassis for F1 and F4
ModelExperiment CRD becomes the *unit of work* carrying both fine-tune trials (F1) and new-arch kill-tests (F4) through build→serve→gauntlet→promote. This dissolves F5's "meta-tooling for N-of-1" risk: the chassis is only worth building because it has real recurring payloads.

### Tension — F3 (squeeze what works) vs F4/F6 (widen what's possible)
Depth vs breadth. Normally squeezing throughput is high-ROI — but the AMD fleet has a *lower-than-usual* ceiling there (no FP4/FP8 silicon), while breadth (new arch) is a ROCm maturity swamp. Resolution: **F1×F2 is neither** — net-new capability built almost entirely from hardware-agnostic or already-proven pieces, dodging *both* the AMD quant-speed ceiling and the new-arch swamp.

---

## Phase 3 — Converge

### Recommended: F1 × F2 — the self-improving model factory, staged on the F5 chassis
The only direction that simultaneously (a) closes flexinfer's single biggest *capability* gap — no training at all today; (b) is built almost entirely from hardware-agnostic or already-proven components, sidestepping the AMD FP4/FP8 ceiling that neuters the quant-speed frontier; (c) **compounds** — the eval harness is already the reward signal and gate, serving lanes already mint the data, LoRA serving already consumes the output; (d) delivers what cloud APIs structurally can't: private-data fine-tunes served privately. Start with the *narrowest* slice — one Unsloth QLoRA pod on gfx1100 → adapter → existing LoRA serving → `eval/model-compare` gate — which is also the kill-test above. Build the F5 ModelExperiment chassis only after the loop proves out, then make fine-tune trials its first payload.

### Runner-up: F3 — orchestration squeeze
What tips it: if the real pain is "models are fine, I want them faster / cheaper / more observable and I rarely fine-tune," the flywheel is over-engineered and the F3 bag (Semantic Router + LMCache + XGrammar + OTel) delivers steadier daily value at lower risk. Also the fallback if the kill-test shows bnb-ROCm training is too flaky to depend on.

### Open question (answer before committing)
**How often do you actually want a *custom* model vs. the best available open weights, well-served?** The flywheel's value rests on this. Signal cuts both ways: 11 GPTQ cycles + multiple abliteration lanes say "constantly" (→ F1×F2 wins) — but those were *quant/abliterate* ops, not *fine-tunes*. If you've never wanted to teach a model new behavior from your own data, F3 wins.

---

## Handoff
- Chosen direction → `plan-loom-core` for a sliced spec (slice 1 = the kill-test above).
- Need the kill-test run first → `rapid-dev-iteration-loop` on a gfx1100 Unsloth QLoRA spike.
- If F3 instead → each item (Semantic Router, LMCache, XGrammar, OTel) is an independent `feature-dev` slice.
