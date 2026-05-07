# Visibility Runbook (Cross-Surface Diagnostics)

Scope: turning a vague operator question — "is anything wrong right now?" — into a definitive answer using the unified visibility surfaces shipped under EPIC 2 (#66).

Audience: anyone driving loom-core in production (you, on-call, devops, or a future Claude session).

## Surfaces and when to reach for each

| Surface | Best for | Latency | Mutates? |
|---|---|---|---|
| `loom status` (CLI) | One-glance "is the daemon up + how many agents/sessions" | <1s | no |
| `loom hud` (web) | Visual investigation, panels for cost/health/RBAC, SSE-driven live updates | seconds | yes (limited) |
| `loom hud --tui` (terminal) | Same dashboard headless; SSH-friendly; no browser | seconds | partial |
| `loom hud --embed` | Throwaway in-process HUD for quick local triage; no daemon socket needed | seconds | no |
| `loom health [--json] [--watch]` | Per-MCP-server health snapshot | <1s | no |
| `loom cost [--by …] [--json] [--watch]` | Cost/usage rollups | <1s | no |
| `loom rbac status [--json]` | RBAC posture + recent denials | <1s | no |
| `loom presence list [--filter …]` | Active agent inventory | <1s | no |
| `loom sessions list [--filter …]` | Live session inventory | <1s | no |
| `loom tasks list [--filter …]` | Task backlog | <1s | no |
| `docs/api/openapi.yaml` | Spec for downstream tooling (loom-zed, mobile companion outside v1) | n/a | no |

All CLI commands above use the contracts package at `internal/visibility/contracts/` for JSON output, so JSON is stable across them.

## Standard investigation flow

1. **Glance**: `loom status` — daemon up? agents/sessions count sane? exit non-zero = stop and dig in.
2. **Health**: `loom health` — any server `down`? any latency outlier? if yes → `loom hud --tui` and switch to the Health panel for the sparkline.
3. **Errors**: `loom cost --by server` — any server with non-zero `ERR`? cross-reference with health.
4. **Security**: `loom rbac status --json | jq '.recent_denied'` — any unexpected denials? if yes → check `loom hud --tui` RBAC panel for context (audit must be enabled).
5. **Agents**: `loom presence list --filter status=active` — any agent missing? `loom sessions list --filter agent=$AGENT` to see what they're doing.
6. **Tasks**: `loom tasks list --filter status=blocked` — anyone stuck? cross-reference with sessions/presence.

## CLI ↔ TUI ↔ HUD parity

Every visibility surface in this runbook has at least two of CLI/TUI/HUD.

| Surface | CLI | TUI panel | HUD panel |
|---|---|---|---|
| Daemon status | `loom status` | Overview tab (`1`) | OverviewPanel |
| Server health | `loom health` | Health tab (`3`) | HealthPanel |
| Cost/usage | `loom cost` | **Cost tab (`8`)** ← new | OverviewPanel cost row |
| RBAC posture | `loom rbac status` | **RBAC tab (`9`)** ← new | ServersPanel RBAC section |
| Sessions | `loom sessions list` | Fleet tab (`2`) | FleetPanel |
| Tasks | `loom tasks list` | Tasks tab (`4`) | TasksPanel |
| Presence | `loom presence list` | Presence tab (`7`) | PresencePanel |

When you don't know which to use: the CLI `--json` output round-trips through the same contracts package the HUD/TUI render from, so script with CLI and explore with HUD/TUI.

## Same investigation, three surfaces

**Question**: "Why is `agent claude-code-1` not making progress on its current session?"

### Via CLI
```bash
# 1. Confirm presence
loom presence list --filter agent_id=claude-code-1 --json | jq

# 2. Find the session
loom sessions list --filter agent=claude-code-1 --json | jq '.[0]'

# 3. Look at recent task state
loom tasks list --filter agent=claude-code-1 --json | jq '.[] | select(.status=="blocked")'

# 4. Check for RBAC blocks against this agent
loom rbac status --json | jq '.recent_denied[] | select(.agent_id=="claude-code-1")'
```

### Via TUI
```bash
loom hud --tui
# Press 7 → Presence: confirm agent is active
# Press 2 → Fleet: find the session row
# Press 4 → Tasks: filter blocked
# Press 9 → RBAC: scan recent denials
```

### Via HUD
```bash
loom hud start          # if not running
open http://localhost:5052
# Click Presence → filter agent_id=claude-code-1
# Click Fleet → drill into the session
# Click Tasks → status filter
# Click Servers → RBAC section
```

All three surfaces use the same daemon RPC backends. If the CLI says one thing and the HUD says another, the daemon is the source of truth and the cached snapshot in HUD/TUI is up to ~30s stale.

## Embedded HUD as a triage tool

When you don't want to disturb the running daemon (or there isn't one), `loom hud --embed` spins up an in-process HUD on an ephemeral port using `bridge.LocalCaller`:

```bash
loom hud --embed
# loom hud (embed): http://127.0.0.1:53112/
```

The embed has empty monitor caches (no real daemon dispatch) by default. It's most useful as a *UI sandbox* — to see the panels rendered against fixture data, or as a pattern for downstream Go binaries that want to embed the HUD library. See [docs/HUD_EMBEDDING.md](../HUD_EMBEDDING.md).

For production triage, prefer `loom hud` against a real daemon.

## Mobile companion

The mobile companion app uses a separate frozen contract (`/api/mobile/v1/*`). See [docs/MOBILE_COMPANION_API.md](../MOBILE_COMPANION_API.md). The visibility runbook does not cover mobile-only flows; mobile is a derived view on top of the same daemon.

## Adding a new visibility surface

1. Add the DTO type to `internal/visibility/contracts/<surface>/types.go`.
2. Add a golden test in `internal/contracts/golden_visibility_test.go` (run with `-update-golden` to seed).
3. Author the CLI command in `cmd/loom/cmd_<surface>.go` mirroring `cmd_health.go`. Use `cmd/loom/internal/render` for output.
4. Add a TUI panel in `internal/tui/panels/<surface>.go` mirroring `panels/cost.go`. Wire into `app.go`, `keymap.go`, `app_commands.go`, `client.go`.
5. Add an OpenAPI path + schema in `docs/api/openapi.yaml`. Add a conformance test in `internal/contracts/openapi_conformance_test.go`.
6. Document in this runbook's parity table.

If you skip step 5, downstream tools (loom-zed, future SDKs) won't see your surface.

## Sources

- `internal/visibility/contracts/` — DTO source of truth.
- `internal/contracts/openapi_conformance_test.go` — drift gate.
- `cmd/loom/internal/render/` — shared CLI rendering.
- `internal/tui/panels/` — TUI panel implementations.
- `docs/api/openapi.yaml` — machine-readable HTTP contract.
- `docs/HUD_EMBEDDING.md` — embedding the HUD library.
- `docs/CONTRACT_TESTING.md` — golden-file workflow.
- `.loom/103-product-spec-unify-visibility-2026-05-06.md` — EPIC 2 product spec.
- `.loom/104-implementation-plan-unify-visibility-2026-05-06.md` — slice-by-slice plan.
