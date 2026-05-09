# Product Spec: `pkg/llmfsm` — Unified LLM-FSM Substrate

**Date**: 2026-05-09
**Implementation Plan**: `.loom/114-implementation-plan-llm-fsm-substrate-2026-05-09.md`

## Goal

Extract the gate / state-transition pattern that Mills and Weaver each implement informally into a typed, reusable substrate (`pkg/llmfsm`) so any automation can express its decision points as a finite state machine where transitions are produced by:

- **LLM gates** — model picks one of N enumerated transitions, emits atomic side-effect args, validated against a schema before commit.
- **Deterministic gates** — pure function (state, input) → transition. For thresholds, routing, score gates.
- **Approval gates** — human picks the transition via HUD, FSM pauses on the existing `agent_workflow_*` event stream.

The substrate is the unified piece. Mills and Weaver migrate onto it incrementally as proof. Future automations get the substrate for free.

## Non-Goals

- **Replace `agent_workflow_*` MCP tools** — substrate sits *on top* of them. Workflow tools remain the persistence + event layer; `llmfsm` adds typed gate definitions and a runner.
- **Rewrite Mills' policy engine** — Mills keeps `pkg/mills/policy.go`; only the gate executions become `llmfsm` gates. Squads, audit pool, and council ensemble configs stay where they are.
- **Disturb the S5a Mills-research-via-Weaver soak** — substrate work is decoupled until the soak completes (1 week from 2026-05-09 commit `09ea87d8`).
- **Build a new model router** — uses `pkg/aimodels.Resolver` for model selection per gate's `model_role`.
- **Workflow editor UI** — FSMs are defined in YAML files in-repo; HUD reads them but does not edit them.
- **Generic agent framework** — this is a state-machine substrate, not a planner or autonomous agent loop. Gates have bounded I/O by design.

## Architecture at a glance

```
                       ┌─────────────────────────────────────────┐
                       │              pkg/llmfsm                 │
                       │                                         │
                       │   FSM YAML ──► Loader ──► Definition    │
                       │                                         │
                       │   Runner ─────► GateExecutor            │
                       │      │              │                   │
                       │      │              ├── LLMGate         │
                       │      │              ├── DeterministicGate│
                       │      │              └── ApprovalGate    │
                       │      ▼                                  │
                       │   Validator (JSONSchema) ──► Transition │
                       └────────────────┬────────────────────────┘
                                        │
                                        ▼
                          agent_workflow_* (MCP)
                          ┌─────────────────────────┐
                          │ events: SSE + DB        │
                          │ approve / reject        │
                          │ status / cancel         │
                          └─────────────────────────┘
                                        │
                                        ▼
                          HUD WorkflowPanel + Spectator
                          (already exists, no new UI)


  Callers (incremental migration):

      pkg/weaver        pkg/mills/audit       pkg/mills/squads     pkg/mills/council
      (S10)             (S11a)                 (S11b)              (S12)
        │                   │                     │                    │
        └───────────────────┴─────────────────────┴────────────────────┘
                                   │
                                   ▼
                              pkg/llmfsm
```

## Changes

### P0 — Substrate (GR-*)

**GR-001: `pkg/llmfsm/gate.go`** — gate interface and three implementations.

```go
package llmfsm

type Gate interface {
    Execute(ctx context.Context, in GateInput) (Transition, error)
    Schema() GateSchema
}

type GateInput struct {
    State    StateID
    Payload  json.RawMessage // validated against InputSchema
    Metadata map[string]string
}

type Transition struct {
    To           StateID
    SideEffects  json.RawMessage // validated against OutputSchema
    Confidence   float64         // optional; LLMGate populates
    Rationale    string          // optional; LLMGate populates
}

type LLMGate struct {
    ModelRole    aimodels.Role
    InputSchema  *jsonschema.Schema
    OutputSchema *jsonschema.Schema  // enum-restricted on `to` field
    Prompt       PromptTemplate
    MaxRepairAttempts int            // default 2
    Resolver     *aimodels.Resolver
    Client       LLMClient            // FlexInfer adapter, or test fake
}

type DeterministicGate struct {
    Func func(ctx context.Context, in GateInput) (Transition, error)
    OutputSchema *jsonschema.Schema
}

type ApprovalGate struct {
    HUDPrompt    string
    Choices      []TransitionChoice
    TimeoutSec   int  // 0 = wait forever
    Workflow     WorkflowAdapter
}
```

