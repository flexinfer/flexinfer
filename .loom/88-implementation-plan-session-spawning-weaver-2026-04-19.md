# Implementation Plan: Session Management + Spawn Auth + Weaver Integration

**Date**: 2026-04-19
**Research**: `.loom/86-research-session-spawning-weaver-integration-2026-04-19.md`
**Product Spec**: `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md`

## Execution Order

```
Slice 1 (SESS: session status + draining + metrics + parent propagation)
    ↓
Slice 2a (AUTH Path B: cluster-agent-api-keys secret + CLI + pod mount switch)
    ↓
Slice 2b (AUTH Path A: cluster OAuth CLI + mcp-auth-refresher CronJob) — optional, ships second
    ↓
Slice 3 (WVR: backend discriminator + spawn bridge interface + router branch)
    ↓
Slice 4 (WVR wiring: DaemonSpawnBridge + telemetry stitching)
    ↓
Slice 5 (OBS + TEST: metrics, SSE, integration tests)
    ↓
Slice 6 (AUTH cleanup: retire agent-auth-tokens after one cycle)
```

Slices 1 and 2a are independent. Slice 2b is independent but depends on 2a being live so the fallback path (API key) exists if OAuth refresh fails. Slice 3 depends on Slice 1 for parent-session propagation. Slice 4 depends on Slices 2a + 3 at minimum (OAuth is optional for the WVR demo). Slice 5 runs alongside Slice 4. Slice 6 waits one release.

Estimated effort: Slice 1 ~1d, 2a ~1.5d, 2b ~2d, 3 ~1d, 4 ~1d, 5 ~1d, 6 ~0.5d.

---

## Slice 1: Session Layer Finish (SESS-001 → SESS-005)

**Spec refs**: SESS-001, SESS-002, SESS-003, SESS-004, SESS-005

### Changes

**`internal/daemon/session.go`**
- Add `Status()` method returning:
  ```go
  type SessionStatus struct {
      DaemonEpoch           int64         `json:"daemonEpoch"`
      ActiveSessions        int           `json:"activeSessions"`
      TotalSessions         int           `json:"totalSessions"`
      Draining              bool          `json:"draining"`
      OldestInFlightAgeSecs int64         `json:"oldestInFlightAgeSeconds"`
  }
  func (m *SessionManager) Status() SessionStatus { ... }
  ```
- Compute oldest-in-flight by scanning sessions for min `LastSeenAt` among active.

**`internal/daemon/session_handlers.go`**
- Add `handleSessionStatus(msg jsonrpc.Message) jsonrpc.Message` returning the new struct.
- Register under method name `loom/session/status` alongside existing `open`/`heartbeat`/`close` registration (find via grep `loom/session/open` in daemon dispatch).

**`internal/daemon/daemon.go` (or the dispatch file)**
- Before handing any `loom/call` to the tool registry, check `if d.sessions.IsDraining()` and if so, return a structured error:
  ```go
  {"code": -32099, "message": "daemon draining", "data": {"retry_after_ms": 2000}}
  ```
- Grace window for in-flight: bounded `time.After(5*time.Second)` in the drain path (existing `DrainAll` is a flag flip; shutdown code needs to actually wait).

**`internal/daemon/metrics.go`** (new or existing)
- Register Prometheus collectors:
  - `loom_session_active` (GaugeFunc reading `sessions.ActiveCount()`)
  - `loom_session_daemon_epoch` (GaugeFunc reading `sessions.Epoch()`)
  - `loom_session_reaped_total` (Counter, incremented in the reaper loop body)
  - `loom_session_evicted_total` (Counter, incremented in `evictOldestLocked`)
  - `loom_session_epoch_mismatch_total` (Counter, incremented in `Heartbeat` when epoch differs)

**`internal/daemon/session.go` — add presence join field**
- Extend `SessionClientInfo`:
  ```go
  type SessionClientInfo struct {
      AgentHint        string `json:"agentHint,omitempty"`
      HostPID          string `json:"hostPid,omitempty"`
      Version          string `json:"version,omitempty"`
      PresenceAgentID  string `json:"presenceAgentId,omitempty"` // NEW
  }
  ```
- In `handleSessionOpen` / `handleSessionHeartbeat`, when `PresenceAgentID != ""`, call `agentcontext.Presence.Heartbeat(agent_id, proxyMeta{SessionID: sess.ID})`. Use a thin interface to keep layer boundaries clean.

