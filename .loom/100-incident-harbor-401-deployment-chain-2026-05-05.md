# Incident: Harbor 401 blocks loom-core image builds + silent stale rollouts

**Date:** 2026-05-05
**Status:** Partially mitigated. Runbook + handoff for ops.
**Affected surfaces:** `hud.flexinfer.ai` (deployed mobile-hud), all CI image builds in `services/loom-core`, ~12 other namespaces with `ImagePullBackOff` pods.

## TL;DR

Recent merges to `services/loom-core` main (![286](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/286), ![287](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/287), ![288](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/288), ![289](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/289), ![290](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/290), ![291](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/291), ![292](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/292)) did not reach the deployed HUD because of two compounding issues:

1. **Long-term: silent stale rollouts.** The `mobile-hud` Deployment used `image: …loom-core:latest` + `imagePullPolicy: IfNotPresent`. SHA bumps in the gitops Kustomization updated the manifest but k3s nodes reused the cached layer for `:latest`, so the on-cluster image silently disagreed with the gitops manifest. ✅ **Fixed by [![293](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/293)](../-/merge_requests/293)** — flipped to `imagePullPolicy: Always`. Verified live.
2. **Active blocker: Harbor 401 on every CI image build.** GitLab Runner pods can't pull their own buildkit base image (`registry.harbor.lan/dockerhub-cache/moby/buildkit:v0.12.5`) because Harbor returns `401 Unauthorized`. No new images get built or pushed → no SHA bump → deployment stays on `loom-core:9c5d8fa2` (2026-05-05 03:43). 🟡 **Needs ops UI access on Harbor.**

This doc is the runbook for (2) plus a triage record so a future incident can short-circuit the diagnosis.

## Symptoms

| Where | Observation |
|---|---|
| `hud.flexinfer.ai` | UI shows `Mills Awaiting first snapshot`, `Token Economics: INSUFFICIENT DATA / STUB`, no recent improvements |
| `kubectl get deploy -n loom-hub mobile-hud -o jsonpath='{.spec.template.spec.containers[0].image}'` | `registry.harbor.lan/mcp/loom-core:9c5d8fa2` (stale; main HEAD has moved well past) |
| `kubectl get pods -n loom-hub` | mix of `loom-core:b2a543b1` (started 2026-05-04 19:21) + `loom-core:9c5d8fa2` (started 2026-05-05 03:43) — drift! |
| `glab -R services/loom-core ci status -b main` | latest pipeline 8128 has `build:image:loom-core` + `build:image:loom-mills-operator` + `build:image:custom-server` all **failed** at prepare step |
| Cluster-wide | Many `ImagePullBackOff` pods across `ai`, `ci`, `crm`, `daemon`, `loom-hub`, `devbox` namespaces; pattern points at registry-side failure |

## Diagnosis (verified by curl-from-cluster + secret inspection)

### Harbor itself is healthy

```
$ kubectl run -n default harbor-probe --rm -i --restart=Never \
    --image=alpine/curl:latest --command -- /bin/sh -c \
    'curl -sSk -o /dev/null -w "%{http_code}\n" https://registry.harbor.lan/api/v2.0/health'
200
```

### The harbor-creds secret has VALID credentials

```
$ kubectl get secret -n ci-jobs harbor-creds -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d
{"auths":{"registry.harbor.lan":{"username":"robot$k3s","password":"…","email":"none","auth":"…"}}}

$ kubectl run -n default harbor-probe --rm -i --restart=Never \
    --image=alpine/curl:latest --command -- /bin/sh -c \
    'curl -sSk -o /dev/null -w "%{http_code}\n" \
       -u "robot$k3s:<password>" \
       "https://registry.harbor.lan/v2/dockerhub-cache/moby/buildkit/manifests/v0.12.5"'
200
```

So: Harbor is up, the credentials in `ci-jobs/harbor-creds` work for the failing image path. **The 401 is happening at kubelet pull time on the runner pod, not at the credential level.**

### CI runner sees 401 anyway

