---
title: Context-Curve Scheduler Spec
description: Spec capsule for using stored context-curve evidence to inform proxy/scheduler routing decisions, with a backtest kill-test before any runtime code lands.
---

# Context-Curve Scheduler Spec

Tracking:
- Issue: none yet
- Roadmap item: `docs/planning/next-roadmap.md` (Context-Curve Benchmarking) and
  `.loom/roadmap-unblock-plan-2026-05-21.md` (Lane 4)
- Owner: RALPH loop
- Status: Ready (for kill-test work; runtime code blocked behind kill-test pass)

## Goal

When two or more lanes can serve the same workload, FlexInfer picks the lane
whose measured context curve gives the operator the best latency at the actual
prompt length of the request — instead of picking by static service-labels and
round-robin alone.

A long-prompt agent request should not silently land on a lane whose decode
throughput collapses past 4k tokens when a sibling lane has the same advertised
service label and a flatter curve.

## Riskiest assumption + kill-test

**Load-bearing assumption**: A two-point curve (2048 and 8192 tokens) per lane,
captured via `scripts/bench-context-curve.sh` and stored in the
`flexinfer-context-curve-results` ConfigMap, is enough signal to make routing
decisions that **measurably improve** p95 decode latency for long-prompt
requests vs. the current "service-label match + round-robin" policy,
**without** introducing route flapping, oscillation, or measurable harm to
short-prompt latency.

**Kill test**: Offline backtest, no runtime changes. A Python script:

1. Reads every report in `flexinfer-context-curve-results` (currently two:
   `gemma4-26b-a4b-gptq` and `qwen3-8b-radeonvii-soak`).
2. Generates a synthetic request mix:
   - 80 short requests with prompt token counts uniformly in [256, 2048]
   - 20 long requests with prompt token counts uniformly in [4096, 14000]
3. For each request, computes simulated total latency under two policies:
   - **baseline**: uniform-random over both lanes (rough approximation of
     round-robin under a single shared service label)
   - **curve-aware**: pick the lane whose linearly-interpolated decode
     throughput at the request's prompt length is highest, with deterministic
     tiebreaks
4. Latency model per request:
   `elapsed ≈ prompt_tokens / prefill_tps(lane, prompt_len) +
              completion_tokens / decode_tps(lane, prompt_len)`
   where `prefill_tps` and `decode_tps` are linearly interpolated from the
   lane's curve points and clamped at the highest observed point.
5. Reports:
   - p95 elapsed for the short subset under each policy
   - p95 elapsed for the long subset under each policy
   - lane-distribution histogram under each policy
   - per-lane average prompt length under each policy

**Pass criteria** (all must hold):

- Curve-aware p95 elapsed on the **long subset** ≥ 20% lower than baseline.
- Curve-aware p95 elapsed on the **short subset** ≤ 5% higher than baseline.
- Neither lane receives 0 requests or 100% of requests under curve-aware
  routing for either subset (rules out trivial degenerate splits).

**Failure mode if the assumption is wrong**: We would land scheduler/proxy code
that adds complexity and a new dependency on ConfigMap data without measurable
operator benefit. Worse, if the two-point interpolation does not reflect
real-world behavior between 2k and 8k, the routing decision could make tail
latency *worse* by sending long requests to the lane that benchmarked poorly at
8k but actually serves 12k better — and we would only discover this from
operator complaints, not the spec.

**Pair with positive/negative search**:

- Positive: confirm that linear interpolation between two measured points is a
  defensible scheduler input. Search vLLM/llama.cpp benchmarking guidance and
  any published prefill/decode curves at adjacent context sizes.
- Negative: confirm we are not assuming a smooth curve where the real behavior
  has step changes (e.g., paged-attention block boundaries, KV-cache
  quantization mode changes). Search for "decode throughput cliff context
  length" or "KV cache quant mode boundary".

**Status**: not run.

## Non-Goals

- No scheduler or proxy code in this spec slice. Code lands only after the
  kill-test passes.
- No CRD changes. The scheduler/router input is a ConfigMap, the same one used
  by the existing reporting-only MVP.
- No new RBAC. The proxy already reads ConfigMaps in its namespace.
- No new benchmark surface. The data source is the existing
  `flexinfer-context-curve-results` ConfigMap.
- No "always-on" curve enforcement. Curve-aware routing must be opt-in per
  Model/route until a second kill-test (live A/B) proves stability under real
  traffic.
- Does not replace prefix-aware routing (CAR-1..CAR-5). Curve-aware routing is
  a different signal; the two are composable later (curve scores within a
  prefix bucket) but not in this slice.

## Users / Operators

- Operators running mixed short/long prompt traffic across heterogeneous lanes
  (e.g., gfx1100 + gfx906) who today rely on service-labels and accept the
  latency variance.
- Operators promoting a new runtime image or hardware lane who want to land on
  a quantitative routing decision instead of "ship and watch".
- Agents (us) building scheduler scoring policy: curve data becomes a stable
  input contract rather than an ad-hoc per-incident measurement.

