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
- vLLM feature-parity brainstorm (AMD, 2026-05-15): `brainstorm-vllm-feature-parity-amd-2026-05-15.md`
- vLLM feature-parity Wave 1 spec (AMD, 2026-05-15): `21-product-spec-vllm-feature-parity-2026-05-15.md`
- vLLM feature-parity Slice 3 falsification (rms_norm ROCm bug, 2026-05-15): `slice3-v1-sandbox-rms-norm-falsified-2026-05-15.md`
- ASR + diarization infra plan for ICC meeting transcription (7900 XTX, 2026-05-18): `asr-diarization-7900xtx-plan-2026-05-18.md`
- ASR Slice 1 kill-test INCONCLUSIVE (controller did not reconcile, 2026-05-19): `asr-diarization-kill-test-inconclusive-2026-05-19.md`
- Roadmap unblock plan (blocked feature churn, 2026-05-21): `roadmap-unblock-plan-2026-05-21.md`
- Context-curve benchmark spec capsule: `../docs/planning/context-curve-benchmark.md`
- RALPH context-curve benchmark spec iteration (2026-05-21): `ralph-context-curve-benchmark-spec-2026-05-21.md`
- RALPH context-curve runner MVP (2026-05-22): `ralph-context-curve-runner-2026-05-22.md`
- RALPH context-curve live capture (2026-05-22): `ralph-context-curve-live-capture-2026-05-22.md`
- RALPH context-curve ConfigMap storage (2026-05-22): `ralph-context-curve-configmap-storage-2026-05-22.md`
- RALPH context-curve spec closeout (2026-05-22): `ralph-context-curve-spec-closeout-2026-05-22.md`
- F4 long-context agent brainstorm (2026-05-25): `brainstorm-f4-long-context-agent-2026-05-25.md`
- RALPH F4-prefix-cache-flip canary plan (2026-05-26): `ralph-f4-prefix-cache-flip-canary-2026-05-26.md`
- RALPH F4-tool-loop-as-prefix kill-test plan (2026-06-01): `ralph-f4-tool-loop-as-prefix-2026-06-01.md`
- RALPH F4 agent-loop ReAct client — slice 1/CLI (2026-06-01): `ralph-f4-agent-loop-client-2026-06-01.md`

## Current Goal (2026-06-01) - F4 agent-loop ReAct client (slice 1: CLI)

Focused plan: `ralph-f4-agent-loop-client-2026-06-01.md`.

The kill-test PASS (below) cleared the spec-riskiest-assumption gate, so the
F4 ReAct **client** is unblocked. Operator picked client forms **(a) CLI +
(b) loom-core MCP tool**, scope **loop + real tool execution**. Because (a)
and (b) are separate modules/repos, RALPH ships them as two vertical slices.

**Slice 1 (this slice) — CLI `cmd/agent-loop/` on a reusable
`internal/agentloop/` engine.** The engine enforces the cache-paying
invariant in the type system: `Conversation` is append-only and
mutability-ordered (immutable system+tools prefix; history grows by `Append`
only — no Insert/Replace/Reorder). `ChatClient` pins
`X-Flexinfer-Cache-Key=session_id` for prefix-consistent routing; per-turn
`TurnMetrics` parse the proxy headers the kill-test used; a `Budget` guard
encodes the usable-context bound (`maxModelLen − system − output`) row 194
surfaced and stops the loop cleanly before HTTP 400 (the F4-413-as-feature
affordance, in-process). The CLI runs REAL read-only, path-jailed tools
(`read_file`, `list_dir`) in a ReAct loop, with an offline `--self-check`.
Offline-validated locally (build/vet/test/-race/gofmt/self-check green);
**live-validated 2026-06-01 (PASS)** via operator-authorized full canary
preemption: two `bin/agent-loop` sessions vs `gemma4-26b-a4b-gptq-apc-canary`
through `flexinfer-proxy`. The prefill-isolation probe showed `prompt_tokens`
growing **22.5×** (247→5565) while `upstream_ms` tracked the per-round token
*delta* (526–701ms for small-delta rounds; only large-file rounds rose) — the
prefix cache absorbing the immutable prefix, observed client-side, matching
row 194's engine-side finding. Caveat: gemma4 omits `cached_tokens`, so the
hit is inferred from delta-bound latency (follow-up: prefill-only/TTFT header).
Production restored cleanly (primary Ready+Active). **Matrix row 195 → `pass`.**

