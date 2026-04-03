# Planning: Next-Gen Skills, Agent Context, and Local Agent Orchestration

> Date: 2026-03-29
> Status: Draft v2 — decisions captured, model research complete
> Scope: Unified skills evolution + k3s-native "loom agents" using local GPU models

---

## 1. Current State Summary

### Skills System
- **Single YAML source of truth**: `mcp/context/skills-registry.yaml` generates configs for 6 platforms
- **Skill types**: command, skill, rule, instruction, workflow
- **Sync**: `loom sync <platform> --regen` propagates changes

### Agent Context
- **14+ domain sub-services** in `pkg/agentcontext/`
- **Persistence**: Qdrant (QdrantRegistry), Neo4j knowledge graph
- **Embeddings**: FlexInfer, Ollama, Morph backends
- **5 tech debt cycles** completed: 50+ monoliths split, coverage 54.1%

### Orchestration Infrastructure (Already Built)
- **Orchestration engine** (`internal/hud/orchestration/`): policy-based dispatch, capacity scoring
- **Spawn controller** (`internal/spawn/`): K8s-native headless agent lifecycle
- **Coordinator domain** (`internal/hud/domain/coordinator/`): LLM-powered summarize/compress/plan
- **MentatLab** (`cmd/mcp-mentatlab/`): DAG workflows with gates, streaming
- **Responses orchestrator** (`pkg/openairesponses/orchestrator.go`): tool-use loop with compaction

### Local GPU Infrastructure
- **FlexInfer**: K8s operator on k3s (AMD 7900 XTX / ROCm, 24GB VRAM)
- **Current models**: Qwen3-8B (MLC-LLM), Qwen3-14B, BGE embeddings, SDXL-Turbo
- **Scale-to-zero**: serverless with idle timeout, OpenAI-compatible API

---

## 2. Decisions (from discussion)

| # | Question | Decision | Rationale |
|---|----------|----------|-----------|
| 1 | Standalone vs embedded | **Embedded in loomd** | Latency-critical for inline compound queries; compute still runs on cluster via FlexInfer API |
| 2 | Tool access scope | **Specialized subagents with tool subsets**, orchestrated by a router agent | Optimizes context window for each local agent (small context = better tool selection) |
| 3 | Model routing | **Multi-model with task routing** — see Model Strategy below | Different models for different workloads; concurrency vs quality tradeoff |
| 4 | MentatLab relationship | Extend MentatLab for spawned long-running tasks; inline orchestration is separate | MentatLab's DAG gates are overkill for sub-second queries but perfect for autonomous tasks |
| 5 | Cost tracking | **Token ratio metrics** — see Telemetry section | Focus on frontier token savings, tool call rate reduction, and context waste measurement |

---

## 3. Model Strategy (Research-Informed)

### 3.1 Model Evaluation

Research conducted 2026-03-29. Sources: HuggingFace model cards, benchmark papers, community testing.

| Model | Type | Active Params | BFCL v4 (tool use) | tau2-Bench (agentic) | VRAM (Q4) | Status |
|-------|------|--------------|---------------------|---------------------|-----------|--------|
| **Qwen3.5-35B-A3B** | MoE | 3B | 67.3% | 81.2 | ~20GB | Best for orchestration |
| **Qwen3.5-9B** | Dense | 9B | ~60% (est) | ~70 (est) | ~6GB | Best for concurrency |
| Qwen3-8B | Dense | 8B | ~55% | ~60 | ~5GB | Current, adequate |
| Nemotron-Cascade-2-30B-A3B | Hybrid MoE | 3B | 52.9% | 58.9 | ~24GB | **Weak on tool use** |

**Key finding**: Nemotron-Cascade-2 is significantly weaker on agentic/tool-calling tasks (BFCL 52.9% vs Qwen3.5 67.3%, tau2 58.9 vs 81.2). It excels at math/code reasoning but is not suitable as the primary orchestration model. May be useful as a specialized reasoning backend.

### 3.2 Chosen Model Lineup

