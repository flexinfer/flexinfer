# RALPH Iteration Plan: Proxy Rollout Drain Live Kill Test

## Review

- Roadmap milestone: inference-serving hardening, remaining live proof gate for graceful proxy draining.
- Spec sections: `.loom/43-plan-proxy-serving-hardening-2026-07-17.md` and `docs/user/operations.md` "Verify rollout draining".
- Prior decisions to preserve: use the parent `Deployment` for the rollout; do not hot-edit Flux-managed objects; keep the production `workhorse-128k` route and configured 640-second termination grace unchanged.

## Riskiest assumption + kill-test

**Load-bearing assumption**: On the deployed single-replica proxy, SIGTERM flips `/readyz` to 503 and allows endpoint propagation before `http.Server.Shutdown`, so one already-started long request survives a Deployment rollout while new Service traffic continues through the replacement pod.

**Kill test**: In no more than 30 minutes, verify the live Deployment carries the expected readiness probe, drain environment, termination grace, and published image; start a long request through `workhorse-128k`; restart `Deployment/flexinfer-proxy`; send short Service probes throughout the rollout; then require the held request to complete, every short probe to return HTTP 200, the rollout to finish, and shutdown evidence to show completion without timeout.

**Failure mode if the assumption is wrong**: The held client receives a disconnect, Service probes fail during endpoint handoff, the rollout stalls, or the old pod reports a shutdown timeout. Any one of these blocks a pass verdict and requires retaining the evidence for a follow-up implementation slice.

**Status**: passed 2026-07-18 (evidence below).

## Align

- Slice name: proxy rollout-drain live certificate.
- Scope in: read-only deployment preflight; one documented Deployment restart; concurrent held request and Service probes; pod/log/metric evidence; tracked plan and roadmap status updates.
- Scope out: manifest changes, live patches, model changes, backend rollouts, tuning drain timeouts, and unrelated serving work.
- Acceptance criteria:
  - The live Deployment has `/readyz`, `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT=10m`, `PROXY_SHUTDOWN_DRAIN_DELAY=5s`, and `terminationGracePeriodSeconds=640` on the expected published image.
  - The held request completes with a valid HTTP response during the rollout.
  - All short Service probes return HTTP 200.
  - The rollout reaches Available and the replacement pod is Ready.
  - Positive shutdown-completion evidence exists and negative searches find no shutdown timeout or request disconnect.
- Dependencies/blockers: node-side SSH access to the k3s API; a Ready model behind `workhorse-128k`; enough cluster capacity for RollingUpdate surge.

## Land

- Planned file areas: this iteration plan, `.loom/43-plan-proxy-serving-hardening-2026-07-17.md`, and `ROADMAP.md` only if its status marker requires synchronization.
- Implementation steps:
  1. Capture a pre-rollout snapshot and exact deployed drain contract.
  2. Run the bounded held-request plus probe-loop rollout test and capture evidence.
  3. Record the pass/fail verdict and synchronize the roadmap/spec status.

## Prove

- Tests to run: live held-request completion, 20+ HTTP Service probes, Deployment rollout status, replacement readiness.
- Lint/static checks: Markdown whitespace check and scoped `git diff --check`.
- CI checks: documentation pipeline for the landing MR.

## Handoff/Harvest

- Docs to update: serving-hardening plan status and this evidence-bearing iteration plan.
- Agent-context entries to add: deployment preflight finding, kill-test verdict, any follow-up question.
- Next-slice candidates: none if the proof passes; otherwise the smallest correction implied by the first failed acceptance criterion.

## Live Evidence

- Test window: `2026-07-18T04:14:48Z` to `2026-07-18T04:15:52Z`.
- Deployed contract:
  - Flux source: `master@sha1:6e136fcd2df5f9ff22c1fd60289b859b98378627`.
  - Helm chart: `flexinfer@1.0.13+6e136fcd2df5.3`.
  - Proxy image: `registry.harbor.lan/flexinfer/flexinfer-proxy:20260717-232506`.
  - Readiness: HTTP `/readyz`, 5s period, failure threshold 2.
  - Drain: `PROXY_SHUTDOWN_DRAIN_DELAY=5s`, `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT=10m`, `terminationGracePeriodSeconds=640`.
- Held request:
  - Route: `workhorse-128k`, resolved to the Ready `qwen35-35b-clean-gptq-workhorse` Model.
  - The old proxy reported `in_flight_connections=1` when SIGTERM began.
  - Result: curl exit 0, HTTP 200, `finish_reason=length`, 2,048 completion tokens, 7,190 content characters.
- Rollout continuity:
  - Old pod: `flexinfer-proxy-7fb9794d8f-nkwfx` (`7fec03e9-0404-4527-8f64-71ee82c15019`).
  - New pod: `flexinfer-proxy-575d994977-2kss4` (`8d9a8dd3-f185-4a56-a57b-465f2a46cef5`), Ready.
  - Deployment rollout exit: 0.
  - Service probes: 60/60 HTTP 200; zero failures.
- Shutdown evidence:
  - `04:14:56.943776297Z`: graceful shutdown started with one in-flight connection, 5s drain delay, and 10m timeout.
  - `04:15:35.1787589Z`: graceful shutdown completed after `38.234964973s`.
  - Twenty-three direct old-pod scrapes observed `flexinfer_proxy_shutdowns_total{result="started"} 1`.
  - Negative search: zero timeout logs, zero timeout metric samples, empty held-request stderr, and zero failed Service probes.

Verdict: **PASS**. The load-bearing readiness-first drain assumption is proven on the live k3s Deployment.

## Slice Handoff

### Slice Summary

- Milestone: inference-serving hardening.
- Slice: proxy rollout-drain live certificate.
- Status: complete.

### What Landed

- Key changes: live certificate captured; serving-hardening plan and roadmap status synchronized.
- Key files: this certificate, `.loom/43-plan-proxy-serving-hardening-2026-07-17.md`, and `ROADMAP.md`.
- Validation results: held request 200 with 2,048 completion tokens; 60/60 probes 200; rollout complete; graceful shutdown completion logged; no timeout evidence.

### What Is Still Open

- Remaining acceptance criteria: none.
- Known issues: issue #65 was already closed before this proof run, so no issue-state mutation is required.
- Dependencies: none for this slice.

### Next Actions

1. Merge this evidence-only slice after the documentation pipeline passes.
2. Select the next unchecked roadmap item in a fresh RALPH iteration.

### Context Links

- Agent-context session: `e1c28eac8d391be5`.
- Task ID: `b1fa182fa4d4967f`.
- Relevant docs/specs: `.loom/43-plan-proxy-serving-hardening-2026-07-17.md`, `docs/user/operations.md`, and `ROADMAP.md`.
