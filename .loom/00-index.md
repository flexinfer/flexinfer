# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research: `10-research.md`
- Product spec: `20-product-spec.md`
- Implementation plan: `30-implementation-plan.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`
- Gemma4 26B/31B GPTQ + TurboQuant plan: `gemma4-26b-31b-gptq-turboquant-plan.md`
- Gemma4 31B TurboQuant closeout: `gemma4-31b-turboquant-closeout.md`
- Gemma4 31B TurboQuant memory fix plan: `gemma4-31b-turboquant-memory-fix-plan.md`
- gfx1100 quantization validation matrix: `60-validation-matrix.md`
- PR-2 readiness plan: `../docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md`
- Roadmap reconciliation checklist: `../docs/planning/roadmap-reconciliation.md`
- gfx1100/gfx906 platform enhancement spec: `gfx1100-gfx906-platform-enhancements-spec.md`
- gfx1100/gfx906 platform enhancement plan: `gfx1100-gfx906-platform-enhancements-plan.md`
- gfx1100/gfx906 next-round parallel plan: `gfx1100-gfx906-next-round-plan.md`

## Current Goal (2026-05-14)

Make the two-instance 26B fleet actually load-balance across both Ready backends. Today `internal/proxy/proxy.go:409` calls `internal/proxy/model_resolver.go:47:ResolveServiceLabel`, which returns `claimants[0]` (`r.serviceLabelCache.Load` — first-by-priority) per shared label. A 10-request probe through `quality-chat` on 2026-05-14 routed 10/10 to the 7900xtx instance. The infrastructure to fix this already exists — `refreshServiceLabelCache` populates `labelGroupCache` with ALL claimants per label and a `labelGroupModels` reverse index — but no caller uses it on the routing path.

- [ ] Pick a routing policy: round-robin / least-busy / weighted-by-priority. Record the decision in `40-decisions.md`.
- [ ] Implement the policy on top of `labelGroupCache` (new `ResolveServiceLabelGroup` returning a `[]string`, or a per-request picker) and wire it into the proxy routing path.
- [ ] Prove load-balancing: 20+ requests through `quality-chat` should split across `gemma4-26b-a4b-gptq` and `gemma4-26b-a4b-gptq-5930k` according to the chosen policy.
- [ ] Update both 26B Model CR comments to remove the "aspirational" caveat once the policy ships.
- [ ] Validation matrix row updated with policy + load-probe evidence.

## Previous Goal (2026-05-13/14, closed)

Promote `gemma4-26b-a4b-gptq` to the warm quality lane on `cblevins-7900xtx` so downstream services consume capable reasoning + 16K context via stable aliases (`quality-chat`, `mid-chat`, `gpt-4`, `project-mgmt`). Pair-demote `qwen3-8b-fast-7900xtx` to scale-to-zero so the 24 GiB lane is not double-claimed. Extended on 2026-05-13 to a two-instance fleet (sister `gemma4-26b-a4b-gptq-5930k` on `cblevins-5930k` via OCI pull). Closed 2026-05-14 with MR !352.

- [x] Land the manifest swap (priority 350 / minReplicas 1 / warmPolicy primary on 26B; mirror demotion on 8B) — MR !343.
- [x] Pipeline green + Flux reconcile shows `gemma4-26b-a4b-gptq` Ready and `qwen3-8b-fast-7900xtx` Idle — 2026-05-13.
- [x] Validation matrix row for the warm 26B canary updated with first served request evidence — `60-validation-matrix.md` row 120.
- [x] Two-instance fleet shipped (MR !345 fleet-reshape) and OCI artifact seeded (MR !350) so the sister Model can pull without re-running the 12-24 h pipeline.
- [x] Sister instance source-path mismatch fixed — MR !352. Both instances Ready, direct smoke passes on each, shared `service_labels` identical, node-specific `litellm.aliases` set. Evidence in `60-validation-matrix.md` row "gemma4-26b-a4b-gptq-5930k sister instance via OCI pull".
- [ ] (Deferred) Document service-side consumption pattern (services point at `quality-chat` or `project-mgmt` alias) — out-of-scope for the fleet build; depends on the load-balancing slice above before downstream services can rely on shared-alias capacity.

