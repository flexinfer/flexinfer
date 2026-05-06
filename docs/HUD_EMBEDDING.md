# Embedded HUD Library API

This guide documents the `internal/hud` package's embedded mode: how downstream
Go consumers can mount the Loom HUD inside their own process, share monitors
with co-hosted UIs, and manage its lifecycle.

> **Stability:** The embedding surface is pre-1.0. The function signatures and
> wiring patterns here are correct as of `internal/hud/embed.go` on `main` but
> MAY change in minor versions. Pin to a tagged Loom Core release if you need
> a stable interface for an external consumer.

> **Forward link:** This doc covers the in-process library API only. The
> `loom hud --embed` CLI flag (which spins up a co-hosted daemon + HUD from a
> single process) is tracked separately as **UNIFY-2b / Slice S6** and is NOT
> part of this slice.

## Table of Contents

- [When to Embed](#when-to-embed)
- [Two Embedding Modes](#two-embedding-modes)
- [Constructor API](#constructor-api)
- [Minimal Example: In-Process](#minimal-example-in-process)
- [Sharing Monitors With a Co-Hosted TUI](#sharing-monitors-with-a-co-hosted-tui)
- [Cache Invalidation and `RefreshMonitors`](#cache-invalidation-and-refreshmonitors)
- [Lifecycle and Signal Handling](#lifecycle-and-signal-handling)
- [Frontend Embedding](#frontend-embedding)
- [Limitations](#limitations)
- [References](#references)

## When to Embed

Embed the HUD when:

- A single binary needs to expose both the daemon's MCP routing and the HUD's
  HTTP/SSE surface (e.g., the `loomd` cluster build, `loom hud --embed`).
- A downstream Go program wants the HUD's REST/SSE endpoints without spawning
  a separate process or maintaining a Unix socket.
- A co-hosted TUI (`internal/tui`) and the web HUD must share one daemon
  connection and one set of polling monitors to avoid double-RPC pressure on
  the daemon.

Skip embedding and use the standalone `loom hud` command when:

- The HUD must run on a different host than the daemon.
- You need process isolation for stability or security.
- You want the HUD to consume the daemon's external SSE event stream rather
  than dispatch in-process (the embedded mode does NOT consume the daemon's
  SSE event consumer — see [Cache Invalidation](#cache-invalidation-and-refreshmonitors)).

## Two Embedding Modes

The HUD has a single `bridge.Caller` interface that abstracts how it talks to
the daemon. Two implementations cover the embedding modes:

| Mode | Caller | Transport | Use Case |
|------|--------|-----------|----------|
| **Subprocess** | `bridge.NewDaemonClient(socketPath, logger)` | Unix socket JSON-RPC | Default `loom hud` standalone CLI; remote daemons |
| **In-process** | `bridge.NewLocalCaller(dispatch)` | Direct function call | Embedded daemon + HUD; same-process co-host |

Both implementations satisfy `bridge.Caller`
(see [`internal/hud/bridge/caller.go:12`](../internal/hud/bridge/caller.go)).

The in-process `LocalCaller` skips the socket, the JSON-RPC framing on the
wire, and the circuit breaker — calls go straight to a `Dispatch` function
with the same signature as the daemon's `handleMessage`
(see [`internal/hud/bridge/local_caller.go:14`](../internal/hud/bridge/local_caller.go)).
This is materially faster than subprocess mode and removes the failure mode
where the daemon and HUD disagree about socket health.

## Constructor API

The HUD exposes three top-level functions for embedding consumers:

```go
// hud.NewApp constructs the HUD App. Caller-injected dependencies
// (daemon caller, logger) are stored on the App; background work
// has not started yet.
func NewApp(cfg Config, caller bridge.Caller, logger *slog.Logger) (*App, error)

// (*App).StartMonitors begins all background monitor polling and
// initializes optional components (coordinator, spawn orchestrator,
// SSE hub, alert engine). Idempotent guards are NOT provided —
// call exactly once per App.
func (a *App) StartMonitors(ctx context.Context) error

// (*App).RegisterRoutes mounts the HUD's HTTP routes on the given mux.
// This is the same route set the standalone HUD installs.
func (a *App) RegisterRoutes(mux *http.ServeMux)
```

Plus two lifecycle helpers:

```go
// (*App).RefreshMonitors forces a one-shot best-effort refresh of all
// embedded snapshots. See "Cache Invalidation" below for when to call.
func (a *App) RefreshMonitors()

// (*App).StopMonitors stops every background monitor, the coordinator,
// the cache, and the OTel tracer. Always call from a defer in the
// owning process so cleanup runs on shutdown.
func (a *App) StopMonitors()
```

`Config` is documented inline at
[`internal/hud/app.go:33`](../internal/hud/app.go) and covers TLS, mobile
operator auth, spawn orchestrator wiring, pipeline monitoring, and webhook
push. For embedding you typically populate only the subset of fields you need
and leave the rest at zero values.

## Minimal Example: In-Process

The smallest embedded HUD wires a `LocalCaller` to a daemon dispatch function,
constructs an `App`, starts monitors, and mounts routes on a shared
`http.ServeMux`:

```go
package main

import (
    "context"
    "log/slog"
    "net/http"
    "os"

    "github.com/crb2nu/loom/internal/hud"
    "github.com/crb2nu/loom/internal/hud/bridge"
)

// dispatch is your daemon's MCP message handler. In loomd this is the
// unexported (*Daemon).handleMessage method; for a custom embedder you
// supply any function that satisfies bridge.Dispatch.
var dispatch bridge.Dispatch // = daemon.HandleMessage, for example

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).
        With("component", "embedded-hud")

    cfg := hud.Config{
        BindAddress: "127.0.0.1",
        Port:        7777,
        // RegistryPath, AdminToken, etc. as required by your deployment.
    }

    caller := bridge.NewLocalCaller(dispatch)

    app, err := hud.NewApp(cfg, caller, logger)
    if err != nil {
        logger.Error("hud.NewApp failed", "error", err)
        os.Exit(1)
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    if err := app.StartMonitors(ctx); err != nil {
        logger.Error("StartMonitors failed", "error", err)
        os.Exit(1)
    }
    defer app.StopMonitors()

    mux := http.NewServeMux()
    app.RegisterRoutes(mux)

    // Refresh in the background so monitor warm-up doesn't block the listener.
    go app.RefreshMonitors()

    if err := http.ListenAndServe(cfg.BindAddress+":7777", mux); err != nil {
        logger.Error("listen", "error", err)
    }
}
```

The reference implementation lives at
[`internal/daemon/hud_embed.go`](../internal/daemon/hud_embed.go) — that file
is what the production `loomd` build uses to mount the HUD on the daemon's
HTTP server.

## Sharing Monitors With a Co-Hosted TUI

When the HUD and the bubbletea TUI co-host (the `loom hud --tui` flag), they
must share monitors. Otherwise both UIs poll the daemon independently, which
doubles RPC pressure and can serve inconsistent snapshots.

The TUI exposes a `tui.Deps` struct that accepts pre-existing monitors owned
by the HUD (see
[`internal/tui/client.go:11`](../internal/tui/client.go)):

```go
type Deps struct {
    Agent  *bridge.AgentBridge
    Fleet  *monitor.FleetMonitor
    Health *monitor.HealthMonitor
    Memory *monitor.MemoryMonitor
    Stream *monitor.StreamMonitor
}
```

The HUD's `App` keeps these monitors as unexported fields. The standalone
runtime constructs a `tui.Deps` from them before launching bubbletea — see
[`internal/hud/runtime.go:287`](../internal/hud/runtime.go) (`runStandaloneTUI`).

`tui.NewClientFromDeps(deps, logger)` returns a `*tui.Client` whose `Start()`
and `Stop()` are no-ops — the HUD owns the monitor lifecycle. This guarantees:

- Exactly one polling loop per monitor type.
- The TUI sees the same `FleetSnapshot` the web dashboard renders.
- `(*App).StopMonitors()` is the single cleanup site.

If you build a custom embedder that needs the TUI, follow the
`runStandaloneTUI` pattern: hand the App's monitors into `tui.Deps`, run the
bubbletea program, and let `StopMonitors()` clean up after the program exits.

## Cache Invalidation and `RefreshMonitors`

Standalone `loom hud` consumes the daemon's HTTP SSE event stream and uses
those events to trigger monitor refreshes (see `startStandaloneEventConsumer`
at [`internal/hud/runtime.go:73`](../internal/hud/runtime.go)). Embedded mode
deliberately does NOT consume that stream — both sides are in the same process,
so the daemon's external event endpoint is bypassed.

The trade-off: embedded HUDs must explicitly refresh after startup and after
daemon reloads, or they serve stale snapshots until the next polling tick
(15s for fleet, 5–30s for the others — see the polling cadences logged in
`StartMonitors`).

`RefreshMonitors()` handles this: it calls `Refresh()` on every monitor,
retries the fleet refresh once if the first call returned an empty snapshot,
and falls back to a 10-minute cache (`embeddedFleetSnapshotCacheKey`,
`embeddedSnapshotCacheTTL`) if the live refresh produced nothing.

When to call:

- Once shortly after `StartMonitors()` returns. The daemon's
  `startEmbeddedHUD` calls it via `go app.RefreshMonitors()` so it happens
  off the request-serving goroutine.
- After the daemon hot-reloads its config or rebuilds its tool registry.
- Before serving an externally-facing endpoint that must reflect current
  state (rare — most consumers can rely on the polling cadence).

The cache lives at:

```go
const (
    embeddedFleetSnapshotCacheKey    = "hud:embedded:fleet_snapshot"
    embeddedPipelineSnapshotCacheKey = "hud:embedded:pipeline_snapshot"
    embeddedSnapshotCacheTTL         = 10 * time.Minute
)
```

Defined in [`internal/hud/embed.go:24`](../internal/hud/embed.go). The cache
backend is the same `loomcache.Store` (memory or Redis) that the HUD's
non-embedded mode uses, configured via `LOOM_CACHE_*` environment variables
read by `loomcache.LoadConfigFromEnv()`.

## Lifecycle and Signal Handling

The embedded HUD has no built-in signal handling — the host process owns
that responsibility. The standalone HUD shows the canonical pattern at
[`internal/hud/runtime.go:249`](../internal/hud/runtime.go):

```go
ctx, stop := signal.NotifyContext(context.Background(),
    syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
defer stop()
```

Embedders should:

1. Pass a cancellable `context.Context` to `StartMonitors(ctx)`. The
   spawn orchestrator's reconcile loop and the session reaper goroutines
   all derive from this context.
2. Defer `(*App).StopMonitors()` so monitors, the coordinator, the cache,
   and the OTel tracer get torn down on exit.
3. Cancel the context before returning so background goroutines stop.
4. Shut down the HTTP server with a bounded timeout (the standalone
   runtime uses 5 seconds — see `runtime.go:265`).

Note that `StartMonitors` is **not** idempotent — call it exactly once per
`App`. Calling it twice will double-start each monitor goroutine.

## Frontend Embedding

The Svelte frontend is compiled into the binary at build time via Go's
`embed.FS`:

```go
//go:embed frontend/dist
var frontendFS embed.FS
```

Defined at [`internal/hud/app.go:29`](../internal/hud/app.go). The route
handler at [`internal/hud/routes.go:100`](../internal/hud/routes.go) does
`fs.Sub(frontendFS, "frontend/dist")` to serve `index.html` and the static
assets directly from the binary.

Implications for embedders:

- No runtime asset path is required. The binary is fully self-contained.
- The frontend must be built (`make build` or the CI pipeline's pnpm build
  step) before `go build` so `frontend/dist` is populated. An empty
  `frontend/dist` produces a binary that serves a 404 for the root.
- `Config.Dev = true` skips the embedded FS and proxies frontend requests
  to the local Vite dev server. Use this only during HUD UI development;
  production embedders leave `Dev = false`.

## Limitations

- **Pre-1.0 surface.** `NewApp`, `StartMonitors`, `RegisterRoutes`,
  `RefreshMonitors`, and `StopMonitors` are stable in current releases but
  may change in minor versions. Pin to a tagged release for downstream
  stability.
- **Single `App` per process.** The HUD assumes process-global resources
  (OTel tracer, cache, port file). Constructing two `App` instances in the
  same process is unsupported.
- **No SSE event consumption.** Embedded mode does not subscribe to the
  daemon's SSE event endpoint. Monitor refreshes run on their fixed
  polling cadence; use `RefreshMonitors()` for explicit synchronization.
- **Frontend build coupling.** The binary embeds `frontend/dist` at compile
  time. Rebuilding the Svelte frontend requires a full `go build` to update
  the embedded assets.
- **Caller responsibility for monitors.** Monitor lifecycle is owned by the
  `App`. Co-hosted UIs (TUI) must use the `tui.Deps` borrow pattern and
  must NOT call `Start()` / `Stop()` on borrowed monitors.

## References

- Embed entry point: [`internal/hud/embed.go`](../internal/hud/embed.go)
- App struct + Config: [`internal/hud/app.go`](../internal/hud/app.go)
- Standalone runtime (reference for signal/lifecycle pattern):
  [`internal/hud/runtime.go`](../internal/hud/runtime.go)
- In-process caller: [`internal/hud/bridge/local_caller.go`](../internal/hud/bridge/local_caller.go)
- Caller interface: [`internal/hud/bridge/caller.go`](../internal/hud/bridge/caller.go)
- Daemon-side embedding wiring (production reference):
  [`internal/daemon/hud_embed.go`](../internal/daemon/hud_embed.go)
- TUI co-host pattern: [`internal/tui/client.go`](../internal/tui/client.go)
- Architecture overview: [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)
- API stability policy: [`docs/API_STABILITY.md`](API_STABILITY.md)
