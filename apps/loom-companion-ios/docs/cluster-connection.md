# Connecting Loom Companion to the Cluster HUD

The iOS companion connects to a Loom HUD instance — either a local `loomd` process or the cluster-hosted **mobile-hud** deployment at `hud.flexinfer.ai`. Features that depend on external services (GitLab pipelines, session graph, memory tiers) work reliably when pointed at the cluster HUD because it's the instance with the MCP servers, secrets, and tokens wired in via GitOps.

## Why Gateway mode, not LAN

| Mode | Talks to | Pipelines | Memory | Spawn | Offline-ready |
|---|---|---|---|---|---|
| **LAN** | Your local `loomd` (`127.0.0.1:3333`) | Only if `HUD_PIPELINE_PROJECTS` + `GITLAB_PERSONAL_ACCESS_TOKEN` are set locally | Only if your MCP servers are running | No | Yes, while on your network |
| **Gateway** | Cluster `mobile-hud` via `hud.flexinfer.ai` | ✅ Auto-discovered from GitLab | ✅ Cluster-backed | ✅ Cluster-backed | Anywhere with internet |

**Default to Gateway.** LAN is useful during local development of the HUD itself, not day-to-day operator use.

## Prerequisites

All three secrets are stored centrally in the `loom-secrets` Kubernetes secret and should be provisioned into your operator profile:

- `HUD_MOBILE_OPERATOR_TOKEN` — bearer token the app sends as `Authorization: Bearer <token>`.
- `CF_ACCESS_CLIENT_ID` — Cloudflare Access service-token ID.
- `CF_ACCESS_CLIENT_SECRET` — Cloudflare Access service-token secret.

Fetch them from the cluster (operator machine with kubectl access):

```bash
kubectl -n loom-hub get secret loom-secrets \
  -o jsonpath='{.data.HUD_MOBILE_OPERATOR_TOKEN}' | base64 -d; echo
kubectl -n loom-hub get secret loom-secrets \
  -o jsonpath='{.data.CF_ACCESS_CLIENT_ID}' | base64 -d; echo
kubectl -n loom-hub get secret loom-secrets \
  -o jsonpath='{.data.CF_ACCESS_CLIENT_SECRET}' | base64 -d; echo
```

## Configure the app

1. Launch Loom Companion.
2. On the **Connect** screen, switch the picker from **LAN** to **Gateway**.
3. Fill in:
   - **Server URL**: `https://hud.flexinfer.ai`
   - **Bearer Token**: `HUD_MOBILE_OPERATOR_TOKEN` value
   - **Cloudflare Access Client ID**: `CF_ACCESS_CLIENT_ID` value
   - **Cloudflare Access Client Secret**: `CF_ACCESS_CLIENT_SECRET` value
4. Tap **Connect**.

The profile is stored in the iOS keychain; you don't need to re-enter credentials on subsequent launches.

## What works in Gateway mode

Everything the cluster mobile-hud exposes is available:

- **Dashboard health** — control plane, agents, sessions, timeline.
- **Pipelines** — auto-discovered from the GitLab MCP server at startup; no client-side config required (see [auto-discovery](#how-pipeline-auto-discovery-works) below).
- **Tasks / Workflows / Approvals** — backed by the cluster agent-context store.
- **Deep links** — `loom://` URLs resolve against the same cluster data.
- **Spawn / remote execution** — runs in the cluster `devbox` namespace.

## How pipeline auto-discovery works

When the cluster mobile-hud starts, it goes through this chain to decide which GitLab projects to poll:

1. `HUD_PIPELINE_PROJECTS` env var — explicit override (comma-separated `path_with_namespace` values). Left unset in production so the list doesn't drift.
2. `codebase.DetectPipelineProject` — checks the daemon's working directory for a git remote. N/A inside the container.
3. `AgentBridge.ListPipelineProjects` — calls `gitlab__list_projects` through the MCP daemon and enumerates every project the token has `membership` on. Paginated to 100/page, up to 500 projects by default, deduped and sorted.

The third step is what makes it "works out of the box" — as long as:

- The `gitlab` MCP server is reachable from the mobile-hud daemon, **and**
- `GITLAB_PERSONAL_ACCESS_TOKEN` has `read_api` scope for the projects you care about

…pipelines appear without any deployment edit. Adding a new repo to GitLab surfaces it on the dashboard at the next mobile-hud restart.

### Debugging "pipelines unavailable"

If the Work → Pipelines peek reports "not available" even in Gateway mode:

```bash
# 1. Check the pod is up.
kubectl -n loom-hub get pods -l app=mobile-hud

# 2. Look for the auto-discovery log line.
kubectl -n loom-hub logs deploy/mobile-hud -c loomd | \
  grep -iE 'auto-discover|pipeline project'

# Expected, when things are working:
#   "auto-discovered pipeline projects via gitlab MCP" count=42

# 3. When it says "auto-discovery unavailable", the reason is logged — usually
#    either the gitlab MCP server is down or the PAT secret is missing.
kubectl -n loom-hub get secret loom-secrets \
  -o jsonpath='{.data.GITLAB_PERSONAL_ACCESS_TOKEN}' | base64 -d | wc -c
# Should print > 0

kubectl -n loom-hub get pods -l mcp.server/name=gitlab
# Should show 1/1 Running
```

If you need to pin a subset for any reason (e.g. rate-limited access), set the env var on the deployment:

```bash
kubectl -n loom-hub set env deploy/mobile-hud \
  HUD_PIPELINE_PROJECTS="services/loom-core,platform/gitops"
```

…and follow up with a proper GitOps commit to `k8s/base/servers/mobile-hud/deployment.yaml` so the setting survives reconciliation.

## References

- Deployment: [`k8s/base/servers/mobile-hud/deployment.yaml`](../../../k8s/base/servers/mobile-hud/deployment.yaml)
- GitLab MCP server: [`k8s/base/servers/gitlab/deployment.yaml`](../../../k8s/base/servers/gitlab/deployment.yaml)
- Flux kustomization (image tags): `platform/gitops/clusters/k3s/flux-system/kustomization-loom-hub-servers.yaml`
- Pipeline monitor source: [`internal/hud/monitor/pipeline.go`](../../../internal/hud/monitor/pipeline.go)
- Auto-discovery source: [`internal/hud/embed.go`](../../../internal/hud/embed.go) (search for "auto-discovered pipeline projects")
- AgentBridge listing: [`internal/hud/bridge/agent_pipeline.go`](../../../internal/hud/bridge/agent_pipeline.go) → `ListPipelineProjects`
