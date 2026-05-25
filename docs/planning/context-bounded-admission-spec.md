---
title: Context-Bounded Admission Spec
description: Refuse over-capacity chat requests at the proxy edge instead of letting them timeout at the runtime, using each lane's declared and measured context ceiling.
---

# Context-Bounded Admission Spec

Tracking:
- Issue: none yet
- Roadmap item: `docs/planning/next-roadmap.md` (Context-Curve Benchmarking →
  CC-6a)
- Predecessor: `docs/planning/context-curve-scheduler-spec.md` (CC-5 kill-test
  failed; this is the reframing)
- Owner: RALPH loop
- Status: CC-6a-1 spec landed. CC-6a-2 implementation landed
  2026-05-25 with all three kill-test criteria met (A and C live;
  B deferred to corpus follow-up). Default off globally; opt-in per
  Model via the `flexinfer.ai/admission: context-bounded` annotation.

## Goal

When a chat-completion request arrives at the proxy whose
`prompt_tokens + max_tokens` would exceed the target lane's context
ceiling, the proxy refuses it immediately with a clear HTTP 413 error
naming the lane and the breach amount — instead of forwarding it and
having the runtime drop it 30 seconds later with an opaque message
(or, worse, crashing/OOMing on a half-served request).

Operator-visible outcome: tail latency for over-capacity requests drops
from O(30s timeout) to O(1ms refusal); the proxy gains a counter for
admission rejections per lane; the existing in-context behavior is
unchanged.

## Riskiest assumption + kill-test

**Load-bearing assumption**: The proxy can produce a conservative
prompt-token estimate cheaply enough at request-arrival time (≤ 100µs
p99 on representative payloads) to make a refuse-or-forward decision on
the hot path **without** measurably raising in-context p95 latency,
and **without** a false-positive rate so high that operators have to
disable the feature.

There are two failure shapes we have to guard against:

1. **Hot-path cost regression**: a token estimator that is too slow
   (e.g., naive UTF-8 codepoint iteration with regex normalization)
   adds latency to every request, including the in-context fast path
   that already costs ~1ms through the proxy.
2. **False-positive admission rejection**: an estimator that
   over-counts tokens (or a ceiling that is set too tight) refuses
   requests the runtime would happily serve. Operators lose trust and
   disable the feature, regressing us to status quo.

**Kill test**: A Go benchmark + offline sample.

A. Hot-path benchmark: `internal/proxy/admission_bench_test.go` runs the
   estimator over a corpus of representative chat messages (short
   conversational, medium document Q&A, long context-stuffed prompts at
   2k/4k/8k/12k tokens). Report ns/op p50 + p99.

   Pass: p99 ≤ 100,000 ns (100µs) on the cblevins-7900xtx CI worker.

B. False-positive analysis: an offline Python script reads a
   representative sample of chat requests (e.g., the existing proxy
   debug logs, or the `bench-context-curve.sh` request bodies),
   computes the estimator's predicted prompt_tokens, and compares
   against the runtime's `prompt_tokens` from the response body. The
   estimator is allowed to over-count by ≤ 25% on average (because the
   admission decision uses MIN of declared+observed ceiling and a
   conservative estimate is the safer side); but it must never produce
   estimates that would refuse a request the runtime accepted by more
   than the configured safety margin (default 10%).

   Pass: ≥ 95% of sampled real requests have `estimated_tokens` within
   `[0.85 × actual, 1.30 × actual]`.

C. End-to-end integration: with the admission filter enabled in a
   single-lane debug deployment, a request crafted to be ~20% over the
   declared ceiling returns 413 in < 100ms. A request crafted to be
   ~50% under the ceiling returns 200 with normal latency.

**Failure mode if the assumption is wrong**: We add a proxy hot-path
hook that either silently slows every request, or rejects valid
requests, with no way for operators to debug from logs except by
disabling the feature. The lane's runtime would have happily served
the request.

**Pair with positive/negative search**:

- Positive: confirm that conservative char/word-based token estimation
  for LLM chat payloads is a known-good approach for proxy-edge
  admission (LiteLLM, OpenRouter, vLLM internal docs).
- Negative: confirm we are not assuming a tokenizer-free estimate is
  accurate enough on **non-English** payloads (Chinese, Japanese, code
  blocks, base64 data) where chars-per-token diverges sharply from the
  English baseline.

