# RALPH: gfx906 vLLM `torch.nn.init` CPU fallback — fill/zero evidence + close

Date: 2026-05-20
Branch (this MR): `docs/gfx906-vllm-init-fill-evidence`
Parent slice plan: `.loom/ralph-gfx906-vllm-init-fill-cpu-fallback-2026-05-20.md`

## TL;DR

The fill/zero extension to the CPU-fallback hook works as designed for the
`torch.nn.init._no_grad_*` family. Two MRs shipped in this loop on top of the
prior `!447`/`!448` chain:

| MR | What landed | Live evidence |
|----|-------------|---------------|
| [!450](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/450) | Two new `_patch_in_place(...)` calls for `_no_grad_fill_` and `_no_grad_zero_` in `build/scripts/install_vllm_gfx906_compat.py`. Two new tokens in `scripts/check-runtime-patch-contracts.py`'s `init_contract`. | Live load now reaches `opt.py:430` (`load_weights`) — passes ALL of `__init__` including `OPTLearnedPositionalEmbedding` (opt.py:218), `VocabParallelEmbedding` construction (opt.py:213), and the `LayerNorm` reset that defeated the prior slice (opt.py:245). |
| [!451](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/451) | Pin `deploy/gpuprofiles/gfx906.yaml` + `deploy/system/values-k3s.yaml` to `sha256:471472d5…` emitted by publish job 111194 on pipeline 10757. | Flux reconciled to `flexinfer-system/flexinfer.v1235` (chart `flexinfer@1.0.2+ff6c47809ed6.3`) within seconds of merge; GPUProfile `gfx906` reflects the new digest. Pod hash `67bf66f84b` confirms the new image is loaded. |

