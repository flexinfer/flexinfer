# Product Spec: Session Management + Spawn Auth Parity + Weaver Integration

**Date**: 2026-04-19
**Research**: `.loom/86-research-session-spawning-weaver-integration-2026-04-19.md`
**Implementation Plan**: `.loom/88-implementation-plan-session-spawning-weaver-2026-04-19.md`

## Goal

Turn the four layers that today know nothing about each other — proxy sessions, spawn pods, agent-context presence, weaver domains — into a single coherent system where **one correlation ID** stitches them together, **all three agent vendors** (Claude / Codex / Gemini) share the same auth contract, and **weaver can dispatch to real headless agents** instead of being FlexInfer-only.

## Non-Goals

- Replacing the proxy session-lease design — it's the foundation, not the target.
- Per-spawn scoped credentials / on-the-fly token minting (parked as P2 decision, see §Decisions).
- Gemini SDK driver — stays on legacy CLI path until a public Node SDK exists.
- Rewriting agent-context session management. We correlate with it, we don't replace it.
- New HUD panels; we surface the new data in existing panels plus one `sessions` endpoint.

## Architecture at a glance

```
      ┌──────────────────────────────────────────────────┐
      │  loomd                                           │
      │                                                  │
      │   SessionManager  ───────────  daemon_epoch      │
      │        │                                         │
      │   ┌────┴────┐  correlates   ┌────────────┐       │
      │   │ Proxy   │───────────────│ spawn      │       │
      │   │ Session │               │ Controller │       │
      │   └────┬────┘               └──────┬─────┘       │
      │        │                           │             │
      │   ┌────┴────┐  ref via query_id   ┌┴───────┐     │
      │   │ Weaver  │─────────────────────│ Spawn- │     │
      │   │ Router  │                     │ Bridge │     │
      │   └─────────┘                     └────────┘     │
      └──────────────────────────────────────────────────┘
                     │                        │
                     ▼                        ▼
           FlexInfer (HTTP)          Headless Claude / Codex / Gemini pods
                                            (mounted OAuth + API-key fallback)
```

## Changes

### P0 — Session layer finish line (SESS-*)

**SESS-001: Implement `loom/session/status`**

Return `{daemon_epoch, active_sessions, draining, oldest_in_flight_age_seconds}`. Used by `dev-upgrade`, HUD, and the weaver spawn-bridge pre-check.

Source: `.loom/tech-debt-session-architecture.md` §"New daemon methods" — spec shipped, handler missing.

**SESS-002: Draining-mode request rejection**

When `SessionManager.IsDraining()` returns true, daemon's JSON-RPC dispatch returns a structured `draining` error for new `loom/call` requests with a `retry_after_ms` hint. In-flight calls get a bounded grace period (default 5s) then hard cutoff.

**SESS-003: Prometheus session metrics**

Export:
- `loom_session_active` gauge
- `loom_session_daemon_epoch` gauge (changes on restart)
- `loom_session_reaped_total` counter
- `loom_session_evicted_total` counter (LRU)
- `loom_session_epoch_mismatch_total` counter

**SESS-004: Session ↔ presence join**

Add `SessionClientInfo.PresenceAgentID` optional field. When a proxy session opens with a non-empty `PresenceAgentID`, daemon calls `agentcontext.Presence.Heartbeat(agent_id, proxy_session=session_id)` so `agent_presence_list` shows which agents have active proxy sessions. No new presence API — just an enriched heartbeat payload.

**SESS-005: Parent session propagation into spawns**

When `SpawnOrchestrator.Spawn()` is called from a context with a `ProxySession` attached, inject env var `LOOM_PARENT_SESSION_ID=<session_id>` into the spawn pod. Recorded in `SpawnState.Metadata["parent_session_id"]` for HUD correlation.

### P0 — Cluster-native agent auth (AUTH-*)

**Principle**: the k3s cluster has its own auth identity for each agent vendor, decoupled from the developer's Mac Keychain. Mac-sourced credentials never get mounted into spawn pods, and pod-side refresh (via the refresher) never touches Mac state.

Two cluster credential stores (both SOPS-encrypted, separate from the legacy `agent-auth-tokens`):