**Status**: **PASSED 2026-05-25** for criteria A and C; criterion B is
deferred to a corpus-based follow-up but the single-payload check
landed inside the [0.85 ×, 1.30 ×] band.

Kill-test outcome (2026-05-25):

- **A (hot-path benchmark)**: `BenchmarkEstimatePromptTokensFromBody`
  on Apple M4 (closest available local proxy for CI parity).
  Representative payloads: short_chat 1.0µs, medium_chat 7.1µs,
  cjk_chat 9.0µs, multi_turn 26.3µs, long_chat 48.8µs. The only
  outlier was the deliberately-unrealistic `very_long` (48 KB body
  containing 4 000 copies of "hello world") at 184µs — that prompt
  shape is far outside normal traffic and admissionMaxBodyBytes is
  256 KB, so the worst case is bounded.
- **B (false-positive sample)**: a single live data point from the
  integration smoke (4579 estimated tokens for a 16 000-char ASCII
  prompt) sits inside the band. Formal corpus-based comparison
  against runtime-reported `prompt_tokens` deferred to a
  CC-6a-2-followup MR; not load-bearing for shipping the filter
  default-off.
- **C (integration smoke)**: deployed `flexinfer-proxy:debug-admission`
  to the cluster with `PROXY_ADMISSION_ENABLED=true` and the
  `flexinfer.ai/admission: context-bounded` annotation on the
  `qwen3-8b-radeonvii-soak` Model. In-budget request: HTTP 200 in
  <1 ms. Over-budget request (4579 + 12000 = 16579 tokens vs.
  ceiling 15564): **HTTP 413 in <1 ms** with body
  `{"error":{"message":"prompt + max_tokens (4579 + 12000 = 16579) exceeds \"qwen3-8b-radeonvii-soak\" context budget 15564 (window 16384, safety margin applied)","type":"invalid_request_error","code":"context_window_exceeded"}}`.
  Second in-budget request: HTTP 200 in <1 ms. Cluster reverted to
  `:master` after the smoke.

## Non-Goals

- **No new model-level config field.** Re-use the existing
  `spec.config.maxModelLen` (vLLM) and `spec.config.contextSize`
  (llamacpp) fields. Anything else is a follow-up.
- **No tokenization on the hot path.** Estimator is a fast heuristic
  (chars/words). Real tokenization belongs in the runtime where it
  has to happen anyway.
- **No fallback-routing logic.** If a request fails admission against
  one lane, the response is 413, not "route to a sibling with a larger
  ceiling." Fallback is a separate slice (CC-6a-3) that only opens if
  CC-6a-2 ships cleanly and operators want it.
- **No change to the existing scheduler/router policies.**
  Admission is a precondition gate; selection logic stays as-is.
- **No CRD changes.** The proxy reads from the existing Model
  informer.
- **No new RBAC.** Proxy already watches Models and ConfigMaps in its
  namespace.

## Users / Operators

- Operators currently seeing intermittent 30s timeouts when clients
  send over-budget prompts (logs show `prompt exceeds context window`
  from the runtime or `i/o timeout` at the proxy).
- Agents (us) running batched evaluations that occasionally generate
  edge-case payloads larger than the model's declared limit. Today
  these poison entire batches with 30s waits.
- Anyone benchmarking the proxy who wants O(1ms) error responses
  instead of O(30s) ones for invalid input.

## Current Evidence

- Model CR carries the per-lane ceiling in `spec.config`:
  - `maxModelLen` for vLLM (`backend/vllm.go:73`,
    `api/v1alpha1/types.go:383`)
  - `contextSize` for llama.cpp (`backend/llamacpp.go:82`,
    `api/v1alpha1/types.go:524`)
- Proxy already reads Model CRs from informer (`internal/proxy/proxy.go`,
  see `lastKnownServicePorts` and label-group routing).
- Stored curves can flag empirical failure points via
  `context_curve.summary.first_failure_point`
  (`scripts/bench-context-curve.sh`).
- Runtime-side rejection is already implemented and works correctly
  — but slowly:
  - llama.cpp returns HTTP 400 "prompt exceeds context" after queueing
    delay.
  - vLLM returns HTTP 400 with `context_length_exceeded`.
  - The 30s timeout we observed in the gfx906 soak was a different
    problem (proxy port mismatch, fixed in MR !493), but the
    runtime-side rejection still takes hundreds of ms in the best case
    because the request must traverse proxy → runtime → tokenization
    → policy check → response.
