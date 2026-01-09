# FlexInfer Agents & Runtime Components

FlexInfer is split into **five** cooperating executables (all written in Go).
This document explains what each agent does, how they communicate, and which options you can tune.

| Component | Binary | Runs on | Key responsibility |
|-----------|--------|---------|--------------------| 
| Node Agent | `flexinfer-agent` | Every GPU-capable node | Detect hardware & emit labels |
| Benchmarker | `flexinfer-bench` | Job pod (ephemeral) | Measure tokens/s per model-device pair |
| Controller Manager | `flexinfer-manager` | Control-plane | Reconciles `ModelDeployment` CRDs |
| Scheduler Extender | `flexinfer-sched` | Control-plane | Filters & scores nodes during scheduling |
| Metrics Exporter | built-in | All components | Collects Prometheus metrics for all of the above |

---

## 1. Node Agent (`flexinfer-agent`)

### What it detects

The node agent performs comprehensive hardware discovery and applies labels to nodes:

| Label | Example | Notes |
|-------|---------|-------|
| `flexinfer.ai/gpu.vendor` | `AMD` / `NVIDIA` | Populated from PCI ID detection |
| `flexinfer.ai/gpu.vram` | `24Gi` | Total VRAM per GPU in GiB |
| `flexinfer.ai/gpu.arch` | `gfx90a` / `sm_89` | GPU architecture identifier |
| `flexinfer.ai/gpu.int4` | `true` | INT4 quantization support capability |
| `flexinfer.ai/gpu.count` | `4` | Number of GPUs detected on the node |
| `flexinfer.ai/cpu.avx512` | `false` | CPU feature detection for fallback |

### Implementation Details

- **Hardware Detection**: Uses system calls and PCI enumeration to identify GPU hardware
- **Label Management**: Automatically applies and updates node labels based on detected capabilities
- **Error Handling**: Robust error handling for hardware detection failures
- **Caching**: Efficient caching of hardware information to reduce system load

### Config flags

| Flag | Default | Description |
|------|---------|-------------|
| `--interval` | `30s` | How often to re-probe hardware |
| `--metrics-port` | `9100` | Prometheus scrape port |
| `--label-prefix` | `flexinfer.ai/` | Customize if conflicts with other labelers |
| `--dry-run` | `false` | Log actions without applying labels |
| `--node-name` | auto-detected | Override node name for labeling |

---

## 2. Benchmarker (`flexinfer-bench`)

### Execution Model

The benchmarker runs as a Kubernetes Job, executed once per unique `ModelDeployment` × device class combination:

1. **Model Acquisition**: Pulls the model artifact into the node's shared cache path
2. **Container Launch**: Starts the specified backend container with configured resources
3. **Performance Testing**: Executes benchmark runs with configurable parameters
4. **Result Storage**: Publishes results to a `ConfigMap` for scheduler consumption

### Implementation Features

- **Mock Benchmarking**: Currently implements simulated performance metrics
- **Extensible Backend**: Designed to support multiple inference backends (Ollama, vLLM, etc.)
- **Resource Management**: Proper cleanup of test resources after completion
- **Error Recovery**: Robust error handling and retry logic

### Configuration Options

Available through the `ModelDeployment` CRD spec:

| CRD Field | Default | Purpose |
|-----------|---------|---------|
| `spec.benchmark.warmupIterations` | `2` | Number of warm-up runs before measurement |
| `spec.benchmark.minDuration` | `30s` | Minimum benchmark duration |
| `spec.benchmark.batchSize` | `128` | Tokens per benchmark batch |
| `spec.benchmark.iterations` | `5` | Number of measurement iterations |

---

## 3. Controller Manager (`flexinfer-manager`)

### Core Functionality

A comprehensive Kubernetes controller built with `controller-runtime` that provides:

