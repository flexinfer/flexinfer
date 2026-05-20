# RALPH: gfx906 HIP embedding-op feasibility probe

Date: 2026-05-20
Branch: `feat/gfx906-hip-embedding-probe`
Parent evidence: `.loom/ralph-gfx906-vllm-tensor-fill-evidence-2026-05-20.md`

## Goal

Determine whether `torch.embedding` / `Tensor.index_select` / `F.embedding`
crashes on Vega20 (gfx906) **independent of vLLM**, by running a
self-contained PyTorch script as a one-off K8s Job inside the same vLLM
image the production runtime is pinned to
(`sha256:2139c92b3ca00716216f9e5644e9fbd29b2bba7237dc0459017c86012ece51c3`).

The result chooses the next slice:

- **standalone crashes** → broken HIP kernel on Vega20, not a vLLM-layer
  bug. Stop monkey-patching, escalate to strategic pivot (declare OPT-125M
  vLLM on gfx906 a feasibility-only canary and move the canary artifact to
  llama.cpp on gfx906, which the GTX 980 Ti work tree already validates as
  a workable substrate).
- **standalone passes** → vLLM-specific interaction (probably layer
  initialization order, dtype, or some surrounding torch state). Next
  slice is a debug-only `F.embedding` / `Tensor.index_select` wrapper for
  instrumentation.

## Scope

In:
- `deploy/debug/gfx906-hip-embedding-probe.yaml` — ConfigMap + Job
  containing a 6-scenario Python probe driven by a shell loop. Each
  scenario runs in its own subprocess so a HIP segfault on one does not
  terminate the rest. Scenarios:
  1. CPU `torch.embedding` `[50272, 768]` — sanity harness check.
  2. HIP `torch.embedding` `[4, 8]` — minimal HIP path.
  3. HIP `torch.embedding` `[50272, 768]` float32, ids `[1, 1]` — OPT-125M
     vocab-matrix shape, smallest input.
  4. HIP `torch.embedding` `[50272, 768]` float16, ids `[256, 1024]` —
     approximates vLLM's `_dummy_run` profile-forward-pass shape.
  5. HIP `F.embedding` — the actual call at
     `vocab_parallel_embedding.py:47`.
  6. HIP `Tensor.index_select` — the underlying op the others lower to.

Out:
- Any production CR changes. Probe is a one-off Job, not a runtime patch.
- New hook scripts in `build/scripts/install_vllm_gfx906_compat.py`. This
  slice is information-gathering only.
- Validation-matrix updates for the runtime smoke. The smoke status from
  the prior loop stands until the probe outcome dictates the next slice.

## Riskiest assumption + kill-test

**Load-bearing assumption**: `torch.embedding(weight, ids)` invoked
standalone in the same `rocm-gfx906@sha256:2139c92b…` image, on a Vega20
device, with OPT-125M-sized inputs (`[50272, 768]` weight, modest-batch
`[256, 1024]` int64 ids), reproduces (or rules out) the same HIP segfault
that vLLM hit at `vocab_parallel_embedding.py:47` during `_dummy_run`.

**Kill test**:
1. `kubectl apply -f deploy/debug/gfx906-hip-embedding-probe.yaml`.
2. `kubectl wait --for=condition=complete --timeout=300s
   job/gfx906-hip-embedding-probe -n flexinfer-system || true` and then
   inspect `kubectl logs job/gfx906-hip-embedding-probe -n flexinfer-system`.
3. Parse the `DRIVER: SCENARIO N exit=K` lines. Observable outcomes:
   - **Scenario 1 must pass** (CPU baseline). If it fails, the harness
     itself is wrong — invalidate the probe and rewrite.
   - **Scenarios 2-6 results determine the verdict**:
     - **Any HIP scenario exits non-zero with the same `F.embedding` /
       `torch.embedding` C-side faulthandler stack the production pod
       emitted (`torch/nn/functional.py:2567 in embedding` or
       lower-level `index_select`)** → standalone reproduces the
       segfault → ROCm runtime bug on Vega20. PASS for "the broken op
       family is below the Python layer," FAIL for "monkey-patching is
       a viable path forward."
     - **All HIP scenarios exit 0** → standalone does NOT reproduce →
       crash is vLLM-specific. PASS for "monkey-patching the embedding
       lookup might still work," and the next slice is a debug-only
       `F.embedding` wrapper to learn the next-deeper crash site.

**Failure mode if the assumption is wrong**: if the probe is too narrow
(e.g., uses different dtype / shape / device context than the actual vLLM
call site), the result is inconclusive. Mitigation: scenario 4 uses
exactly OPT-125M's vocab matrix shape (`[50272, 768]`) and the float16
dtype vLLM defaults to, on a freshly created cuda tensor pair. Scenarios
5 and 6 also exercise `F.embedding` and `index_select` directly. The set
covers the call stack from the high-level `F.embedding` down to the
underlying `index_select` op so a partial breakage (e.g. only certain
shapes / strides) is still visible.

**Status**: not run.

## Acceptance criteria

1. Probe Job runs to completion (job phase `Succeeded` or `Failed` — not
   `Pending` indefinitely).
2. Scenario 1 (CPU baseline) reports `PASS`.
3. Each HIP scenario emits a clear `DRIVER: SCENARIO N exit=K` line.
4. The verdict (ROCm-layer vs vLLM-layer) is captured in the evidence
   doc with the decisive log lines copy-pasted from the pod.
5. Recommendation for next slice (continue wrapper ladder vs strategic
   pivot) is documented with reasoning.

## Why this slice is shippable without runtime risk

- No changes to the runtime image, no changes to the runtime CR, no
  changes to GPUProfile. The Job is a one-off resource that
  auto-cleans after 2 hours via `ttlSecondsAfterFinished`.
- Resource footprint is small: 2-4 CPU, 8-16 GiB RAM, 1 GPU. The
  Vega20 already runs only the SDXL inpainting lane and the
  feasibility canary at `minReplicas: 0`; the probe contends with at
  most a transient canary cycle.
- Image pin is the exact digest the production runtime is on, so the
  result reflects production behavior, not a parallel build.

## Out-of-scope (will not happen in this slice)

- Wrapping `F.embedding` / `index_select` in
  `flexinfer_vllm_torch_tensor_compat.py`. That is a follow-up slice and
  only justified if scenario 4-6 pass.
- Building a new vLLM image. The probe runs on the existing pinned
  digest.
- Bisecting the ROCm / PyTorch tree for the underlying bug. If the
  probe confirms a kernel-level breakage, the strategic-pivot
  recommendation moves the canary off vLLM rather than committing to a
  source bisect inside RALPH.

## References

- Parent evidence with the production segfault stack:
  `.loom/ralph-gfx906-vllm-tensor-fill-evidence-2026-05-20.md`
- Existing `deploy/debug/qwen35-gfx906-abliteration-gpu-load.yaml`:
  pattern for ConfigMap + Job on `cblevins-radeonvii`.
- Memory: `gfx906-vllm-fill-segfault.md` for the sharpened root-cause
  understanding driving this probe.
- vLLM `vocab_parallel_embedding.py:47` =
  `return F.embedding(input_, layer.weight)`.