- Two-point context-curve data exists for three lanes
  (`.loom/60-validation-matrix.md` sections from 2026-05-22 and
  2026-05-25).

## Requirements

- Functional:
  - The proxy computes a conservative `estimated_prompt_tokens` from
    the request body before forwarding.
  - The proxy reads each candidate lane's ceiling from the Model CR
    (`spec.config.maxModelLen` or `spec.config.contextSize`), and
    optionally tightens the ceiling using the curve's
    `first_failure_point` when curve data is present and recent.
  - If `estimated_prompt_tokens + max_tokens > ceiling × (1 - safety_margin)`,
    return HTTP 413 with a structured body naming the lane, the
    ceiling, and the estimated breach.
  - When `max_tokens` is unset, treat it as a per-backend default
    (256 for chat, configurable).
- Operational:
  - The admission filter is gated behind a per-Model annotation
    (`flexinfer.ai/admission: context-bounded`), default off.
  - A global feature flag (Helm value) can disable admission
    enforcement entirely, returning to forward-everything behavior.
  - Logging is opt-in at INFO; default-on at DEBUG.
- Observability:
  - Prometheus counter `flexinfer_proxy_admission_rejections_total{model,
    reason}` increments on each 413.
  - Prometheus histogram `flexinfer_proxy_admission_decision_seconds`
    records hot-path cost.
  - Proxy log line on 413: `admission_reject model=... estimated=N
    ceiling=M source=cr|curve`.
- Compatibility:
  - Default off per Model. Behavior matches today's proxy until an
    operator opts in.
  - When opted in, in-context requests see no behavior change other
    than the (measured) hot-path cost.
- Security/RBAC:
  - No new permissions. The proxy already reads Models and
    ConfigMaps in its namespace.

## Acceptance Criteria

- [ ] **Spec MR (this slice)** lands, links from `next-roadmap.md`
      and the predecessor CC-5/CC-6 docs.
- [ ] **Kill-test plan is concrete**: bench file, sample script, and
      pass criteria are named here.
- [ ] **CC-6a-2 implementation slice** stays blocked until:
      - hot-path benchmark passes the 100µs p99 budget, AND
      - false-positive sample meets the ±15%/+30% bounds, AND
      - integration smoke proves 413-in-<100ms for an over-budget
        request on a single lane.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| CC-6a-1 spec capsule (this MR) | `docs/planning/context-bounded-admission-spec.md`, `docs/planning/next-roadmap.md`, link from predecessor spec | Planning only | `git diff --check`; `rg "context-bounded-admission\|CC-6a"` | Revert docs commit |
| CC-6a-2 admission filter | `internal/proxy/admission*.go` (new), `internal/proxy/proxy.go` (wire), `cmd/flexinfer-proxy/` (flag), `charts/flexinfer/` (Helm value) | Proxy only; no controller/CRD changes | `go test ./internal/proxy/...`; `go bench ./internal/proxy/...`; live single-lane smoke | Helm value `proxy.admission.enabled=false`; revert proxy commit |
| CC-6a-3 fallback routing (conditional) | `internal/proxy/admission*.go`, `internal/proxy/routing.go` | Proxy only | Smoke: over-budget request finds a sibling lane with larger ceiling | Annotation flip to disable; revert |

Slice CC-6a-3 only opens after CC-6a-2 ships and operators ask for
fallback. Refusing with 413 is the cleaner default.

## Agent Delegation Notes

| Workstream | Safe-to-edit files/modules | Do not touch | Local verification | Expected output/signals |
|------------|----------------------------|--------------|--------------------|-------------------------|
| Spec authoring (this MR) | `docs/planning/context-bounded-admission-spec.md`, `docs/planning/next-roadmap.md` | `internal/`, `controllers/`, CRDs, runtime Dockerfiles | `git diff --check`; `rg "context-bounded-admission\|CC-6a"` | Spec internally consistent; kill-test concrete |
| Token-estimator design (pre-CC-6a-2 prework) | `internal/proxy/admission_estimator.go` (new), corpus fixtures under `internal/proxy/testdata/` | proxy hot path, scheduler, controllers | `go test ./internal/proxy/... -run TestEstimator`; `go test -bench BenchmarkEstimator -benchmem` | Estimator has ns/op p99 ≤ 100,000 |
| Admission integration (CC-6a-2) | `internal/proxy/admission_filter.go` (new), proxy wire-in, Helm value | controllers, CRDs, runtime images | `go test ./internal/proxy/...`; live smoke with annotation toggle | 413 in <100ms for over-budget; 200 for in-budget; metric increments |

