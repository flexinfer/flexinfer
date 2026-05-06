# Research — EPIC 2 Unify Visibility (2026-05-06)

Tracking: [Issue #66 — EPIC 2: Unify Visibility](https://gitlab.flexinfer.ai/services/loom-core/-/issues/66)
Branch: `claude/serene-proskuriakova-aae3e6` (off `main` `b684a600`)
Author/agent: Claude Opus 4.7 (1M)
Related shipped work: `d0d1518` (UNIFY-3), `8c2c50d` (#21 stage 1 agent contracts), `8c2c50d`/`d501b385` (HUD error envelope unification)

## Goal

Establish current-state evidence and design constraints to slice EPIC 2 into 4 remaining UNIFY-N children (UNIFY-3 already shipped). Output: research → spec → implementation plan trio so the epic can be ralphed in independent slices that always green CI and never break Mobile v1 contract freeze.

## Scope (Re-stated From Issue #66)

- Shared API contracts for status / health / cost / security views.
- Embedded HUD integration parity with CLI/TUI.
- Cross-surface UX consistency for diagnostics and remediation workflows.

## Surfaces Inventory

### CLI (cmd/loom)

| Command | File | Output formats | Source of truth |
|---|---|---|---|
| `loom status` | [cmd/loom/status.go:106](cmd/loom/status.go) | text, `--json` | HUD `/api/presence`, `/api/sessions`, daemon socket via `bridge.DaemonClient` |
| `loom doctor` | [cmd/loom/cmd_doctor.go:25](cmd/loom/cmd_doctor.go) | text, `--json`, `--fix` | `pkg/generator.DoctorCheckAll()` (hook config) |
| `loom catalog list/search` | [cmd/loom/cmd_catalog.go](cmd/loom/cmd_catalog.go) | text, `--json` | `pkg/registry` + daemon enable state |
| `loom agent presence|heartbeat` | [cmd/loom/cmd_agent_presence.go:78](cmd/loom/cmd_agent_presence.go) | text + JSON-shaped response | HUD `/api/agent/heartbeat` (RPC), bridge fallback |
| `loom agent session …` | [cmd/loom/cmd_agent_session.go](cmd/loom/cmd_agent_session.go) | text | `bridge.AgentBridge` |
| `loom agent task-update` | [cmd/loom/cmd_agent_dispatch.go](cmd/loom/cmd_agent_dispatch.go) | text | `bridge.AgentBridge` |
| `loom rbac …` | [cmd/loom/cmd_rbac.go](cmd/loom/cmd_rbac.go) | text + simulation JSON | daemon RPC `loom/rbac-policy` |
| `loom secrets …` | [cmd/loom/cmd_secrets.go](cmd/loom/cmd_secrets.go) | text | daemon RPC |
| `loom mills …` | [cmd/loom/cmd_mills*.go](cmd/loom/cmd_mills.go) | text + JSON | `loom-mills-operator` REST/MCP |
| `loom hud start [--tui]` | [cmd/loom/hud.go:52](cmd/loom/hud.go) | launches HUD or TUI | embedded `internal/hud` |

Gaps: no first-class `loom cost`, `loom health`, `loom presence list`, `loom sessions list`, `loom tasks list`, `loom catalog status`. Each domain has uneven `--json` support and uneven filter/watch flags.

### HUD HTTP API (internal/hud)

32 `/api/*` routes plus `/api/mobile/v1/*` parallel surface. Categorized:

| Category | Routes | Backing types |
|---|---|---|
| Status/Health | `/api/status`, `/api/health` | monitor snapshots in [internal/hud/monitor](internal/hud/monitor) |
| Fleet/Presence | `/api/fleet`, `/api/presence` (via fleet handlers) | [internal/hud/domain/fleet](internal/hud/domain/fleet) |
| Sessions/Tasks | `/api/sessions`, `/api/sessions/{id}/trace`, `/api/tasks` | [internal/hud/bridge/agent_dto.go](internal/hud/bridge/agent_dto.go) |
| Cost | `/api/cost` (observability route) | [internal/hud/monitor/cost.go:14](internal/hud/monitor/cost.go), [internal/hud/bridge/daemon.go](internal/hud/bridge/daemon.go) `CostStatsResult` |
| RBAC | `/api/rbac` | daemon RPC `loom/rbac-policy` → [internal/hud/app_routes_observability.go](internal/hud/app_routes_observability.go) |
| OTel/Traces | `/api/otel`, `/api/traces`, `/api/timeline` | [internal/hud/eventlog.go](internal/hud/eventlog.go), traced server coverage in `8413fb8` |
| Catalog | `/api/catalog`, enable/disable POSTs | [internal/hud/api_catalog.go](internal/hud/api_catalog.go) |
| Coordinator/Workflow | `/api/coordinator/*`, `/api/agent/workflow-define`, `/api/handoffs/*` | [internal/hud/domain/workflow](internal/hud/domain/workflow), [internal/hud/coordinator](internal/hud/coordinator) |
| Sandbox | `/api/sandbox/*` | [internal/hud/domain/sandbox](internal/hud/domain/sandbox) |
| SSE | `/api/events` | [internal/hud/sse_hub.go](internal/hud/sse_hub.go) |
| Mobile v1 | `/api/mobile/v1/*` (≈30 endpoints) | [internal/hud/domain/mobile](internal/hud/domain/mobile) — **frozen contract** per [docs/MOBILE_COMPANION_API.md](docs/MOBILE_COMPANION_API.md) |

Domain registry pattern at [internal/hud/domain/domain.go:15](internal/hud/domain/domain.go) is already a contract seam — each domain registers routes via the `Domain` interface.

### TUI (internal/tui)

Panels: `fleet`, `health`, `memory`, `overview`, `presence`, `stream`, `tasks` ([internal/tui/panels/](internal/tui/panels/)). All read from shared monitor snapshots via `Client` ([internal/tui/client.go:36](internal/tui/client.go)) — `Deps` pattern at line 14 already enables HUD↔TUI co-host monitor sharing (no double polling).

Missing TUI panels (vs HUD frontend): `Cost`, `Catalog`, `Servers`, `RBAC`, `Workflows`, `Spawn`, `Mills`, `Traces`, `Topology`, `Knowledge`, `Sandbox`, `Lifecycle`, `Reasoning`, `Shuttle`, `Weaver`. Many are domain-niche, but Cost/Catalog/Servers/RBAC are first-tier visibility gaps.

### HUD Frontend (internal/hud/frontend)

Stores at [internal/hud/frontend/src/lib/stores/](internal/hud/frontend/src/lib/stores/) (≈40) and components (≈30 panels) — every store maps 1:1 to a backend route or SSE event. Shared primitives `FilterBar`, `DataTable` already exist (per [.loom/41](.loom/41-implementation-plan-hud-ux-continuation-2026-03-13.md) Phase 1).

## Already-Shared Contracts

Existing contract layers that the epic should extend, not duplicate:

- [internal/hud/bridge/agent_contracts.go](internal/hud/bridge/agent_contracts.go) — `SessionRequest`, `SessionListRequest`, `HeartbeatRequest`, `ContextInspectRequest`, `NudgePolicy`. Adopted by CLI + HUD + mobile per `8c2c50d` (#21 Stage 1).
- [internal/hud/bridge/agent_dto.go](internal/hud/bridge/agent_dto.go) — `SessionInfo`, `TaskInfo`, `PresenceInfo`. Golden-tested at [internal/contracts/](internal/contracts/) (18 golden files per [docs/CONTRACT_TESTING.md](docs/CONTRACT_TESTING.md)).
- [internal/hud/bridge/daemon.go](internal/hud/bridge/daemon.go) — `StatusResult`, `HealthEntry`, `HealthResult`, `CostStatsResult`, `CostAgentUsage`, `CostServerUsage`, `CostTotals`.
- [internal/hud/monitor/](internal/hud/monitor/) — `FleetSnapshot`, `HealthSnapshot`, `CostSnapshot`, `MemorySnapshot`, `StreamSnapshot` via `BaseMonitor[T]`. Shared by HUD handlers + TUI client + frontend stores.
- [internal/hud/bridge/local_caller.go](internal/hud/bridge/local_caller.go) + [internal/hud/embed.go](internal/hud/embed.go) — embedded HUD already a working pattern (LocalCaller routes to in-process daemon functions).
- [internal/contracts/golden_test.go](internal/contracts/golden_test.go) — drift-prevention scaffolding ready for new DTO surfaces.

## Divergence Points (the epic's actual work)

1. **Status DTO placement** — `platformStatus` is a private struct in [cmd/loom/status.go:15](cmd/loom/status.go) (CLI-only). HUD and mobile have separate aggregated status views. No single canonical `Status` DTO.
2. **Cost / RBAC CLI absence** — `CostSnapshot` and `RBACSnapshot` are only consumable through HUD HTTP. No `loom cost`, no `loom rbac status` JSON output. Operator on a remote shell with no browser has no equivalent.
3. **Catalog enablement timing** — CLI `loom catalog list` reads registry; HUD `POST /api/catalog/{name}/enable` mutates daemon enable state. Race conditions possible; no shared mutation contract.
4. **Doctor vs health scope mismatch** — `loom doctor` is hook-config-centric; HUD `/api/health` is daemon-server-centric. Operators get two non-overlapping health stories.
5. **Heartbeat semantics** — CLI heartbeat `POST /api/agent/heartbeat` (RPC, fresh) vs HUD `GET /api/presence` (cached fleet snapshot). Reads can lag writes by up to 15s ([internal/hud/monitor/fleet.go](internal/hud/monitor/fleet.go) refresh interval).
6. **TUI parity gaps** — Cost, Catalog, Servers, RBAC absent from TUI despite shared monitor pattern being trivially extensible.
7. **No machine-readable HUD API spec** — drift-tested by golden files but no OpenAPI/JSON-Schema export. External consumers (loom VS Code extension, loom-zed, mobile companion outside v1, future SDKs) read tea leaves.
8. **Embedded HUD undocumented** — `LocalCaller` + `embed.FS` already functional but not surfaced as `loom hud --embed` or library-style API for downstream Go binaries; no public docs.

## Constraints

- **Mobile v1 contract freeze** ([docs/MOBILE_COMPANION_API.md](docs/MOBILE_COMPANION_API.md)) — additive-only changes within `/api/mobile/v1`. Any contract refactor must keep golden files green.
- **Pre-1.0 stability rules** ([docs/API_STABILITY.md](docs/API_STABILITY.md)) — `internal/*` is unstable but consumed; promoting types to `pkg/` raises stability bar.
- **Existing golden files** ([internal/contracts/testdata/](internal/contracts/testdata/)) — 18 surfaces. New DTOs need new golden coverage in same PR or follow-up before code lands on `main`.
- **Domain registry pattern** ([internal/hud/domain/](internal/hud/domain/)) is the preferred extension seam. New routes should register through it, not bare mux handlers.
- **Embedded HUD already works** — UNIFY-2 is *documenting + flagging + hardening*, not new architecture.

## Prior Art In-Repo

- `8c2c50d` (Feb 17) refactor: shared agent contracts; pattern to repeat for visibility DTOs.
- `d0d1518` (UNIFY-3, Mar 1): `loom status` aggregation pattern + `--json` precedent + non-zero exit on unhealthy. Re-use for `loom cost|rbac|health|catalog status`.
- `4c2425e8` / `d501b385`: HUD error envelope unification across CLI and bridge — error wire format already converged.
- `internal/hud/eventlog.go`: ring-buffered timeline already shared by `/api/timeline` and frontend.
- `pkg/profiles/` (CONFIG-1/2): YAML-driven generator pattern that could inform a YAML-driven contract registry if needed.

## External References

- [docs/CONTRACT_TESTING.md](docs/CONTRACT_TESTING.md) — golden-file workflow.
- [docs/MOBILE_COMPANION_API.md](docs/MOBILE_COMPANION_API.md) — v1 freeze table.
- [docs/API_STABILITY.md](docs/API_STABILITY.md) — public-package surface rules.
- [docs/USER_GUIDE.md:248](docs/USER_GUIDE.md) — current contract-convergence narrative.
- [.loom/35-simplification-epics.md](.loom/35-simplification-epics.md) — EPIC 1 (SIMP) precedent for epic decomposition style.
- [.loom/41-implementation-plan-hud-ux-continuation-2026-03-13.md](.loom/41-implementation-plan-hud-ux-continuation-2026-03-13.md) — shared HUD primitives state.

## Risks

- **Contract sprawl**: tempting to extract every internal type; resist. Promote only types backing two or more *user-visible* surfaces.
- **Mobile v1 regression**: any rename or reshape that touches mobile golden files = breaking. Pin DTOs to existing JSON shape on extraction; add adapters internally if Go names change.
- **Polling vs RPC consistency**: surfaces that today read cached monitor snapshots and surfaces that today RPC the daemon will produce subtly different counts. UNIFY-1 must declare which is canonical per DTO.
- **TUI lag**: bringing Cost/RBAC/Catalog into TUI adds polling load; reuse `Deps` co-host pattern to avoid duplicate connections.
- **`pkg/` promotion**: if visibility DTOs become a `pkg/visibility`, semver pressure goes up. Could keep in `internal/visibility/contracts` initially with golden tests + docs for consumers.

## Open Questions

- Q1: Promote contracts to `pkg/visibility` or keep in `internal/visibility/contracts`? Recommendation: start internal, promote after 2 minor releases of stability.
- Q2: Generate OpenAPI from Go types (e.g., `swaggo/swag`, `kin-openapi`) or hand-author YAML and golden-test conformance? Recommendation: hand-author + handler conformance golden tests (matches existing pattern).
- Q3: Should `loom hud --embed` mean "spawn HUD in-process" (today's `LocalCaller`) or "launch foreground HUD with auto-shutdown on CLI exit"? Recommendation: the former; the latter is `loom hud start --foreground`.
- Q4: TUI parity: ship as one PR with all four missing panels or one panel per slice? Recommendation: per-panel slices for ralph cadence.

## Sources

- Command: `glab issue view 66 --repo services/loom-core`
- Command: `git log --oneline --all | grep -i UNIFY`
- Command: `git show d0d1518 --stat`
- Command: `grep -hrE 'mobile/v1|/api/' internal/hud/`
- File: [ROADMAP.md:293](ROADMAP.md)
- File: [.loom/00-index.md:56](.loom/00-index.md)
- File: [.loom/archive/roadmap-reconciliations/roadmap-reconciliation-2026-03-05.md:82](.loom/archive/roadmap-reconciliations/roadmap-reconciliation-2026-03-05.md)
- File: [docs/USER_GUIDE.md:248](docs/USER_GUIDE.md)
- File: [docs/MOBILE_COMPANION_API.md](docs/MOBILE_COMPANION_API.md)
- File: [docs/CONTRACT_TESTING.md](docs/CONTRACT_TESTING.md)
- File: [docs/API_STABILITY.md](docs/API_STABILITY.md)
- File: [internal/hud/domain/domain.go:15](internal/hud/domain/domain.go)
- File: [internal/hud/bridge/agent_contracts.go](internal/hud/bridge/agent_contracts.go)
- File: [internal/hud/embed.go](internal/hud/embed.go)
- File: [internal/tui/client.go:14](internal/tui/client.go)
