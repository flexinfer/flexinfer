# Harbor 401 incident — long-term followup plan

**Source:** [`.loom/100-incident-harbor-401-deployment-chain-2026-05-05.md`](100-incident-harbor-401-deployment-chain-2026-05-05.md)
**Status (2026-05-06 EOD):** #2 + #3 shipped to main. #4 drafted as MR (gated on manual mirror). #1 partially planned — blocked on a CI tag-scheme change.
**Date:** 2026-05-06

| # | Followup | Status | Reference |
|---|---|---|---|
| 1 | Flux Image Automation | **planned, blocked on CI tag scheme** — see updated section below | (this doc) |
| 2 | Stale-pin alert | **shipped** | [platform/gitops!89](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/89) — `loom-hub-image-drift` daily CronJob + `LoomHubImageDriftStale` alert |
| 3 | Robot expiry monitor | **shipped** | [platform/gitops!88](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/88) — `harbor-robot-expiry-check` daily CronJob + `HarborRobotAccountExpiringSoon` alert. **Prereq:** create `harbor-admin-creds` secret. |
| 4 | Runner image fallback | **drafted, gated** | [platform/gitops!87](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/87) (DRAFT) — gated on `crane copy` mirror to Harbor `library/buildkit`. |

## Followup 1 — Flux Image Automation (BIGGEST WIN, also biggest scope)

**Goal:** Replace the manual SHA-bump dance in `platform/gitops` with Flux's [Image Automation Controllers](https://fluxcd.io/flux/components/image/) so successful CI pushes auto-update the Kustomization.

**Why this is the highest-leverage fix:** every loom-core/custom-server/fi-mcp-gateway image change today requires (a) push to loom-core, (b) wait for CI, (c) hand-bump `newTag:` in `platform/gitops/clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml`, (d) hand-merge a gitops MR. The 2026-05-05 incident sat for ~2 days at step (c) because no human was watching. Image automation removes the human step entirely.

**STATUS UPDATE (2026-05-06):** the cluster *already* has Flux Image Automation infrastructure deployed for `flexinfer-site`, `streamslate-site`, and `fi-fhir`/`fi-fhir-ui`. Look at:

```
platform/gitops/k3s/flux/image-automation/
  ├── harbor-registry-secret.enc.yaml   ← Harbor creds for Flux (SOPS, exists)
  ├── harbor-registry-ca.yaml           ← Harbor CA (exists)
  ├── flexinfer-site-image{repository,policy,updateautomation}.yaml
  ├── streamslate-site-image{repository,policy,updateautomation}.yaml
  ├── fi-fhir-image{repository,policy,updateautomation}.yaml
  └── PLAN.md                           ← original setup plan
```

So the Harbor creds + CA work is **already done**. What's missing for loom-core is:

### Real blocker: CI tag scheme

The existing Image Automation policies use `^[0-9]{8}-[0-9]{6}$` (timestamp `YYYYMMDD-HHMMSS`) and select the alphabetically-latest tag. **loom-core CI does not push timestamp tags** — only `$CI_COMMIT_SHORT_SHA` (8-char hex) plus `:latest` on main (see `services/loom-core/scripts/ci/buildkit-build.sh:99-105`).

8-char hex SHAs cannot be sorted by recency in any meaningful way. ImagePolicy options:

| Approach | Pro | Con |
|---|---|---|
| **A. Add timestamp tag in CI** (recommended) | Reuses existing flux-system pattern verbatim; ~1-line CI change | Touches `services/loom-core` CI, not just gitops |
| **B. Switch to `:latest` + image digest tracking** | No CI change | `imagePullPolicy: Always` already lands the image, but Flux can't pin to a specific digest in the manifest, so deployments serve "whichever digest is current" — bad for reproducibility |
| **C. ImagePolicy + `extract` regex on tag content** | No CI change | Hex SHAs have no usable structure; can't extract a sortable timestamp |
| **D. Annotate-pushed-images with `org.opencontainers.image.created`** | Cleanest semantic | Flux ImagePolicy doesn't sort by manifest annotations, only by tag string |

**Recommended:** Option A. CI change in `services/loom-core/scripts/ci/buildkit-build.sh`:

```diff
   IMAGE_NAMES="$IMAGE:$CI_COMMIT_SHORT_SHA"
+  TIMESTAMP_TAG=$(date -u +%Y%m%d-%H%M%S)
+  IMAGE_NAMES="$IMAGE_NAMES,$IMAGE:$TIMESTAMP_TAG"
   if [ "$CI_COMMIT_BRANCH" = "$CI_DEFAULT_BRANCH" ]; then
     IMAGE_NAMES="$IMAGE_NAMES,$IMAGE:latest"
   fi
```