```
# pipeline 8127 / job 88718 (build:image:loom-core), trace tail:
WARNING: Failed to pull image "registry.harbor.lan/dockerhub-cache/moby/buildkit:v0.12.5" \
  for container "build" with policy "IfNotPresent": image pull failed: \
  failed to resolve reference "registry.harbor.lan/dockerhub-cache/moby/buildkit:v0.12.5": \
  unexpected status from HEAD request to \
  https://registry.harbor.lan/v2/dockerhub-cache/moby/buildkit/manifests/v0.12.5: 401 Unauthorized
```

The GitLab runner config (`kubectl get configmap -n ci gitlab-runner -o yaml`) **does** declare `image_pull_secrets = ["harbor-creds"]` in the kubernetes runner section, and a `host_aliases` entry for `192.168.50.230 registry.harbor.lan`. So the secret should be applied.

### Most likely root causes (in order of likelihood)

1. **Harbor robot account `robot$k3s` lost permissions on the `dockerhub-cache` project after Harbor's restart at 17:18:48Z (2026-05-05).** The Harbor VM was re-created (per `kubectl get vmi -n default harbor`); robot account permissions on proxy-cache projects sometimes drop on Harbor reinit if the project's RBAC isn't part of the persistent state.
2. **Harbor's pull-through proxy upstream credentials to Docker Hub expired/were revoked.** When Harbor's cache misses on a manifest, it tries to revalidate with Docker Hub. If those creds 401, Harbor surfaces 401 to the client even when the client's own auth is valid.
3. **Runner pod's `imagePullSecrets` injection silently failed.** Less likely (config looks right) but possible if a cert mount or runner version regression broke secret injection.

The fact that **a curl with the same creds returns 200** for the same path strongly favors #2: the cache hit path works, but the runner triggers a cache-miss revalidation that 401s upstream. To confirm, check Harbor logs for the upstream 401.

## Runbook (ops UI access required)

### Step 1 — Check Harbor robot account permission

1. Log in to `https://registry.harbor.lan` as admin.
2. Projects → `dockerhub-cache` → Robot Accounts.
3. Confirm `robot$k3s` (or whatever name matches the secret) is **enabled**, has **`pull` permission**, and the **expiry date is in the future**.
4. If it's expired/missing/disabled, recreate it. Then update the `harbor-creds` Secret in **all** namespaces that consume it (see "secret rotation" below).

### Step 2 — Verify the upstream Docker Hub credentials in the proxy cache

1. Projects → `dockerhub-cache` → Configuration → Registry Endpoints.
2. Click "Test Endpoint". If it fails with 401: re-enter the Docker Hub access token. If MFA is enabled on the Docker Hub account, you must use a Personal Access Token, not the password.
3. After fixing, **purge a cached manifest** to test the path:
   ```
   curl -sSk -X DELETE -u "<admin>:<pw>" \
     "https://registry.harbor.lan/v2/dockerhub-cache/moby/buildkit/manifests/sha256:<digest>"
   ```
   Then have CI retry — the cache miss should now succeed.

### Step 3 — Retry the failed CI builds

```
glab -R services/loom-core api projects/services%2Floom-core/pipelines/8128/retry -X POST
glab -R services/loom-core ci status -b main
```

Wait for `build:image:loom-core` (and `build:image:loom-mills-operator`, `build:image:custom-server`) to go green.

### Step 4 — Bump the gitops Kustomization SHA

Once the new image is in Harbor (verify via `curl … /v2/mcp/loom-core/manifests/<new-sha>` returning 200), bump the platform/gitops pin:

```
cd ~/workspace/platform/gitops
NEW_SHA=$(git -C ../services/loom-core rev-parse --short=8 origin/main)
sed -i "s/newTag: \"9c5d8fa2\"  # loom-core/newTag: \"$NEW_SHA\"  # loom-core/" \
  clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml
git commit -am "chore(loom-hub): bump loom-core to $NEW_SHA"
git push
```

Flux reconciles within ~2 min. With ![293](../-/merge_requests/293)'s `imagePullPolicy: Always` now live, pods do a fresh pull on rollout.

### Step 5 — Verify

