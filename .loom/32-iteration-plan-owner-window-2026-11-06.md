# Owner window from 2026-11-06 00:15Z: one ordered runbook for the held Drafts

Status: **prepared 2026-11-05, nothing merged.** The owner gives the go per step. Every Draft
below was refreshed on 2026-11-05 by merging `master` (`51e6bd81`) into it, with no force-push.
Each one merges cleanly with master and with every other Draft (`git merge-tree`, pairwise).
`docs/certification-matrix.json` is current on every branch.

The order keeps the blast radius small. One control-plane component or one card changes at a
time, and each change soaks before the next. The steps fall into four phases: control plane,
the 5930k CPU twin, the Radeon VII, then the 7900 XTX. The XTX goes last because it carries the
most moving parts and the canary queue may still hold it at 00:15Z.

## 0. Summary

| # | MR | Component | Change | Needs | Time incl. soak |
|---|---|---|---|---|---|
| A1 | !1289 | controller | roll to master `51e6bd81` (`6a3ad9e6`): `vllmVersion` render (!1281), render hygiene (!1291), refused-fit unload (!1256) | no Model declares `vllmVersion` | 40 min |
| A2 | !1255 | proxy | InferenceJob API on, no image move | A1 soaked; no KT harness running through the proxy | 45 min |
| — | !1221 | — | **do not merge**: superseded (stale pins, and its jobs half is in !1255) | close it | — |
| B1 | !1257 | 5930k CPU twin | gte twin 12 threads, 14-core limit | A1 | 15 min |
| B2 | — | VII gte lane (test) | KT-RET K2 re-run, forced 5 min park | B1 at ≥ 140 tok/s; A1 (for !1256) | 15 min |
| C1 | !1261 | VII runtime | `6fc6fcbd` → `cb4c8dc3` (pyannote layer), HF token mount | B1 | 45 min |
| C2 | !1262 | VII lane + test | pyannote lane + provisional certificate, then KT-DIAR | A2, C1 | 90 min |
| C3 | !1287 | VII runtime | `cb4c8dc3` → `34e60fd3` (llama.cpp b11847) | **owner accepts S8**; C2 verdict | 45 min |
| D0 | — | XTX warm lane (test) | warm-lane Deployment baseline (D1 "order 0") | XTX free | 15 min |
| D1 | — | XTX (test) | KT-JOBS on the current layout | A2, XTX free | 50 min |
| D2 | !1235 | XTX warm lane | warm lane in-pod on `527e2714` | D0 | 60 min |
| D3 | !1236 | XTX Qwen-Image | Qwen-Image in-pod; NF4 check | D2 | 30 min |
| D4 | — | XTX (test) | KT-SWAP | D3, GPULease | 90 min |
| D5 | !1237 | XTX Wan T2V | Wan T2V in-pod; decode A/B | D4 | 45 min |
| D6 | !1238 | XTX Wan VACE | VACE in-pod; decode A/B | D5 | 45 min |
| D7 | !1239 | XTX Stable Audio | Stable Audio in-pod | D6 | 20 min |

Suggested split, about 2.5 h each: **night 1** A1–B2, **night 2** C1–C3, **nights 3–4** D0–D7.
Each step is a natural stop point. A step that fails stops its phase, not the other phases. The
exception is A1: nothing in phases B to D should start on a failed controller roll.

## 1. What changed in the refresh (2026-11-05)

- **!1289 retargeted** from `ccb2b61b` (`78a5ef9a`) to master `51e6bd81`
  (`sha256:6a3ad9e603110df8207a59f815d4e809e4b4a3357138302fd2faa39cfc5ccdcd`, tag
  `20261105-153904`).
  - The controller sources changed once after `ccb2b61b`: !1291 (`95e96f66`, render hygiene).
    They are identical from `470d0fe9` to `51e6bd81`.
  - I re-ran the roll session's live-snapshot render test against a 16:14Z snapshot (30 Models,
    3 GPUProfiles, the live controller env) at `d5aa35a7` (live) and `51e6bd81`. All 28 Models
    without `vllmVersion` render byte-identical pod templates and runtime payloads. Only the two
    0.33 KT canaries change, by the expected `vllmVersion` gate.
- **!1255**: the comment and changelog now name the live proxy build, `72628e0e`
  (`sha256:bdd4bd25…`), not `d5aa35a7`.
- **!1235–!1239**: the headers and changelogs said "BLOCKED on runtime image" and named
  `7ae8e92f`/`ef28b7b0`. That is no longer true. The XTX runtime has run `527e2714`
  (`rocm-gfx1100-serving-v032-ddf22437`) since !1234, and I checked its packages in the live
  runtime pod `flexinfer-runtime-gfx1100-v95tv`:
  - bitsandbytes 0.50.2, torchsde 0.2.6, imageio 2.37.0, imageio-ffmpeg 0.6.0, ftfy 6.3.1;
  - diffusers 0.42.0, text plugin 0.9.0, plugin-common 0.2.0;
  - `_bypass_miopen_for_wan_vae` in `/opt/flexinfer/server-diffusers.py` (!1268 `225b7150` is an
    ancestor of `ddf22437`);
  - still no librosa/soundfile, so whisper cannot transcribe on the XTX in-pod (accepted).

  The titles drop "BLOCKED" and name the check each lane needs instead.
