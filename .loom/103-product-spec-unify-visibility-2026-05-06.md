# Product Spec — EPIC 2 Unify Visibility (2026-05-06)

Tracking: [Issue #66](https://gitlab.flexinfer.ai/services/loom-core/-/issues/66)
Companion: [102-research-unify-visibility-2026-05-06.md](.loom/102-research-unify-visibility-2026-05-06.md)
Status: proposed

## Outcome

A single typed contract layer for visibility (status / health / cost / RBAC / catalog / sessions / tasks / presence) that HUD, CLI, TUI, and embeddable consumers all read from. Drift-tested by golden files. Mobile v1 contract preserved bit-for-bit. Embedded HUD becomes a documented, flagged operator mode. CLI and TUI gain consistent `--json|--watch|--filter` ergonomics.

## Non-Goals

- Replacing `/api/mobile/v1/*` (frozen).
- Building an SDK in another language. SDKs may consume the OpenAPI spec but are out of scope.
- Reworking authentication, RBAC enforcement, or cost accounting semantics.
- New domains (Mills/Spawn/Weaver) — they may benefit from the contract package but their own visibility evolution is its own roadmap.

## Decision Matrix

| ID | Decision | Choice | Rationale |
|---|---|---|---|
| D1 | Where do shared DTOs live? | `internal/visibility/contracts/` (Go) + per-category sub-packages | Keeps pre-1.0 stability bar moderate. Promote to `pkg/visibility` after two minor releases proven. |
| D2 | OpenAPI spec source | Hand-authored YAML at `docs/api/openapi.yaml`, drift-tested by handler-conformance golden tests | Matches current `internal/contracts` golden pattern; avoids generator lock-in. |
| D3 | Embedded HUD UX | `loom hud --embed` runs HUD in CLI process; HUD shuts down when CLI exits. Library API exposed via `internal/hud/embed.go` (already exists). | `LocalCaller` pattern already proven; just needs flag + docs. |
| D4 | Mobile v1 freeze handling | Contracts package includes `mobile/v1` adapters that map shared DTOs ↔ frozen JSON shapes when names diverge | Preserves bit-for-bit golden compatibility. |
| D5 | TUI parity scope (UNIFY-5) | Add `cost`, `catalog`, `servers`, `rbac` panels first; defer Mills/Spawn/Weaver/Reasoning | Highest ops value, smallest surface. |
| D6 | CLI parity scope (UNIFY-4) | New commands: `loom cost`, `loom rbac status`, `loom health`, `loom catalog status`, `loom presence list`, `loom sessions list`, `loom tasks list` | Mirror existing HUD/TUI surfaces; no new data sources. |
| D7 | Polling vs RPC canonicalization | Per DTO, declare canonical source in contract docs. Status/Cost/Health = monitor cache (eventual). Sessions/Tasks/Presence = on-demand RPC. | Reflects current load-and-correctness profile; documents what was implicit. |
| D8 | Output format ergonomics | All visibility commands accept: `--json`, `--watch <interval>`, `--filter key=val`, `--no-color`. Default text format reuses shared CLI renderers. | Sets the bar; UNIFY-3's `loom status --json` already shipped this pattern. |
| D9 | Backward compatibility | Old internal types in `internal/hud/bridge/` re-export new types as aliases for one minor release before removal | Limits churn for downstream packages. |
| D10 | Versioning of OpenAPI spec | Spec is `v1` and lives under `/api/v1` aliases for non-mobile routes; existing un-prefixed routes stay live for one minor release | Establishes versioning without big-bang rename. |

## UNIFY-N Decomposition

### UNIFY-1 — Canonical Visibility Contracts Package
**Outcome**: `internal/visibility/contracts/` packages typed DTOs for status/health/cost/rbac/catalog/sessions/tasks/presence + golden coverage + adapter shims for mobile v1.
**Acceptance**:
- `internal/visibility/contracts/{status,health,cost,rbac,catalog,sessions,tasks,presence}/types.go` defined.
- All HUD handlers, CLI commands, TUI panels for these surfaces import from the new package.
- New golden files at `internal/contracts/testdata/visibility_*.golden`; existing 18 untouched.
- No mobile v1 golden file changes.
- `cmd/loom/status.go` `platformStatus` replaced with `contracts.status.PlatformStatus`.
- Cross-references: closes the contract drift items in [.loom/102](.loom/102-research-unify-visibility-2026-05-06.md) §Divergence Points 1, 4, 7.

### UNIFY-2 — Embedded HUD: Flag, Library API, OpenAPI Spec
**Outcome**: `loom hud --embed` runs HUD in-process; library callers can `hud.NewEmbedded(deps)`; canonical machine-readable HTTP spec ships in repo.
**Acceptance**:
- `loom hud --embed` starts HUD on ephemeral port, prints URL, lifetimes the HUD process for the CLI's lifetime.
- `internal/hud/embed.go` exports a documented `NewEmbedded(deps Deps) (*App, error)` constructor; godoc-complete.
- `docs/HUD_EMBEDDING.md` walks through the in-process and library use cases (LocalCaller pattern, monitor sharing, cache invalidation).
- `docs/api/openapi.yaml` describes `/api/status`, `/api/health`, `/api/cost`, `/api/rbac`, `/api/catalog`, `/api/sessions`, `/api/tasks`, `/api/presence`, `/api/events` (SSE).
- `internal/contracts/openapi_conformance_test.go` golden-checks each handler response against schema.
- Cross-references: closes #102 §Divergence 8 + missing public embed contract.

### UNIFY-3 ✅ — Unified `loom status` (shipped `d0d1518`, 2026-03-01)
Already merged. Listed for completeness; no new work.

### UNIFY-4 — CLI Visibility Parity
**Outcome**: New `loom <surface>` commands for the categories CLI is missing today, all using the UNIFY-1 contracts and the `--json|--watch|--filter` ergonomics.
**Acceptance**:
- New commands: `loom cost [--by agent|server|day] [--json] [--watch]`, `loom rbac status [--json]`, `loom health [--json] [--watch]`, `loom catalog status [--json]`, `loom presence list [--filter status=active] [--json]`, `loom sessions list [--filter agent=…] [--json]`, `loom tasks list [--filter status=…] [--json]`.
- All commands return non-zero exit when their backing data indicates an unhealthy/degraded condition (matches UNIFY-3 precedent).
- `--watch <duration>` re-renders inline using ANSI cursor controls; `Ctrl-C` exits cleanly.
- Shared `cmd/loom/internal/render` helpers used by all commands.
- `loom status` extended (no breaking change) to surface a `cost.last_24h_usd` and `rbac.denied_count_last_24h` as part of `platformStatus`.
- Cross-references: closes #102 §Divergence 2.

### UNIFY-5 — TUI Parity + Cross-Surface UX Consistency
**Outcome**: TUI gains Cost / Catalog / Servers / RBAC panels backed by the same monitors used by HUD; cross-surface diagnostics flow defined.
**Acceptance**:
- New TUI panels: `internal/tui/panels/{cost,catalog,servers,rbac}.go` registered in `app.go` with shared keymap and `FilterBar` reuse.
- `bridge/local_caller` allows TUI ↔ HUD ↔ CLI to share the same monitor instance via `Deps` when co-hosted.
- New `loom hud --tui` keymap: `?` opens cross-surface jump menu (e.g., from Cost row → "open in HUD URL", "drill to RBAC for this agent", "tail loki logs").
- A "Diagnostics workflow" runbook at `docs/operations/visibility-runbook.md` shows the same investigation in CLI / TUI / HUD step-by-step (e.g., "session X is stuck — find it, inspect it, end it").
- Cross-references: closes #102 §Divergence 6.

## Contract Categories (DTO Skeletons)

These are intentionally aligned with today's structs to minimize migration churn.

### `contracts.status.PlatformStatus`
Mirrors today's `cmd/loom/status.go` `platformStatus` with `cost.last_24h_usd` and `rbac.denied_count_last_24h` added.

### `contracts.cost.Snapshot`
Direct lift of [internal/hud/bridge/daemon.go](internal/hud/bridge/daemon.go) `CostStatsResult` + `CostAgentUsage`/`CostServerUsage`/`CostTotals`. Add `WindowStart`/`WindowEnd` ISO-8601.

### `contracts.rbac.Snapshot`
- `Policy` (current rule set summary, not full body)
- `DeniedCount24h`
- `RecentDenials []Denial { Time, Actor, Resource, Reason }`
- `AuditEnabled bool`
- `SimulationMode bool`

### `contracts.health.Snapshot`
Already exists as `HealthResult` ([internal/hud/bridge/daemon.go](internal/hud/bridge/daemon.go)) — promote into contracts package; CLI `loom health` reuses.

### `contracts.catalog.{Status,Entry}`
Wrap registry list + per-server enable state + last-error.

### `contracts.sessions.{Info,List}` / `contracts.tasks.{Info,List}` / `contracts.presence.{Info,List}`
Direct lift from `internal/hud/bridge/agent_dto.go`. Re-export under new path.

### Mobile v1 adapter
`contracts.mobile.v1.MapDashboard(contracts.status.PlatformStatus) → mobile.DashboardResponse` etc. Keeps mobile golden bytes unchanged.

## OpenAPI Spec Shape

`docs/api/openapi.yaml` (excerpt structure):
```yaml
openapi: 3.0.3
info:
  title: Loom HUD HTTP API
  version: "1.0.0"
servers:
  - url: http://localhost:5052/api
paths:
  /status:    { get: ... }
  /health:    { get: ... }
  /cost:      { get: ... }
  /rbac:      { get: ... }
  /catalog:   { get: ... }
  /sessions:  { get: ... }
  /tasks:     { get: ... }
  /presence:  { get: ... }
  /events:    { get: ... }     # SSE described as text/event-stream
components:
  schemas: {...}                # generated from contracts package types
```

Conformance test ([internal/contracts/openapi_conformance_test.go]):
- For each path, hit handler with table-driven seeded fixture, assert response validates against the OpenAPI schema (`kin-openapi/openapi3filter`).

## SSE Event Catalog

Document existing `hud.*` events in OpenAPI spec under `x-sse-events` extension. Today's events (per [docs/CONTRACT_TESTING.md](docs/CONTRACT_TESTING.md)): `hud.fleet`, `hud.health`, `hud.session.*`, `hud.heartbeat`, `hud.cost`. Add: `hud.rbac`, `hud.catalog` for new TUI panels (so TUI gets push, not poll, for slow-changing surfaces).

## Backward Compatibility Plan

- Internal type re-exports in `internal/hud/bridge/` for one minor release: `type StatusResult = contracts.status.PlatformStatus` etc.
- Deprecated comment + CHANGELOG entry per release.
- Mobile golden files: zero diff target. Add adapter test that proves serialized bytes are identical pre- and post-extraction.

## Success Criteria (Epic-Level)

- All 9 new contracts shipped, golden-tested, cross-consumed.
- HUD frontend `cost`, `rbac`, `catalog`, `health`, `status` stores all import generated TypeScript types from the OpenAPI spec (or hand-authored equivalents that match).
- CLI parity matrix at `docs/operations/visibility-runbook.md` shows every surface available in CLI, TUI, HUD with consistent ergonomics.
- New external SDK consumer (loom-zed or loom VS Code extension) can compile against the OpenAPI spec without reading Go source.
- No regression on `make ci-contracts` mobile golden files across the entire epic.

## Risks (mitigations)

- Contract churn during epic → land UNIFY-1 first as additive (re-exports), then migrate consumers slice-by-slice.
- Polling/RPC drift → D7 explicitly canonicalizes per DTO; tests assert that monitor-cached snapshots cannot be older than 2× refresh interval.
- TUI keymap conflicts → audit `internal/tui/keymap.go` before adding new bindings; document in panel headers.
- OpenAPI maintenance burden → conformance test fails CI on drift, forcing the spec to stay current.

## Sources

- [.loom/102-research-unify-visibility-2026-05-06.md](.loom/102-research-unify-visibility-2026-05-06.md)
- [docs/API_STABILITY.md](docs/API_STABILITY.md)
- [docs/CONTRACT_TESTING.md](docs/CONTRACT_TESTING.md)
- [docs/MOBILE_COMPANION_API.md](docs/MOBILE_COMPANION_API.md)
- Commit: `d0d1518` (UNIFY-3 reference pattern)
- Commit: `8c2c50d` (#21 contract refactor reference pattern)
- File: [internal/hud/embed.go](internal/hud/embed.go)
- File: [internal/hud/domain/domain.go](internal/hud/domain/domain.go)
- File: [internal/contracts/golden_test.go](internal/contracts/golden_test.go)
