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

## Current Goal (2026-04-26)

Drive the Gemma4 26B/31B gfx1100 lanes to fully working abliterated GPTQ artifacts first, then promote TurboQuant only through validated KV-cache canaries.

- [x] Slice A - 26B guardrails: long-canary dGPU selector is safe, hybrid 8K remains the fallback.
- [ ] Slice B - 26B validation: 32K fp16-KV canary failed at engine init with an estimated max length of 8896 tokens; dense-validated rebuild reached only harmful prompt 80/128 before the 4h abliteration deadline and still needs a rerun with the longer timeout.
- [x] Slice C - 31B rebuild/recovery: immediate GPTQ lane is back on the clean `keqv` artifact from !193/!194, Ready/Active at `maxModelLen: 2048`, and direct smoke returned HTTP 200 with answer `4`.
- [x] Slice D - 31B production posture: 31B is the primary warm model with `minReplicas: 1`, `priority: 250`, `gpu.count: 2`, and `warmPolicy: primary`; 26B fallback is queued/preempted.
- [ ] Slice E - TurboQuant canaries: primitive sharing is implemented and patch-idempotence tested, but E4B/31B/26B runtime canaries are still pending a built runtime image.

See `gemma4-26b-31b-gptq-turboquant-plan.md` and `30-implementation-plan.md` for slice detail and acceptance gates.

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