**`cmd/loom/proxy_session.go`**
- On `proxyOpenSession()`, read env `LOOM_AGENT_ID` (already conventional) and include in `ClientInfo.PresenceAgentID`.

**`internal/hud/spawn.go`**
- In `runSpawn()` (around line 354 where `StartOpts` is built), look up caller's `ProxySession` from request context:
  ```go
  if sessID := sessionFromContext(ctx); sessID != "" {
      startOpts.Env = append(startOpts.Env, backend.EnvVar{Name: "LOOM_PARENT_SESSION_ID", Value: sessID})
      state.Metadata["parent_session_id"] = sessID
  }
  ```
- Requires threading the proxy session through the REST handler's context middleware. Add `sessionMiddleware` that reads `X-Loom-Session-Id` header (proxy calls set it) and stores it in `context.Value`.

### Tests

**`internal/daemon/session_test.go` (existing)**
- Add `TestStatus_ReturnsEpochAndCounts`.
- Add `TestStatus_Draining`.

**`internal/daemon/session_handlers_test.go` (existing)**
- Add `TestHandleSessionStatus_RoundTrip`.

**`internal/daemon/daemon_loops_test.go` (existing)**
- Extend reaper test to assert `loom_session_reaped_total` increments.

**`internal/daemon/dispatch_test.go` (existing or new)**
- Add `TestDispatch_DrainingReturnsRetryableError`.

### Verification

```bash
go test ./internal/daemon/... -run 'Session|Drain|Reaper' -v -count=1
go test ./cmd/loom/... -run 'ProxySession' -v -count=1
go build ./cmd/loom ./cmd/loomd
```

Manual smoke: start loomd, `curl -X POST /api/daemon -d '{"method":"loom/session/status"}'` returns non-zero epoch and correct counts.

---

## Slice 2a: Cluster API-Key Auth Identity (AUTH-001, AUTH-002, AUTH-003, AUTH-006, AUTH-007, AUTH-008, AUTH-009)

**Spec refs**: AUTH-001, AUTH-002, AUTH-003, AUTH-006, AUTH-007, AUTH-008, AUTH-009
**Intent**: ship cluster-native auth with API keys + Gemini service account. Mac Keychain stops feeding pod credentials. Pods fail fast with a clear error when cluster auth is missing.

### Pre-flight (blocking)

1. Decide on the cluster's vendor identity per agent (this is a human decision, document in `.loom/40-decisions.md`):
   - **Claude**: dedicated Anthropic workspace + API key? Or share the user's org's API key with a tag?
   - **Codex**: dedicated OpenAI project + API key?
   - **Gemini**: dedicated GCP service account with Gemini API scope?
2. Obtain the initial credentials out-of-band (human goes to vendor dashboards). The CLI in AUTH-003 stores them; it doesn't mint them.

### Changes

**`platform/gitops/k3s/devbox/cluster-agent-api-keys.yaml`** (new — sibling repo)
- SOPS-encrypted Secret manifest, keys:
  - `ANTHROPIC_API_KEY`
  - `OPENAI_API_KEY`
  - `GOOGLE_APPLICATION_CREDENTIALS_JSON` (the full service-account JSON as a single string)
- Starts empty/placeholder; populated by `loom auth cluster-set-key`.

**`cmd/loom/cmd_auth.go`** (new)
- New cobra command group `loom auth` with subcommands:
  - `cluster-set-key --agent <claude|codex>` — prompts for key, writes via SOPS, commits + pushes + reconciles
  - `cluster-set-key --agent gemini --file <path>` — reads service-account JSON, writes under `GOOGLE_APPLICATION_CREDENTIALS_JSON` key
  - `status` — prints per-vendor auth state (cluster + host)
- Reuses the existing `sync-agent-tokens` SOPS helper patterns in `cmd/loom/cmd_sync_agent_tokens.go` for SOPS read/write; do not re-implement.

**`internal/hud/spawn.go`** — switch secret sources
- `agentSecretEnvVars()` at `:1117-1140`: change `const secretName = "agent-api-keys"` → `"cluster-agent-api-keys"`. Update all three cases (claude, codex, gemini) to pull from the cluster secret.
- `agentSecretMounts()` at `:1145-1163`: remove Claude-only special case. Every agent returns cluster mounts:
  - Claude: no OAuth mount in Slice 2a (cluster API-key-only path). Slice 2b adds the OAuth mount.
  - Codex: same — API-key only in 2a.
  - Gemini: mount service-account JSON from `cluster-agent-api-keys` under `/root/.gcp/sa.json`.
