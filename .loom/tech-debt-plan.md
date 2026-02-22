# Technical Debt Remediation Plan

## Summary

- Planning date: 2026-02-21
- Scope: FlexInfer — all tech debt discovered or created during the sdxl-turbo-imagegen deployment and image quality optimization session
- Total items considered: 8

## Scoring Snapshot

- Ranking artifact: `.loom/tech-debt-priority.md`
- Scoring model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%
- Top score: DEBT-001 (86), Bottom score: DEBT-006 (53)

---

## Wave 1 — Quick Wins / High Leverage (Score ≥ 78)

**Goal:** Eliminate the two highest-leverage items that require minimal effort and reduce daily operational drag.

### DEBT-001: Fix ConfigBool to handle string values (Score: 86)

- **Status:** DONE (commit `2c6bd04`)
- **Component:** `backend/interface.go`
- **Problem:** `ConfigBool` uses `v.(bool)` type assertion. CRD config values are JSON strings (`"true"`), not Go bools. Silent failure returns default `false`.
- **Evidence:** Caused `USE_CPU_OFFLOAD=0` despite `cpuOffload: "true"` in CRD config. Led to ROCm memory access fault crash-looping sdxl-turbo-imagegen (12 restarts). Commit `fabf883` fixed the caller but not the root cause.
- **Fix:** In `backend/interface.go`, make `ConfigBool` handle string `"true"`/`"false"` via `strconv.ParseBool` before falling back to `v.(bool)`.
- **Acceptance criteria:**
  - `ConfigBool("key", false)` returns `true` when config map has `"true"` (string)
  - `ConfigBool("key", false)` returns `true` when config map has `true` (bool)
  - Unit tests cover string, bool, and missing-key cases
- **Test/verification:** Unit test `TestConfigBool_StringValues` with table-driven cases.
- **Rollback:** No rollback needed — additive change to type handling.
- **Effort:** S (1 point) — single function change + tests.

### DEBT-002: Switch to immutable image tags in CI/GitOps (Score: 78)

- **Status:** DONE (commit `892f3e6` — auto-detect pullPolicy in Helm chart)
- **Component:** `deployment/pullPolicy`, `values.yaml`, CI pipeline
- **Problem:** All deployments use mutable tags (`master`, `rocm-latest`) with `pullPolicy: IfNotPresent`. Node containerd caches old images by tag. Every deploy risks running stale code.
- **Evidence:** Hit 3 times this session: proxy, controller, and diffusers images all served stale cached versions after tag updates. Each required manual `crictl rmi` or unique SHA tags.
- **Fix:**
  1. CI pipeline already tags images with `CI_COMMIT_SHORT_SHA` — make this the primary tag in `values.yaml`
  2. Update Helm chart templates to use commit SHA tags from values
  3. Keep `pullPolicy: IfNotPresent` (correct for immutable tags, avoids registry dependency)
  4. Add `pullPolicy: Always` only for `kubeScheduler` sidecar (upstream image, semver-tagged)
- **Acceptance criteria:**
  - `values.yaml` uses commit SHA tags for all flexinfer images
  - CI pipeline updates `values.yaml` with new SHA on each build
  - No `pullPolicy: Always` on flexinfer images
  - Manual `crictl rmi` not needed after any deploy
- **Test/verification:** Deploy a code change, verify the node pulls the new image without manual intervention.
- **Rollback:** Revert values.yaml to previous SHA tag.
- **Effort:** S (2 points) — CI pipeline change + values.yaml update.
- **Dependencies:** None.

### DEBT-004: Proxy alias resolution — already fixed (Score: 84)

- **Status:** fixed (commit `dd15221`)
- **No further action.** 7 unit tests added, deployed and verified.

---

## Wave 2 — Medium Effort / High Risk Reduction (Score 53–69)

**Goal:** Harden build reproducibility and improve operational observability.

### DEBT-003: Pin diffusers base image and Python deps (Score: 69)

- **Status:** DONE (commit `01f602b` — pinned base image to rocm6.2.3, created requirements-diffusers-rocm.txt)
- **Component:** `build/Dockerfile.diffusers-rocm`
- **Problem:** `rocm/pytorch:latest` base image is unpinned. Python deps float to latest, causing breaking import chains.
- **Evidence:** transformers v5.2.0 removed `MT5Tokenizer`, breaking the diffusers build. Required emergency pin and rebuild.
- **Fix:**
  1. Pin base image to specific tag: `rocm/pytorch:2.9.1-rocm7.1` (or current known-good version)
  2. Create `requirements.txt` with pinned versions and hash verification
  3. Add Renovate/Dependabot config to track upstream updates
- **Acceptance criteria:**
  - `FROM` line uses pinned tag with digest
  - All Python deps pinned with `==` versions in `requirements.txt`
  - `pip install --require-hashes` used in Dockerfile
  - Build is reproducible from cold cache
- **Test/verification:** Rebuild image from clean cache; verify identical pip freeze output.
- **Rollback:** Revert Dockerfile to previous pinned versions.
- **Effort:** S (2 points).

### DEBT-005: Add structured logging for controller deployment updates (Score: 64)

- **Status:** DONE (commit `539060f`)
- **Component:** `controllers/model_controller.go`
- **Problem:** `ensureDeployment()` only logs on Create, not on Update. Impossible to verify reconciliation from logs.
- **Evidence:** During cpuOffload debugging, controller logs showed zero evidence of sdxl-turbo-imagegen reconciliation. Had to inspect ReplicaSet history manually.
- **Fix:**
  1. Add `log.Info("Updating Deployment", "name", ...)` at the update path in `ensureDeployment()`
  2. Use `apiequality.Semantic.DeepEqual` to detect actual changes before logging
  3. Log changed fields at Info level; log full desired spec at Debug level
