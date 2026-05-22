# RALPH: gfx906 llama.cpp 24h Soak Setup

Date: 2026-05-21

## Review

- Roadmap milestone: Lane 1, Slice 1B from the roadmap-unblock plan: controlled
  24 hour llama.cpp soak before Radeon VII alias/default fallback promotion.
- Spec section: `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md`
  Slice 1.
- Prior decisions to preserve:
  - `gfx906` production textgen substrate is llama.cpp, not vLLM.
  - The shimmed image
    `registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`
    passed the standalone HIP memory-info gate and the Qwen3 8B model-load
    smoke.
  - Do not add `default-chat-fallback` or any broad chat alias until the soak
    passes.

## Align

- Slice name: `gfx906` llama.cpp soak setup.
- Scope in:
  - Add a debug manifest that runs the shimmed standalone llama.cpp image on
    `cblevins-radeonvii`, mounts the existing node-local Qwen3 8B GGUF cache,
    starts `llama-server`, and drives it for 24 hours.
  - Add a 24 hour low-rate traffic loop that sends one deterministic
    `/v1/chat/completions` request per minute and exits non-zero on request
    failure or p95 decode latency above 300 ms/token.
  - Document harvest and rollback commands in the manifest comments.
- Scope out:
  - No fallback/default alias promotion.
  - No GPUProfile or Helm profile image promotion.
  - No FlexInfer proxy/controller serving soak yet; a first live attempt showed
    that per-model `spec.image` does not affect the persistent
    `flexinfer-runtime-gfx906` direct-load path, so runtime-image promotion must
    precede a proxy-backed soak.
  - No vLLM canary closeout.
- Acceptance criteria:
  - The standalone soak artifact can be applied with one `kubectl apply`.
  - The traffic Job records per-request latency/token data as JSON lines and a
    final summary.
  - Follow-up evidence collection covers request failures, pod restarts/events,
    and the `sdxl-inpainting-radeonvii` co-tenant baseline.
- Dependencies/blockers:
  - Live cluster access to `cblevins-radeonvii`.
  - Existing local Qwen3 8B GGUF cache at
    `/var/lib/flexinfer/models/flexinfer-system/qwen3-8b-radeonvii/`.
  - Agent-context MCP was unavailable during setup, so harvest notes must be
    recorded once the MCP route recovers.
  - Proxy-backed soak remains blocked until the persistent `gfx906` runtime
    image carries the hipMemGetInfo shim or llama.cpp is forced onto a
    standalone backend Deployment path.

## Land

- Planned file areas:
  - `deploy/debug/gfx906-llamacpp-soak.yaml`
  - `.loom/ralph-gfx906-llamacpp-soak-2026-05-21.md`
- Implementation steps:
  1. Add the standalone llama.cpp server plus traffic generator Job.
  2. Validate YAML rendering with `kubectl --dry-run=client`.
  3. Apply the manifest to start the 24 hour standalone image soak.

## Live Attempt Note

An initial proxy-backed attempt created a temporary `qwen3-8b-radeonvii` Model
with `spec.image` pointing at the shimmed standalone image. That failed before a
usable soak began: the controller still sent the load request to the persistent
`flexinfer-runtime-gfx906` pod, whose image is
`registry.harbor.lan/flexinfer/runtime@sha256:cbe1157c2fb6a24fc67e901bec92a72bbf16498a86ad1a064ce9bf4db1f2ddf4`
and does not include the shim. The runtime wedged during the handoff from
`qwen3-1p7b-tools-radeonvii`; recovery deleted the temporary Model/Job,
recycled `flexinfer-runtime-gfx906`, and lowered the live vLLM canary priority
to `100` so the tool-router fallback can reclaim the shared group.

## Standalone Soak Start

The corrected standalone soak was applied on 2026-05-21. Job
`gfx906-llamacpp-soak-traffic` scheduled on `cblevins-radeonvii`, pulled
`registry.harbor.lan/library/llamacpp:rocm-gfx906-hipmem-shim@sha256:79cc4eb24c5260e835637b9de34d93b58b74f03dc9826056a1bea22d566a3407`,
mounted `/var/lib/flexinfer/models`, and started two containers:

- `server`: shimmed `llama-server` with `Qwen3-8B-Q4_K_M.gguf`,
  `--n-gpu-layers 999`, `--flash-attn on`, and q4_0 KV cache.
- `traffic`: one deterministic OpenAI-compatible chat request per minute for
  24 hours.

Initial evidence:

- server loaded Qwen3 8B and printed ROCm memory breakdown:
  `ROCm0 model buffer size = 4455.34 MiB`,
  `ROCm0 KV buffer size = 324.00 MiB`,
  `ROCm0 compute buffer size = 304.75 MiB`.
- first traffic request returned HTTP 200 with 64 completion tokens and
  `15.716 ms/token`; this request is marked warmup and excluded from the final
  p95.
- follow-up heartbeat automation: `check-gfx906-llama-cpp-soak`.

## Prove

- Tests to run:
  - `kubectl apply --dry-run=client -f deploy/debug/gfx906-llamacpp-soak.yaml`
  - `git diff --check`
- Live validation:
  - `kubectl apply -f deploy/debug/gfx906-llamacpp-soak.yaml`
  - `kubectl -n flexinfer-system get job gfx906-llamacpp-soak-traffic`
  - `kubectl -n flexinfer-system logs job/gfx906-llamacpp-soak-traffic`

## Handoff/Harvest

### Final harvest (2026-05-22)

The standalone soak completed successfully.

Kubernetes status:

- Job `gfx906-llamacpp-soak-traffic`
  - `startTime`: 2026-05-21T18:40:23Z
  - `completionTime`: 2026-05-22T18:43:42Z
  - conditions: `SuccessCriteriaMet=True`, `Complete=True`
  - `succeeded`: 1
- Pod `gfx906-llamacpp-soak-traffic-brpcf`
  - phase: `Succeeded`
  - node: `cblevins-radeonvii`
  - `server` container: exit `0`, restart count `0`
  - `traffic` container: exit `0`, restart count `0`

Traffic-script contract:

- The script exits `20` on request failures.
- The script exits `21` if no measured p95 exists.
- The script exits `22` if p95 exceeds `300 ms/token`.
- Therefore traffic container exit `0` proves the soak had no recorded request
  failures and stayed inside the latency envelope.

Observed mid-run health:

- A 19h harvest showed attempts 981-1140 returning HTTP 200, 64 completion
  tokens, and approximately `13.6-13.8 ms/token`.

Co-tenant baseline at final harvest:

- `sdxl-inpainting-radeonvii`: `Idle`
- `qwen3-1p7b-tools-radeonvii`: `Ready`

Evidence caveat:

- Final completed-container logs were unavailable after completion; `kubectl
  logs` returned `unable to retrieve container logs for containerd://...`.
- This prevents recording the exact final `soak_summary.p95_ms_per_token`.
- Next proxy-backed soak must persist its summary to a ConfigMap or PVC before
  alias/default fallback promotion.

Decision:

- Standalone kill-test: PASS.
- Alias promotion remains blocked until a persistent `gfx906` runtime image
  carries the shim and a proxy-backed soak passes with durable summary storage.

Next slice:

- Build or promote a `gfx906` runtime image carrying
  `libflexinfer_hipmeminfo_shim.so`, then rerun a proxy-backed soak.