- `injectAgentConfig()` at `:582-637`: Claude branch — remove the `apiKeyHelper` script entirely in 2a since we're on pure env-var auth. Gemini branch — write `GOOGLE_APPLICATION_CREDENTIALS=/root/.gcp/sa.json` into the env or pod spec. Codex branch — unchanged.

**`internal/spawn/types.go`**
- Add `AuthMode string` to `SpawnState` with values `"cluster_oauth"`, `"cluster_api_key"`, `"cluster_service_account"`, `"missing"`.

**`internal/hud/spawn.go`** — AuthMode resolution + fail-fast
- After mount resolution, compute `AuthMode`. If the relevant secret key is absent (pod will start but CLI will fail with 401), set `AuthMode="missing"` and **abort the spawn** with error `"cluster auth missing for <agent>: run 'loom auth cluster-set-key --agent <agent>'"`. Fail fast beats failing inside the pod.

**`cmd/loom/cmd_sync_agent_tokens.go`** — demote
- Remove the `--apply` flag's gitops push behavior (keep the command for host-only introspection; emit a deprecation warning if `--apply` is passed).
- The launchd-installed periodic sync continues running but stops pushing to gitops.

**`README.md` / docs updates**
- Document the new auth model in a short ADR at `.loom/40-decisions.md` (reference D1-D8 from spec).

### Tests

**`internal/hud/spawn_auth_test.go`** (new)
- `TestAgentSecretEnvVars_ClusterSecretName` — all three agents pull from `cluster-agent-api-keys`
- `TestAgentSecretMounts_ClaudeNoOAuth_PathB` — Slice 2a: Claude has no OAuth mount
- `TestAgentSecretMounts_GeminiServiceAccount` — mount includes `/root/.gcp/sa.json`
- `TestAgentSecretMounts_NeverReferencesLegacySecret` — grep-style assertion: no returned mount has `SecretName == "agent-auth-tokens"`
- `TestInjectAgentConfig_PreservesMountedCredFiles` — table test across agents asserting injected paths don't overlap credential mount paths

**`internal/spawn/state_test.go`**
- `TestResolveAuthMode_ClusterAPIKey`
- `TestResolveAuthMode_Missing_FailsSpawn`

**`cmd/loom/cmd_auth_test.go`** (new)
- `TestClusterSetKey_DryRun` — produces correct SOPS YAML
- `TestStatus_RendersBothHostAndCluster`

### Verification

```bash
go test ./internal/hud/... ./internal/spawn/... ./cmd/loom/... -run 'Auth|Spawn' -v -count=1
go build ./cmd/loom ./cmd/loomd
```

Manual smoke (needs cluster):
1. `loom auth cluster-set-key --agent gemini --file ./my-sa.json` (one-shot)
2. `loom spawn --agent-type gemini --task "echo hi"`
3. `kubectl exec <pod> -- cat /root/.gcp/sa.json` shows the service-account JSON
4. Delete the Mac's `~/.gemini/` entirely; re-run the spawn → still works.

---

## Slice 2b: Cluster OAuth + In-Cluster Refresher (AUTH-004, AUTH-005)

**Spec refs**: AUTH-004 (`loom auth cluster-login`), AUTH-005 (`mcp-auth-refresher`)
**Intent**: recapture subscription pricing for Claude + Codex via cluster-owned OAuth, with in-cluster refresh that never touches Mac state.

### Changes

**`cmd/loom/cmd_auth.go`** — add `cluster-login`
- Subcommand `loom auth cluster-login --agent <claude|codex>`:
  - Starts a local HTTP server on `127.0.0.1:<random>` as OAuth redirect target
  - Opens vendor consent URL with `response_type=code` + the local redirect URI
  - On callback, exchanges code for tokens (access + refresh)
  - Builds the vendor-specific JSON (for Claude: `{"claudeAiOauth":{"accessToken":"...", "refreshToken":"...", "expiresAt":...}}`; for Codex: the `auth.json` shape Codex CLI reads natively)
  - Writes SOPS-encrypted into `cluster-agent-auth` secret under key `claude-oauth-json` or `codex-auth-json`
  - Warns if the authenticated email looks personal vs. cluster-dedicated

**`cmd/mcp-auth-refresher/main.go`** (new binary)
- Go binary deployed as k8s CronJob
- Reads in-cluster kubeconfig (RBAC: `get, patch` on Secret `cluster-agent-auth` in its own namespace)
- For each `*-oauth-json` key:
  1. Parse JSON, extract `expiresAt` (or equivalent vendor field)
  2. If `now + 90min > expiresAt`, call vendor refresh endpoint
  3. Replace the JSON with the refreshed tokens
  4. Write back via `client.CoreV1().Secrets(ns).Patch(...)` with SSA