**Kill-test verdict**: HTTP 200 not yet achieved. The slice's load-bearing
assumption ("HIP `_no_grad_fill_/_no_grad_zero_` is the next segfault root,
CPU mirror works") is **confirmed for the `nn.init.*` family**. The slice's
predicted **failure mode 3** ("the segfault moves outside
`torch.nn.init._no_grad_*` family") fired exactly as written: the new crash is
a direct `Tensor.fill_(0)` call inside vLLM's
`VocabParallelEmbedding.weight_loader` that **bypasses** `torch.nn.init`. This
sharpens the root-cause picture and the next slice's target.

## Riskiest assumption (close)

**Status**: PASS for `nn.init.*` family; FAIL for end-to-end OPT load.

- `flexinfer_vllm_torch_init_compat.py` is loaded by the new image and is on
  the path for all init-time fill/zero (`init.ones_(weight)` and
  `init.zeros_(bias)` in `LayerNorm.reset_parameters`).
- After the extension the OPT load reaches `opt.py:430` (`load_weights`)
  instead of `opt.py:245` (`LayerNorm.__init__`). That is a strictly downstream
  step **outside `__init__`** entirely, so the `_no_grad_fill_/_no_grad_zero_`
  path is no longer the blocker.
- Curl smoke from `.loom/60-validation-matrix.md` row 175 still returns a
  15-min proxy timeout, not HTTP 200, because the model pod CrashLoopBackOffs
  on the new segfault before it can serve.

## Decisive live evidence (in-cluster Loki query)

The faulthandler stack from pod
`qwen3-1p7b-vllm-radeonvii-67bf66f84b-kd5tb` on `cblevins-radeonvii`
(image `sha256:471472d5…`), restart `model/0.log`, 2026-05-20T16:52:55Z:

```text
Fatal Python error: Segmentation fault

Thread <main> (most recent call first):
  File "/usr/lib/python3.12/threading.py", line 359 in wait
  …

Current thread 0x00007f653c21a000 (most recent call first):
  File ".../vllm/model_executor/layers/vocab_parallel_embedding.py", line 401 in weight_loader
  File ".../vllm/model_executor/models/opt.py", line 430 in load_weights
  File ".../vllm/model_executor/model_loader/loader.py", line 409 in load_model
  File ".../vllm/worker/model_runner.py", line 1112 in load_model
  File ".../vllm/worker/worker.py", line 183 in load_model
  File "/usr/lib/python3.12/multiprocessing/process.py", line 108 in run
  File ".../flexinfer_vllm_worker_diagnostics.py", line 40 in run_with_trace
  …
```

The same traceback reproduces on restarts `model/1.log` through `model/3.log`
within the 15-min smoke window (verbatim modulo the thread pointer). All four
restarts traverse the same call chain into `weight_loader`.

Notable differences from the `60b1ab0b` traceback that closed the prior
slice:

- **No `flexinfer_vllm_torch_init_compat.py` frame in the Python stack** — the
  hook is not on the new path. That is correct: this is not a
  `torch.nn.init._no_grad_*` call.
- **`opt.py:245` is no longer the crash site.** Model `__init__` now completes
  for `OPTDecoder` (line 346) plus its `final_layer_norm` (line 245), its
  positional embedding (line 218), and the make_layers fan-out (line 305).
  Load advances to the post-`__init__` weight-load phase.
- **`opt.py:430` is `weight_loader(param, loaded_weight)`** — the per-parameter
  dispatch in `OPTForCausalLM.load_weights`:

  ```python
  428:                 weight_loader = getattr(param, "weight_loader",
  429:                                         default_weight_loader)
  430:                 weight_loader(param, loaded_weight)
  ```

- **`vocab_parallel_embedding.py:401` is `param[loaded_weight.shape[0]:].data.fill_(0)`**:

  ```python
  398:             param.data.copy_(padded_weight)
  399:         else:
  400:             param[:loaded_weight.shape[0]].data.copy_(loaded_weight)
  401:             param[loaded_weight.shape[0]:].data.fill_(0)
  ```

  Line 400 (`copy_(loaded_weight)`) executed first and did **not** crash —
  the C-side fault is specifically on the `.data.fill_(0)` call on a HIP
  tensor slice (zero-padding for the vocab embedding when the loaded vocab
  is smaller than the parallel-padded vocab).

## What does NOT need another slice

- `_no_grad_fill_/_no_grad_zero_` family in this image: the hook now routes
  `LayerNorm.reset_parameters` (`init.ones_`/`init.zeros_`) through CPU mirror
  tensors. Live load reaches the post-`__init__` weight-load step before
  crashing, which means the `__init__` failure mode introduced in the prior
  cycle is closed.
- The `.pth` ordering + site-packages install: unchanged from the prior cycle.
  The hook works on the new image (`471472d5`) — proven by the load
  advancing past `LayerNorm` into `load_weights`.
- The `_patch_in_place` framework itself: unchanged. Adding two more entries
  required no signature, idempotency, or wrap-flag changes.

## What the next slice needs to cover

The new segfault is a `Tensor.fill_(0)` HIP call **outside**
`torch.nn.init._no_grad_*`. Concrete next-slice scope:

1. Add a tensor-method wrapper for `torch.Tensor.fill_` and
   `torch.Tensor.zero_` on Vega20 so any in-place fill/zero into a HIP tensor
   routes through CPU mirror + `copy_`. Reuse `_flexinfer_gfx906_safe`
   idempotency. This is the wrapper site originally anticipated by the
   handoff candidate (2.a) in
   `.loom/ralph-gfx906-vllm-torch-init-cpu-fallback-2026-05-20.md`, but now
   keyed on `fill_/zero_` (NOT `normal_/uniform_`, which the in-place RNG
   hypothesis was wrong about).
2. Verify that `Tensor.copy_(cpu_tensor_or_host_tensor)` from line 400
   continues to work after wrapping (it did in this slice — line 400 ran
   before the line 401 crash). If wrapping `.fill_` somehow affects `.copy_`
   on the same Tensor instance, fall back to a narrower wrap.
3. **Tier-1 PASS gate**: the OPT-125M smoke curl from
   `.loom/60-validation-matrix.md` row 175 returns HTTP 200 with a non-empty
   completion.

If `Tensor.fill_/.zero_` wrapping is insufficient (segfault moves to e.g.
`.copy_()` or to a different op in another vLLM layer), extend the wrapper
list one op at a time rather than monkey-patching all in-place tensor ops
upfront — the blast radius of patching `Tensor.copy_` is much larger than
`Tensor.fill_` and may surface false positives.

## Production impact during this loop

- 7900 XTX warm primary `gemma4-26b-a4b-gptq` was unaffected (sister 26B on
  5930k handled traffic).
- Radeon VII (gfx906) hosted four CrashLoopBackOff cycles on the
  `qwen3-1p7b-vllm-radeonvii` canary inside the 15-min smoke window. No
  other gfx906 workloads were preempted; the SDXL inpainting lane stayed
  Ready. The canary stays at `serverless.minReplicas: 0` so the cycle stops
  naturally when no curl is in flight.

## Handoff

Next-slice candidates ranked by priority:

1. **Tensor-level `fill_/zero_` wrapper for HIP** (continuation). Monkey-patch
   `torch.Tensor.fill_` and `torch.Tensor.zero_` in
   `flexinfer_vllm_torch_init_compat.py` (or a new sibling hook) for HIP
   tensors only. Verify with the same OPT-125M curl. This is the recommended
   path because the segfault is now strictly inside a HIP `fill_` kernel
   invoked outside `torch.nn.init`.
2. **mistral_common bump in gfx1100 vLLM image** — still queued from
   `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`. Independent
   of this gfx906 kill-test; can run in parallel.

## References

- Slice plan: `.loom/ralph-gfx906-vllm-init-fill-cpu-fallback-2026-05-20.md`
- Prior slice evidence (init.normal_/_uniform_/_trunc_normal_):
  `.loom/ralph-gfx906-vllm-cpu-init-fallback-evidence-2026-05-20.md`
- Diagnostic digest history:
  `.loom/ralph-gfx906-vllm-diagnostic-digest-2026-05-19.md`,
  `.loom/ralph-gfx906-vllm-worker-diagnostics-2026-05-19.md`.
- Validation row: `.loom/60-validation-matrix.md` row 175.
- MRs landed in this loop: !450 (this branch's parent), !451 (digest pin),
  this MR.
- Publish artifacts: publish job `111194` on pipeline 10757, image
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:471472d51eb17e54662bdbfd864207c0b3b4ae0b253c30d6c2ba427e2d036fae`.
- Live pod with decisive trace:
  `qwen3-1p7b-vllm-radeonvii-67bf66f84b-kd5tb` on `cblevins-radeonvii`
  (Radeon VII / gfx906), Loki window 2026-05-20T16:52:55Z … 16:55:43Z, four
  restarts captured (`model/0.log` … `model/3.log`).
- Upstream vLLM v0.7.3 references:
  - `vllm/model_executor/models/opt.py:430` = `weight_loader(param, loaded_weight)`.
  - `vllm/model_executor/layers/vocab_parallel_embedding.py:401` =
    `param[loaded_weight.shape[0]:].data.fill_(0)`.
