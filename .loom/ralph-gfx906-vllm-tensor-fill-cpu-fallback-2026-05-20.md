# RALPH: gfx906 vLLM `torch.Tensor.fill_/zero_` CPU fallback (tensor-level wrap)

Date: 2026-05-20
Branch: `fix/gfx906-vllm-tensor-fill-cpu-fallback`
Parent evidence: `.loom/ralph-gfx906-vllm-init-fill-evidence-2026-05-20.md`

## TL;DR

The init-time `torch.nn.init._no_grad_fill_/_no_grad_zero_` CPU fallback from
[!450](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/450)
fixed the `LayerNorm.reset_parameters` crash at `opt.py:245`. Live load on
image `sha256:471472d5…` advanced into the post-`__init__` weight-load step
and crashed at `vocab_parallel_embedding.py:401`:

```python
401:             param[loaded_weight.shape[0]:].data.fill_(0)
```

That is a **direct `Tensor.fill_(0)` call on a HIP tensor slice** — same
broken HIP op as before, but reached **without** `torch.nn.init` wrapping, so
the existing init-level hook is not on the path.

This slice closes the gap at the tensor-method layer. New hook
`flexinfer_vllm_torch_tensor_compat.py` monkey-patches `torch.Tensor.fill_`
and `torch.Tensor.zero_` on HIP tensors only, routing through a CPU mirror +
`self.copy_(cpu_mirror)`. Reuses the existing `_flexinfer_gfx906_safe`
idempotency flag. Two new tokens added to the runtime-patch-contract check.

## Riskiest assumption + kill-test

**Load-bearing assumption**: Python-level monkey-patching of
`torch.Tensor.fill_` and `torch.Tensor.zero_` actually intercepts the call
from `vllm/model_executor/layers/vocab_parallel_embedding.py:401`
(`param[loaded_weight.shape[0]:].data.fill_(0)`), AND
`self.copy_(cpu_mirror)` on the HIP destination does NOT itself segfault on
Vega20 — i.e. `Tensor.copy_` between CPU source and HIP destination is the
known-good HIP kernel that line 400 already exercised successfully.

**Kill test**: OPT-125M smoke curl from
`.loom/60-validation-matrix.md` row 175 against the
`qwen3-1p7b-vllm-radeonvii` canary on `cblevins-radeonvii`. Pass criterion:
HTTP 200 with a non-empty `choices[0].message.content` field inside the
15-min smoke window. Run after the new image digest is pinned via Flux and
the GPUProfile + model pod reflect the new hash. Procedure verbatim from
row 175 of the validation matrix.

**Failure mode if the assumption is wrong**:

1. *Patch doesn't apply*: same `vocab_parallel_embedding.py:401` traceback,
   no `flexinfer_vllm_torch_tensor_compat.py` frame in the stack. Suggests a
   C-level dispatch path that bypasses Python attribute lookup (unlikely for
   `Tensor.fill_/zero_` which are normal Python-overridable methods on
   `torch._C._TensorBase`, but possible on some PyTorch versions). Recovery:
   move the wrap to `torch._C._TensorBase.fill_` directly, or use a
   `__torch_function__` override at module load time.
2. *`copy_` also crashes*: new traceback at `safe()` lines, with `copy_` in
   the deeper Python frames. Would mean the HIP `copy_` kernel from
   CPU→HIP is also broken on Vega20 even though it ran cleanly at line 400
   (e.g. only specific dtype/shape combinations crash). Recovery: switch
   the CPU mirror approach to `torch.full(...).to(device=hip)` followed by
   `data_ptr` rebind, or wrap `copy_` with a chunked variant.
3. *Patch and copy_ both succeed, new segfault*: load advances past
   `weight_loader` and crashes somewhere further along (compile, warmup,
   forward pass). Recovery: capture the new traceback, queue the next slice
   targeting whatever op is broken next.

**Status**: not run (will execute after MR !451-equivalent digest pin merges
and Flux reconciles the new image onto `cblevins-radeonvii`).

## Scope

**In**:

