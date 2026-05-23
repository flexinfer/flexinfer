# Spec: gfx906 llama.cpp production lane

Date: 2026-05-20
Status: draft
Branch: `docs/gfx906-llamacpp-production-spec`
Author: ralph-loop / 2026-05-20 iteration

## Context

The 2026-05-20 RALPH iteration's embedding-op feasibility probe (MR !456,
evidence `.loom/ralph-gfx906-vllm-embedding-probe-evidence-2026-05-20.md`,
MR !457) proved that vLLM on gfx906 (Vega20 / Radeon VII) is structurally
blocked at the ROCm-kernel layer. Five of six HIP scenarios — including
the smallest possible HIP tensor allocation — segfault. The broken op
family includes `at::native::zero_kernel_cuda` and the HIP RNG float16
path, both invoked through C++ fused paths that bypass Python-level
`Tensor.fill_` / `Tensor.zero_` monkey-patches. Closing the gap would
require patches in the PyTorch C++ dispatcher or the ROCm runtime, out
of RALPH-slice scope on a stack that has been in ROCm maintenance mode
since Q3 2023.

This spec formalizes the strategic pivot: **production gfx906 inference
substrate is llama.cpp**. The vLLM canary remains in the validation
matrix at row 175 as a future ROCm-kernel-bisect reference, with status
`feasibility-only`.

The substrate is not net-new. `qwen3-8b-radeonvii` (`backend: llamacpp`,
`deploy/models/qwen3-8b-radeonvii.yaml`) already serves traffic against
the `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3` image,
built from `build/Dockerfile.llamacpp-rocm-gfx906`. This spec scopes the
work to **promote** the existing lane from sidecar to production posture
and to close out the vLLM canary's experimental status.

## Riskiest assumption + kill-test

**Load-bearing assumption**: `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3`
remains stable when the Radeon VII is the **sole** textgen substrate on
gfx906 — i.e. when the runtime DaemonSet promotes llama.cpp from
co-tenant (alongside SDXL inpainting and the vLLM canary at
`minReplicas: 0`) to primary tenant carrying the radeonvii textgen
share of fast-chat / quality-chat traffic in steady-state.

**Kill test**: serve a 24-hour soak on a single warm llama.cpp model
(`qwen3-8b-radeonvii` or a sibling) at the steady-state QPS the
gfx906 lane is expected to absorb (≤4 concurrent decode streams,
GGUF Q4_K_M, ≤8 GiB VRAM), measuring:
1. zero CrashLoopBackOff cycles on the model pod;
2. p95 decode latency stays within a documented envelope (e.g. ≤300 ms
   per token at C=1 with the Q4_K_M quant);
3. SDXL inpainting lane (the gfx906 image-gen co-tenant) remains Ready
   throughout — no GPU contention regression vs the current layout.

**Failure mode if the assumption is wrong**: a steady-state contention
mode (VRAM, HSA queue, or scheduler-level) only surfaces under
sustained dual-workload load. Mitigation: run the soak BEFORE removing
the vLLM canary row from `minReplicas: 0`, so the canary's pinned
image-pull-policy doesn't compete with the soak. If the soak fails,
the recommended remediation is GPU group partitioning (similar to
`5930k-imagegen-textgen` group on cblevins-5930k) rather than
re-introducing vLLM.

**Status**: pre-soak memory-info, model-load, and standalone 24h soak gates are
unblocked by the shimmed llama.cpp image. A 2026-05-21 temporary
`qwen3-8b-radeonvii` canary first reached llama.cpp startup and detected the
Radeon VII, then aborted during GPU-backed model load at
`ggml_backend_cuda_device_get_memory` / `hipMemGetInfo(free, total)`. The
standalone probe in
`deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml` reproduced raw
`hipMemGetInfo=1:invalid argument` in all four tested env variants. MR !467 then
landed `registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`,
which converts that raw ROCm failure into sysfs VRAM totals. The shimmed image
passed both the standalone HIP probe and a Qwen3 8B GGUF model-load smoke on
`cblevins-radeonvii` (`SMOKE_RESULT PASS`, 81.1 tok/s short generation). The
24h standalone soak then completed successfully on 2026-05-22 with both
containers exit `0` and zero restarts. Alias promotion remains blocked until a
persistent `gfx906` runtime image carries the shim and a proxy-backed soak
persists final summary evidence.

## Scope

### In-scope

