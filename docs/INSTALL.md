# Installing FlexInfer

This repo ships a Helm chart at `charts/flexinfer/` that installs:

- Controller (`flexinfer-manager`)
- Node agent (`flexinfer-agent`)
- Scheduler + `kube-scheduler` sidecar (`flexinfer-sched`)
- Proxy/activator (`flexinfer-proxy`)
- CRDs:
  - v1alpha2: `Model`
  - v1alpha1: `ModelDeployment`, `ModelCache`, `GPUGroup`

## Prerequisites

- Kubernetes cluster with GPU nodes (NVIDIA, AMD, etc.)
- `helm` v3
- (Optional) Prometheus/Grafana for dashboards/metrics

## Install (Helm)

```bash
helm upgrade --install flexinfer charts/flexinfer \
  --namespace flexinfer-system \
  --create-namespace
```

## Configure Images

By default the chart references GHCR images. Override as needed:

```bash
helm upgrade --install flexinfer charts/flexinfer \
  --namespace flexinfer-system \
  --set controller.image.repository=registry.harbor.lan/flexinfer/flexinfer-controller \
  --set agent.image.repository=registry.harbor.lan/flexinfer/flexinfer-agent \
  --set scheduler.image.repository=registry.harbor.lan/flexinfer/flexinfer-scheduler \
  --set benchmarker.image.repository=registry.harbor.lan/flexinfer/flexinfer-bench \
  --set proxy.image.repository=registry.harbor.lan/flexinfer/flexinfer-proxy
```

## Enable/Disable Components

Each component can be toggled:

```bash
helm upgrade --install flexinfer charts/flexinfer \
  --namespace flexinfer-system \
  --set proxy.enabled=false \
  --set grafanaDashboard.enabled=false
```

## Verify

```bash
kubectl -n flexinfer-system get deploy,ds,svc
kubectl -n flexinfer-system get pods
```

## Deploy your first model (recommended: v1alpha2 `Model`)

```bash
kubectl apply -n flexinfer-system -f services/flexinfer/examples/v1alpha2/model-basic.yaml
kubectl -n flexinfer-system get models -w
```

## Legacy example (v1alpha1 ModelCache + ModelDeployment)

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: llama-7b-cache
spec:
  source: huggingface://meta-llama/Llama-2-7b-chat-hf
  storageStrategy: SharedPVC
---
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: llama-7b
spec:
  backend: ollama
  model: llama2:7b
  replicas: 1
  modelCacheRef: llama-7b-cache
  resources:
    limits:
      nvidia.com/gpu: 1
```

Apply:

```bash
kubectl apply -f your-model.yaml
kubectl get modeldeployments
```

## Next steps

- User docs: `docs/user/README.md`
- Proxy usage: `docs/user/proxy.md`
- CRD reference: `docs/specs/crds.md`