- **Facts the descriptions had wrong** (corrected in the MRs):
  - !1257 says !1256 is in the running controller. It is not: `2b1ad119` merged at 18:11Z on
    11-02, after `d5aa35a7` (14:36Z). It goes live with A1. The 15000 budget park in B2 unloads
    on either build. Only K2's original 20000 park needs !1256.
  - !1289 says the proxy runs `d5aa35a7`. It runs `72628e0e` (!1264 and !1270 rode on top).
  - The D1 creative descriptions named lane files `2AME.yaml`…`5AME.yaml`. The files are
    `qwen-image-21-gfx1100.yaml`, `wan21-t2v-1p3b-gfx1100.yaml`,
    `wan21-vace-1p3b-i2v-gfx1100.yaml` and `stable-audio-open-gfx1100.yaml`.
  - The KT-SWAP row (`gfx1100-xtx-inpod-swap-2026-11-01`) still names `7ae8e92f` as the runtime
    under test. Pass `--runtime-image …527e2714…` in D4. The row text is left for its verdict MR.

## 2. Live state at the time of writing (2026-11-05 16:15Z)

- **Nodes.** All Ready, no DiskPressure or MemoryPressure.
  - Root free: 5930k 237 GB (≥ 200 GB rule holds), VII 101 GB, XTX 155 GB (83 % used, image GC
    at 90 %).
  - 5930k `MemAvailable` 15.7 GB.
- **Images.**

  | Role | Live digest |
  |---|---|
  | controller | `1286a2f1` (d5aa35a7) |
  | proxy | `bdd4bd25` (72628e0e) |
  | agent | `6b832f38` |
  | XTX runtime | `527e2714` |
  | 5930k runtime (parked) | `cfefae18` |
  | gfx906 runtime | `6fc6fcbd` |

- **XTX.** The KT-QOS / KT-SO window has run since 14:41Z and ends around 18:15Z
  (`qwen38-27b-qos-canary` Ready, `qwen38-27b-so-canary` Idle, both 480 with `vllmVersion: 0.33.0`).
- **Residency.** Every digest this runbook rolls to or back to is already on its node, so nothing
  is pulled in the window:
  - VII: `cb4c8dc3`, `34e60fd3`, `6fc6fcbd`;
  - XTX: `527e2714`, `7ae8e92f`, `cfefae18`, `b06b06fe`, `2c94c6d9`, `b1f98ad0`, `83cda206`,
    `d87f75f8`.
- **Harbor `pinned-*` tags.**
  - Present on every runtime digest above.
  - **Absent on every controller and proxy digest**, target and rollback alike. The pin-tags
    guard does not match the split `digest:` keys.
  - Rollback controller `1286a2f1` was last pulled 11-02 and can leave retention after about
    12-02. Proxy `bdd4bd25` was last pulled 11-03.
- **Secrets and CRD.** `flexinfer-jobs-minio`, `flexinfer-hf-token` and the CRD
  `inferencejobs.ai.flexinfer` exist.
- **ClusterIPs.** Unchanged since KT-RET.

  | Service | ClusterIP |
  |---|---|
  | proxy | `10.43.49.107` |
  | gte twin | `10.43.94.177:8080` |
  | gte VII | `10.43.153.107:8000` |
  | standalone pyannote | `10.43.139.54:8000` |
  | XTX warm lane | `10.43.71.72:8000` |
  | Qwen-Image | `10.43.195.207:8000` |
  | Wan T2V | `10.43.34.237:8000` |
  | VACE | `10.43.179.163:8000` |
  | Stable Audio | `10.43.28.46:8000` |

- **Background noise to expect.** `flexinfer-scheduler` runs a mutable timestamp tag
  (`:20261105-153904`) that gitops image automation moves, so it rolls ~20–30 min after **every**
  master merge in the window. It is not caused by the MR you just merged. Pinning it by digest
  like the agent (!1243) is a follow-up.

## 3. Conflicts and queued XTX canaries

| Pair | Conflict | Resolution in this order |
|---|---|---|
| !1289 vs !1221 | two controller rolls; !1221's pins (10-31) are older than live | close !1221; A1 is the only controller roll |
| !1255 vs !1221 | both turn jobs on; !1221 also rolls the proxy back to 10-31 | A2 only; close !1221 |
| !1289 vs any Model with `vllmVersion` | the roll re-renders and restarts it (drops dead `VLLM_*` env, `--mamba-cache-mode` on 0.33) | A1 gate: the jq check prints nothing |
| !1255 vs a KT harness through the proxy (KT-QOS seat reserve, KT-SO) | the proxy pod restarts mid-run | A2 only when no harness targets `10.43.49.107` |
| !1261 → !1262 → !1287 | the same DaemonSet `flexinfer-runtime-gfx906` repinned twice; the pyannote certificate must follow the runtime image | strictly C1 → C2 → C3 with a soak between; if KT-DIAR fails and !1262 is reverted, !1287 needs a rebase onto !1261 that drops its certificate hunk before C3 |
| !1257 (K2) vs !1287 | !1287 lowers gte `vramEstimateMB` 2600 → 2400; K2's unpark patch writes 2600 | B2 runs before C3, so 2600 is right; if K2 slips past C3, unpark to **2400** |
| B2 vs any `flexinfer-models` merge | K2 suspends the `flexinfer-models` Kustomization for ~8 min | merge nothing under `deploy/` until it is resumed |
| VII rolls (C1, C3) vs gte | each roll unloads gte ~80 s; gte fails over to the CPU twin | B1 lands first, so the twin has 12 threads for both rolls |
| !1235–!1239 | one stack: each branch carries its predecessors and targets `master` | merge strictly in order; merging a later one first brings the earlier lanes with it |
| proxy (72628e0e, pre-!1281) vs in-pod lanes | the proxy's direct-load path renders the pre-!1281 payload | no in-pod lane declares `vllmVersion` here (D2–D7 do not); see question Q2 |

**Queued XTX canaries.** Check right before A1 and before each D step:

```bash
export KUBECONFIG=/Users/cblevins/workspace/platform/gitops/.kube/k3s.yaml
kubectl get models.ai.flexinfer -n flexinfer-system -o json | jq -r '.items[] | select(.spec.gpu.shared=="7900xtx-textgen" and .spec.gpu.priority >= 480) | "\(.metadata.name) \(.spec.gpu.priority) \(.status.phase) vllmVersion=\(.spec.config.vllmVersion // "-")"'
kubectl get gpuleases.ai.flexinfer -A
```

- **KT-QOS / KT-SO** (`qwen38-27b-qos-canary`, `qwen38-27b-so-canary`): due to end around
  18:15Z on 11-05. They block A1 (`vllmVersion`) and A2 (harness through the proxy) until both
  Models are deleted.
- **S7 + G3 pool canary** (`qwen38-27b-pool-canary`, 480, ~2.5–3 h, after KT-QOS/SO): its MR is
  not on master yet. It blocks A1 only if it declares `vllmVersion` (the jq check decides). It
  blocks every D step while it holds the group. Wait for "XTX free" in
  `s7-g3-kv-pool-2026-11-05.md` in the shared notes.
- **S5 draft head**: stage A is offline first. **GPTQ split-K kernel**: an upstream PR first.
  Neither has a canary manifest yet. Ask both to queue after D7, or schedule D between them. No
  XTX canary may start while D2–D7 or KT-SWAP's GPULease is in flight.

## 4. Common procedure

**Pre-checks (every step).**

```bash
export KUBECONFIG=/Users/cblevins/workspace/platform/gitops/.kube/k3s.yaml
kubectl get nodes cblevins-5930k cblevins-7900xtx cblevins-radeonvii -o 'custom-columns=NAME:.metadata.name,READY:.status.conditions[?(@.type=="Ready")].status,DISK:.status.conditions[?(@.type=="DiskPressure")].status,MEM:.status.conditions[?(@.type=="MemoryPressure")].status'
kubectl get models.ai.flexinfer -n flexinfer-system -o json | jq -r '.items[] | select(.status.phase=="Failed") | .metadata.name'   # nothing
kubectl -n flexinfer-system get pods -o 'custom-columns=NAME:.metadata.name,UID:.metadata.uid,RESTARTS:.status.containerStatuses[0].restartCount' >| /tmp/pods-before.txt
```

**Harbor (for each digest the step pins, and its rollback digest).** Harbor does not resolve from
the Mac:

```bash
D=6a3ad9e60311; R=flexinfer-controller   # repo: flexinfer-controller | flexinfer-proxy | runtime
ssh cblevins-5930k "curl -sk 'https://registry.harbor.lan/api/v2.0/projects/flexinfer/repositories/$R/artifacts?q=digest%3D~sha256%3A$D&with_tag=true'" | jq -r '.[] | "\(.digest[7:19]) \([.tags[]?.name] | join(","))"'
```

**Merge.**

1. Check the branch against master:

   ```bash
   git fetch origin
   git merge-tree --write-tree --name-only origin/master origin/<branch>
   git diff --stat origin/master...origin/<branch>
   ```

   The merge-tree must be clean, and the diff must show only the MR's files. If master moved
   since the refresh, merge master in again; never force-push.
2. Mark the MR ready, then merge with auto-merge, so it merges when its pipeline passes:

   ```bash
   glab mr update <n> --ready
   ```

   ```bash
   glab api -X PUT "projects/services%2Fflexinfer/merge_requests/<n>/merge" -f auto_merge=true
   ```

   A 405 here means the MR is still preparing. Retry after a minute.
3. Reconcile Flux:
   - values change: `flux reconcile hr flexinfer -n flexinfer-system --with-source`;
   - `deploy/models` or `deploy/gpuprofiles` change:
     `flux reconcile ks flexinfer-models -n flux-system --with-source`;
   - over MCP: `flux_reconcile kind=hr name=flexinfer namespace=flexinfer-system with_source=true`.

**Soak, then go on.** The step's pass checks hold for the soak time, with no new `Failed` Model,
no unexpected pod restarts and a quiet host kernel log for that card:

```bash
kubectl -n flexinfer-system get pods -o 'custom-columns=NAME:.metadata.name,UID:.metadata.uid,RESTARTS:.status.containerStatuses[0].restartCount' | diff /tmp/pods-before.txt - | grep -v scheduler
```

```bash
ssh <node> 'sudo journalctl -k --since "-30 min" | grep -iE "amdgpu|smu|pcie|link down" | tail'
```

---

## Phase A: control plane

### A1. !1289: controller roll to master `51e6bd81`

- **Depends on.** Nothing. It is the only controller roll in this window (!1221 is closed, not
  merged).
- **Conflicts.** It restarts every Model that declares `vllmVersion`.
- **Pre-checks.**
  - The jq check prints **nothing**:

    ```bash
    kubectl get models.ai.flexinfer -A -o json | jq -r '.items[] | select(.spec.config.vllmVersion != null) | .metadata.name'
    ```

  - KT-QOS/SO are deleted. The S7 pool canary is not applied, or does not declare `vllmVersion`.
  - Harbor still has `6a3ad9e6` (target) and `1286a2f1` (rollback).
