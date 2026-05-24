# Roadmap Unblock Plan

Date: 2026-05-21
Status: Ready for execution
Owner: Codex planning pass

## Goal

Turn the current blocked/churn-heavy feature queue into a small number of
ordered, evidence-gated roadmap lanes. The intent is to stop re-litigating
runtime failures, ship the next useful features in thin slices, and keep the
validation matrix as the source of truth.

## Riskiest assumption + kill-test

**Load-bearing assumption**: The remaining feature churn is mostly caused by
missing runtime/canary evidence, not by unresolved product direction. If the
first blocker can be converted into a concrete runtime kill-test, the rest of
the roadmap can move as small implementation slices.

**Kill test**: Complete Lane 1, Slice 1A: patch or replace the gfx906
llama.cpp image so `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`
returns a clean `hipMemGetInfo` and `hipMalloc` result for at least one
documented environment variant, then record the verdict in
`.loom/60-validation-matrix.md`.

**Failure mode if the assumption is wrong**: If the probe cannot be made to
pass without a deeper ROCm/Vega20 driver or llama.cpp rewrite, the gfx906
production fallback lane remains blocked and the roadmap should pivot to
gfx1100-only resilience rather than spending more slices on Radeon VII
production promotion.

**Status**: not passed. The 2026-05-21 pre-soak probe reproduced
`hipMemGetInfo=1:invalid argument` in all tested env variants, so Lane 1 is
currently blocked until the image-level compatibility issue is patched.

Sources:

- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:62`
- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:158`
- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:170`
- `.loom/60-validation-matrix.md:21`

## Facts Found

- FlexInfer's main roadmap says the core product is production-ready and open
  scope is now maintenance, next-slice selection, and delivery-process focus.
  Sources: `ROADMAP.md:11`, `ROADMAP.md:23`.
- The near-term roadmap still has concrete unchecked work: major Docker/base
  image updates, runtime validation matrix expansion, gfx906 conservative
  canaries, and gfx1100 text canaries. The old gfx1100 imagegen canary wording
  is stale because FluxPony now runs on Radeon VII. Sources:
  `docs/planning/next-roadmap.md:159`,
  `deploy/models/gonzalomo-fluxpony-imagegen.yaml`,
  `deploy/models/kustomization.yaml`.
- The validation matrix already defines the promotion contract: runtime image
  digest, hardware lane, backend, canary command, rollback digest/ref, evidence,
  and promotion decision. Sources: `.loom/60-validation-matrix.md:21`,
  `.loom/60-validation-matrix.md:44`.
- gfx906 vLLM is no longer a production candidate. The embedded probe showed
  HIP allocation/kernel failures below the Python monkey-patch layer, and the
  strategic pivot is llama.cpp for production gfx906 inference. Sources:
  `.loom/60-validation-matrix.md` row `qwen3-1p7b-vllm-radeonvii`,
  `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:10`,
  `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:22`.
- gfx906 llama.cpp is also not yet ready for promotion: the pre-soak gate failed
  at `hipMemGetInfo`, before the 24 hour soak can start. Source:
  `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:62`.
- The deploy/swap/tracing proof work is complete through PR-2A..PR-2C and is
  ready for live rollout validation on a selected gfx1100 model/node pair.
  Source: `docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md:229`.
- The next genuinely new feature opportunity is long-context runtime
  measurement: capture prefill, decode, VRAM slope, and failure points across
  several context sizes before adding controller enforcement. Sources:
  `.loom/brainstorm-long-context-architecture-runtime-2026-05-18.md:16`,
  `.loom/brainstorm-long-context-architecture-runtime-2026-05-18.md:74`.
- Current fast-chat production routing is already on two warm Gemma4 26B GPTQ
  7900 XTX instances; radeonvii should only enter as a later fallback after the
  gfx906 llama.cpp soak passes. Sources:
  `docs/planning/fast-chat-resilience.md:7`,
  `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:196`.
- Current image generation routing is Radeon VII-first: FluxPony
  (`gonzalomo-fluxpony-imagegen`) and `sdxl-inpainting-radeonvii` share
  `radeonvii-imagegen` on `cblevins-radeonvii`. There is no active reconciled
  gfx1100 imagegen canary until a 7900 XTX diffusers manifest is explicitly
  restored. Sources: `deploy/models/gonzalomo-fluxpony-imagegen.yaml`,
  `deploy/models/sdxl-inpainting-radeonvii.yaml`,
  `deploy/models/kustomization.yaml`.
- Tooling is good enough for execution: Loom full profile reports 51 servers
  and 514 tools, and `codebase_memory` for `repo_id=flexinfer` reports 2,831
  indexed chunks. Sources: `.loom/00-mcp-inventory.md`,
  command `mcp__loom__.codebase_memory__codebase_stats({"repo_id":"flexinfer"})`.

## Roadmap Lanes

### Lane 0 - Planning Hygiene and Backlog Freeze

Purpose: stop planning drift before feature work resumes.

Slices:

1. Make this file the active roadmap-unblock pointer in `.loom/00-index.md`,
   `.loom/20-product-spec.md`, and `.loom/30-implementation-plan.md`.
2. Keep `docs/planning/next-roadmap.md` as the public roadmap surface, but avoid
   broad rewrites until Lane 1 and Lane 2 resolve the first runtime facts.
3. Update `.loom/60-validation-matrix.md` only with evidence rows or row-status
   changes, not chat-derived optimism.

Acceptance:

- The context pack links to this plan.
- Each downstream slice names one owner boundary, one validation command set,
  and one rollback path.
- Roadmap reconciliation after each merge cites the exact changed plan/matrix
  rows.

Validation:

- `git diff --check`
- `rg 'Roadmap Unblock Plan|roadmap-unblock-plan' .loom`

### Lane 1 - Unblock gfx906 Production Fallback

Purpose: decide whether Radeon VII can be a production fallback lane through
llama.cpp, and close the vLLM churn loop.

Slice 1A: HIP memory-info compatibility fix.

- Target files/modules:
  - `build/Dockerfile.llamacpp-rocm-gfx906`
  - `deploy/debug/gfx906-llamacpp-hipmeminfo-probe.yaml`
  - `deploy/models/qwen3-8b-radeonvii.yaml` only if the image fix needs a
    manifest digest/tag update
  - `.loom/60-validation-matrix.md`
- Acceptance:
  - `hipMemGetInfo` and `hipMalloc` pass in at least one documented env variant.
  - The result is recorded in the matrix with the image digest/tag and rollback.
  - No model-load retry happens until the probe passes.
- Validation:
  - Apply/run the probe job.
  - Capture job logs under `.loom/local/validation/gfx906-llamacpp/<date>/`.
  - `git diff --check`.

Slice 1B: 24 hour llama.cpp soak.

- Conditional on Slice 1A passing.
- Acceptance:
  - Zero CrashLoopBackOff cycles.
  - p95 decode latency envelope recorded.
  - SDXL/imagegen co-tenant remains Ready.
  - `.loom/60-validation-matrix.md` gets a soak verdict row/update.
- Validation:
  - One low-rate traffic generator Job, plus pod restart/event scrape.

Slice 1C: alias promotion and vLLM closeout.

- Conditional on Slice 1B passing.
- Acceptance:
  - Add `default-chat-fallback` to the radeonvii llama.cpp lane.
  - Update `docs/planning/fast-chat-resilience.md` with fallback order:
    7900 XTX primaries, 5930k secondary, radeonvii tertiary.
  - Freeze `qwen3-1p7b-vllm-radeonvii` as feasibility-only/minReplicas 0.

### Lane 2 - Close Runtime Promotion Evidence

Purpose: finish RG-3/RG-4/RG-5 without letting canary attempts sprawl.

Slices:

1. Matrix coverage pass:
   - Ensure each required active lane has a current row: gfx1100 textgen,
     gfx906 textgen/quantization, and gfx906 imagegen/offload.
   - Keep the retired gfx1100 imagegen slot as an explicit `skip` row unless a
     real reconciled gfx1100 diffusers Model is restored.
   - Convert stale `TBD` fields into either evidence or explicit block reasons.
2. Radeon VII imagegen canary:
   - Pin/capture the diffusers runtime digest for
     `gonzalomo-fluxpony-imagegen`.
   - Capture 512x512 generation timing through the proxy. Treat 1024x1024 as
     optional headroom evidence, not part of the required gfx906 contract.
3. gfx1100 textgen canary closure:
   - Refresh Gemma4/Qwen rows that are still conditional because runtime digest,
     canary command, or rollback digest is missing.

Acceptance:

- No `promote` row has a missing digest, canary command, rollback path, or
  source link.
- Pending rows have a next command and a named blocker.
- No row may claim `gonzalomo-fluxpony-imagegen` as gfx1100 evidence while its
  manifest targets `cblevins-radeonvii`.
- `scripts/check-runtime-profile-consistency.sh` passes after any digest change.

Validation:

- `scripts/check-runtime-profile-consistency.sh`
- `scripts/test-promote-runtime-digest.sh`
- Targeted smoke commands captured in `.loom/local/validation/...`

### Lane 3 - Live Rollout Validation for Deploy/Swap Observability

Purpose: convert already-complete proof work into cluster evidence.

Slices:

1. Pick one gfx1100 model/node pair for rollout validation.
2. Render Helm with flash-loader and tracing values enabled in a non-default
   validation path.
3. Reconcile and capture cold-start/swap histograms plus tracing bootstrap
   signals.

Acceptance:

- `flexinfer_model_cold_start_duration_seconds` and
  `flexinfer_model_swap_duration_seconds` are observed in a live scrape.
- Tracing remains disabled by default and only enables through Helm/env.
- Rollback is documented as disabling Helm values or reverting the chart commit.

Validation:

- `go test ./pkg/metrics ./pkg/observability ./controllers -run 'ColdStart|Swap|Metric|Tracing|FlashLoader'`
- `helm template flexinfer ./charts/flexinfer --set controller.runtime.flashLoader.enabled=true --set observability.tracing.enabled=true --set observability.tracing.otlpEndpoint=http://otel-collector.observability:4318`