```
┌─────────────────────────────────────────────────────────────┐
│  Tier 1: Router / Orchestrator                              │
│  Qwen3.5-35B-A3B (3B active, ~20GB VRAM)                   │
│  - Routes queries to specialized subagents                  │
│  - Handles free-form orchestra_query                        │
│  - Best tool-calling scores in its compute class            │
│  Backend: vLLM (MLC-LLM doesn't support Gated DeltaNet)    │
│                                                             │
│  Tier 2: Specialized Subagents (high concurrency)           │
│  Qwen3.5-9B (dense, ~6GB VRAM per instance)                │
│  - Multiple concurrent instances with small tool subsets    │
│  - Cluster ops, codebase search, CI/CD, agent briefing     │
│  - Can run 3-4 instances in 24GB when Tier 1 is idle       │
│  Backend: vLLM                                              │
│                                                             │
│  Tier 3: Deep Reasoning (on-demand)                         │
│  Nemotron-Cascade-2-30B-A3B or Qwen3.5-27B                 │
│  - Activated for complex analysis tasks only                │
│  - Math-heavy, code-heavy, or multi-step reasoning          │
│  - Scale-to-zero when not in use                            │
│                                                             │
│  Experimental: Claude Distill                               │
│  Qwen3.5-9B-Claude-4.6-Opus-Reasoning-Distilled-v2         │
│  - Jackrong's LoRA fine-tune (14K samples)                  │
│  - 8K context limit (down from 262K) — use with caution     │
│  - Evaluate for: agentic coding, self-correction, tool use  │
└─────────────────────────────────────────────────────────────┘
```

### 3.3 FlexInfer Backend Change Required

Current state: Qwen3-8B runs on **MLC-LLM** backend.
Required: Qwen3.5 models need **vLLM** (Gated DeltaNet architecture not yet in MLC-LLM).

**Action items**:
1. Add vLLM backend support to FlexInfer (if not already present — check `v1alpha2` Model CRD)
2. Deploy Qwen3.5-35B-A3B via vLLM on k3s
3. Deploy Qwen3.5-9B via vLLM (can coexist; serverless scaling handles GPU sharing)
4. Keep Qwen3-8B on MLC-LLM as fallback during transition
5. Evaluate SDXL-Turbo GPU time vs. Qwen3.5-9B concurrency tradeoff

### 3.4 GPU Budget (24GB 7900 XTX)

| Config | VRAM Usage | Concurrency | Use Case |
|--------|-----------|-------------|----------|
| Qwen3.5-35B-A3B Q4 only | ~20GB | 1 instance | Router + general orchestration |
| Qwen3.5-9B Q4 × 3 | ~18GB | 3 instances | High-throughput subagent work |
| 35B-A3B Q4 + 9B Q4 × 1 | ~26GB | Swap | Hybrid (needs serverless scaling) |
| 9B Q4 + SDXL-Turbo | ~14GB | 1+1 | Keep imagegen + basic orchestration |

**Recommendation**: Start with Qwen3.5-9B × 2-3 instances for subagent concurrency. Add 35B-A3B as the router once we validate the pattern works. Use FlexInfer's serverless scaling to swap between configs based on demand.

---

## 4. WS-3: K3s-Native "Loom Agents" (MCP Orchestra) — Revised

### 4.1 Architecture: Hierarchical Subagent Model

```
┌──────────────────────────────────────────────────────┐
│  Claude Code / Codex  (frontier agent)               │
│  "What's the cluster health and any failing tests?"  │
└────────────────────┬─────────────────────────────────┘
                     │ single MCP tool call: orchestra_query
                     ▼
┌──────────────────────────────────────────────────────┐
│  ROUTER (embedded in loomd)                          │
│  Model: Qwen3.5-35B-A3B (or 9B for simple routing)  │
│                                                      │
│  1. Parses intent → identifies required domains      │
│  2. Dispatches to specialized subagents in parallel  │
│  3. Collects results, compresses, returns            │
│                                                      │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐             │
│  │ Cluster  │  │Codebase │  │  CI/CD  │  ...more    │
│  │ Subagent │  │Subagent │  │Subagent │             │
│  │          │  │         │  │         │             │
│  │ Tools:   │  │ Tools:  │  │ Tools:  │             │
│  │ k8s_get  │  │ cb_srch │  │ gl_pipe │             │
│  │ prom_qry │  │ cb_defn │  │ gl_jobs │             │
│  │ flux_src │  │ cb_call │  │ ql_test │             │
│  │ loki_qry │  │ git_log │  │ gl_diff │             │
│  │ am_alert │  │ git_dif │  │         │             │
│  └─────────┘  └─────────┘  └─────────┘             │
│  Model: Qwen3.5-9B per subagent (small context)     │
└──────────────────────────────────────────────────────┘
```