After that lands and one image is pushed with a timestamp tag, the gitops MR adding loom-core's Image{Repository,Policy,UpdateAutomation} CRDs becomes a 5-minute copy-paste from the existing flexinfer-site set, not the original "spec from scratch" effort.

### Open prerequisites (for the writeback path)

The existing `flexinfer-site-imageupdateautomation.yaml` writes back to the **`flexinfer-site` GitRepository** (the source repo), not the gitops repo. For loom-core, that means writeback would target `services/loom-core/k8s/base/servers/*/deployment.yaml` — a deploy key with write scope on `services/loom-core` is needed. Or, alternative: write back to `platform/gitops` and update the kustomization image override there. Either works; pick whichever matches the existing flexinfer-site pattern for consistency.

### Required CRDs (per image)

```yaml
# clusters/k3s/flux-system/imagerepositories.yaml
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImageRepository
metadata:
  name: loom-core
  namespace: flux-system
spec:
  image: registry.harbor.lan/mcp/loom-core
  interval: 5m
  secretRef:
    name: harbor-creds-flux
  certSecretRef:
    name: harbor-ca

---
apiVersion: image.toolkit.fluxcd.io/v1beta2
kind: ImagePolicy
metadata:
  name: loom-core
  namespace: flux-system
spec:
  imageRepositoryRef:
    name: loom-core
  filterTags:
    pattern: '^[0-9a-f]{8}$'        # 8-char short SHA from CI
  policy:
    alphabetical:
      order: asc                    # newest CI push wins because timestamps are monotonic in the registry
```

Repeat for `custom-server` and `fi-mcp-gateway`.

### ImageUpdateAutomation (writeback)

```yaml
# clusters/k3s/flux-system/imageupdateautomation-loom-hub.yaml
apiVersion: image.toolkit.fluxcd.io/v1beta1
kind: ImageUpdateAutomation
metadata:
  name: loom-hub-images
  namespace: flux-system
spec:
  interval: 5m
  sourceRef:
    kind: GitRepository
    name: gitops-gitlab
  git:
    checkout:
      ref:
        branch: main
    commit:
      author:
        email: flux@flexinfer.ai
        name: flux
      messageTemplate: |
        chore(loom-hub): bump {{ range .Updated.Images }}{{ .Name }} to {{ .NewTag }}{{ end }} (auto)
    push:
      branch: main
  update:
    path: ./clusters/k3s/flux-system
    strategy: Setters
```

### Markers in the Kustomization

```yaml
images:
  - name: registry.harbor.lan/mcp/loom-core
    newTag: "28b64b65" # {"$imagepolicy": "flux-system:loom-core:tag"}
```

### Open prerequisites

1. **Harbor pull credential for image-reflector-controller.** The `harbor-creds` secret is in `ci-jobs`/`ci-build`/`ai`. Need to create one in `flux-system`. Generate via:
   ```sh
   kubectl create secret docker-registry harbor-creds-flux -n flux-system \
     --docker-server=registry.harbor.lan \
     --docker-username='robot$k3s' \
     --docker-password='<from-Harbor-UI>'
   ```
2. **GitOps writeback identity.** The image-automation-controller commits to `platform/gitops` main. It needs:
   - SSH deploy key with **write** scope on the gitops project (currently the `gitops-gitlab` GitRepository is read-only).
   - Or a GitLab access token with `write_repository` scope, mounted as a secret.
   - Recommend: dedicated `flux-image-bot@flexinfer.ai` user/PAT to keep audit trail clean.
3. **Branch protection bypass.** If `main` is push-protected on `platform/gitops`, the bot account needs an exemption, otherwise commits get rejected.

### Risk + rollback

- Rollback is simple: delete the `ImageUpdateAutomation` CR and pin the `newTag` manually.
- The Kustomization can keep its current pin even with the magic comment present — the comment is opt-in.
- Test on a single low-stakes deployment first (suggest custom-server which already has `imagePullPolicy: Always` from another change).

### Effort

- Spec + first CR set: 30 min
- Harbor secret + deploy key: 30 min (mostly UI)
- Test rollout in staging: 1 h
- Full rollout to all loom-hub deployments: 30 min
- **Total: ~2-3 h focused session.**

## Followup 2 — Stale-pin alert ✅ SHIPPED

**Status:** [`platform/gitops!89`](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/89) merged. Manifests at `k3s/monitoring/loom-hub-image-drift/`.

