# Kill-test verdict — Autotune Goodhart guard (Slice 1)

**Plan:** `plan-goodhart-guard-for-autotune-rewardspy-flexinfer-combination-1676c1`
**Date:** 2026-06-26
**Status:** ✅ **PASSED**
**Executed by:** claude-code (live A/B/A on cblevins-7900xtx)

## Riskiest assumption under test

> FlexInfer can produce a cheap, trustworthy true-objective signal that materially **separates** a throughput-gaming config from baseline, catching a regression the aggregate/short-prompt proxy rewards.

## Refinement discovered during execution

The `ngram-sd-workload-conditional` episode is a **throughput** regression, not a quality one. n-gram speculative decoding is mathematically lossless on greedy decode (temp 0) — output is byte-identical. So the "true objective" it harms is **long-form decode throughput**, which a short-prompt / aggregate TPS proxy hides. The kill-test therefore measures **workload-stratified decode tok/s**, not a fuzzy coherence score. (Coherence/recall canaries remain relevant for the APC-32k / RoPE-cliff sibling cases — different knob, different true objective.)

## Method

- **Model:** `gemma4-26b-a4b-gptq` (primary, non-shared during test), 7900xtx, vLLM, GPTQ w4g128, maxModelLen 32768, maxNumSeqs 1.
- **Clean isolation:** Flux `flexinfer-models` suspended; primary `.spec.config` patched to add **only** `speculativeConfig` (the exact canary tuning `{method:ngram, num_speculative_tokens:7, prompt_lookup_max:6, prompt_lookup_min:1}`), rolled, measured, then the key was removed and rolled back. The *only* variable between arms is n-gram SD (no APC confound — APC stayed off in both).
- **Instrument:** `scratchpad/longform_canary.py` — `/v1/chat/completions`, temp 0, 3 repeats/class, two workload classes:
  - `lookup` — reproduce a supplied inventory record verbatim (prompt-copy → high n-gram acceptance).
  - `novel` — original ~300-word reflective essay (no copyable prompt n-grams → drafts miss).
- **Design:** A/B/A (SD-off → SD-on → SD-off) to rule out drift/thermal/load confounds.

## Results (median decode tok/s, 3 repeats; tight spreads)

| workload | A1: SD-off | B: SD-on n-gram (7,6) | A2: SD-off restored | B vs baseline |
|---|---|---|---|---|
| **lookup** (prompt-copy) | 67.0 | 138.9 | 66.8 | **+107%** |
| **novel** (long-form) | 72.6 | 38.1 | 72.2 | **−47.6%** |
| **aggregate** (mean of classes) | 69.8 | 88.5 | 69.5 | **+26.7%** |

Quality identical across all arms: repetition-score 0.0, no degeneracy, full token counts. Per-run spreads were tight (novel SD-off 71.5–72.8; SD-on 37.8–38.4 — no overlap).

## Interpretation

1. **The proxy rewards the bad config.** An autotune loop maximizing a single aggregate/short-prompt TPS number sees **+26.7%** (or +107% if it benchmarks a lookup-style prompt) and **ACCEPTS** n-gram SD.
2. **The true objective regresses, hidden.** Long-form decode throughput **−47.6%** (72.6 → 38.1). Reproduces the historical "short Q/A 2×, long-form −53% to −75%."
3. **A cheap workload-stratified canary catches it.** The `novel` class alone separates the configs with no overlap — exactly the signal a Goodhart guard needs. This is the kill-test PASS.
4. **Causal, reversible (A/B/A).** Removing SD restored novel 38.1 → 72.2 (A1≈A2: 72.6 vs 72.2). The regression is the config, not drift.

## Disconfirming check (guard is not a blunt "veto everything")

- **No-op / revert control:** A2 (SD removed) shows the `novel` class does **not** regress → the guard would **not** veto a neutral/no-op change. A1≈A2 confirms re-measurement noise does not trip a regression detector.
- **Genuine gain, quality-neutral:** the same SD config delivered **+107%** on `lookup` with **zero** quality loss. The veto must therefore be **workload-class-specific** (protect the deployment's real serving mix), not a global "throughput changed → veto." A future universally-beneficial decode optimization (no class regresses) would pass the guard.
- Positive+negative both hold: n-gram SD genuinely helps copy-heavy workloads AND genuinely harms novel generation — the guard's job is to know which class is the protected true objective.

## Implication for the plan

- **Unblocks Slices 2–4.** The veto loop is worth building.
- **Sharpens the design:** autotune's widened objective should benchmark a **workload mix** (at minimum a long-form / novel class) and veto a candidate that improves the aggregate while regressing a protected class beyond tolerance. The pure-Go detector library (Slice 2) needs a **workload-stratified throughput regression** detector first; coherence/degeneracy detectors are secondary (for the APC/RoPE sibling cases).
- Fallback (Combination B) not triggered — the signal separates cleanly.

## Reproduce

`scratchpad/longform_canary.py --base-url http://localhost:18000 --model gemma4-26b-a4b-gptq --arm <label> --repeats 3 --json out.json` against a port-forward to the model svc. Raw per-run JSON: `baseline-sdoff.json`, `tuned-sdon.json`, `baseline-restored.json` (session scratchpad).

## Infra hygiene

Flux `flexinfer-models` resumed (`suspended=false, Ready=True`); primary restored (`speculativeConfig` empty, gpuMemUtil 0.98); apc-canary untouched; port-forwards killed. Chat primary back on baseline. No residual mutation.
