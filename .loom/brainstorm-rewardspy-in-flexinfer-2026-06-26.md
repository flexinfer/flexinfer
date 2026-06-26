# Brainstorm: RewardSpy ideas/offshoots applied to FlexInfer

**Date:** 2026-06-26
**Topic:** How the ideas/concepts (or offshoots) of the `AvAdiii/rewardspy` repo might be useful in FlexInfer.
**Source repo:** https://github.com/AvAdiii/rewardspy

## What RewardSpy actually is

A plug-in debugger/visualizer for **RL reward functions**: detects reward hacking, tracks training health, renders a live terminal dashboard. Pure-Python, MIT.

- **Non-invasive wrapper:** `rewardspy.watch(reward_fn)(...)` — observes, never changes return values.
- **O(1) online detectors** (six hack signatures): reward variance collapse, component dominance, response-length drift, reward-slope breaks (CUSUM change-point), ceiling saturation, GRPO group collapse.
- **Three-layer design:** wrapper → metrics & detectors → dashboard / CLI / exporters.
- **Surfaces:** JSONL streaming; `show` (live dashboard), `summary`, `audit` (CI exit codes), `export` (CSV), `probe` (offline). Integrations: GRPO (`GRPOSpy`), TRL (`watch_trl`), W&B.
- **Core insight it operationalizes:** Goodhart / overoptimization — optimizing a proxy measure silently diverges from the true objective.

## FlexInfer grounding (verified this session)

- **No RL/reward training exists.** Finetune is pure SFT — `build/scripts/finetune.py:172` (`from trl import SFTTrainer`); LoRA/QLoRA/full only. No PPO/DPO/GRPO/reward-model code anywhere.
- **Scored signals exist everywhere but are one-shot gates, not trended:**
  - Quant cosine validation, threshold 0.98 — `build/scripts/quantize_gptq.py:954-1002`.
  - Abliteration safeguards — `build/scripts/abliterate_safety.py` (`select_full_attention_layers`, `refusal_norm_exceeds` @ threshold 100, `is_degenerate_generation`); perplexity-regression gate (25%) in `abliterate.py:2147-2332`.
  - Artifact validation incl. repeated-token-run detection — `build/scripts/validate_quantized_artifact.py`.
- **Eval harnesses are scattered, ad-hoc JSON, no common spine:** gauntlet/probe (`pkg/gauntlet/*.go`), benchmarker → Postgres (`agents/benchmarker/*`), spec-decode-bench (`cmd/spec-decode-bench/`), context-needle-bench (`scripts/context-needle-bench.py`), autotune coordinate-descent (`agents/autotune/autotune.go`).
- **Prometheus is 100% operational** (TPS, VRAM, TTFT, cache) — `pkg/metrics/exporter.go`; **no per-request quality/score metric, no anomaly/drift/change-point detection anywhere.**
- **FlexInfer already gets bitten by proxy-overoptimization (documented):** n-gram SD tuning helped short-prompt p95 but cut long-form −53% to −75% (`ngram-sd-workload-conditional`); APC structurally infeasible at 32k FP8-KV (`project_f4_apc_canary_pending`); maxNumSeqs knee=2. These are real Goodhart cases caught *manually, after the fact*.

---

## Riskiest assumption + kill-test

**Load-bearing assumption:** FlexInfer can produce a *cheap, trustworthy true-objective quality signal* (a coherence / held-out eval) that runs inline against a candidate config or a live model. The recommended path (Goodhart guard for autotune) inherits this bet — without a true objective, every detector just watches one proxy against another.

**Kill test (≤30 min, replayable on history):** Take the **n-gram SD workload-conditional** episode — a config that maximized the throughput proxy while degrading long-form generation. Run the existing `scripts/context-needle-bench.py` (or `pkg/gauntlet` probe with long-form `--gauntlet-expect`) against both the "tuned" and "baseline" configs and show that a held-out coherence/recall signal **measurably separates them** (tuned config scores materially worse on long-form) — i.e., a detector reading that signal *would have vetoed* the bad config. Observable outcome: a numeric gap on the true-objective metric that the throughput proxy hid.

**Failure mode if wrong:** If no cheap true-objective signal separates good from gamed configs, the "Goodhart guard" collapses to proxy-vs-proxy theater; we'd ship a detector that fires on noise or never fires, and the real lesson (these failures need a human-judged eval) stays unautomated. Effort reusable: the detector library (Combination B) still has value as trend-visibility even if the autotune loop can't be closed.

**Pair with disconfirming search:** before declaring passed, search both "does a coherence canary reliably separate throughput-tuned from baseline vLLM configs" AND "speculative-decoding / batching throughput gains that do NOT cost coherence" — to avoid concluding the guard works from one cherry-picked episode.

**Status:** not run

---

## Phase 1 — Diverge (7 framings)

### 1. Literal port — stand up a GRPO/RL finetuning lane so rewardspy has a home
Add a TRL GRPO trainer to the F1 lane; wrap reward fns with `rewardspy.watch`.
- **Bet:** the abliterated/daily-driver program eventually wants preference/RL training; rewardspy de-risks it.
- **Risk:** building a whole RL lane to justify a debugger — tail wagging the dog. Speculative demand, large effort.

