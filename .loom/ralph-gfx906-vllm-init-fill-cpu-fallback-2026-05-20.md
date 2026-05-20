# RALPH: gfx906 vLLM `torch.nn.init` CPU fallback — fill/zero extension

Date: 2026-05-20
Branch: `fix/gfx906-vllm-init-fill-cpu-fallback`
Prior slice: `docs/gfx906-vllm-cpu-init-fallback-evidence` (merged `5dfc7644`) —
recorded that OPT-125M load now segfaults at vLLM `opt.py:245`
(`final_layer_norm = nn.LayerNorm(...)`) instead of `opt.py:218`
(`OPTLearnedPositionalEmbedding`) after the `_no_grad_normal_/_uniform_/
_trunc_normal_` CPU fallback landed in image `sha256:60b1ab0b…`.

## Review

- Roadmap milestone: ROCm `gfx906` platform enhancements; the textgen vLLM
  canary lane remains `experimental` until a real HTTP 200 OPT response.
- Decisive evidence (via Loki on pod
  `qwen3-1p7b-vllm-radeonvii-bf87b74d6-n6nst`, restart 0,
  2026-05-20T14:39:40Z):

  ```text
  Current thread 0x00007f3864b1e000 (most recent call first):
    File ".../torch/utils/_device.py", line 109 in __torch_function__
    File ".../torch/nn/init.py", line 132 in _no_grad_fill_
    File ".../torch/nn/init.py", line 327 in ones_
    File ".../torch/nn/modules/normalization.py", line 224 in reset_parameters
    File ".../torch/nn/modules/normalization.py", line 220 in __init__
    File ".../vllm/model_executor/models/opt.py", line 245 in __init__
    File ".../vllm/model_executor/models/opt.py", line 305 in __init__
    File ".../vllm/compilation/decorators.py", line 151 in __init__
    File ".../vllm/model_executor/models/opt.py", line 346 in __init__
    …
  ```

  No `flexinfer_vllm_torch_init_compat.py` frame, so the existing
  `_no_grad_normal_/_uniform_/_trunc_normal_` patches are not on this path.

- Prior slice's predicted **failure mode 3** ("new HIP RNG site downstream")
  was wrong in spirit — the new crash is NOT an RNG kernel; it is the
  constant-init `Tensor.fill_` HIP kernel reached from
  `LayerNorm.reset_parameters` → `init.ones_` → `_no_grad_fill_(tensor, 1.)`.
  The same CPU-mirror framework still applies; only the patched function
  family changes.

## Align

Slice name: gfx906 vLLM `torch.nn.init` CPU fallback for `_no_grad_fill_` +
`_no_grad_zero_`.

Scope in:
- Extend `_install()` in `flexinfer_vllm_torch_init_compat.py` (carried inside
  `build/scripts/install_vllm_gfx906_compat.py`) with two more
  `_patch_in_place(...)` calls for `_no_grad_fill_` and `_no_grad_zero_`.
- Update the inline comment to document both code paths (random init AND
  constant init) and link them to specific `opt.py` lines in vLLM v0.7.3.
- Update `scripts/check-runtime-patch-contracts.py`'s
  `init_contract` tuple to require the two new function names.

Scope out:
- Promoting `gfx906` vLLM from `experimental`.
- Touching the GPUProfile / Helm digest pin (that happens after the
  rebuild + live smoke, as a separate one-line MR per workspace policy).
- Patching anything outside `torch.nn.init`. The pre-step Loki traceback
  confirms the wrapper site lives in `torch.nn.init`, not at the tensor
  method level.
- Changing canary model family or vLLM scheduler flags.

## Riskiest assumption + kill-test

**Load-bearing assumption**: The fatal Python segfault during OPT-125M
`nn.LayerNorm.__init__` on gfx906 originates inside the HIP `Tensor.fill_`
kernel invoked by `torch.nn.init._no_grad_fill_(tensor, val)` and the
companion `_no_grad_zero_(tensor)`. Routing the in-place fill/zero
through a CPU mirror tensor and copying back to the HIP tensor avoids the
faulting HIP code path; pretrained weight load then overwrites the
intermediate `[ones]/[zeros]` content so behavior remains correct.

**Kill test**: After publishing the rebuilt gfx906 vLLM image and pinning
its digest in `deploy/gpuprofiles/gfx906.yaml` +
`deploy/system/values-k3s.yaml`, rerun the documented OPT-125M canary
curl (`.loom/60-validation-matrix.md` row 175). Pass condition is
**HTTP 200** with a non-empty completion. Anything else
(new traceback, hung readyz, empty reply) falsifies the assumption.

