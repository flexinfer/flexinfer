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

## Current Goal (2026-05-14, evening) — surface a 2.13x decode-rate gap on the 5930k upstream for explicit user direction

A matched-workload benchmark on 2026-05-14 confirmed the cblevins-5930k 26B upstream runs **2.13x slower** than the cblevins-7900xtx upstream on identical config (22.99 s vs 48.89 s mean for a 141-completion-token request). Root cause: hardware. The `cblevins-5930k` node hostname is legacy — the actual CPU is an Intel Xeon E5-2680 v4 (Broadwell-EP, 2016), vs the 7900xtx node's AMD Ryzen 9 7900X3D (Zen 4, 2023). Engine init logs corroborate (aiter JIT 22.9s vs 12.2s; weight load 40.1s vs 21.5s — same ~1.9x ratio). With `enforce_eager: true` (correctness lock) + `maxNumSeqs: 1`, every decoded token bears Python-side CPU overhead and there's no batching to amortize it. Cannot be fixed serving-side.

The current 1:1 round-robin produces a fleet mean latency of ~35.9 s vs ~23 s if everything went to the 7900xtx alone — a ~1.6x mean-latency tax for parallel capacity that may or may not be worth it.

Four follow-up slices, listed for explicit user direction:

- [ ] **(Option A) Weighted routing.** Add a `flexinfer.ai/routing-weight` Model-CR annotation and have `pickReadyMember` honor weights. Weight `7900xtx=2, 5930k=1` → ~67% of traffic to the faster node, fleet mean latency drops to ~30 s. Real code change (`internal/proxy/resolver.go` + new field + tests + image rebuild + rollout).
- [ ] **(Option B) Demote 5930k to failover.** Set `gemma4-26b-a4b-gptq-5930k` `minReplicas: 0` + lower priority. Spins up only if the 7900xtx instance is unavailable. Loses parallel capacity, eliminates the slow-path tax on routine traffic.
- [ ] **(Option C) Hardware swap.** Replace the Xeon E5-2680 v4 with something post-2020. Real cost, not a code change.
- [ ] **(Option D) Accept the gap.** Status-quo + documented. Reasonable if expected request volume stays modest and parallel capacity matters more than mean latency.

Detailed evidence in `.loom/60-validation-matrix.md` row "26B fleet asymmetric decode rate: 5930k node is 2.2x slower" and `.loom/50-worklog.md` 2026-05-14 entry "RALPH slice — investigate 5930k vs 7900xtx decode-rate asymmetry".

## Previously queued follow-ups (lower priority)

- [ ] **(Optional) Increase per-upstream concurrency.** Both 26B Models run `maxNumSeqs: 1`. A 100-req `-P 20` probe saw 42% HTTP=000 timeouts — purely upstream queue saturation, not routing. Raising `maxNumSeqs` on both Model CRs would scale fleet throughput beyond 2 concurrent reqs but trade per-token latency. Worth re-measuring after a real workload shape is known. Likely not urgent until project-management (or another downstream caller) flips `ICC_LLM_ENABLED=1` and produces sustained concurrency.
- [x] **Document/wire service-side consumption pattern** — closed by services/project-management MR !73 (2026-05-14). `llm_qwen.py` defaults now point at `flexinfer-proxy` + the `project-mgmt` alias; new `FLEXINFER_QWEN_MODEL` env override mirrors the existing `FLEXINFER_QWEN_URL` pattern; rollback recipe documented in ICC's `.loom/40-decisions.md`. Cluster validation captured in flexinfer `.loom/50-worklog.md` 2026-05-14 entry — proxy round-tripped an ICC-shaped extraction request through `project-mgmt → gemma4-26b-a4b-gptq-5930k` and returned valid JSON matching ICC's `_validate_response` schema. ICC overlay still needs `ICC_LLM_ENABLED=1` to actually enable extraction, but that's a deployment-side toggle independent of code.
- [ ] **(Optional) Migrate `--log-level=debug` toggle to Helm values.** The 2026-05-14 debugging cycle required `kubectl patch` to add `--log-level=debug` (Flux didn't revert during the window, but the path is fragile). A first-class `proxy.logLevel` Helm value would make future debugging cycles a one-liner.

## Previous Goal (2026-05-14, late afternoon, closed)

Close the concurrent-load failure mode in shared-service-label routing. **Closed by MR !356.** The 26% failure rate observed at parallelism 10 was NOT a cache-refresh race in the picker — `slog.Debug("forwarding to upstream", ...)` logs (MR !355) with `--log-level=debug` revealed `getRoutingStrategy` was auto-defaulting to `StrategyLeastLoaded` for any label-group member, and `refreshEndpoints`' aggregation wrote the union of all members' pod endpoints into each member's router ring, cross-routing bodies (5930k body → 7900xtx pod → 404).

- [x] Reproduce: 50-req `xargs -P 10` probe → 13/50 vLLM 404s with both directions of mis-routing.
- [x] Root-cause from the new forwarding log: `target=10.42.0.7:8000` (7900xtx pod) for `model=gemma4-26b-a4b-gptq-5930k`.
- [x] Fix: removed the two `isModelInLabelGroup` auto-default branches in `internal/proxy/routing.go:getRoutingStrategy`. Picker (MR !354) now owns cross-model selection; router branch stays dormant unless an operator explicitly opts in via `flexinfer.ai/routing`. Aggregation in `refreshEndpoints` preserved for that explicit case.
- [x] Test: renamed `TestGetRoutingStrategy_LabelGroup_DefaultsToLeastLoaded` → `_StaysDefault` with inverted assertion and lock-in comment.
- [x] Prove: 20 reqs at parallelism 2 → 20/20 success, exact 10/10 split, 16/16 forwarding logs show model-name matching target (0 mismatches). At parallelism 10/20, HTTP=000 failures persist but are upstream queue saturation (`maxNumSeqs: 1`), not routing.
- [x] Validation matrix row added (MR !356 row in `60-validation-matrix.md`).

## Previous Goal (2026-05-13/14 afternoon, closed)

Make the two-instance 26B fleet load-balance across both Ready backends. Closed 2026-05-14 with MR !354 — `internal/proxy/proxy.go:409` now calls `ResolveServiceLabelGroup` + `pickReadyMember` (round-robin among Ready, alphabetical fallback when none Ready).

- [x] Pick a routing policy: round-robin among Ready members. Rationale captured in MR !354 description and `.loom/50-worklog.md` 2026-05-14 entry.
- [x] Implement `ResolveServiceLabelGroup` (`internal/proxy/model_resolver.go`) + `pickReadyMember` (`internal/proxy/resolver.go`). Per-label `atomic.Uint64` counter on `Proxy.labelRRCounters`. Sorted claimants for stable round-robin across cache refreshes. 5 unit tests in `internal/proxy/pick_member_test.go`.
- [x] Prove load-balancing: 20-request probe through `quality-chat` at 0.5 s spacing splits **exactly 10/10** across the two instances. Evidence in `60-validation-matrix.md` row "Proxy round-robin Ready-member routing across shared service-labels".
- [x] Drop "aspirational" caveat from both 26B Model CR comments.
- [x] Validation matrix row updated.

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
