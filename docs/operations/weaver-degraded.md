# Runbook: Weaver shows "Model preflight: degraded"

**Audience**: Loom operators investigating a yellow banner on the HUD
WeaverPanel, the iOS WeaverScreen, or the VS Code Loom Weaver tree.

**Symptom**: `GET /api/weaver/status` returns
`{"degraded": true, "missing_models": [...]}`. The HUD shows a yellow
"Model preflight: degraded" section listing one or more model names that
the daemon expected to find on FlexInfer but couldn't.

The first weaver query against any of those models will return an HTTP
error from the FlexInfer proxy (typically `model not found`).

---

## Decision tree

```
                     ┌─ /api/weaver/status returns
                     │   degraded: true
                     ▼
   ┌───────────────────────────────────┐
   │ catalog_error non-empty?          │
   └──────────┬───────────────┬────────┘
              │               │
            yes              no
              │               │
              ▼               ▼
      §1 Catalog         §2 Catalog reachable
      unreachable        but model missing
```

## §1: Catalog unreachable (`catalog_error` is set)

**Cause**: the daemon could not reach FlexInfer's `/v1/models` at
preflight time.

**Investigate**:

```bash
# 1. Check the URL the daemon is using.
kubectl logs deploy/loomd -n loom-hub | grep -i flexinfer | head -10
# Look for: "weaver: FlexInfer health check failed" or the URL
# the daemon resolved.

# 2. Confirm the FlexInfer proxy is up.
kubectl get svc -n flexinfer-system flexinfer-proxy
kubectl get pods -n flexinfer-system -l app.kubernetes.io/name=flexinfer-proxy

# 3. From inside the cluster, hit /v1/models directly.
kubectl run --rm -i --restart=Never --image=curlimages/curl \
  -n flexinfer-system curl-test -- \
  curl -s -m 5 http://flexinfer-proxy.flexinfer-system.svc:8080/v1/models | head
```

**Common fixes**:

- Wrong URL in `loom-secrets/FLEXINFER_URL` → fix and rotate the secret,
  Flux will roll the daemon
- Proxy CrashLoop → investigate FlexInfer pod logs
- Network policy blocking the daemon namespace → check NetworkPolicy
  resources

## §2: Catalog reachable but configured models are missing

**Cause**: a configured model name (from `pkg/aimodels` defaults, an
`~/.config/loom/aimodel-roles.yaml` override, or `WEAVER_*_MODEL` env)
isn't advertised by FlexInfer.

**Three remediation paths** — pick one:

### Option A: deploy the missing model on FlexInfer

If the model genuinely should exist:

1. Edit (or add) the Model CR YAML in `services/flexinfer/deploy/models/`.
2. Add the model to `services/flexinfer/deploy/models/kustomization.yaml`.
3. Verify locally with `kubectl kustomize services/flexinfer/deploy/models | grep <model>`.
4. Open MR, merge, confirm Flux reconciles:
   ```bash
   flux reconcile source git flexinfer -n flux-system
   flux reconcile kustomization flexinfer-models -n flux-system
   kubectl get model <name> -n flexinfer-system  # wait for Phase=Ready
   ```
5. Restart daemon (or wait one preflight cycle) — banner clears.

### Option B: add a LiteLLM alias on an existing Ready model

If the model name is a stale alias for something that's already deployed:

1. Edit the Ready Model's spec — typically
   `services/flexinfer/deploy/models/fast-chat-7900xtx.yaml` for
   `qwen3-8b`-class needs.
2. Add the alias under `spec.litellm.aliases` (and optionally
   `spec.serviceLabels` for the Service).
3. Open MR, merge, Flux reconciles.
4. Verify the alias appears:
   ```bash
   kubectl get model qwen3-8b-fast-7900xtx -n flexinfer-system \
     -o jsonpath='{.spec.litellm.aliases}{"\n"}'
   ```
5. Restart daemon — banner clears.

### Option C: change the resolver to point at a Ready model

If the missing model isn't worth deploying:

```yaml
# ~/.config/loom/aimodel-roles.yaml
roles:
  weaver-subagent:
    primary: <a-model-that-is-Ready>
    fallbacks: [qwen3-8b]
```

Restart daemon. Banner clears at the next preflight cycle.

If the missing model came from `WEAVER_ROUTER_MODEL` /
`WEAVER_SUBAGENT_MODEL` env vars, unset or update them — env wins over
the resolver.

---

## Verifying the fix

```bash
# Hit the HUD endpoint:
curl -s http://hud.loom.flexinfer.ai/api/weaver/status | jq '{degraded, missing_models, ready_models}'
# Want: {"degraded": false, "missing_models": [], "ready_models": [...]}

# Or watch the daemon log on next preflight:
kubectl logs deploy/loomd -n loom-hub --tail=100 | grep "weaver: model preflight"
# Want: "weaver: model preflight ok ready_models=[...]"
```

## When it's safe to ignore

- **Soak deploys**: if the missing model is intentionally pending a
  cluster-side rollout, the banner is informational, not actionable.
  Weaver still serves queries against the ready models on the fallback
  chain.
- **Per-domain optional models**: domain-specific `model:` fields in
  `~/.config/loom/weaver-domains.yaml` that point at niche models are
  flagged as missing even if no caller routes to that domain. Drop the
  override or align it with what's deployed.

## Recently shipped (history)

- 2026-05-08 — preflight + degraded surface introduced via
  [loom-core!330](https://gitlab.flexinfer.ai/services/loom-core/-/merge_requests/330).
  Prior to that, missing models silently 404'd at first query.
- 2026-05-08 — `qwen3-8b` LiteLLM alias added on
  `qwen3-8b-fast-7900xtx` via
  [services/flexinfer!295](https://gitlab.flexinfer.ai/services/flexinfer/-/merge_requests/295).
  Resolves the most common "qwen3-8b missing" report.
