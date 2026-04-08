# Planning: Next Productivity-Unlocking Features

> Date: 2026-04-03
> Status: Draft — awaiting review
> Scope: Identify and sequence the highest-leverage features that multiply daily development productivity

---

## 1. Current State Assessment

### What just shipped (last 2 weeks)
- OTel metrics, session/hub lifecycle spans, cost byte tracking (`5f995f27`)
- DEBT-062/063/064/065/068 — CI stabilization, mobile test harness, HUD bootstrap split, docs guardrails, session lifecycle decomposition
- Fleet orchestration UX — merge queue, recommendations, dispatch history (`1ff61661`)
- Attention lanes across HUD and iOS companion (`e49e1ae0`, `c665a344`)
- Orchestra MCP system for local-model tool orchestration (`d3e3739d`, `1b47034c`)
- Standalone orchestra binary for k3s deployment

### What's partially built but not yet unlocking value
| Feature | Built | Missing for value |
|---------|-------|-------------------|
| Orchestra (local agents) | Router, 6 domains, compound tools, k3s binary | Specialized subagents with real tool subsets, FlexInfer vLLM backend, production model deployment |
| OTel trace export | Daemon init, server handler spans, `otel-status` | Tool call latency instrumentation in proxy, HUD trace explorer |
| Fleet orchestration UX | IA pass, dispatch panel, attention lanes, merge queue | Actual merge orchestration assistance, file-claim conflict surfacing in real-time |
| Server catalog | HUD catalog framing, registry.yaml | `loom catalog list/enable` CLI, one-command activation, env requirement discovery |
| OpenAI Responses | M0-M1 (contract, orchestrator, CLI) | M2 token preflight, compaction, RBAC/audit e2e tests |
| Universal proxy policy | Registry-driven YAML policies, platform refs | Enforcement testing across all 8 platforms, policy audit trail in HUD |

### Productivity bottlenecks observed in recent sessions
1. **Context window waste**: Frontier agents (Claude, Codex) spend 30-50% of context on raw tool output that could be pre-summarized
2. **Multi-tool queries are sequential**: "What's the cluster health?" requires 5-8 sequential tool calls through the frontier agent
3. **Server onboarding friction**: Adding/enabling servers requires manual registry.yaml editing and `loom sync`
4. **No trace visibility**: When tool calls are slow, there's no way to see where time is spent without manual `loom status` checks
5. **Recall quality**: Agent context recall returns keyword-matched results without semantic ranking, leading to noisy context injection

---

## 2. Proposed Priority Ranking

Scored on: **daily productivity impact** (40%), **foundations unlocked** (30%), **effort** (20%), **risk** (10%).

| Rank | Feature | Impact | Foundations | Effort | Risk | Score |
|---:|---------|-------:|----------:|-------:|-----:|------:|
| 1 | Orchestra subagent maturation | 9 | 10 | 6 | 3 | 8.5 |
| 2 | Server catalog + discovery CLI | 8 | 7 | 8 | 1 | 7.4 |
| 3 | OTel trace explorer in HUD | 7 | 8 | 7 | 2 | 7.1 |
| 4 | Agent recall reranking | 8 | 6 | 7 | 2 | 6.9 |
| 5 | Config complexity reduction | 6 | 8 | 5 | 2 | 6.2 |

---

## 3. Feature Details

### F1: Orchestra Subagent Maturation (Phases 2-3)

**Why this is #1**: Every other feature becomes more productive when compound queries are fast and context-efficient. Today, asking Claude Code "what's the cluster health and any failing tests?" costs ~5K frontier tokens and 30+ seconds. With orchestra, it becomes a single `orchestra_query` call returning a compressed ~500-token summary in 3-5 seconds.

**What to build**:
- Phase 2: Embed orchestra in `loomd` as a synthetic MCP server (currently standalone binary only)
- Phase 3: Wire 4 specialized subagents with curated tool subsets (cluster-ops, codebase, ci-pipeline, agent-fleet)
- YAML-driven subagent definitions so new domains can be added without code changes
- Per-subagent timeout and token budget enforcement
- Parallel subagent dispatch with compressed result assembly

**Prerequisites**:
- FlexInfer vLLM backend support for Qwen3.5 models (Phase 0 from `.loom/64-planning`)
- Validate tool-calling format through flexinfer-proxy

**Acceptance criteria**:
- `orchestra_query("What's failing in CI?")` returns structured response via embedded loomd in <5s
- `orchestra_cluster_status` compound tool returns cluster health via parallel subagent dispatch
- Subagent definitions are YAML-driven in registry
- Token metrics track local vs frontier usage