1. **Validation soak** (one slice): a 24-hour bench against a warm
   llama.cpp model on `cblevins-radeonvii` with continuous low-QPS
   traffic, measuring the metrics in the kill-test section. Output:
   one new row in `.loom/60-validation-matrix.md` capturing the soak
   verdict.
2. **Promote `qwen3-8b-radeonvii` or a sibling to fast-chat alias
   coverage** (one slice): wire the radeonvii llama.cpp model into the
   service-label alias map so fast-chat / quality-chat routes
   shed-test to gfx906 in a controlled fashion. Concretely: extend
   the `serviceLabels:` block in `deploy/models/qwen3-8b-radeonvii.yaml`
   to include the `default-chat-fallback` alias and document the alias
   table in `.loom/60-validation-matrix.md`.
3. **Formalize vLLM canary closeout** (one slice): a small docs MR
   that:
   - sets row 175's status to `feasibility-only` (already done in
     MR !457; this slice ratifies the language in the spec);
   - adds a `## Closeout posture` block to
     `deploy/models/qwen3-1p7b-vllm-radeonvii.yaml` explaining that
     the file stays for future ROCm-kernel-bisect work but is not
     scaled above `minReplicas: 0`;
   - moves the prior RALPH evidence docs into a `.loom/archive/`
     subtree after the soak slice has consumed them as references.

### Out-of-scope

- **Building a new llama.cpp image** for gfx906. The existing
  `rocm-gfx906-patched-v3` tag is the substrate. Patches accumulate
  on top via the existing `Dockerfile.llamacpp-rocm-gfx906` chain.
- **Adding new model architectures** to the radeonvii lane beyond
  what GGUF supports cleanly on gfx906 today (Qwen2/3 dense, Mistral
  7B family, Llama 3.x dense). MoE and ultra-long-context models
  belong on gfx1100 / 7900 XTX warm primaries.
- **Bisecting the ROCm/PyTorch source tree** for the broken HIP fill
  kernel that makes vLLM unviable. Vega20 ROCm support is in
  maintenance mode (Q3 2023+); a source bisect is out of scope for
  this lane.
- **The 5930k or 7900xtx warm primaries.** This spec is gfx906 only.

## Architecture

```
  ┌─ cblevins-radeonvii (Radeon VII, Vega20, 16 GiB) ─────────────────┐
  │                                                                    │
  │   ┌─ flexinfer-runtime-gfx906 (DaemonSet) ──────────────────────┐  │
  │   │   Image: registry.harbor.lan/flexinfer/runtime              │  │
  │   │          @sha256:<runtime-pin>                              │  │
  │   │   Per gfx906.yaml gpuprofile                                │  │
  │   └─────────────────────────────────────────────────────────────┘  │
  │                                                                    │
  │   ┌─ llama.cpp textgen lane (THIS SPEC) ────────────────────────┐  │
  │   │   Image: library/llamacpp:rocm-gfx906-patched-v3            │  │
  │   │   Source: build/Dockerfile.llamacpp-rocm-gfx906             │  │
  │   │   Patches: hipMemGetInfo fallback (VMM not supported on    │  │
  │   │             Vega20), HSA_USE_SVM=0                          │  │
  │   │   Models: qwen3-8b-radeonvii (Q4_K_M, ~5 GiB VRAM)          │  │
  │   │           + 1-2 future siblings                             │  │
  │   │   Aliases: default-chat-fallback (new in this spec)         │  │
  │   └─────────────────────────────────────────────────────────────┘  │
  │                                                                    │
  │   ┌─ SDXL inpainting lane (existing, co-tenant) ────────────────┐  │
  │   │   flux-fill-inpainting / FLUX Schnell                        │  │
  │   │   ~6-8 GiB VRAM                                              │  │
  │   └─────────────────────────────────────────────────────────────┘  │
  │                                                                    │
  │   ┌─ vLLM canary (feasibility-only, minReplicas=0) ─────────────┐  │
  │   │   qwen3-1p7b-vllm-radeonvii                                  │  │
  │   │   Image: vllm:rocm-gfx906@sha256:2139c92b… (frozen)          │  │
  │   │   Stays for future ROCm-kernel-bisect work; NOT in traffic   │  │
  │   └─────────────────────────────────────────────────────────────┘  │
  │                                                                    │
  └────────────────────────────────────────────────────────────────────┘
```

The llama.cpp lane sits beside the existing SDXL inpainting lane. The
vLLM canary is frozen at `minReplicas: 0`. The image and Dockerfile
chain are already in production — this spec is about workload
promotion, not infrastructure construction.