- Emits Prometheus metrics on `:9090/metrics`:
  - `loom_auth_refresh_total{agent,outcome}` (success|skipped|error|revoked)
  - `loom_auth_refresh_expiry_seconds{agent}` — time remaining before next refresh needed
  - `loom_auth_refresh_last_success_timestamp{agent}`
- Exits 0 on success; non-zero if any vendor returned a hard error (CronJob surfaces in k8s events)

**`platform/gitops/k3s/devbox/mcp-auth-refresher.yaml`** (new — sibling repo)
- ServiceAccount + Role (get, patch on one Secret) + RoleBinding
- CronJob: every 30 min, image `registry.harbor.lan/library/mcp-auth-refresher:<pinned>`
- Resources: 50m CPU / 64Mi memory (tiny)
- PodMonitor entry for Prometheus scraping

**`platform/gitops/k3s/devbox/cluster-agent-auth.yaml`** (new — sibling repo)
- SOPS-encrypted Secret with empty placeholders for `claude-oauth-json` and `codex-auth-json`
- Populated at runtime by `loom auth cluster-login`
- Written in-place by `mcp-auth-refresher`

**`internal/hud/spawn.go`** — restore OAuth mount now that cluster secret exists
- `agentSecretMounts()`:
  ```go
  case "claude-code":
      return []backend.SecretMount{{
          SecretName: "cluster-agent-auth",
          MountPath:  "/root/.claude.auth",
          Items: []backend.SecretMountItem{{Key: "claude-oauth-json", Path: "oauth.json"}},
      }}
  case "codex":
      return []backend.SecretMount{{
          SecretName: "cluster-agent-auth",
          MountPath:  "/root/.codex",
          Items: []backend.SecretMountItem{{Key: "codex-auth-json", Path: "auth.json"}},
      }}
  ```
  Gemini stays on service-account.
- Mounts are **additive** to Slice 2a's `cluster-agent-api-keys` env — both sources coexist. If OAuth JSON is present, CLI prefers it; otherwise falls back to API key env. For Claude, restore the `apiKeyHelper` script that extracts the OAuth accessToken but falls back to `$ANTHROPIC_API_KEY`.
- `AuthMode` resolution: if OAuth mount succeeded AND the key is present, `cluster_oauth`; else if API-key env present, `cluster_api_key`; else `missing` (fail fast).

### Tests

**`cmd/mcp-auth-refresher/main_test.go`** (new)
- `TestRefresh_SkipsNonExpiring`
- `TestRefresh_RefreshesExpiring` — mock vendor endpoint returns new tokens, assert secret patched
- `TestRefresh_HandlesRevoked` — vendor returns 400, metric increments, secret untouched
- `TestRefresh_NeverTouchesHostFiles` — confirm no filesystem writes outside the pod

**`cmd/loom/cmd_auth_cluster_login_test.go`** (new)
- Mock OAuth callback flow; assert correct SOPS secret produced

**`internal/hud/spawn_auth_test.go`** (extend)
- `TestAgentSecretMounts_Claude_Path_A_OAuth` — mount present
- `TestResolveAuthMode_ClusterOAuth_PrefersOverAPIKey`

### Verification

```bash
go test ./cmd/mcp-auth-refresher/... ./cmd/loom/... ./internal/hud/... -v -count=1
go build ./cmd/mcp-auth-refresher ./cmd/loom
docker build -t mcp-auth-refresher:dev -f cmd/mcp-auth-refresher/Dockerfile .
```

Manual smoke:
1. `loom auth cluster-login --agent claude` (completes OAuth in browser)
2. `kubectl get secret cluster-agent-auth -o jsonpath='{.data.claude-oauth-json}' | base64 -d | jq .`
3. Wait for CronJob tick or trigger manually: `kubectl create job --from=cronjob/mcp-auth-refresher refresher-test -n <ns>`
4. Check logs + Prometheus metrics.
5. Spawn a Claude agent — `SpawnState.AuthMode == "cluster_oauth"`.

---

## Slice 3: Weaver Backend Discriminator + Spawn Bridge Interface (WVR-001, WVR-002, WVR-006)

**Spec refs**: WVR-001, WVR-002, WVR-006

### Changes