| Secret | Purpose | Writers | Readers |
|--------|---------|---------|---------|
| `cluster-agent-api-keys` | Vendor API keys tied to the **cluster identity** (not the user's Mac identity) | `loom auth cluster-set-key` one-shot; or manual SOPS edit | All spawn pods |
| `cluster-agent-auth` | OAuth tokens obtained by the user on behalf of the cluster | `loom auth cluster-login` one-shot; `mcp-auth-refresher` CronJob | All spawn pods |

**AUTH-001: Stop writing to `agent-auth-tokens` from the cluster path**

The legacy secret remains present for back-compat (Mac-derived state) but is **no longer mounted by any spawn pod** after this slice. `cmd/loom/cmd_sync_agent_tokens.go` is demoted: it updates host-side only (no `--apply` flag that pushes to gitops). A deprecation warning fires if anyone attempts a gitops push. Source: `cmd/loom/cmd_sync_agent_tokens.go:137-156`.

**AUTH-002: New `cluster-agent-api-keys` secret + mount path (Path B, ships first)**

Create SOPS-encrypted secret at `platform/gitops/k3s/devbox/cluster-agent-api-keys.yaml` with cluster-scoped API keys:

| Key | Vendor | Notes |
|-----|--------|-------|
| `ANTHROPIC_API_KEY` | Anthropic | Workspace-scoped key, not tied to user subscription |
| `OPENAI_API_KEY` | OpenAI | Project-scoped key for Codex |
| `GOOGLE_APPLICATION_CREDENTIALS_JSON` | Google | **Service account JSON** for Gemini (preferred non-interactive auth) |

`internal/hud/spawn.go:1117-1140` `agentSecretEnvVars()` is updated:
- Source secret name changes from `agent-api-keys` → `cluster-agent-api-keys`
- Gemini mount adds `GOOGLE_APPLICATION_CREDENTIALS=/root/.gcp/sa.json` env + a volume mount pulling the service-account JSON from the same secret

**AUTH-003: `loom auth cluster-set-key --agent <x>` CLI command**

New subcommand under `loom auth`. Prompts for a key, writes to the SOPS-encrypted cluster secret, commits + pushes to gitops, triggers Flux reconcile.

```
loom auth cluster-set-key --agent claude   # prompts for Anthropic API key
loom auth cluster-set-key --agent codex    # prompts for OpenAI API key
loom auth cluster-set-key --agent gemini --file ./sa.json   # reads service account JSON
```

**AUTH-004: `loom auth cluster-login --agent <x>` — OAuth flow (Path A, Claude + Codex only)**

Interactive device-code OAuth flow scoped for cluster use:

1. Opens the vendor's OAuth consent URL in a browser
2. Receives access + refresh tokens at a local redirect
3. Writes tokens into `cluster-agent-auth` SOPS secret (keys: `claude-oauth-json`, `codex-auth-json`)
4. Commits + pushes to gitops + triggers Flux reconcile
5. Emits a warning if the user is authenticating the cluster with their personal account vs. a dedicated cluster account: "Consider creating a separate Anthropic/OpenAI account for cluster use — this OAuth session will attribute all pod activity to [you@example.com]."

**AUTH-005: `mcp-auth-refresher` in-cluster CronJob (Path A runtime)**

New Go binary `cmd/mcp-auth-refresher/` deployed as a CronJob in the k3s cluster (every 30 min). Manifest lives at `platform/gitops/k3s/devbox/mcp-auth-refresher.yaml`.

Behavior per run:
1. Read `cluster-agent-auth` secret via k8s API (RBAC: `get, patch` on that one secret in its namespace)
2. For each agent token present, parse the expiry; if `now + 90min > expiry`, call the vendor refresh endpoint
3. Write refreshed tokens back via `kubectl patch` equivalent (SSA with server-side apply)
4. Emit Prometheus metrics: `loom_auth_refresh_total{agent,outcome}`, `loom_auth_refresh_expiry_seconds{agent}`
5. **Never touch** `agent-auth-tokens` (the legacy Mac-derived secret) or anything in the user's Mac

Failure mode: if refresh fails (e.g., revoked refresh token), emit alert + mark secret key as stale. Pods continue with the stale access token until it expires, then spawns fail with explicit "cluster auth revoked, run `loom auth cluster-login`" error.

**AUTH-006: Pod mount source switches to cluster secrets**

`internal/hud/spawn.go` — `agentSecretMounts` and `agentSecretEnvVars` swap their secret references:

```go
// agentSecretEnvVars: secret name changes
const secretName = "cluster-agent-api-keys"   // was "agent-api-keys"

// agentSecretMounts: per-agent cluster OAuth mounts
case "claude-code":
    return []backend.SecretMount{{
        SecretName: "cluster-agent-auth",
        MountPath:  "/root/.claude.auth",
        Items: []backend.SecretMountItem{
            {Key: "claude-oauth-json", Path: "oauth.json"},
        },
    }}
case "codex":
    return []backend.SecretMount{{
        SecretName: "cluster-agent-auth",
        MountPath:  "/root/.codex",
        Items: []backend.SecretMountItem{
            {Key: "codex-auth-json", Path: "auth.json"},
        },
    }}
case "gemini":
    // Gemini uses service account; no OAuth mount needed
    return []backend.SecretMount{{
        SecretName: "cluster-agent-api-keys",
        MountPath:  "/root/.gcp",
        Items: []backend.SecretMountItem{
            {Key: "GOOGLE_APPLICATION_CREDENTIALS_JSON", Path: "sa.json"},
        },
    }}
```

Mounts are **read-only** (enforced by `SecretMount.ReadOnly: true` — verify backend supports; if not, document that pods MUST NOT write to mount paths).

**AUTH-007: `injectAgentConfig` updated for Gemini service-account auth**

Gemini branch of `injectAgentConfig` (`internal/hud/spawn.go:631-636`) sets `GOOGLE_APPLICATION_CREDENTIALS=/root/.gcp/sa.json` and writes a minimal `settings.json` that enables service-account auth. Claude and Codex branches unchanged (their OAuth mount paths match the CLI defaults).

**AUTH-008: `SpawnState.AuthMode` field — cluster-aware values**

Values: `"cluster_oauth"`, `"cluster_api_key"`, `"cluster_service_account"`, `"missing"`. Computed at spawn time. Surfaced in HUD spawn detail page. `"missing"` fails the spawn fast with a clear error pointing the user at `loom auth cluster-login` or `loom auth cluster-set-key`.

**AUTH-009: `loom auth status` command**

Shows per-vendor cluster auth state:
- Path B (API key / service account): present? last rotated?
- Path A (OAuth): present? access-token expires in? last refresh?
- Refresher CronJob last-success timestamp + last error

Replaces the parts of `loom agent-tokens status` that concerned the cluster. `loom agent-tokens status` keeps its host-side display only.

**AUTH-010: Migration — retire `agent-auth-tokens`**

One cycle after AUTH-001 through AUTH-009 ship:
- `loom agent-tokens run --apply` command is removed (host-only sync remains)
- `agent-auth-tokens` secret is deleted from gitops
- Any remaining `agentSecretMounts` references to it are removed (they should already be gone after AUTH-006)

### P1 — Weaver × spawn bridge (WVR-*)

**WVR-001: `SubAgent.Backend` discriminator**

Add to `pkg/weaver/domain.go:9`:

```go
type SubAgent struct {
    // ... existing fields ...
    Backend         string          `json:"backend,omitempty" yaml:"backend,omitempty"`  // "flexinfer" (default) | "claude-code" | "codex" | "gemini"
    SpawnOverrides  *SpawnOverrides `json:"spawn,omitempty" yaml:"spawn,omitempty"`
    RequiresSpawn   bool            `json:"requires_spawn,omitempty" yaml:"requires_spawn,omitempty"`
}

type SpawnOverrides struct {
    Timeout      time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
    MaxCostUSD   float64       `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
    MaxTurns     int           `json:"max_turns,omitempty" yaml:"max_turns,omitempty"`
    Project      string        `json:"project,omitempty" yaml:"project,omitempty"`
    UseSDKDriver bool          `json:"use_sdk_driver,omitempty" yaml:"use_sdk_driver,omitempty"`
}
```

`Backend == ""` defaults to `"flexinfer"` (backward compatible with all existing domains).

**WVR-002: `SpawnBridge` in weaver package**

New file `pkg/weaver/spawn_bridge.go`:

```go
type SpawnBridge interface {
    Dispatch(ctx context.Context, agent SubAgent, query string, parentSessionID string, weaverQueryID string) (BridgeResult, error)
}