## Slice breakdown

Order is gated by the pre-soak memory-info probe and then by the kill-test:

### Slice 0 — HIP memory-info isolation (pre-soak gate)

1. Apply `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`.
2. Capture per-case results for:
   - current profile env;
   - no `HSA_OVERRIDE_GFX_VERSION`;
   - `ROCR_VISIBLE_DEVICES=0` only;
   - `HIP_VISIBLE_DEVICES=0` plus `GPU_DEVICE_ORDINAL=0`.
3. Acceptance: either one env variant returns clean `hipMemGetInfo` and
   `hipMalloc` results, or the failure is classified as a llama.cpp image /
   ROCm compatibility bug before another model-load retry is attempted.

Verdict: PASS after shim. The original `rocm-gfx906-patched-v3` image returned
`hipMemGetInfo=1:invalid argument` after successful device discovery. The
shimmed image returns clean `hipMemGetInfo`, `hipMalloc4096`, and post-malloc
`hipMemGetInfo` results by falling back to sysfs VRAM totals when raw ROCm
returns `err=1`.

Evidence doc: `.loom/ralph-gfx906-llamacpp-meminfo-probe-2026-05-21.md`.

### Slice 0a — Model-load smoke on shimmed image

1. Run a one-off debug Job on `cblevins-radeonvii` with the shimmed standalone
   llama.cpp image and the node-local Qwen3 8B GGUF mounted from
   `/var/lib/flexinfer/models`.
2. Load `/models/flexinfer-system/qwen3-8b-radeonvii/Qwen3-8B-Q4_K_M.gguf`
   with `--gpu-layers 999`, `--flash-attn on`, `--cache-type-k q4_0`, and
   `--cache-type-v q4_0`.
3. Acceptance: llama.cpp reaches model memory breakdown, generates a short
   response, exits `0`, and the pre-existing CPU-fallback router path still
   smokes afterward.

Verdict: PASS. The shimmed image loaded Qwen3 8B on the Radeon VII, printed
ROCm memory breakdown (`model=4455 MiB`, `context=324 MiB`, `compute=304 MiB`),
generated a short response at `81.1 t/s`, and exited `SMOKE_RESULT PASS`.
Restore smoke against `qwen3-1p7b-tools-radeonvii` returned `Blue` at
`69.51 tok/s`.

Evidence doc: `.loom/ralph-gfx906-llamacpp-model-load-smoke-2026-05-21.md`.

### Slice 1 — Soak validation (kill-test, ~24h wall clock)

Conditional on Slice 0 and Slice 0a producing a viable memory-info and
model-load path.

1. Pick the soak target. Current default: the debug-only sibling
   `qwen3-8b-radeonvii-soak`, defined in
   `deploy/debug/gfx906-llamacpp-proxy-soak-target.yaml`, so the live gate can
   use `gpu.forcePromotion: true` without permanently raising the production
   `qwen3-8b-radeonvii` priority or promoting aliases. Alternate: a
   freshly-pulled Qwen2.5-7B-Instruct Q4_K_M if the team wants to validate a
   new model line.