## Current Evidence

- Two curves live in `flexinfer-context-curve-results`:
  - `gemma4-26b-a4b-gptq` (2026-05-22): 2k prefill 1756 tok/s, 2k decode 12.2
    tok/s, 8k prefill 1471 tok/s, 8k decode 2.62 tok/s
    (`.loom/60-validation-matrix.md` section
    `2026-05-22 context-curve MVP: gemma4-26b-a4b-gptq`).
  - `qwen3-8b-radeonvii-soak` (2026-05-25): 2k prefill 1245 tok/s, 2k decode
    42.72 tok/s, 8k prefill 884 tok/s, 8k decode 7.76 tok/s
    (`.loom/60-validation-matrix.md` section
    `2026-05-25 context-curve 2nd family: qwen3-8b-radeonvii-soak`).
- Curve runner + storage are reporting-only and remain so until the kill-test
  passes (`docs/planning/context-curve-benchmark.md:24`).
- The existing context-aware routing (CAR-1..CAR-5) handles prefix/cache-key
  affinity but does not look at context length when picking among siblings
  (`docs/planning/context-aware-router-execution.md:26`).
- Proxy already reads per-Model Service annotations and Service-label routing
  state from informer caches (`internal/proxy/routing.go`).
- Existing scheduler scoring path: `agents/benchmarker/store.go` and the
  proxy's least-loaded/prefix/session policies in `internal/routing/`.

## Requirements

- Functional:
  - Define a per-lane "curve score" function: given a lane's most-recent
    report and a request's prompt length, return a scalar throughput estimate
    (or `unknown` when no curve is stored).
  - Define a routing policy that consumes the score and picks among lanes
    sharing a service label / model alias.
  - Specify staleness behavior: a curve older than N hours is treated as
    `unknown` for that lane, falling back to the existing policy.
  - Specify the opt-in mechanism per Model/route (annotation or routing-mode
    enum value).
- Operational:
  - The policy must be deterministic for identical inputs.
  - The policy must degrade to the existing policy when no curve exists for
    any candidate lane.
  - Operators can disable the policy with a single annotation flip.
- Observability/status:
  - Proxy logs the routing decision input (`policy`, `curve_score_per_lane`,
    `chosen_lane`, `prompt_tokens`).
  - Prometheus counter `flexinfer_proxy_curve_routing_decisions_total{policy,
    fallback_reason}` increments per decision.
  - `flexinfer_proxy_curve_score{model,context_bucket}` gauge surfaces the
    score per lane per context bucket for ad-hoc dashboards.
- Compatibility:
  - The existing service-label + prefix + session routing modes remain
    unchanged when curve routing is off.
  - The opt-in mechanism MUST be additive (annotation default-off).
- Security/RBAC:
  - No new permissions. The proxy already reads ConfigMaps in its namespace.

## Acceptance Criteria

- [ ] **Kill-test runner exists** under `scripts/sim-curve-router.py` (or a
      `cmd/flexinfer-bench/curve-sim/` Go tool — whichever matches the
      existing benchmarker structure) and reads from the live ConfigMap.
- [ ] **Kill-test report** with all three pass criteria checked is committed
      under `.loom/local/validation/context-curve/<date>/sim-report.json` (raw)
      and summarized in `.loom/60-validation-matrix.md`.
- [ ] **Spec doc is `Ready`** with kill-test status updated to either
      `passed YYYY-MM-DD` or `FAILED YYYY-MM-DD` plus the evidence link.
- [ ] **Decision rule for next slice** recorded: pass = open CC-6
      proxy-implementation slice; fail = open a CC-6a "improve curve
      resolution" slice instead.
- [ ] Scheduler/proxy code remains untouched in this spec slice.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| CC-5 spec capsule | `docs/planning/context-curve-scheduler-spec.md`, `docs/planning/next-roadmap.md`, `docs/planning/context-curve-benchmark.md` link to follow-up | Planning only | `git diff --check`; `rg "context-curve-scheduler"` | Revert docs-only commit |
| CC-6 kill-test runner | `scripts/sim-curve-router.py`, `.loom/local/validation/context-curve/<date>/` | Offline backtest only; no runtime code | Run script against live ConfigMap, validate JSON shape, evaluate pass criteria | Delete script |
| CC-7 proxy curve-routing (conditional) | `internal/proxy/routing.go`, `internal/proxy/proxy.go`, `internal/proxy/proxy_test.go` | Proxy only; no controller/CRD changes | `go test ./internal/proxy/...`; smoke through proxy with curve-aware annotation on `qwen3-8b-radeonvii-soak` ↔ another lane | Flip default-off annotation; revert proxy commit |
| CC-8 live A/B (conditional, after CC-7) | proxy logs, Prometheus, `.loom/60-validation-matrix.md` | Observation only | Compare measured p95 decode under two policies on actual traffic | Annotation flip to disable |

Slices CC-7 and CC-8 are explicitly conditional on the CC-6 kill-test passing.
A failed kill-test reopens the spec, not the implementation.

