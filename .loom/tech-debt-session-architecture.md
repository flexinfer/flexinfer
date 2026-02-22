# Session Architecture Target (Stale Socket + Hot Reload Resilience)

## Goals

- No hung agents from stale sockets or daemon restarts.
- CLI/proxy calls remain bounded and self-healing.
- Daemon/proxy/server hot rebuilds/reloads are transparent to active clients.
- Local loom-mode remains CPU/memory efficient on developer laptops.

## Non-Goals

- Breaking changes to existing MCP client setup (`loom proxy` entry stays stable).
- Replacing Streamable HTTP session semantics.

## Core Model

Introduce a unified **Session Lease** for local proxy clients (mirrors HTTP session behavior):

- `session_id`: stable UUID per proxy process lifecycle.
- `daemon_epoch`: monotonic generation value incremented on daemon startup/restart.
- `lease_expires_at`: idle lease expiry timestamp.
- `state`: `active | draining | expired | closed`.
- `last_seen_at`: heartbeat/update timestamp.
- `client_info`: agent hint + host/pid fingerprint.

### Why

- Local stdio path currently has no durable session contract, so reconnect behavior is heuristic-only.
- Epoch mismatch provides deterministic stale-daemon detection without string parsing.

## Protocol Additions (Additive)

### New daemon methods

- `loom/session/open`
  - Input: `client_info`, optional prior `session_id`.
  - Output: `session_id`, `daemon_epoch`, lease metadata.
- `loom/session/heartbeat`
  - Input: `session_id`, `daemon_epoch`.
  - Output: refreshed lease or `epoch_mismatch`.
- `loom/session/status`
  - Output: active session count, draining flag, oldest in-flight age.
- `loom/session/close`
  - Input: `session_id`.

### Existing method extensions

- `loom/status` gains additive fields:
  - `daemon_epoch`
  - `active_proxy_sessions`
  - `draining`

## Runtime Behavior

### Proxy startup

1. `initialize` to client immediately (current behavior preserved).
2. lazily call `loom/session/open` on first daemon-bound method.
3. cache `session_id` + `daemon_epoch`.

### Normal calls

1. Create call context with explicit timeout.
2. Attach `session_id` + `daemon_epoch` in `loom/call` metadata.
3. On success, update lightweight heartbeat timestamp.

### On transport failure

1. classify transport error via `errors.Is`/`net.Error`.
2. reset transport state atomically.
3. reconnect with bounded exponential backoff.
4. call `loom/session/open` with prior session id for best-effort resume.
5. if `epoch_mismatch`, transparently accept new lease and retry once.

### Daemon reload/restart

- Daemon enters `draining` state before shutdown/reload.
- New calls receive retryable `daemon_draining` error with backoff hint.
- In-flight calls get grace window, then hard cutoff.
- After restart, `daemon_epoch` increments; proxies re-open/resume lease.

## CPU/Memory Efficiency Constraints

- Session table bounded (LRU + idle reap).
- Heartbeat interval adaptive (active: 5s, idle: 30s).
- Health monitor defaults to passive checks on running/pool state.
- Deep probe process spawn moved to low-frequency fallback mode.

## Test Matrix

- Proxy session survives daemon restart without IDE/client restart.
- Epoch mismatch recovery path retries exactly once and succeeds.
- Draining mode rejects new calls quickly, does not hang.
- Stale socket path recovery works for ENOENT, ECONNRESET, EPIPE, EOF.
- Resource profile test proves reduced process churn in health checks.

## Rollout

1. Ship additive daemon session/status APIs.
2. Teach proxy to consume APIs behind feature flag.
3. Enable by default after restart-chaos suite is green.
4. Update `dev-upgrade` to use `loom/session/status` drain gate.

## Compatibility

- Legacy clients still work via existing initialize/tools forwarding.
- New fields/methods are additive; unknown-method fallback preserved.