2. Set up a low-rate continuous traffic generator (e.g. a small `Job`
   that issues one `/v1/chat/completions` call per minute against the
   model's proxy alias) and arm Prometheus to alert on
   `rate(model_pod_restart_total[5m]) > 0` for the target.
3. Let it run for 24 hours. Capture: pod-restart count, p95 decode
   latency, GPU VRAM peak, SDXL inpainting lane uptime.
4. Acceptance: zero pod restarts, latency envelope honored, no
   inpainting regression. If any criterion fails, abort the
   promotion and open a remediation slice (GPU group partitioning
   etc.).

Verdict: PASS for the standalone shimmed-image kill-test. Job
`gfx906-llamacpp-soak-traffic` ran from 2026-05-21T18:40:23Z to
2026-05-22T18:43:42Z, with both the `server` and `traffic` containers exiting
`0` and restart count `0`. The traffic script exits nonzero on request
failures, missing p95 data, or p95 above `300 ms/token`, so exit `0` proves the
latency envelope. Final logs were not retrievable from Kubernetes after
completion, so the exact final p95 was not harvested; the next proxy-backed
soak must persist its summary to a ConfigMap or PVC.

Evidence doc: `.loom/ralph-gfx906-llamacpp-soak-2026-05-21.md`.

Proxy-backed follow-up status: blocked once by cross-family runtime unload, then
blocked again by same-group priority arbitration. The next attempt must first
run the activation preflight from
`.loom/ralph-gfx906-proxy-soak-activation-gate-2026-05-23.md`.

### Slice 2 — Alias promotion (small docs/manifest MR)

Conditional on Slice 1 PASS and a proxy-backed soak using the persistent
`gfx906` runtime image with the hipMemGetInfo shim. The standalone soak proved
the image and hardware path, but not the controller/proxy runtime path.

1. Edit `deploy/models/qwen3-8b-radeonvii.yaml` to add
   `default-chat-fallback` to `serviceLabels`.
2. Update `.loom/60-validation-matrix.md` row for `qwen3-8b-radeonvii`
   with the new alias and the soak evidence link.
3. Update `docs/planning/fast-chat-resilience.md` to reflect the new
   fallback ordering: primary 7900 XTX, secondary 5930k, tertiary
   radeonvii-llamacpp.
4. Flux reconciles, alias takes effect within minutes.

### Slice 3 — vLLM canary closeout (docs MR)

Conditional on Slice 2 merge.

1. Add a `## Closeout posture` block to
   `deploy/models/qwen3-1p7b-vllm-radeonvii.yaml` documenting:
   - the canary stays for future ROCm-kernel-bisect work
   - `minReplicas: 0` is intentional and not subject to autoscaling
   - the embedding-probe evidence is the closing reference
2. Move the eight `.loom/ralph-gfx906-vllm-*` evidence docs into
   `.loom/archive/2026-05-20-gfx906-vllm-feasibility/` after the
   closeout MR merges. (`.loom/archive/` is gitignored locally but
   the canonical archive lives under `~/workspace/.loom/local/...`
   per the workspace AGENTS.md policy.)
3. Add a one-line memory entry summarizing the lane closeout: "gfx906
   production substrate = llama.cpp; vLLM frozen at minReplicas=0".

## Acceptance criteria (per-spec, not per-slice)

The spec is satisfied when:

1. Slice 1's soak PASSES the metrics above with documented evidence.
2. Slice 2's alias is live in the validation matrix and in
   `fast-chat-resilience.md`.
3. Slice 3's closeout block is in the vLLM canary manifest.
4. `MEMORY.md`'s gfx906 line reflects the production posture:
   "production gfx906 substrate = llama.cpp; vLLM canary
   feasibility-only".

## Risks + mitigations

| Risk | Likelihood | Mitigation |
|------|------------|-----------|
| Llama.cpp ROCm patch regression in a future image rebuild | low | The `rocm-gfx906-patched-v3` tag is frozen via digest pin in `deploy/system/values-k3s.yaml`; future bumps go through the existing promote-runtime-digest.sh flow which validates `--help` output and runs the kill-test. |
| Soak surfaces a sustained-load GPU regression | medium | Per the kill-test plan, soak runs before alias promotion. Failure aborts promotion and opens a remediation slice; the canary stays frozen. |
| HIP `hipMemGetInfo` failure mode evolves with new model loads | medium | The Dockerfile's `patch-hipmemgetinfo.sh` fallback handles the known case (VMM not supported on Vega20). Any new failure mode would surface in Slice 1 soak as a pod restart and trigger remediation. |
| User wants Whisper / multimodal on gfx906 | low | Out-of-scope; multimodal stays on gfx1100 (where the parallel `mistral_common` bump slice unblocks Whisper). The gfx906 lane is dense-text only. |

## References

- Embedding-probe evidence (closes the vLLM gfx906 viability question):
  `.loom/ralph-gfx906-vllm-embedding-probe-evidence-2026-05-20.md`.
- Existing llama.cpp gfx906 substrate: `build/Dockerfile.llamacpp-rocm-gfx906`.
- Production image pin: `deploy/system/values-k3s.yaml:203` and
  `deploy/gpuprofiles/gfx906.yaml:55` — both reference
  `registry.harbor.lan/library/llamacpp:rocm-gfx906-patched-v3`.
- Active llama.cpp gfx906 model: `deploy/models/qwen3-8b-radeonvii.yaml`.
- vLLM canary (to be closed out): `deploy/models/qwen3-1p7b-vllm-radeonvii.yaml`.
- Validation matrix row 175 (vLLM canary, now `feasibility-only`):
  `.loom/60-validation-matrix.md`.
- Memory: `gfx906-vllm-fill-segfault.md` (root-cause + C++-vs-Python
  boundary finding).
- Workspace policy on archive: `services/flexinfer/CLAUDE.md` →
  "Local Loom Planning Artifacts Policy".