**Slice 2 (NEXT, queued):** expose the same engine shape as an MCP tool
loom-core hosts, mirroring this slice's append-only prefix layout against the
agent-context surface.

## Previous Goal (2026-06-01) - F4-tool-loop-as-prefix kill-test

Focused plan: `ralph-f4-tool-loop-as-prefix-2026-06-01.md`.

Second leg of the F4 compound. The canary slice (below) returned a
**conditional** verdict on 2026-05-28: APC survives the *alternating
two-prefix* (multi-tenant) eviction pattern at `maxModelLen ≤ 20480`
(hit_rate 0.666, TTFT decay 17-24×) but is structurally infeasible at
32k FP8 KV on the 22 GB cap. This slice ships the kill-test for the
**distinct, unproven** *append-only growing* pattern
(`F4-tool-loop-as-prefix`): an immutable `system + tool-schema` prefix
followed by an append-only `(user → assistant → tool-result)` history,
re-sent in full each round. Runner `scripts/f4-tool-loop-as-prefix.py`
(schema `flexinfer.f4_tool_loop_as_prefix.v1`) with an offline
`--self-check`.

**RESOLVED — live kill-test PASSED 2026-06-01.** Ran on the canary
(`flux suspend` → force-promote at `maxModelLen 20480` → 16 tuned rounds →
restore + `flux resume`; production primary preempted ~7 min, reclaimed Ready).
Two independent signals confirmed the assumption: TTFT-flatness ratio **1.42**
(≤ 1.5) despite **2.94× prompt growth**, and engine `/metrics` prefix-cache
block hit rate **93.0%** over the run (the gemma4 engine omits `cached_tokens`,
so the fallback path was taken as designed). Matrix row 194 → `promote`.
Operational bound surfaced: at a 6k system prefix the append-only context
exceeds `maxModelLen 20480` by round 12 (HTTP 400) — usable budget =
`maxModelLen − system − output`, which is the `F4-413-as-feature` leg's domain.

**Next slice (unblocked by the PASS, per spec-riskiest-assumption rule): build
the ReAct client.** Open question from the brainstorm — pick the client form:
(a) CLI `cmd/agent-loop/`, (b) MCP tool loom-core hosts, (c) Open WebUI plugin;
recommendation leans (a). Append-only tool history is proven near-free per turn.

## Previous Goal (2026-05-26) - F4-prefix-cache-flip canary

Focused plan: `ralph-f4-prefix-cache-flip-canary-2026-05-26.md`.

After F4 decode-tail kill-test PASS on 2026-05-25 (decode flat 50-67 tok/s
across 2k→28k context — F4 "feels instant" structurally viable), the
recommended F4 first slice from
`brainstorm-f4-long-context-agent-2026-05-25.md` was unblocked. MR !519
landed the side-by-side `gemma4-26b-a4b-gptq-apc-canary` Model that
mirrors the warm primary except for `enablePrefixCaching: true` and
`gpuMemoryUtilization: "0.94"`. **Live verdict 2026-05-28: conditional**
— APC passes eviction-thrash at `maxModelLen ≤ 20480`, fails to load at
32k. See `60-validation-matrix.md` row 193.

## Previous Goal (2026-05-21) - Roadmap unblock plan

Focused plan: `roadmap-unblock-plan-2026-05-21.md`.

The current execution posture is to stop feature churn by ordering the open
work around explicit runtime kill-tests and validation-matrix evidence:

- Lane 0: link and freeze the roadmap-unblock context.
- Lane 1: unblock or retire gfx906 production fallback through the llama.cpp
  `hipMemGetInfo` kill-test before soak or alias promotion.
- Lane 2: close runtime-promotion evidence gaps for gfx1100/gfx906 canaries.
- Lane 3: run live deploy/swap observability validation for the proof-complete
  PR-2 surfaces.
- Lane 4: spec and implement a reporting-only context-curve benchmark MVP.
- Lane 5: resume major dependency/base-image rollout only as one image family
  per MR with rollback evidence.

