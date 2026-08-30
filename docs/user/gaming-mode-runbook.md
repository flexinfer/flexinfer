# Gaming Mode Runbook (Sunshine / Moonlight)

Operate a GPU node as an on-demand game-streaming host. A `GamingSession` CR
drains inference from the node and starts a headless Sunshine host that a
Moonlight client pairs against; deleting the CR (or its TTL/idle expiry)
returns the node to the inference fleet.

The node's gaming **capability** (image, mounts, hostNetwork) and **policy**
(approved images, TTLs) are GitOps-owned; individual play **sessions** are
ephemeral API objects driven by the CLI:

```bash
flexinfer game preflight cblevins-7900xtx          # read-only checks
flexinfer game start cblevins-7900xtx --for 2h --wait
flexinfer game status cblevins-7900xtx
flexinfer game stop cblevins-7900xtx
```

Activation is refused (`CapabilityReady=False/ImageNotApproved`) until the
runtime image is on the `gaming.approvedImages` policy list — see
`docs/dev/gaming-image-canary.md` for the security + exact-hardware canary
that gates that list. While a session holds the node, the controller's
scheduling overlay parks every hostname-pinned inference workload (dedicated
Deployments and explicit `minReplicas` floors included) and restores them
automatically when the session ends — per-session edits to model manifests
are obsolete. Client-side setup for the Steam Deck (and the Xbox path) lives
in `docs/user/steamdeck-xbox-clients.md`.

- **Validated on:** `cblevins-7900xtx` (AMD RX 7900 XTX, gfx1100/RDNA3), 2026-07-01.
- **Stack:** Sunshine + headless `sway` (wlroots, Xwayland) + Mesa RADV (Vulkan
  render) + VA-API HW encode (H.264/HEVC/AV1 via `radeonsi`) + Steam client
  (running as the non-root `gamer` session user). See
  `backend/sunshine.go`, `build/sunshine-headless.sh`, `build/Dockerfile.runtime`
  (`INCLUDE_GAMING`), and the kill-test evidence in
  `.loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md`.

## Architecture

```
GamingSession CR ──> GamingSessionReconciler ──> RuntimeReconciler.SetMode
   (declarative)        (drains inference)          PUT /api/v1/mode {gaming}
                                                          │
                                                          ▼
                                     runtime pod (gaming image) runs
                                     sunshine-headless.sh: sway + Sunshine
                                     + RADV render + VA-API HW encode
```

The gaming node's runtime DaemonSet uses the dedicated gaming image
(`runtime:rocm-gfx1100-gaming`) and runs with `hostNetwork` so a LAN Moonlight
client reaches Sunshine on its fixed ports (see below).

## Prerequisites (per gaming node)

1. **Gaming runtime image** built + pushed (off-CI on the GPU builder):
   ```bash
   ./build/build-runtime.sh gfx1100-gaming --push
   ```
   Expected: `Done: gfx1100-gaming → registry.harbor.lan/flexinfer/runtime:rocm-gfx1100-gaming`.
2. **Runtime profile** for the node pinned to that image with `hostNetwork` +
   `gaming` in `deploy/system/values-k3s.yaml` (`runtime.profiles[].{image,hostNetwork,gaming}`).
3. **Controller** on Slice 3+ (has the `gamingsessions` controller and the
   chart ClusterRole grants `gamingsessions`).
4. **Gaming state volume** on the profile (`extraVolumes`/`extraVolumeMounts`):
   a hostPath (on `cblevins-7900xtx`: `/home/flexinfer-gaming`, on the 1.8T
   NVMe) mounted at `/var/lib/flexinfer-gaming` (`GAMING_STATE_DIR`). Layout,
   managed by `sunshine-headless.sh`:
   - `sunshine/` — `sunshine.conf`, pairing state, `apps.json`: Moonlight
     clients stay paired across pod restarts.
   - `home/` — the `gamer` user's `$HOME`: Steam client bootstrap, account
     login, and the game library (games install to
     `home/.local/share/Steam/steamapps` by default).