**Failure modes if wrong**:
1. The segfault recurs at the same `_no_grad_fill_` Python frame
   (meaning the patch did not take effect — likely a `.pth` ordering or
   import issue, identical to the failure mode of the original RNG
   slice's first build).
2. The segfault moves to `_no_grad_zero_` only (meaning patching `fill_`
   alone would have been enough; benign over-coverage, not a regression).
3. The segfault moves to a new in-place op outside the
   `torch.nn.init._no_grad_*` family (e.g. directly into
   `Tensor.normal_()` from a vLLM custom layer, or into a `copy_` from
   a sharded weight load). That escalates to a tensor-method wrapper, as
   anticipated by handoff candidate (2.a) in
   `.loom/ralph-gfx906-vllm-torch-init-cpu-fallback-2026-05-20.md`.

**Status**: not run — depends on cluster rebuild + canary execution after
this MR merges + `publish_vllm_rocm_gfx906` emits a new digest + pin MR
lands.

## Land

Files changed in this MR:
- `build/scripts/install_vllm_gfx906_compat.py` — two new
  `_patch_in_place(...)` calls and a refreshed comment that documents both
  the random-init and constant-init failure paths with `opt.py` line
  references.
- `scripts/check-runtime-patch-contracts.py` — two new entries in the
  `init_contract` tuple so future hook refactors cannot silently drop the
  workaround for the fill family.

Files intentionally not changed in this MR:
- `build/Dockerfile.vllm-rocm-gfx906` — the existing `COPY` +
  `python3 /tmp/install_vllm_gfx906_compat.py` step already runs the
  amended hook on every rebuild.
- `deploy/gpuprofiles/gfx906.yaml`, `deploy/system/values-k3s.yaml` —
  digest pin happens after the post-merge `publish_vllm_rocm_gfx906` job
  emits a new immutable tag and the live canary returns HTTP 200.
- `.loom/60-validation-matrix.md` — updated after the live smoke produces
  evidence (same discipline as prior slices).

## Prove

Local checks (all passed against this branch):
- `python3 -m py_compile build/scripts/install_vllm_gfx906_compat.py
   scripts/check-runtime-patch-contracts.py`
- `python3 build/scripts/install_vllm_gfx906_compat.py
   --target /tmp/flexinfer-gfx906-vllm-hooks-fillfix` (writes 5 hooks +
  `.pth`, with the new `_no_grad_fill_/_no_grad_zero_` lines visible in
  the generated `flexinfer_vllm_torch_init_compat.py`).
- `PYTHONPATH=/tmp/flexinfer-gfx906-vllm-hooks-fillfix python3 -c
   'import flexinfer_vllm_torch_init_compat'` succeeds.
- `python3 scripts/check-runtime-patch-contracts.py --run-script-tests`
  passes the contract block plus 28 patch tests.

CI checks:
- Normal MR pipeline runs `runtime_patch_contracts` in the lint stage; the
  new assertion fires only if the install script regresses.
- After merge, trigger `publish_vllm_rocm_gfx906`. The published immutable
  digest must then be pinned in `deploy/gpuprofiles/gfx906.yaml` +
  `deploy/system/values-k3s.yaml` in a follow-up MR before the live OPT
  smoke.

## Handoff

Next slice candidates:
1. **Publish + pin + live smoke (highest priority).** Trigger the manual
   gfx906 vLLM publish job, pin the resulting digest, rerun the OPT canary,
   and update `.loom/60-validation-matrix.md` row 175 with the live
   result. On HTTP 200 promote `vllm.support: experimental → supported` in
   `deploy/gpuprofiles/gfx906.yaml`. On new traceback at a non-init site,
   scope a follow-up slice per failure mode 3 above (tensor-method
   wrapper).
2. **mistral_common bump in gfx1100 vLLM image** — still queued from
   `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`. Independent
   of this gfx906 kill-test; can run in parallel.

## References

- Prior slice plan + evidence:
  `.loom/ralph-gfx906-vllm-torch-init-cpu-fallback-2026-05-20.md`,
  `.loom/ralph-gfx906-vllm-cpu-init-fallback-evidence-2026-05-20.md`.
- Diagnostic digest history:
  `.loom/ralph-gfx906-vllm-diagnostic-digest-2026-05-19.md`,
  `.loom/ralph-gfx906-vllm-worker-diagnostics-2026-05-19.md`.
- Validation row: `.loom/60-validation-matrix.md` row 175.
- Live pod with decisive trace:
  `qwen3-1p7b-vllm-radeonvii-bf87b74d6-n6nst` on `cblevins-radeonvii`
  (Radeon VII / gfx906), Loki window 2026-05-20T14:39:00Z … 14:48:57Z.
- Upstream torch reference (release/2.6 branch): `_no_grad_fill_` at
  `torch/nn/init.py:62`, `_no_grad_zero_` at `torch/nn/init.py:67`.
- Upstream vLLM v0.7.3 reference: `opt.py:245` =
  `self.final_layer_norm = nn.LayerNorm(...)`. Dockerfile pins
  `--branch v0.7.3` against `github.com/vllm-project/vllm.git`; only
  `csrc/*` is locally seded (WARP_SIZE), `opt.py` is untouched.