**GR-002: `pkg/llmfsm/runner.go`** — orchestrates gate execution, emits events.

- Loads FSM definition.
- For each transition: resolves gate by current state, calls `Execute`, validates, commits, emits event.
- On invalid LLM output: triggers repair prompt (up to `MaxRepairAttempts`), then escalates to an `ApprovalGate` if defined for the state, then aborts.
- Emits to `agent_workflow_events` via `WorkflowAdapter` (GR-005).

**GR-003: `pkg/llmfsm/schema.go`** — JSON Schema validation.

- Compiled once at FSM load time using `github.com/santhosh-tekuri/jsonschema/v5`.
- Output schemas must restrict the `to` field to an `enum` of declared transitions — loader rejects FSMs that don't.
- Side-effect args are validated as part of the output schema.

**GR-004: `pkg/llmfsm/yaml.go`** — FSM YAML loader.

```yaml
fsm: weaver.router
description: Decide which strategy to use for a Weaver query.
states:
  - id: query_received
    gate:
      type: llm
      model_role: weaver-router
      input_schema: schemas/weaver_router_input.json
      output_schema: schemas/weaver_router_output.json
      prompt: prompts/weaver_router.tmpl
      max_repair_attempts: 2
    transitions:
      - to: spawn_subagents
        side_effect: spawn
      - to: respond_directly
        side_effect: direct_answer
      - to: escalate_to_human
        side_effect: handoff
  - id: spawn_subagents
    gate:
      type: deterministic
      func: pkg/weaver.routeByDomain
    transitions: [synthesizing, abort]
  - id: synthesizing
    gate:
      type: llm
      model_role: weaver-subagent
      input_schema: schemas/weaver_synth_input.json
      output_schema: schemas/weaver_synth_output.json
    transitions: [terminal_success, escalate_to_human]
  - id: escalate_to_human
    gate:
      type: approval
      hud_prompt: "Weaver router/subagent failed validation. Pick a transition."
      choices:
        - { to: respond_directly, label: "Answer with what we have" }
        - { to: terminal_failure, label: "Abort query" }
  - id: terminal_success
    terminal: true
    outcome: success
  - id: terminal_failure
    terminal: true
    outcome: failure
```

**GR-005: `pkg/llmfsm/workflow.go`** — adapter to `agent_workflow_*`.

- `WorkflowAdapter` interface: `EmitEvent`, `RequestApproval`, `WaitForApproval`, `MarkTerminal`.
- Default impl talks to the daemon via existing JSON-RPC, identical to how the MCP tools work today.
- Events use a `llmfsm.*` namespace: `llmfsm.gate.start`, `llmfsm.gate.invalid_output`, `llmfsm.gate.transition`, `llmfsm.fsm.complete`, `llmfsm.fsm.aborted`.

**GR-006: Metrics** (Prometheus, in `pkg/llmfsm/metrics.go`)

- `loom_llmfsm_gate_total{fsm,state,gate_type,outcome}` — counter
- `loom_llmfsm_gate_duration_seconds{fsm,state,gate_type}` — histogram
- `loom_llmfsm_gate_invalid_output_total{fsm,state,model}` — counter (LLM gates only)
- `loom_llmfsm_gate_repair_attempts{fsm,state}` — histogram
- `loom_llmfsm_fsm_runs_total{fsm,outcome}` — counter

**GR-007: Failure semantics** — strict cascade.

1. LLM returns invalid JSON or schema-invalid output → log + metric, retry with repair prompt (template prepends the validator error).
2. Repair attempts exhausted (`MaxRepairAttempts`, default 2) → emit `llmfsm.gate.invalid_output`, escalate to `ApprovalGate` for that state if defined.
3. No `ApprovalGate` defined → emit `llmfsm.fsm.aborted`, mark terminal with outcome `aborted`.
4. Approval timeout → emit `llmfsm.fsm.aborted` if no `on_timeout` transition declared.

No silent fallbacks. Every failure is observable.

### P1 — Migration adapters (one per caller, lands in S10–S12)

