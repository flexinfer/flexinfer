# FlexInfer Configuration

This document describes all environment variables and configuration options for FlexInfer components.

## Table of Contents

- [Controller Manager](#controller-manager)
- [Proxy](#proxy)
- [Benchmarker](#benchmarker)
- [Node Agent](#node-agent)
- [Backend Images](#backend-images)
- [Image Pinning Best Practices](#image-pinning-best-practices)

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
| `DEFAULT_SHM_SIZE_LIMIT` | `8Gi` | Default `/dev/shm` size limit for v1alpha2 model pods |
| `DEFAULT_FLASH_LOADER_ENABLED` | `false` | Enable flash-loader init container by default for eligible v1alpha2 models |
| `DEFAULT_FLASH_LOADER_IMAGE` | `registry.harbor.lan/flexinfer/flash-loader:latest` | Default flash-loader image |
| `DEFAULT_FLASH_LOADER_CONCURRENCY` | `4` | Default flash-loader copy parallelism |
| `DEFAULT_FLASH_LOADER_TMPFS_SIZE_LIMIT` | - | Optional default tmpfs size limit for flash-loader staging volume |
| `DEFAULT_FLASH_LOADER_BUFFER_KB` | `4096` | Default flash-loader copy buffer size in KiB |
| `DEFAULT_FLASH_LOADER_VERIFY` | `false` | Verify copied files after flash-loader transfer |
| `DEFAULT_FLASH_LOADER_EXCLUDE` | - | Comma-separated flash-loader exclude patterns |
| `FLEXINFER_OTEL_ENABLED` | `false` | Enable OpenTelemetry tracing export (manager + proxy + scheduler) |
| `FLEXINFER_OTEL_SERVICE_NAMESPACE` | - | Optional `service.namespace` attribute for exported spans |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTEL SDK default | OTLP endpoint for tracing exporter (typically `http://collector:4318`) |

Flash-loader defaults can be set globally through Helm at
`controller.runtime.flashLoader.*`. Per-cache and per-model settings override
those defaults in this order:

1. Controller env defaults rendered from Helm.
2. Matching v1alpha1 `ModelCache.spec.flashLoader`.
3. v1alpha2 `Model.spec.cache.flashLoader`.

The v1alpha2 override is the highest-priority layer. Shared-GPU models with
`cache.strategy: Local` auto-enable flash-loader unless an explicit flash-loader
config says otherwise.

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
| `FLEXINFER_QUANTIZER_GGUF_IMAGE` | `ghcr.io/flexinfer/quantizer:gguf` | GGUF quantizer job image for `ModelCache.spec.quantization.format=GGUF` |
| `FLEXINFER_QUANTIZER_AWQ_IMAGE` | `ghcr.io/flexinfer/quantizer:awq` | AWQ quantizer job image for `ModelCache.spec.quantization.format=AWQ` |
| `FLEXINFER_QUANTIZER_GPTQ_IMAGE` | `ghcr.io/flexinfer/quantizer:gptq` | GPTQ quantizer job image for `ModelCache.spec.quantization.format=GPTQ` |
| `FLEXINFER_QUANTIZER_EXL2_IMAGE` | `ghcr.io/flexinfer/quantizer:exl2` | EXL2 quantizer job image for `ModelCache.spec.quantization.format=EXL2` |
| `FLEXINFER_QUANTIZER_FP8_IMAGE` | `ghcr.io/flexinfer/quantizer:fp8` | FP8 quantizer job image for `ModelCache.spec.quantization.format=FP8` |
| `FLEXINFER_MODEL_TOOLS_IMAGE` | `ghcr.io/flexinfer/model-tools:latest` | Lightweight CPU-only image for ModelCache publish and publish-validator jobs; includes `oras`, `safetensors`, `huggingface_hub`, and FlexInfer helper scripts |
| `FLEXINFER_PUBLISH_IMAGE` | `FLEXINFER_MODEL_TOOLS_IMAGE` | Optional publish-job image override |
| `FLEXINFER_VALIDATOR_IMAGE` | `FLEXINFER_MODEL_TOOLS_IMAGE` | Optional pre-publish artifact validator image override |
| `FLEXINFER_RUNTIME_IMAGE` | - | Optional unified runtime image for quantization jobs and legacy validator fallback |

---

## Proxy

The proxy handles incoming inference requests, manages serverless scaling, and routes traffic to backend models.

| Variable | Default | Description |
|----------|---------|-------------|
| `POD_NAMESPACE` | `default` | Namespace where proxy watches for ModelDeployments |
| `PROXY_MAX_QUEUE_SIZE` | `100` | Maximum number of requests that can be queued per model |
| `PROXY_QUEUE_TIMEOUT` | `60s` | How long a request can wait in queue before timeout |
| `PROXY_COLD_START_TIMEOUT` | `60s` | Default timeout waiting for a model to become ready after scale-up |
| `PROXY_ROUTING_ENABLED` | `true` | Enable advanced routing (session affinity, prefix-based) |
| `PROXY_ROUTING_EXPLICIT_KEY_MAX_LENGTH` | `128` | Maximum accepted length for explicit cache keys (`X-Flexinfer-Cache-Key`, `cache_key`, `cacheKey`) |
| `PROXY_ROUTING_SYSTEM_SEGMENT_MAX_LENGTH` | `512` | Maximum canonicalized system-context segment length used for prefix keying |
| `PROXY_ROUTING_DOCUMENT_SEGMENT_MAX_LENGTH` | `256` | Maximum canonicalized document-context segment length used for prefix keying |
| `PROXY_MAX_TOKENS_CLAMP_ENABLED` | `true` | Clamp OpenAI `max_tokens` so requests leave prompt headroom within the resolved model context window |
| `PROXY_MAX_TOKENS_CLAMP_PROMPT_RESERVE_TOKENS` | `512` | Prompt-token reserve subtracted from the context window when clamping `max_tokens` |
| `PROXY_VALIDATE_REQUESTS` | `false` | Enable OpenAI request schema validation (validates required fields, field types, and value ranges) |
| `PROXY_BACKOFF_ENABLED` | `false` | Enable exponential backoff with jitter for failed activations |
| `PROXY_BACKOFF_MAX_RETRIES` | `3` | Maximum retry attempts after initial activation failure |
| `PROXY_BACKOFF_INITIAL_WAIT` | `5s` | Initial wait time before first retry |
| `PROXY_BACKOFF_MAX_WAIT` | `30s` | Maximum wait time between retries |
| `FLEXINFER_OTEL_ENABLED` | `false` | Enable OpenTelemetry tracing export for proxy requests |
| `FLEXINFER_OTEL_SERVICE_NAMESPACE` | - | Optional `service.namespace` attribute for proxy spans |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTEL SDK default | OTLP endpoint for proxy trace export |

### Routing Configuration

Enable routing strategies via model annotations:

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
  annotations:
    flexinfer.ai/routing: session-affinity  # or: prefix, least-loaded
```

Available strategies:
- `session-affinity`: Route by session ID for KV-cache locality
- `prefix`: Route by system prompt for shared prefix caching
- `least-loaded`: Route to pod with fewest active connections
- (default): Kubernetes Service round-robin

See [docs/user/routing.md](user/routing.md) for detailed routing documentation.

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8080` | Port to listen for incoming requests |
| `--log-level` | `info` | Log level: debug, info, warn, error |

---

## Scheduler Extender

The scheduler extender (`flexinfer-sched`) plugs into kube-scheduler as an extender to filter and score nodes.

| Variable | Default | Description |
|----------|---------|-------------|
| `BENCHMARK_RESULTS_CONFIGMAP` | `flexinfer-benchmark-results` | ConfigMap containing benchmark results |
| `SCHED_TPS_WEIGHT` | `0.7` | Weight for tokens/sec score |
| `SCHED_UTIL_WEIGHT` | `0.2` | Weight for GPU utilization hint |
| `SCHED_COST_WEIGHT` | `0.1` | Weight for node “cost” hint |
| `SCHED_CACHE_WEIGHT` | `0.3` | Weight for KV-cache locality hint |
| `FLEXINFER_OTEL_ENABLED` | `false` | Enable OpenTelemetry tracing export for filter/score requests |
| `FLEXINFER_OTEL_SERVICE_NAMESPACE` | - | Optional `service.namespace` attribute for scheduler spans |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTEL SDK default | OTLP endpoint for scheduler trace export |

The scheduler emits `scheduler.filter` and `scheduler.score` spans and propagates W3C Trace Context from incoming extender requests. Enable via Helm `observability.tracing.enabled=true`.

### Command Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--port` | `8082` | Port to listen on for extender HTTP endpoints |

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
- **AMD gfx1100 (RDNA3)**: `registry.harbor.lan/library/vllm-api:rocm-gfx1100`

### MLC-LLM
- **NVIDIA**: `ghcr.io/mlc-ai/mlc-llm:cuda`
- **AMD**: `ghcr.io/mlc-ai/mlc-llm:rocm`
- **AMD gfx1100 (RDNA3)**: `registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100`
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

### Quantization Jobs
- **GGUF**: `ghcr.io/flexinfer/quantizer:gguf`
- **AWQ**: `ghcr.io/flexinfer/quantizer:awq`
- **GPTQ**: `ghcr.io/flexinfer/quantizer:gptq`
- **EXL2**: `ghcr.io/flexinfer/quantizer:exl2`
- **FP8**: `ghcr.io/flexinfer/quantizer:fp8`

### Image Pinning Best Practices

#### Why image pinning matters

Kubernetes uses `imagePullPolicy: IfNotPresent` by default for tagged images. This can cause issues when:

1. **Mutable tags** (like `:latest` or `:cuda`) are used
2. A node has an older version of the image cached
3. A new pod schedules to that node and gets the stale cached image
4. Different nodes in your cluster end up running different image versions

This leads to inconsistent behavior and makes debugging difficult.

#### Recommended patterns

**For production deployments:**

Pin images by digest rather than tag:

```yaml
# Instead of:
image: ghcr.io/mlc-ai/mlc-llm:cuda

# Use digest:
image: ghcr.io/mlc-ai/mlc-llm@sha256:abc123...
```

**For development/testing:**

Use explicit versioned tags with `imagePullPolicy: Always`:

```yaml
image: ghcr.io/mlc-ai/mlc-llm:v0.1.0
imagePullPolicy: Always
```

**To force re-pull on all nodes:**

If you need to update a mutable tag cluster-wide, either:
1. Change the image reference to use a digest
2. Delete the cached image on each node (`crictl rmi <image>`)
3. Use a different tag and update the Model spec

#### Prewarming large serving images

For heavyweight runtime images, configure Helm `imagePrewarm` profiles so
kubelet pulls the image before the first user request:

```yaml
imagePrewarm:
  enabled: true
  profiles:
    - name: gfx1100-serving
      nodeSelector:
        kubernetes.io/hostname: gpu-node-1
      images:
        - registry.example.com/flexinfer/vllm@sha256:...
        - registry.example.com/flexinfer/diffusers:rocm-gfx1100
```

Each profile creates a low-priority DaemonSet whose containers sleep after the
image is pulled. This keeps the image in use on the selected node, reducing the
first activation path to scheduling, cache staging, and backend/model loading.

#### Configuring default images

Override default backend images via controller environment variables:

```yaml
# In your controller Deployment or Helm values:
env:
  - name: DEFAULT_MLC_LLM_IMAGE
    value: "ghcr.io/mlc-ai/mlc-llm@sha256:abc123..."
  - name: DEFAULT_VLLM_IMAGE
    value: "vllm/vllm-openai:v0.4.0"
  - name: DEFAULT_MLC_LLM_IMAGE_GFX1100
    value: "registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100"
  - name: DEFAULT_VLLM_IMAGE_GFX1100
    value: "registry.harbor.lan/library/vllm-api:rocm-gfx1100"
```

See the [Backend Image Overrides](#backend-image-overrides) section for the complete list of environment variables.

#### Per-model image override

Individual models can specify a custom image in the spec:

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-model
spec:
  backend: mlc-llm
  source: HF://mlc-ai/Qwen3-8B-q4f16_1-MLC
  image: ghcr.io/mlc-ai/mlc-llm@sha256:abc123...  # Pinned image
```

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

### Model Spec (v1alpha2)

Key configuration fields in the `Model` CRD:

| Field | Type | Description |
|-------|------|-------------|
| `backend` | string | Backend type: ollama, vllm, mlc-llm, llamacpp, comfyui, diffusers, vllm-omni |
| `source` | string | Source URI: `HF://...`, `ollama://...`, `file://...`, `pvc://...` |
| `gpu.shared` | string | Shared GPU group name for time-sharing |
| `gpu.priority` | int | Preemption priority within shared group |
| `serverless.*` | object | Scale-to-zero behavior (idle/cold start timeouts) |
| `cache.*` | object | Cache strategy (Memory/SharedPVC/None) |
| `serviceLabels` | []string | Semantic labels for routing |

#### Shared-group leadership and warm-pinning

A shared GPU group serves one model at a time (`chooseSharedGroupLeader` in
`controllers/model_shared_gpu.go`). Leadership resolves in this order: an
operator `gpu.forcePromotion`, the anti-thrashing cooldown, the in-flight
cold-start, demand-based preemption (a higher-priority member with recent proxy
traffic preempts an idle leader), the Ready/recently-active member, an explicit
`warmPolicy: primary` member, and finally raw priority.

When **no** member has demand and none is Ready or recently active, a member
pinned warm via `serverless.minReplicas >= 1` is preferred over an idle
`minReplicas: 0` member even if the idle member has higher `gpu.priority`.
Without this, a higher-priority idle scale-to-zero member would permanently hold
the single slot and starve the warm incumbent. A higher-priority member still
preempts the warm incumbent the moment it receives real traffic, so this only
changes the otherwise-idle steady state.

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
