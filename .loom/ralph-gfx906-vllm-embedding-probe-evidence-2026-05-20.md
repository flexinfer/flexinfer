# RALPH: gfx906 HIP embedding-op feasibility probe — live evidence + close

Date: 2026-05-20
Branch (this MR): `docs/gfx906-hip-embedding-probe-evidence`
Parent slice plan: `.loom/ralph-gfx906-vllm-embedding-probe-2026-05-20.md`

## TL;DR

The standalone HIP probe on Vega20 (gfx906) **reproduces and broadens**
the broken-op signature beyond what the prior monkey-patch ladder could
catch. The Python-level `Tensor.fill_/zero_` hook landed in MR !453
intercepts the calls vLLM makes through Python attribute resolution, but
the underlying C++ fused paths (`torch.zeros(..., device='cuda')`,
`torch.randn(..., dtype=float16, device='cuda')`) bypass that hook
and segfault directly.

Five of six HIP scenarios crashed. The crash always occurred at the
first non-float32 HIP tensor allocation, BEFORE the actual `F.embedding`
/ `Tensor.index_select` call could be reached. This means:

- The broken op family on Vega20 is **broader** than `torch.embedding`
  / `index_select`. It covers `at::native::zero_kernel_cuda` (used by
  `torch.zeros(device='cuda')`) and the HIP RNG-into-float16 path
  (used by `torch.randn(dtype=float16, device='cuda')`).
- Python-level Tensor-method monkey-patching has a **structural
  limit**: it can only intercept calls that go through Python attribute
  resolution. C++ fused tensor-creation APIs (`torch.zeros`,
  `torch.randn` when called with `device='cuda'`) bypass the patch
  entirely.
- vLLM's `weight_loader` reaches `model_runner.py:1115` cleanly because
  every fill goes through the Python `Tensor.fill_`, which the hook
  intercepts. But the moment `_dummy_run` creates a HIP tensor through
  any C++ fused path or invokes `F.embedding`, the bypass exposes the
  same broken HIP kernel family.

**Verdict**: monkey-patching path is structurally bounded. Strategic
pivot (declare OPT-125M vLLM on gfx906 a feasibility-only canary, move
the kill-test artifact to llama.cpp on gfx906) is the right next move.

## Riskiest assumption — close

**Status**: PASS for the probe's stated assumption ("standalone HIP
embedding ops will reproduce or rule out the segfault"). The probe
unambiguously reproduced the segfault — and the bypass mechanism is
now understood.

The probe's load-bearing assumption was that standalone
`torch.embedding(weight, ids)` on Vega20 would crash or pass. In
practice the probe never reached the `torch.embedding` call line —
each HIP scenario crashed at an earlier non-float32 HIP tensor
allocation line (the `torch.zeros(dtype=long, device='cuda')` and
`torch.randn(dtype=float16, device='cuda')` calls that prepared the
inputs). This is a **sharper** finding than the assumption anticipated:
even input-tensor preparation on Vega20 segfaults at C++ fused paths,
so the broken op family is broader than the production trace
suggested.

## Decisive in-cluster evidence

Job: `gfx906-hip-embedding-probe` in `flexinfer-system` on
`cblevins-radeonvii`. Image: pinned production digest
`registry.harbor.lan/flexinfer/vllm:rocm-gfx906@sha256:2139c92b3ca00716216f9e5644e9fbd29b2bba7237dc0459017c86012ece51c3`.
Pod ran 28 seconds wall clock (`startedAt: 2026-05-20T20:21:04Z` →
`finishedAt: 2026-05-20T20:21:32Z`). Container exit code 1 (driver
returned non-zero because failed scenarios were non-empty).

Environment per scenario:

```text
python=3.12.13
torch=2.10.0+git8514f05
hip_available=True
gpu_name=AMD Radeon VII
gpu_total_vram_gib=15.98
hip_version=7.2.53211
mem_get_info free=17163091968 total=17163091968
```

Scenario summary:

| Scenario | Op | Device | Dtype(s) | Crash line | Exit |
|---:|---|---|---|---|---:|
| 1 | `torch.embedding` `[50272, 768]` ids `[1, 1]` | CPU | f32, long | — | **0 PASS** |
| 2 | `torch.embedding` `[4, 8]` ids `[1, 1]` | HIP | f32, long | `ids = torch.zeros((1,1), dtype=long, device='cuda')` | **139 SEGV** |
| 3 | `torch.embedding` `[50272, 768]` ids `[1, 1]` | HIP | f32, long | `ids = torch.zeros((1,1), dtype=long, device='cuda')` | **139 SEGV** |
| 4 | `torch.embedding` `[50272, 768]` ids `[256, 1024]` | HIP | f16, long | `weight = torch.randn(50272, 768, dtype=f16, device='cuda')` | **139 SEGV** |
| 5 | `F.embedding` `[50272, 768]` ids `[1, 1]` | HIP | f16, long | `ids = torch.zeros((1,1), dtype=long, device='cuda')` | **139 SEGV** |
| 6 | `Tensor.index_select` `[50272, 768]` indices `[1024]` | HIP | f16, long | `ids = torch.zeros(1024, dtype=long, device='cuda')` | **139 SEGV** |

Decisive Python faulthandler stack (representative, scenario 2):

```text
Fatal Python error: Segmentation fault

Current thread 0x00007f2dc3c39000 (most recent call first):
  File "/scripts/probe.py", line 57 in run_scenario_2_hip_tiny
  File "/scripts/probe.py", line 122 in <module>

/scripts/run.sh: line 8: 51 Segmentation fault (core dumped) python3 /scripts/probe.py --scenario "$s"
DRIVER: SCENARIO 2 exit=139
```

`probe.py:57` = `ids = torch.zeros((1, 1), dtype=torch.long, device="cuda")`.
The preceding line 56 = `weight = torch.randn(4, 8, dtype=torch.float32, device="cuda")` completed successfully (Python execution advanced past it). So **HIP `torch.randn(dtype=float32, device='cuda')` works on Vega20**, but **HIP `torch.zeros(dtype=long, device='cuda')` does not**.

Scenario 4 sharpens further: crash at `probe.py:83` = `weight = torch.randn(50272, 768, dtype=torch.float16, device="cuda")`. Float32 randn works (scenarios 2, 3), **float16 randn segfaults** (scenario 4). This implies the HIP RNG init kernel has a dtype-dispatch break for float16 in addition to the long-tensor zero-init break.

DRIVER summary line:

```text
DRIVER: FAILED SCENARIOS = 2 3 4 5 6
```

(Scenarios 1 PASS.)

## Why the probe crashes BEFORE F.embedding

Python's `faulthandler` reports the call site frame at signal time. For
`torch.zeros(..., device='cuda')`, Python execution sits on the
assignment line until the C++ fused implementation returns. Internally,
`torch.zeros(device='cuda')` calls `at::empty(...)` + `at::zero_(...)`
where the latter dispatches to `at::native::zero_kernel_cuda`, which is
implemented in terms of the **same `fill_` HIP kernel** that vLLM's
`Tensor.fill_` hook is designed to bypass. But the C++ `at::zero_`
fused impl never goes through Python attribute resolution, so the
Python-level `Tensor.zero_` monkey-patch is bypassed entirely.

The same logic applies to `torch.randn(dtype=float16, device='cuda')`:
the C++ fused RNG impl skips the Python `Tensor.normal_`/`uniform_`
hooks and segfaults directly on the broken HIP kernel for float16.

vLLM's `weight_loader` ONLY hit the broken op via Python-level
`param[loaded:].data.fill_(0)` calls — which IS interceptable. That's
why MR !453's tensor-level hook closed the weight-load surface. But
`_dummy_run` produces input tensors (and intermediate scratch buffers)
through `torch.zeros`/`torch.empty`/`torch.full` C++ fused paths
internally, plus calls `F.embedding` which itself goes through a C++
op dispatch.

**Structural implication**: Python-level Tensor-method monkey-patching
cannot close the gap. The only patches that can plug the C++ fused
path are at the dispatcher / kernel level — i.e. patches in the
PyTorch C++ source tree or in the ROCm runtime, not in Python.

## What this means for the next slice

**The strategic pivot is the recommendation.**