**`pkg/weaver/domain.go`**
- Extend `SubAgent`:
  ```go
  type SubAgent struct {
      // existing fields ...
      Backend        string          `json:"backend,omitempty" yaml:"backend,omitempty"`
      SpawnOverrides *SpawnOverrides `json:"spawn,omitempty" yaml:"spawn,omitempty"`
      RequiresSpawn  bool            `json:"requires_spawn,omitempty" yaml:"requires_spawn,omitempty"`
  }
  type SpawnOverrides struct {
      Timeout      time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
      MaxCostUSD   float64       `json:"max_cost_usd,omitempty" yaml:"max_cost_usd,omitempty"`
      MaxTurns     int           `json:"max_turns,omitempty" yaml:"max_turns,omitempty"`
      Project      string        `json:"project,omitempty" yaml:"project,omitempty"`
      UseSDKDriver bool          `json:"use_sdk_driver,omitempty" yaml:"use_sdk_driver,omitempty"`
  }
  ```
- Add a validator `SubAgent.Validate() error` that rejects `Backend="claude-code"/"codex"/"gemini"` without `RequiresSpawn: true`.

**`pkg/weaver/spawn_bridge.go` (new)**
- Define interface:
  ```go
  type SpawnBridge interface {
      Dispatch(ctx context.Context, agent SubAgent, query string, parentSessionID, weaverQueryID string) (BridgeResult, error)
  }
  type BridgeResult struct {
      SpawnID      string
      LastMessage  string
      ToolCalls    int
      TotalCostUSD float64
      StopReason   string
  }
  ```
- Default `NoopSpawnBridge` that returns a structured "no bridge configured" error. Used in unit tests and when weaver is embedded but no daemon is present (e.g., standalone `mcp-weaver`).

**`pkg/weaver/router.go`**
- Add `spawnBridge SpawnBridge` field to `Router`.
- Constructor accepts optional `WithSpawnBridge(SpawnBridge)` option.
- In `runSubAgent()` (locate via grep), branch at entry:
  ```go
  if agent.Backend != "" && agent.Backend != "flexinfer" {
      return r.spawnBridge.Dispatch(ctx, agent, query, parentSessionID, queryID)
  }
  ```
- Thread `parentSessionID` from `Query()` (read from context set by daemon dispatcher).

**`pkg/weaver/domain_yaml.go`**
- The YAML unmarshaller already handles unknown fields as zero values via struct tags, so `backend:`, `spawn:`, `requires_spawn:` should Just Work once the struct has the tags. Add a case in the loader's test matrix.

### Tests

**`pkg/weaver/domain_test.go` (existing)**
- `TestSubAgent_Validate_RequiresSpawnMissing` — `Backend: "claude-code", RequiresSpawn: false` returns error
- `TestSubAgent_Validate_FlexInferOK` — default/empty backend passes

**`pkg/weaver/router_test.go` (existing)**
- `TestRouter_NonFlexInferBackendUsesBridge` — fake `SpawnBridge` asserts `Dispatch` is called with correct args; router returns `BridgeResult.LastMessage` as subagent output
- `TestRouter_NonFlexInferBackendWithoutBridgeFails` — no `WithSpawnBridge` option → returns structured error
- `TestRouter_FlexInferBackendUsesExistingPath` — backend=="" still goes through FlexInferResponsesClient

**`pkg/weaver/domain_yaml_test.go` (existing)**
- `TestLoadOverride_BackendField` — YAML with `backend: claude-code` parses correctly

### Verification

```bash
go test ./pkg/weaver/... -v -count=1
go build ./... # ensure pkg/weaver still compiles without internal/spawn
```

---

## Slice 4: DaemonSpawnBridge + telemetry stitching (WVR-003, WVR-004, WVR-005)

**Spec refs**: WVR-003 (carried), WVR-004, WVR-005

### Changes