- **Merge and reconcile.** Merge !1289, then reconcile the HelmRelease.
- **Live checks (pass).**
  - The controller rolled within ~1 min:

    ```bash
    kubectl -n flexinfer-system rollout status deploy/flexinfer-controller --timeout=5m
    ```

  - The pod's `imageID` ends in `6a3ad9e603110df8…`:

    ```bash
    kubectl -n flexinfer-system get pods -l app.kubernetes.io/name=flexinfer,app.kubernetes.io/component=controller -o jsonpath='{..imageID}'
    ```

    If the labels differ, take the pod name from `get pods | grep controller`.
  - **No lane pod restarts.** The before/after diff shows only the controller (and maybe the
    scheduler). The runtime-served lanes did not reload: radeonvii lanes stay Ready, and the
    runtime logs show no new load.
  - No Model turns `Failed` or `ConfigInvalid`:

    ```bash
    kubectl get models.ai.flexinfer -n flexinfer-system -o json | jq -r '.items[] | select([.status.conditions[]?.reason] | index("ConfigInvalid")) | .metadata.name'
    ```

    This prints nothing.
  - Controller logs, 10 min: no reconcile error loop.

    ```bash
    kubectl -n flexinfer-system logs deploy/flexinfer-controller --since=10m | grep -c ERROR
    ```

    Compare the count with a pre-roll 10 min window.
- **Soak.** 30 min.
- **Rollback.** Revert the MR, or set `controller.image.digest` back to
  `sha256:1286a2f15c0a5a9f95de19275ce57bfa564ddcbd75517be4b80cf306d29b7c0f` (tag
  `20261102-144015`), then reconcile the HelmRelease.

### A2. !1255: InferenceJob API on (no image move)

- **Depends on.** A1 soaked, so one component changes at a time.
- **Conflicts.**
  - The proxy pod restarts. It is one replica, rolling, so expect seconds of disturbance.
  - No KT harness may be running through the proxy.
  - imagegen-ui's video tab switches to jobs on its next request.
- **Pre-checks.**
  - Secret `flexinfer-jobs-minio` (keys `accessKey`, `secretKey`) exists:

    ```bash
    kubectl -n flexinfer-system get secret flexinfer-jobs-minio
    ```

  - `GET /v1/jobs` answers 501 `jobs_disabled` today.
- **Merge and reconcile.** Merge !1255, then reconcile the HelmRelease.
- **Live checks (pass)**, from its description:

  ```bash
  kubectl -n flexinfer-system rollout status deploy/flexinfer-proxy --timeout=5m
  ```

  ```bash
  kubectl -n flexinfer-system logs deploy/flexinfer-proxy --since=5m | grep -i -E 'jobs|error' | head
  ```

  ```bash
  ssh cblevins-radeonvii 'curl -s http://10.43.49.107/v1/jobs'
  ```

  ```bash
  ssh cblevins-radeonvii 'curl -s http://10.43.49.107/v1/chat/completions -H "Content-Type: application/json" -d "{\"model\":\"qwen3-1p7b-tools-radeonvii\",\"background\":true,\"max_tokens\":8,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"'
  ```

  ```bash
  kubectl -n flexinfer-system get inferencejobs -o wide
  ```

  - `GET /v1/jobs` returns `{"object":"list","data":[]…}`.
  - The background job goes `queued` → `completed` within seconds, and `/v1/jobs/<id>/content`
    returns the completion.
  - The proxy log has no `job store is not usable`.
  - A synchronous chat to a Ready lane still answers.
  - gte p95 through the proxy is back to its pre-roll level within 5 min:

    ```promql
    histogram_quantile(0.95, sum by (le) (rate(flexinfer_proxy_request_duration_seconds_bucket{model="gte-qwen2-1p5b-radeonvii"}[5m])))
    ```

- **Soak.** 30 min. Watch `flexinfer_jobs_running` and proxy 5xx.
- **Rollback.** Revert, or set `proxy.jobs.enabled: false`, then reconcile the HelmRelease.
  - Queued InferenceJobs stay until re-enabled or deleted.
  - imagegen-ui falls back on the 501.

## Phase B: the 5930k CPU twin (gte's failover target)

### B1. !1257: gte twin 12 threads, 14-core limit

- **Why before the VII.** C1 and C3 each unload gte for ~80 s, and gte fails over to this twin.
- **Depends on.** A1, so !1256 is in the controller for B2.
- **Conflicts.** None. Only the twin's Deployment restarts. The Radeon VII is untouched.
- **Merge and reconcile.** Merge !1257, then reconcile `flexinfer-models`.
- **Live checks (pass).**

  ```bash
  kubectl -n flexinfer-system rollout status deploy/gte-qwen2-1p5b-cpu-5930k --timeout=5m
  ```

  ```bash
  kubectl -n flexinfer-system get deploy gte-qwen2-1p5b-cpu-5930k -o jsonpath='{.spec.template.spec.containers[0].args}{"\n"}{.spec.template.spec.containers[0].resources.limits.cpu}{"\n"}'
  ```

  - The args show `"--threads","12"` and the limit is `14`.
  - Twin-only check (3 min, from the MR):

    ```bash
    ssh cblevins-5930k 'cd ~/kt-ret-20261031 && python3 -I ~/kt-ret-retrieval-swap.py capacity --url http://10.43.94.177:8080 --concurrency 1 4 --seconds 30 --out cap-t12 && python3 -I ~/kt-ret-retrieval-swap.py capacity --url http://10.43.94.177:8080 --concurrency 1 --seconds 30 --words 550 --out cap-t12-long'
    ```

    ```bash
    kubectl -n flexinfer-system logs deploy/gte-qwen2-1p5b-cpu-5930k --since=3m | grep -E "prompt processing|stop processing: n_tokens = [5-9][0-9][0-9]" | tail -4
    ```

  - **Go at ≥ 140 tokens/s** prompt processing (82.5 at 6 threads). At ≤ 100, revert and skip B2.
- **Rollback.** Revert the MR (back to 6 threads, limit 6).

### B2. KT-RET K2 re-run (test, no MR)

- **Depends on.**
  - B1 passed.
  - A1 merged. It carries !1256. The 15000 budget park unloads on either build, but run it on
    the build that is now live.
- **Conflicts.**
  - `flexinfer-models` is suspended for ~8 min: merge nothing under `deploy/` until it is
    resumed.
  - The gte Service stream fails by design during the park.
