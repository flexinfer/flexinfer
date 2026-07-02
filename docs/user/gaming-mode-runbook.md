# Gaming Mode Runbook (Sunshine / Moonlight)

Operate a GPU node as an on-demand game-streaming host. A `GamingSession` CR
drains inference from the node and starts a headless Sunshine host that a
Moonlight client pairs against; deleting the CR returns the node to the
inference fleet.

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

## Enable gaming on a node

Everything is GitOps-managed — commit to `services/flexinfer` and let Flux
reconcile (`flexinfer-models` for the CR/models, the HelmRelease for the runtime
profile). Do **not** `kubectl apply`/`edit` directly.

1. Move any warm primary off the node first (avoid a chat gap). Example: promote
   the sister instance on another node and de-advertise the node's lane
   (`litellm.enabled: false`, `serverless.minReplicas: 0`, clear `serviceLabels`,
   `warmPolicy: ondemand`, and **remove `gpu.forcePromotion`** — see Troubleshooting).
2. Add the `GamingSession` (see `deploy/gaming/gamingsession-7900xtx.yaml`):
   ```yaml
   apiVersion: ai.flexinfer/v1alpha2
   kind: GamingSession
   metadata: { name: gaming-<node>, namespace: flexinfer-system }
   spec: { nodeName: <node>, mode: gaming }
   ```
3. After merge, force reconcile if impatient:
   ```bash
   export KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml
   flux reconcile source git flexinfer -n flux-system
   flux reconcile helmrelease flexinfer -n flexinfer-system   # rolls the runtime to the gaming image
   flux reconcile kustomization flexinfer-models -n flux-system
   ```
4. Verify:
   ```bash
   kubectl get gamingsession -n flexinfer-system
   # NODE  MODE    PHASE
   # ...   gaming  Active
   ```
   `phase=Active`, `observedMode=gaming` means Sunshine is up. Check the runtime
   pod log for `Found H.264 encoder: h264_vaapi` / `Configuration UI available`.
   `phase=Degraded` means the node is in gaming mode but the Sunshine subprocess
   crashed; the runtime restarts it with exponential backoff (2s → 60s) and the
   phase returns to Active once a restart succeeds. A session stuck in Degraded
   means every restart is failing — check the runtime pod log for the crash
   (`Backend subprocess crashed`) and the restart attempts.

## Pair Moonlight

- **Host:** the node IP (e.g. `192.168.50.125`); auto-discovers via mDNS.
- **Ports (hostNetwork):** TCP `47984` (HTTPS), `47989` (HTTP), `47990` (web UI),
  `48010` (RTSP); UDP `47998/47999/48000/48002`.
- **First run:** open `https://<node-ip>:47990`, set a username/password.
- **Pair:** in Moonlight add `<node-ip>`; enter the PIN it shows into the web UI.
- **Play:** pick an app — `Desktop` (Steam autostarts in the session) or
  `Steam Big Picture`. An empty headless desktop streams as a static gray
  screen — that is expected; launch something to render.

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
kubectl delete gamingsession gaming-<node> -n flexinfer-system   # via GitOps: remove deploy/gaming/*
```
The reconciler's finalizer issues `SetMode(inference)`. To fully restore the
inference fleet also revert the runtime image → serving digest and re-advertise
the node's model lane (undo step 1).

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
| GamingSession stuck `Degraded`; no sunshine process in the pod | Sunshine crashes on every supervised restart (e.g. the 2026-07-01 `useradd` exit-4 wrapper crash). The runtime retries with backoff forever while in gaming mode — fix the crash cause; check `flexinfer_runtime_gaming_backend_restarts_total{result="error"}` and the pod log. |
| Image build fails `Steam License Agreement was DECLINED` | The `steam`/`steamcmd` debconf license preseeds must run **before** `apt-get install` in the `INCLUDE_GAMING` layer (both selectors: `steam steam/question` and `steamcmd steam/question`). |
| Steam exits immediately / `Cannot run as root` | Steam refuses uid 0. The session (sway/PipeWire/Steam) runs as the `gamer` user; only Sunshine + avahi stay root. Don't launch `steam` from a root shell — use the `steam-app.sh` wrapper on the gaming volume. |
| Steam window never appears on the stream | Xwayland missing (Steam is X11): the sway config needs `xwayland enable` and the image needs the `xwayland` package (sway's Recommends are suppressed by `--no-install-recommends`). |
| Games/login lost after a pod restart | The gaming state volume isn't mounted — check the profile's `extraVolumes` (`/home/flexinfer-gaming` → `/var/lib/flexinfer-gaming`) and `GAMING_STATE_DIR`. |
| A dedicated-Deployment model (pvc:// source) still runs on the gaming node | `SetMode(gaming)` drains runtime-managed models, not dedicated Deployments. De-advertise it (`litellm.enabled: false`, `minReplicas: 0`) so it idles out. |

## Related

- Plan / evidence: `.loom/30-implementation-plan-gaming-mode-sunshine-2026-06-30.md`,
  `.loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md`.
- Code: `api/v1alpha2/gamingsession_types.go`, `controllers/gamingsession_controller.go`,
  `controllers/runtime_controller.go` (`SetMode`), `internal/runtime/manager.go`.