Primary blocker: the gfx906 llama.cpp pre-soak probe currently fails
`hipMemGetInfo=1:invalid argument`, so downstream radeonvii alias promotion is
blocked until that image-level compatibility issue is fixed and recorded.

## Previous Goal (2026-05-14, late evening) - F1+F7-vectorize shipped: -24.9% cumulative, 5930k gap closed from 2.13x -> 1.60x

Brainstorm session (`.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md`) produced 8 framings for closing the 5930k decode-rate gap without hardware swap. User picked F1, then F2 (falsified), then F7 (profile first), then vectorize the MoE patch inner loop. Four slices attempted on 2026-05-14.

**Status:**

| Slice | Mean req time | Δ vs prev | Cum gain |
| --- | --- | --- | --- |
| Baseline (Flux-managed config) | 48.89 s | — | — |
| F1 (CPU governor `schedutil` → `performance`) | 47.01 s | −3.8% | −3.8% |
| F7 sync-hoist (MR !361, `runtime:...-moe-patched-fast`) | 45.87 s | −2.4% | −6.2% |
| F2 (revisit `enforce_eager`) | — | n/a | **falsified** (HIP stream-capture crash, manifest comment still binding) |
| F7 vectorize (MR !363, `runtime:...-moe-vectorized`) | **36.70 s** | **−20.0%** | **−24.9%** |

**5930k went from 2.13x slower than 7900xtx → 1.60x slower.** The gap closed by ~25% with no hardware change.

MR !363 (vectorize) replaced the top_k per-slot inner loop in the GEMMA4_MOE_ROCM_REFERENCE_PATCH with two `torch.bmm` calls per token: 16 small matmul launches per layer per token → 2. PCIe roundtrip savings dominate the gain on the older X99 platform. Coherence gauntlet (6 prompts at temperature=0, golden = 7900xtx output): **5/6 exact-match**. The 6th (haiku) diverges at line 3 due to expected FP16 reduction non-associativity (vectorized `sum(dim=0)` parallel reduce vs the original sequential accumulator); output is still a valid 5-7-5 haiku. Factual outputs are bit-identical. Image registry digest: `sha256:c2c89b330c3f414e23b75f468d94b1d80b512a8d539951645c6971446adf77a1`.

**What's left in the gap (1.60x → 1.0x = ~14 s remaining):** likely memory bandwidth + residual per-token Python overhead (cache lookups + stacking + remaining per-layer small launches we still do). Closing further requires either (a) pre-dequantizing all experts at startup (memory cost: 46 GB total, doesn't fit on 24 GB GPU), (b) writing a proper Triton/HIP kernel for INT4 + GELU MoE on ROCm (real upstream engineering, months), or (c) hardware. Returns diminish sharply from here.

**Remaining options, listed for explicit user direction:**

- [ ] **(Pin 7900xtx to vectorized image)** After 24 h of clean 5930k canary, pin the 7900xtx Model to the same `sha256:c2c89b330c3f...` digest. Same patch benefits it proportionally (likely 5-15% on the already-fast lane; the per-token launch overhead matters less but is still real). Trivial follow-up MR; one-line digest swap in `deploy/models/gemma4-26b-a4b-gptq.yaml`. No correctness exposure — patch already proven on sister.
- [ ] **(F5) Workload-shaped routing.** Route short-output requests to 5930k, long-output to 7900xtx. Proxy code change in `internal/proxy/proxy.go`. Routes around the residual gap; doesn't fix it.
- [ ] **(F6) llama.cpp on 5930k.** Highest potential lift; 12-24 h GGUF re-quant + feature divergence (different tool/reasoning parsers, separate operational paths).
- [ ] **(Demote 5930k)** Set `minReplicas: 0` + lower priority. Loses parallel capacity; eliminates the slow-path tax on routine traffic.
- [ ] **(Accept)** Status-quo + documented. The 1.60x gap is operationally usable for current workload volumes. F5 (cheap routing tweak) is the natural next step if user-visible latency becomes a complaint.

Detailed evidence in `.loom/brainstorm-26b-5930k-decode-perf-2026-05-14.md` (execution log), `.loom/60-validation-matrix.md`, and `.loom/50-worklog.md` 2026-05-14 entries.

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
