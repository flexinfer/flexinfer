# Implementation Plan: `pkg/llmfsm` — Unified LLM-FSM Substrate

**Date**: 2026-05-09
**Product Spec**: `.loom/113-product-spec-llm-fsm-substrate-2026-05-09.md`

## Execution order (four slices)

```
S9:  pkg/llmfsm substrate (no callers)
        ↓
S10: Weaver router/subagent migration
        ↓
S11a: Mills audit pool migration   ┐
S11b: Mills squads router migration ├─ parallel after S10
                                    ┘
        ↓
S12: Mills council ensemble migration
```

S9 must land before any caller. S10 is the API validator on the simplest, lowest-blast-radius caller. S11a + S11b are independent and can ship in parallel. S12 lands last because the council is the highest-stakes path.

This plan **starts after the S5a Mills-research-via-Weaver soak completes** (1 week from commit `09ea87d8` on 2026-05-09 → ~2026-05-16). Substrate work in S9 can start in parallel with the soak; migrations in S10+ wait for the soak to finish so the audit/research telemetry baselines are stable.

Estimated effort: S9 ~3d, S10 ~2d, S11a ~2d, S11b ~1.5d, S12 ~4d (incl. 24h replay). Total: ~12 working days plus 1 week wait on S5a soak.

---

## S9 — `pkg/llmfsm` substrate

**Spec refs**: GR-001 through GR-007.

### Files

**`pkg/llmfsm/gate.go`** (new)

- `type Gate interface { Execute(ctx, GateInput) (Transition, error); Schema() GateSchema }`.
- `type GateInput struct { State StateID; Payload json.RawMessage; Metadata map[string]string }`.
- `type Transition struct { To StateID; SideEffects json.RawMessage; Confidence float64; Rationale string }`.
- `type LLMGate`, `type DeterministicGate`, `type ApprovalGate` implementations as in spec GR-001.
- `type LLMClient interface { Complete(ctx, model string, prompt string, schema *jsonschema.Schema) (json.RawMessage, error) }`.

**`pkg/llmfsm/runner.go`** (new)

- `type Runner struct { def *Definition; gates map[StateID]Gate; workflow WorkflowAdapter; sideEffects SideEffectRegistry; metrics *runnerMetrics }`.
- `func (r *Runner) Run(ctx context.Context, initialPayload json.RawMessage) (Outcome, error)`.
- Implements failure cascade per GR-007: invalid output → repair → escalate → abort.
- Emits `llmfsm.gate.start`, `llmfsm.gate.transition`, `llmfsm.gate.invalid_output`, `llmfsm.fsm.complete`, `llmfsm.fsm.aborted`.

**`pkg/llmfsm/schema.go`** (new)

- `func compileSchemas(def *Definition) (map[StateID]*compiledSchemas, error)`.
- Validator using `github.com/santhosh-tekuri/jsonschema/v5`.
- Loader rejects output schemas where `to` is not enum-restricted to declared transitions; clear error message points to the offending state.

**`pkg/llmfsm/yaml.go`** (new)

- `func LoadFSM(path string) (*Definition, error)`.
- `type Definition struct { Name string; Description string; States []StateDef; SideEffects []SideEffectDef }`.
- `type StateDef struct { ID StateID; Gate GateConfig; Transitions []TransitionDef; Terminal bool; Outcome string }`.
- Validates: every `to` in transitions points to a declared state; every terminal state has an outcome; LLM gates have prompt + input_schema + output_schema; approval gates have at least one choice.

**`pkg/llmfsm/workflow.go`** (new)

- `type WorkflowAdapter interface { EmitEvent(...); RequestApproval(...); WaitForApproval(...); MarkTerminal(...) }`.
- `type DaemonAdapter struct { client *daemon.Client }` — production impl, talks to existing daemon JSON-RPC.
- `type FakeAdapter struct { events []Event; approvals chan Approval }` — test impl, exposed for callers to write integration tests.

**`pkg/llmfsm/sideeffects.go`** (new)

- `type SideEffectRegistry struct { handlers map[string]SideEffectHandler }`.
- `type SideEffectHandler func(ctx context.Context, args json.RawMessage) error`.
- Substrate calls handler after a transition is committed but before the next gate runs.

**`pkg/llmfsm/metrics.go`** (new)

- All counters/histograms per GR-006.

**`pkg/llmfsm/client_flexinfer.go`** (new)

- `type FlexInferClient struct { proxy *flexinfer.Proxy; resolver *aimodels.Resolver }`.
- Implements `LLMClient` using LiteLLM `response_format: { type: "json_schema", schema: ... }`.
- Falls back to `response_format: { type: "json_object" }` + post-validation when proxy lacks schema-mode support.

