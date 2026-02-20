# Tech Debt Implementation Report

## Item

- Debt ID: `DEBT-002` (`TD-SESSION-01`)
- Branch/PR: `main` (local debt-slice execution)
- Owner: daemon/proxy runtime

## Problem

- Original pain point: daemon-side upstream calls in `internal/daemon/callpipeline.go` used inherited request context without explicit per-call timeout envelopes, leaving tool/control hops vulnerable to long stalls.
- Affected components: `internal/daemon/callpipeline.go`, `internal/daemon/callpipeline_test.go`

## Changes

- Summary of remediation:
  - Added explicit method-tiered RPC timeout selection for daemon upstream calls (`tools/call` vs control-plane methods).
  - Wrapped `Send` and `Recv` phases in timeout-bound contexts.
  - Added structured timeout errors with phase + duration + recoverability hint.
  - Added regression tests for timeout defaults/overrides and timeout-wrapped execute paths.
- Notable design choices:
  - Reused existing env names (`LOOM_DAEMON_CONTROL_TIMEOUT`, `LOOM_DAEMON_TOOL_TIMEOUT`) so CLI and daemon paths share a single timeout control surface.
  - Kept this slice scoped to call-execution behavior only (no routing/autostart policy changes).

## Verification

- Local checks:
  - `go test ./internal/daemon -run 'CallPipeline|DaemonRPC'`
  - `go test ./cmd/loom -run 'ProxyRPC|DaemonRPC|Timeout'`
  - `bash /Users/cblevins/.codex/skills/tech-debt-backlog-dev-loop/scripts/verify_local_loop.sh`
- CI pipeline/run:
  - Pending push + CI poll in loop step 6.
- Extra validation (perf, load, ops):
  - Not part of this slice.

## Outcome

- Risk reduced:
  - Upstream daemon transport calls now fail fast with explicit timeout diagnostics instead of potentially stalling indefinitely.
- Delivery drag reduced:
  - Timeout failures now carry consistent actionable hints, reducing restart/retry guesswork during incident triage.
- Residual debt / follow-ups:
  - Add explicit connect-phase timeout/error normalization in call routing path.
  - Add restart-chaos integration tests (`TD-SESSION-03` / `DEBT-007`) to validate full reconnect behavior under daemon restarts.
