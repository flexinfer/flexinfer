---
title: Proxy Observability
description: Dashboards and alerts for the proxy graceful-shutdown drain contract and label-group least-loaded routing.
---

# Proxy Observability

The FlexInfer activator proxy ships a Grafana dashboard and Prometheus alerts
covering two recently added subsystems:

- **Graceful shutdown / drain** — the proxy drains in-flight requests on
  SIGTERM before exiting, bounded by a deadline (default 10m).
- **Label-group routing** — requests to a shared label group are steered to a
  single member using least-loaded or consistent-hash strategies.

This page describes what each panel and alert means, and the first-response
steps for the shutdown-timeout alert. For how routing is *configured*, see
[Request Routing](routing.md); for general proxy behavior, see [Proxy](proxy.md).

## Where to look

| Surface | Name |
|---------|------|
| Grafana dashboard | **FlexInfer Proxy Operations** (uid `flexinfer-proxy`) |
| Dashboard rows | *Graceful Shutdown & Drain*, *Label-Group Routing* |
| Prometheus rule group | `flexinfer-proxy` |

The dashboard is delivered as a ConfigMap
(`charts/flexinfer/templates/grafana-dashboard-proxy.yaml`, gated on
`grafanaDashboard.enabled`). Alerts are delivered as a `PrometheusRule`
(`charts/flexinfer/templates/prometheusrule.yaml`, gated on `alerting.enabled`).

## Dashboard panels

### Graceful Shutdown & Drain

**Graceful Shutdown Events** — a rolling 15m count of
`flexinfer_proxy_shutdowns_total` broken out by `result`:

- `started` — a replica received SIGTERM and began draining.
- `completed` — the replica drained all in-flight requests before the deadline
  (rendered green).
- `timeout` — the drain deadline elapsed with requests still in flight; those
  requests were cut off (rendered red).

In a healthy rollout you expect matched `started` / `completed` pairs and zero
`timeout`.

**Shutdown Drain Duration** — p50 / p95 / p99 of
`flexinfer_proxy_shutdown_duration_seconds` (how long draining actually took).
Because drains are rare, the quantiles use a 1h window so a recent drain stays
visible. Thresholds turn the panel yellow at 480s and red at 600s — the 600s
(10m) mark is the default drain deadline, so a p99 climbing into that band means
replicas are about to start timing out.

### Label-Group Routing

**Route Decisions by Mode** — `flexinfer_proxy_label_group_route_decisions_total`
as a per-second rate, split by configured `strategy` and per-call `outcome`:

- `least_loaded` — the least-loaded member was picked.
- `hashed_prefix` / `hashed_session` — a consistent-hash pin on the request's
  prefix or session key.
- `default_rr`, `fallback_single`, `fallback_no_ready`, `fallback_no_key` —
  round-robin or a fallback path (only one ready member, no ready members, or no
  extractable routing key).

Use this to confirm the configured strategy is actually taking effect rather
than silently falling back to round-robin.

**Label-Group Target Hits** — `flexinfer_proxy_label_group_route_target_hits_total`
as a per-second rate, by `label` and selected `model`. This answers *who is
actually receiving shared-label traffic*. Persistent skew toward one member is
expected for hash strategies (sticky by key) but for `least-loaded` should track
inversely with load. This is the ROADMAP "watch per-Model active connections
plus label-group target hits" signal.

### Related existing panels

Per-model active connections are already shown by the **Active Connections**
stat panel (`flexinfer_proxy_active_connections`) in the *Proxy Operations* row —
read it alongside *Label-Group Target Hits* to judge whether least-loaded
routing is balancing connections as intended. No new active-connections panel
was added to avoid duplication.

## Alerts

Both alerts live in the `flexinfer-proxy` rule group.

### FlexInferProxyGracefulShutdownTimeout (warning)

```promql
increase(flexinfer_proxy_shutdowns_total{result="timeout"}[15m]) > 0
```

Fires when any proxy replica exceeds its graceful-shutdown drain deadline in the
last 15 minutes. A timeout means in-flight requests were terminated instead of
draining cleanly — clients saw dropped connections during a rollout, scale-down,
or node drain.

### FlexInferProxyDrainDurationHigh (warning)

```promql
histogram_quantile(0.99, sum by (le) (rate(flexinfer_proxy_shutdown_duration_seconds_bucket[1h]))) > 480
```

Fires (`for: 5m`) when p99 drain duration approaches the 600s (10m) deadline.
This is a leading indicator of the timeout alert: drains are still succeeding,
but only just. The 480s threshold is configurable via
`alerting.thresholds.proxyDrainSecondsWarn`.

## First response: shutdown-timeout alert

When `FlexInferProxyGracefulShutdownTimeout` fires:

1. **Identify the replica and when it happened.** Correlate the alert with the
   *Graceful Shutdown Events* panel (the `timeout` series) and the proxy pod's
   restart / termination time:

   ```bash
   kubectl get pods -l app.kubernetes.io/component=proxy -o wide
   kubectl describe pod <proxy-pod>
   ```

   Look at the `Events` and `Last State` / termination reason. A drain timeout
   typically coincides with a rollout, scale-down, or node drain.

2. **Check for stuck in-flight long requests.** The proxy cannot exit until
   in-flight requests finish or the deadline elapses. Long streaming completions
   or a wedged upstream backend are the usual cause. Inspect the *Request
   Latency Percentiles* and *Active Connections* panels around the shutdown, and
   the proxy logs:

   ```bash
   kubectl logs <proxy-pod> --previous | grep -i "graceful shutdown"
   ```

   You should see `proxy graceful shutdown started` followed by
   `proxy graceful shutdown timed out` with a `duration` and `timeout`.

3. **Compare the two deadlines.** Two independent timeouts govern shutdown:

   - `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT` — how long the proxy *tries* to drain
     (default 10m).
   - the pod's `terminationGracePeriodSeconds` — how long Kubernetes waits
     before sending SIGKILL.

   If `terminationGracePeriodSeconds` is **shorter** than the drain timeout, the
   kubelet kills the pod mid-drain and the proxy never records `completed`.
   Ensure `terminationGracePeriodSeconds` is at least as long as the drain
   timeout:

   ```bash
   kubectl get pod <proxy-pod> -o jsonpath='{.spec.terminationGracePeriodSeconds}{"\n"}'
   ```

4. **Decide: extend or accept.** If drains legitimately need longer (very long
   streaming workloads), raise both `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT` and
   `terminationGracePeriodSeconds`. If a request or backend is wedged, treat the
   upstream as the root cause — extending the deadline only delays the cut-off.

## Metric reference

| Metric | Type | Labels | Meaning |
|--------|------|--------|---------|
| `flexinfer_proxy_shutdowns_total` | counter | `result` (`started` / `completed` / `timeout`) | Graceful-shutdown lifecycle events. |
| `flexinfer_proxy_shutdown_duration_seconds` | histogram | — | Drain duration per shutdown (buckets to 600s). |
| `flexinfer_proxy_label_group_route_decisions_total` | counter | `label`, `strategy`, `outcome` | One increment per label-group routing decision. |
| `flexinfer_proxy_label_group_route_target_hits_total` | counter | `label`, `strategy`, `model` | Selected member per label-group decision. |
| `flexinfer_proxy_active_connections` | gauge | `model` | Current active proxy connections per model. |

Metric definitions: `internal/proxy/metrics.go`. Shutdown emission:
`internal/proxy/proxy.go` (`waitForServer`). Routing emission:
`internal/proxy/resolver.go` (`observeLabelGroupPick`).