type BridgeResult struct {
    SpawnID      string
    LastMessage  string
    ToolCalls    int
    TotalCostUSD float64
    StopReason   string
    Err          error
}
```

Implementation `DaemonSpawnBridge` lives in `internal/daemon/weaver_spawn_bridge.go` (not `pkg/weaver/`) to avoid `pkg/weaver` importing `internal/spawn`. Weaver consumes the interface.

**WVR-003: Router dispatches to spawn bridge for non-flexinfer backends**

In `pkg/weaver/router.go` `runSubAgent()`, branch at the top:

```go
if agent.Backend != "" && agent.Backend != "flexinfer" {
    if r.spawnBridge == nil {
        return "", fmt.Errorf("spawn bridge not configured for backend %q", agent.Backend)
    }
    return r.spawnBridge.Dispatch(ctx, agent, query, parentSessionID, queryID)
}
// ... existing FlexInfer path
```

**WVR-004: Weaver query_id → SpawnState.Metadata**

`DaemonSpawnBridge` writes `weaver_query_id` into the spawn Request's metadata. Surfaced via `GET /api/agent/spawn/{id}` (admin) + mobile equivalent. HUD SpawnPanel shows a badge "from weaver query abc123" with a link to that query's row in the weaver history.

**WVR-005: `RequiresSpawn` safety gate**

Domains with `Backend != "flexinfer"` MUST set `RequiresSpawn: true`. The daemon-level handler for `loom/weaver/query` rejects the request unless the caller holds scope `ScopeAgentSpawn` (same as `POST /api/agent/spawn/{id}/stop` in mobile API). This prevents a request flowing in via an unauthenticated channel from triggering a real pod spawn.

**WVR-006: Domain YAML support for backend**

Existing YAML override loader in `pkg/weaver/domain_yaml.go` gains support for the new `backend` / `spawn` / `requires_spawn` keys. Back-compat: files without those keys behave identically.

### P1 — Observability (OBS-*)

**OBS-001: `loom_weaver_backend_dispatch_total{backend,outcome}` counter**

Increments for every `runSubAgent` call tagged with backend (flexinfer/claude-code/codex/gemini) and outcome (success/error/timeout).

**OBS-002: Spawn SSE event `agent.spawn.weaver_parent`**

When a spawn's `Metadata["weaver_query_id"]` is non-empty, broadcast an SSE event at creation time so the HUD can wire the link without polling.

**OBS-003: Session+spawn correlation API**

`GET /api/hud/sessions` returns proxy session list with nested `{spawns: [...]}` for spawns whose `parent_session_id` matches. One JSON endpoint, fuels the new HUD Sessions panel (future) or ad-hoc debugging.

### P2 — Tests (TEST-*)

**TEST-001: `loom/session/status` handler**
- Returns current counts + draining flag
- Changes when sessions are drained

**TEST-002: Draining rejects new calls with retry hint**

**TEST-003: Cluster secret mount smoke tests**

Fake k8s backend assertions that:
- Claude spawns mount `cluster-agent-auth` → `/root/.claude.auth/oauth.json` (or fall back to env `ANTHROPIC_API_KEY` from `cluster-agent-api-keys`)
- Codex spawns mount `cluster-agent-auth` → `/root/.codex/auth.json` (or fall back to `OPENAI_API_KEY`)
- Gemini spawns mount `cluster-agent-api-keys` → `/root/.gcp/sa.json` with `GOOGLE_APPLICATION_CREDENTIALS` env pointer
- **No spawn ever references `agent-auth-tokens` after AUTH-006.** Grep-test at compile time.

**TEST-004: `injectAgentConfig` preserves mounted credential files**

Table test: for each agent type, assert `injectAgentConfig` writes only config (not credential) files. Explicitly verifies that `/root/.codex/auth.json`, `/root/.claude.auth/oauth.json`, `/root/.gcp/sa.json` are never touched by the injection step.

**TEST-007: `mcp-auth-refresher` unit tests**

- Refreshes a token whose expiry is within 90min; writes back to secret
- Skips a token whose expiry is >90min away
- Handles a revoked refresh-token gracefully; emits `loom_auth_refresh_total{outcome="revoked"}`
- Never touches host-side files (no filesystem writes to `$HOME`)

**TEST-008: `loom auth cluster-set-key` and `cluster-login` CLI**

- Dry-run mode doesn't touch gitops
- Apply mode produces correct SOPS-encrypted secret YAML
- Rejects if user is not on the gitops repo / wrong branch

**TEST-005: Weaver dispatches to spawn bridge for `Backend: claude-code`**

Fake `SpawnBridge` asserts `Dispatch` receives the right args; Router returns the bridge result as the subagent output.

**TEST-006: `RequiresSpawn` safety gate**

Handler rejects non-flexinfer weaver query without `ScopeAgentSpawn`.

## Success Criteria

1. `loom/session/status` returns correct epoch and counts; `dev-upgrade` uses it to wait-drain before restart.
2. **Mac Keychain is never read or written during any spawn.** Spawning a Codex or Gemini agent with an empty `~/.codex` / `~/.gemini` on the Mac still works as long as `cluster-agent-api-keys` / `cluster-agent-auth` are populated.
3. `loom auth cluster-set-key --agent codex` followed by a Codex spawn succeeds with `AuthMode: cluster_api_key`. Same flow with `loom auth cluster-login --agent codex` produces `AuthMode: cluster_oauth`.
4. Gemini spawns use a service-account JSON (`AuthMode: cluster_service_account`); no OAuth refresh needed.
5. `mcp-auth-refresher` CronJob refreshes OAuth tokens before expiry, writing back to `cluster-agent-auth` without triggering any Mac-side state change. Prometheus metric `loom_auth_refresh_total{outcome="success"}` increments.
6. `SpawnState.AuthMode` is populated and shown in HUD spawn detail.
7. A weaver domain `cluster-ops-claude` with `Backend: claude-code, RequiresSpawn: true` fires a real Claude spawn, whose `parent_session_id` matches the caller's proxy session and whose `weaver_query_id` matches the originating query. The spawn uses cluster auth, not the user's Mac auth.
8. `loom_session_active`, `loom_weaver_backend_dispatch_total`, `loom_auth_refresh_*` and the new SSE events are visible in HUD / Prometheus.
9. All existing tests still pass. Gate: `go build ./... && go test ./pkg/weaver/... ./internal/daemon/... ./internal/hud/... ./internal/spawn/... ./cmd/mcp-auth-refresher/...`

## Acceptance

- **Backward compatible.** Existing weaver domains (all FlexInfer) work unchanged. Existing Claude spawns keep their OAuth mount unchanged. Proxy sessions keep their wire format.
- **No new external dependencies.** The new session status / draining / presence-join is all Go stdlib + existing modules.
- **No new vendor CLI quirks.** We use the documented auth file locations (`~/.codex/auth.json`, `~/.gemini/oauth_creds.json`) — not undocumented paths. Cite: existing `cmd/loom/cmd_sync_agent_tokens.go:60-81`.

## Decisions (carried from research)

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | **Cluster auth is decoupled from Mac identity.** Pods mount from `cluster-agent-api-keys` / `cluster-agent-auth`; Mac Keychain + `~/.codex` + `~/.gemini` are host-only and never feed cluster credentials | Prevents refresh-token clobber between Mac and cluster; isolates rate-limit / audit attribution; removes blast-radius coupling |
| D2 | **Gemini uses a service account**; Claude and Codex can use either cluster-scoped API key (Path B, ships first) or cluster OAuth via `loom auth cluster-login` (Path A) | Service accounts eliminate OAuth refresh for GCP; Path B is shippable in one cycle; Path A unlocks subscription pricing later |
| D3 | **Refresh happens in-cluster via `mcp-auth-refresher` CronJob** with RBAC scoped to one secret | Decouples refresh from Mac launchd; no cross-identity writes possible |
| D4 | Default sub-agent trust model is **Inherit the cluster credentials** (pod mounts the same cluster secret the parent does) | Safe because cluster creds are bounded to cluster identity; simplest to reason about; budget enforcement (`runBudgetWatcher`) bounds blast radius |
| D5 | `Backend != flexinfer` domains **require `ScopeAgentSpawn`** via `RequiresSpawn` gate | Prevents surprise pod creation from untrusted code paths |
| D6 | Parent session ID **does** propagate into spawns (`LOOM_PARENT_SESSION_ID`) | Cheap; unlocks HUD stitching |
| D7 | Weaver does **not** import `internal/spawn`; uses an interface satisfied by a daemon-side bridge | Keeps `pkg/weaver` clean and importable from other tools |
| D8 | `loom agent-tokens` is demoted to host-only and retired after one release cycle | Removes the source of the clobber risk; no long-term dual-write path |

## Dependencies

- Slices ship in the order SESS → AUTH → WVR → OBS → TEST. Session layer unlocks propagation; auth parity unlocks Codex/Gemini being viable spawn targets; both are prerequisites for the weaver bridge being useful.
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` Track A (multi-turn control plane) is complementary; this spec doesn't require it, but once both land the HUD spawn detail page is the natural place to surface weaver parent + auth mode + parent session.

## Out of Scope (explicit)

- Gemini Node SDK driver (no upstream SDK at parity).
- Vault / HashiCorp-style dynamic credential issuance.
- Per-request scoped API keys from Anthropic/OpenAI/Google.
- Replacing or merging `agentcontext.SessionSvc` with `daemon.SessionManager`. They stay orthogonal; we add correlation, not unification.
- Making loomd reachable from inside spawn pods for sub-sub-agent spawning. Call-back-to-daemon-from-pod is a separate slice; this spec handles the happy case of frontier/host → daemon → spawn.
- Creating dedicated vendor accounts for the cluster (Anthropic/OpenAI/Google account signup). That's a human process; `loom auth cluster-login` emits a warning if the current login looks like a personal account but doesn't block.
- Hot-rotation of cluster API keys without a pod restart. Today a mount change requires pod restart; deferring any reload-on-change logic to a later cycle.