### 4.2 Subagent Domains

Each subagent has a **focused tool set** (5-10 tools max) and a **domain system prompt**:

| Subagent | Tools (via daemon loopback) | System Prompt Focus |
|----------|---------------------------|---------------------|
| **cluster-ops** | k8s_get, k8s_getPods, k8s_describe, k8s_logs, prometheus_query, alertmanager_list_alerts, flux_get_sources, flux_get_kustomizations | K8s health, pod status, alerts, GitOps state |
| **codebase** | codebase_search, codebase_get_definition, codebase_find_callers, codebase_get_references, codebase_get_context, git_log, git_diff, git_status | Code understanding, symbol resolution, change analysis |
| **ci-pipeline** | gitlab_list_pipelines, gitlab_get_pipeline, gitlab_list_pipeline_jobs, gitlab_get_job_trace, quality_test, quality_lint | Build status, test failures, lint issues |
| **agent-fleet** | agent_session_list, agent_task_list, agent_presence_list, agent_memory_recall, agent_context_search | Agent activity summary, task status, session state |
| **observability** | prometheus_query, prometheus_query_range, loki_query_range, grafana_search, grafana_get_dashboard | Metrics, logs, dashboards, time-series analysis |
| **infra-ops** | docker_ps, docker_logs, helm_list, helm_status, cloudflare_list_dns_records | Container, Helm release, DNS, and service status |

**Why subagent isolation matters**: A Qwen3.5-9B with 5 tool definitions in a 4K context window makes better tool-selection decisions than the same model with 60+ tools in a 32K window. Smaller context = faster inference = better accuracy.

### 4.3 Router Design

The router is the intelligence layer. It:
1. Receives a natural language query from the frontier agent
2. Classifies which subagent domains are needed (can be multiple)
3. Dispatches subagent tasks **in parallel** via goroutines
4. Collects results with timeout
5. Uses a final LLM pass to compress and structure the combined response
6. Returns a single, concise response to the frontier agent

```go
// pkg/orchestra/router.go
type Router struct {
    agents   map[string]*SubAgent  // domain → subagent
    llm      openairesponses.ResponsesClient  // FlexInfer
    daemon   *daemon.Client  // loomd socket
    budget   TokenBudget     // per-query and per-subagent limits
    telemetry *OrchestraTelemetry
}

type SubAgent struct {
    Domain      string            // "cluster-ops", "codebase", etc.
    Tools       []ToolDefinition  // curated tool subset
    SystemPrompt string           // domain-specific instructions
    Model       string            // flexinfer model name
    MaxTokens   int               // context budget for this subagent
}
```

### 4.4 Embedding in loomd

The orchestra runs as an **embedded MCP server** within `loomd`, similar to how agent-context tools are already bridged:

```
loomd process
├── MCP server multiplexer (existing)
├── HUD web server (existing)
├── Orchestra router (NEW)
│   ├── Subagent: cluster-ops
│   ├── Subagent: codebase
│   ├── Subagent: ci-pipeline
│   └── ...
└── FlexInfer client (calls k3s flexinfer-proxy API)
```

**Latency path**: Frontier agent → stdio/HTTP → loomd → orchestra router → FlexInfer API (HTTP to k3s) → model inference → tool calls via daemon loopback → response assembly → frontier agent.

Target latency for simple queries: **2-5 seconds** (includes model inference + 2-3 tool calls).

### 4.5 Implementation Phases (Revised)

#### Phase 0: FlexInfer vLLM + Model Deployment (prerequisite)
- Verify FlexInfer vLLM backend support for Qwen3.5
- Deploy Qwen3.5-9B via FlexInfer on k3s
- Validate tool-calling format (`qwen3_coder` XML) through flexinfer-proxy
- Benchmark: latency, throughput, VRAM usage