**Effort**: 5-7 days
**Risk**: Medium — vLLM on ROCm for Qwen3.5 needs validation

**Sources**:
- `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md` (Phases 2-3)
- `pkg/orchestra/router.go`
- `cmd/mcp-orchestra/main.go`
- `ROADMAP.md:227-233`

---

### F2: Server Catalog + Discovery CLI

**Why this is #2**: With 46 servers and 498 tools, discovering what's available and enabling new servers is manual and friction-heavy. A browsable catalog with one-command activation turns the server library from a maintenance burden into a competitive advantage.

**What to build**:
- `loom catalog list` — browsable server catalog with capabilities, env requirements, health
- `loom catalog enable <server>` — one-command server activation (adds to registry, sets env hints)
- `loom catalog disable <server>` — clean removal
- `loom catalog search <query>` — fuzzy search across server names, descriptions, tool names
- HUD catalog panel upgrade — browse, enable/disable, per-server health and tool count
- Auto-detect missing env vars and prompt for them during `enable`

**What already exists**:
- `loom://servers` resource with full server inventory
- HUD Server Catalog panel (framing done in `c32cec62`)
- Registry.yaml as canonical server definition source
- `pkg/generator/registry.go` for reading registry

**Acceptance criteria**:
- `loom catalog list` outputs table of all registered servers with status, tool count, required env
- `loom catalog enable prometheus` adds server to active profile and triggers sync
- `loom catalog search "kubernetes"` returns matching servers and tools
- HUD catalog shows enable/disable toggles with env requirement prompts

**Effort**: 3-4 days
**Risk**: Low — builds on existing infrastructure

**Sources**:
- `ROADMAP.md:235-240`
- `internal/hud/frontend/src/lib/components/ServerCatalogPanel.svelte`
- `mcp/context/registry.yaml`

---

### F3: OTel Trace Explorer in HUD

**Why this is #3**: Tool call latency is invisible today. When a compound operation takes 30 seconds, you can't tell if it's model inference, network, or a slow MCP server. Adding trace visibility to the HUD makes performance debugging self-serve.

**What to build**:
- Instrument tool call latency at the proxy layer (request→server→response spans)
- Instrument server spawn/restart lifecycle spans
- Add HUD Traces panel with recent spans, latency breakdown, error highlighting
- Add per-tool latency percentiles to the HUD Overview metrics rail
- Export traces to configured OTel collector endpoint

**What already exists**:
- `pkg/mcpotel` tracing wrappers on all MCP server handlers
- Daemon OTel runtime init and `loom/otel-status` reporting
- `loom_orchestra_latency_seconds` metric in orchestra
- Server restart tracing spans

**Acceptance criteria**:
- `loomd` emits spans for every proxied tool call with server, tool, duration, status
- HUD Traces panel shows recent tool calls with waterfall visualization
- Per-tool P50/P95 latency visible in HUD metrics
- `OTEL_EXPORTER_OTLP_ENDPOINT` enables export to external collector

**Effort**: 4-5 days
**Risk**: Low-Medium — OTel plumbing exists, HUD panel is new

**Sources**:
- `ROADMAP.md:180-188`
- `internal/daemon/daemon_lifecycle.go` (OTel init)
- `pkg/mcpotel/` (existing tracing)
- `internal/daemon/callpipeline.go` (tool call flow)

---

### F4: Agent Recall Reranking

**Why this is #4**: Current recall returns keyword-matched results from Qdrant without semantic reranking, leading to context pollution. Using the local FlexInfer model for reranking would dramatically improve recall precision, which compounds across every agent session.

**What to build**:
- Add reranking step to `agent_recall` and `agent_context_search` using local embeddings model
- Cross-encoder reranking via FlexInfer BGE or Qwen3.5-9B
- Configurable rerank depth (top-K candidates → reranked top-N)
- Score threshold filtering to suppress low-relevance results
- Metrics: rerank latency, result count before/after filtering

**What already exists**:
- `pkg/agentcontext/service_recall.go` — unified recall pipeline
- FlexInfer embeddings provider with morph awareness
- Qdrant vector search with similarity scoring
- `agent_recall` tool with `scope` parameter

**Acceptance criteria**:
- `agent_recall(query="...", scope="vector")` returns reranked results when FlexInfer is available
- Reranking adds <200ms to recall latency
- Result relevance improves measurably (manual evaluation on 10 test queries)
- Graceful fallback to unranked results when FlexInfer is unavailable

**Effort**: 3-4 days
**Risk**: Low — FlexInfer BGE is already deployed

**Sources**:
- `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md:331-334`
- `pkg/agentcontext/service_recall.go`
- `pkg/agentcontext/svc_context_search.go`

