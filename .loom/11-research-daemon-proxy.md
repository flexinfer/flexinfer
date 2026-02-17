# Research Brief: Daemon & Proxy Reliability Patterns

## Problem

The `loomd` daemon uses flock-based singleton enforcement and dial-based stale socket detection. The proxy auto-starts the daemon via LaunchAgent kickstart with direct-spawn fallback. While functional, the current implementation has race conditions, no test coverage for critical paths, and lacks patterns recommended by production daemon literature.

## Questions

- Q1: What is the best-practice pattern for Go daemon singleton enforcement on macOS?
- Q2: How should stale socket detection work atomically with lock acquisition?
- Q3: What are the recommended patterns for LaunchAgent-based daemon autostart?
- Q4: How should graceful shutdown coordinate child process cleanup?
- Q5: What testing patterns work for flock, sockets, and process lifecycle?

## Constraints

- Target platform: macOS (primary), Linux (secondary/K8s).
- No systemd. macOS uses launchd/LaunchAgent for process supervision.
- Abstract socket namespace (`@` prefix) is Linux-only, not available on macOS.
- Daemon manages child MCP server processes via stdin/stdout pipes.
- Proxy is a short-lived stdio bridge spawned per-client by IDE/agent.

## Method

- Tavily advanced search across 5 topic areas (25 results analyzed).
- Reviewed production patterns from: systemd socket activation, launchd best practices, Go graceful shutdown guides, flock semantics (Linux/BSD), Go testing patterns for sockets and mocks.
- Cross-referenced with current implementation in `daemon.go`, `proxy.go`, `daemon_control.go`.

## Findings

### F1: Lock-then-socket ordering eliminates the race (source: flock(2) man page, systemd patterns)

**Current issue:** `acquireLock()` is called at `daemon.go:345` and stale socket detection at `daemon.go:379-391`. The lock is acquired first, which is correct. However, the stale socket check (dial test) has a TOCTOU window: between the stat+dial and the `os.Remove`, another process could bind the socket.

**Best practice:** The flock lock should be the authoritative singleton gate. Once the lock is held, the socket is owned by the lock holder. The pattern should be:

1. Acquire flock (non-blocking, fail if held) -- **already implemented correctly**
2. If socket exists, remove it unconditionally (lock holder owns the socket path)
3. Bind the new socket

The dial-test is redundant when the lock is held, because:
- If another daemon is running, it holds the lock, so step 1 fails
- If no daemon holds the lock, the socket is stale by definition
- The dial-test adds 200ms latency and a race window for no benefit

**Exception:** The dial-test remains useful as a defense-in-depth fallback if the lock file is on a filesystem where flock doesn't work (e.g., NFS). Since our primary target is macOS local dev, this is low risk.

Sources:
- [S1] flock(2): "locks are on the open file description, released when all FDs closed or process exits"
- [S2] https://unix.stackexchange.com/questions/594027 — flock stale lock handling
- [S3] https://wmdev.medium.com/ — abstract socket namespace pattern (Linux-only, not applicable to macOS)

### F2: Abstract socket namespace is Linux-only; macOS needs flock + socket (source: domain socket article)

The "use `@` prefix for automatic cleanup" pattern only works on Linux (abstract socket namespace). macOS does not support abstract sockets. On macOS, the standard approach is:

1. **flock for mutual exclusion** (kernel auto-releases on process exit, even SIGKILL)
2. **Filesystem socket** cleaned up on startup by the lock holder

Our current flock approach is the correct pattern for macOS. The socket file at `~/.config/loom/loom.sock` is a filesystem socket, which persists after crashes. The lock prevents races on cleanup.

Sources:
- [S4] Wayne Marsh article: abstract sockets auto-cleanup on Linux but not macOS
- [S5] Rust IPC article: "on the next start, bind() removes the stale entry before rebinding"

### F3: LaunchAgent should be the primary supervisor; never `fork`/`exec` a daemon yourself (source: Apple developer docs)

Apple's documentation is explicit:

> "You must not daemonize your process. This includes calling `fork` followed by `exec`, or `fork` followed by `exit`. If you do, `launchd` thinks your process has died."