#### Phase 1: Foundation — `pkg/orchestra/` (2-3 days)
- `SubAgent` type with tool subset, system prompt, model config
- `Router` with parallel dispatch and result collection
- `FlexInferClient` implementing `openairesponses.ResponsesClient`
- `TokenBudget` enforcement per subagent and per query
- Unit tests with mock FlexInfer responses

#### Phase 2: Embedded Orchestra in loomd (2-3 days)
- Register orchestra tools in daemon's MCP tool registry
- Wire `orchestra_query` (free-form) and `orchestra_gather` (structured schema)
- Route through existing daemon loopback for tool execution
- Add to registry.yaml so frontier agents can discover it
- Integration test: Claude Code → orchestra_query → subagent → tool calls → response

#### Phase 3: Specialized Subagents (3-4 days)
- Implement 4-6 subagent domains from the table above
- YAML-driven subagent definitions (tools, prompt, model, budget)
- Parallel execution with per-subagent timeout
- Subagent result caching (short TTL for repeated queries)

#### Phase 4: Predefined Compound Tools (2-3 days)
- `orchestra_cluster_status` — single-call full cluster snapshot
- `orchestra_ci_status` — pipeline + test failure analysis
- `orchestra_codebase_context` — deep code understanding for a topic
- `orchestra_agent_briefing` — all-agent activity summary
- Each compound tool maps to a specific subagent flow with a fixed output schema

#### Phase 5: Spawned Autonomous Mode (3-5 days)
- Extend spawn controller: `AgentType: "loom-agent"`
- Long-running tasks via MentatLab DAG (with approval gates)
- Results persisted to agent-context, callback to frontier agent
- HUD Fleet panel shows loom-agent spawns

#### Phase 6: Telemetry + Feedback Loop (2-3 days)
- See Telemetry section below

---

## 5. Telemetry: Token Economics Dashboard

### 5.1 Metrics to Track

The goal is to quantify how much frontier agent context/cost the orchestra saves.

#### Frontier Agent Metrics (collected via CLI hooks)
```
loom_frontier_tokens_total{agent="claude-code", direction="input|output"}
loom_frontier_tool_calls_total{agent="claude-code", tool="..."}
loom_frontier_tool_response_tokens{agent="claude-code", tool="..."}
loom_frontier_session_duration_seconds{agent="claude-code"}
```

#### Orchestra Metrics
```
loom_orchestra_queries_total{type="query|gather|compound"}
loom_orchestra_subagent_calls_total{domain="cluster-ops|codebase|..."}
loom_orchestra_local_tokens_total{model="qwen3.5-9b", direction="input|output"}
loom_orchestra_tool_calls_total{domain="...", tool="..."}
loom_orchestra_response_tokens{type="raw|compressed"}  # before/after compression
loom_orchestra_latency_seconds{type="query|compound"}
loom_orchestra_errors_total{domain="...", error_type="..."}
```

#### Derived Ratios (computed in HUD)

| Metric | Formula | What It Tells You |
|--------|---------|------------------|
| **Token Savings Ratio** | `1 - (orchestra_response_tokens / frontier_tool_response_tokens_equivalent)` | How much context window the orchestra saves by compressing tool results |
| **Tool Call Reduction** | `frontier_tool_calls_before / frontier_tool_calls_after` | How many sequential frontier tool calls are replaced by single orchestra calls |
| **Cost Ratio** | `frontier_token_cost / (frontier_token_cost + local_token_cost)` | Fraction of total compute going to expensive frontier models (target: drive down) |
| **Context Waste** | `tool_response_tokens / total_frontier_input_tokens` | What % of frontier context window is consumed by raw tool output |
| **Compression Ratio** | `raw_subagent_output_tokens / compressed_response_tokens` | How effectively the router compresses multi-source results |
| **Local Utilization** | `local_tokens_total / (local_tokens_total + frontier_tokens_total)` | What fraction of all inference is handled locally (target: drive up) |

### 5.2 HUD Integration