## Agent Delegation Notes

| Workstream | Safe-to-edit files/modules | Do not touch | Local verification | Expected output/signals |
|------------|----------------------------|--------------|--------------------|-------------------------|
| Spec authoring (this MR) | `docs/planning/context-curve-scheduler-spec.md`, `docs/planning/next-roadmap.md`, `docs/planning/context-curve-benchmark.md` (link only) | `internal/`, `controllers/`, `scheduler/`, CRDs, runtime Dockerfiles | `git diff --check`; `rg "context-curve-scheduler\|CC-5\|CC-6"` | Spec is internally consistent; kill-test is concrete |
| Kill-test runner (CC-6, next MR) | `scripts/sim-curve-router.py`, `.loom/local/validation/context-curve/<date>/` | proxy, scheduler, CRDs | `python3 scripts/sim-curve-router.py --help`; live run against ConfigMap; JSON shape check | Pass/fail verdict against the three criteria above |
| Proxy implementation (CC-7, conditional) | `internal/proxy/`, `cmd/flexinfer-proxy/`, proxy tests | controllers, CRDs, runtime Dockerfiles, scheduler agent | `go test ./internal/proxy/...`; smoke run | Tests cover the opt-in annotation + fallback behavior |

Coordination notes:

- Shared contract: the ConfigMap key shape `<model>-context-curve-<run-id>.json`
  and the report JSON `flexinfer.context_curve.v1` schema are stable and owned
  by the CC-1..CC-4 runner. Do not change the schema in this slice.
- Merge order: CC-5 (this) → CC-6 → CC-7/CC-8 (only if CC-6 passes).
- Conflict risks: any change to `scripts/bench-context-curve.sh` JSON schema
  blocks CC-6. Coordinate with the runner owner if the schema needs to
  evolve.

## Readiness

Status: Ready (spec capsule), Blocked (CC-7+ until kill-test passes)

- Target files/modules: `docs/planning/context-curve-scheduler-spec.md`,
  roadmap link, benchmark closeout link.
- Owner boundary: planning only in this slice.
- Validation commands: `git diff --check`; `rg "context-curve-scheduler"
  docs/planning`.
- Generated artifacts: none.
- Rollout/backout: revert the docs-only commit.
- Non-blocking open questions:
  - Should the curve score interpolation be linear, log-linear, or
    nearest-point? Defer to CC-6 evidence; the kill-test should try at least
    two and report which one met the pass criteria.

## Validation Plan

Spec slice:

```bash
git diff --check
rg "context-curve-scheduler|CC-5|CC-6" docs/planning .loom
```

Kill-test slice (CC-6, future MR):

```bash
python3 scripts/sim-curve-router.py \
  --configmap flexinfer-context-curve-results \
  --namespace flexinfer-system \
  --short-prompts 80 --long-prompts 20 \
  --short-range 256,2048 --long-range 4096,14000 \
  --seed 20260525 \
  --report .loom/local/validation/context-curve/<date>/sim-report.json
python3 -m json.tool .loom/local/validation/context-curve/<date>/sim-report.json >/dev/null
```

Proxy slice (CC-7, conditional):

```bash
go test ./internal/proxy/... -count=1
go vet ./internal/proxy/...
```

## Rollout / Backout

- Rollout: ship this spec → run CC-6 kill-test → decide CC-7 vs CC-6a.
- Backout: spec slice is docs-only, `git revert` the commit.
- Risk controls:
  - Kill-test is the gate. Do not ship proxy code without a passed kill-test
    linked in the validation matrix.
  - When CC-7 ships, curve routing is annotation-opt-in per Model, default
    off. A single annotation flip disables it.

## Open Questions

- [ ] Should the curve score interpolation be linear, log-linear, or
      nearest-point? Defer to CC-6 evidence.
- [ ] Should staleness threshold default to 24h, 7d, or "never stale until
      manually invalidated"? Defer to CC-7 spec sub-slice.
- [ ] Should curve routing prefer the highest-decode lane, or the lane closest
      to a configured per-route SLO? Defer to CC-7 once we know whether the
      kill-test pass holds for a simple max-throughput rule.

## Sources

- `docs/planning/context-curve-benchmark.md` (parent spec; CC-1..CC-4 closed)
- `docs/planning/context-aware-router-execution.md` (related but orthogonal)
- `docs/planning/next-roadmap.md` (Context-Curve Benchmarking section)
- `.loom/roadmap-unblock-plan-2026-05-21.md` (Lane 4)
- `.loom/60-validation-matrix.md` sections
  `2026-05-22 context-curve MVP: gemma4-26b-a4b-gptq` and
  `2026-05-25 context-curve 2nd family: qwen3-8b-radeonvii-soak`
- `scripts/bench-context-curve.sh` (runner producing `flexinfer.context_curve.v1`)
- `internal/proxy/routing.go` (current routing decision surface)
- `internal/routing/router.go` (least-loaded/prefix/session modes)
- Global rule: `~/.claude/rules/spec-riskiest-assumption.md`