**`internal/daemon/weaver_spawn_bridge.go` (new)**
- Implements `pkg/weaver.SpawnBridge`:
  ```go
  type DaemonSpawnBridge struct {
      orch      *hud.SpawnOrchestrator // or the interface it satisfies
      logger    *slog.Logger
      defaultTimeout time.Duration
  }

  func (b *DaemonSpawnBridge) Dispatch(ctx context.Context, agent weaver.SubAgent, query string, parentSessionID, queryID string) (weaver.BridgeResult, error) {
      req := spawn.Request{
          AgentType:    agent.Backend,
          Task:         query,
          Project:      agent.SpawnOverrides.Project,
          Timeout:      chooseTimeout(agent, b.defaultTimeout),
          MaxCostUSD:   agent.SpawnOverrides.MaxCostUSD,
          MaxTurns:     agent.SpawnOverrides.MaxTurns,
          UseSDKDriver: agent.SpawnOverrides.UseSDKDriver,
          Metadata: map[string]string{
              "weaver_query_id":    queryID,
              "weaver_domain":      agent.Name,
              "parent_session_id":  parentSessionID,
          },
      }
      spawnID, err := b.orch.Spawn(ctx, req)
      if err != nil { return weaver.BridgeResult{}, err }
      state, err := b.orch.Wait(ctx, spawnID) // new helper — blocks until terminal state
      if err != nil { return weaver.BridgeResult{}, err }
      return weaver.BridgeResult{
          SpawnID:      spawnID,
          LastMessage:  state.Telemetry.LastMessage,
          ToolCalls:    len(state.Telemetry.ToolCalls),
          TotalCostUSD: state.Telemetry.TotalCostUSD,
          StopReason:   state.Telemetry.StopReason,
      }, nil
  }
  ```

**`internal/hud/spawn.go`**
- Add `(o *SpawnOrchestrator) Wait(ctx, spawnID)` — subscribe to SSE hub filtered by spawnID; return once state is terminal (`completed|failed|stopped|killed`). Bounded by ctx deadline.

**`internal/daemon/daemon_dispatch_weaver.go`**
- On daemon startup, wire `DaemonSpawnBridge` into the Router:
  ```go
  bridge := &weaver_spawn_bridge.DaemonSpawnBridge{orch: d.spawnOrch, logger: d.logger}
  router := weaver.NewRouter(cfg, cli, reg, weaver.WithSpawnBridge(bridge))
  ```