- **Steps.** Follow `.loom/32-iteration-plan-kt-ret-2026-10-31.md` § K2 re-run, step 3.
  - Read the gfx906 runtime pod IP first.
  - At `PARK`: suspend `flexinfer-models`, then patch `vramEstimateMB: 15000`.
  - At `UNPARK`: patch `vramEstimateMB: 2600` (**2400 if C3 has merged**), then resume.
- **Pass** (row `gfx906-retrieval-swap-safety-2026-10-31`):
  - park p95 ≤ 2 s;
  - 0 5xx;
  - a reroute or replay header on every park-phase answer.

  Then tell a session to record the verdict from `~/kt-ret-20261031/k2b`.
- **Rollback.** Patch the estimate back and resume `flexinfer-models` (plan § Rollback). A twin
  that misses only on long production inputs points to `ubatchSize` next, not a revert.

## Phase C: the Radeon VII (gfx906 runtime, three steps)

Each C step rolls `flexinfer-runtime-gfx906`, and the warm set reloads in ~80 s. Run C in a
quiet hour, with `gemma4-e4b-radeonvii` (110) and `openjev-0p8b-crossencoder-radeonvii` (112)
idle: both are Idle now. Telemetry for all three steps:

```promql
max_over_time(flexinfer_runtime_hw_queues{gpu_arch="gfx906",source="observed"}[1h])
histogram_quantile(0.95, sum by (le) (rate(flexinfer_proxy_request_duration_seconds_bucket{model="gte-qwen2-1p5b-radeonvii"}[5m])))
```

### C1. !1261: VII runtime `6fc6fcbd` → `cb4c8dc3` (pyannote layer)

- **Depends on.** B1, so the twin can absorb gte's failover.
- **Conflicts.**
  - The same DaemonSet is repinned again in C3.
  - It carries no certificate: the controller still refuses pyannote Models.
- **Pre-checks.**
  - `cb4c8dc3` is resident on the VII (it is), and Harbor has `pinned-cb4c8dc3c2bd` (it does).
  - No other VII change is in flight.
- **Merge and reconcile.** Merge !1261. Reconcile the HelmRelease (DaemonSet image and token
  mount), then `flexinfer-models` (GPUProfile `gfx906` `runtime.image` and `bundledBackends`),
  back to back.
- **Live checks (pass)**, from the MR:

  ```bash
  kubectl -n flexinfer-system rollout status ds/flexinfer-runtime-gfx906 --timeout=15m
  ```

  ```bash
  kubectl -n flexinfer-system get models -o wide | grep radeonvii
  ```

  ```bash
  kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- sh -c 'cd /opt/flexinfer && /opt/pyannote/bin/python -c "import torchaudio_compat, torch, pyannote.audio; print(torch.__version__, torch.version.hip, torch.cuda.is_available(), torch.cuda.get_device_name(0), pyannote.audio.__version__)"'
  ```

  ```bash
  kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- test -r /var/run/flexinfer/secrets/flexinfer-hf-token/HF_TOKEN && echo token-mounted
  ```

  - The pyannote import prints `2.9.0 6.3.42134… True AMD Radeon VII 3.3.2`, and the token check
    prints `token-mounted`.
  - The warm set is back in its phases (bge-large, bge-reranker, gte, orpheus, whisper.cpp Ready).
  - gte answered throughout via the twin, with 0 5xx (the proxy reroute header is on the answers
    in the gap).
  - gte p95 is back to the pre-roll level within 5 min, and compute queues are ≤ 24.
- **Soak.** 30 min.
- **Rollback.** Revert (revert C2 first if it merged), then reconcile both. The VII returns to
  `6fc6fcbd` (prewarmed, pinned) and the token is unmounted.

### C2. !1262: pyannote lane, provisional certificate, then KT-DIAR

- **Depends on.** A2 (jobs on) and C1 rolled and soaked. GitLab retargets !1262 to master when
  !1261's branch is deleted on merge. If it does not, retarget it by hand before merging.
- **Conflicts.**
  - `docs/certification-matrix.json` shows the certificate `certified` while the row is
    `pending`, by design.
  - Queue budget: 18 of 21 estimated, 21 of 24 compute measured. Keep gemma4-e4b and openjev idle.
- **Merge and reconcile.** Merge !1262, then reconcile `flexinfer-models`.
- **Live checks.** Run the D5-a plan, § "Window as prepared 2026-11-02", G3 and W7.
  1. `BackendCertificate` is True and the phase is Idle:

     ```bash
     kubectl -n flexinfer-system get model pyannote-diarization-radeonvii -o jsonpath='{.status.conditions[?(@.type=="BackendCertificate")].status} {.status.phase}{"\n"}'
     ```

  2. The descriptor shows diarization `available`.
  3. Warm the lane with one 60 s job.
  4. Run the harness on `cblevins-radeonvii` with `--kfd`, using the ClusterIPs in §2. Re-read
     the bge-large ClusterIP (`10.43.47.6` on 11-02).
  5. Run the `diarize_conformance` gate.
- **Pass** (row `gfx906-diarize-job-2026-11-xx`, every check):
  - `meeting_length` ≥ 29.5 min;
  - `speaker_attributed_words` ≥ 95 %;
  - DER(job) − DER(standalone) ≤ 5 points;
  - `gte_p95` ≤ 1.2× its 240 s baseline;
  - compute queues ≤ 24, no `oversubscribed` line, pyannote process ≤ 1,700 MB.
- **Verdict.** A session records it in a new MR. On a FAIL, revert !1262.
- **Rollback.** Revert !1262, then reconcile `flexinfer-models`. Flux prunes the lane, the
  annotation and the certificate. The standalone Deployment serves `/diarize` throughout.

