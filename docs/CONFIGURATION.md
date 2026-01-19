# FlexInfer Configuration

This document describes all environment variables and configuration options for FlexInfer components.

## Table of Contents

- [Controller Manager](#controller-manager)
- [Proxy](#proxy)
- [Benchmarker](#benchmarker)
- [Node Agent](#node-agent)
- [Backend Images](#backend-images)

---

## Controller Manager

The controller manager handles reconciliation of FlexInfer CRDs and manages the lifecycle of inference workloads.

| Variable | Default | Description |
|----------|---------|-------------|
| `POD_NAMESPACE` | `default` | Namespace where controller resources are deployed |
| `BENCHMARK_SERVICE_ACCOUNT` | - | Service account used for benchmark pods. If not set, benchmark pods won't be created |
| `ENABLE_WEBHOOKS` | `true` | Enable admission webhooks for CRD validation |
| `METRICS_BIND_ADDRESS` | `:8080` | Address for Prometheus metrics endpoint |
| `HEALTH_PROBE_BIND_ADDRESS` | `:8081` | Address for health and readiness probes |
| `LEADER_ELECT` | `false` | Enable leader election for HA deployments |

### Backend Image Overrides

The controller uses these environment variables to override default backend container images:

| Variable | Default | Description |
|----------|---------|-------------|
| `DEFAULT_BACKEND_IMAGE` | - | Default image for NVIDIA GPUs (used if vendor-specific not set) |
| `DEFAULT_BACKEND_IMAGE_NVIDIA` | - | Ollama image for NVIDIA GPUs |
| `DEFAULT_BACKEND_IMAGE_AMD` | - | Ollama image for AMD GPUs |
| `DEFAULT_BACKEND_IMAGE_INTEL` | - | Ollama image for Intel GPUs |
| `DEFAULT_MLC_LLM_IMAGE` | - | MLC-LLM image for NVIDIA/default |
| `DEFAULT_MLC_LLM_IMAGE_AMD` | - | MLC-LLM image for AMD GPUs |
| `DEFAULT_MLC_LLM_IMAGE_MAXWELL` | - | MLC-LLM image for NVIDIA Maxwell GPUs (sm_52) |
| `DEFAULT_LLAMA_CPP_IMAGE` | - | llama.cpp image for NVIDIA/default |
| `DEFAULT_LLAMA_CPP_IMAGE_AMD` | - | llama.cpp image for AMD GPUs |
| `DEFAULT_LLAMA_CPP_IMAGE_CPU` | - | llama.cpp image for CPU-only inference |
| `DEFAULT_COMFYUI_IMAGE` | - | ComfyUI image for NVIDIA/default |
| `DEFAULT_COMFYUI_IMAGE_AMD` | - | ComfyUI image for AMD GPUs |
| `DEFAULT_DIFFUSERS_IMAGE` | - | Diffusers API image for NVIDIA/default |
| `DEFAULT_DIFFUSERS_IMAGE_AMD` | - | Diffusers API image for AMD GPUs |
| `DEFAULT_VLLM_IMAGE` | - | vLLM image for NVIDIA/default |
| `DEFAULT_VLLM_IMAGE_AMD` | - | vLLM image for AMD GPUs |

---

## Proxy

The proxy handles incoming inference requests, manages serverless scaling, and routes traffic to backend models.

| Variable | Default | Description |
|----------|---------|-------------|
| `POD_NAMESPACE` | `default` | Namespace where proxy watches for ModelDeployments |
| `PROXY_MAX_QUEUE_SIZE` | `100` | Maximum number of requests that can be queued per model |
| `PROXY_QUEUE_TIMEOUT` | `60s` | How long a request can wait in queue before timeout |
| `PROXY_COLD_START_TIMEOUT` | `60s` | Default timeout waiting for a model to become ready after scale-up |

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | Port to listen for incoming requests |
| `--log-level` | `info` | Log level: debug, info, warn, error |

---

## Benchmarker

The benchmarker runs inference performance tests against model backends.

| Variable | Required | Description |
|----------|----------|-------------|
| `PROXY_URL` | Yes | URL of the FlexInfer proxy endpoint (e.g., `http://flexinfer-proxy:8080`) |
| `POD_NAMESPACE` | Yes | Namespace where benchmarker is running |
| `NODE_NAME` | Yes | Name of the node running the benchmark (from downward API) |
| `BENCHMARK_RESULTS_CONFIGMAP` | Yes | Name of ConfigMap to store benchmark results |

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--model` | - | HuggingFace model name to benchmark (required) |
| `--model-name` | - | ModelDeployment name for proxy routing |
| `--configmap` | - | ConfigMap name for storing results (required) |
| `--backend` | `ollama` | Backend type: ollama, vllm, mlc-llm, tei |
| `--warmup-iterations` | `2` | Number of warmup iterations before measurement |
| `--min-duration` | `30s` | Minimum benchmark duration (wall time) |
| `--iterations` | `5` | Number of measurement iterations |
| `--batch-size` | `128` | Target tokens to generate per request |
| `--cold-start-timeout` | `5m` | Timeout waiting for model to become ready |

---

## Node Agent

The node agent runs as a DaemonSet and labels nodes with GPU capabilities.

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_NAME` | - | Name of the node (from downward API) |

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | `30s` | How often to re-probe hardware |
| `--metrics-port` | `9100` | Prometheus scrape port |
| `--label-prefix` | `flexinfer.ai/` | Prefix for node labels |

---

## Backend Images

Default container images for each backend type:

### Ollama
- **NVIDIA**: `ollama/ollama:latest`
- **AMD**: `ollama/ollama:rocm`
- **Intel**: `ollama/ollama:latest`

### vLLM
- **NVIDIA**: `vllm/vllm-openai:latest`
- **AMD**: `vllm/vllm-openai:rocm`

### MLC-LLM
- **NVIDIA**: `ghcr.io/mlc-ai/mlc-llm:cuda`
- **AMD**: `ghcr.io/mlc-ai/mlc-llm:rocm`
- **Maxwell (sm_52)**: `registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7`

### llama.cpp
- **NVIDIA**: `ghcr.io/ggerganov/llama.cpp:server-cuda`
- **AMD**: `ghcr.io/ggerganov/llama.cpp:server-rocm`
- **CPU**: `ghcr.io/ggerganov/llama.cpp:server`

### ComfyUI
- **NVIDIA**: `comfyanonymous/comfyui:latest`
- **AMD**: `registry.harbor.lan/library/comfyui:rocm6.2.3-v8`

### Diffusers
- **NVIDIA**: `registry.harbor.lan/library/diffusers-api:cuda`
- **AMD**: `registry.harbor.lan/library/diffusers-api:rocm-latest`

---

## CRD Configuration

### ModelDeployment Spec

Key configuration fields in the `ModelDeployment` CRD:

| Field | Type | Description |
|-------|------|-------------|
| `model` | string | Model identifier (e.g., HuggingFace model ID) |
| `backend` | string | Backend type: ollama, vllm, mlc-llm, llamacpp, comfyui, diffusers |
| `replicas` | *int32 | Number of replicas (0 for serverless) |
| `gpuGroupRef` | *string | Reference to a GPUGroup for exclusive scheduling |
| `coldStartTimeoutSeconds` | *int32 | Per-model cold start timeout (overrides proxy default) |
| `idleTimeoutSeconds` | *int32 | Time before scaling to zero when idle |
| `serviceLabels` | []string | Service labels for routing (e.g., "textgen", "chat") |
| `config` | map | Backend-specific configuration |

### GPUGroup Spec

| Field | Type | Description |
|-------|------|-------------|
| `nodeSelector` | map | Node selector for the GPU group |
| `models` | []GPUGroupMember | List of models and their priorities |
| `antiThrashing` | *AntiThrashingConfig | Configuration to prevent rapid model swaps |

---

## Helm Values

See `charts/flexinfer/values.yaml` for complete Helm chart configuration options.

Key values:

```yaml
# Controller settings
controller:
  image: flexinfer/controller:latest
  replicas: 1
  resources:
    limits:
      cpu: 500m
      memory: 256Mi

# Proxy settings
proxy:
  image: flexinfer/proxy:latest
  replicas: 1
  port: 8080
  maxQueueSize: 100
  queueTimeout: 60s
  coldStartTimeout: 60s

# Agent settings
agent:
  image: flexinfer/agent:latest
  labelPrefix: flexinfer.ai/
  probeInterval: 30s
```