5. **Gaming policy approval**: the runtime image digest must be listed in
   `gaming.approvedImages` (`deploy/system/values-k3s.yaml` → the
   `flexinfer-gaming-policy` ConfigMap). The list is empty until a candidate
   passes the canary in `docs/dev/gaming-image-canary.md`; until then every
   session is refused before touching inference.

## Start a session (one command)

```bash
flexinfer game preflight <node>                 # policy/image/runtime/drain/label checks, read-only
flexinfer game start <node> --for 2h --wait     # creates GamingSession gaming-<node>
```

`start` creates the session directly against the API (sessions are ephemeral
and TTL-bound by design — they do not belong in Git; only the node capability
and the gaming policy do). The controller then walks the session through its
conditions, all visible via `flexinfer game status <node>`:

1. `CapabilityReady` — runtime pod reachable, image on the approved list,
   owner present. A refusal here parks **nothing**: inference continues
   untouched until the session is vetted.
2. `DrainComplete` — the scheduling overlay parks the node's hostname-pinned
   models (election + `desiredReplicas` go to zero; no manifest edits) and the
   controller waits until no model pods remain on the node
   (`phase=Draining`). If pods are still there after the drain timeout the
   reason flips to `DrainBlocked` with the blocker pod names — the session
   never force-evicts silently.
3. `SunshineReady` — only after the drain does the runtime switch to gaming
   mode (unloading its in-process models) and start Sunshine.
   `phase=Degraded` means Sunshine crashed and the runtime is supervising
   restarts with backoff.
4. `StreamActive` — best-effort live-client telemetry (`Unknown` on runtime
   images that predate it).

Manual fallback (GitOps-authored session or `kubectl` create) still works —
the same gates apply; see `config/samples/ai_v1alpha2_gamingsession.yaml`.

## Lease expiry (mandatory TTL, idle, policy)

Every session has an effective deadline — the earliest of `spec.expiresAt`,
`creation + spec.ttl`, and `creation + maxTTL` from the gaming policy, with
the policy's `defaultTTL` (2h) applied when the spec sets nothing. The
controller stamps it in `status.expiresAt`. At the deadline it reverts the
node to inference and reports `phase=Expired` (`status.endReason: Expired`).
A deadline expiry can be undone by extending `spec.expiresAt`/`spec.ttl`.

Two further conclusions are **terminal** (delete the session and `start`
again to game more):

- `IdleTimeout` — `spec.idleTimeout` (or `--idle-timeout`) ended the session
  after the runtime reported no connected Moonlight client for that long.
  Requires runtime client telemetry; without it only the deadline and the
  runtime's own `GAMING_IDLE_TIMEOUT` guard apply.
- `RuntimeReverted` — the node left gaming mode outside the controller (the
  runtime idle guard fired, or an operator flipped the mode). The controller
  concludes the session instead of fighting the revert.

The policy (`flexinfer-gaming-policy` ConfigMap, rendered from
`gaming:` in `deploy/system/values-k3s.yaml`) also carries `approvedImages`,
`drainTimeout`, and `requireOwner`.

## Pair Moonlight

- **Host:** the node IP (e.g. `192.168.50.125`); auto-discovers via mDNS on the
  same LAN. From a different subnet/VLAN, mDNS won't cross — add the node by IP.
- **Ports (hostNetwork):** TCP `47984` (HTTPS), `47989` (HTTP), `47990` (web UI),
  `48010` (RTSP); UDP `47998/47999/48000/48002`. This set covers a single
  high-resolution stream (e.g. 3440×1440@120) — bitrate rides the existing video
  port, so no extra ports are needed for the ultrawide.
- **Resolution/refresh:** the host output matches the client automatically —
  see "Resolution & refresh rate" below (for a Mac/LG34 → Custom 3440×1440@120).
- **First run:** open `https://<node-ip>:47990`, set a username/password.
- **Pair:** in Moonlight add `<node-ip>`; enter the PIN it shows into the web UI.
- **Play:** pick an app — `Desktop` (Steam autostarts in the session) or
  `Steam Big Picture`. An empty headless desktop streams as a static gray
  screen — that is expected; launch something to render.

