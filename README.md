![FlexInfer Banner](assets/banner.png)

# FlexInfer

**Smart GPU scheduling for AI workloads in Kubernetes**

FlexInfer is a Kubernetes-native solution that automatically discovers GPU capabilities, benchmarks AI model performance, and intelligently schedules workloads to optimize throughput and resource utilization.

## Current Status

FlexInfer is **functional and working** with comprehensive implementations of all core components:

- ✅ **Node Agent**: Hardware detection and labeling system
- ✅ **Controller Manager**: Complete CRD reconciliation with status management
- ✅ **Scheduler Extender**: Advanced filtering and scoring algorithms
- ✅ **Benchmarker**: Model performance measurement framework
- ✅ **API Types**: Comprehensive CRD definitions with status tracking
- ✅ **Test Suite**: Extensive unit tests across all components
- ✅ **Backend Plugin System**: Centralized backend configuration (ollama, vllm, mlc-llm, llamacpp, diffusers, comfyui)
- ✅ **Model v1alpha2**: Simplified single-resource API for homelab users

The project is ready for homelab deployment via the included Helm chart and the `docs/` guides (install, quickstart, operations).

## Features

### ✅ **Currently Implemented**

- **Hardware Discovery**: Automatic detection of GPU vendor, architecture, VRAM, and capabilities
- **Model Performance Benchmarking**: Automated measurement of tokens/second via real inference (Ollama, vLLM, MLC-LLM, llama.cpp)
- **Intelligent Scheduling**: Multi-factor scoring combining performance, utilization, and cost
- [x] **Model Caching**: Intelligent model artifact management with deduplication and pre-warming
- [x] **Resource Management**: Complete lifecycle management of AI workload deployments
- **Status Tracking**: Comprehensive status conditions and phase management
- **Event System**: Detailed event recording for debugging and monitoring
- **Metrics Collection**: Prometheus-compatible metrics for all components
- **Finalizer Handling**: Proper cleanup of resources and dependencies

### 🔄 **Partially Implemented**

- **Integration Testing**: Framework exists but needs more comprehensive scenarios
- **ModelCache Downloader**: Supports `huggingface-cli` in the controller; OCI-based sources are still TODO.
- **Scale-to-Zero Proxy**: Implemented and working; needs more real-world burn-in across backends and streaming clients.
- **Smart Routing (L7)**: Routing strategies exist (session affinity, prefix routing, least-loaded); needs more test coverage and performance validation under load.

### 📋 **Planned Features / Innovation Roadmap**

- **"Flash-Loader" Sidecar**: P2P/RDMA model loading to bypass disk I/O.
- **Context-Aware Router**: L7 Prefix-Caching router for "Chat with Doc" workloads.
- **Dynamic Multi-LoRA**: Hot-swapping adapters on running deployments.
- **Spot-Instance Resilience**: Proactive draining on termination notice.

## Architecture

FlexInfer consists of five cooperating components with a pluggable backend system:

```mermaid
graph TB
    subgraph "Control Plane"
        Agent[Node Agent<br/>Hardware Detection] --> Controller[Controller Manager<br/>CRD Reconciliation]
        Benchmarker[Benchmarker<br/>Performance Testing] --> Controller
        Controller --> Scheduler[Scheduler Extender<br/>Smart Placement]
    end

    subgraph "Backend Plugins"
        Controller --> Backend[Backend Registry]
        Backend --> Ollama[ollama]
        Backend --> VLLM[vllm]
        Backend --> MLC[mlc-llm]
        Backend --> LlamaCpp[llamacpp]
        Backend --> Diffusers[diffusers]
        Backend --> ComfyUI[comfyui]
    end

    Scheduler --> K8s[Kubernetes Scheduler]

    Agent -.-> Metrics[Prometheus Metrics]
    Controller -.-> Metrics
```

The **Backend Plugin System** centralizes all backend-specific configuration (images, ports, args, environment variables, probes) into a single interface, making it easy to add new inference backends.

See [AGENTS.md](AGENTS.md) for detailed component documentation.
See [docs/README.md](docs/README.md) for user/dev/spec docs (including the flexinfer-site playground schema reference).

## GPU Compatibility

FlexInfer supports multiple GPU architectures with backend-specific considerations:

| GPU Architecture | Compute Capability | Ollama | vLLM | MLC-LLM | Diffusers | Notes |
|-----------------|-------------------|--------|------|---------|-----------|-------|
| Maxwell (GTX 980 Ti) | 5.x | ✅ | ❌ | ✅* | ❌ | *FP32 only, see [Maxwell Guide](build/README-maxwell.md) |
| Pascal (GTX 1080) | 6.x | ✅ | ✅ | ✅ | ✅ | Full support |
| Volta+ (RTX 20xx+) | 7.0+ | ✅ | ✅ | ✅ | ✅ | Full support with Tensor Cores |
| AMD RDNA3 (RX 7900) | ROCm gfx1100 | ✅ | ✅ | ✅ | ✅** | ROCm 6.0+ required |

### Special GPU Documentation