- New hook file `flexinfer_vllm_torch_tensor_compat.py` registered in
  `build/scripts/install_vllm_gfx906_compat.py`. Body:
  - `_install()` returns early on non-HIP hosts.
  - `_patch_tensor_method("fill_")` and `_patch_tensor_method("zero_")`.
  - HIP-only branch: allocate `torch.empty(self.shape, dtype=self.dtype,
    device="cpu")`, call `original(cpu_mirror, *args, **kwargs)`, then
    `self.copy_(cpu_mirror)` inside `torch.no_grad()`.
  - Idempotency via `_flexinfer_gfx906_safe` flag on the wrapper.
- Contract check additions in `scripts/check-runtime-patch-contracts.py`:
  - `flexinfer_vllm_torch_tensor_compat.py` in `required_hooks`.
  - New `tensor_contract` tuple with `_patch_tensor_method("fill_")` and
    `_patch_tensor_method("zero_")` tokens.

**Out**:

- Wrapping `Tensor.copy_`. Live evidence shows line 400 already used
  `param[:N].data.copy_(loaded_weight)` against a HIP destination
  successfully. Wrapping `copy_` blast-radius is much larger (every load,
  every gradient update, every shard merge) and not justified by current
  evidence. Add only if failure mode 2 fires.
- Wrapping `Tensor.normal_/uniform_` at the tensor level. Already covered
  by the `torch.nn.init.*` CPU-mirror in the init hook.
- Touching `torch.nn.init` — that path is unchanged.

## Implementation

Single-file diff against `build/scripts/install_vllm_gfx906_compat.py` adds
one new entry to the `HOOKS` dict between `flexinfer_vllm_torch_init_compat`
and `flexinfer_vllm_worker_diagnostics`. The new entry mirrors the
`_patch_in_place` framework used by the init hook but targets
`torch.Tensor.<name>` instead of `torch.nn.init.<name>`.

Two contract assertions added to
`scripts/check-runtime-patch-contracts.py`:

```python
required_hooks = (..., "flexinfer_vllm_torch_tensor_compat.py", ...)

tensor_contract = (
    '_patch_tensor_method("fill_")',
    '_patch_tensor_method("zero_")',
)
```

The `.pth` file auto-includes the new hook because
`PTH_IMPORTS = "\n".join(f"import {name.removesuffix('.py')}" for name in HOOKS)`
iterates the dict in insertion order.

## Verification gate

Local (pre-merge):

```bash
python3 scripts/check-runtime-patch-contracts.py --run-script-tests
TGT=$(mktemp -d) && python3 build/scripts/install_vllm_gfx906_compat.py --target "$TGT"
python3 -c "import py_compile; py_compile.compile('$TGT/flexinfer_vllm_torch_tensor_compat.py', doraise=True)"
```

Live (post-merge, after digest pin reconciles via Flux):

- Pod hash reflects new image digest.
- OPT-125M curl from `.loom/60-validation-matrix.md` row 175 returns
  HTTP 200 with non-empty completion.
- No `vocab_parallel_embedding.py:401` traceback in Loki for the canary
  pod during the smoke window.

## What this loop does NOT cover

- Larger gfx906 models (Qwen3-1.7B at real load, 4B+ models). Those are
  separate slices once OPT-125M turns HTTP 200.
- The `mistral_common` bump in the gfx1100 vLLM image. Still queued from
  `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md` and independent
  of this gfx906 work.

## References

- Closing evidence from prior slice:
  `.loom/ralph-gfx906-vllm-init-fill-evidence-2026-05-20.md` (decisive
  traceback with `vocab_parallel_embedding.py:401`).
- Init-fallback framework reused here:
  `.loom/ralph-gfx906-vllm-init-fill-cpu-fallback-2026-05-20.md`.
- vLLM v0.7.3 references:
  - `vllm/model_executor/layers/vocab_parallel_embedding.py:401` =
    `param[loaded_weight.shape[0]:].data.fill_(0)`.
  - `vllm/model_executor/models/opt.py:430` =
    `weight_loader(param, loaded_weight)`.
- Memory: `gfx906-vllm-fill-segfault.md` (root-cause = `Tensor.fill_` on
  Vega20, not RNG).