**`pkg/llmfsm/testdata/`** (new)

- `weaver_demo_fsm.yaml` — minimal 4-state FSM exercising all three gate types.
- `schemas/*.json` — input/output schemas for the demo FSM.

### Tests

**`pkg/llmfsm/runner_test.go`**

- Happy path: LLM gate → deterministic gate → terminal_success.
- Invalid output → repair → success: fake client returns bad JSON once, valid second time. Assert one `llmfsm.gate.invalid_output` event, then `llmfsm.gate.transition`.
- Repair exhausted → approval escalation: fake client always returns invalid, fake adapter approves a fallback transition.
- Approval timeout: `TimeoutSec=1`, no approval comes, assert `llmfsm.fsm.aborted` with `outcome=timeout`.
- Side-effect execution: register handler, run FSM, assert handler called with correct args.

**`pkg/llmfsm/yaml_test.go`**

- Loader rejects output schema without enum on `to`.
- Loader rejects transition pointing at undeclared state.
- Loader accepts canonical demo FSM.

**`pkg/llmfsm/schema_bench_test.go`**

- `BenchmarkValidate` — assert <1ms p99 per validation on a representative payload (10 fields, 3-deep nesting).

### Acceptance

- `go test ./pkg/llmfsm/...` green; `-cover` ≥ 80%.
- `golangci-lint run pkg/llmfsm/...` clean.
- Bench p99 < 1ms.
- No regressions in `go test ./...`.

---

## S10 — Weaver router migration

**Spec refs**: GR-008.

### Files

**`pkg/weaver/llmfsm_adapter.go`** (new)

- `func (r *Router) buildFSM() *llmfsm.Definition` — constructs Weaver's router/subagent FSM in Go (not YAML for v1; YAML version follows in S12 docs slice).
- States: `query_received` (LLM router gate) → `routing_decided` (deterministic dispatch) → `subagent_running` (LLM gate per subagent, parallel) → `synthesizing` (LLM gate) → terminal.
- Side-effect handlers: `spawn_subagent`, `direct_answer`, `handoff_to_human`.

**`pkg/weaver/router.go`** (modify)

- `Query(ctx, input) (*Result, error)` now constructs and runs an FSM via `pkg/llmfsm.Runner` instead of inline LLM calls.
- Old code path retained behind feature flag `WEAVER_LLMFSM_MODE` (`off` | `shadow` | `on`), mirroring the S5a pattern.
- Shadow mode: runs both, records latency + output diff, returns legacy result.
- On mode: returns FSM result; falls back to legacy on FSM init error (not on FSM-internal error — those are real failures).

### Tests

**`pkg/weaver/llmfsm_adapter_test.go`**

- FSM construction is well-formed (loader validates).
- Mock LLM client + fake workflow adapter: golden-file compare against legacy Router output.
- Shadow mode: both paths called, diff computed, legacy returned.

### Acceptance

- All existing `pkg/weaver/...` tests pass.
- New adapter tests green.
- Shadow mode soak: 48h on dev cluster, <5% diff rate, latency p95 within 1.2x of legacy.
- Flip to `on` after soak.

### Files to add metrics for

- `loom_weaver_llmfsm_diff_total{kind}` — kind ∈ {output, latency, error}.
- `loom_weaver_llmfsm_mode` — gauge, value ∈ {0=off, 1=shadow, 2=on}.

---

## S11a — Mills audit pool migration

**Spec refs**: GR-009.

### Files

**`pkg/mills/audit/llmfsm_adapter.go`** (new)

- Each audit model becomes an `LLMGate` with output schema:
  ```json
  {
    "type": "object",
    "properties": {
      "to": { "enum": ["audit_pass", "audit_fail", "audit_uncertain"] },
      "side_effects": {
        "type": "object",
        "properties": {
          "verdict": { "enum": ["pass", "fail", "uncertain"] },
          "confidence": { "type": "number", "minimum": 0, "maximum": 1 },
          "evidence": { "type": "string", "maxLength": 500 }
        },
        "required": ["verdict", "confidence", "evidence"]
      }
    },
    "required": ["to", "side_effects"]
  }
  ```
- FSM per audit model run; results aggregated by existing `pkg/mills/audit/aggregator.go` (unchanged).

**`pkg/mills/audit/runner.go`** (modify)

- `RunAudit(ctx, target)` constructs an FSM per model, runs them in parallel, aggregates verdicts.
- Feature flag `MILLS_AUDIT_LLMFSM=on|off`, default `off` until 48h shadow on dev.

### Tests

