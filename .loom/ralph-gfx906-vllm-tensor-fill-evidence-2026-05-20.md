# RALPH: gfx906 vLLM `torch.Tensor.fill_/zero_` CPU fallback — live evidence + close

Date: 2026-05-20
Branch (this MR): `docs/gfx906-vllm-tensor-fill-evidence`
Parent slice plan: `.loom/ralph-gfx906-vllm-tensor-fill-cpu-fallback-2026-05-20.md`

## TL;DR

The tensor-level CPU fallback for `torch.Tensor.fill_/zero_` works as
designed. Two MRs shipped in this loop on top of the prior `!450`/`!451`
chain:

| MR | What landed | Live evidence |
|----|-------------|---------------|
| [!453](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/453) | New hook `flexinfer_vllm_torch_tensor_compat.py` in `build/scripts/install_vllm_gfx906_compat.py` that monkey-patches `torch.Tensor.fill_` and `torch.Tensor.zero_` on HIP tensors only, routing through a CPU mirror + `self.copy_(cpu_mirror)`. Two new tokens (`_patch_tensor_method("fill_")` and `_patch_tensor_method("zero_")`) added to `scripts/check-runtime-patch-contracts.py`'s `tensor_contract`. | Live load now reaches `model_runner.py:1115 — Loading model weights took 0.2500 GB`. ALL of `__init__` AND ALL of the per-parameter `weight_loader` calls (including `vocab_parallel_embedding.py:401`'s `param[loaded:].data.fill_(0)` zero-pad) complete cleanly. Load advances into `_dummy_run` for KV cache sizing. |
| [!454](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/454) | Pin `deploy/gpuprofiles/gfx906.yaml` + `deploy/system/values-k3s.yaml` to `sha256:2139c92b…` emitted by publish job 111364 on pipeline 10767. | Flux reconciled the `flexinfer` HelmRelease to revision `1.0.2+7d6d507fa7c2.3` within seconds; GPUProfile `gfx906` reflects the new digest. Pod hash `7c47cf898c` confirms the new image is loaded. Publish job duration was **59s** (BuildKit cache reused all but the small Python-script copy layer). |

