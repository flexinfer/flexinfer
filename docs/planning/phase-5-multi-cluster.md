---
title: Phase 5: Multi-Cluster
description: Concrete checklist for multi-cluster federation support.
---

# Phase 5: Multi-Cluster

> Last updated: 2026-02-17

This is the checklist for Phase 5: enable **multi-cluster federation** for FlexInfer deployments.

See `docs/design/multi-cluster.md` for the full design document.

## Tracking

- [Phase 5.1 Cluster Registry (MVP)](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/3)
- [Phase 5.2 Cross-Cluster Model Sync](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/4)
- [Phase 5.3 Global Routing](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/5)
- [Phase 5.4 Advanced Features](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/6)

## Goals

- Register and manage multiple Kubernetes clusters from a hub cluster.
- Deploy models across clusters with unified management.
- Route requests to appropriate clusters based on latency, load, or availability.

## Non-Goals

- Full Kubernetes federation (we focus on inference-specific needs).
- Automatic cross-cluster KV-cache migration.

## Prerequisites

- Stable single-cluster baseline (controller hardening, serverless, routing, operational polish)
- Network connectivity between clusters (VPN, service mesh, or direct)
- Cluster credentials management strategy

## Work items (PR-sized)

### Phase 5.1: Cluster Registry (MVP)

#### 1) Cluster CRD

- [x] Define `Cluster` CRD (`api/v1alpha2/cluster_types.go`):
  - `spec.apiEndpoint` - Kubernetes API server URL
  - `spec.secretRef` - Reference to kubeconfig Secret
  - `spec.labels` - Cluster metadata (region, gpu-vendor, tier)
- [x] Add `status` fields:
  - `phase` (Pending, Ready, NotReady, Unknown)
  - `capacity` (GPU counts by type)
  - `available` (free GPU counts)
  - `models` (list of deployed models with status)
- [x] Add unit tests for Cluster spec validation helpers (`api/v1alpha2/cluster_types_test.go`)

**Acceptance**
- `kubectl apply` of Cluster resource creates/updates correctly.
- Status fields are populated by controller.

**Primary files**
- `api/v1alpha2/cluster_types.go` (new)
- `controllers/cluster_controller.go` (new)

#### 2) Cluster health checking

- [x] Implement cluster connectivity check (API server reachable)
- [x] Implement periodic health probe (configurable interval)
- [x] Update `Cluster.status.phase` based on health
- [x] Add metrics:
  - `flexinfer_cluster_health{cluster, region}` (gauge: 0/1)
  - `flexinfer_cluster_probe_latency_seconds{cluster}` (histogram)
  - `flexinfer_cluster_models_discovered{cluster, region}` (gauge: discovered model count)
  - `flexinfer_cluster_model_inventory_source{cluster, source}` (gauge: active source one-hot; `source=watch|list`)
  - `flexinfer_cluster_model_watch_ready{cluster}` (gauge: 1 when remote watch stream is healthy)

**Acceptance**
- Unhealthy clusters are marked `NotReady` within configured timeout.
- Metrics available for alerting.
- `Cluster.status.conditions` includes `ModelWatchReady` with:
  - `Reason=WatchSynced` when watch stream is healthy
  - `Reason=ListFallback` when inventory is using list/cached fallback
  - `Reason=WatchDegraded` when watch loop failures are detected
  - `Status=False` for both `ListFallback` and `WatchDegraded` (inventory still populated via fallback)
- Condition transitions emit cluster events:
  - `ModelWatchSynced` (normal)
  - `ModelWatchFallback` (normal)
  - `ModelWatchDegraded` (warning)
- `flexinfer_cluster_model_inventory_source{cluster,source}` is one-hot per cluster (`source=watch|list`)
- `flexinfer_cluster_model_watch_ready{cluster}=1` only when `Reason=WatchSynced`

**Primary files**
- `controllers/cluster_controller.go`
- `internal/cluster/health.go` (new)

#### 3) Cluster inventory aggregation

- [x] Watch FlexInfer resources in remote clusters via client
- [x] Aggregate GPU capacity from remote nodes (allocatable GPU resources)
- [x] Aggregate model inventory from remote Model resources
- [x] Update `Cluster.status.models` with remote model list