- Existing audit tests pass.
- Verdict structure unchanged from caller's perspective (aggregator API stable).
- Invalid-output handling: a model that returns malformed JSON once recovers via repair; if it persists, audit run records `verdict=invalid` (does not block, since pool is advisory).

### Acceptance

- Audit pool runs continue to be advisory in production.
- Shadow on dev: verdict distributions identical to pre-migration baseline (within noise floor, <2% drift on `pass/fail/uncertain` ratios).
- Flip flag to `on`, monitor for 48h.

---

## S11b — Mills squads router migration

**Spec refs**: GR-010.

### Files

**`pkg/mills/squads/llmfsm_adapter.go`** (new)

- Squad selection becomes a `DeterministicGate`:
  ```go
  func routeByConfidence(ctx context.Context, in llmfsm.GateInput) (llmfsm.Transition, error) {
      var payload struct {
          SquadConfidences map[string]float64 `json:"squad_confidences"`
      }
      _ = json.Unmarshal(in.Payload, &payload)
      best, score := pickBest(payload.SquadConfidences)
      if score < policy.MinConfidence {
          return llmfsm.Transition{To: "_default"}, nil
      }
      return llmfsm.Transition{
          To: llmfsm.StateID("squad_" + best),
          SideEffects: marshalArgs(routeArgs{Squad: best, Score: score}),
      }, nil
  }
  ```
- Wired into Mills v2 router as a single-state FSM (router + N terminal states, one per squad + `_default`).

**`pkg/mills/squads/router.go`** (modify)

- `Route(ctx, work)` invokes the FSM. Output is the squad assignment; existing Mills runner consumes it identically.
- No feature flag needed — deterministic gate is verifiably equivalent to the existing function. Direct cutover with unit-test parity.

### Tests

- Parity test: 1000 generated routing inputs, assert FSM output == legacy `Route` output for every case.

### Acceptance

- Parity test passes.
- HUD WorkflowPanel shows squad selection events on Mills runs.

---

## S12 — Mills council ensemble migration

**Spec refs**: GR-011. Highest risk; lands last.

### Files

**`pkg/mills/council/llmfsm_adapter.go`** (new)

- Council becomes a 3-state FSM:
  - `editor_proposing` (LLMGate, role `mills-judge` with `editor` sub-prompt) → `reviewer_critiquing`
  - `reviewer_critiquing` (LLMGate, parallel reviewers) → `judge_deciding`
  - `judge_deciding` (LLMGate) → `terminal_accept` | `terminal_reject` | `terminal_debate_round`
- Debate-round loop: `terminal_debate_round` is non-terminal; transitions back to `editor_proposing` with debate context appended. Capped at `policy.MaxDebateRounds`.

**`pkg/mills/council/ensemble.go`** (modify)

- `RunCouncil(ctx, proposal)` constructs the 3-state FSM, runs it, returns the council outcome.
- Feature flag `MILLS_COUNCIL_LLMFSM=off|shadow|on`. Shadow mode required for 1 week before flipping to `on`.

**`pkg/mills/council/replay.go`** (new)

- `func ReplayCouncilDay(ctx, dayStart time.Time) (DiffReport, error)` — replays the last 24h of council runs through the new FSM, compares verdict + chosen-action match rate.
- Used as gating signal for flip from `shadow` to `on`.

### Tests

- FSM structure validates.
- Mock client + golden-file compare against existing council outputs for a fixed seed.
- Replay test against recorded production council runs (under `pkg/mills/council/testdata/replay/`).

### Acceptance

- Shadow mode for 7 days on production traffic.
- Replay agreement ≥ 95% verdict-match, ≥ 90% chosen-action match.
- No latency regression beyond 1.5x p95.
- Flip to `on` only after replay agreement confirmed for 3 consecutive days.

---

## Cross-cutting

### Documentation

- Update `services/loom-core/AGENTS.md` to reference `pkg/llmfsm` after S9.
- Add `docs/architecture/llmfsm.md` after S10 (real example to point to).
- Add HUD WorkflowPanel screenshot to spec doc after S11b.

### Rollout staging

- All slices: implement → unit tests → dev cluster → 48h shadow (where applicable) → flip flag.
- Mills council (S12): production shadow required, not dev.

### Rollback

- Each migrated caller keeps its pre-migration code path behind the feature flag for one full release after flip to `on`.
- Substrate (`pkg/llmfsm`) is additive — no rollback needed.

### Open work that may shake out during S9–S12

- Sub-FSM composition (out of spec scope; revisit if S12 council debate-round loop feels awkward).
- HUD authoring UI for FSM YAML (out of scope; might be valuable post-S12).
- FSM definition versioning + migration (out of scope; pin-at-run-start is enough for v1).
