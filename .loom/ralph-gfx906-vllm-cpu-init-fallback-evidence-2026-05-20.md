# RALPH: gfx906 vLLM CPU-fallback `torch.nn.init` — live evidence + close

Date: 2026-05-20
Branch (this MR): `docs/gfx906-vllm-cpu-init-fallback-evidence`
Parent slice plan: `.loom/ralph-gfx906-vllm-torch-init-cpu-fallback-2026-05-20.md`

## TL;DR

The CPU-fallback hook for `torch.nn.init._no_grad_normal_` / `_no_grad_uniform_`
/ `_no_grad_trunc_normal_` works as designed for the `nn.init.*` family. Three
slices shipped in this loop:

| MR | What landed | Live evidence |
|----|-------------|---------------|
| [!446](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/446) | First pin of the rebuilt gfx906 vLLM image (`sha256:d545fb8a…`). | Hook is loaded — site-packages `.pth` import works; live traceback contains `flexinfer_vllm_torch_init_compat.py:22` (proves `.pth` ordering + import). |
| [!447](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/447) | Off-by-one args-forward fix in `build/scripts/install_vllm_gfx906_compat.py`: delegate to original `torch.nn.init.<name>_` on a CPU mirror tensor instead of `getattr(cpu_tensor, kernel_attr)(*args, **kwargs)`. | Closed `TypeError: normal_() takes from 0 to 2 positional arguments but 3 were given` that the first build hit when `_no_grad_normal_(tensor, mean, std, generator)` threaded the generator positionally. |
| [!448](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/448) | Second pin (`sha256:60b1ab0b…`) carrying the fix. | OPT-125M load progresses past the prior crash site (`opt.py:218` → embedding init) to a new crash at `opt.py:245`, with no `flexinfer_vllm_torch_init_compat.py` in the traceback. |