**What shipped:** Daily CronJob (10:00 UTC) compares each loom-hub Deployment's container image short-SHA against the loom-core Flux GitRepository HEAD revision. Alerts when a deployment is on a stale SHA AND Flux has been on the new revision for >24h. Self-contained design — no Pushgateway, no exporter, uses existing kube_job_status_failed alerting pipeline.

**Original design notes (kept for context):**



**Goal:** Detect when a deployed pod's image SHA disagrees with the SHA pinned in the Flux Kustomization. The 2026-05-05 incident had `loom-core:9c5d8fa2` running while the source repo had moved well past that tag — but Flux happily reported "Applied revision: ✓" because the Kustomization itself hadn't been updated.

**Once Followup 1 ships, the symptom this alert catches becomes much rarer (no human in the bump loop). But it remains useful as a defense-in-depth signal for Image Automation failures (Harbor unreachable, RBAC change, etc.).**

### Architecture options

**Option A — Custom exporter (cleanest):**
- Small Go/Python service that runs as a Deployment in `flux-system`
- Watches Kubernetes Deployments + reads Flux Kustomization specs
- Exports `pinned_image_age_seconds{namespace, deployment, image}` and `pinned_image_drift_seconds`
- ServiceMonitor scrapes; PrometheusRule alerts on `pinned_image_age_seconds > 86400` AND `image_repository_latest_tag` differs

**Option B — kubectl-based PromQL via kube-state-metrics:**
- `kube_pod_container_info` already exposes `image_id` (immutable digest) and `image` (tag)
- Compare against ImageRepository status (which Flux exports as `flux_image_automation_*` metrics) — no custom exporter needed, just PromQL
- Limit: only works once Followup 1 ships (no ImageRepository CR otherwise)

**Option C — Harbor + git-state CronJob:**
- Daily CronJob: query Harbor for latest tag matching `^[0-9a-f]{8}$` per image, read pinned tag from gitops, compute drift
- Push metric to Pushgateway
- Simplest to ship without Followup 1

### Recommended path

After Followup 1: **Option B** (free metrics + PromQL alert).
Without Followup 1: **Option C** (one Python script + CronJob, ~150 lines).

### Effort

- Option B: 1 h (PromQL + alert rule + Slack route)
- Option C: 2-3 h (script + CronJob + Pushgateway scrape config)

## Followup 3 — Harbor robot account expiry monitor ✅ SHIPPED

**Status:** [`platform/gitops!88`](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/88) merged. Manifests at `k3s/monitoring/harbor-robot-expiry/`.

**What shipped:** Daily CronJob (09:00 UTC) queries `/api/v2.0/robots`, exits non-zero if any robot expires within 14 days. PrometheusRule `HarborRobotAccountExpiringSoon` fires on Job failure. **Prereq:** create `harbor-admin-creds` Secret in `monitoring` namespace — see `k3s/monitoring/harbor-robot-expiry/README.md`.

**Original design notes (kept for context):**



**Goal:** Surface impending Harbor robot-account expiry 14 days ahead so we can rotate before the next 401 incident.

### Implementation sketch

```python
# ~/workspace/bin/harbor-robot-expiry-check.py (or k8s CronJob)
import requests, datetime, sys, os
HARBOR = 'https://registry.harbor.lan'
ADMIN_USER = os.environ['HARBOR_ADMIN_USER']
ADMIN_PASS = os.environ['HARBOR_ADMIN_PASS']
WARN_DAYS = 14

resp = requests.get(f'{HARBOR}/api/v2.0/robots',
                    auth=(ADMIN_USER, ADMIN_PASS), verify=False)
now = datetime.datetime.utcnow()
warned = []
for r in resp.json():
    if not r['expires_at'] or r['expires_at'] < 0:
        continue
    expires = datetime.datetime.utcfromtimestamp(r['expires_at'])
    days = (expires - now).days
    if days < WARN_DAYS:
        warned.append((r['name'], days, expires.isoformat()))

if warned:
    # Post to Alertmanager API or send to Slack
    ...
    sys.exit(1)
```

### Deployment

- CronJob in `monitoring` namespace, daily at 09:00
- Secret with admin credentials (rotate quarterly)
- Output → Alertmanager via webhook OR direct Slack

### Effort

- ~1 h (script + CronJob manifest + secret + test)

## Followup 4 — Runner image fallback 🟡 DRAFT (gated)