- **[Maxwell GPUs (GTX 980 Ti, etc.)](build/README-maxwell.md)** - Running MLC-LLM on older NVIDIA GPUs without FP16 support
- **[Maxwell Backend Guide](docs/user/backends-maxwell.md)** - Maxwell-specific constraints and working model formats
- **[ROCm GFX1100 Backend Guide](docs/user/backends-rocm-gfx1100.md)** - ROCm 6.4+ tuning for RX 7900 (gfx1100)
- **[Deployment Runbook](docs/DEPLOYMENT_RUNBOOK.md#11-sdxl-vae-fp16-nan-on-rocm-gfx1100-amd-rdna3)** - SDXL VAE fp16 fix for AMD RDNA3 GPUs

**\*\*SDXL on AMD RDNA3**: Requires `madebyollin/sdxl-vae-fp16-fix` VAE due to NaN issues in standard VAE on gfx1100

### Mixed-Vendor k3s Notes (gfx1100 + Maxwell)

- Prefer setting `spec.gpu.vendor` explicitly in `ai.flexinfer/v1alpha2` Models when your cluster has both AMD and NVIDIA nodes.
- NVIDIA GPU workloads typically require `runtimeClassName: nvidia` (containerd + NVIDIA runtime) to get `/dev/nvidia*` injected reliably.
- AMD ROCm workloads require `/dev/kfd` + `/dev/dri` access; if `rocm-smi` is not available inside the agent container, FlexInfer falls back to sysfs and will use `rocminfo` (if present) to infer `gfx*` for image selection.

## Quick Start

### Prerequisites

- Kubernetes 1.25+
- GPU-enabled nodes with appropriate drivers
- Prometheus (optional, for metrics)

### Installation

```bash
# Helm (recommended)
helm upgrade --install flexinfer charts/flexinfer \
  --namespace flexinfer-system \
  --create-namespace
```

### Example Usage (v1alpha2 - Recommended)

The simplified `Model` CRD replaces the multi-file workflow with a single resource:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: qwen3-8b
spec:
  backend: mlc-llm
  source: HF://mlc-ai/Qwen3-8B-q4f16_1-MLC
  gpu:
    shared: homelab-gpu    # Models with same name share GPU
    priority: 100          # Higher = more important
```

That's it! Cache, serverless scaling, and GPU scheduling are all handled automatically.

#### Supported Backends

| Backend | Port | Description |
|---------|------|-------------|
| `ollama` | 11434 | Downloads models on-demand |
| `vllm` | 8000 | OpenAI-compatible, high throughput |
| `mlc-llm` | 8000 | Pre-compiled models, AMD ROCm support |
| `llamacpp` | 8080 | GGUF models, CPU/GPU hybrid |
| `diffusers` | 8000 | Image generation (Stable Diffusion) |
| `comfyui` | 8188 | Workflow-based image generation |
| `vllm-omni` | 8000 | Diffusion models with OpenAI API |

#### GPU Sharing

Multiple models can time-share a GPU using the `gpu.shared` field:

```yaml
# Both models share the same GPU, with qwen3 having higher priority
---
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: qwen3-8b
spec:
  backend: mlc-llm
  source: HF://mlc-ai/Qwen3-8B-q4f16_1-MLC
  gpu:
    shared: my-gpu
    priority: 100
---
apiVersion: ai.flexinfer/v1alpha2
kind: Model
metadata:
  name: llama3-8b
spec:
  backend: vllm
  source: HF://meta-llama/Meta-Llama-3-8B
  gpu:
    shared: my-gpu
    priority: 50
```

When a request arrives for the higher-priority model, the lower-priority one is preempted.

### Legacy API (v1alpha1)

The v1alpha1 API with separate `ModelDeployment`, `GPUGroup`, and `ModelCache` resources is still supported but deprecated:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: llama-7b
spec:
  backend: ollama
  model: llama2:7b
  replicas: 1
  modelCacheRef: llama-7b-cache
```

## Development

### Building

```bash
# Build all components
make build

# Run tests
make test

# Generate CRD manifests
make manifests
```

### Testing

```bash
# Run unit tests
make test

# Run specific component tests
go test ./controllers/...
go test ./agents/...
```

## TODO

### High Priority (Deployment Ready)

- [ ] **Complete Helm templates** - Finish charts/flexinfer/ with proper configurations
- [ ] **Installation documentation** - Step-by-step deployment guides
- [ ] **Integration tests** - End-to-end testing scenarios
- [x] **Real benchmarking** - Real inference benchmarking (Ollama, vLLM, MLC-LLM, llama.cpp)

### Medium Priority (Production Ready)

- [ ] **Performance optimization** - Memory usage and startup time improvements
- [ ] **Security hardening** - RBAC refinements and security scanning
- [ ] **Monitoring dashboards** - Grafana dashboards for operational visibility
- [ ] **Documentation** - API documentation and troubleshooting guides

### Low Priority (Advanced Features)

- [ ] **KV-Cache tiering** - Advanced memory management
- [ ] **Harbor OCI integration** - Direct model registry support
- [ ] **Multi-tenancy** - Namespace isolation features
- [ ] **CNCF submission** - Sandbox application preparation

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

## License

Apache 2.0 - see LICENSE file for details.