New **Token Economics** panel in HUD dashboard:
- Real-time token flow visualization (frontier ↔ local)
- Per-session cost breakdown with estimated savings
- Tool call heatmap (which tools generate the most context waste)
- Subagent utilization and latency distribution
- Historical trend: local utilization % over time

### 5.3 Hook-Based Collection

Frontier agent metrics come from existing CLI hooks:
- `PostToolUse` hook already captures tool name and can measure response size
- Add `tool_response_bytes` to hook payload
- Aggregate in HUD via existing fleet/session monitoring

---

## 6. WS-1: Unified Skills Evolution (unchanged)

### Priority Order
1. **`mcp-skills` discovery server** — runtime skill query/invocation
2. Skill Telemetry — invocation tracking, Prometheus metrics, HUD panel
3. Conditional Skills — project-context activation
4. Skill Versioning — deprecation workflow

---

## 7. WS-2: Agent Context Maturation (unchanged)

### Priority Order
1. **Recall reranking** via local model (Qwen3.5-9B through FlexInfer)
2. Auto-compaction with LLM summarization
3. Cross-session context bridging
4. Auto-dispatch with approval gates

---

## 8. Execution Plan

```
Phase 0 (prereq): FlexInfer vLLM backend + Qwen3.5-9B deployment
                   Validate tool-calling format through flexinfer-proxy

Week 1: WS-3 Phase 1+2 (pkg/orchestra + embedded in loomd)
         WS-1A (mcp-skills server — independent, parallel)

Week 2: WS-3 Phase 3 (specialized subagents — cluster, codebase, CI, fleet)
         WS-2A (recall reranking — reuses FlexInfer client from WS-3)

Week 3: WS-3 Phase 4 (compound tools)
         WS-2B (auto-compaction)
         WS-1B (skill telemetry)

Week 4: WS-3 Phase 5 (spawned autonomous mode via MentatLab)
         WS-3 Phase 6 (telemetry dashboard)
         WS-2C+D (context bridging, auto-dispatch)
```

---

## 9. Key Files Reference

| Area | Key Files |
|------|-----------|
| Skills registry | `mcp/context/skills-registry.yaml` |
| Agent context service | `pkg/agentcontext/service.go` |
| Context recall | `pkg/agentcontext/svc_context_search.go` |
| Compaction | `pkg/agentcontext/compaction_*.go` |
| Orchestration engine | `internal/hud/orchestration/engine.go` |
| Spawn controller | `internal/spawn/controller.go`, `types.go` |
| Coordinator domain | `internal/hud/domain/coordinator/coordinator.go` |
| Responses orchestrator | `pkg/openairesponses/orchestrator.go` |
| MCP scaffold | `pkg/mcpscaffold/` |
| FlexInfer deployments | `platform/gitops/k3s/ai/flexinfer/` |
| HUD app | `internal/hud/app.go` |
| Daemon | `internal/daemon/daemon.go` |

---

## 10. Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| vLLM on ROCm unstable for Qwen3.5 | Medium | High | Test thoroughly in Phase 0; fallback to Qwen3-8B on MLC-LLM |
| Local model tool-call accuracy insufficient | Medium | High | Start with predefined compound tools (constrained); graduate to free-form |
| 24GB VRAM too tight for router + subagents | Medium | Medium | Start with 9B-only; use serverless scaling to time-share GPU |
| Latency too high for inline use | Low | Medium | Cache frequent queries; precompute compound tools on schedule |
| Frontier agents don't use orchestra tools | Low | High | Make compound tools genuinely useful; track adoption in telemetry |

---

## 11. Open Items

1. **FlexInfer vLLM support audit** — Does the v1alpha2 Model CRD already support vLLM backend? Need to check before Phase 0.
2. **Qwen3.5 Claude distill evaluation** — Set up A/B test between base Qwen3.5-9B and Jackrong's Claude-distilled variant on a standard tool-calling benchmark.
3. **Nemotron-Cascade-2 role** — Park for now. Revisit when abliteration tooling supports hybrid Mamba-MoE architecture, or if a math/code-heavy subagent domain emerges.
4. **SDXL-Turbo GPU budget** — If we need full 24GB for inference models, imagegen moves to scheduled/off-peak only.