**Status:** [`platform/gitops!87`](https://gitlab.flexinfer.ai/platform/gitops/-/merge_requests/87) is a DRAFT — gated on the prerequisite `crane copy` mirror. The gitops change itself is trivial (2-line image path swap in helmrelease-runner.yaml + helmrelease-runner-overflow.yaml). What's missing is a one-time mirror operation that requires push permission on Harbor `library/`.

**Manual mirror command** (run from a host with `crane` + Harbor DNS + push perms):

```sh
crane copy \
  registry.harbor.lan/dockerhub-cache/moby/buildkit:v0.12.5 \
  registry.harbor.lan/library/buildkit:v0.12.5
```

After mirror lands, mark MR !87 ready and merge. Then:

```sh
KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml \
  kubectl rollout restart deploy/gitlab-runner -n ci
KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml \
  kubectl rollout restart deploy/gitlab-runner-overflow -n ci
```

**Original design notes (kept for context):**



**Goal:** Eliminate `dockerhub-cache/moby/buildkit` as a single point of failure for CI startup. When that image's pull-through cache fails (the actual 2026-05-05 trigger), every CI build on the cluster cascades to red.

### Plan

1. Mirror the canonical buildkit image into Harbor's `library/` namespace (no proxy-cache dependency):
   ```sh
   crane copy \
     registry.harbor.lan/dockerhub-cache/moby/buildkit:v0.12.5 \
     registry.harbor.lan/library/buildkit:v0.12.5
   ```
   Requires push permission on `library/`. The `robot$k3s` account currently used for CI pulls may not have `library/` push — check Harbor UI > Projects > library > Robot Accounts.
   Worth pre-creating tags for several BuildKit versions (`v0.12.5`, current LTS) so we have rollback choices.
2. Update `~/workspace/platform/gitops/k3s/ci/gitlab/helmrelease-runner.yaml:189` and `helmrelease-runner-overflow.yaml:153`:
   ```diff
   - image = "registry.harbor.lan/dockerhub-cache/moby/buildkit:v0.12.5"
   + image = "registry.harbor.lan/library/buildkit:v0.12.5"
   ```
3. Merge the gitops MR; Flux reconciles the Helm release; new runner pods pull from `library/` (no proxy hop).

### Verification

- Existing buildkit pods stay on cached image until restart — restart the runner deployment to force a re-pull.
- Run a known-good CI pipeline (e.g., a docs-only commit on loom-core) and confirm `build:image:*` jobs come up without the previous 401.

### Risk + rollback

- Rolling back is one revert commit; no data migration.
- The `helper_image: registry.harbor.lan/library/gitlab-runner-helper:x86_64-v18.6.2` already follows the `library/` pattern — this followup just brings the build container in line with the same convention.

### Effort

- Image mirror: 5 min (assuming push perms exist)
- Gitops MR + reconcile: 15 min
- Verification: 30 min (kick a real pipeline, watch logs)
- **Total: ~1 h.**

## Cross-cutting prerequisites

| Prereq | Followups that need it | Status |
|---|---|---|
| Harbor admin UI access (read robot accounts, edit dockerhub-cache project) | #1, #3 | Manual |
| Harbor `library/` push permission for `robot$k3s` (or another robot) | #4 | Unknown — verify in UI |
| GitLab deploy key with `write_repository` scope on `platform/gitops` | #1 | Needs creation |
| `flux-system` namespace `harbor-creds-flux` secret | #1 | Needs creation |
| Alertmanager webhook receiver for incidental alerts | #2, #3 | Already configured (existing rules use it) |

## Updated sequencing (2026-05-06 EOD)

| # | State | Next action |
|---|---|---|
| 4 | DRAFT MR | User: run `crane copy` to mirror buildkit, mark MR !87 ready, merge, restart runners |
| 3 | SHIPPED | User: create `harbor-admin-creds` secret in `monitoring` namespace |
| 2 | SHIPPED | None (defense-in-depth, will activate within 24h of any drift) |
| 1 | PARTIAL PLAN | Add timestamp tag to loom-core CI (1-line change in `scripts/ci/buildkit-build.sh`), then copy flexinfer-site Image* CRDs across to loom-core. Or skip if #2 + manual bumps are sufficient ongoing. |

## Sources

- [Flux Image Automation docs](https://fluxcd.io/flux/components/image/)
- [`platform/gitops/k3s/ci/gitlab/helmrelease-runner.yaml`](../../../platform/gitops/k3s/ci/gitlab/helmrelease-runner.yaml) — current buildkit image references at line 189
- [`platform/gitops/clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml`](../../../platform/gitops/clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml) — current manual `newTag:` pin
- [`.loom/100-incident-harbor-401-deployment-chain-2026-05-05.md`](100-incident-harbor-401-deployment-chain-2026-05-05.md) — original incident