**Acceptance**
- `kubectl get cluster us-west -o yaml` shows current GPU availability and model list.

**Primary files**
- `controllers/cluster_controller.go`

### Phase 5.2: Cross-Cluster Model Sync

#### 4) FederatedModel CRD

- [x] Define `FederatedModel` CRD:
  - `spec.template` - Model spec to deploy
  - `spec.placement.clusterSelector` - Label selector for target clusters
  - `spec.placement.clusters` - Explicit cluster list
  - `spec.placement.replicasPerCluster` - Replicas per cluster
  - `spec.routing` - Cross-cluster routing strategy
- [x] Add `status` aggregation:
  - Per-cluster deployment status
  - Overall ready/total counts

**Acceptance**
- FederatedModel creates Model resources in matching clusters.

**Primary files**
- `api/v1alpha2/federatedmodel_types.go` (new)
- `controllers/federatedmodel_controller.go` (new)

#### 5) Model propagation

- [x] Watch FederatedModel resources (status/placement scaffold controller)
- [x] Create/update/delete Model resources in target clusters
- [x] Handle cluster membership changes (add/remove clusters)
- [x] Aggregate status from remote Models

**Acceptance**
- Model appears in all selected clusters within reconcile interval.
- Status reflects actual state across clusters.

**Primary files**
- `controllers/federatedmodel_controller.go`
- `internal/cluster/client.go` (new)

### Phase 5.3: Global Routing

#### 6) GlobalProxy component

- [x] Create `cmd/flexinfer-global-proxy/` binary
- [x] Implement cluster endpoint registry
- [x] Implement round-robin routing as default
- [x] Implement failover routing (primary/backup)

**Acceptance**
- Requests to global proxy reach healthy cluster proxies.
- Failover triggers on cluster unavailability.

**Primary files**
- `cmd/flexinfer-global-proxy/main.go` (new)
- `internal/globalrouting/router.go` (new)

#### 7) GlobalProxy CRD

- [x] Define `GlobalProxy` CRD:
  - `spec.externalEndpoint` - External hostname
  - `spec.tls` - TLS configuration
  - `spec.clusters` - Cluster endpoints with weights

**Acceptance**
- GlobalProxy CRD configures routing behavior.

**Primary files**
- `api/v1alpha2/globalproxy_types.go` (new)

#### 8) Latency-based routing

- [x] Implement latency probing to cluster endpoints
- [x] Route to lowest-latency healthy cluster
- [x] Add metrics for routing decisions

**Acceptance**
- Traffic shifts to lower-latency clusters automatically.

**Primary files**
- `cmd/flexinfer-global-proxy/main.go`
- `internal/globalrouting/latency.go` (new)

### Phase 5.4: Advanced Features (Optional)

#### 9) Weighted routing

- [x] Implement weighted distribution based on cluster weights
- [x] Support dynamic weight adjustment via CRD

#### 10) GPU-aware routing

- [x] Route based on GPU availability
- [x] Match model requirements to cluster capabilities

#### 11) Cross-cluster metrics aggregation

- [x] Aggregate metrics from all clusters
- [x] Provide unified Grafana dashboard

## Security Considerations

- Kubeconfig secrets must be encrypted at rest
- RBAC in remote clusters scoped to FlexInfer resources only
- mTLS for cross-cluster traffic recommended
- Audit logging for cross-cluster operations

## Rollout Strategy

1. Deploy Cluster Registry to hub cluster
2. Register existing clusters with kubeconfig secrets
3. Test FederatedModel with non-critical model
4. Deploy GlobalProxy for unified entry point
5. Migrate traffic gradually to global endpoint

## Tracking

- This checklist is the source-of-truth for Phase 5 items.
- When a PR lands, add a checkbox + link to the PR/commit in this doc.
- Tracking issues:
  - [Phase 5.1 Cluster Registry (MVP)](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/3)
  - [Phase 5.2 Cross-Cluster Model Sync](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/4)
  - [Phase 5.3 Global Routing](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/5)
  - [Phase 5.4 Advanced Features](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/6)