- **CRD Reconciliation**: Complete lifecycle management of `ModelDeployment` resources
- **Status Management**: Detailed status tracking with conditions and phases
- **Event Recording**: Comprehensive event logging for debugging and monitoring
- **Finalizer Handling**: Proper cleanup of dependent resources
- **Benchmark Orchestration**: Automatic triggering of benchmarking jobs

### Status Tracking

The controller maintains detailed status information:

```go
type ModelDeploymentStatus struct {
    Phase      ModelDeploymentPhase `json:"phase,omitempty"`
    Conditions []metav1.Condition   `json:"conditions,omitempty"`
    Replicas   int32                `json:"replicas,omitempty"`
    Endpoints  []Endpoint           `json:"endpoints,omitempty"`
}
```

### Supported Phases

| Phase | Description |
|-------|-------------|
| `Pending` | Initial state, awaiting scheduling |
| `Benchmarking` | Performance measurement in progress |
| `Deploying` | Creating underlying Kubernetes resources |
| `Running` | Deployment is active and serving requests |
| `Failed` | Deployment has encountered an error |
| `Terminating` | Cleanup in progress |

### Environment variables

| Name | Default | Description |
|------|---------|-------------|
| `MODEL_CACHE_PATH` | `/models` | Shared model storage location |
| `DEFAULT_BACKEND_IMAGE` | `ollama/ollama:latest` | Legacy: Default NVIDIA backend (deprecated) |
| `DEFAULT_BACKEND_IMAGE_NVIDIA` | `ollama/ollama:latest` | NVIDIA (CUDA) inference backend |
| `DEFAULT_BACKEND_IMAGE_AMD` | `ollama/ollama:rocm` | AMD (ROCm) inference backend |
| `DEFAULT_BACKEND_IMAGE_INTEL` | `ollama/ollama:latest` | Intel inference backend |
| `BENCHMARK_IMAGE` | `flexinfer/benchmarker:latest` | Benchmarker container image |
| `METRICS_PORT` | `8080` | Controller metrics endpoint |

---

## 4. Scheduler Extender (`flexinfer-sched`)

### Scheduling Algorithm

The scheduler extender implements a sophisticated two-phase approach:

#### Filter Phase
Eliminates nodes that cannot satisfy the workload requirements:
- **GPU Requirements**: Matches requested GPU count and type
- **VRAM Requirements**: Ensures sufficient memory for the model
- **Quantization Support**: Verifies hardware supports requested quantization
- **Architecture Compatibility**: Matches model requirements with GPU architecture

#### Score Phase
Ranks suitable nodes using a weighted scoring algorithm:

```go
score = (TPS_normalized × TPS_weight) - (GPU_util × Util_weight) - (Cost × Cost_weight)
```

### Scoring Factors

| Factor | Weight | Description |
|--------|--------|-------------|
| Tokens/Second | 0.7 | Benchmarked performance for model-device pair |
| GPU Utilization | 0.2 | Current GPU resource usage |
| Node Cost | 0.1 | Cost per hour (from node annotations) |

### Configuration

Scoring weights can be configured via Helm values:

```yaml
scheduler:
  weights:
    tps: 0.8      # Performance weight
    util: 0.1     # Utilization weight  
    cost: 0.1     # Cost weight
  port: 8000      # Extender webhook port
```

### Implementation Details

- **Webhook Server**: HTTP server implementing Kubernetes scheduler extender protocol
- **Concurrent Processing**: Efficient handling of multiple scheduling requests
- **Caching**: Performance data caching for improved response times
- **Fallback Logic**: Graceful degradation when benchmark data is unavailable

---

## 5. Metrics Exporter

### Architecture

The metrics exporter is implemented as a shared Go module embedded in every binary, providing consistent observability across all components.

### Available Metrics