**GR-008**: `pkg/weaver/llmfsm_adapter.go` — convert existing router/subagent calls to `llmfsm` gates.

**GR-009**: `pkg/mills/audit/llmfsm_adapter.go` — audit pool models become `LLMGate`s with output schema `{verdict: enum, confidence: number, evidence: string}`.

**GR-010**: `pkg/mills/squads/llmfsm_adapter.go` — squad routing becomes `DeterministicGate` keyed on confidence threshold.

**GR-011**: `pkg/mills/council/llmfsm_adapter.go` — editor/reviewer/judge ensemble becomes a 3-state FSM. Highest risk, last to land.

## Acceptance

### Substrate (S9)

- `go test ./pkg/llmfsm/...` green; coverage > 80%.
- Round-trip integration test: define a 4-state FSM in YAML (LLM + deterministic + approval + terminal), run with mocked LLM client, assert correct event sequence on a fake `WorkflowAdapter`.
- Invalid-output integration test: LLM returns bad JSON twice, then valid; assert repair prompts fire and final transition lands.
- Approval-escalation test: LLM exhausts repairs, ApprovalGate fires, fake operator picks transition, FSM resumes.
- Schema-validation rejection test: FSM YAML with output schema missing `enum` on `to` field fails to load with clear error.

### Migration

- **S10 (Weaver)**: Weaver router/subagent calls flow through `llmfsm`. Existing Weaver integration tests pass without modification. New metrics emit. No regression in Weaver query latency p95 (>2x = block).
- **S11a (Mills audit)**: Audit verdicts emit through `llmfsm`. Audit pool soak (already advisory-only) shows no behavior change beyond metric emission.
- **S11b (Mills squads)**: Squad routing decisions emit `llmfsm.gate.transition` events. HUD shows squad selection in WorkflowPanel.
- **S12 (Mills council)**: Council runs visible as 3-state FSM in HUD. Council outputs identical to pre-migration baseline on a 24h replay.

## Risks

| Risk | Mitigation |
|------|------------|
| Schema validation overhead in hot paths | Compile schemas once at FSM load. Benchmark in S9 (`go test -bench`). Budget: <1ms p99 per validation. |
| LiteLLM JSON-mode coupling | `LLMClient` interface; FlexInfer adapter is one impl. Test fake is another. |
| Unbounded retry loops | `MaxRepairAttempts` hard cap (default 2, max 5). Approval timeout default 30min, configurable. |
| Mills council migration breaks subtle ensemble behavior | S12 includes 24h replay against pre-migration baseline before flip. Shadow mode like S5a if needed. |
| Two systems to learn (`agent_workflow_*` + `llmfsm`) | Substrate docs make the layering explicit: workflow tools = persistence; llmfsm = typed gates. HUD shows both as one stream. |
| Substrate becomes a kitchen sink | Non-goals enumerated; substrate scope is fixed at "gate, validate, transition, emit." Anything else (planning, memory, model routing) belongs in its existing package. |

## Decisions (resolved 2026-05-09)

1. **Side-effect execution — caller-owned.** Substrate stays pure: it picks the transition, validates it, emits the event. The caller registers a `SideEffectHandler` keyed by `side_effect` name when constructing the `Runner`. Handlers run synchronously between transitions; long-running effects are the caller's problem (spawn a goroutine, queue, etc.). This locks the layering: `pkg/llmfsm` knows nothing about FlexInfer, daemon RPC, or business logic — only `LLMClient` and `WorkflowAdapter` interfaces. See GR-001 / GR-005 in Changes.
2. **FSM versioning — pin at run-start.** Definitions are loaded at run-start and pinned for the lifetime of that run. If the YAML on disk changes mid-run, in-flight runs finish on the old definition; new runs use the new one. No live migration. This trades some operational complexity (you can have two definition versions concurrently in flight) for predictability (a run never fails because its FSM was edited under it). The runner records the FSM definition hash on the first emitted event so traces are interpretable after a redefinition.
3. **Cross-FSM composition — deferred to v2.** Out of scope for v1. Revisit after S12 if the Mills council debate-round loop feels awkward as a self-loop transition. If we add it, the path is `SubFSMGate` as a fourth gate type that runs a child `Runner` and surfaces its terminal outcome as the parent transition. Don't pre-build the API.
