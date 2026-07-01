# Gaming Mode Runbook (Sunshine / Moonlight)

Operate a GPU node as an on-demand game-streaming host. A `GamingSession` CR
drains inference from the node and starts a headless Sunshine host that a
Moonlight client pairs against; deleting the CR returns the node to the
inference fleet.

- **Validated on:** `cblevins-7900xtx` (AMD RX 7900 XTX, gfx1100/RDNA3), 2026-07-01.
- **Stack:** Sunshine + headless `sway` (wlroots) + Mesa RADV (Vulkan render) +
  VA-API HW encode (H.264/HEVC/AV1 via `radeonsi`). See
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

## Pair Moonlight

- **Host:** the node IP (e.g. `192.168.50.125`); auto-discovers via mDNS.
- **Ports (hostNetwork):** TCP `47984` (HTTPS), `47989` (HTTP), `47990` (web UI),
  `48010` (RTSP); UDP `47998/47999/48000/48002`.
- **First run:** open `https://<node-ip>:47990`, set a username/password.
- **Pair:** in Moonlight add `<node-ip>`; enter the PIN it shows into the web UI.
- **Play:** pick an app (Desktop, or a launched game). An empty headless desktop
  streams as a static gray screen — that is expected; launch something to render.

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

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| gemma/model won't leave the node; another model stays `Active` leader | A shared-GPU member has `gpu.forcePromotion: true` — it wins the election unconditionally. Remove it. Also clear `warmPolicy: primary` + set `minReplicas: 0` when de-advertising. |
| Node won't warm the new primary after freeing the card | Election prefers the higher-priority incumbent; raise the intended primary's `gpu.priority` above it (it must also be `litellm.enabled: true` + `minReplicas ≥ 1`). |
| Controller crashlooping `gamingsessions ... is forbidden` | Chart ClusterRole missing `gamingsessions` — add to `charts/flexinfer/templates/rbac.yaml` (the `ai.flexinfer` rule). A new CRD needs both `config/rbac/role.yaml` and the chart ClusterRole. |
| Moonlight can't reach the node | The gaming runtime profile needs `hostNetwork: true` + `gaming: true`; NodePort's 30000–32767 range cannot serve Moonlight's fixed ports. Ensure no other process holds 47984/47989/47990/48010 on the host. |
| Sunshine build fails `Steam License Agreement was DECLINED` | `steamcmd` needs a debconf license preseed; the gaming image omits it (Sunshine streams any app without Steam). Add steamcmd + preseed only when wiring Steam/Proton. |
| A dedicated-Deployment model (pvc:// source) still runs on the gaming node | `SetMode(gaming)` drains runtime-managed models, not dedicated Deployments. De-advertise it (`litellm.enabled: false`, `minReplicas: 0`) so it idles out. |

## Related

- Plan / evidence: `.loom/30-implementation-plan-gaming-mode-sunshine-2026-06-30.md`,
  `.loom/killtest-gaming-sunshine-gfx1100-2026-06-30.md`.
- Code: `api/v1alpha2/gamingsession_types.go`, `controllers/gamingsession_controller.go`,
  `controllers/runtime_controller.go` (`SetMode`), `internal/runtime/manager.go`.