- **Acceptance criteria:**
  - Controller logs show "Updating Deployment" with model name and changed fields when reconciliation triggers an update
  - No log noise when deployment is already in desired state
- **Test/verification:** Apply a config change to a Model CRD; verify controller logs show the update.
- **Rollback:** Remove the log lines.
- **Effort:** XS (1 point).

### DEBT-006: Bundle fp16-fix VAE for SDXL on ROCm (Score: 53)

- **Status:** DONE (commits `aa991fe` controller VAE prefetch, `4c9df9b` diffusers VAE_PATH wiring, gitops `64a34cb0`)
- **Component:** `build/Dockerfile.diffusers-rocm`
- **Problem:** SDXL on ROCm requires `madebyollin/sdxl-vae-fp16-fix` for artifact-free fp16 inference. Without it, fp32 is required (2x VRAM, slower).
- **Fix:**
  1. Download `madebyollin/sdxl-vae-fp16-fix` to SharedPVC cache during model init
  2. Add `VAE_PATH` env var support in `server.py`
  3. Load custom VAE when `VAE_PATH` is set and model is SDXL
  4. Update `image-gen.yaml` config: `vaePath: "madebyollin/sdxl-vae-fp16-fix"`, `useFp16: "1"`
- **Acceptance criteria:**
  - SDXL model generates artifact-free images in fp16 on ROCm
  - VRAM usage ~50% lower than fp32
  - VAE cached on SharedPVC (no re-download on restart)
- **Test/verification:** Generate test image in fp16 on gfx1100; compare quality to fp32 baseline.
- **Rollback:** Set `useFp16: "0"` in config to fall back to fp32.
- **Effort:** S (2 points).

---

## Wave 3 — Strategic Refactors (Score 56–58)

**Goal:** Address platform-level issues that require infrastructure coordination.

### DEBT-007: Unify Docker/containerd image stores (Score: 58)

- **Status:** DONE (commit `78c66da` — CI publish job for ollama-maxwell, SHA tags on all backend images)
- **Component:** `platform/gitops + Docker/containerd`
- **Problem:** Docker Desktop and K3s containerd use separate image stores on GPU nodes. Manual `docker save | ctr import` required for locally-built images.
- **Fix:**
  1. Move all image builds to CI (no local builds needed)
  2. Use `buildctl`/`buildx` pushing directly to Harbor
  3. K3s pulls from Harbor exclusively
- **Acceptance criteria:**
  - No manual image import workflow needed
  - All images built in CI and pushed to Harbor
  - K3s nodes pull from Harbor for all images
- **Test/verification:** Build and deploy a new diffusers image entirely through CI.
- **Rollback:** N/A — process change.
- **Effort:** M (3 points).
- **Dependencies:** DEBT-002 (immutable tags make this workflow reliable).

### DEBT-008: Fix GPU device misidentification (iGPU vs dGPU) (Score: 56)

- **Status:** DONE (commit `2e75cfb` — shared DeviceIsolationEnvVars, gitops pinned to hipVisibleDevices:"0")
- **Component:** `controllers/model_controller.go`, AMD k8s-device-plugin config
- **Problem:** On nodes with both iGPU and dGPU, some pod recreations land on the iGPU (AMD Radeon Graphics, 31GB shared) instead of the dGPU (Radeon RX 7900 XTX, 24GB).
- **Fix:**
  1. Investigate AMD k8s-device-plugin configuration
  2. Exclude iGPU from device plugin's device list via `renderD*` device filtering
  3. Check `/sys/class/drm/card*/device/vendor` and renderD device assignments
  4. Update device plugin DaemonSet config to filter by PCI device ID
- **Acceptance criteria:**
  - `amd.com/gpu` resource requests always schedule to dGPU
  - iGPU is not visible as an allocatable resource
  - Pod logs consistently show "Radeon RX 7900 XTX, 24.0 GB"
- **Test/verification:** Delete and recreate image gen pod 5 times; verify all land on dGPU.
- **Rollback:** Revert device plugin config to expose all GPUs.
- **Effort:** M (3 points).

---

## Backlog Conversion

| Debt ID | GitLab Issue | Title | Wave | Status |
|---|---|---|---|---|
| DEBT-001 | [#15](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/15) | Fix ConfigBool string handling | 1 | done (`2c6bd04`) |
| DEBT-002 | [#14](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/14) (comment added) | Immutable image tags in CI/GitOps | 1 | done (`892f3e6`) |
| DEBT-004 | [#12](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/12) | Proxy alias resolution | — | done (`dd15221`) |
| DEBT-003 | [#16](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/16) | Pin diffusers base image + deps | 2 | done (`01f602b`) |
| DEBT-005 | [#17](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/17) | Controller update logging | 2 | done (`539060f`) |
| DEBT-006 | [#18](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/18) | Bundle fp16-fix VAE | 2 | done (`4c9df9b`) |
| DEBT-007 | [#19](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/19) | Unify Docker/containerd stores | 3 | done (`78c66da`) |
| DEBT-008 | [#20](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/20) | GPU device misidentification | 3 | done (`2e75cfb`) |

## Deferred / Not In Scope

- No items deferred. All 8 items are included across the three waves.
- DEBT-004 is already resolved and requires no further work.