### 2. Extract the detector engine — retarget at quant/abliteration quality drift
Feed existing per-layer/per-run quant & abliterate signals (cosine, perplexity, refusal-norm, degenerate-gen) through rewardspy-style O(1) detectors to catch drift *across layers* and *across runs over time*, not just one-shot pass/fail.
- **Bet:** the scored signals already exist and are under-exploited; detectors add early-warning the binary gates miss.
- **Risk:** per-layer cosine isn't a temporal series in the RL sense; CUSUM may be a hammer hunting a nail.

### 3. "Goodhart guard" for the autotune loop
Wrap `autotune.go`'s TPS-maximizing objective with overoptimization detectors (component dominance, ceiling saturation) + a held-out coherence canary, so the tuner refuses configs that gamed the proxy.
- **Bet:** highest-fidelity transfer — this is the exact disease rewardspy treats, and FlexInfer demonstrably suffers it (n-gram SD, APC, maxNumSeqs knee).
- **Risk:** needs a true-objective signal wired into the loop; without it, proxy-vs-proxy.

### 4. Unify scattered eval harnesses behind a rewardspy-style streaming spine
Common JSONL contract for gauntlet/probe/benchmarker/spec-decode/context-needle; `flexinfer eval show` (live dashboard), `flexinfer eval audit` (CI exit codes).
- **Bet:** harnesses already exist; pure integration/DX win with immediate operator value.
- **Risk:** standardization toil across many call sites; convenience, not new capability.

### 5. Serving-time quality-drift sentinel (production Goodhart)
Run non-invasive wrapper + length-drift/variance-collapse/repetition detectors on live proxy traffic (completion-length dist, finish_reason mix, repetition runs) to catch a silently-degrading served model.
- **Bet:** catches the "served 200 but garbage" class operational metrics structurally miss (96k coherence cliff; SD safety-checker black images).
- **Risk:** defining "quality" on uncontrolled prod traffic is hard; false positives; needs a reference distribution.

### 6. CI-gate quant/abliterate with `audit` exit-codes + W&B-style run tracking
Emit per-run quality time series (cosine, perplexity, norm) as a tracked artifact; gate publish on a detector-trip audit.
- **Bet:** upgrades existing one-shot guards into regression-tracked, historically-comparable gates.
- **Risk:** overlaps heavily with `validate_quantized_artifact.py`; risk of re-skinning.

### 7. Serve reward/verifier models as a product lane + bundle rewardspy as observability
FlexInfer serves reward/verifier models as a first-class backend; rewardspy ships as the bundled sidecar for customers running GRPO against them.
- **Bet:** aligns with the GTM/monetization directive — "RLHF infra + reward-hacking observability" as a differentiated lane.
- **Risk:** far from current product; demand unproven; large surface.

---

## Phase 2 — Cross-Pollinate

**Combination A — #3 × #5 = closed-loop quality-aware tuning.**
Autotune proposes a config → serving sentinel measures true-objective quality on shadow/canary traffic → detector vetoes regressions. The sentinel becomes autotune's *missing true-objective signal*: "tune for throughput, bounded by live coherence." Automates the n-gram-SD lesson (caught manually last time).

**Combination B — #2 × #4 × #6 = one detector library, three consumption modes.**
Build the O(1) detector library once; feed it from (a) quant/abliterate per-layer streams, (b) eval JSONL, (c) CI audit gate. Extract the engine, fan out the consumers — maximizes reuse of rewardspy's single genuinely-novel asset.

**Tension — #1 vs. everything else: purpose vs. mechanism.**
rewardspy's *purpose* (reward hacking) needs an RL lane FlexInfer lacks; its *mechanism* (online overoptimization detection) applies today. Don't conflate: porting the library is cheap; porting the premise is a roadmap bet on RL/preference training.

---

## Phase 3 — Converge

**Recommended: Combination A grounded by #3 — a "Goodhart guard" for autotune, fed by a minimal coherence canary.**
The one place rewardspy's core insight lands on a real, recurring, expensive FlexInfer problem rather than a hypothetical. FlexInfer's documented proxy-overoptimization failures are exactly Goodhart/reward-hacking transplanted from RL training to config tuning. Bounded scope (`autotune.go` + 2–3 detectors + an existing coherence canary), immediate guardrail value, mechanism-over-purpose (no RL-roadmap bet required).

**Runner-up: #2 + #4 — the detector library as a shared eval spine.**
Tips it if the felt pain is "we can't see eval trends across scattered harnesses" more than "the tuner games us." Broader reuse, better DX, but diffuse value and more call sites.

**Open question (answer before committing):**
Does FlexInfer have — or can it cheaply build — a trustworthy true-objective quality signal that runs inline against a candidate config or live model? This is the riskiest-assumption kill-test above. If that signal can't be made cheap, every framing degrades to proxy-watching-proxy, and the runner-up (trend visibility) becomes the safer first step.

---

## Handoff

- Direction chosen → `plan-loom-core` to slice the Goodhart guard (Combination A), with **slice 1 = the kill-test above** (prove a coherence canary separates the historical n-gram-SD configs before building the veto loop).
- Need facts to validate → `research` on cheap inline coherence/true-objective signals for vLLM serving.
- If runner-up chosen → start with the JSONL detector library (Combination B) as standalone, lowest-risk reuse.

**Related FlexInfer context:** `ngram-sd-workload-conditional`, `project_f4_apc_canary_pending`, `project_hardware_utilization_arc` (autotune/per-lane batching), abliteration safety (`#52`), `project_uncensored35b_64k_context` (RoPE coherence cliff = a serving-time quality-drift case for #5).
