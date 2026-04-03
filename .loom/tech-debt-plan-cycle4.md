# Technical Debt Remediation Plan — Cycle 4

## Summary

- Planning date: 2026-03-28
- Previous cycle: Cycle 3 (15 items shipped across 3 waves, MRs !126-!129)
- Scope: remaining 1000+ LOC monoliths, adapter sprawl, mcpscaffold adoption, noctx consolidation
- Scoring model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%

## Inventory

| ID | Title | Component | LOC | Funcs | Impact | Risk | Drag | Effort(inv) | Score |
|---|---|---|---:|---:|---:|---:|---:|---:|---:|
| DEBT-038 | Split skills/generator.go monolith | pkg/skills/generator.go | 1342 | 37 | 3 | 2 | 3 | 3 | 54 |
| DEBT-039 | Split agentcontext/knowledge_graph.go | pkg/agentcontext/knowledge_graph.go | 1250 | 39 | 3 | 2 | 3 | 3 | 54 |
| DEBT-040 | Split agentcontext/qdrant.go | pkg/agentcontext/qdrant.go | 1229 | 48 | 3 | 2 | 3 | 3 | 54 |
| DEBT-041 | Split daemon.go core | internal/daemon/daemon.go | 1098 | 21 | 3 | 3 | 2 | 3 | 56 |
| DEBT-042 | Split daemon_toolcache.go | internal/daemon/daemon_toolcache.go | 1073 | 21 | 2 | 2 | 2 | 3 | 44 |
| DEBT-043 | Split callpipeline.go | internal/daemon/callpipeline.go | 1054 | 38 | 3 | 3 | 2 | 2 | 52 |
| DEBT-044 | Split codebase/qdrant/client.go | pkg/codebase/qdrant/client.go | 1052 | 46 | 2 | 2 | 3 | 3 | 48 |
| DEBT-045 | Split TUI fleet panel | internal/tui/panels/fleet.go | 1004 | 33 | 2 | 1 | 2 | 3 | 38 |
| DEBT-046 | Decompose domain_adapters.go (117 funcs) | internal/hud/domain_adapters.go | 825 | 117 | 3 | 2 | 3 | 2 | 50 |
| DEBT-047 | Migrate top 10 MCP servers to mcpscaffold | cmd/mcp-* | — | — | 2 | 1 | 3 | 3 | 42 |
| DEBT-048 | Consolidate noctx into launchctl helper | cmd/loom/*.go | — | — | 1 | 1 | 2 | 5 | 37 |
| DEBT-049 | Add tests for 11 untested packages | cmd/mcp-*, internal/tui/theme | — | — | 2 | 2 | 2 | 3 | 44 |

## Priority Ranking

| Rank | ID | Score |
|---:|---|---:|
| 1 | DEBT-041 (daemon.go) | 56 |
| 2 | DEBT-038 (skills/generator.go) | 54 |
| 3 | DEBT-039 (knowledge_graph.go) | 54 |
| 4 | DEBT-040 (qdrant.go) | 54 |
| 5 | DEBT-043 (callpipeline.go) | 52 |
| 6 | DEBT-046 (domain_adapters.go) | 50 |
| 7 | DEBT-044 (codebase/qdrant/client.go) | 48 |
| 8 | DEBT-042 (daemon_toolcache.go) | 44 |
| 9 | DEBT-049 (untested packages) | 44 |
| 10 | DEBT-047 (mcpscaffold migration) | 42 |
| 11 | DEBT-045 (TUI fleet panel) | 38 |
| 12 | DEBT-048 (noctx consolidation) | 37 |

## Wave 1 (High leverage splits — 5 items)

Target: the 5 largest remaining monoliths that follow the proven split pattern.

- **DEBT-041**: Split `internal/daemon/daemon.go` (1,098 LOC) into lifecycle/server/transport modules
- **DEBT-038**: Split `pkg/skills/generator.go` (1,342 LOC) into template/render/registry modules
- **DEBT-039**: Split `pkg/agentcontext/knowledge_graph.go` (1,250 LOC) into query/mutation/schema modules
- **DEBT-040**: Split `pkg/agentcontext/qdrant.go` (1,229 LOC) into collection/search/index modules
- **DEBT-046**: Decompose `internal/hud/domain_adapters.go` (825 LOC, 117 funcs) by domain group

## Wave 2 (Medium effort)

- DEBT-043 (callpipeline.go split)
- DEBT-044 (codebase/qdrant/client.go split)
- DEBT-042 (daemon_toolcache.go split)
- DEBT-047 (mcpscaffold migration: top 10 servers)

## Wave 3 (Cleanup)

- DEBT-049 (tests for untested packages)
- DEBT-045 (TUI fleet panel split)
- DEBT-048 (noctx consolidation)