## Resolution & refresh rate (per-client / ultrawide)

The headless `sway` output has no physical panel, so it presents whatever mode
we tell it. Two mechanisms set that mode:

1. **Base mode** — `GAMING_RESOLUTION` × `GAMING_FPS` on the gaming runtime
   profile env (`deploy/system/values-k3s.yaml`). This is the pre-connection /
   desktop mode. On `cblevins-7900xtx` it is `3440x1440` @ `120` — native for
   the operator's **LG 34" 21:9 ultrawide (3440×1440)** driven from a Mac.
2. **Per-client override** — `sunshine-headless.sh` writes a Sunshine
   `global_prep_cmd` (the `sunshine-resize.sh` helper on the gaming volume) that
   resizes the `sway` output to the **connecting client's** requested
   width/height/fps on stream start, then reverts to the base mode on stream
   end. So a 3440×1440 Mac, a 1920×1080 TV, and a 1280×800 Steam Deck each get a
   pixel-matched stream with no per-client server config. Set
   `GAMING_DYNAMIC_RESOLUTION=false` to pin the base mode for every client.

   The helper is best-effort: an invalid or out-of-range client mode (caps at
   3840×2160) leaves the client on the base mode rather than aborting the stream.
   Verify inside the runtime pod after a client connects:
   ```bash
   swaymsg -t get_outputs | grep -A2 HEADLESS-1   # current_mode should match the client
   ```

### Streaming to a Mac on the LG 34" ultrawide (3440×1440)

The Mac and the gaming node are on the **same LAN** (`192.168.50.0/24`), so no
router/port-forward changes are needed — the host ports below are already bound
on the node and Moonlight auto-discovers via mDNS (or add `192.168.50.125` by
IP). In **Moonlight (macOS)** → Settings:

- **Resolution:** add **Custom → 3440×1440** (Moonlight ships common 16:9 modes
  only; add 3440×1440 as a custom resolution so the aspect ratio matches the
  LG 34 and the per-client hook sets the host output to 21:9).
- **Frame rate:** `120` FPS (the base mode is 120; the Mac's LG34 must be at a
  ≥120 Hz refresh for tear-free playback).
- **Video bitrate:** ~100–150 Mbps for 3440×1440@120 over wired GbE (Moonlight's
  auto value for this mode is a good start; lower it if you see network drops).
- **Codec:** HEVC or AV1 (both HW-encoded on the 7900 XTX; they hold realtime at
  this pixel rate with more headroom than H.264 — see the kill-test).
- **Fullscreen**, and (Mac) disable "Optimize game settings" if a game keeps
  overriding the streamed resolution.

The 21:9 VA-API encode path (3440×1440@120, H.264/HEVC/AV1) is exercised by
`deploy/debug/gaming-sunshine-killtest.yaml` alongside the 1080p60 baseline.

## Steam (one-time account login)

Steam starts automatically inside the sway session (set
`GAMING_STEAM_AUTOSTART=false` on the profile env to disable, or
`GAMING_LAUNCH_CMD` to autostart something else). Because credentials and
Steam Guard are interactive, the first sign-in must be done by a human:

1. Connect with Moonlight and pick `Desktop`.
2. In the Steam login window, sign in (approve the Steam Guard prompt on your
   phone).