Coordination notes:

- Shared contract: estimator function signature
  `func EstimatePromptTokens(ChatCompletionRequest) int` is stable and
  callable from any future admission scope.
- Merge order: CC-6a-1 (this) → CC-6a-2 → CC-6a-3.
- Conflict risks: routing.go is also touched by the existing curve-spec
  CC-5/CC-7 path (blocked). If the CC-6a fallback slice eventually
  lands, coordinate with any concurrent CC-7 reopening.

## Readiness

Status: Ready (spec slice). Implementation gated by kill-test.

- Target files/modules: planning docs only.
- Owner boundary: planning.
- Validation commands: `git diff --check`;
  `rg "context-bounded-admission|CC-6a" docs/planning`.
- Generated artifacts: none.
- Rollout/backout: revert the docs-only commit.
- Non-blocking open questions:
  - Default `safety_margin` value (0% means trust the ceiling
    exactly; 10% means refuse when estimated+max_tokens > 0.9 ×
    ceiling). Suggest defaulting to 5%.
  - Whether the estimator should special-case `max_tokens` absence
    (chat completions API allows it; OpenAI's docs say the default
    is "model-dependent").

## Validation Plan

Spec slice:

```bash
git diff --check
rg "context-bounded-admission|CC-6a" docs/planning .loom
```

Implementation slice (CC-6a-2, future MR):

```bash
go test ./internal/proxy/... -count=1
go test -bench BenchmarkEstimator -benchmem ./internal/proxy/...
# live smoke with admission annotation on a debug Model:
curl -s -o /dev/null -w '%{http_code} %{time_total}\n' \
  -X POST http://localhost:18080/model/<m>/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"<m>","messages":[{"role":"user","content":"<over-budget>"}], "max_tokens":256}'
# expect: 413 <0.1
```

## Rollout / Backout

- Rollout: ship this spec → write the estimator + benchmark (CC-6a-2
  pre-work) → run the kill-test → decide whether to ship the
  admission filter or revisit estimator design.
- Backout:
  - Spec slice: `git revert`.
  - Implementation slice: Helm value `proxy.admission.enabled=false`
    disables the feature globally without a deploy revert.
  - Per-Model: remove the `flexinfer.ai/admission: context-bounded`
    annotation.
- Risk controls:
  - Default off per Model. Operators must opt in.
  - Kill-test passes are a precondition for shipping.
  - Counter + histogram make false-positive rate observable from
    Prometheus alone.

## Open Questions

- [ ] Should the proxy refresh the curve `first_failure_point` cache
      on a schedule, or only on Model CR change? Defer to CC-6a-2 once
      we know the read frequency.
- [ ] Default `safety_margin` value (0 / 5 / 10%). Suggest 5%.
- [ ] How aggressive should `max_tokens` defaulting be when the field
      is unset? Suggest 256 chat / 512 completion.

## Sources

- `docs/planning/context-curve-scheduler-spec.md` (CC-5 predecessor;
  routing reframing → admission)
- `docs/planning/context-curve-benchmark.md` (CC-1..CC-4 closeout)
- `docs/planning/next-roadmap.md` (Context-Curve Benchmarking section)
- `.loom/60-validation-matrix.md` (CC-6 kill-test FAILED verdict
  documenting why routing-style use is blocked)
- `api/v1alpha1/types.go:383` (`MaxModelLen`)
- `api/v1alpha1/types.go:524` (`ContextSize`)
- `backend/vllm.go:73` (vLLM `--max-model-len` from config)
- `backend/llamacpp.go:82` (llama.cpp `--ctx-size` from config)
- `internal/proxy/proxy.go` (proxy informer; `lastKnownServicePorts`
  cache pattern from MR !493 is the precedent for per-Model caches)
- `scripts/bench-context-curve.sh` (curve runner; populates
  `first_failure_point`)
- `scripts/sim-curve-router.py` (CC-6 backtest tool that proved the
  routing-style use case isn't viable on two-point curves)
- Global rule: `~/.claude/rules/spec-riskiest-assumption.md`
