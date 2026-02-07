# Multi-Cluster Support Design

**Status:** Proposed
**Author:** FlexInfer Team
**Created:** 2026-01-31

## Overview

Multi-cluster support enables FlexInfer to federate inference workloads across multiple Kubernetes clusters. This allows organizations to:

- Route requests to the nearest cluster for lower latency
- Failover to healthy clusters during outages
- Balance load across heterogeneous GPU resources
- Consolidate management of models across environments

## Architecture

```
                     ┌─────────────────┐
                     │   Global Proxy  │
                     │  (Entry Point)  │
                     └────────┬────────┘
                              │
            ┌─────────────────┼─────────────────┐
            │                 │                 │
    ┌───────▼───────┐ ┌───────▼───────┐ ┌───────▼───────┐
    │   Cluster A   │ │   Cluster B   │ │   Cluster C   │
    │   (NVIDIA)    │ │    (AMD)      │ │    (Mixed)    │
    │               │ │               │ │               │
    │ ┌───────────┐ │ │ ┌───────────┐ │ │ ┌───────────┐ │
    │ │ FlexInfer │ │ │ │ FlexInfer │ │ │ │ FlexInfer │ │
    │ │Controller │ │ │ │Controller │ │ │ │Controller │ │
    │ └───────────┘ │ │ └───────────┘ │ │ └───────────┘ │
    └───────────────┘ └───────────────┘ └───────────────┘
```

## Components

### 1. Federation Controller

A new controller that runs in a "hub" cluster and manages cross-cluster resources:

```go
// FederatedModel represents a model deployed across multiple clusters
type FederatedModel struct {
    metav1.TypeMeta   `json:",inline"`
    metav1.ObjectMeta `json:"metadata,omitempty"`

    Spec   FederatedModelSpec   `json:"spec,omitempty"`
    Status FederatedModelStatus `json:"status,omitempty"`
}

type FederatedModelSpec struct {
    // Template is the Model spec to deploy
    Template ModelSpec `json:"template"`

    // Placement controls which clusters receive the model
    Placement PlacementSpec `json:"placement"`

    // Routing controls how traffic is distributed
    Routing RoutingSpec `json:"routing"`
}

type PlacementSpec struct {
    // ClusterSelector selects clusters by labels
    ClusterSelector *metav1.LabelSelector `json:"clusterSelector,omitempty"`

    // Clusters explicitly lists target clusters
    Clusters []string `json:"clusters,omitempty"`

    // ReplicasPerCluster is the number of replicas per cluster
    ReplicasPerCluster int32 `json:"replicasPerCluster,omitempty"`
}

type RoutingSpec struct {
    // Strategy: RoundRobin, Latency, Weighted, Failover
    Strategy string `json:"strategy"`

    // Weights for Weighted strategy (cluster -> weight)
    Weights map[string]int32 `json:"weights,omitempty"`

    // FailoverOrder for Failover strategy
    FailoverOrder []string `json:"failoverOrder,omitempty"`
}
```

### 2. Cluster Registry

Tracks registered clusters and their capabilities:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Cluster
metadata:
  name: us-west-nvidia
spec:
  apiEndpoint: https://k8s-us-west.example.com:6443
  secretRef:
    name: cluster-us-west-kubeconfig
  labels:
    region: us-west
    gpu-vendor: nvidia
    tier: production
status:
  phase: Ready
  capacity:
    nvidia.com/gpu: "8"
    amd.com/gpu: "0"
  available:
    nvidia.com/gpu: "4"
  models:
    - name: llama3
      phase: Ready
    - name: codellama
      phase: Ready
```

### 3. Global Proxy

A federated proxy that routes to cluster-local proxies:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: GlobalProxy
metadata:
  name: global
spec:
  # External endpoint for clients
  externalEndpoint: https://ai.example.com

  # TLS configuration
  tls:
    secretRef:
      name: global-proxy-tls

  # Cluster endpoints
  clusters:
    - name: us-west-nvidia
      endpoint: http://flexinfer-proxy.us-west:80
      weight: 50
    - name: eu-central-amd
      endpoint: http://flexinfer-proxy.eu-central:80
      weight: 30
    - name: asia-mixed
      endpoint: http://flexinfer-proxy.asia:80
      weight: 20
```

## Routing Strategies

### Round Robin