## Previous Goal (2026-05-06, round 1 closed)

Decompose the remaining `gfx1100`/`gfx906` work into eight tracks (A-H) sized for parallel sub-agent execution.

Round 1 status (first wave: A, E, F, H):

- [x] **Track F** — runtime profile generation decision (consistency-test-only). MR !273 merged.
- [x] **Track E** — validation matrix schema rotation. MR !274 merged.
- [x] **Track A** — GPUProfile contract slice (`ResolveBackendImage` helper). MR (`feat/gpuprofile-contract-slice`) merged via `6ba66e06`.
- [x] **Track H** — qwen36-27b-gptq coherence triage. Investigation report at `.loom/local/qwen36-coherence-triage.md`; matrix pointer added; concrete one-line fix queued at `deploy/modelcaches/qwen36-27b-gptq-gfx1100.yaml:87`.

Held for round 2:

- [ ] **Track B** — gfx906 disk-pressure unblock (operator pairs).
- [ ] **Track C** — gfx906 vLLM revive-or-retire (coordinate with `backlog/31-vllm-gfx906-build`).
- [ ] **Track D** — gfx1100 capability push (qwen36 dynamic-exclusion fix from H, 26B-long KV ceiling, FLUX warmups).
- [ ] **Track G** — fast-chat resilience after 5930k MLC fallback removal.

## Previous Goal (2026-05-06)

Plan the next full-platform feature round for AMD ROCm `gfx1100` and `gfx906`, spanning GPUProfile contracts, runtime image promotion, backend capability gates, validation evidence, and operator workflows.

- [x] Refresh `.loom/00-workspace-snapshot.md`.
- [x] Re-check Loom resource inventory and codebase index readiness.
- [x] Capture source-backed facts for `gfx1100` and `gfx906` runtime/profile behavior.
- [x] Add focused spec and implementation-plan artifacts for the next round.
- [x] Convert accepted slices into tracking issues/MRs (Tracks A, E, F, H first-wave shipped).

## Previous Goal (2026-05-03)

Close the remaining Gemma4/Qwen/TurboQuant gfx1100 queue through spec-driven gates. Keep closure evidence in `60-validation-matrix.md`, with runtime posture summarized in `gemma4-26b-31b-gptq-turboquant-plan.md` and `docs/dev/gemma4-rocm-status.md`.

Spec-driven delivery contracts SD-1 through SD-5 are now complete in tracked
planning docs. Future planning changes should use
`../docs/planning/roadmap-reconciliation.md` after merge so `ROADMAP.md`,
`docs/planning/next-roadmap.md`, `.loom/00-index.md`, and GitLab issues remain
aligned.

- [x] Slice A - 26B guardrails: long-canary dGPU selector is safe; 16K FP8-KV is the validated fallback posture and 22K remains partial/non-primary.
- [ ] Slice B - `26B-dense-rerun-gate`: dense-validated rebuild reached only harmful prompt 80/128 before the 4h abliteration deadline and still needs a rerun with the longer timeout plus cosine/runtime evidence.
- [x] Slice C - 31B rebuild/recovery: immediate GPTQ lane is back on the clean `keqv` artifact from !193/!194, Ready/Active at `maxModelLen: 2048`, and direct smoke returned HTTP 200 with answer `4`.
- [x] Slice D - 31B production posture: 31B is the primary warm model at the validated 2048 ceiling with `minReplicas: 1`, `priority: 250`, `gpu.count: 2`, and `warmPolicy: primary`.
- [ ] Slice E - `E4B-turboquant-runtime-probe`: primitive sharing is implemented and patch-idempotence tested, but E4B/31B/26B runtime canaries are still pending a digest-pinned built runtime image.
- [ ] Slice F - `31B-long-turboquant-posture-gate`: keep 31B long/TurboQuant disabled until E4B passes and the 31B canary reaches KV sizing without the previous plugin allocation OOM.
- [ ] Slice G - `Qwen35-9B-gfx1100-validation-gate`: staged manifest remains disabled; run the full gfx1100 ModelCache/validator/smoke path before promotion or retiring the gfx906 artifact.

