# AMD Radeon VII (gfx906) GPU Support in FlexInfer

> Last updated: 2026-03-20. Verified against ROCm 6.4.1, PyTorch 2.6, transformers 5.x.

## Hardware: Radeon VII / Vega20 / gfx906

- **Node**: `cblevins-radeonvii` (128 GB system RAM, 16 GB HBM2)
- **ISA**: gfx906. KFD reports it natively (`gfx_target_version 90006`; llama.cpp logs
  `gfx906:sramecc+:xnack- (0x906)`), so no `HSA_OVERRIDE_GFX_VERSION` is needed; see
  [Required Environment Variables](#required-environment-variables)
- **Wave size**: 64
- **Key limitation**: No VMM (Virtual Memory Management) support

## ROCm Support Timeline

| Milestone | Date | ROCm Version | Detail |
|-----------|------|-------------|--------|
| Full support | Through Q3 2023 | ROCm 5.7 | Last version with full gfx906 support |
| Maintenance mode | Q3 2023 | ROCm 5.7+ | No new features or optimizations |
| Bug fixes end | Q2 2024 | ROCm 6.0.x | End of maintenance |
| Effective removal | 2025+ | ROCm 6.4+ | Not in default build targets; Tensile libraries not shipped |

gfx906 is **deprecated but not removed** from ROCm. Code paths still exist, but AMD
does not test, optimize, or build pre-compiled libraries (rocBLAS Tensile, etc.) for it.

**Sources**:
- [ROCm/ROCm Discussion #3893](https://github.com/ROCm/ROCm/discussions/3893)
- [ROCm/ROCm Issue #2849](https://github.com/ROCm/ROCm/issues/2849)
- [ROCm Compatibility Matrix](https://rocm.docs.amd.com/en/latest/compatibility/compatibility-matrix.html)

## Known Hardware Limitations

### hipMemGetInfo / VMM Not Supported (UNFIXABLE)

`hipMemGetInfo()` returns `hipErrorInvalidValue` on gfx906 because the underlying VMM
API is not available on Vega20 hardware. This is a **hardware limitation**, not a
software bug. It cannot be fixed in any ROCm version.

```
Device 0: AMD Radeon Graphics, gfx906:sramecc+:xnack-, VMM: no
```

**Impact**: Any code that calls `torch.cuda.mem_get_info()`, including:
- `transformers` `caching_allocator_warmup` (added in v5.x)
- `accelerate` device map auto-detection
- vLLM memory profiling
- GPTQModel GPU memory estimation

**Workarounds by application**:

| Application | Workaround | Reference |
|-------------|-----------|-----------|
| llama.cpp | `patch-hipmemgetinfo.sh` — reads `/sys/class/drm/card0/device/mem_info_vram_total` | `build/patch-hipmemgetinfo.sh` |
| ONNX Runtime | Uses `rocm_smi` library (`rsmi_dev_memory_total_get`) | [PR #21190](https://github.com/microsoft/onnxruntime/pull/21190) |
| PyTorch/transformers | Monkey-patch `torch.cuda.mem_get_info` to return hardcoded values | `pkg/quantization/abliteration.go` wrapper script |
| FlexInfer abliteration | `device_map=cpu` (avoids GPU entirely) | Current approach for gfx906 |

**Sources**:
- [ROCm/ROCm Issue #1909](https://github.com/RadeonOpenCompute/ROCm/issues/1909)
- [PyTorch Issue #126015](https://github.com/pytorch/pytorch/issues/126015)
- [vLLM Issue #5994](https://github.com/vllm-project/vllm/issues/5994)

### torch.empty() on GPU Also Fails (ROCm 6.4)

On ROCm 6.4.1 with `HSA_OVERRIDE_GFX_VERSION=9.0.6`, even basic GPU memory allocation
via `torch.empty(..., device="cuda")` fails with `RuntimeError: HIP error: invalid argument`.
This means **GPU compute is completely non-functional** on gfx906 with ROCm 6.4.

This was confirmed on 2026-03-20 during abliteration testing. The `caching_allocator_warmup`
function in transformers 5.x calls `torch.empty()` on GPU after `mem_get_info`, and both fail.

### Kernel log bursts during llama.cpp model loads (harmless)

Every llama.cpp model load on the Radeon VII writes a burst of

```
amdgpu: init_user_pages: Failed to get user pages: -1
```

to the kernel log (hundreds of lines per model, ~288 for the 358 MB bge-large GGUF; nothing
between loads). llama.cpp memory-maps the GGUF and the HIP runtime tries to register those
file-backed pages with KFD as userptr memory before the host-to-device copy; KFD only accepts
anonymous pages for that path and answers `-EPERM`, so the runtime falls back to staging
copies and the load succeeds. Confirmed on 2026-10-23 by loading the same GGUF from the
2026-10-12 and 2026-10-23 gfx906 runtime images in throwaway containers on the node: both
logged exactly 288 lines. `--no-mmap` removes the burst but made the same load take more than
60 s instead of 3 s, so it is not a drop-in mitigation.

Operational consequences: a burst can rotate real `SMU` / `amdgpu` errors out of `dmesg`, and
`dmesg -T` wall-clock stamps drift on this long-uptime host (~50 s on 2026-10-23). Correlate
GPU events against `journalctl -k -o short-iso` and check its earliest entry before treating a
message as new. Evidence: `.loom/60-validation-matrix.md` row `gfx906-serving-2026-10-23`.

### 24 hardware compute queues: every additive GPU process is a co-residency hazard

The Radeon VII exposes **24 HWS compute queues**:

- `num_cp_queues 24` in `/sys/class/kfd/kfd/topology/nodes/1/properties`.
- `/sys/kernel/debug/kfd/hqds` lists 4 CP pipes × 6 queues. `sched_policy=0` (HWS).

Without a cap, every HIP process takes 4–6 of them. Each llama-server starts at 4 and creates
up to 6 lazily under concurrent requests.

On 2026-10-24 the warm plane held 22, then 26 after an hour of traffic, with nothing else on
the card:

| Process | Queues |
| --- | ---: |
| bge-large | 4–6 |
| bge-reranker | 4–6 |
| gte-qwen2 | 6 |
| orpheus | 6 |
| pyannote-diarization | 2 |

Past 24 the kernel logs `amdgpu: Runlist is getting oversubscribed. Expect reduced ROCm
performance.` and the HWS time-slices queues. Whichever process is unmapped waits. Measured
with throwaway llama-server containers beside the live plane:

| Extra process | Queues total | Live lanes through `flexinfer-proxy` |
| --- | ---: | --- |
| Qwen3-Embedding-4B, default queues | 26–28 | Every probe request took a flat 30 s. gte timed out at 20–60 s and recovered the moment the probe stopped (seen twice). |
| Same probe, `GPU_MAX_HW_QUEUES=1`, idle | 25–27 | Normal: probe 18–80 ms, gte 0.07–0.3 s |
| + Qwen3-Reranker, `GPU_MAX_HW_QUEUES=1` | 28 | gte over 8 s; the reranker could not finish loading |
| Qwen3-Embedding-4B, `GPU_MAX_HW_QUEUES=1`, continuous embedding | 29 | bge-large over 3 s twice in 7 s, so the watchdog killed the probe |

The plane itself crossed 24 at 13:50:11Z under nothing but proxy traffic.

The hazard covers every additive GPU tenant on this card:

- the cold runtime members (gemma4-e4b, humanizer while its swap overlaps orpheus, any new lane);
- dedicated pods such as the openjev vLLM cross-encoder;
- Jobs (quantization, abliteration);
- non-Model tenants such as pyannote.

VRAM admission (`vramEstimateMB`) does not see queues. A lane can fit the VRAM budget and
still stall the retrieval plane.

Mitigation:

- `GPU_MAX_HW_QUEUES=1` (HIP) caps a llama-server at 3 compute queues: 1 kernel queue plus the
  runtime's utility queues. It is set in the gfx906 GPUProfile `spec.env`.
- It reaches each runtime subprocess at its next load. A profile change reloads nothing, so
  restart the gfx906 runtime pod once to apply it to the warm lanes.
- Check the total with:

  ```bash
  sudo awk '/Compute queue/{t++} END{print t}' /sys/kernel/debug/kfd/mqds
  ```

  Keep it at 24 or below, with headroom for the next member.
- The same count is readable without debugfs, per process, from any privileged pod on the
  node (the runtime pod, for example). KFD lists every process on the host, with queue
  `type` 0 for compute and 1 for SDMA; only compute queues count against the 24:

  ```bash
  kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- sh -c '
    for d in /sys/class/kfd/kfd/proc/*; do
      c=0; for q in $d/queues/*; do [ "$(cat $q/type)" = 0 ] && c=$((c+1)); done
      echo "$(basename $d) compute=$c vram=$(cat $d/vram_* 2>/dev/null)"
    done'
  ```

  The PIDs are host PIDs; match a process by its VRAM. The runtime publishes the total as
  `flexinfer_runtime_hw_queues{source="observed"}` every 15 s.
- Rule of thumb with the cap: 3 queues per llama-server, 2 for pyannote.

#### Queue admission (GPUProfile `maxHWQueues`)

Since 2026-10-30 the budget is enforced, not left to convention.

- **GPUProfile** `spec.maxHWQueues: 24` is KFD `num_cp_queues`.
  `spec.reservedHWQueues: 2` is held back for pyannote, a hand-written Deployment the Model
  API cannot see. Jobs that take a GPU lease park the serving lanes, so they need no
  reserve. Unset `maxHWQueues` disables queue admission, which is the case on every other
  profile.
- **Model** `spec.gpu.hwQueueEstimate` overrides the estimate. Unset, it is
  `(GPU_MAX_HW_QUEUES + 2) × HIP processes`, where `GPU_MAX_HW_QUEUES` is the GPUProfile
  env overlaid by `spec.config.env` (ROCclr default 4):

  | Backend | HIP processes | Estimate with the gfx906 cap of 1 | Uncapped | Basis |
  | --- | ---: | ---: | ---: | --- |
  | llamacpp | 1 | 3 | 6 | Measured: 3 on four live lanes; 4–6 uncapped |
  | ollama | 1 | 3 | 6 | One runner per loaded model; the `ollama serve` parent only probes devices from short-lived children |
  | diffusers, snac | 1 | 3 | 6 | One Python server |
  | vllm, vllm-omni | 2 | 6 | 12 | API server + engine-core process. Not measured on gfx906, so both are assumed to reach the device |

  The `+2` is what a capped llama-server holds beyond its one kernel queue with
  `HSA_ENABLE_SDMA=0`. With SDMA on, one of the two becomes an SDMA queue, as pyannote
  shows, so the estimate errs high there. Measure a vLLM lane with the sysfs loop above
  and set `hwQueueEstimate` if it holds fewer.
- **Enforcement**, wherever VRAM admission runs:
  - Controller, multi-model election (`chooseSharedGroupLeadersBounded`): a member joins
    the Active set only if the summed estimate stays within `maxHWQueues -
    reservedHWQueues` = 22. The elected primary is always included. A member that cannot
    fit stays stamped `primary/` like a member that does not fit VRAM.
  - Controller, validation: a Model whose own estimate exceeds 22 goes `Schedulable=False`
    with reason `HWQueuesInsufficient`.
  - Runtime load admission, in both single-slot and multi-model mode. In-use queues are the
    larger of (the estimates of its other subprocesses + the reserve) and (the compute
    queues KFD reports on the GPU across all processes, which also sees dedicated pods,
    Jobs and pyannote). A load is refused when in-use + estimate exceeds 24. The runtime
    answers 503 with `insufficient hardware compute queues`, and the Model waits as
    `Pending` / `RuntimeCapacity` until another model releases queues.
  - The controller sends the budget with every load (`hwQueues` in the load payload). It is
    left out of the launch spec hash, so changing the budget reloads nothing.
- **Metrics**: `flexinfer_sharedgroup_hw_queues{group,gpu_arch,kind=estimated|budget|reserved}`
  (controller, per shared group = per GPU), and `flexinfer_runtime_hw_queues{source=estimated|observed}`
  plus `flexinfer_runtime_hw_queue_budget{kind=max|reserved}` (runtime).
- **Live accounting, 2026-10-30 13:40Z** (read through the runtime pod's sysfs view):

  | Process | Compute queues | VRAM |
  | --- | ---: | ---: |
  | bge-large and bge-reranker (one llama-server each; VRAM does not tell them apart) | 3 + 3 | 0.53 / 0.55 GB |
  | gte-qwen2 (llama-server) | 3 | 2.26 GB |
  | orpheus (llama-server) | 3 | 2.79 GB |
  | pyannote-diarization | 2 (+2 SDMA) | 0.18 GB |
  | **Total** | **14 of 24** | |

  The estimate matches: 4 × 3 = 12 for the Models plus 2 reserved = 14. The Model budget is
  22, so three more llama.cpp lanes fit (21) and a fourth is refused (24 > 22). The
  2026-10-24 set with both Qwen3 lanes comes to 18 + 2 = 20. With the openjev judge at its
  vLLM estimate of 6 it is also 18 + 2 = 20. Replaying these sets through the election
  (`TestHWQueueBoundIsLiveNeutralOnRadeonVII`) gives exactly the VRAM-only Active sets.
- **Second HWS limit (not enforced separately)**: KFD also oversubscribes when more
  processes are mapped than `max_proc_per_quantum`. `hws_max_conc_proc` is `-1` on this node,
  so the limit is the KFD VMID count, 8 on gfx9 per the amdgpu source (not measured). At 3
  queues per process the 22-queue Model budget plus pyannote stops at 8 processes, so the
  queue budget covers it for today's lanes.

Applied 2026-10-24 (!1140) with one runtime pod restart at 15:39:08Z:

- The four warm lanes went from 26 queues to 14 and were Ready again within 26 s.
- Both Qwen3 lanes loaded beside them came to 20 queues, with no new oversubscription message.
- During a full-duty Qwen3-4B embedding run (GPU busy 98%), each live lane answered directly
  at its Service in p95 11–143 ms and max 195 ms.
- Two hours later (17:39Z) the plane still held 14 queues: with the cap, lanes no longer add
  queues lazily under traffic.
- The deployed Qwen3 lanes (!1146) inherited the cap at 3 queues each, for a total of 20 and no
  oversubscription message. The live lanes answered directly in ≤ 30 ms during both cold loads
  (17:45:51Z and 17:46:04Z).

Measure GPU contention at each lane's Service, not only through `flexinfer-proxy`. The proxy's
200m CPU limit throttles it in 14–26% of periods, and that adds 0.5–10 s tails unrelated to
the card.

Evidence: `.loom/60-validation-matrix.md` row `qwen3-embedding-gfx906-2026-10-24`.

### Required Environment Variables

These are set in the `gfx906` GPUProfile (`deploy/gpuprofiles/gfx906.yaml`):

```bash
HSA_ENABLE_SDMA=0                 # DMA stability on Vega20
HSA_USE_SVM=0                     # hipMemGetInfo workaround (ROCm/ROCm#2433)
PYTORCH_ROCM_ARCH=gfx906         # Build target
TORCH_BLAS_PREFER_HIPBLASLT=0    # Not available on GCN5
MIOPEN_FIND_MODE=2               # Reduce MIOpen overhead
GPU_MAX_HW_QUEUES=1              # 3 compute queues per process; the card has 24 (see above)
```

`HSA_OVERRIDE_GFX_VERSION` stays **unset**, with the key absent. KFD reports the card as
gfx906 (`gfx_target_version 90006`), so ROCr already picks the gfx906 code objects and the
override buys nothing. An empty inherited value is worse than none: ROCr still reads it
and `hipInit` can return `hipErrorNoDevice`. That is why `build/runtime-entrypoint.sh`
removes the key when `GPU_ARCH=gfx906`. On 2026-10-30 the live runtime pod, the
runtime-manager process and all four llama-server subprocesses ran without the key.
`9.0.6` equals the native target, so an image or script that still sets it is harmless; the
pyannote Deployment and the GPTQ job script (after `rocminfo` reports gfx906) are examples.
The old "reports as gfx900" note was wrong for this card on ROCm 6.3.

## PyTorch + Transformers Compatibility Matrix

### The Core Constraint

transformers >= 5.0 **hard-requires PyTorch 2.4+**. This was a deliberate decision
([transformers #43508](https://github.com/huggingface/transformers/issues/43508)).
There is no workaround or compatibility shim.

### Available Combinations

| ROCm | PyTorch | transformers | gfx906 GPU works? | Source |
|------|---------|-------------|-------------------|--------|
| 5.7 | 2.0-2.1 | < 5.0 | Yes (full support) | Official |
| 6.1 | 2.4.1 | >= 5.0 | **Likely yes** (untested) | Official wheel: `torch==2.4.1+rocm6.1` |
| 6.2.3 | 2.3.0 | < 5.0 only | Yes (with HSA override) | `Dockerfile.quantizer-gptq-rocm-gfx906` (removed 2026-06-12; recipe in git history) |
| 6.4.1 | 2.6.0 | >= 5.0 | **No** (GPU alloc broken) | `Dockerfile.quantizer-gptq-rocm64-gfx906` |
| 6.3-7.2 | 2.7-2.8 | >= 5.0 | **Likely yes** (community) | mixa3607/ML-gfx906 |

### Current FlexInfer Images for gfx906

| Image | Base | PyTorch | transformers | GPU? | Use case |
|-------|------|---------|-------------|------|----------|
| `quantizer:gptq-rocm-gfx906` | ROCm 6.2.3 | 2.3 | < 5 | Yes | GPTQ quant (non-Qwen3.5 models) |
| `quantizer:gptq-rocm64-gfx906` | ROCm 6.4.1 | 2.6 | >= 5 | **CPU only** | Abliteration/quant for Qwen3.5+ |
| `runtime:rocm-gfx906` | ROCm 6.2.3 | 2.3 | N/A | Yes | vLLM inference |

## Upgrade Paths to Restore GPU on gfx906

### Option A: PyTorch 2.4.1 + ROCm 6.1 (Recommended)

```dockerfile
FROM rocm/pytorch:rocm6.1_ubuntu22.04_py3.10_pytorch_release_2.4.1
# OR install via pip:
# pip install torch==2.4.1 --index-url https://download.pytorch.org/whl/rocm6.1
```

- **Pro**: Official AMD wheel, gfx906 likely still in default build targets
- **Pro**: PyTorch 2.4 satisfies transformers>=5.0 requirement
- **Con**: ROCm 6.1 is older; fewer bug fixes
- **Con**: hipMemGetInfo still broken (need monkey-patch for `torch.cuda.mem_get_info`)
- **Status**: Untested. Validate `torch.empty(device="cuda")` works before adopting.

### Option B: mixa3607/ML-gfx906 Community Images (ADOPTED)

[github.com/mixa3607/ML-gfx906](https://github.com/mixa3607/ML-gfx906) — active
community project providing Docker images with PyTorch 2.7-2.11 on ROCm 6.3-7.2,
all built with gfx906 targets. Rebuilds rocBLAS+Tensile from source for gfx906.

- **Pro**: Latest PyTorch + ROCm, actively maintained (130 stars, last commit 2026-03-15)
- **Pro**: Includes vLLM 0.12, llama.cpp, ComfyUI images
- **Pro**: ROCm 6.3.3 is the last ROCm with shipped Tensile libs for gfx906
- **Pro**: GPU compute confirmed working by community
- **Con**: Community builds, not AMD-supported
- **Status**: **Adopted** as base for unified gfx906 image. See `build/Dockerfile.unified-gfx906`.
- **Available images**: `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3` (recommended)
- FlexInfer's `build/Dockerfile.vllm-rocm-gfx906` does not consume a prebuilt
  community vLLM runtime. It keeps a custom source-build runtime and uses the
  official `rocm/vllm-dev:base` image, pinned by digest, only as a coherent
  HIP/CMake/PyTorch build environment. The Dockerfile also patches vLLM 0.7.3's
  HIP-side `WARP_SIZE` macro to the gfx906 wavefront size (`64`) so ROCm 7.x can
  compile the MoE kernels.

### Option C: nlzy/vllm-gfx906 (Archived)

[github.com/nlzy/vllm-gfx906](https://github.com/nlzy/vllm-gfx906) — modified vLLM
for gfx906 with custom triton-gfx906. Reports 43-92 tok/s on MI50 (same gfx906 arch).
**Archived Feb 2026** — use as reference only.

## Current Workaround: CPU-Only on radeonvii

For workloads requiring transformers>=5.0 (e.g., Qwen3.5 abliteration), we run
CPU-only on radeonvii's 128GB RAM:

- `device_map=cpu` via `FLEXINFER_ABLITERATION_DEVICE_MAP=cpu`
- `useGPU: false` in ModelCache abliteration spec
- ~10s/sample for 27B forward passes (vs ~27s/sample GPU on gfx1100)
- 128 harmful + 128 harmless samples = ~45 min activation collection
- Total abliteration including save: ~55 min

This is acceptable for abliteration (one-time operation per model) but would be
too slow for inference or iterative quantization calibration.

## Inference on gfx906

For inference, gfx906 **works** with:
- **vLLM** through FlexInfer's custom source-build runtime image — the
  Dockerfile builds the pinned vLLM wheel with `BUILD_FA=0` from the official
  ROCm vLLM development base
- **llama.cpp** with `patch-hipmemgetinfo.sh` for GGUF models
- **Ollama** with patched llama.cpp backend
- **Diffusers** (SDXL inpainting) with bitsandbytes built from source for gfx906

The GPU is functional for inference workloads that don't require transformers>=5.0
or `torch.cuda.mem_get_info()` during loading.

## Unified Runtime Image (2026-03-20)

A unified image replaces the 3 separate gfx906 images with a single image based on
[mixa3607/ML-gfx906](https://github.com/mixa3607/ML-gfx906) community builds.

**Base**: `mixa3607/pytorch-gfx906:v2.9.0-rocm-6.3.3`
- ROCm 6.3.3 is the last version with pre-compiled Tensile libraries for gfx906
- mixa3607 rebuilds rocBLAS+Tensile from source, restoring GPU compute on ROCm 6.3+
- PyTorch 2.9 satisfies `transformers>=5.0` (needed for Qwen3.5, Llama 4, etc.)
- GPU compute confirmed working by community (130+ stars, last commit 2026-03-15)

**Dockerfile**: `build/Dockerfile.unified-gfx906`
**Image**: `registry.harbor.lan/flexinfer/runtime:unified-gfx906`
**Build**: `make build-unified-gfx906`

Since 2026-10-30 the image no longer bakes `HSA_OVERRIDE_GFX_VERSION=9.0.6` (see
[Required Environment Variables](#required-environment-variables)). No GPUProfile pins this
image any more: abliteration moved to the gfx906 runtime image on 2026-10-23.

**Replaces**:
| Old Image | Purpose | Status |
|-----------|---------|--------|
| `quantizer:gptq-rocm-gfx906` | GPTQ quantization (ROCm 6.2.3) | Superseded |
| `quantizer:gptq-rocm64-gfx906` | Qwen3.5 CPU-only quantization (ROCm 6.4.1) | Superseded |
| `diffusers:rocm-gfx906` | Diffusers inference | Superseded |

**Capabilities in unified image**:
- GPTQ quantization (GPU-accelerated, transformers >=5.0)
- Abliteration (GPU-accelerated with monkey-patched `hipMemGetInfo`)
- Diffusers inference (SDXL, FLUX) with bitsandbytes NF4 from source
- Modern model support (Qwen3.5, Llama 4, all architectures requiring transformers 5.x)

**GPU validation required**: The build script auto-detects whether GPU compute works
at runtime (`torch.empty(device="cuda")` test) and falls back to CPU-only if it fails.

**Key risk**: Depends on community-maintained base image. Mitigation: mixa3607 build
scripts are public and self-contained; can be forked if maintenance stops.

## Decision Log

| Date | Decision | Rationale |
|------|----------|-----------|
| 2026-10-30 | Enforce the 24-queue HWS budget: GPUProfile `maxHWQueues: 24` / `reservedHWQueues: 2`, per-Model `hwQueueEstimate` | VRAM admission cannot see queues; past 24 the runlist oversubscribes and co-resident lanes stall |
| 2026-10-30 | `HSA_OVERRIDE_GFX_VERSION` unset everywhere on gfx906, including the unified image | KFD reports gfx906 natively; the live runtime runs without the key; an empty value hides the GPU |
| 2026-03-20 | Unified gfx906 image based on mixa3607 | Restores GPU compute + transformers>=5.0 in single image |
| 2026-03-20 | CPU-only abliteration on gfx906 | GPU alloc broken on ROCm 6.4; 128GB RAM makes CPU viable |
| 2026-03-20 | Use `gptq-rocm64-gfx906` image | Only image with transformers>=5.0 for Qwen3.5 support |
| 2026-03-08 | Keep `gptq-rocm-gfx906` (ROCm 6.2.3) | GPU works for non-Qwen3.5 GPTQ quantization |
