# Brainstorm: Qwen3.5-9B RP long-context and performance optimization

**Date**: 2026-07-21
**Triggered by**: Apply the successful Fable runtime/scheduler lessons to the
repaired Qwen3.5-9B GPTQ + rank-64 RP LoRA lane.
**Constraints noted**: one 24 GiB gfx1100; preserve the verified 131K window;
do not edit the production Model during exploratory bring-up.

## Baseline

The current upstream 9B profile already serves 131,072 tokens with FP16 KV and
four scheduler slots. Its one-needle 131K check passed; a 245,760-token probe
remained coherent but missed recall. Performance is constrained by the vLLM
0.17 eager runtime at roughly 4.8 output tok/s. Enabling graphs on that image
crash-looped, while the pinned vLLM 0.23 image has since qualified dense
Qwen3.5, native gfx1100 W4A16, and bounded graphs on the larger Fable artifact.

## Phase 1 — Framings

### F1 — Transplant the qualified vLLM 0.23 path

Keep the exact 9B artifact, adapter, FP16 KV, and 131K context; change the
runtime to the digest-pinned native W4A16 image with graph shapes `[1,2,4]`.

- **Bet**: the obsolete runtime, not the model, causes most of the 4.8 tok/s ceiling.
- **Risk**: LoRA-specialized graphs consume the memory needed for 131K.

### F2 — Separate fast and maximum-context profiles

If graph memory prevents 131K, retain a demand-only 131K route and use a
64K-96K graph profile as the warm RP primary.

- **Bet**: ordinary RP fits well below 131K and benefits from more headroom.
- **Risk**: cold shared-group swaps cost more than the saved token latency.

### F3 — Bisect the recall ceiling

After the runtime is stable at 131K, test five-depth recall at 160K, 192K, and
224K rather than advertising the official 262K metadata without evidence.

- **Bet**: useful capacity exists between the proven pass and fail points.
- **Risk**: sparse recall passes overstate practical long-context quality.

### F4 — Test hybrid prefix caching

Measure repeated-turn TTFT on an append-only RP transcript using vLLM's Mamba
align-mode prefix cache and explicit cache-hit metrics.

- **Bet**: repeated conversation prefixes avoid most subsequent prefill.
- **Risk**: upstream still calls hybrid prefix caching experimental.

### F5 — Restore native MTP-1

Use the existing bit-exact 15-tensor 9B MTP graft only after the target path is
stable, then measure acceptance under the actual RP sampling profile.

- **Bet**: one-token speculation improves light-load interactive latency.
- **Risk**: the clean-base draft mismatches the abliterated + LoRA target.

### F6 — Requantize for quality only

Compare W4/G32 against the current W4/G128 artifact with blind RP and recall
gates while retaining the native W4A16-compatible quantization contract.

- **Bet**: smaller groups recover perceptible quality.
- **Risk**: metadata/kernel cost spends speed and KV for no visible gain.

### F7 — Fix lifecycle variance

Persist compile caches and warm base/LoRA shapes 1/2/4 plus a long GDN prefill
shape so first-user JIT does not hide steady-state performance.

- **Bet**: deterministic warmup materially improves p95 latency.
- **Risk**: it cannot fix a fundamentally slow kernel path.

## Phase 2 — Cross-pollinations and tensions

- **F1 + F7**: qualify the real production shape—native kernel, bounded graph,
  persistent cache, and active LoRA—rather than a base-only microbenchmark.
- **F1 + F2**: try 131K first; split profiles only if graph memory is the sole
  failure so the current context contract is never silently weakened.
- **F1 + F3**: certify performance at the known-good context before expanding
  the recall envelope.
- **Graph speed vs. context capacity** is the load-bearing tension: LoRA graph
  specialization reserves VRAM that otherwise backs FP16 KV.
- **FP8 capacity vs. long quality** is already evidence-backed: the Fable arm
  regressed multi-turn and long-context workloads, so FP8 is excluded here.

## Phase 3 — Convergence

### Recommended: F1 + F7

Run a disposable `ModelExperiment` with the exact vLLM 0.23 native-W4A16
digest, `gpuMemoryUtilization=0.94`, FP16 KV, 131K, chunked prefill, four
scheduler slots, graph shapes `[1,2,4]`, and explicit rank-64 LoRA support.
Require coherent base and adapter output, at least 15 tok/s median adapter
decode, two-stream aggregate evidence, five-depth recall near 128K, no runtime
faults, and automatic restoration of the current parent Model.

### Runner-up: F1 + F2

If and only if 131K graph mode fails on memory, qualify a smaller warm graph
profile while retaining the current 131K eager endpoint under an explicit
long-context alias.

### Open question

If one warm 131K graph endpoint cannot fit, is a fast-RP/long-context route
split acceptable in exchange for materially better p95 latency?

## Riskiest assumption + kill-test

**Load-bearing assumption**: image
`sha256:2e9652edee30ed078843935ce5672280efd3585de0527d27703dd6880592981d`
can serve the repaired dense 9B W4/G128 target on gfx1100 with graph shapes
1/2/4, active rank-64 `nsfw-rp`, FP16 KV, and a 131,072-token maximum window.

**Kill test**: boot an owned candidate from a fresh compile-cache namespace;
assert the image ID, vLLM 0.23, native W4A16 kernel, and graph sizes; test base
numerics; hot-load and test the adapter; require median adapter decode >=15
tok/s; run two ordinary concurrent streams; recall five needles near 128K;
scan for restart, traceback, OOM, ROCm/HSA, NaN, or abort; then verify the
experiment deletes the candidate and restores `qwen35-9b-ablit-rp`.

**Failure mode if wrong**: a direct runtime swap would crash-loop, shrink the
verified context, or regress the real adapter despite a coherent base. Keep the
current eager parent and test graph shape one or a split profile instead.

**Status**: not run

## Handoff

- If chosen → `rapid-dev-iteration-loop` with
  `deploy/experiments/qwen35-9b-v023-rp-canary.yaml`.
- Evidence log → `.loom/iteration-qwen35-9b-v023-rp-optimization-2026-07-21.md`.