Simple rotation across healthy clusters. Good for uniform workloads.

### Latency-Based

Route to cluster with lowest observed latency. Requires latency probing.

### Weighted

Distribute traffic based on configured weights. Good for heterogeneous clusters.

### Failover

Primary/backup clusters. Route all traffic to primary until it fails.

### GPU-Aware

Route based on GPU availability and type. Match model requirements to cluster capabilities.

## Implementation Phases

### Phase 1: Cluster Registry (MVP)

1. Add `Cluster` CRD for registering clusters
2. Implement health checking for registered clusters
3. Add cluster status aggregation (GPU capacity, model inventory)

### Phase 2: Cross-Cluster Model Sync

1. Add `FederatedModel` CRD
2. Implement model propagation to registered clusters
3. Add status aggregation from member clusters

### Phase 3: Global Routing

1. Add `GlobalProxy` component
2. Implement round-robin and failover strategies
3. Add latency probing for latency-based routing

### Phase 4: Advanced Features

1. Weighted routing with auto-tuning
2. Cross-cluster GPU sharing
3. Model cache synchronization
4. Multi-cluster metrics aggregation

## API Design

### Register a Cluster

```bash
# Create kubeconfig secret
kubectl create secret generic cluster-us-west \
  --from-file=kubeconfig=~/.kube/us-west.yaml

# Register cluster
kubectl apply -f - <<EOF
apiVersion: ai.flexinfer/v1alpha2
kind: Cluster
metadata:
  name: us-west-nvidia
  labels:
    region: us-west
    gpu-vendor: nvidia
spec:
  secretRef:
    name: cluster-us-west
EOF
```

### Create a Federated Model

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: FederatedModel
metadata:
  name: llama3-global
spec:
  template:
    backend: ollama
    source: ollama://llama3:8b
    gpu:
      count: 1
    serverless:
      idleTimeout: 5m
  placement:
    clusterSelector:
      matchLabels:
        tier: production
  routing:
    strategy: Latency
```

### View Global Status

```bash
$ flexinfer federated status
CLUSTER          REGION      GPUS    MODELS  HEALTH
us-west-nvidia   us-west     4/8     5       Healthy
eu-central-amd   eu-central  2/4     3       Healthy
asia-mixed       asia        6/8     4       Degraded

$ flexinfer federated models
NAME              CLUSTERS    READY    ROUTING
llama3-global     3/3         3/3      Latency
codellama-fed     2/3         2/2      Failover
```

## Security Considerations

### Cluster Authentication

- Each cluster connection uses a dedicated ServiceAccount
- Kubeconfig secrets are encrypted at rest
- RBAC scoped to FlexInfer resources only

### Network Security

- All cross-cluster traffic uses mTLS
- Cluster endpoints validated via CA certificates
- Network policies restrict cluster-to-cluster communication

### Secret Management

- Model credentials (HuggingFace tokens, etc.) not synced across clusters
- Each cluster maintains its own credential store
- Secret references in FederatedModel are resolved locally

## Alternatives Considered

### Kubernetes Federation v2 (KubeFed)

**Pros:** Standard Kubernetes approach, community support
**Cons:** Heavyweight, requires full federation stack, not inference-optimized

**Decision:** Build custom federation for inference-specific needs.

### Service Mesh (Istio Multi-Cluster)

**Pros:** Mature routing, mTLS built-in
**Cons:** Complex setup, not model-aware, heavy resource overhead

**Decision:** Use service mesh optionally for transport, but implement model-aware routing ourselves.

### DNS-Based Routing

**Pros:** Simple, works with any client
**Cons:** No request-level routing, slow failover, no GPU awareness

**Decision:** DNS as optional entry point, but primary routing via Global Proxy.

## Open Questions

1. **Cache Synchronization**: Should model caches sync across clusters? (storage cost vs. cold start latency)

2. **Quota Management**: How to enforce quotas across clusters?

3. **Metrics Aggregation**: Unified dashboard or per-cluster views?

4. **Model Versions**: How to handle model version differences across clusters?

## References

- [Kubernetes Multi-Cluster Patterns](https://kubernetes.io/docs/concepts/cluster-administration/federation/)
- [Liqo - Multi-Cluster Networking](https://liqo.io/)
- [Karmada - Multi-Cluster Management](https://karmada.io/)
