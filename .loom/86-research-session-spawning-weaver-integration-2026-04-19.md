# Research: Enhanced Session Mgmt + Agent Spawn Auth + Weaver Integration

**Date**: 2026-04-19
**Scope**: `internal/daemon/session*.go`, `cmd/loom/proxy_session.go`, `internal/hud/spawn.go`, `internal/spawn/`, `tools/spawn-driver/`, `pkg/weaver/`, `cmd/loom/cmd_sync_agent_tokens.go`
**Drives**: `.loom/87-product-spec-session-spawning-weaver-2026-04-19.md`, `.loom/88-implementation-plan-session-spawning-weaver-2026-04-19.md`
**Predecessors**:
- `.loom/tech-debt-session-architecture.md` — proxy session-lease target (shipped, see §2.1)
- `.loom/74-76` — weaver hardening cycle (ORCHESTRA→WEAVER rename + resilience)
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` — multi-turn control plane + telemetry mapping
- `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md` — hierarchical subagent model (weaver router + specialized domains)

---

## 1. What the user asked for

> "Let's build out the plan for enhanced session management and proper spawning with auth from codex and claude and gemini subs and true integration with the weaver system."

Decomposed into four work streams:

| WS | Theme | Core question |
|----|-------|---------------|
| **A** | Session management (next tier) | The proxy/daemon lease shipped — what remains to make sessions a first-class substrate across spawn + weaver + agent-context? |
| **B** | Spawn auth parity | Why is Claude the only agent with a working OAuth-bridge today, and how do we bring Codex/Gemini to parity? |
| **C** | Sub-agent spawning | When a parent agent (frontier Claude, frontier Codex) fires an MCP tool that spawns another Claude/Codex/Gemini, how does auth flow and how do sessions correlate? |
| **D** | Weaver × spawn integration | Today weaver is FlexInfer-only. How do weaver domains dispatch to *real* headless Claude/Codex/Gemini subagents? |

---

## 2. Current State (sourced)

### 2.1 Proxy session-lease — **shipped**

The target architecture in `.loom/tech-debt-session-architecture.md` is implemented and live:

| Component | File | Status |
|-----------|------|--------|
| `ProxySession` struct (ID, PriorID, DaemonEpoch, State, LeaseExpires, ClientInfo) | `internal/daemon/session.go:22-39` | ✅ |
| `SessionManager.Open/Heartbeat/Close/Touch/ReapExpired/DrainAll` | `internal/daemon/session.go:70-207` | ✅ |
| LRU eviction at `maxSessions` (default 1000), lease 30min | `internal/daemon/session.go:53-65, 75-77` | ✅ |
| Daemon JSON-RPC handlers `loom/session/{open,heartbeat,close}` | `internal/daemon/session_handlers.go:12-116` | ✅ |
| Proxy-side lease state + heartbeat ticker (30s active, 90s idle) | `cmd/loom/proxy_session.go:18-119` | ✅ |
| Epoch-mismatch transparent reconnect | `cmd/loom/proxy_session.go:58-84` | ✅ |

**What's still missing from the original design:**

1. `loom/session/status` — advertised in spec but not implemented in `session_handlers.go`. Needed for `dev-upgrade` drain gate.
2. Daemon never advertises `draining=true` to in-flight requests; drain exists as an API (`DrainAll`) but there's no server-side hook that rejects new calls with a retry hint.
3. No Prometheus metrics on session counts / epoch bumps / reap rate. Hard to observe session-lease health in HUD today.
4. `SessionClientInfo.AgentHint` is captured but never surfaced in `agent_presence_list` — proxy sessions and the `mcp-agent-context` presence service are orthogonal.

Source: `grep -n "loom/session/status\|SessionStatus\|metrics.Session" internal/daemon/*.go` returns zero matches.

### 2.2 Agent-context sessions — **separate layer, not correlated**

- `pkg/agentcontext/svc_sessions.go` manages persistent memory sessions (Qdrant-backed, observations, handoffs).
- Presence via `agent_presence_register/heartbeat/list` lives in `pkg/agentcontext/svc_presence.go`.
- **No link** between a `ProxySession.ID` and an `agentcontext.SessionID`. The proxy tracks transport liveness; agent-context tracks semantic work lifecycles. Both layers are useful individually; their lack of correlation is the friction point.

### 2.3 Spawned agent lifecycle — mature for Claude/Codex/Gemini

| Agent | Spawn type | SDK driver | Multi-turn | Source |
|-------|-----------|------------|-----------|--------|
| `claude-code` | K8s pod | ✅ `tools/spawn-driver/src/claude-driver.ts` | ✅ control file | `internal/hud/spawn.go:275-286, 641-661` |
| `codex` | K8s pod | ✅ `tools/spawn-driver/src/codex-driver.ts` | ✅ control file | same |
| `gemini` | K8s pod | ❌ legacy CLI only (no public Node SDK) | ❌ | same |
| `mentatlab-node` | K8s pod (DAG worker) | N/A | N/A | same |

CLI versions pinned at `internal/hud/spawn.go:29-31` (`claudeCodeVersion`, `codexVersion`, `geminiVersion`). `buildAgentCommand()` at `:641-661` routes per-type.

### 2.4 Spawn auth today — Claude works, Codex/Gemini half-plumbed

Two credential paths per agent:

- **Env-var API key** — `agentSecretEnvVars()` at `internal/hud/spawn.go:1117-1140`. Reads from K8s secret `agent-api-keys`. Covers Claude (`ANTHROPIC_API_KEY`), Codex (`OPENAI_API_KEY`+`CODEX_API_KEY`), Gemini (`GEMINI_API_KEY`+`GOOGLE_API_KEY`).
- **OAuth file mount** — `agentSecretMounts()` at `internal/hud/spawn.go:1145-1163`. **Only Claude** today: mounts `claude-oauth-json` → `/root/.claude.auth/oauth.json`. `injectAgentConfig` at `:596-605` writes a `.claude/settings.json` with an `apiKeyHelper` script that extracts the OAuth `accessToken` from the JSON, falling back to `$ANTHROPIC_API_KEY`.

**The gap**: `loom agent-tokens` already syncs Codex (`~/.codex/auth.json`) and Gemini (`~/.gemini/oauth_creds.json`, `~/.gemini/google_accounts.json`) into the same `agent-auth-tokens` secret (`cmd/loom/cmd_sync_agent_tokens.go:60-81`), but those mount+inject paths are missing. Today the tokens sit in the secret, unused. Codex and Gemini spawns always fall back to API-key auth.

Source: `agentSecretMounts` switch case has `case "claude-code"` + `default: return nil`. The sync script knows about more keys than the mounter consumes.

### 2.5 Weaver × spawn — no link today

`pkg/weaver/domain.go:8-21` defines `SubAgent` as a curated tool set + system prompt + optional `Model` (FlexInfer model name). Router dispatch goes `Router.Query()` → `responses_client.go:FlexInferResponsesClient.Create()` → HTTP POST to FlexInfer. **No path** from weaver to `internal/spawn.Controller` today.

The relevant holes:

1. `SubAgent` has no discriminator for "what runs this agent?" — it's implicitly FlexInfer.
2. No bridge: `pkg/weaver/router.go` doesn't import `internal/spawn` or `internal/hud/spawn.go`.
3. No auth reuse: when (future) weaver dispatches to a real Claude sub-process, it would need to propagate OAuth + session + presence context.
4. No session stitching: a weaver query that fans out to N subagents has no correlation identifier that survives FlexInfer calls + spawn calls + agent-context updates.

---

## 3. Problem statement

Across the four layers — proxy sessions, agent-context sessions, spawn pods, weaver router — **there is no end-to-end correlation identifier and no uniform auth model.** This shows up as:

| Symptom | Root cause |
|---------|-----------|
| Can't see "which weaver query produced which spawn" in HUD | Weaver query_id (from `.loom/74-76` slice 3) is not joined to `SpawnState.SpawnID` or `agent-context.session_id` |
| Codex/Gemini spawns lose subscription auth, always pay API-key rate | `agentSecretMounts()` doesn't include them |
| Parent agent spawning a child agent has to re-authenticate from scratch | No creds-propagation contract |
| Session-lease reaper fires but can't tell HUD "draining=true" | `loom/session/status` not implemented |
| Agent-context presence list shows presence; proxy sessions show proxy state — they never cross-reference | `AgentHint` captured but never exported |

---

## 4. Key findings

### 4.1 The session-lease work IS a foundation, not a destination

Shipping `ProxySession` in `internal/daemon/session.go` unlocks the *rest* of the plan. Everything downstream — spawn correlation, weaver query stitching, HUD drain UX — should reuse the same `session_id` + `daemon_epoch` contract. Do not invent a new session concept.

### 4.2 Auth today mixes Mac identity with cluster identity — must be separated

The current `agentSecretMounts + injectAgentConfig` shape is correct *mechanically*, but the current **source of credentials is wrong**. Today:

- `loom agent-tokens` pulls creds from the developer's Mac (Keychain + `~/.codex/auth.json` + `~/.gemini/*.json`) and pushes them into the gitops-backed `agent-auth-tokens` k8s secret (`cmd/loom/cmd_sync_agent_tokens.go:60-81`).
- Pods mount that secret and run as "the user, from a disposable pod."

This has three compounding problems:

1. **Refresh-token clobber risk.** OAuth refresh tokens rotate on use (Anthropic, Google). If a pod (or a future in-cluster refresher) refreshes and writes back to the shared secret, and the sync job later pushes the Mac's Keychain state into the same secret, whichever wrote last wins — and the loser's refresh token is now invalid. The user's Mac CLI silently stops working until they re-login.
2. **Single-identity blast radius.** Every spawned pod (and every future sub-agent spawned by weaver) runs under the developer's personal account. Rate-limit exhaustion, audit-log noise, and blame for any automated misuse all attach to the human.
3. **Refresh-token quota ownership.** Vendor refresh-token usage counters belong to whoever issued the token. The Mac's refresh allowance shouldn't be spent by a cluster-side automation path.

**Target model: cluster-owned auth identity, separate from host.**

| Concept | Mac (host) identity | Cluster identity |
|---------|---------------------|------------------|
| Where credentials live | macOS Keychain + `~/.codex/auth.json` + `~/.gemini/*.json` | k8s Secret `cluster-agent-auth` (SOPS-encrypted in gitops) |
| Who authenticates | User runs `claude`, `codex`, `gemini` on Mac | User runs `loom auth cluster-login --agent <claude\|codex\|gemini>` **once** per vendor |
| Who refreshes | The CLI itself, on demand | An in-cluster refresher (CronJob or controller) that reads/writes the cluster secret |
| Failure mode | Mac re-login | Cluster re-login (does not affect Mac) |
| Rate-limit/audit attribution | User's personal account | Cluster-scoped workspace/service identity (if vendor supports it) or a dedicated secondary login |

**Key properties of the new design:**

- **Asymmetric writers**: the cluster secret is writable only by the in-cluster refresher (via k8s API) and by the `loom auth cluster-login` one-shot. Pods mount read-only. `loom agent-tokens` stops pushing to the cluster secret.
- **No Mac state touches the cluster.** The Mac Keychain is authoritative for host CLI use; the cluster secret is authoritative for pod use. `loom agent-tokens` continues to exist but is scoped to host-side convenience only (or is deprecated — see §5 D2).
- **Vendor fallback tiers**: if the cluster OAuth secret is missing or refresh fails, pods fall back to a dedicated cluster API-key secret (`cluster-agent-api-keys`), which uses vendor-issued API keys tied to the *cluster identity*, not the Mac.

**Authoritative auth-file docs per vendor:**
- Claude Code credentials: https://docs.claude.com/en/docs/claude-code/settings (apiKeyHelper + subscription OAuth via Keychain on Mac)
- Codex CLI auth: https://github.com/openai/codex-cli (reads `~/.codex/auth.json`; OAuth device-code flow supported)
- Gemini CLI auth: https://github.com/google/gemini-cli (reads `~/.gemini/oauth_creds.json`; OAuth via Google account or service-account JSON)

**Vendor support for cluster-distinct identities** (needs validation at spec time, not research):
- Anthropic: Workspaces (sub-accounts under an Org); each has its own API keys. Subscription OAuth is per-user, not per-workspace, so subscription auth in the cluster still attributes to a human — if the user wants "cluster identity" literally, they need a dedicated Anthropic account or a workspace API key.
- OpenAI: Project-scoped API keys are first-class; OAuth CLI flow yields a user-scoped token.
- Google: Service-account JSON is first-class for non-interactive / cluster workloads — strongly preferred over OAuth for cluster use.

**Implication**: for Gemini, cluster auth should prefer **service account** over OAuth (eliminates refresh entirely). For Claude and Codex, cluster auth should support both (OAuth for cost parity with subscription; API key as fallback).

### 4.3 Sub-agent spawning has a trust model question we haven't answered

When a spawned Claude (pod A) invokes an MCP tool that spawns a Codex (pod B), what credentials does pod B get?

Three possible models:

| Model | Who auths pod B | Pros | Cons |
|-------|----------------|------|------|
| **Inherit** | Pod B mounts the same host-OAuth secret | Simplest; matches host intent | Host OAuth blast radius scales with pod count |
| **Per-spawn scoped token** | Daemon mints a short-lived scoped token (e.g., API-key with budget cap) | Safer; aligns with `MaxCostUSD` budget | Requires provider-side scoped tokens (Anthropic API keys yes; Codex API keys partially; Gemini service accounts yes) |
| **Proof-of-session** | Pod B mounts just a `session_id` and calls back to loomd for auth proxying | Full audit trail | Adds latency; loomd becomes auth hop for every LLM call |

**Recommendation for spec**: default to **Inherit** for now (it's what Claude spawns do today), add per-spawn budget enforcement (already shipped at `internal/hud/spawn.go:879 runBudgetWatcher`), and park per-spawn scoped tokens as a P2 once we have usage data.

### 4.4 Weaver integration needs two knobs, not twenty

For weaver to usefully dispatch real headless agents, the minimum viable contract is:

1. **`SubAgent.Backend`** — `"flexinfer" | "claude-code" | "codex" | "gemini"` (default: flexinfer).
2. **`SubAgent.SpawnOverrides`** — optional `{timeout, max_cost_usd, max_turns, image}` that propagate into `internal/spawn.Request` when `Backend != flexinfer`.

Router dispatch then branches:

```
if subagent.Backend == "flexinfer":
    // existing FlexInferResponsesClient.Create path
else:
    // new spawn-bridge path:
    //   spawnReq := weaverSubAgentToSpawnRequest(subagent, query, query_id)
    //   spawnResult := spawnController.Spawn(ctx, spawnReq)
    //   awaitTerminal(spawnResult, timeout)
    //   domain_result = spawnResult.Telemetry.LastMessage (+ tool calls + cost)
```

The `query_id` from weaver (slice 3 of `.loom/76`) becomes the join key: it gets written to `SpawnState.Metadata["weaver_query_id"]` so HUD can show "query X spawned pods Y+Z."

### 4.5 Observability is the force multiplier

None of this is useful if you can't see it working. Every proposed addition needs a surfaced signal:

- `loom/session/status` — exposes `{daemon_epoch, active_sessions, draining, oldest_in_flight_age_s}`
- `loom_session_active{state}` Prometheus gauge
- `loom_weaver_spawn_bridge_total{agent_type,outcome}` counter when slice D ships
- Spawn SSE event `agent.spawn.weaver_parent` with `weaver_query_id` in data
- HUD `SessionsPanel.svelte` stub (new) or at least a row in the existing fleet panel

---

## 5. Open questions / decisions needed

1. **Cluster auth identity model (new D1).** Cluster uses its own credentials, decoupled from the Mac. Two paths:
   - **Path A — Cluster OAuth with in-cluster refresher**: user runs `loom auth cluster-login --agent <claude|codex>` once; tokens land in SOPS-encrypted `cluster-agent-auth` secret; a `mcp-auth-refresher` deployment refreshes independently.
   - **Path B — Cluster API keys only**: dedicated vendor API keys for cluster use, stored in `cluster-agent-api-keys`. No OAuth dance, no refresher needed, but no subscription pricing.
   - **Recommendation**: ship **Path B first** (it's one-shot and risk-free), then layer Path A for Claude + Codex to recapture subscription pricing. For Gemini, always use a **service account JSON** (Path B equivalent) — this is the vendor's recommended non-interactive auth mode.
   - **Stop `loom agent-tokens` from writing to the cluster secret.** It becomes a host-only tool (or is deprecated). The Mac Keychain is never the source of truth for pod auth.

2. **Token-refresher placement (new D2).** If Path A ships, the refresher is a new component. Options:
   - **CronJob** every 30 min reads secret, refreshes near-expiry tokens, writes back. Simple, stateless, easy to reason about. Downside: 30-min blind window on token expiry bugs.
   - **Controller/Deployment** watches secret + ticks every N seconds. More complex, tighter refresh window.
   - **Recommendation**: CronJob. Refresh window for both Claude and Codex OAuth tokens is well over 30 min (typically 1h+ access, multi-day refresh). Fits Path A nicely.

3. **Sub-agent spawn trust model (was D1).** Ship "Inherit" by default — a sub-spawn mounts the same **cluster** secret the parent does. Under the new model this is safe because the cluster secret is bounded to the cluster identity. Keep per-spawn scoped tokens as a P2 design decision.

4. **Weaver domain authorization.** If domain `cluster-ops` is declared `Backend: claude-code`, should *any* caller be able to fire it? **Recommendation**: require an explicit flag `SubAgent.RequiresSpawn bool` (plus admin-token for the REST endpoint that invokes it) so we don't accidentally ship free-for-all spawning.

5. **Session-ID scope.** Does `ProxySession.ID` propagate into spawned pods as `LOOM_PARENT_SESSION_ID`? **Recommendation yes** — cheap, makes telemetry stitching trivial.

6. **loomd socket reachability from pods.** Today spawned pods in k3s don't talk back to loomd on the host (no unix-socket mount). If we want sub-agent spawning (parent calls `loom/agent/spawn` from inside pod), the daemon needs a bounded HTTP endpoint reachable from pods. **Park for a later slice.** Not on the critical path for this plan.

7. **`loom agent-tokens` deprecation path.** Keep-with-warning vs. remove. **Recommendation**: keep it running but flip its default target to **host-only** (updates `~/.config/loom/` or similar) and emit a deprecation warning if it detects a `--cluster`/gitops sync flag. Remove the gitops sync after one release cycle.

---

## 6. Sources

- `internal/daemon/session.go:22-255` — session manager + reaper
- `internal/daemon/session_handlers.go:12-116` — `loom/session/open` + `heartbeat`; no `status` handler yet
- `cmd/loom/proxy_session.go:18-119` — proxy-side lease keepalive
- `internal/hud/spawn.go:641-661` — `buildAgentCommand` (legacy CLI path)
- `internal/hud/spawn.go:1117-1163` — `agentSecretEnvVars` + `agentSecretMounts`
- `internal/hud/spawn.go:582-637` — `injectAgentConfig` (Claude apiKeyHelper, Codex config.toml, Gemini settings.json)
- `cmd/loom/cmd_sync_agent_tokens.go:60-81` — `loom sync agent-tokens` pulls Codex + Gemini + Claude creds
- `pkg/weaver/domain.go:8-21` — `SubAgent` struct with no Backend discriminator
- `pkg/weaver/router.go` + `responses_client.go` — FlexInfer-only dispatch
- `pkg/agentcontext/svc_sessions.go`, `svc_presence.go` — orthogonal session+presence layer
- `.loom/tech-debt-session-architecture.md` — original proxy session spec (now shipped)
- `.loom/82-plan-headless-agent-fullstack-2026-04-07.md` §3 — canonical telemetry mapping
- `.loom/64-planning-next-gen-skills-agents-orchestration-2026-03-29.md` §4 — subagent model
- `grep -n "loom/session/status\|handleSessionStatus" internal/daemon/*.go` — 0 matches (status endpoint not implemented)
