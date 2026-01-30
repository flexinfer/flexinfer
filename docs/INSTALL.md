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

## Verification checklist

After installation, verify everything is working:

| Check | Command | Expected |
|-------|---------|----------|
| Pods running | `kubectl -n flexinfer-system get pods` | All pods Running/Ready |
| CRDs installed | `kubectl get crds \| grep flexinfer` | Model, ModelDeployment, etc. |
| Proxy reachable | `kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 8080:80` then `curl localhost:8080/healthz` | `ok` |
| GPU nodes labeled | `kubectl get nodes -l flexinfer.ai/gpu-vendor` | GPU nodes listed |

## Troubleshooting

### Installation issues

**CRDs not created**
```bash
# Check if controller deployed successfully
kubectl -n flexinfer-system logs deploy/flexinfer-controller --tail=50

# Manually apply CRDs if needed
kubectl apply -f config/crd/
```

**Scheduler extender not working**
```bash
# Verify scheduler pod has kube-scheduler sidecar
kubectl -n flexinfer-system get pods -l app.kubernetes.io/name=flexinfer-scheduler -o yaml | grep image

# Check scheduler logs
kubectl -n flexinfer-system logs deploy/flexinfer-scheduler -c kube-scheduler --tail=50
```

**Node agent not labeling nodes**
```bash
# Check agent DaemonSet status
kubectl -n flexinfer-system get ds flexinfer-agent

# Check agent logs on a specific node
kubectl -n flexinfer-system logs ds/flexinfer-agent --tail=50

# Verify node labels
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}: {.metadata.labels.flexinfer\.ai/gpu-vendor}{"\n"}{end}'
```

### Model deployment issues

**Model stuck in Pending phase**
- Check if GPU nodes are available: `kubectl get nodes -l flexinfer.ai/gpu-vendor`
- Check Model conditions: `kubectl describe model <name>`
- Verify GPU resources: `kubectl describe node <gpu-node> | grep -A5 "Allocated resources"`

**Model stuck in Downloading phase**
- Check model pod logs: `kubectl logs -l model.flexinfer.ai/name=<model-name> --tail=100`
- Verify network access to model source (HuggingFace, Ollama registry, etc.)

**CUDA errors in pods**
- See [GPU requirements](#nvidia-gpu-requirements) in operations.md
- Ensure RuntimeClass `nvidia` exists: `kubectl get runtimeclass nvidia`

For more troubleshooting guidance, see `docs/user/operations.md`.

## Uninstall

```bash
# Remove Helm release (keeps CRDs by default)
helm uninstall flexinfer -n flexinfer-system

# Remove CRDs (WARNING: deletes all FlexInfer resources)
kubectl delete -f config/crd/

# Remove namespace
kubectl delete namespace flexinfer-system
```

## Next steps

- User docs: `docs/user/README.md`
- Quickstart: `docs/user/quickstart.md`
- Proxy usage: `docs/user/proxy.md`
- CRD reference: `docs/specs/crds.md`
- Configuration: `docs/CONFIGURATION.md`