**Current issue:** The proxy's fallback path (`proxy.go:236-280`) spawns `loomd` directly via `exec.Command` + `cmd.Start()` + `cmd.Process.Release()`. This creates an orphan process not managed by launchd. If the proxy exits, the daemon keeps running but launchd doesn't know about it.

**Best practice:**
1. Always prefer `launchctl kickstart` (current stage 1, correct)
2. If no LaunchAgent plist exists, **install one** rather than spawning directly
3. Only use direct spawn as an absolute last resort (dev builds without plist)
4. The proxy should never `Release()` a child process — if it starts the daemon, it should let launchd manage it

**Current mitigation:** The flock singleton prevents duplicate daemons, so the orphan problem is limited to "unmanaged but singular." Acceptable for dev, but should be documented.

Sources:
- [S6] Apple Developer: "Creating Launch Daemons and Agents" — do not daemonize
- [S7] https://ieftimov.com/posts/create-manage-macos-launchd-agents-golang/ — Go + LaunchAgent patterns
- [S8] launchd vs systemd comparison: launchd relies on the daemon exiting gracefully; it will try to restart

### F4: Graceful shutdown should use signal → context cancel → waitgroup drain (source: Go shutdown guides)

The recommended Go graceful shutdown pattern:

```
1. signal.NotifyContext(ctx, SIGINT, SIGTERM)
2. On signal: cancel context
3. Server.Shutdown(ctx) — stop accepting new connections
4. WaitGroup.Wait() — drain in-flight requests
5. Close resources (child processes, file handles, lock file)
6. Exit
```

**Current implementation** (`daemon.go:640-676`) does:
1. Emit `process.stop` events ✅
2. `procMgr.StopAll()` — stops child processes ⚠️ (no timeout/wait)
3. Stop file watcher ✅
4. Save manifest ✅
5. `wg.Wait()` ✅
6. Close lock file ✅

**Gap:** `procMgr.StopAll()` should send SIGTERM, wait with timeout, then SIGKILL. The MCP spec recommends: "close input stream → wait for exit → SIGTERM → SIGKILL." Current implementation may not wait for child processes to finish in-flight work.

Sources:
- [S9] MCP Lifecycle article: "close input → wait → SIGTERM → SIGKILL"
- [S10] RudderStack graceful shutdown: context + errgroup + timeout
- [S11] Go dev.to article: signal.Notify + Shutdown(ctx)

### F5: Flock child process leak via `--close` (source: Stack Overflow)

**Critical pattern:** When a process that holds a flock spawns child processes, those children **inherit the lock FD** and keep the lock held even after the parent exits.

The `flock --close` flag exists for shell scripts to handle this. In Go, we should set `syscall.CloseOnExec` on the lock FD, or use `FD_CLOEXEC`, to prevent child MCP server processes from inheriting the lock.

**Current risk:** `loomd` spawns MCP servers as child processes. If those children inherit the lock FD, and `loomd` crashes, the lock remains held by the orphaned child process. The next `loomd` start will fail with "lock held."

**Fix:** After `acquireLock()`, set `FD_CLOEXEC` on the lock file descriptor:
```go
syscall.CloseOnExec(int(f.Fd()))
```

