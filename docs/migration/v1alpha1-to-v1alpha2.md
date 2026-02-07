# Migration Guide: v1alpha1 to v1alpha2

This guide helps you migrate from `v1alpha1.ModelDeployment` to `v1alpha2.Model`.

## Overview

The v1alpha2 API introduces a simplified `Model` resource that replaces the v1alpha1 `ModelDeployment` + `GPUGroup` workflow. The new API is designed for homelab users who want a streamlined experience.

### Key Changes

| v1alpha1 | v1alpha2 | Notes |
|----------|----------|-------|
| `ModelDeployment` + `GPUGroup` | `Model` | Single unified resource |
| `spec.model` | `spec.source` | URI format (ollama://, HF://, pvc://) |
| `spec.replicas`, `spec.minReplicas`, `spec.idleTimeoutSeconds` | `spec.serverless.*` | Structured serverless config |
| `spec.resources` with GPU limits | `spec.gpu.*` | Separate GPU configuration |
| `spec.modelCacheRef` | `spec.cache.*` | Integrated cache config |
| Backend-specific fields (MLCLLM, VLLM, etc.) | `spec.config` | JSON blob for flexibility |

## Migration Examples

### Basic Ollama Model

**v1alpha1:**
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: llama3
spec:
  backend: ollama
  model: llama3:8b
  replicas: 1
  minReplicas: 0
  idleTimeoutSeconds: 300
```

**v1alpha2:**
```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: llama3
spec:
  backend: ollama
  source: ollama://llama3:8b
  serverless:
    minReplicas: 0
    idleTimeout: 5m
```

### Model with GPU Configuration

**v1alpha1:**
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: codellama
spec:
  backend: ollama
  model: codellama:7b
  resources:
    limits:
      nvidia.com/gpu: "1"
```

**v1alpha2:**
```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: codellama
spec:
  backend: ollama
  source: ollama://codellama:7b
  gpu:
    vendor: nvidia
    count: 1
```

### Model with Cache

**v1alpha1:**
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: mistral
spec:
  backend: ollama
  model: mistral:7b
  modelCacheRef: my-model-cache
```

**v1alpha2:**
```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: mistral
spec:
  backend: ollama
  source: ollama://mistral:7b
  cache:
    strategy: SharedPVC
    pvcName: my-model-cache
```

### GPU Sharing (formerly GPUGroup)

**v1alpha1:**
```yaml
# First create a GPUGroup
apiVersion: ai.flexinfer/v1alpha1
kind: GPUGroup
metadata:
  name: shared-gpu
spec:
  nodeSelector:
    node.flexstack.io/gpu-vendor: nvidia
---
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: phi
  labels:
    gpugroup: shared-gpu
spec:
  backend: ollama
  model: phi:mini
```

**v1alpha2:**
```yaml
# Single Model with shared GPU - no separate GPUGroup needed
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: phi
spec:
  backend: ollama
  source: ollama://phi:mini
  gpu:
    shared: shared-gpu
    priority: 100
```

### MLC-LLM Backend Configuration

**v1alpha1:**
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen
spec:
  backend: mlc-llm
  model: Qwen/Qwen2-0.5B-Instruct
  mlcllm:
    mode: server
    gpuMemoryBytes: 23068672000
    overrides:
      maxNumSequence: 4
```

**v1alpha2:**
```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: qwen
spec:
  backend: mlc-llm
  source: HF://Qwen/Qwen2-0.5B-Instruct
  config:
    mode: server
    gpuMemoryBytes: 23068672000
    maxNumSequence: 4
```

### LiteLLM Integration

**v1alpha1:**
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: gpt-local
spec:
  backend: ollama
  model: llama3:8b
  litellm:
    enabled: true
    servedModelName: gpt-4
    aliases:
      - gpt-4-turbo
```

**v1alpha2:**
```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: gpt-local
spec:
  backend: ollama
  source: ollama://llama3:8b
  litellm:
    enabled: true
    servedModelName: gpt-4
    aliases:
      - gpt-4-turbo
```

## Field Mapping Reference

### ModelDeploymentSpec to ModelSpec

| v1alpha1 Field | v1alpha2 Field | Notes |
|----------------|----------------|-------|
| `backend` | `backend` | Unchanged |
| `model` | `source` | Add protocol prefix (ollama://, HF://, file://, pvc://) |
| `replicas` | `serverless.minReplicas` | Use 1 for always-on |
| `minReplicas` | `serverless.minReplicas` | Direct mapping |
| `idleTimeoutSeconds` | `serverless.idleTimeout` | Now uses duration format (e.g., "5m") |
| `coldStartTimeoutSeconds` | `serverless.coldStartTimeout` | Now uses duration format |
| `resources` | `resources` | GPU resources moved to `gpu.*` |
| `modelCacheRef` | `cache.pvcName` | Set `cache.strategy: SharedPVC` |
| `litellm` | `litellm` | Mostly unchanged |
| `nodeSelector` | `nodeSelector` | Unchanged |
| `mlcllm.*` | `config.*` | Flatten into JSON config |
| `vllm.*` | `config.*` | Flatten into JSON config |
| `llamacpp.*` | `config.*` | Flatten into JSON config |

### Source URI Formats

| Model Type | v1alpha1 | v1alpha2 |
|------------|----------|----------|
| Ollama | `model: llama3:8b` | `source: ollama://llama3:8b` |
| HuggingFace | `model: meta-llama/Llama-2-7b` | `source: HF://meta-llama/Llama-2-7b` |
| Local file | `model: /models/mymodel` | `source: file:///models/mymodel` |
| PVC | (via modelCacheRef) | `source: pvc://my-pvc/models/path` |

## Migration Steps

1. **Export existing ModelDeployments:**
   ```bash
   kubectl get modeldeployments -A -o yaml > modeldeployments-backup.yaml
   ```

2. **Convert each ModelDeployment to v1alpha2 Model:**
   Use the field mapping above to create new Model resources.

3. **Test in a non-production namespace:**
   ```bash
   kubectl apply -f new-model.yaml -n test
   kubectl wait --for=condition=Ready model/my-model -n test
   ```

4. **Delete old resources and apply new ones:**
   ```bash
   kubectl delete modeldeployment my-model -n production
   kubectl apply -f new-model.yaml -n production
   ```

5. **Clean up GPUGroups (if not needed):**
   With v1alpha2, GPUGroups are only needed for advanced multi-node scenarios.
   For single-GPU sharing, use `spec.gpu.shared` instead.

## Deprecation Timeline

- **v1.0.0**: v1alpha1 ModelDeployment marked as deprecated
- **v1.1.0**: Warning logs emitted when using v1alpha1
- **v2.0.0**: v1alpha1 ModelDeployment removed

## Getting Help

If you encounter issues during migration:

1. Check the [FlexInfer documentation](https://flexinfer.ai/docs)
2. Open an issue on [GitLab](https://gitlab.flexinfer.ai/services/flexinfer/issues)
3. Review the v1alpha2 Model examples in `examples/v1alpha2/`