**`internal/daemon/daemon_dispatch_weaver.go` (scope gate)**
- In `handleWeaverQuery`: iterate `req.Domains` (or, if request doesn't enumerate domains, check registry after classification). If any domain has `RequiresSpawn: true`, require `ScopeAgentSpawn` on the JSON-RPC caller. Reject otherwise with structured `unauthorized` error.

**`pkg/weaver/router.go` — thread parentSessionID**
- `Query()` reads `parent_session_id` from incoming context metadata (daemon sets it from proxy session).
- Passes it into `runSubAgent()` which forwards to the bridge.

### Tests

**`internal/daemon/weaver_spawn_bridge_test.go` (new)**
- `TestDispatch_HappyPath_Claude` — fake orchestrator asserts correct Request built; returns a canned `SpawnState`; bridge returns BridgeResult with expected fields
- `TestDispatch_MetadataStitching` — asserts `weaver_query_id` + `parent_session_id` in `Request.Metadata`
- `TestDispatch_SpawnFailure_PropagatesError`

**`internal/daemon/daemon_dispatch_weaver_test.go`**
- `TestHandleWeaverQuery_RequiresSpawnWithoutScope_Rejects`
- `TestHandleWeaverQuery_RequiresSpawnWithScope_Accepts`

**`internal/hud/spawn_wait_test.go` (new or existing)**
- `TestWait_TerminalState` — subscribes, state transitions to completed, Wait returns
- `TestWait_ContextCancel` — ctx cancels before terminal, returns ctx err

### Verification

```bash
go test ./internal/daemon/... ./internal/hud/... ./pkg/weaver/... -v -count=1
```

Manual: add a toy `cluster-ops-claude` domain via YAML with `backend: claude-code, requires_spawn: true`. `loom weaver query "what's k8s health"` with `ScopeAgentSpawn` fires a real Claude pod, pod telemetry shows `parent_session_id` matching the proxy session, HUD SpawnPanel shows the "from weaver query" badge.

---

## Slice 5: Observability + Integration Tests (OBS-* + TEST-*)

**Spec refs**: OBS-001, OBS-002, OBS-003, TEST-001 through TEST-006 (fill any gaps)

### Changes

**`pkg/weaver/metrics.go`**
- Add `weaver_backend_dispatch_total` CounterVec `{backend, outcome}`.
- Increment from `runSubAgent()` after bridge/FlexInfer call returns.

**`internal/hud/spawn.go`**
- In `broadcastSpawnEvent()` or at spawn-create time, when `state.Metadata["weaver_query_id"] != ""`, also emit a `agent.spawn.weaver_parent` event with `{spawn_id, weaver_query_id, weaver_domain}` data.

**`internal/hud/domain/session/` (new package)**
- `handler_list.go`: `GET /api/hud/sessions` returns:
  ```json
  {
    "daemon_epoch": 12345,
    "sessions": [
      {"id": "...", "agent_hint": "claude-code", "presence_agent_id": "cc-host-1", "lease_expires": "...",
       "spawns": [{"spawn_id": "...", "agent_type": "claude-code", "status": "running"}]}
    ]
  }
  ```
- Wire into `internal/hud/app.go` router registration.

### Tests (fills TEST-001 through TEST-006 that weren't covered in prior slices)

- `TestHandleSessionStatus` (TEST-001) — already Slice 1
- `TestDispatch_DrainingReturnsRetryableError` (TEST-002) — already Slice 1
- `TestAgentSecretMounts_Codex|Gemini` (TEST-003) — already Slice 2
- `TestInjectAgentConfig_DoesNotStompOAuth` (TEST-004) — already Slice 2
- `TestRouter_NonFlexInferBackendUsesBridge` (TEST-005) — already Slice 3
- `TestHandleWeaverQuery_RequiresSpawnWithoutScope_Rejects` (TEST-006) — already Slice 4

Slice 5 adds:
- `TestSessionsHandler_JoinsSpawnsByParentID`
- `TestWeaverMetrics_BackendDispatchCounter`

### Verification

```bash
go test ./... -count=1
go vet ./...
golangci-lint run ./pkg/weaver/... ./internal/daemon/... ./internal/hud/...
make build   # sanity: binaries still compile
```

Check `curl /metrics | grep -E 'loom_session_|weaver_backend_dispatch'` shows the new metrics.

---

## Slice 6: Retire `agent-auth-tokens` (AUTH-010)

**Spec ref**: AUTH-010
**Intent**: remove the Mac-sourced path after one release cycle of clean operation on Slices 2a + 2b.

### Changes

**`cmd/loom/cmd_sync_agent_tokens.go`**
- Remove `--apply` flag and the gitops push path entirely
- Keep the command for host-file introspection (`loom agent-tokens status`)

**`platform/gitops/k3s/devbox/agent-auth-tokens.yaml`** (sibling repo)
- Delete the Secret manifest
- `kubectl delete secret agent-auth-tokens -n <ns>` on reconcile

**`bin/sync-agent-tokens`** (sibling repo)
- Delete or reduce to host-only shim

### Verification

```bash
grep -r 'agent-auth-tokens\|sync-agent-tokens' cmd/ internal/ --include='*.go'
# Should return zero results except for comments in deprecation warnings
```

---

## Execution Summary

| Slice | Primary files | New files | Est. tests | Risk |
|-------|--------------|-----------|-----------|------|
| 1: SESS finish | `internal/daemon/session*.go`, `cmd/loom/proxy_session.go`, `internal/hud/spawn.go` | 0 | 5–7 | Draining semantic hook into dispatcher needs care |
| 2a: Cluster API-key auth | `internal/hud/spawn.go`, `internal/spawn/types.go`, `cmd/loom/cmd_auth.go`, `cmd/loom/cmd_sync_agent_tokens.go` | 2 (`cmd_auth.go`, `spawn_auth_test.go`) | 7–9 | Requires sibling gitops Secret creation first; CLI-to-SOPS write path is moderately complex |
| 2b: Cluster OAuth + refresher | `internal/hud/spawn.go`, `cmd/loom/cmd_auth.go` (extend) | 2 (`cmd/mcp-auth-refresher/`, `cmd_auth_cluster_login.go`) + 2 sibling gitops manifests | 6–8 | Vendor OAuth endpoints have undocumented quirks; scope refresher RBAC tightly |
| 3: WVR interface | `pkg/weaver/{domain,router,domain_yaml}.go` | 1 (`spawn_bridge.go`) | 4–6 | None material — all additive |
| 4: WVR wiring | `internal/daemon/daemon_dispatch_weaver.go`, `internal/hud/spawn.go` (Wait) | 1 (`weaver_spawn_bridge.go`) | 5 | `Wait` has to be cancellation-safe |
| 5: OBS + integration | `pkg/weaver/metrics.go`, `internal/hud/domain/session/` | 1 pkg | 3 | Small |
| 6: Retire legacy auth-tokens | `cmd/loom/cmd_sync_agent_tokens.go` + gitops cleanup | 0 | 0 | Ships one cycle after 2a+2b prove stable |

## Key Files (reference map)

| File | Slices |
|------|--------|
| `internal/daemon/session.go` | 1 |
| `internal/daemon/session_handlers.go` | 1 |
| `internal/daemon/metrics.go` | 1, 5 |
| `cmd/loom/proxy_session.go` | 1 |
| `internal/hud/spawn.go` | 1, 2a, 2b, 4, 5 |
| `internal/spawn/types.go` | 2a |
| `cmd/loom/cmd_auth.go` (new) | 2a, 2b |
| `cmd/loom/cmd_sync_agent_tokens.go` | 2a, 6 |
| `cmd/mcp-auth-refresher/` (new binary) | 2b |
| `platform/gitops/k3s/devbox/cluster-agent-api-keys.yaml` (new) | 2a |
| `platform/gitops/k3s/devbox/cluster-agent-auth.yaml` (new) | 2b |
| `platform/gitops/k3s/devbox/mcp-auth-refresher.yaml` (new) | 2b |
| `platform/gitops/k3s/devbox/agent-auth-tokens.yaml` (delete) | 6 |
| `pkg/weaver/domain.go` | 3 |
| `pkg/weaver/router.go` | 3, 5 |
| `pkg/weaver/spawn_bridge.go` (new) | 3 |
| `pkg/weaver/domain_yaml.go` | 3 |
| `pkg/weaver/metrics.go` | 5 |
| `internal/daemon/weaver_spawn_bridge.go` (new) | 4 |
| `internal/daemon/daemon_dispatch_weaver.go` | 4 |
| `internal/hud/domain/session/` (new pkg) | 5 |

## Verification (after all slices)

```bash
go build ./... && go test ./... -count=1
go vet ./... && golangci-lint run ./...
# Manual smoke path
loom daemon restart && \
  curl -s /api/daemon -d '{"method":"loom/session/status"}' | jq . && \
  loom agent-tokens run --apply && \
  # spawn a Codex agent, verify /root/.codex/auth.json mounted:
  loom spawn --agent-type codex --task 'echo hi' && kubectl exec ... -- ls /root/.codex
```

## Rollout

1. Ship **Slice 1 (SESS)** first — pure additive, low risk, unlocks parent-session propagation.
2. Ship **Slice 2a (cluster API-key auth)** — this is the point where the Mac → cluster coupling breaks. After this slice, the gitops `cluster-agent-api-keys` secret is the single source of truth for pod auth. Document the cutover in `.loom/40-decisions.md` and make sure at least one vendor key is set before cutting over (otherwise spawns fail fast with a clear error, which is the intended behavior but still needs a warm-start).
3. Ship **Slice 2b (cluster OAuth + refresher)** to recapture subscription pricing for Claude + Codex. This slice is optional — if the user is fine with API-key pricing, skip or defer. Can run in parallel with Slice 3.
4. Ship **Slice 3 (WVR interface + stub bridge)** — no behavioral change since default backend stays `flexinfer`.
5. Ship **Slice 4 (bridge wiring)** + a single opt-in demo domain (e.g., `cluster-ops-claude`) behind `requires_spawn`. Test end-to-end against a throwaway task. Sub-agent spawns inherit the cluster secret — no Mac coupling.
6. Ship **Slice 5 (observability)**. HUD Sessions panel UI can be a follow-up; the REST endpoint alone is enough to confirm correctness.
7. After one release cycle of clean operation, ship **Slice 6** to delete `agent-auth-tokens` and remove the host→cluster push path.

Each slice is a single MR with green CI. Use `small-change-loop` or `feature-dev` for slices 1, 3, 6; `feature-dev` for 2a, 2b, 4, 5. Slices 2a and 2b both touch gitops so must include the sibling-repo PRs as part of the rollout plan (use `parallel-slice-ship` if you want the loom-core and gitops PRs to land together).

### Rollback strategy

- **Slice 2a regression**: if `cluster-agent-api-keys` is misconfigured, spawns fail fast with `AuthMode: missing` and an actionable error. Fix is a single `loom auth cluster-set-key` call; no rollback needed.
- **Slice 2b regression**: if `mcp-auth-refresher` has a bug that nukes OAuth tokens, spawns fall back to `cluster-agent-api-keys` API-key auth automatically. Refresher is `suspend`-able via `kubectl patch cronjob`.
- Both slices are additive to Mac state — rolling back never touches Mac Keychain.

## Out of Scope (reaffirmed from spec)

- Gemini SDK driver path
- Daemon-reachable-from-pod call-back (sub-sub-agent spawning)
- Per-spawn scoped credential minting
- Unifying proxy sessions and agentcontext sessions into one type