```
# After Flux reconciles, check the deployed image:
KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml kubectl get deploy \
  -n loom-hub mobile-hud \
  -o jsonpath='{.spec.template.spec.containers[0].image}'

# Expected: registry.harbor.lan/mcp/loom-core:<new-sha>

# Watch the rollout:
KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml kubectl rollout status \
  -n loom-hub deployment/mobile-hud --timeout=5m

# Confirm pods are on the new SHA:
KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml kubectl get pods -n loom-hub \
  -l app=mobile-hud \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[0].image}{"\n"}{end}'
```

Then load `https://hud.flexinfer.ai` and verify:
- Token Economics card shows numbers (was "INSUFFICIENT DATA" — fixed by ![287](../-/merge_requests/287))
- Live Agents table column doesn't garble (was overflowing 130px — fixed by ![285](../-/merge_requests/285))
- "1 degraded" gitlab server flap stops (was `daemon callLock` saturation — fixed by ![289](../-/merge_requests/289))
- Activity feed surfaces session.start / session.end events (after end-to-end smoke per ![290](../-/merge_requests/290))

## Secret rotation cheat-sheet

Harbor robot account password lives in:

| Namespace | Secret | Notes |
|---|---|---|
| `ci-jobs` | `harbor-creds` | GitLab runner pods (build:image:* jobs) |
| `ci-build` | `harbor-creds` + `harbor-ca` | Buildkit cache |
| `ai` | `harbor-creds` | AI workloads |

Rotate with:
```
NEW_PW='<copy from Harbor UI after creating new robot token>'
USERNAME='robot$k3s'
DOCKERCONFIG=$(printf '{"auths":{"registry.harbor.lan":{"username":"%s","password":"%s","email":"none","auth":"%s"}}}' \
  "$USERNAME" "$NEW_PW" "$(printf '%s:%s' "$USERNAME" "$NEW_PW" | base64)")

for NS in ci-jobs ci-build ai; do
  kubectl create secret docker-registry harbor-creds \
    --docker-server=registry.harbor.lan \
    --docker-username="$USERNAME" \
    --docker-password="$NEW_PW" \
    --dry-run=client -o yaml \
    | kubectl apply -n "$NS" -f -
done
```

(SOPS-encrypted source-of-truth lives in `platform/gitops/k3s/<ns>/sealed-secrets/` — for durability, encrypt the new value via SOPS and commit there too. Direct `kubectl apply` is for incident-mitigation speed; reconcile back to GitOps after.)

## Long-term followups (not blockers)

1. **Flux Image Automation.** Replace the manual SHA-bump-in-platform/gitops dance with [`ImageRepository`](https://fluxcd.io/flux/components/image/imagerepositories/) + [`ImagePolicy`](https://fluxcd.io/flux/components/image/imagepolicies/) + [`ImageUpdateAutomation`](https://fluxcd.io/flux/components/image/imageupdateautomations/) CRDs so successful CI pushes auto-bump the kustomization. Eliminates the human-in-the-loop step.
2. **Alert on stale `:latest` resolution.** Prometheus rule: alert when on-cluster image SHA doesn't match the pinned SHA in the Flux Kustomization. Would have caught this incident weeks ago.
3. **Harbor robot account expiry monitor.** Cron + Harbor API to alert 14 days before any robot token expires. Robot account expiry is a known Harbor footgun.
4. **CI runner image fallback.** Pre-pull `moby/buildkit:v0.12.5` into a non-proxy Harbor project (e.g., `library/buildkit`) and update the runner config to prefer that. Removes the dockerhub-cache failure mode from the critical CI startup path.

## Sources

- ![293](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/293) — `chore(k8s): set imagePullPolicy: Always on mobile-hud` (long-term fix, merged + reconciled)
- `k8s/base/servers/mobile-hud/deployment.yaml:24-32` — current image + pull policy
- `platform/gitops/clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml:26-28` — Kustomization image override (currently `9c5d8fa2`)
- GitLab runner ConfigMap: `kubectl get configmap -n ci gitlab-runner -o yaml | yq .data.\"config.template.toml\"`
- Pipeline 8128 (latest main, blocked): https://gitlab.flexinfer.ai/services/loom-core/-/pipelines/8128
- Failed job trace: https://gitlab.flexinfer.ai/services/loom-core/-/jobs/88867 (`build:image:loom-core`)
- Harvester VM `harbor` reinit: `kubectl get vmi -n default harbor` (2026-05-05T17:18:48Z)