3. Done — the login token, client, and all installed games persist on the
   gaming volume (`/home/flexinfer-gaming/home` on the node's NVMe), so pod
   restarts and image rolls do not sign you out or lose the library.

Install games from the Steam UI over the stream. For Windows titles enable
Proton: Steam → Settings → Compatibility → "Enable Steam Play for all other
titles". `steamcmd` is also in the image for scripted/depot installs.

## Revert to inference

```bash
flexinfer game stop <node> --wait     # or: kubectl delete gamingsession gaming-<node> -n flexinfer-system
```
The reconciler's finalizer issues `SetMode(inference)`, and the scheduling
overlay lifts the moment deletion starts — parked models re-elect, scale back
up, and re-advertise on their own. No manifest reverts are needed for the
session itself; the only GitOps-owned pieces are the node's capability stanza
and the policy.

## Idle auto-revert (opt-in, default OFF)

By default a gaming session persists until an explicit revert. To auto-revert a
node to inference after N with **no connected Moonlight client**, set on the
gaming runtime profile's env:
```
GAMING_IDLE_TIMEOUT=45m
```
The runtime probes `/proc/net/tcp*` for established connections on the Moonlight
control/RTSP ports; if none for the timeout it calls `SetMode(inference)`. A
failed probe assumes "active" (never auto-reverts a live session).

When this guard fires under an active `GamingSession`, the controller notices
the mode flip and concludes the session (`endReason: RuntimeReverted`) rather
than re-asserting gaming. The controller-side equivalent is
`spec.idleTimeout` (`flexinfer game start --idle-timeout 45m`), which uses
the runtime's client telemetry and needs no env change or pod roll.

## Observability

Scraped by the existing `flexinfer-runtime` PodMonitor (`/metrics` on the api port):

| Metric | Meaning |
|---|---|
| `flexinfer_runtime_node_mode{mode="gaming"}` | 1 when the node is in gaming mode |
| `flexinfer_runtime_node_mode{mode="inference"}` | 1 when serving inference |
| `flexinfer_runtime_gaming_idle_reverts_total` | count of idle auto-reverts |
| `flexinfer_runtime_gaming_backend_restarts_total{result}` | supervised Sunshine restarts after a crash (`ok`/`error`) |

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| gemma/model won't leave the node; another model stays `Active` leader | A shared-GPU member has `gpu.forcePromotion: true` — it wins the election unconditionally. Remove it. Also clear `warmPolicy: primary` + set `minReplicas: 0` when de-advertising. |
| Node won't warm the new primary after freeing the card | Election prefers the higher-priority incumbent; raise the intended primary's `gpu.priority` above it (it must also be `litellm.enabled: true` + `minReplicas ≥ 1`). |
| Controller crashlooping `gamingsessions ... is forbidden` | Chart ClusterRole missing `gamingsessions` — add to `charts/flexinfer/templates/rbac.yaml` (the `ai.flexinfer` rule). A new CRD needs both `config/rbac/role.yaml` and the chart ClusterRole. |
| Moonlight can't reach the node | The gaming runtime profile needs `hostNetwork: true` + `gaming: true`; NodePort's 30000–32767 range cannot serve Moonlight's fixed ports. Ensure no other process holds 47984/47989/47990/48010 on the host. |
| Stream is letterboxed / pillarboxed / blurry on the ultrawide | The host output did not match the client. Confirm `swaymsg -t get_outputs` shows `HEADLESS-1` at the client's mode after connect; if it is stuck on the base mode, check the runtime pod log for `[sunshine-resize]` warnings (missing sway socket, out-of-range mode), that `GAMING_DYNAMIC_RESOLUTION` is not `false`, and that Moonlight is set to a real 3440×1440 (not "native/auto" scaling a 16:9 mode). |
| Moonlight video connects but controller/keyboard/mouse input does nothing | Sway must run with `WLR_BACKENDS=headless,libinput`, must not set `WLR_LIBINPUT_NO_DEVICES`, `gamer` must be in the host gids that own `/dev/input/event*`, and the gaming profile must mount `/dev/input`, `/dev/uinput`, and `/run/udev`. Verify with `swaymsg -t get_inputs` inside the runtime pod; it should include Sunshine passthrough devices after a client connects. |
| GamingSession stuck `Degraded`; no sunshine process in the pod | Sunshine crashes on every supervised restart (e.g. the 2026-07-01 `useradd` exit-4 wrapper crash). The runtime retries with backoff forever while in gaming mode — fix the crash cause; check `flexinfer_runtime_gaming_backend_restarts_total{result="error"}` and the pod log. |
| Image build fails `Steam License Agreement was DECLINED` | The `steam`/`steamcmd` debconf license preseeds must run **before** `apt-get install` in the `INCLUDE_GAMING` layer (both selectors: `steam steam/question` and `steamcmd steam/question`). |
| Steam exits immediately / `Cannot run as root` | Steam refuses uid 0. The session (sway/PipeWire/Steam) runs as the `gamer` user; only Sunshine + avahi stay root. Don't launch `steam` from a root shell — use the `steam-app.sh` wrapper on the gaming volume. |
| Steam window never appears on the stream | Xwayland missing (Steam is X11): the sway config needs `xwayland enable` and the image needs the `xwayland` package (sway's Recommends are suppressed by `--no-install-recommends`). |
| Launching a game from Big Picture keeps streaming Big Picture; game audio may play but the game never shows | Big Picture runs fullscreen, and sway keeps the existing fullscreen container on top when the Proton/Xwayland game window (`class steam_app_<appid>`, `visible: false` in `swaymsg -t get_tree`) appears. Fixed by the generated sway config rule `for_window [class="^steam_app_"] focus, fullscreen enable` (images ≥ 2026-08-19). Live rescue on an older image, inside the runtime pod (as root, `-s` on the session's `$XDG_RUNTIME_DIR/sway-ipc.*.sock`): `swaymsg '[class="^steam$"] fullscreen disable'`, then `swaymsg '[class="^steam_app_"] focus'` + `swaymsg '[class="^steam_app_"] fullscreen enable'`, and add the rule at runtime with `swaymsg 'for_window [class="^steam_app_"] fullscreen enable'` so subsequent launches work. |
| Steam process alive but nothing downloads / no window for many minutes | Debian's launcher gates the first-run bootstrap on a zenity Install/Cancel dialog (`--default-cancel`) that nobody can click headlessly. `sunshine-headless.sh` shims that one dialog to "Install" (`$XDG_RUNTIME_DIR/shim/zenity`); if it regresses, check the shim is on the session PATH. |
| Games/login lost after a pod restart | The gaming state volume isn't mounted — check the profile's `extraVolumes` (`/home/flexinfer-gaming` → `/var/lib/flexinfer-gaming`) and `GAMING_STATE_DIR`. |
| Session stuck `Pending` with `CapabilityReady=False/ImageNotApproved` | Working as designed: the runtime image is not on `gaming.approvedImages` (the list ships empty until a Sunshine build passes the security + streaming canary — `docs/dev/gaming-image-canary.md`). No inference is disturbed while refused. |
| Session stuck `Draining` / `DrainComplete=False,reason=DrainBlocked` | Pods named in the condition message are still on the node past the drain timeout. Usually a model **not** hostname-pinned (arch-only selector) that the overlay deliberately does not park, or a pod stuck Terminating. Investigate the pod; the session never force-evicts. |
| A dedicated-Deployment model (pvc:// source) still runs on the gaming node | On current controllers the scheduling overlay parks hostname-pinned dedicated Deployments automatically once the session is vetted (`CapabilityReady=True`). If it persists, check the model actually pins `kubernetes.io/hostname` (see the DrainBlocked row) — legacy fallback: de-advertise it (`litellm.enabled: false`, `minReplicas: 0`) so it idles out. |

## Related

- Security/streaming canary gate: `docs/dev/gaming-image-canary.md`.
- Steam Deck + Xbox client setup: `docs/user/steamdeck-xbox-clients.md`.
- Plan / evidence: `.loom/implementation-plan-gaming-lease-overlay-2026-08-30.md`,
  `.loom/30-implementation-plan-gaming-mode-sunshine-2026-06-30.md`,
  `.loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md`.
- Code: `api/v1alpha2/gamingsession_types.go`, `controllers/gamingsession_controller.go`,
  `controllers/gaming_overlay.go` (scheduling overlay), `pkg/gaming/policy.go`,
  `cmd/flexinfer/commands/game.go`, `controllers/runtime_controller.go`
  (`SetMode`), `internal/runtime/manager.go`.