### Lane 4 - New Feature: Context-Curve Benchmark MVP

Purpose: add the next product capability without depending on uncertain model
architecture hype: measure the long-context curve first.

Slice 4A: spec capsule.

- Define an optional benchmark profile that records:
  - prefill throughput,
  - decode throughput,
  - VRAM/free-memory slope,
  - failure point,
  - context sizes such as 2k, 8k, 32k, and 128k when practical.

Slice 4B: reporting-only implementation.

- Extend benchmark result storage/reporting without changing scheduling
  decisions yet.
- Keep controller/scheduler enforcement out of scope until at least two model
  families have measured curves.

Acceptance:

- A reviewer can compare two model/runtime lanes by context curve, not a single
  TPS number.
- Results are stored in an auditable ConfigMap or matrix row linked from the
  validation matrix.
- Scheduler scoring is unchanged unless a later spec proves the data is stable.

Validation:

- Targeted benchmarker tests for result schema.
- One live benchmark on an existing Gemma4 or Qwen lane.

### Lane 5 - Major Dependency/Base Image Rollout

Purpose: resume the deferred major Docker updates only after runtime evidence is
stable enough to catch regressions.

Slices:

1. Python 3.14 image lane.
2. PyTorch 2.3/ROCm image lane.
3. CUDA 12.9 image lane.
4. MLC ROCm 6.4 image lane.

Ordering rule:

- Do not batch these together. One base/runtime family per MR, with a matching
  validation-matrix row or explicit "not runtime-affecting" rationale.

Acceptance:

- The relevant build job passes.
- Targeted Go tests and image-specific smoke tests pass.
- A rollback image digest or previous tag is recorded before promotion.

Validation:

- `make test`
- Image build job for the changed family.
- Runtime smoke for any lane whose serving image changes.

## Execution Order

1. Lane 0: link and freeze this plan.
2. Lane 1A: fix/prove gfx906 llama.cpp HIP memory-info compatibility.
3. Lane 2 matrix coverage pass, in parallel with no code changes while Lane 1A
   image work is prepared.
4. Lane 1B/1C if the kill-test passes; otherwise explicitly pivot gfx906 to
   non-production/support-only.
5. Lane 3 live observability rollout validation.
6. Lane 4 context-curve benchmark MVP.
7. Lane 5 major dependency rollout, one runtime/base family at a time.

## Open Questions

- Is radeonvii production fallback still worth pursuing if the llama.cpp image
  fix requires more than one patch/rebuild cycle?
- Which model should anchor the first context-curve benchmark: current Gemma4
  warm lanes, Qwen3 dense, or a new architecture-efficient candidate?
- Should major dependency rollouts wait for Lane 3 observability rollout so
  regressions have better live signals?

## Sources

- `ROADMAP.md:11`
- `ROADMAP.md:23`
- `docs/planning/next-roadmap.md:159`
- `docs/planning/next-roadmap.md:188`
- `.loom/60-validation-matrix.md:21`
- `.loom/60-validation-matrix.md:44`
- `.loom/60-validation-matrix.md` row `qwen3-1p7b-vllm-radeonvii`
- `.loom/60-validation-matrix.md` row `gonzalomo-fluxpony-imagegen`
- `docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md:229`
- `docs/planning/fast-chat-resilience.md:7`
- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:10`
- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:22`
- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:62`
- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:158`
- `.loom/spec-gfx906-llamacpp-production-lane-2026-05-20.md:196`
- `.loom/brainstorm-long-context-architecture-runtime-2026-05-18.md:16`
- `.loom/brainstorm-long-context-architecture-runtime-2026-05-18.md:74`