---

### F5: Config Complexity Reduction (EPIC 3 kickoff)

**Why this is #5**: The sync system is the most-churned non-CI file area (DEBT-067 targets `cmd_sync.go` at 880 lines). Reducing config complexity by moving to data-driven platform profiles would lower the maintenance cost of supporting 8+ platforms.

**What to build**:
- Split `cmd_sync.go` into generate, sync, pull, and backup slices (DEBT-067)
- Extract platform profile definitions from Go code into YAML/JSON (CONFIG-1)
- Validate platform profiles with schema-driven checks instead of imperative Go logic
- Add `loom sync diff` to preview what would change before applying

**What already exists**:
- `platform_profiles.yaml` with policy_refs and enforcement modes
- `pkg/generator/configs.go` split into per-platform files (DEBT-026)
- `pkg/sync/ops.go` split into ops_sync, ops_regen, ops_validate (DEBT-053)

**Acceptance criteria**:
- `cmd_sync.go` reduced from 880 to <200 lines (composition root only)
- New platform support requires only YAML additions, not Go code changes
- `loom sync diff` shows preview of config changes before write
- Existing platform parity maintained

**Effort**: 4-5 days
**Risk**: Medium — touches critical sync infrastructure

**Sources**:
- `.loom/tech-debt-plan-cycle6.md:77-85` (Wave 3)
- `ROADMAP.md:254-256` (EPIC 3)
- `.loom/35-simplification-epics.md`
- `cmd/loom/cmd_sync.go`

---

## 4. Proposed Execution Sequence

```
Week 1: F1 Phase 0 (FlexInfer vLLM validation) + F2 (catalog CLI)
         ├── Validate Qwen3.5-9B on vLLM/ROCm
         ├── Build loom catalog list/enable/disable/search
         └── Upgrade HUD catalog panel

Week 2: F1 Phases 2-3 (embedded orchestra + subagents)
         ├── Embed orchestra in loomd
         ├── Wire 4 specialized subagents
         └── YAML-driven subagent definitions

Week 3: F3 (OTel trace explorer) + F4 (recall reranking)
         ├── Instrument proxy tool call spans
         ├── Build HUD Traces panel
         ├── Add reranking to agent_recall
         └── Metrics and fallback

Week 4: F5 (config reduction) + polish/integration
         ├── DEBT-067 cmd_sync split
         ├── Data-driven platform profiles
         ├── loom sync diff
         └── Integration testing across features
```

### Parallel Tracks

F1 and F2 can execute in parallel in week 1 because they share no code paths. F2 is lower-risk and can ship independently while F1's prerequisite (vLLM validation) runs.

F3 and F4 can execute in parallel in week 3 because they touch different subsystems (daemon/proxy vs agentcontext).

---

## 5. Dependencies and Risks

| Dependency | Feature | Mitigation |
|-----------|---------|------------|
| FlexInfer vLLM for Qwen3.5 | F1, F4 | Test in Phase 0; fallback to Qwen3-8B on MLC-LLM |
| 24GB VRAM budget | F1 | Start with Qwen3.5-9B only; serverless scaling for GPU sharing |
| HUD frontend (not indexed) | F2, F3 | Direct file reads for Svelte; keep slices small and well-scoped |
| Proxy call path coupling | F3 | Add spans incrementally; use existing callpipeline stage boundaries |

---

## 6. Success Metrics

| Metric | Current | Target | Feature |
|--------|---------|--------|---------|
| Compound query latency | N/A (manual 5-8 tool calls) | <5s single call | F1 |
| Frontier token waste on tool output | ~40% of context | <15% via compression | F1 |
| Server activation time | ~5 min (manual edit + sync) | <30s (one command) | F2 |
| Tool call latency visibility | None | Per-tool P50/P95 in HUD | F3 |
| Recall precision (manual eval) | ~60% relevance | >85% relevance | F4 |
| Platform config maintenance per change | ~20 min (Go code) | ~2 min (YAML only) | F5 |

---

## Sources

- `ROADMAP.md` (full roadmap state as of 2026-03-31)
- `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md` (orchestra/skills/agents plan)
- `.loom/tech-debt-plan-cycle6.md` (active tech debt cycle)
- `.loom/tech-debt-priority-cycle6.md` (scored debt items)
- `.loom/35-simplification-epics.md` (architecture simplification)
- `.loom/50-worklog.md` (sessions 28-29, recent shipping context)
- `loom://config` resource read (2026-04-03): 46 servers, 498 tools, profile `full`
- Git log: `5f995f27..f0dcd67f` (last 30 non-merge commits)