Or open with `O_CLOEXEC` (Go's `os.OpenFile` already sets this on most platforms, but verify).

Sources:
- [S12] Stack Overflow flock answer: "flock launched a child process that remained... flock --close fixed it"
- [S13] flock(2): "lock is released when all file descriptors are closed"

### F6: Testing daemon lifecycle requires temp directories and parallel-safe helpers (source: Go testing patterns)

Recommended patterns for testing flock + socket:

1. **Use `t.TempDir()`** for socket paths and lock files — auto-cleaned per test
2. **Interface-based mocking** for `net.Conn` (factory pattern, inject dial function)
3. **Table-driven tests** with mock structs for `WaitGroup`, `Mutex`, `Signal`
4. **For flock tests:** spawn a subprocess with `exec.Command(os.Args[0], "-test.run=TestHelper")` to test lock contention across processes
5. **For socket tests:** create real Unix sockets in temp dirs; they're fast and local

Key test scenarios to cover:
- Two daemons competing for the same lock → second fails immediately
- Stale socket + no lock → socket cleaned up, new daemon starts
- Lock held by crashed child → verify `CloseOnExec` prevents this
- Graceful shutdown → all children terminated within timeout
- Proxy autostart → daemon accessible within 2s

Sources:
- [S14] Go mocking article: interface + factory pattern for `net.Conn`
- [S15] Go testing patterns: `t.TempDir()`, table-driven, subprocess helpers

### F7: Socket activation is a non-goal on macOS but could inform future K8s deployment (source: systemd docs)

Systemd's socket activation pattern (daemon receives pre-opened FD from init system) is elegant but macOS launchd supports it differently (`Sockets` key in plist with `launch_data_get_fd`). Since our primary target is local macOS dev with a single daemon, full socket activation is over-engineered. However:

- For K8s deployment, the daemon listens on TCP (already supported via `/health` endpoint)
- Socket activation could reduce cold-start latency if we ever want launchd to hold the socket open during daemon restarts

Not recommended for now. The flock + socket + LaunchAgent pattern is sufficient.

Sources:
- [S16] Vincent Bernat: systemd socket activation in Go
- [S17] Apple Developer: launchd socket handoff via `Sockets` plist key

## Options

### Option A: Minimal hardening (close the known gaps)

Fix the three concrete issues without rearchitecting:
1. Add `CloseOnExec` to lock FD (F5)
2. Remove redundant dial-test, trust flock as sole gate (F1)
3. Add timeout to `procMgr.StopAll()` (F4)
4. Add test coverage for critical paths (F6)

- Pros: Small diff, directly addresses known risks.
- Cons: Doesn't address CLI vs proxy autostart inconsistency.

### Option B: Full lifecycle alignment (recommended)

All of Option A, plus:
5. Unify CLI and proxy daemon start logic into shared `ensureDaemon()` function
6. Add `loom daemon status` subcommand exposing lock holder PID and socket state
7. Document LaunchAgent as the canonical supervision method; direct spawn marked as dev-only fallback

- Pros: Consistent behavior across all entry points, better observability.
- Cons: Slightly larger scope; `daemon status` is a new subcommand.

### Option C: Socket activation via launchd

Implement launchd `Sockets` key handoff so the daemon inherits the socket from launchd and restarts are zero-downtime.

- Pros: Zero-downtime restarts, launchd owns the socket lifecycle.
- Cons: Significant complexity, macOS-only, requires plist changes, overkill for dev tool.

## Recommendation

**Option B: Full lifecycle alignment.** The fixes in Option A are critical (especially `CloseOnExec`). The additions in Option B provide observability and consistency at low cost. Option C is deferred — unnecessary for a local dev tool.

## Priority Order

1. **CloseOnExec on lock FD** — prevents lock leak to child processes (correctness bug)
2. **Simplify stale socket handling** — trust flock, remove dial-test race window
3. **Graceful child shutdown** — SIGTERM → timeout → SIGKILL sequence
4. **Test coverage** — flock contention, socket lifecycle, proxy autostart
5. **Unified `ensureDaemon()`** — merge CLI and proxy autostart logic
6. **`loom daemon status`** — expose lock holder and socket state

## Sources

- [S1] flock(2) man page — lock semantics, FD inheritance
- [S2] https://unix.stackexchange.com/questions/594027 — stale flock handling
- [S3] https://wmdev.medium.com/ — abstract socket namespace (Linux-only)
- [S4] Wayne Marsh: domain socket singleton enforcement
- [S5] Rust IPC article: stale socket cleanup on rebind
- [S6] Apple Developer: "Creating Launch Daemons and Agents"
- [S7] https://ieftimov.com/ — Go + LaunchAgent patterns
- [S8] FreeBSD Forums: launchd vs systemd comparison
- [S9] MCP Lifecycle (Medium): shutdown sequence
- [S10] RudderStack: graceful shutdown in Go
- [S11] dev.to: Unix domain sockets in Go
- [S12] Stack Overflow: flock child process leak via `--close`
- [S13] flock(2): lock released when all FDs closed
- [S14] Medium: Go unit testing mock patterns
- [S15] Twilio: 4 mocking approaches for Go
- [S16] Vincent Bernat: systemd socket activation in Go
- [S17] Apple Developer: launchd Sockets plist key