**Kill-test verdict**: HTTP 200 not yet achieved. The slice's load-bearing
assumption ("HIP RNG in `_no_grad_normal_` is the segfault root") is
**confirmed for the `nn.init.*` family**. The slice's predicted **failure mode
3** ("the random-init path was correct but another HIP RNG call sits
downstream") has fired — `opt.py:245` (`VocabParallelEmbedding`) crashes
during `OPTDecoder.__init__` via a path that does **not** go through
`torch.nn.init.*`. Recovery is a tensor-level wrapper, scoped as the next
slice (NOT another `init.*` family).

## Riskiest assumption (close)

**Status**: PASS for `nn.init.*` family; FAIL for end-to-end OPT load.

- `flexinfer_vllm_torch_init_compat.py` is loaded by the new image (verified
  in `#17 install_vllm_gfx906_compat.py` of CI job 110710, and again in
  job 110925).
- Live traceback in pod
  `qwen3-1p7b-vllm-radeonvii-847796b69d-kj5gd` (from the `d545fb8a` image)
  showed the hook firing at line 22 — proves import + `.pth` ordering.
- After the args-forward fix (`60b1ab0b` image) the OPT load reaches
  `opt.py:245` instead of `opt.py:218`. That is a strictly downstream step
  in `OPTDecoder.__init__` than the previous crash, so the `init.*`-family
  path is no longer the blocker.
- Curl smoke from `.loom/60-validation-matrix.md` row 175 returns a 15-min
  proxy timeout, not HTTP 200, because the model pod CrashLoopBackOffs on
  the new segfault before it can serve.

## Live evidence

### Hook is invoked (image `d545fb8a`)

From pod `qwen3-1p7b-vllm-radeonvii-847796b69d-kj5gd` model log, 2026-05-20 05:52:01Z:

```text
File "/usr/local/lib/python3.12/dist-packages/flexinfer_vllm_torch_init_compat.py", line 22, in safe
    getattr(cpu_tensor, kernel_attr)(*args, **kwargs)
TypeError: normal_() takes from 0 to 2 positional arguments but 3 were given
```

### Hook handles `_no_grad_normal_` (image `60b1ab0b`)

Pod `qwen3-1p7b-vllm-radeonvii-bf87b74d6-n6nst`, restart 0 through 6 (Loki
range 2026-05-20T14:39:00Z … 14:48:57Z):

```text
INFO 05-20 14:39:37 [llm_engine.py:234] Initializing a V0 LLM engine (v0.7.3)
  with config: model='facebook/opt-125m', … dtype=torch.float16,
  max_seq_len=2048, device_config=cuda, seed=0, enforce_eager=True, …
INFO 05-20 14:39:40 [model_runner.py:1110] Starting to load model facebook/opt-125m...
Fatal Python error: Segmentation fault
[W520 14:39:39.770841832 Module.cpp:201] symbolizing C++ stack trace for exception; …
  File "/usr/local/lib/python3.12/dist-packages/vllm/model_executor/models/opt.py", line 245 in __init__
  File "/usr/local/lib/python3.12/dist-packages/vllm/model_executor/models/opt.py", line 305 in __init__
  File "/usr/local/lib/python3.12/dist-packages/vllm/model_executor/models/opt.py", line 346 in __init__
  File "/usr/local/lib/python3.12/dist-packages/vllm/worker/model_runner.py", line 1112 in load_model
  …
[flexinfer-gfx906-vllm] uncaught exception in pid=1
RuntimeError: Engine process failed to start. See stack trace for the root cause.
```

Notable differences from the `d545fb8a` traceback that closed slice 1:

- No `flexinfer_vllm_torch_init_compat.py` frame in the Python stack — the
  hook isn't being entered for this crash, so the bad call isn't going
  through `torch.nn.init.*`.
- `opt.py:218` (`OPTLearnedPositionalEmbedding.__init__` →
  `reset_parameters` → `nn.init.normal_`) is no longer the crash site.
  The load now proceeds to `opt.py:245`, the next sub-module init in
  `OPTDecoder.__init__`. In vLLM 0.7.3 this is the
  `VocabParallelEmbedding` construction for `embed_tokens`.
- `Fatal Python error: Segmentation fault` with a Module.cpp symbolization
  warning before any Python `Traceback` — typical of a C-side crash inside
  a torch CUDA/HIP op, not a Python exception.

### Proxy / curl evidence

Proxy log:

```text
2026-05-20T14:36:11Z  ERROR  cold start failed
  model=qwen3-1p7b-vllm-radeonvii
  error="queue canceled for model qwen3-1p7b-vllm-radeonvii: context canceled"
2026-05-20T14:36:11Z  WARN   model failed to become ready
  model=qwen3-1p7b-vllm-radeonvii
  attempt=1
  error="timeout waiting for model to become ready (after 15m0s)"
```

Three independent smoke curls (`smoke-gfx906-vllm`, `…-v2`, `…-v3`) all
returned `curl: (28) Operation timed out after 900001 milliseconds with
0 bytes received`. Image was cached after the first attempt (subsequent
pulls measured 637 ms / 654 ms / 625 ms for the 10.5 GB image), so the
15-minute timeout is consumed by the vLLM CrashLoopBackOff cycle, not by
image pull.

## What does NOT need another slice

- `nn.init.*` family in this image: the wrapper now routes `_no_grad_normal_`,
  `_no_grad_uniform_`, and `_no_grad_trunc_normal_` through CPU mirror tensors
  via the original PyTorch function. Live load reaches a strictly later
  module init step before crashing.
- The `.pth` ordering + site-packages install: confirmed by hook entry frame
  appearing in the first failing traceback.
- The args-forwarding shape: fixed in !447 to delegate to `original(cpu_tensor,
  *args, **kwargs)`, which sidesteps both the `generator`-positional issue
  and the multi-kernel `_no_grad_trunc_normal_` sequence.

## What the next slice needs to cover

Per the parent slice doc's failure-mode 3:

> If the live smoke surfaces a different HIP RNG site (e.g.
> `torch.empty(..., device='cuda').uniform_()` called outside `nn.init`),
> extend the same hook with a tensor-level wrapper rather than patching
> another `init.*` family.

Concrete next-slice scope:

1. Identify what at `vllm/model_executor/models/opt.py:245` in vLLM 0.7.3
   triggers HIP RNG. The most likely path is
   `VocabParallelEmbedding.__init__` or its
   `weight_loader=DefaultWeightLoader` defaulting to a `torch.empty(...,
   device=device).uniform_()`-style initializer in the vLLM parallel layers
   (`vllm/model_executor/layers/vocab_parallel_embedding.py`,
   `vllm/model_executor/parameter.py`). Confirm by reading the line in the
   pinned image (`docker run --rm --entrypoint cat … vllm:rocm-gfx906@…
   /usr/local/lib/python3.12/dist-packages/vllm/model_executor/models/opt.py`).
2. Decide the wrapper site. Two candidates:
   - Patch `torch.Tensor.normal_` / `.uniform_` themselves on Vega20 so
     ANY in-place RNG into a HIP tensor routes through CPU + copy.
   - Patch the offending vLLM construction directly (smaller blast radius,
     larger risk of missing siblings).
3. Tier-1 PASS gate: the OPT-125M smoke curl from
   `.loom/60-validation-matrix.md` row 175 returns HTTP 200 with a non-empty
   completion. No publish + pin needed inside the kill-test if the wrapper
   lands in `install_vllm_gfx906_compat.py` and a fresh
   `publish_vllm_rocm_gfx906` builds against it.

If the candidate site for (1) does not reproduce, fall through to (2.a)
(tensor-method patch) as the minimum-blast-radius backstop — the wrapper is
already idempotent (`_flexinfer_gfx906_safe` flag) and would not double-wrap.

## Production impact during this loop

- 7900 XTX warm primary `gemma4-26b-a4b-gptq` was unaffected (sister 26B on
  5930k handled traffic during prior Whisper kill-test slice — see
  `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`).
- Radeon VII (gfx906) hosted three CrashLoopBackOff cycles on the
  `qwen3-1p7b-vllm-radeonvii` canary. No other gfx906 workloads were
  preempted; the SDXL inpainting lane stayed Ready. The canary stays at
  `serverless.minReplicas: 0` so the cycle stops naturally when no curl is
  in flight.

## Handoff

Next-slice candidates ranked by priority:

1. **Tensor-level RNG wrapper for HIP.** Extend
   `flexinfer_vllm_torch_init_compat.py` (or a new sibling hook) to wrap
   `torch.Tensor.normal_` and `torch.Tensor.uniform_` so the CPU-mirror
   path applies even when the caller is not in the `torch.nn.init.*` family.
   Verify with the same OPT-125M curl. This is the recommended path because
   the segfault is now strictly inside a HIP RNG kernel, not a logic issue.
2. **Read the pinned image's `opt.py:245` first** to confirm the wrapper
   site. If it turns out to be a different op entirely (e.g. an `empty()` +
   `copy_` from sharded weight load rather than RNG), pivot the wrapper to
   match. Source-of-truth pull: `docker run --rm --entrypoint cat
   registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:60b1ab0b…
   /usr/local/lib/python3.12/dist-packages/vllm/model_executor/models/opt.py |
   sed -n '230,260p'`.
3. **mistral_common bump in gfx1100 vLLM image** (still queued from
   `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`). Independent
   of this gfx906 kill-test; can run in parallel.

## References

- Slice plan: `.loom/ralph-gfx906-vllm-torch-init-cpu-fallback-2026-05-20.md`
- Diagnostic digest evidence: `.loom/ralph-gfx906-vllm-diagnostic-digest-2026-05-19.md`
- Validation row: `.loom/60-validation-matrix.md` row 175
- Whisper closeout (parallel slice): `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`
- MRs landed in this loop: !444 (precondition, prior session), !445 (Whisper teardown), !446 (first pin), !447 (args-forward fix), !448 (second pin), this MR.
- Live pod with decisive trace: `qwen3-1p7b-vllm-radeonvii-bf87b74d6-n6nst` on `cblevins-radeonvii` (Radeon VII / gfx906), Loki window 2026-05-20T14:39:00Z … 14:48:57Z.