| Metric | Labels | Description |
|--------|--------|-------------|
| `flexinfer_tokens_per_second` | `model`, `backend`, `node` | Model performance metrics |
| `flexinfer_model_load_seconds` | `model`, `node` | Model loading time |
| `flexinfer_gpu_temperature_celsius` | `gpu`, `node` | GPU temperature monitoring |
| `flexinfer_controller_reconciles_total` | `result` | Controller reconciliation count |
| `flexinfer_scheduler_requests_total` | `phase`, `result` | Scheduler extender request count |
| `flexinfer_benchmark_duration_seconds` | `model`, `device_class` | Benchmark execution time |

### Prometheus Configuration

```yaml
scrape_configs:
- job_name: 'flexinfer-agent'
  kubernetes_sd_configs:
  - role: node
  relabel_configs:
  - source_labels: [__meta_kubernetes_node_label_flexinfer_ai_enabled]
    regex: "true"
    action: keep

- job_name: 'flexinfer-controller'
  static_configs:
  - targets: ['flexinfer-controller:8080']

- job_name: 'flexinfer-scheduler'
  static_configs:
  - targets: ['flexinfer-scheduler:8000']
```

---

## Communication Flow

```mermaid
sequenceDiagram
    participant User
    participant Controller
    participant Benchmarker
    participant Scheduler
    participant K8sScheduler
    participant Agent

    User->>Controller: kubectl apply ModelDeployment
    Controller->>Controller: Validate and set status to Pending
    
    alt Benchmark data missing
        Controller->>Benchmarker: Create benchmark Job
        Benchmarker->>Benchmarker: Run performance tests
        Benchmarker->>Controller: Store results in ConfigMap
    end
    
    Controller->>Controller: Create underlying Deployment
    Controller->>Controller: Update status to Deploying
    
    K8sScheduler->>Scheduler: Filter/Score request
    Scheduler->>Scheduler: Apply filters and scoring
    Scheduler->>K8sScheduler: Return ranked nodes
    
    K8sScheduler->>Agent: Schedule pod to selected node
    Agent->>Agent: Validate hardware compatibility
    
    Controller->>Controller: Update status to Running
```

---

## Advanced Features

### Status Conditions

The controller maintains detailed condition information:

- `Available`: Deployment is ready to serve traffic
- `Progressing`: Deployment is being updated
- `ReplicaFailure`: Unable to create desired replicas
- `BenchmarkComplete`: Performance measurement finished
- `NodeSelected`: Scheduling completed successfully

### Event Recording

Comprehensive event logging covers:
- Deployment lifecycle events
- Benchmark execution status
- Scheduling decisions and outcomes
- Error conditions and recovery actions

### Resource Management

- **Finalizers**: Proper cleanup ordering with `flexinfer.ai/finalizer`
- **Owner References**: Garbage collection of dependent resources
- **Resource Quotas**: Integration with Kubernetes resource management

---

## Development and Testing

### Building Components

```bash
# Build all binaries
make build

# Build specific component
make build-agent
make build-controller
make build-scheduler
make build-benchmarker
```

### Testing

```bash
# Run all tests
make test

# Component-specific tests
go test ./agents/agent/...
go test ./controllers/...
go test ./scheduler/...
```

### Local Development

```bash
# Run controller locally
export KUBECONFIG=~/.kube/config
go run cmd/flexinfer-manager/main.go

# Run with debug logging
go run cmd/flexinfer-manager/main.go --log-level=debug
```

---

## Troubleshooting

### Common Issues

1. **Node labels not appearing**: Check agent logs and RBAC permissions
2. **Benchmarks not triggering**: Verify benchmark job creation and ConfigMap access
3. **Scheduling failures**: Check scheduler extender logs and webhook connectivity
4. **Status not updating**: Verify controller reconciliation loops and event recording

### Debug Commands

```bash
# Check node labels
kubectl get nodes --show-labels | grep flexinfer

# View controller logs
kubectl logs -n flexinfer-system deployment/flexinfer-controller

# Check benchmark results
kubectl get configmaps -n flexinfer-system -l app=flexinfer-benchmarks

# View scheduler decisions
kubectl logs -n kube-system deployment/flexinfer-scheduler
```

---

## Future Roadmap