**Kill-test verdict**: HTTP 200 not yet achieved. The slice's load-bearing
assumption ("Python-level monkey-patching of `torch.Tensor.fill_/zero_`
intercepts the call from `vocab_parallel_embedding.py:401`, AND
`self.copy_(cpu_mirror)` does NOT itself segfault on Vega20") is
**confirmed for the tensor-level fill/zero ops**. The slice's predicted
**failure mode 3** ("new segfault — load advances past `weight_loader`
and crashes further along — compile, warmup, forward pass") fired
verbatim: the new crash is the **forward-pass `F.embedding` op** in
`vocab_parallel_embedding.py:47`, reached via `_dummy_run → profile_run →
determine_num_available_blocks`. This is a strictly downstream phase from
weight load and an entirely different HIP op family
(`torch.embedding`/`index_select`, not `fill_/zero_`).

## Riskiest assumption (close)

**Status**: PASS for `torch.Tensor.fill_/zero_` family; FAIL for end-to-end
OPT-125M HTTP 200.

- `flexinfer_vllm_torch_tensor_compat.py` is loaded by the new image and
  is on the path for all `.data.fill_(0)` calls (including the vocab
  zero-pad slice in `weight_loader`).
- After the extension, the OPT load completes ALL of `__init__` AND ALL of
  the per-parameter `weight_loader` dispatch (`opt.py:428-430`), reaches
  `model_runner.py:1115 — Loading model weights took 0.2500 GB`, then
  enters the post-load profile run for KV cache sizing.
- Curl smoke from `.loom/60-validation-matrix.md` row 175 still returns
  exit 28 (15-min curl timeout), not HTTP 200, because the model pod
  startup-probe fails on the new forward-pass crash before vLLM's
  `/health` endpoint comes up. Pod was scaled down 17 seconds after start
  by Deployment health gating.

## Decisive live evidence (in-cluster Loki query)

The faulthandler stack from pod
`qwen3-1p7b-vllm-radeonvii-7c47cf898c-bpczm` on `cblevins-radeonvii`
(image `sha256:2139c92b…`), 2026-05-20T19:27:02Z:

```text
INFO 05-20 19:27:02 model_runner.py:1115] Loading model weights took 0.2500 GB
[rank0]:[W520 19:27:02.222...] symbolizing C++ stack trace for exception ...

Fatal Python error: Segmentation fault

Current thread 0x00007f49f4afe000 (most recent call first):
  File ".../torch/nn/functional.py", line 2567 in embedding
  File ".../vllm/model_executor/layers/vocab_parallel_embedding.py", line 47 in embedding
  File ".../vllm/model_executor/layers/vocab_parallel_embedding.py", line 415 in forward
  File ".../vllm/model_executor/models/opt.py", line 258 in get_input_embeddings
  File ".../vllm/model_executor/models/opt.py", line 271 in forward
  File ".../torch/nn/modules/module.py", line 1787 in _call_impl
  File ".../vllm/model_executor/models/opt.py", line 325 in forward
  File ".../vllm/compilation/decorators.py", line 172 in __call__
  File ".../vllm/model_executor/models/opt.py", line 370 in forward
  File ".../vllm/worker/model_runner.py", line 1724 in execute_model
  File ".../vllm/worker/model_runner.py", line 1346 in _dummy_run
  File ".../vllm/worker/model_runner.py", line 1235 in profile_run
  File ".../vllm/worker/worker.py", line 229 in determine_num_available_blocks
  File ".../vllm/engine/llm_engine.py", line 421 in _initialize_kv_caches
  File ".../vllm/engine/llm_engine.py", line 276 in __init__
  File ".../vllm/engine/multiprocessing/engine.py", line 391 in run_mp_engine
  File "/usr/lib/python3.12/multiprocessing/process.py", line 108 in run
  File ".../flexinfer_vllm_worker_diagnostics.py", line 40 in run_with_trace
  ...
```

Notable differences from the `471472d5` traceback that closed the prior
slice:

- **No `flexinfer_vllm_torch_tensor_compat.py` frame in the Python stack**
  — the tensor-level hook is not on this new path. That is correct: this
  is not a `Tensor.fill_/zero_` call; it is `F.embedding`
  (`index_select`).
- **`vocab_parallel_embedding.py:401` is no longer the crash site.**
  Weight load completes for OPT-125M's `VocabParallelEmbedding`, all
  attention/MLP layers, and the final `LayerNorm` — confirmed by the
  `Loading model weights took 0.2500 GB` log line at
  `model_runner.py:1115` immediately before the crash. The
  `param[loaded:].data.fill_(0)` zero-pad slice that defeated the prior
  cycle now routes through CPU mirror + `copy_` and runs cleanly.
- **`vocab_parallel_embedding.py:47` is `return F.embedding(input_, layer.weight)`**
  — the actual embedding lookup (vocab matrix indexed by input token
  IDs). The crash is in the C-side `torch.embedding`/`index_select` HIP
  kernel on Vega20, invoked here by `_dummy_run` constructing a profile
  forward pass for KV cache sizing.

## What does NOT need another slice

- `Tensor.fill_/zero_` family in this image: the hook now routes every
  in-place fill/zero through CPU mirror tensors. Live load reaches the
  post-load `_dummy_run` phase before crashing, which means the
  weight-load failure mode introduced in the prior cycle is closed.
- The `_no_grad_*` family from the cycle before that: also confirmed
  closed (its layer is downstream-passed; `__init__` is in the steady
  state).
- The hook framework (`_patch_tensor_method`, `_patch_in_place`,
  `_flexinfer_gfx906_safe` idempotency, `.pth` ordering, site-packages
  install) is unchanged. Adding two tensor-method entries required no
  signature, idempotency, or wrap-flag changes.

## What the next slice needs to consider

The new segfault is at a fundamentally different layer: a forward-pass
HIP kernel (`torch.embedding` / `index_select`), not an init or
weight-load op. This forces a strategic question, not a tactical one.
Three candidate framings:

1. **Continue the monkey-patch ladder**: wrap `torch.Tensor.index_select`
   or `torch.embedding` so the embedding lookup runs on CPU. Smallest
   diff, fastest to test, but **defeats GPU acceleration for the hot
   path** — every forward pass would force the embedding matrix lookup
   through host memory. Probably wrong as a permanent fix, but might
   make HTTP 200 achievable as a proof of concept and unlock further
   debugging of downstream forward-pass ops.
2. **Stop monkey-patching, fix the HIP kernel**: gfx906 (Vega20) is in
   ROCm maintenance mode since Q3 2023, full support ended ROCm 5.7,
   bug fixes ended Q2 2024 (`MEMORY.md`). The current image is on ROCm
   6.x via the `mixa3607/pytorch-gfx906` community PyTorch. The
   underlying `torch.embedding` may be broken in this build of
   ROCm+PyTorch on Vega20 even though it works on every other
   gfx9xx/gfx10xx/gfx11xx target. Verify with a minimum-reproducer
   `torch.embedding(torch.zeros(1, dtype=torch.int64, device='cuda'),
   torch.randn(50272, 768, device='cuda'))` against the same image.
   If the minimum repro crashes, this is a HIP runtime bug, not a vLLM
   bug, and the right next step is a ROCm/PyTorch source bisect — out
   of RALPH-slice scope.
3. **Strategic pivot**: declare OPT-125M on vLLM on gfx906 a
   feasibility-only canary, and pivot the canary artifact to a backend
   that does not hit the broken forward-pass ops (e.g. llama.cpp on
   gfx906, which the GTX 980 Ti work tree already validates as a
   workable kill-test substrate). The vLLM canary stays in the
   validation matrix for future kernel-fix bisect work but is not the
   gate for declaring radeonvii a useful inference node.

Recommended initial probe: framing **1** as a debug instrument only (NOT
to merge as a permanent fix), to determine whether wrapping the
embedding lookup unblocks HTTP 200 — if yes, the next forward-pass crash
tells us how deep the ROCm kernel breakage runs. If no, the breakage is
in a still-deeper op and the strategic pivot becomes preferable.

## Production impact during this loop

- 7900 XTX warm primary `gemma4-26b-a4b-gptq` was unaffected (sister 26B
  on 5930k handled traffic).
- Radeon VII (gfx906) hosted one CrashLoopBackOff cycle on the
  `qwen3-1p7b-vllm-radeonvii` canary inside the 15-min smoke window
  (pod scaled down by health gating after 17 seconds, replicaset
  cleaned up cleanly). No other gfx906 workloads were preempted; the
  SDXL inpainting lane stayed Ready. The canary stays at
  `serverless.minReplicas: 0` so the cycle stops naturally when no curl
  is in flight.

## Handoff

Next-slice candidates ranked by priority:

1. **HIP embedding-op feasibility probe**: a self-contained Python
   script that exercises `torch.embedding(input_ids, weight)` on a
   Vega20 device inside the same vLLM image. If it crashes
   standalone, the issue is a HIP runtime bug, not a vLLM-layer bug,
   and no Python-level wrapping is the right fix. Run as a one-off
   Job on `cblevins-radeonvii` with the same image. ~30 minutes.
2. **Tensor-level `index_select`/`embedding` wrapper as debug probe
   only**: monkey-patch `torch.Tensor.index_select` (or `F.embedding`)
   in `flexinfer_vllm_torch_tensor_compat.py`. Routes the
   embedding-matrix lookup through host memory. Permanent merge would
   defeat GPU acceleration; this is an instrumentation MR to learn
   the next-deeper crash site.
3. **`mistral_common` bump in gfx1100 vLLM image** — still queued from
   `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`. Independent
   of this gfx906 kill-test; can run in parallel.

## References

- Slice plan: `.loom/ralph-gfx906-vllm-tensor-fill-cpu-fallback-2026-05-20.md`
- Prior slice evidence (init.*_fill_/_zero_):
  `.loom/ralph-gfx906-vllm-init-fill-evidence-2026-05-20.md`
- Diagnostic digest history:
  `.loom/ralph-gfx906-vllm-diagnostic-digest-2026-05-19.md`,
  `.loom/ralph-gfx906-vllm-worker-diagnostics-2026-05-19.md`.
- Validation row: `.loom/60-validation-matrix.md` row 175.
- MRs landed in this loop: !453 (this branch's parent), !454 (digest pin),
  this MR.
- Publish artifacts: publish job `111364` on pipeline 10767, image
  `registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:2139c92b3ca00716216f9e5644e9fbd29b2bba7237dc0459017c86012ece51c3`.
- Live pod with decisive trace:
  `qwen3-1p7b-vllm-radeonvii-7c47cf898c-bpczm` on `cblevins-radeonvii`
  (Radeon VII / gfx906), Loki window 2026-05-20T19:26:58Z … 19:27:02Z.
- Upstream vLLM v0.7.3 references:
  - `vllm/model_executor/layers/vocab_parallel_embedding.py:47` =
    `return F.embedding(input_, layer.weight)`.
  - `vllm/model_executor/layers/vocab_parallel_embedding.py:415` =
    `output = self.linear_method.embedding(self, masked_input.long())`.
  - `vllm/model_executor/models/opt.py:258` = `get_input_embeddings`.
  - `vllm/worker/model_runner.py:1115` = `Loading model weights took ...`.
  - `vllm/worker/model_runner.py:1235` = `profile_run`.
  - `vllm/worker/worker.py:229` = `determine_num_available_blocks`.
- Memory: `gfx906-vllm-fill-segfault.md` (sharpened root-cause: the broken
  HIP op family is broader than `fill_` — extends to `torch.embedding`,
  the actual forward-pass index_select on Vega20).