### C3. !1287: VII runtime `cb4c8dc3` → `34e60fd3` (llama.cpp b11847)

- **Depends on.**
  - **Owner acceptance of the S8 verdict (Q1).** Row `gfx906-llamacpp-b11847-2026-11-05`
    becomes `promote`, or a rerank rerun with a scale-aware bound passes.
  - The C2 verdict is in.
  - If C2 failed and !1262 was reverted, !1287 must first be rebased onto !1261 (merge, not
    force) with its certificate hunk dropped. That is a session's job before C3.
- **Conflicts.**
  - The second repin of the same DaemonSet.
  - It lowers `vramEstimateMB` on gte (2400), qwen3-reranker (1200) and qwen3-embedding (5400).
  - The pyannote certificate moves to `34e60fd3` with it. Reconcile HelmRelease and
    `flexinfer-models` back to back, so the lane does not sit refused.
- **Merge and reconcile.** Merge !1287, then reconcile both.
- **Live checks (pass)**, from the MR and the row:

  ```bash
  kubectl -n flexinfer-system rollout status ds/flexinfer-runtime-gfx906 --timeout=15m
  ```

  ```bash
  kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- sh -c 'cat /opt/llamacpp/BUILD; env -u HSA_OVERRIDE_GFX_VERSION LD_PRELOAD= /opt/llamacpp/bin/llama-server --version 2>&1 | head -1'
  ```

  ```bash
  kubectl -n flexinfer-system exec ds/flexinfer-runtime-gfx906 -- sh -c 'n=0; for t in /sys/class/kfd/kfd/proc/*/queues/*/type; do [ "$(cat $t)" = 0 ] && n=$((n+1)); done; echo $n'
  ```

  - `/opt/llamacpp/BUILD` shows `b11847 9145ddb9…`, and `--version` shows commit `9145ddb`.
  - Every radeonvii lane is back in its phase.
  - Compute queues are no higher than the same count taken before the merge. Expect 17–18 with
    the pyannote lane idle (5 runtime lanes × 3, plus the standalone pyannote Deployment's 2–3),
    and 3 more while it is loaded. The ceiling is 24.
  - gte p95 is ≤ 1.05× the pre-roll p95 (the canary measured 142 → 123 ms at conc 4).
  - Each lane's KFD VRAM is under its new estimate.
- **Soak.** 30 min.
- **Rollback.** Revert, then reconcile both. The VII returns to `cb4c8dc3` (prewarmed, pinned)
  and the certificate goes back with it.

## Phase D: the 7900 XTX flex card

Before every D step:
- "XTX free" (§3 check prints nothing at ≥ 480; no GPULease);
- the card is on the bus at its link, Gen3 x8 since 11-03:

  ```bash
  ssh cblevins-7900xtx "sudo lspci -vv 2>/dev/null | grep -A60 'VGA compatible controller.*Navi 31' | grep -m1 LnkSta:"
  ```

- the kernel log is quiet.

The XTX is an etcd voter. Abort the phase on any `amdgpu`/`smu`/PCIe error.

**Hand-back rule.** The warm lane is `warmPolicy: ondemand`. After any creative render, send one
proxied warm-lane request so it reclaims the card (it stayed Queued 16 min without one on 11-04):

```bash
ssh cblevins-5930k 'curl -s http://10.43.49.107/v1/chat/completions -H "Content-Type: application/json" -d "{\"model\":\"qwen38-27b-xtx-warm-canary\",\"max_tokens\":4,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}" | head -c 200'
```

**Runtime pod memory.** In-pod lanes share the XTX runtime pod's 48 GiB limit. An OOM kill
restarts every in-pod lane on the card. Watch:

```promql
max_over_time(container_memory_working_set_bytes{namespace="flexinfer-system",pod=~"flexinfer-runtime-gfx1100-.*",container!=""}[10m])
```

It must stay < 44 GiB.

**Profiler binary.** Nodes reach ClusterIPs and pod IPs; the Mac does not. Build once:

```bash
GOOS=linux GOARCH=amd64 go build -o /tmp/flexinfer-bench ./cmd/flexinfer-bench
```

```bash
scp /tmp/flexinfer-bench cblevins-5930k:~/kt-swap/
```

Copy `deploy/gpuprofiles/gfx1100.yaml` and the five lane manifests there too.

### D0. Warm-lane Deployment baseline (D1 "order 0", test)

- **Depends on.** The XTX is free, and the warm lane is Ready as its dedicated Deployment (it is
  Preempted while KT-QOS runs).
- **Run.**

  ```bash
  ssh cblevins-5930k 'cd ~/kt-swap && ./flexinfer-bench swap --probe-url http://10.43.71.72:8000 --cycles 3 --out baseline-warm.json'
  ```

  Keep `baseline-warm.json`: D2 and D4 compare against it.
- **Pass.** The report has three cycles and the three parity hashes. Nothing changes live.

### D1. KT-JOBS on the current layout (test, row `proxy-async-jobs-2026-10-31`)

- **Why here.** It tests the D2 contract on the layout the row was written for (dedicated
  creative lanes), before D1 changes swap dynamics. It needs A2.
- **Run.** Follow `.loom/32-iteration-plan-kt-jobs-2026-10-31.md` § Run: the in-cluster `kt-jobs`
  pod, `scripts/kt-jobs.py`, 35–50 min.
- **Pass.** Row bars (1)–(9). Bar (9) needs the loom-core `error.code` classifier; run its unit
  test on the captured `cold_probe`.
- **Rollback.** None needed (test only). `proxy.jobs.enabled: false` disables jobs if the run
  shows a leak.

### D2. !1235: warm lane in-pod on `527e2714`

- **Depends on.** D0's baseline exists. The XTX runtime pod runs `527e2714` with 0 restarts and
  mounts `/cache/compile` (true since 11-04).
- **Conflicts.**
  - No render in flight.
  - No XTX canary.
  - The lane declares no `vllmVersion`, so the pre-!1281 proxy does not matter.
- **Merge and reconcile.** Merge !1235, then reconcile `flexinfer-models`.
  - The controller deletes the lane's Deployment and loads it in-pod.
  - The first in-pod load compiles cold (new image-scoped cache). The proxy holds requests for
    up to the lane's 25 min `coldStartTimeout`.
- **Live checks (pass).**

  ```bash
  kubectl -n flexinfer-system get model qwen38-27b-xtx-warm-canary -o jsonpath='{.status.phase} {.status.loadingSubstage} {.status.kvPool}{"\n"}'
  ```

  - The status goes `LoadingWeights` → `Compiling` → `ProfilingKV` → `Ready`.
  - The KV pool is ~132K tokens (~2.0×). A short pool triggers the in-pod recheck.
  - `http://<xtx-runtime-pod-ip>:8080/api/v1/models/qwen38-27b-xtx-warm-canary/health` shows
    `Ready` and `kvCacheTokens` set.
  - A Mills call through the proxy answers, and
    `flexinfer_proxy_requests_total{model="qwen38-27b-xtx-warm-canary"}` rises with no 5xx.
  - **Decode sanity:** one 256-token greedy request decodes at ≥ 0.9× D0's median tokens/s.
    This catches a missing W4A16 kernel early; KT-SWAP's 0.97× bar is the real test.
  - The kernel log is quiet.
- **Soak.** 60 min with Mills traffic.
- **Rollback.**
  - Fast: `flexinfer_update_model` merge-patch `{"config":{"dedicatedDeployment":true}}`. The
    Deployment returns on `b06b06fe`; Flux does not undo an added field.
  - Full: revert, which also restores the per-Model compile-cache path.

### D3. !1236: Qwen-Image 2.1 in-pod (NF4 check)

- **Depends on.** D2 soaked.
- **Conflicts.**
  - The branch carries !1235's commits; merge only after D2.
  - Its fast rollback must restore the `command` wrapper too, so roll back by **revert** only.
- **A arm, before merging.** One 1024² render on the dedicated lane, at a fixed prompt and seed,
  through the proxy. Save the PNG and the server's render time.
- **Merge and reconcile.** Merge !1236, then reconcile `flexinfer-models`. At its next demand the
  lane loads in-pod and preempts the warm lane (500 > 300).
- **Live checks (pass).**
  - The same render in-pod returns 200, and the PNG decodes at 1024².
  - The owner, side by side with the A arm, sees a coherent image: not noise, not black, not
    uniform. Exact pixels will differ across torch builds.
  - The runtime log shows the NF4 load, with no bitsandbytes `CUDA`/`HIP` error and no NaN
    warning.
  - The in-pod render time is ≤ 1.25× the A arm (warm).
  - Runtime pod memory stays < 44 GiB.
  - The warm lane is back within the 5-min cooldown after the hand-back ping.
- **Rollback.** Revert (restores `dedicatedDeployment` and the `command` wrapper), then reconcile.

### D4. KT-SWAP (test, row `gfx1100-xtx-inpod-swap-2026-11-01`)

- **Depends on.** D2 and D3 in-pod, and D0's baseline. The full arm (warm ↔ Qwen-Image) is
  possible now that bitsandbytes is in the image.
- **Hold the card first.** Create a GPULease on `7900xtx-textgen`, so the controller does not
  reload lanes into the VRAM being measured. Release it at the end.
- **Run** (row command, runtime image corrected):

  ```bash
  ssh cblevins-5930k 'cd ~/kt-swap && ./flexinfer-bench swap --runtime-url http://<xtx-runtime-pod-ip>:8080 --gpuprofile gfx1100.yaml --lane warm=qwen38-27b-xtx-warm-canary.yaml --lane image=qwen-image-21-gfx1100.yaml --cycles 5 --baseline baseline-warm.json --row-id gfx1100-xtx-inpod-swap-<date> --runtime-image registry.harbor.lan/flexinfer/runtime@sha256:527e2714c163f3c284bcba7e168590e8cb8960e2b1cf247b00976bb7b8f30d0b --rollback-image registry.harbor.lan/flexinfer/runtime@sha256:cfefae18061fd2f9f07cfbfb9b0c2abb51cb4ab34dabda86f3969c13eca9f278 --out kt-swap.json --row-out kt-swap-row.md'
  ```

- **Pass** (row, verbatim bars):
  - 27B load p50 ≤ 120 s and p95 ≤ 180 s;
  - diffusers load p50 ≤ 90 s;
  - greedy hash identical to the Deployment baseline on the 3 parity prompts;
  - decode ≥ 0.97× baseline;
  - 0 SMU/amdgpu errors;
  - the warm lane back within 5 min after each render.

  Record the substage breakdown whatever the verdict.
- **On a FAIL.** Roll back D3 then D2, and stop phase D.

### D5. !1237: Wan 2.1 T2V in-pod (decode A/B)

- **Why an A/B.** In-pod Wan runs on the runtime image's torch 2.13 / ROCm 7.2.3 MIOpen, not
  the dedicated `b1f98ad0` stack. The VAE bypass is the same code (MIOpen off for 16-bit Wan
  VAEs), but the im2col + GEMM path it falls back to is a different build.
- **Pass rule, fixed here before any data.**
  - **Setup.** One 33-frame 832×480 clip, the same prompt, seed and steps. Run it twice on each
    arm: the first run is cold and recorded only, the second is warm and compared.
  - **Arm A:** the dedicated lane before the merge. The 11-04 reference was a cold clip in 252 s
    with a 7 s decode. **Arm B:** in-pod after the merge.
  - **Timings.** Read `decode_postprocess_seconds` and `total_seconds` from the
    `[video-timing]` line:
    - A: `kubectl logs deploy/wan21-t2v-1p3b-gfx1100`;
    - B: `kubectl logs ds/flexinfer-runtime-gfx1100`.
  - **B passes when all of these hold:**
    - the log has `Wan VAE encode/decode run with MIOpen off`;
    - warm decode ≤ 1.5× A's warm decode and ≤ 15 s;
    - warm total ≤ 1.25× A's warm total;
    - the cold clip's first byte through the proxy ≤ 300 s (the 5-min rule);
    - the MP4 is H.264 with 33 frames (`ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,nb_frames`);
    - runtime pod memory < 44 GiB;
    - the host log is quiet;
    - the warm lane is back after the hand-back ping.
  - **Outcome.** Any miss means revert !1237 and keep T2V dedicated; a session writes it up.
- **Merge and reconcile.** Merge !1237 (it carries !1235 and !1236, already merged), then
  reconcile `flexinfer-models`.
- **Rollback.** Revert (restores `dedicatedDeployment` on `b1f98ad0`), then reconcile.

### D6. !1238: Wan 2.1 VACE in-pod (decode A/B)

- **Why an A/B.** The VACE VAE is fp32, so the bypass does not apply. Its decode runs through
  MIOpen on both arms: the dedicated `83cda206` stack against the runtime's ROCm 7.2.3 MIOpen.
  ROCm 7.2.3 may be faster or slower.
- **Pass rule, fixed here.**
  - Same setup as D5, with one fixed keyframe and prompt on each arm.
  - The log has `Keeping MIOpen for the Wan VAE in torch.float32`.
  - Warm total ≤ 1.25× A's warm total.
  - The output is H.264 with the requested frames.
  - The `ftfy` path runs: no `NameError` in `basic_clean`.
  - Memory < 44 GiB, and the host log is quiet.
  - A miss means revert and keep VACE dedicated.
- **Merge and reconcile.** Merge !1238, then reconcile `flexinfer-models`.
- **Rollback.** Revert, then reconcile.

### D7. !1239: Stable Audio Open in-pod

- **Pass.**
  - One render (the lane's default length) returns 200.
  - The audio decodes, and its duration matches the request.
  - The runtime log shows `CosineDPMSolverMultistepScheduler` loaded, with no dummy-object
    error (torchsde).
  - The render time is ≤ 1.25× the dedicated lane's (an A arm before the merge).
  - The warm lane is back after the ping.
- **Merge and reconcile.** Merge !1239, then reconcile `flexinfer-models`.
- **Rollback.** Revert (back to `d87f75f8`, `rocm-gfx1100-audio2`), then reconcile.

After D7, the XTX is free for S5 / GPTQ-kernel canaries. Write "XTX free" in the shared notes.

## 5. Open questions only the owner can answer

- **Q1. S8 verdict (gates C3).** Accept the three calibration misses of row
  `gfx906-llamacpp-b11847-2026-11-05` and mark it `promote`? The misses:
  - bge-reranker |Δscore| 0.033 on a logit scale, same order;
  - Qwen3-Reranker ranks 5 and 6 swap at about 8e-5;
  - one 13-probe live-gte tail that did not reproduce on 59 probes.

  The alternative is a rerank rerun with a scale-aware bound first.
- **Q2. Proxy repin.** Should a proxy repin to master `51e6bd81`
  (`sha256:d0439aacec17a4dc7655a99bd06c72f3dd65018fb9aaf4fc7ac226328fe3cc72`) ride in !1255's
  restart (one proxy restart instead of two)?
  - Between the live `72628e0e` build and master, the proxy's sources differ only in
    `backend/vllm.go`, `backend/vllm_omni.go` (the `vllmVersion`-gated render) and a comment.
  - The same live-snapshot test shows identical payloads for all 28 lanes.
  - It removes the "no `vllmVersion` on in-pod lanes" caveat.
  - If yes, a session adds the hunk to !1255 before A2. If no, it waits for the first in-pod 0.33
    lane.
- **Q3. Close !1221?** Recommended: its pins are older than live, and its jobs half is !1255.
- **Q4. Warm-lane hand-back.** Keep `warmPolicy: ondemand` and the ping rule, or make the warm
  lane `warmPolicy: primary`, so it reclaims the card on its own after a borrow (D2–D7, KT-SWAP,
  KT-JOBS)?
- **Q5. XTX canary queue.** Order S7's pool canary, S5 stage B and the GPTQ split-K canary
  around phase D. Recommended: S7 before phase D (it may already run at 00:15Z); S5 and GPTQ
  after D7.
- **Q6. Harbor protection for the control-plane digests.** No controller or proxy digest has a
  `pinned-*` tag, including rollbacks `1286a2f1` and `bdd4bd25`. Tag the four (target and
  rollback for each) by hand before the next Saturday 02:00Z retention run, or wait for the
  pin-guard fix?
- **Q7. Wan and Stable Audio in-pod at all?** D5–D7 put a 48 GiB-limit render inside the same
  pod as the warm lane. If the owner would rather keep video and audio dedicated this round,
  stop after D4. The Drafts stay valid.

## 6. Follow-ups (not in the window)

- Pin `flexinfer-scheduler` by digest (it rolls on every master build).
- Teach the Harbor pin-tags guard the split `repository:`/`digest:` form.
- After D4, the KT-SWAP verdict MR corrects the row's runtime image to `527e2714`.
- After C2 PASS: retire the standalone pyannote Deployment and its queue reservation (D5-a plan
  § After a PASS).