### Phase 2 (Production Ready)
- KV-Cache tiering: GPU HBM to host DDR memory management
- Harbor model OCI plugin: Direct model registry integration
- Advanced monitoring: Detailed performance analytics
- Multi-tenancy: Namespace isolation and resource quotas

### Phase 3 (Enterprise Features)  
- Autoscaling: HPA integration with custom metrics
- Cost optimization: Advanced cost-aware scheduling
- Security: Enhanced RBAC and admission controllers
- Compliance: Audit logging and policy enforcement

---

## Feedback & Support

- **GitHub Issues**: [FlexInfer Issues](https://github.com/flexinfer/flexinfer/issues)
- **Discussions**: [GitHub Discussions](https://github.com/flexinfer/flexinfer/discussions)
- **Discord**: #flexinfer channel on Llama.cpp Discord

Happy hacking! 🚀

## 6. Model CRDs for Deployment

FlexInfer uses Custom Resource Definitions (CRDs) to manage model deployments declaratively.

### ModelDeployment CRD

The `ModelDeployment` CRD is the primary way to deploy inference workloads:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-abliterated
  namespace: flexinfer-system
  labels:
    app: qwen3-abliterated
    backend: mlc-llm
    gpu-vendor: amd
spec:
  backend: mlc-llm           # Backend: mlc-llm, ollama, vllm, llama-cpp
  model: Qwen3-8B-abliterated-q4f32_1-MLC  # Model name or path
  replicas: 1

  # Backend-specific configuration (for MLC-LLM)
  mlcllm:
    mode: server             # local, interactive, server
    modelLibPath: /models/Qwen3-8B-abliterated-q4f32_1-MLC/lib_rocm_gfx1100.so
    gpuMemoryBytes: 23000000000
    jitPolicy: "OFF"         # ON, OFF, REDO, READONLY
    overrides:
      prefillChunkSize: 512
      maxTotalSeqLength: 32768

  # Resource requests/limits
  resources:
    requests:
      amd.com/gpu: "1"
      cpu: "4"
      memory: 16Gi
    limits:
      amd.com/gpu: "1"
      cpu: "8"
      memory: 24Gi

  # Node scheduling
  nodeSelector:
    kubernetes.io/hostname: cblevins-5930k  # Target specific node

  # Health checks
  healthCheck:
    initialDelaySeconds: 120
    periodSeconds: 30
    timeoutSeconds: 10
    failureThreshold: 3
```

### ModelCache CRD

The `ModelCache` CRD manages model storage and caching:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-8b-abliterated-mlc
  namespace: flexinfer-system
spec:
  storageStrategy: SharedPVC  # SharedPVC, NodeLocal, OCI
  existingClaimName: mlc-models-nfs
  modelPath: Qwen3-8B-abliterated-q4f32_1-MLC
  source: huggingface://huihui-ai/Qwen3-8B-abliterated
  storageSize: 20Gi
```

### Scheduling to AMD 7900XTX Nodes

The cluster has two AMD 7900XTX GPU nodes:
- `cblevins-5930k` - Intel i7-5930K + 7900XTX
- `cblevins-7900xtx` - AMD Zen4 + 7900XTX

#### Targeting a Specific Node

```yaml
spec:
  nodeSelector:
    kubernetes.io/hostname: cblevins-5930k
```

#### Targeting Any AMD GPU Node

```yaml
spec:
  nodeSelector:
    flexinfer.ai/gpu.vendor: AMD
```

#### Example: Split Models Across Both 7900XTX Nodes

Deploy 8B on cblevins-5930k:
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-abliterated
spec:
  backend: mlc-llm
  model: Qwen3-8B-abliterated-q4f32_1-MLC
  nodeSelector:
    kubernetes.io/hostname: cblevins-5930k
  mlcllm:
    mode: server
    modelLibPath: /models/lib_rocm_gfx1100.so
```

Deploy 32B on cblevins-7900xtx:
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-32b
spec:
  backend: mlc-llm
  model: Qwen3-32B-q4f16_1-MLC
  nodeSelector:
    kubernetes.io/hostname: cblevins-7900xtx
  mlcllm:
    mode: local
```

### MLC-LLM Backend Configuration

#### Mode Selection

| Mode | Max Batch | KV Cache | Use Case |
|------|-----------|----------|----------|
| `local` | 4 | ~8k tokens | Low memory, interactive use |
| `interactive` | 1 | Full context | Single user, maximum context |
| `server` | 128 (configurable) | Max possible | High throughput, multiple users |

#### Memory Optimization (Server Mode)

In server mode, MLC-LLM pre-allocates KV cache based on batch size and context length. Use the `overrides` section to control memory usage:

| Override | Purpose | Example |
|----------|---------|---------|
| `maxNumSequence` | Max concurrent requests (batch size) | `2` for low concurrency |
| `maxTotalSeqLength` | Total KV cache tokens | `131072` for 128k context |
| `gpuMemoryUtilization` | Fraction of VRAM to use (0.0-1.0) | `"0.85"` for 85% |
| `prefillChunkSize` | Prefill attention chunk size | `2048` for throughput |

**Memory Formula (approximate):**
```
Total VRAM = Model Weights + KV Cache + Temp Buffers

KV Cache ≈ max_num_sequence × max_total_seq_length × ~55 bytes/token (for Qwen 7B)

Example: 2 sequences × 131072 tokens × 55 bytes = ~14.4GB KV cache
```

**Example: 7B Model with 128k Context on 24GB GPU**
```yaml
mlcllm:
  mode: server
  modelLibPath: /models/Qwen2.5-7B-abliterated-v2-q4f32_1-MLC/lib_rocm_gfx1100.so
  overrides:
    maxNumSequence: 2           # 2 concurrent requests
    maxTotalSeqLength: 131072   # 128k total context
    prefillChunkSize: 2048      # Larger chunks for throughput
    gpuMemoryUtilization: "0.85" # Use 85% of 24GB = ~20GB
```

This configuration yields ~20GB VRAM usage (85% of 24GB), leaving headroom for stability.

#### Quantization & TVM Bug Workaround

**Important**: Use `q4f32_1` quantization (NOT `q4f16`) for Qwen3 models on ROCm to avoid TVM segfault bug.

See: https://github.com/mlc-ai/mlc-llm/issues/3283

#### Pre-compiled vs JIT Compilation

For production, always use pre-compiled model libraries:

```yaml
mlcllm:
  modelLibPath: /models/Qwen3-8B-abliterated-q4f32_1-MLC/lib_rocm_gfx1100.so
  jitPolicy: "OFF"
```

JIT compilation is useful for development but adds significant startup time (2-5 minutes).

### Common Operations

#### View all model deployments
```bash
kubectl get modeldeployment -n flexinfer-system
```

#### Check model cache status
```bash
kubectl get modelcache -n flexinfer-system
```

#### Scale a deployment
```bash
kubectl scale deployment qwen3-8b-abliterated -n flexinfer-system --replicas=2
```

#### Update node selector
```bash
kubectl patch modeldeployment qwen3-8b-abliterated -n flexinfer-system \
  --type=merge -p='{"spec":{"nodeSelector":{"kubernetes.io/hostname":"cblevins-7900xtx"}}}'
```

#### Check pod placement
```bash
kubectl get pods -n flexinfer-system -o wide | grep qwen
```

---

## Benchmark Results (AMD 7900XTX)

Tested January 2026 on AMD Radeon RX 7900 XTX (24GB VRAM) nodes.

| Model | Quantization | Context | Tokens/sec | VRAM Usage |
|-------|--------------|---------|------------|------------|
| Qwen3-8B-Abliterated | q4f32_1 | 32k | **107 tok/s** | ~12GB |
| Qwen3-32B | q4f16_1 | 32k | **37 tok/s** | ~22GB |

### Benchmark Methodology

Run from within cluster using Python for accurate timing:

```bash
kubectl run bench --image=python:3.11-alpine --restart=Never -- sleep 600
kubectl exec bench -- python3 -c '
import urllib.request, json, time

url = "http://MODEL-SERVICE.flexinfer-system.svc.cluster.local:8000/v1/chat/completions"
data = json.dumps({
    "model": "/models",
    "messages": [{"role": "user", "content": "Your prompt here"}],
    "max_tokens": 200
}).encode()

for i in range(5):
    start = time.time()
    req = urllib.request.Request(url, data=data, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=60) as resp:
        result = json.loads(resp.read())
    tokens = result["usage"]["completion_tokens"]
    elapsed = time.time() - start
    print(f"Run {i+1}: {tokens} tokens in {elapsed:.2f}s = {tokens/elapsed:.1f} tok/s")
'
kubectl delete pod bench
```

---

## Operational Workflows

### Deploying a New Model

1. **Create ModelCache** (if using shared storage):
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: my-model-mlc
  namespace: flexinfer-system
spec:
  storageStrategy: SharedPVC
  existingClaimName: mlc-models-nfs
  modelPath: Model-Name-q4f32_1-MLC
```

2. **Create ModelDeployment**:
```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: my-model
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  model: /models/Model-Name-q4f32_1-MLC
  modelCacheRef: my-model-mlc
  mlcllm:
    mode: server
    modelLibPath: /models/Model-Name-q4f32_1-MLC/lib_rocm_gfx1100.so
    jitPolicy: "OFF"
    overrides:
      maxNumSequence: 2
      maxTotalSeqLength: 131072
      gpuMemoryUtilization: "0.85"
  resources:
    limits:
      amd.com/gpu: "1"
  nodeSelector:
    kubernetes.io/hostname: target-node
```

3. **Verify deployment**:
```bash
kubectl get modeldeployment -n flexinfer-system
kubectl get pods -n flexinfer-system -o wide | grep my-model
```

### Updating Chart/CRDs

```bash
# 1. Make changes to types.go, controller, etc.
# 2. Regenerate CRDs
make manifests

# 3. Run tests
make test

# 4. Bump chart version in charts/flexinfer/Chart.yaml
# 5. Copy updated CRDs to chart
cp config/crd/*.yaml charts/flexinfer/crds/

# 6. Commit and push
git add -A && git commit -m "feat: description" && git push

# 7. Wait for CI, then reconcile
flux reconcile source git flexinfer -n flux-system
flux reconcile helmrelease flexinfer -n flexinfer-system

# 8. Apply CRD manually (Helm doesn't auto-update CRDs)
kubectl apply -f charts/flexinfer/crds/
```

### LiteLLM Integration

FlexInfer models are auto-discovered by LiteLLM via service annotations:

```yaml
# Add to ModelDeployment
spec:
  litellm:
    enabled: true
    servedModelName: "my-model-name"
    aliases:
      - "model-alias-1"
      - "model-alias-2"
```

Access via LiteLLM:
```bash
curl http://litellm.ai.svc:8000/v1/chat/completions \
  -H "Authorization: Bearer sk-litellm-master-key" \
  -H "Content-Type: application/json" \
  -d '{"model": "my-model-name", "messages": [...]}'
```

---

## Known Issues & Improvements Needed

### Benchmarking
- [ ] FlexInfer benchmarker (`flexinfer-bench`) needs better CLI tooling
- [ ] No built-in way to trigger benchmarks from command line
- [ ] Results not automatically stored in ModelDeployment status

### LiteLLM Discovery
- [ ] Service annotations not always applied by controller
- [ ] Need to verify controller is adding `litellm.flexinfer.ai/*` annotations

### Documentation
- [ ] Need end-to-end deployment guide
- [ ] MLC-LLM compilation workflow not documented

---

## Planning
- See `ROADMAP.md` for project status and plans.