See `gemma4-26b-31b-gptq-turboquant-plan.md`, `60-validation-matrix.md`, and `../docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md` for slice detail, acceptance gates, and evidence targets.

Historical baseline goals (completed):

- [x] Refresh `.loom/` templates and regenerate workspace snapshot.
- [x] Capture current MCP/runtime inventory with tool counts and constraints.
- [x] Validate `codebase_memory` indexing/search readiness and document blockers.
- [x] Produce an evidence-backed research/spec/plan set for next execution.

## Current State (2026-02-20)

- FlexInfer architecture baseline remains six cooperating executables (`agent`, `bench`, `manager`, `sched`, `global-proxy`, metrics embedded) and is documented in `AGENTS.md`.
- Workspace is on `master` at `d01972d` after MR !40 merge (`fad43a7`). Cold-start reliability fixes and dependency batches are all on `master`.
- MCP inventory is available through `loom` CLI fallback (`42` servers, `445` tools) because direct MCP resource listing returned empty sets.
- `codebase_memory` indexing is operational via `loom tools call` after collection repair + binary rebuild (`total_chunks=1877`).

### Cluster Model Fleet (2026-02-20)

| Model | Backend | Phase | GPU Pool | Serverless | Notes |
|-------|---------|-------|----------|------------|-------|
| `nomic-embed-text` | ollama | Ready | — | idle 30m | Embedding model, always warm |
| `qwen3-30b-a3b-abliterated` | llamacpp | Idle | amd-gpu-pool | idle 30m, cold 25m | MoE 18.7GB GGUF, local-path NVMe cache, 108 tok/s gen |
| `qwen3-8b-fast` | mlc-llm | Idle | 5930k-models | idle 5m, cold 10m | Dense 8B, NFS cache |
| `sdxl-turbo-imagegen` | diffusers | Ready | 7900xtx-image | idle 10m | Image generation, ROCm |

- Controller and proxy images deployed with cold-start fixes (Loading phase guard, conflict retry, GPUGroup per-model timeout).
- Proxy timeouts: `queue=25m`, `coldStart=25m` (via Helm values).

## Open Questions

- What is the preferred fix for `codebase_memory` failures:
  - new Qdrant collection (`CODEBASE_QDRANT_COLLECTION`) with expected vector schema, or
  - server-side point-id generation update to UUID/numeric?
- Should `repo_id=flexinfer` remain canonical, or should this workspace use a namespaced id (for example `services-flexinfer`) to avoid collisions?
- Do we want a "minimum viable MCP set" for planning tasks in this repo to reduce tool selection overhead?

## Risks

- Direct MCP bridge instability may block tool calls even when daemon-side tools are healthy.
- MCP inventory can drift; if not refreshed, plans may rely on unavailable tools.
- Planning docs can drift quickly after merge trains unless reconciliation notes are refreshed alongside backlog updates.

## Sources

- [S1] `AGENTS.md:7`
- [S2] `AGENTS.md:12`
- [S3] `AGENTS.md:13`
- [S4] `AGENTS.md:14`
- [S5] `AGENTS.md:15`
- [S6] `AGENTS.md:16`
- [S7] `.loom/00-workspace-snapshot.md:11`
- [S8] `.loom/00-workspace-snapshot.md:12`
- [C1] `loom servers --json | jq '.servers | length'` -> `42`
- [C2] `loom tools list --json --limit 500 --page 1 | jq '{server,page,pageSize,totalTools,totalPages,serverCount,cachedAt}'` -> `totalTools=445, totalPages=1`
- [C3] `loom tools call codebase_memory__codebase_index_poll --args '{"job_id":"1869e8aca6a0ab14"}' --json` -> `status: done, chunks_total: 1877`
- [C4] `loom tools call codebase_memory__codebase_stats --args '{"repo_id":"flexinfer"}' --json` -> `total_chunks: 1877`
