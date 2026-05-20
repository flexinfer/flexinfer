# RALPH: gfx906 vLLM torch.nn.init CPU fallback

Date: 2026-05-20
Branch: `fix/gfx906-vllm-opt-init-cpu-fallback`
Prior slice: `codex/gfx906-vllm-diagnostic-digest` (merged `9ece0441`) — pinned the
diagnostic digest that captured the OPT-125M child-worker segfault.

## Review

- Roadmap milestone: ROCm `gfx906` platform enhancements; the textgen vLLM canary
  lane remains `experimental` until a real HTTP 200 OPT response.
- Spec/evidence inputs:
  - `.loom/ralph-gfx906-vllm-diagnostic-digest-2026-05-19.md`
  - `.loom/ralph-gfx906-vllm-smoke-2026-05-17.md` (sections through OPT pivot)
  - `.loom/60-validation-matrix.md` row 175 (`qwen3-1p7b-vllm-radeonvii`)
- Prior decisions preserved:
  - Keep `gfx906` vLLM `experimental` until the OPT canary returns HTTP 200.
  - Do not chase more model-family or profile-flag variants until the exact
    crash named by the diagnostic image is patched.

## Align

Slice name: gfx906 vLLM `torch.nn.init` CPU fallback for HIP tensors.

Scope in:
- Extend `build/scripts/install_vllm_gfx906_compat.py` with a new site-packages
  hook (`flexinfer_vllm_torch_init_compat.py`) that wraps
  `torch.nn.init._no_grad_normal_`, `_no_grad_uniform_`, and
  `_no_grad_trunc_normal_` to allocate the random init on CPU and copy back to
  the HIP target tensor.
- Lock the new hook into the runtime patch contract check so future hook
  refactors cannot silently drop the workaround.

Scope out:
- Promoting `gfx906` vLLM from `experimental`.
- Updating any GPUProfile / Helm digest pin (that happens after the rebuild +
  live smoke, as a separate one-line MR).
- Patching any other init function families beyond the documented crash
  neighborhood. New variants surface in the diagnostic image if they hit.
- Changing canary model family, attention backend, or scheduler flags.

## Riskiest assumption + kill-test

**Load-bearing assumption**: The fatal Python segfault during OPT-125M
`Embedding` init on gfx906 originates inside the HIP random-number kernels
invoked by `torch.Tensor.normal_` (called via `torch.nn.init._no_grad_normal_`).
Routing the in-place fill through a CPU tensor and copying back avoids the
faulting HIP code path; pretrained weight load then overwrites the random
content so behavior remains correct.

**Kill test**: After publishing the rebuilt diagnostic image and pinning its
digest in `deploy/gpuprofiles/gfx906.yaml` + `deploy/system/values-k3s.yaml`,
rerun the documented OPT-125M canary curl. Pass condition is **HTTP 200** with a
non-empty completion. Anything else (new traceback, hung readyz, empty reply)
falsifies the assumption.

**Failure mode if wrong**: the segfault recurs on the same `init.normal_` stack
(meaning the patch did not take effect — likely a `.pth` ordering or import
issue) or moves to a different HIP kernel surfaced by a later vLLM init step
(meaning the random-init path was correct but another HIP RNG call sits
downstream). Either case is visible in the same faulthandler diagnostics that
caught the first crash; recovery is a follow-up slice scoped at the new stack.

**Status**: not run — depends on cluster rebuild + canary execution after MR
merges.

## Land

Files changed in this MR:
- `build/scripts/install_vllm_gfx906_compat.py` — new
  `flexinfer_vllm_torch_init_compat.py` hook entry + automatic registration via
  the existing `flexinfer_vllm_gfx906_compat.pth` write.
- `scripts/check-runtime-patch-contracts.py` — new
  `assert_gfx906_vllm_compat_hooks_contract` plus reading
  `build/scripts/install_vllm_gfx906_compat.py` from `main()`.

Files intentionally not changed in this MR:
- `build/Dockerfile.vllm-rocm-gfx906` — the existing `COPY` +
  `python3 /tmp/install_vllm_gfx906_compat.py` step already runs the new hook
  on every rebuild.
- `deploy/gpuprofiles/gfx906.yaml`, `deploy/system/values-k3s.yaml` — digest
  pin happens after the post-merge `publish_vllm_rocm_gfx906` job emits a new
  immutable tag and the live canary returns HTTP 200.
- `.loom/60-validation-matrix.md` — updated after the live smoke produces
  evidence (matches the same discipline used by the prior diagnostic slice).

## Prove

Local checks (all passed against this branch):
- `python3 -m py_compile build/scripts/install_vllm_gfx906_compat.py
   scripts/check-runtime-patch-contracts.py`
- `python3 build/scripts/install_vllm_gfx906_compat.py
   --target /tmp/flexinfer-gfx906-vllm-hooks` (writes 5 hooks + `.pth`)
- `PYTHONPATH=/tmp/flexinfer-gfx906-vllm-hooks python3 -c
   'import flexinfer_vllm_torch_init_compat; ...'`
- `python3 scripts/check-runtime-patch-contracts.py --run-script-tests`
  (contract assertions + 28 patch tests pass).

CI checks:
- Normal MR pipeline runs `runtime_patch_contracts` in the lint stage; the new
  assertion fires only if the install script regresses.
- After merge, manually trigger `publish_vllm_rocm_gfx906`. The published
  immutable digest must then be pinned in
  `deploy/gpuprofiles/gfx906.yaml` + `deploy/system/values-k3s.yaml` in a
  follow-up MR before the live OPT smoke.

## Handoff

Next slice candidates:
1. **Publish + pin + live smoke (highest priority).** Trigger the manual
   gfx906 vLLM publish job, pin the resulting digest, rerun the OPT canary,
   and update `.loom/60-validation-matrix.md` row 175 with the live result.
   On HTTP 200 promote `vllm.support: experimental → supported` in
   `deploy/gpuprofiles/gfx906.yaml`. On new traceback, scope a follow-up at
   the named call site (per the kill-test failure modes above).
2. **If the live smoke surfaces a different HIP RNG site** (e.g.
   `torch.empty(..., device='cuda').uniform_()` called outside `nn.init`),
   extend the same hook with a tensor-level wrapper rather than patching
   another `init.*` family.
3. The Whisper kill-test v3 live observation (queued in
   `.loom/ralph-whisper-kill-test-v3-handoff-2026-05-19.md`) is independent of
   this slice and can run in parallel on the same Radeon VII node once the
   gfx906 vLLM digest is pinned.
