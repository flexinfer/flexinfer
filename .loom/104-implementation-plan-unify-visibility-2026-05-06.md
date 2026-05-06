# Implementation Plan — EPIC 2 Unify Visibility (2026-05-06)

Tracking: [Issue #66](https://gitlab.flexinfer.ai/services/loom-core/-/issues/66)
Companions: [102-research](.loom/102-research-unify-visibility-2026-05-06.md), [103-spec](.loom/103-product-spec-unify-visibility-2026-05-06.md)
Status: ready to ralph

## Sequencing

```
UNIFY-1 ──→ UNIFY-2 ──→ UNIFY-4 ──┐
   │                              ├──→ UNIFY-5
   └──────→ (frontend types) ─────┘
```

UNIFY-1 must land first (additive, no consumer migrations) so 2/4/5 can pull from it independently. UNIFY-4 and UNIFY-5 can run in parallel worktrees once UNIFY-1 is on `main`. UNIFY-2 (embedded HUD + OpenAPI) can begin after UNIFY-1 contracts stabilize.

## Slice Map

Each slice is independently shippable, has its own `claude/...` branch + worktree, ends with a green-CI MR, never breaks `make ci-contracts` mobile golden files, and lands one MR (or a tight series) before the next slice begins.

### Slice S1 — UNIFY-1a: scaffold contracts package (additive)
- Branch: `feat/unify-1a-contracts-scaffold`
- Files:
  - new: `internal/visibility/contracts/doc.go`
  - new: `internal/visibility/contracts/{status,health,cost,rbac,catalog,sessions,tasks,presence}/types.go`
  - new: `internal/visibility/contracts/mobile/v1/adapter.go` (no-op stubs initially)
- Implementation:
  - Copy struct definitions (no rename) from `internal/hud/bridge/{daemon.go,agent_dto.go,agent_contracts.go}` into per-category files.
  - Add doc.go declaring stability (`// Status: pre-1.0, additive-only within minor versions.`).
  - Re-export from old locations as type aliases: `type StatusResult = status.PlatformStatus`.
  - Add `// Deprecated: use internal/visibility/contracts/status` comment on aliases.
- Tests:
  - new: `internal/visibility/contracts/contracts_alias_test.go` — assert each alias compiles + JSON-equivalent.
  - run: `go test ./internal/contracts/...` — assert all 18 existing golden files still pass.
- Acceptance:
  - `go vet ./...` clean.
  - `go test -count=1 ./...` clean.
  - `make ci-contracts` green.
- Estimated size: ~600 LOC moved + ~150 new tests. One MR.

### Slice S2 — UNIFY-1b: golden coverage for new DTO surfaces
- Branch: `feat/unify-1b-contracts-golden`
- Files:
  - new: `internal/contracts/testdata/visibility_status.golden` and one per category.
  - update: `internal/contracts/golden_test.go` (add visibility test funcs).
- Implementation:
  - For each new DTO, add a fixture-driven serializer test mirroring `mobile_dashboard.golden` shape.
  - First run uses `-update-golden` to seed; commit the resulting bytes.
- Acceptance: `make ci-contracts` green; new golden files reviewable in MR diff.
- Estimated size: 9 new golden files + 9 test funcs.

### Slice S3 — UNIFY-1c: migrate CLI status to contracts package
- Branch: `refactor/unify-1c-cli-status-uses-contracts`
- Files:
  - update: `cmd/loom/status.go` — replace private `platformStatus` with `contracts.status.PlatformStatus`.
  - update: `cmd/loom/status_test.go` — adjust imports.
- Implementation:
  - Pure rename + import swap; printer functions unchanged.
- Acceptance: `loom status` text + JSON byte-identical to pre-change (capture before/after via `go test -run TestStatusJSONFormat`).

### Slice S4 — UNIFY-1d: migrate HUD handlers to contracts package
- Branch: `refactor/unify-1d-hud-uses-contracts`
- Files:
  - update: `internal/hud/app_routes_observability.go`, `internal/hud/app_routes_fleet.go`, `internal/hud/api_*.go`.
  - update: `internal/hud/monitor/{cost,health,fleet}.go` if struct embedding moves.
- Implementation:
  - Swap struct returns to contracts types; keep wire format identical.
  - Mobile v1 handlers wrap with `contracts.mobile.v1.MapDashboard()` etc. so frozen JSON bytes don't shift.
- Acceptance: `make ci-contracts` green (mobile bytes identical); `go test ./internal/hud/...` green.

### Slice S5 — UNIFY-2a: embedded HUD library API + docs
- Branch: `feat/unify-2a-embed-library-api`
- Files:
  - update: `internal/hud/embed.go` — promote `NewApp`/`NewAppFromDeps` doc comments; export `NewEmbedded(deps Deps) (*App, error)` if missing.
  - new: `docs/HUD_EMBEDDING.md` walking through LocalCaller, Deps, monitor sharing, cache invalidation.
- Implementation:
  - No behavior change; godoc + docs.
- Acceptance: `go doc ./internal/hud` shows `NewEmbedded`; docs PR reviewable.

### Slice S6 — UNIFY-2b: `loom hud --embed` flag
- Branch: `feat/unify-2b-hud-embed-flag`
- Files:
  - update: `cmd/loom/hud.go` — add `--embed` flag; when set, run HUD in-process on ephemeral port; print URL; bind lifetime to CLI process.
  - update: `internal/hud/runtime.go` if start/stop hooks need wiring.
  - new: `cmd/loom/hud_embed_test.go`.
- Implementation:
  - Spin up `App` via embed library; listen on `127.0.0.1:0`; print `loom hud (embed): http://127.0.0.1:NNNNN/`; defer Shutdown on `SIGINT/SIGTERM`/parent exit.
- Acceptance: `loom hud --embed` opens browser? No — prints URL only. `--embed --tui` runs both in-process. Existing `loom hud start` unchanged.

### Slice S7 — UNIFY-2c: OpenAPI spec for visibility surfaces
- Branch: `docs/unify-2c-openapi-spec`
- Files:
  - new: `docs/api/openapi.yaml` covering `/api/{status,health,cost,rbac,catalog,sessions,tasks,presence,events}`.
  - new: `internal/contracts/openapi_conformance_test.go`.
  - update: `Makefile` — `make ci-openapi` target running conformance test.
- Implementation:
  - Hand-author YAML using `kin-openapi`-compatible 3.0.3 schemas mirroring contracts package types.
  - Conformance test uses table-driven seeded fixtures + `openapi3filter.ValidateResponse`.
- Acceptance: `make ci-openapi` green; spec readable; CI integrated.

### Slice S8 — UNIFY-4a: shared CLI render helpers
- Branch: `feat/unify-4a-cli-render`
- Files:
  - new: `cmd/loom/internal/render/{table.go,json.go,watch.go,filter.go}`.
- Implementation:
  - Extract patterns from `cmd/loom/status.go printPlatformStatus` and `cmd_catalog.go listServers` into shared helpers.
  - `watch.Run(interval, render func() error)` does ANSI cursor home + clear + render loop.
  - `filter.Parse(strings) → map[string]string` handles `--filter k=v,k2=v2`.
- Acceptance: existing commands continue to work after refactor; new tests in `render_test.go`.

### Slice S9 — UNIFY-4b: `loom cost` + `loom rbac status` commands
- Branch: `feat/unify-4b-cli-cost-rbac`
- Files:
  - new: `cmd/loom/cmd_cost.go`, `cmd_cost_test.go`, `cmd_rbac_status.go`, `cmd_rbac_status_test.go`.
  - update: `cmd/loom/main.go` to register.
- Implementation:
  - Use `bridge.AgentBridge` + `bridge.DaemonClient` already wired via `cmd_daemon.go`.
  - `loom cost --by agent|server|day [--json] [--watch 5s]`.
  - `loom rbac status [--json]`.
  - Non-zero exit when degraded (matches UNIFY-3 pattern).
- Acceptance: integration tests stub HUD `/api/cost` + `/api/rbac` and assert renderer output + JSON round-trip.

### Slice S10 — UNIFY-4c: `loom health|presence list|sessions list|tasks list|catalog status`
- Branch: `feat/unify-4c-cli-parity`
- Files:
  - new: `cmd/loom/cmd_health.go`, `cmd_presence_list.go`, `cmd_sessions.go`, `cmd_tasks.go`, `cmd_catalog_status.go` (each with test).
- Implementation:
  - Mirror UNIFY-4b pattern; use shared render helpers.
  - `loom status` extended to roll up `cost.last_24h_usd` + `rbac.denied_count_last_24h`.
- Acceptance: `loom <cmd> --json | jq` works for each; `--watch` exits cleanly on `SIGINT`.

### Slice S11 — UNIFY-5a: TUI Cost panel
- Branch: `feat/unify-5a-tui-cost-panel`
- Files:
  - new: `internal/tui/panels/cost.go`, `cost_test.go`, `cost_update_test.go`.
  - update: `internal/tui/app.go` register panel + keybinding.
  - update: `internal/tui/client.go` add `CostMonitor` to `Deps` and own/start in standalone mode.
- Implementation:
  - Reuse `monitor.CostMonitor`; render top-N agents/servers, totals row, sparkline if cheap.
- Acceptance: `loom hud --tui` shows Cost panel; HUD shared-mode reuses single CostMonitor instance (no double polling).

### Slice S12 — UNIFY-5b: TUI Catalog + Servers panels
- Branch: `feat/unify-5b-tui-catalog-servers`
- Files: `internal/tui/panels/{catalog,servers}.go` + tests.
- Implementation: pull from `bridge.DaemonClient` server list + catalog handler equivalent.

### Slice S13 — UNIFY-5c: TUI RBAC panel + SSE wiring for `hud.rbac`/`hud.catalog`
- Branch: `feat/unify-5c-tui-rbac-sse`
- Files:
  - new: `internal/tui/panels/rbac.go` + tests.
  - update: `internal/hud/sse_hub.go` and producers (`internal/hud/monitor/cost.go`, `internal/hud/app_routes_observability.go`) to broadcast `hud.rbac` and `hud.catalog` events.
  - update: `internal/contracts/golden_test.go` `sse_events.golden` (additive) — new event types appended.
- Implementation:
  - SSE event payloads use the new contracts package.
  - TUI subscribes via existing stream client.
- Acceptance: SSE event golden file has 2 new event types; TUI panels reflect updates within 1s of source event.

### Slice S14 — UNIFY-5d: cross-surface diagnostics runbook + jump menu
- Branch: `docs/unify-5d-visibility-runbook`
- Files:
  - new: `docs/operations/visibility-runbook.md` walking the same investigation through CLI / TUI / HUD.
  - update: `internal/tui/keymap.go` add `?` jump menu.
  - update: `internal/tui/app.go` jump-menu modal.
- Implementation: document existing flows (no new code beyond keymap modal); ensure URL-jumps print openable HUD links from CLI.

## Quality Gates Per Slice

Each slice MR must pass:
1. `go test -count=1 ./...`
2. `go vet ./...`
3. `make ci-contracts` (mobile golden files unchanged unless explicitly part of slice)
4. `make ci-openapi` (post-S7)
5. `golangci-lint run`
6. PR self-review checklist (see `.claude/skills/pr-self-review.md`)
7. CI green on GitLab pipeline before merge.

## Estimated Cadence

| Slice | Est | Notes |
|---|---|---|
| S1–S4 (UNIFY-1) | 4 sessions | Sequential; each ~1–2hr |
| S5–S7 (UNIFY-2) | 3 sessions | S7 (OpenAPI) longest |
| S8–S10 (UNIFY-4) | 3 sessions | S8 then S9/S10 in parallel |
| S11–S14 (UNIFY-5) | 4 sessions | S11–S13 sequential, S14 last |

Total: ~14 ralph sessions over ≈ 2 weeks if shipped one slice/day.

## Risk Tracking

| Risk | Slice | Mitigation |
|---|---|---|
| Mobile v1 byte drift | S4 | Mobile adapter test asserts byte-identity pre/post extraction. |
| Contract churn for downstream loom-zed/extension | S1–S4 | Type aliases stay one minor release. CHANGELOG entry per minor. |
| OpenAPI spec rot | S7 onward | Conformance test fails CI on drift. |
| TUI keymap collisions | S11–S14 | Audit `keymap.go` before each slice; document in panel header. |
| Polling load if Cost/RBAC SSE poorly tuned | S13 | Reuse existing `BaseMonitor[T]` with conservative intervals (Cost=30s, RBAC=15s). |

## Out-of-Scope (Deferred)

- TUI Mills/Spawn/Weaver/Reasoning panels.
- Generated SDKs in TS/Swift/Python from OpenAPI.
- Redoing observability (`/api/otel`, `/api/traces`) — those are #12's territory.
- Mutation contracts (POSTs in HUD beyond status); read-only first.

## Sources

- [.loom/102-research-unify-visibility-2026-05-06.md](.loom/102-research-unify-visibility-2026-05-06.md)
- [.loom/103-product-spec-unify-visibility-2026-05-06.md](.loom/103-product-spec-unify-visibility-2026-05-06.md)
- [docs/CONTRACT_TESTING.md](docs/CONTRACT_TESTING.md)
- [docs/MOBILE_COMPANION_API.md](docs/MOBILE_COMPANION_API.md)
- [docs/API_STABILITY.md](docs/API_STABILITY.md)
- Commit `d0d1518` (UNIFY-3 pattern)
- Commit `8c2c50d` (#21 contracts pattern)
- File: [cmd/loom/status.go:15](cmd/loom/status.go)
- File: [internal/hud/embed.go](internal/hud/embed.go)
- File: [internal/tui/client.go:14](internal/tui/client.go)
- File: [internal/hud/domain/domain.go](internal/hud/domain/domain.go)
- File: [internal/contracts/golden_test.go](internal/contracts/golden_test.go)
