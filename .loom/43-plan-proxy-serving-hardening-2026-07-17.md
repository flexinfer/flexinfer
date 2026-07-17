# Plan: Inference Serving Hardening — Drain Sequencing + Burst-Safe Routing (2026-07-17)

## Review

- Roadmap "Next": graceful proxy drain during rollouts
  ([#65](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/65), P1).
- Harvested next-slice candidates from
  `.loom/40-iteration-plan-label-group-least-loaded-2026-07-14.md`:
  graceful drain; connection-aware admission/reservations for bursts.
- Verified code state (2026-07-17):
  - `feat(proxy): add graceful HTTP shutdown contract` (`ea965f8bd`, 2026-07-16)
    already ships SIGTERM-aware `server.Shutdown` with
    `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT` (default 10m) and
    `flexinfer_proxy_shutdowns_total` / `flexinfer_proxy_shutdown_duration_seconds`
    metrics (`internal/proxy/proxy.go:513-541`, `cmd/flexinfer-proxy/main.go:77`).
  - The proxy Deployment template
    (`charts/flexinfer/templates/activator-deployment.yaml`) has **no**
    readiness/liveness probes, no preStop, no `terminationGracePeriodSeconds`,
    and no drain env wiring — Kubernetes' default 30s grace kills the pod long
    before the 10m drain completes. This is the remaining #65 gap.
  - `pickLeastLoaded` (`internal/proxy/resolver.go:148`) reads active-connection
    gauges that only increment when `trackAndServe` starts the upstream request
    (`internal/proxy/connections.go`); a concurrent burst can select the same
    idle member N times before any gauge moves.
  - `cmd/flexinfer-global-proxy/main.go:219` uses bare `http.ListenAndServe` —
    no graceful shutdown at all.
  - Shutdown + `flexinfer_proxy_label_group_route_{decisions,target_hits}_total`
    metrics exist (`internal/proxy/metrics.go:156-171,289-307`) but have no
    Grafana panels or PrometheusRule coverage.

Plan-store note: the agent-context plan transport was recorded closed in plan 40
("preserve decisions and evidence in this tracked plan instead"); this tracked
doc is the canonical slice record, following that precedent.

## Riskiest assumption + kill-test

**Load-bearing assumption**: For the single-replica proxy Deployment, flipping a
`/readyz` readiness gate to 503 on SIGTERM plus an in-process pre-drain delay
removes the terminating pod from Service endpoints before `server.Shutdown`
closes the listener, and the default RollingUpdate surge (maxSurge 1 /
maxUnavailable 0 at replicas=1) brings the new pod Ready first — so a rollout
under load neither drops the in-flight long request nor black-holes new
requests.

**Kill test** (live, ≤30 min, post-merge): hold one long-context request
directly against a 128K workhorse Model through the proxy; trigger a proxy
rollout (`kubectl rollout restart deployment/flexinfer-proxy -n
flexinfer-system`); require (a) the held request completes successfully, (b)
short probe requests sent through `workhorse-128k` during the rollout all
succeed, (c) `flexinfer_proxy_shutdowns_total{result="completed"}` increments
with no `result="timeout"`.

**Failure mode if wrong**: rollouts still return RemoteDisconnected on long
requests (the exact defect harvested in plan 40), or the drain window
black-holes new traffic because endpoint removal races the listener close.

**Status**: not run (offline slices ship first; live kill-test is the
post-merge gate before closing #65).

## Slices

All branches cut from `master` (`5a4c14c9f`). File boundaries are disjoint by
design; `internal/proxy/metrics.go` belongs to slice 2 only, shared docs files
are assigned to exactly one slice.

### Slice 1 — `feat/proxy-rollout-drain-sequencing` (completes #65, P1)

- **Goal**: make the existing Go drain contract effective under Kubernetes
  rollouts.
- **Code**: `/readyz` endpoint that flips to 503 the moment shutdown starts;
  configurable in-process pre-drain delay (`PROXY_SHUTDOWN_DRAIN_DELAY`,
  default ~5s) between the readiness flip and `server.Shutdown` so endpoint
  removal propagates while the listener still accepts; log in-flight connection
  count at drain start. NOTE: do not add a liveness probe pointed at the HTTP
  server — `Shutdown` closes the listener, so liveness would SIGKILL a long
  drain.
- **Chart**: readinessProbe → `/readyz`; `terminationGracePeriodSeconds` =
  drain timeout + delay + buffer (values-driven); wire
  `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT` + `PROXY_SHUTDOWN_DRAIN_DELAY` via a new
  `proxy.gracefulShutdown` values block; explicit enablement in
  `deploy/system/values-k3s.yaml`.
- **Files**: `internal/proxy/proxy.go`, new `internal/proxy/readiness*.go`
  (+tests), `cmd/flexinfer-proxy/main.go` (if needed),
  `charts/flexinfer/templates/activator-deployment.yaml`,
  `charts/flexinfer/values.yaml`, `deploy/system/values-k3s.yaml`,
  `docs/user/proxy.md`, `docs/user/operations.md`.
- **Acceptance**: readiness flips before Shutdown is called (unit-tested);
  rendered chart carries probes/grace/env; helm lint green; targeted +
  package proxy tests green.

### Slice 2 — `feat/proxy-least-loaded-reservations` (burst-safe routing)

- **Goal**: close the selection-vs-connection-tracking race so a concurrent
  burst spreads across label-group members instead of piling onto one.
- **Design**: TTL'd in-flight reservation ledger keyed by model;
  `pickLeastLoaded` load = active connections + unexpired reservations, and
  records a reservation for the chosen member; `incrementConnections` consumes
  one unexpired reservation for that model (no-op when none). Config read
  inside the new file (`PROXY_LEAST_LOADED_RESERVATION_TTL`, default ~10s,
  0 disables) — NOT in `ProxyConfig`, to keep the file boundary disjoint from
  slice 1. New reservation metrics registered in `internal/proxy/metrics.go`.
- **Files**: new `internal/proxy/reservations.go` (+tests),
  `internal/proxy/resolver.go`, `internal/proxy/connections.go`,
  `internal/proxy/pick_member_test.go`, `internal/proxy/metrics.go`,
  `docs/user/routing.md`.
- **Acceptance**: concurrent-burst unit test — 2 idle members, 4 simultaneous
  picks before any connection registers → 2/2 split; TTL expiry restores base
  behavior; real connections consume reservations; package tests green.

### Slice 3 — `feat/proxy-drain-routing-observability`

- **Goal**: dashboards + alerts for the drain contract and least-loaded
  routing (ROADMAP "Now": watch per-Model active connections + label-group
  target hits).
- **Files**: `charts/flexinfer/templates/grafana-dashboard-proxy.yaml`,
  `charts/flexinfer/templates/prometheusrule.yaml`, new
  `docs/user/proxy-observability.md`.
- **Acceptance**: panels for shutdown events/duration, label-group route
  decisions by mode, target hits per member, active connections per model
  (no duplicates of existing panels); 1–2 justified alerts (shutdown timeout
  at minimum); dashboard JSON validates; helm lint green.

### Slice 4 — `feat/global-proxy-graceful-shutdown`

- **Goal**: parity — give the global proxy the same drain contract.
- **Files**: `cmd/flexinfer-global-proxy/main.go` (+`main_test.go`), any
  in-repo global-proxy manifests found.
- **Acceptance**: SIGTERM-aware `http.Server.Shutdown` with
  `GLOBAL_PROXY_GRACEFUL_SHUTDOWN_TIMEOUT` (default mirrors proxy);
  in-flight request survives shutdown in a unit test; package tests green.

## Integration

Merge slice branches into `feat/inference-serving-hardening-2026-07-17` in
order 1 → 2 → 4 → 3, run full repo tests + helm lint, self-review the combined
diff, then one MR to `master` with auto-merge. Post-merge: live kill-test
above, then close #65 with evidence.

Known integration hazard (plan 41): a newer local `controller-gen` regenerates
unrelated CRD/deepcopy output under `make test` — mechanical deltas must not be
committed.

## Status

- [x] Slice 1 implemented (`11e01e229`, branch `feat/proxy-rollout-drain-sequencing`)
- [x] Slice 2 implemented (`a48d8abb1`, branch `feat/proxy-least-loaded-reservations`)
- [x] Slice 3 implemented (`9c4bea8a9`, branch `feat/proxy-drain-routing-observability`)
- [x] Slice 4 implemented (`13030974c`, branch `feat/global-proxy-graceful-shutdown`)
- [x] Integration branch green 2026-07-17: `gofmt`/`go vet ./...`/`go build ./...`
      clean, `go test -count=1 ./...` exit 0, targeted `-race` proxy tests pass,
      `helm lint` 0 failed, rendered chart carries readyz/grace/drain wiring.
      Integration refactor: reservation ledger moved from package-level
      `sync.Map` to a lazily-initialized Proxy struct field (`e377e5f1d`).
- [x] MR merged 2026-07-17: !855 (`fa01da929`) + lint follow-up !856
      (`2f282816f`, errcheck in reservations_test — the CI lint job runs
      golangci-lint, which slice verification with plain `go vet` missed).
      Master pipeline 19733 fully green; publish jobs shipped the new
      proxy image.
- [ ] Live rollout-under-load kill-test run; #65 closed. Prereqs: Flux
      applies the chart/values (readyz probe, grace 640s, drain env) and the
      proxy Deployment rolls onto the freshly published image. Procedure in
      `docs/user/operations.md` "Verify rollout draining".

Harvest note (process): the slice-2 agent committed on local `master` in the
canonical repo instead of its branch (worktree-path drift — the known
`feedback_worktree_absolute_paths` failure mode). Recovered by re-pointing the
slice branch at the commit and resetting local `master` to `origin/master`;
canonical baseline dirt untouched, nothing was pushed.
