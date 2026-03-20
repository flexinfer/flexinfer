# Loom Context Pack

## Quick Links

- Workspace snapshot: `00-workspace-snapshot.md`
- MCP inventory: `00-mcp-inventory.md`
- Research: `10-research.md`
- Product spec: `20-product-spec.md`
- Implementation plan: `30-implementation-plan.md`
- Decisions: `40-decisions.md`
- Worklog: `50-worklog.md`

## Current Goal

- [x] Refresh `.loom/` templates and regenerate workspace snapshot.
- [x] Capture current MCP/runtime inventory with tool counts and constraints.
- [x] Validate `codebase_memory` indexing/search readiness and document blockers.
- [x] Produce an evidence-backed research/spec/plan set for next execution.
- [ ] Plan next feature/improvement round based on open GitLab issues and code gaps.

## Current State (2026-03-05)

- FlexInfer at `a9ed3af` on `master`. All phases 1-5 and advanced features complete.
- 18 commits since last loom refresh (`da62a60` -> `a9ed3af`):
  - Architecture docs expanded with 8 Mermaid workflow diagrams (`a9ed3af`)
  - FLUX.1 NF4 support, gc.collect OOM fix (`053a2d6`)
  - gfx1100 perf tuning: HipBLASLt, split attention (`45d311c`)
  - Scheduler RBAC hardening: dedicated ClusterRole (#25, `017d46f`)
  - Configurable CRD tolerations (#24, `9334ba3`)
  - Benchmark sidecar termination (#23, `cfafb4e`)
  - K8s allocatable GPU detection fallback (#22, `076ba57`)
  - gfx906 fixes and Makefile targets (`580d7d8`, `937cc70`)
  - 3 Renovate dependency batches merged (`2dc2626`, `5732903`, `b0d74a9`)
  - Quantization status preservation (`859d4d7`, `03f1ccf`)
- MCP: resource API still empty; `loom tools call` CLI is reliable fallback.
- 15 open GitLab issues (see `10-research.md` for prioritized inventory).

### Cluster Model Fleet (2026-03-05)

| Model | Backend | Phase | GPU Pool | Priority | Notes |
|-------|---------|-------|----------|----------|-------|
| `nomic-embed-text` | ollama | Ready | — | — | Embedding model, always warm |
| `qwen3-30b-a3b-abliterated` | llamacpp | Standby | 5930k-models | 110 | MoE 18.7GB GGUF |
| `qwen3-14b-claude-distill` | llamacpp | Active | 5930k-models | 160 | 14B distill |
| `sdxl-turbo-imagegen` | diffusers | Ready | 7900xtx-image | — | Image gen, ROCm |
| `sdxl-inpainting` | diffusers | Idle | 7900xtx-image | — | Inpainting, ROCm |
| `instruct-pix2pix` | diffusers | Idle | 7900xtx-image | — | Image editing, ROCm |

## Open Questions

- Next feature round selection: which of the 15 open issues to prioritize?
- Renovate Docker major updates (ROCm 6.4 -> 7.x) require staged rollout.
- Image-gen benchmarking is stub-only (health check, no images/sec).

## Risks

- MCP resource discovery still returns empty in Claude Code sessions.
- FLUX.1 NF4 user guide missing (GitLab #36, critical).
- vLLM gfx906 build blocked by cmake/ROCm mismatch (GitLab #31).

## Sources

- [S1] `git log --oneline -20` -> HEAD at `a9ed3af`, 18 commits since `da62a60`
- [S2] `ListMcpResourcesTool({})` -> `No resources found` (2026-03-05)
- [S3] Workspace snapshot regenerated 2026-03-05T18:43:39-05:00
- [S4] GitLab issue scan: 15 open issues (2026-03-05)