Three concrete next steps in priority order:

1. **Strategic pivot — production**: declare OPT-125M vLLM on gfx906 a
   feasibility-only canary. Move the gfx906 production inference
   substrate to **llama.cpp on gfx906** (the GTX 980 Ti work already
   validates llama.cpp as a working embedding-lookup substrate; gfx906
   has a more mature ROCm path in llama.cpp than vLLM does). The
   `qwen3-1p7b-vllm-radeonvii` canary stays at `minReplicas: 0` in
   the validation matrix as a future ROCm-kernel-bisect reference.
2. **Document the C++-vs-Python boundary**: update
   `MEMORY.md` and `gfx906-vllm-fill-segfault.md` to record that
   Python `Tensor.fill_`/`Tensor.zero_` interception works for vLLM's
   `weight_loader` path but cannot close `torch.zeros(device='cuda')`
   / `torch.randn(dtype=float16, device='cuda')` C++ fused paths.
   Future agents must not assume one more monkey-patch closes the
   surface.
3. **Park the wrapper ladder**: do NOT add an `F.embedding` /
   `Tensor.index_select` Python-level wrapper to
   `flexinfer_vllm_torch_tensor_compat.py`. Even if it were possible
   to intercept (it would have to monkey-patch `F.embedding` at the
   `torch.nn.functional` module level), the call path now uses
   `torch.embedding` (the C++ op), which would still segfault from any
   intermediate `torch.zeros(device='cuda')` allocation in the
   `_dummy_run` prep.

## Out-of-scope (will NOT happen as a follow-up)

- **ROCm/PyTorch source bisect** to find the root commit that broke
  `at::native::zero_kernel_cuda` for long dtypes on Vega20. Vega20 has
  been in ROCm maintenance mode since Q3 2023 (full support ended ROCm
  5.7, bug fixes ended Q2 2024 — per `MEMORY.md`). A community
  PyTorch build (`mixa3607/pytorch-gfx906`) is the only viable
  substrate, and bisecting it is out of RALPH-slice scope.
- **Custom kernel replacement** for the broken `zero_kernel_cuda`.
  Same reason — out of scope and unjustified given vLLM is not the
  production gfx906 path.

## Production impact during this loop

- 7900 XTX warm primary `gemma4-26b-a4b-gptq` unaffected (sister 26B on
  5930k handled traffic).
- Radeon VII (gfx906): the probe Job ran 28 seconds wall clock plus
  ~6 minutes of image pull (the production digest was not cached on
  the node). No other gfx906 workloads were preempted; the SDXL
  inpainting lane stayed Ready throughout. The
  `qwen3-1p7b-vllm-radeonvii` canary stayed at `minReplicas: 0`.

## Handoff

The strategic pivot becomes the next-loop framing. RALPH inputs for
the next iteration:

1. **Declare the gfx906 vLLM lane feasibility-only** in
   `.loom/60-validation-matrix.md` row 175 (update `block` → `defer`
   or similar status; the row stays for future bisect work).
2. **Spec the gfx906 llama.cpp lane** as the production embedding
   substrate. Reuse the validated GTX 980 Ti llama.cpp work — the
   gfx906 path is materially different (HIP target, different image)
   but the spec template carries over.
3. **mistral_common bump in gfx1100 vLLM image** — still queued from
   `.loom/ralph-whisper-kill-test-v3-evidence-2026-05-20.md`,
   independent of this gfx906 work.

## References

- Slice plan: `.loom/ralph-gfx906-vllm-embedding-probe-2026-05-20.md`
- Prior tensor-fill evidence: `.loom/ralph-gfx906-vllm-tensor-fill-evidence-2026-05-20.md`
- Probe MR (job manifest): !456 (merged at `dc2fdb6a`).
- Pod logs: `kubectl -n flexinfer-system logs gfx906-hip-embedding-probe-94c9q`
  (Loki window `2026-05-20T20:21:04Z` → `20:21:32Z`).
- Validation row: `.loom/60-validation-matrix.md` row 175 (to be updated
  with this verdict).
- Memory: `gfx906-vllm-fill-segfault.md` (to be updated with the
  C++-vs-Python boundary finding).
