# FlexInfer Agents & Runtime Components

FlexInfer is split into **five** cooperating executables (all written in Go).
This document explains what each agent does, how they communicate, and which options you can tune.

| Component | Binary | Runs on | Key responsibility |
|-----------|--------|---------|--------------------| 
| Node Agent | `flexinfer-agent` | Every GPU-capable node | Detect hardware & emit labels |
| Benchmarker | `flexinfer-bench` | Job pod (ephemeral) | Measure tokens/s per model-device pair |
| Controller Manager | `flexinfer-manager` | Control-plane | Reconciles `Model` (v1alpha2) and legacy v1alpha1 CRDs |
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

The node agent also applies node annotations that the scheduler can use as heuristic inputs:

| Annotation | Example | Notes |
|-----------|---------|-------|
| `flexinfer.ai/gpu.util` | `12.34` | Average GPU utilization (%) across all GPUs |
| `flexinfer.ai/gpu-free-memory` | `24550` | Sum of free VRAM across GPUs (MB) |
| `flexinfer.ai/kv-cache-usage` | `0.1234` | Best-effort KV-cache usage ratio from backend pod metrics |

### Implementation Details

- **Hardware Detection**: Uses system calls and PCI enumeration to identify GPU hardware
- **Label Management**: Automatically applies and updates node labels based on detected capabilities
- **Error Handling**: Robust error handling for hardware detection failures
- **Caching**: Efficient caching of hardware information to reduce system load

### GPU Detection Sources (gfx1100 + Maxwell focus)

- **NVIDIA**: uses `nvidia-smi` (direct, then `chroot /host nvidia-smi` for glibc compatibility) to get architecture (`sm_52` for Maxwell), VRAM, and utilization.
- **AMD**: prefers `rocm-smi` + `rocminfo` (direct, then `chroot /host ...` as a fallback). If those utilities are unavailable, it falls back to sysfs VRAM detection and may omit `flexinfer.ai/gpu.arch`.

When multiple GPUs are present, the agent chooses the "best" representative values (highest major `gfx*` generation / highest `sm_*`, and max VRAM) so scheduling stays stable on mixed or heterogeneous nodes.

If the agent cannot list pods in `flexinfer-system`, it will still label hardware but may set telemetry annotations like `flexinfer.ai/gpu-free-memory` or `flexinfer.ai/kv-cache-usage` to `0`, which reduces scheduler placement quality.

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

The benchmarker runs as a Kubernetes Job, executed once per unique model × device class combination:

1. **Model Acquisition**: Pulls the model artifact into the node's shared cache path
2. **Container Launch**: Starts the specified backend container with configured resources
3. **Performance Testing**: Executes benchmark runs with configurable parameters
4. **Result Storage**: Publishes results to a `ConfigMap` for scheduler consumption

### Implementation Features

- **Real Benchmarking**: Runs real inference requests through `flexinfer-proxy` and records tokens/sec into a ConfigMap (used by the scheduler extender)
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
score = (TPS_normalized × TPS_weight) - (GPU_util × Util_weight) - (Cost × Cost_weight) - (KV_cache × Cache_weight) + (FreeVRAMRatio × VRAMFree_weight)
```

### Scoring Factors

| Factor | Weight | Description |
|--------|--------|-------------|
| Tokens/Second | 0.7 | Benchmarked performance for model-device pair |
| GPU Utilization | 0.2 | Current GPU resource usage |
| Node Cost | 0.1 | Cost per hour (from node annotations) |
| Free VRAM Ratio | 10.0 | Bonus: `free_vram / total_vram` headroom (uses agent labels + annotations) |

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

Happy hacking!

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
  storageStrategy: SharedPVC  # SharedPVC, NodeLocal, Memory
  existingClaimName: mlc-models-nfs
  modelPath: Qwen3-8B-abliterated-q4f32_1-MLC
  source: huggingface://huihui-ai/Qwen3-8B-abliterated
  storageSize: 20Gi
```

#### Storage Strategies

| Strategy | Location | Switch Time | Use Case |
|----------|----------|-------------|----------|
| `SharedPVC` | NFS/RWX PVC | ~4-5s | Shared models across nodes |
| `NodeLocal` | `/var/lib/flexinfer/models` | ~4-5s | Per-node disk cache |
| `Memory` | `/dev/shm/flexinfer` | **~2-3s** | RAM cache for fast switching |

### RAM-Cached Models (Memory Strategy)

The `Memory` storage strategy uses `/dev/shm` (tmpfs) to cache models in RAM, providing ~40-50% faster cold starts compared to NVMe-based loading.

#### Prerequisites

```bash
# Check /dev/shm size on GPU node (default: 50% of RAM)
ssh gpu-node-1 "df -h /dev/shm"
# Example: tmpfs 32G 0 32G 0% /dev/shm

# Optionally resize if needed
ssh gpu-node-1 "sudo mount -o remount,size=40G /dev/shm"
```

#### Example: RAM-Cached Multi-Model Setup

```yaml
# ModelCache with RAM storage
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-8b-ram
spec:
  source: HF://mlc-ai/Qwen3-8B-abliterated-q4f16_1-MLC
  storageStrategy: Memory
  nodeSelector:
    kubernetes.io/hostname: gpu-node-1
---
# ModelDeployment using RAM cache
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-8b-chat
spec:
  backend: mlc-llm
  model: Qwen3-8B-abliterated-q4f16_1-MLC
  modelCacheRef: qwen3-8b-ram
  replicas: 1
  minReplicas: 0  # Enable scale-to-zero
  nodeSelector:
    kubernetes.io/hostname: gpu-node-1
```

#### RAM Cache Benefits

| Benefit | Description |
|---------|-------------|
| **Faster cold start** | ~2-3s from RAM vs ~4-5s from NVMe |
| **Full VRAM per model** | 100% GPU memory available (no sharing) |
| **More models cached** | RAM cheaper than VRAM; cache 5+ models |
| **Serverless ready** | Works with scale-to-zero for on-demand activation |

#### Expected Performance

| Action | Time | Notes |
|--------|------|-------|
| First cold start (NFS → RAM) | ~30s | One-time cache population |
| Subsequent cold start (RAM → VRAM) | ~2-3s | Fast RAM-based loading |
| Model switch (scale down A, up B) | ~3-4s | Concurrent scale operations |
| Hot request (already in VRAM) | <100ms | No loading needed |

#### Checking Cache Status

```bash
# View all model caches and their strategies
flexinfer cache status

# Example output:
# NAME           STRATEGY   PATH                             READY  SOURCE
# qwen3-8b-ram   Memory     /dev/shm/flexinfer/qwen3-8b-ram  Ready  HF://mlc-ai/...
# qwen3-3b-ram   Memory     /dev/shm/flexinfer/qwen3-3b-ram  Ready  HF://mlc-ai/...
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

## FlexInfer CLI

The `flexinfer` CLI provides a convenient way to manage ModelDeployments from the command line.

### Installation

```bash
# Build and install
make build-cli
make install-cli  # Copies to /usr/local/bin

# Or build only
make build-cli
./bin/flexinfer --help
```

### Commands

| Command | Description |
|---------|-------------|
| `flexinfer list` | List all ModelDeployments with status, TPS, and GPU info |
| `flexinfer status <name>` | Detailed status of a deployment (conditions, endpoints, events) |
| `flexinfer logs <name>` | Stream logs from a deployment's pods |
| `flexinfer delete <name>` | Delete a ModelDeployment |
| `flexinfer scale <name> <replicas>` | Scale a deployment |
| `flexinfer cache status` | Show status of all ModelCaches (strategy, path, ready state) |

### Examples

```bash
# List all deployments
flexinfer list
NAME              BACKEND   MODEL                      STATUS    TPS       GPU
qwen3-8b-amd      mlc-llm   Qwen3-8B-Abliterated       Running   107/s     7900XTX (gfx1100)
qwen3-32b-amd     mlc-llm   Qwen3-32B                  Running   37/s      7900XTX (gfx1100)

# Get detailed status
flexinfer status qwen3-8b-amd

# Follow logs
flexinfer logs qwen3-8b-amd -f

# Scale to zero (serverless)
flexinfer scale qwen3-8b-amd 0

# Scale back up
flexinfer scale qwen3-8b-amd 1

# Delete a deployment
flexinfer delete qwen3-8b-amd

# View model caches and their storage strategies
flexinfer cache status
NAME           STRATEGY   PATH                             READY  SOURCE
qwen3-8b-ram   Memory     /dev/shm/flexinfer/qwen3-8b-ram  Ready  HF://mlc-ai/...
qwen3-3b-ram   Memory     /dev/shm/flexinfer/qwen3-3b-ram  Ready  HF://mlc-ai/...
```

### Flags

| Flag | Description |
|------|-------------|
| `-n, --namespace` | Kubernetes namespace (default: `flexinfer-system`) |
| `-A, --all-namespaces` | List across all namespaces |
| `--kubeconfig` | Path to kubeconfig file |

---

## GPU Compatibility Matrix

| Backend | RDNA3 (7900XTX) | Maxwell (980Ti) | Notes |
|---------|-----------------|-----------------|-------|
| Ollama | ✅ Full | ✅ Full | Universal compatibility |
| vLLM | ✅ Full | ❌ Not supported | Requires sm_70+ |
| MLC-LLM | ✅ Full | ⚠️ Pre-compiled only | Needs `modelLibPath` |
| llama.cpp | ✅ Full | ✅ Full | GGUF format |

### Maxwell (GTX 980 Ti) Configuration

Maxwell GPUs (compute capability 5.x) require special handling:

1. **vLLM**: Not supported - use Ollama or llama.cpp instead
2. **MLC-LLM**: Requires pre-compiled model library
   ```yaml
   spec:
     backend: mlc-llm
     mlcllm:
       modelLibPath: /models/Model-q4f32_1-MLC/lib_cuda_maxwell.so
       gpuMemoryBytes: 5000000000  # 5GB limit for 6GB card
       jitPolicy: "OFF"
   ```

### RDNA3 (RX 7900 XTX) Configuration

Full support across all backends:

```yaml
spec:
  backend: mlc-llm
  mlcllm:
    mode: server
    modelLibPath: /models/Model-MLC/lib_rocm_gfx1100.so
    overrides:
      maxNumSequence: 2
      maxTotalSeqLength: 131072
      gpuMemoryUtilization: "0.85"
  nodeSelector:
    amd.com/gpu.arch: gfx1100
```

### ROCm gfx1100 Stability Requirements

PyTorch-based backends (diffusers, vLLM) on gfx1100 (RX 7900 XTX) require specific environment variables to prevent SIGSEGV crashes:

| Environment Variable | Value | Purpose |
|---------------------|-------|---------|
| `HSA_OVERRIDE_GFX_VERSION` | `11.0.0` | Enables RDNA3 GPU support |
| `PYTORCH_ROCM_ARCH` | `gfx1100` | Target architecture for PyTorch |
| `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL` | `1` | **Critical**: Enables experimental AOTriton flash attention |
| `HIP_VISIBLE_DEVICES` | `0` | GPU device selection |
| `ROCR_VISIBLE_DEVICES` | `0` | ROCm runtime device selection |

**Note**: The `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1` setting is essential for stability on gfx1100. Without it, PyTorch operations like attention can trigger SIGSEGV crashes.

These variables are automatically injected by the `ROCmEnvVars()` helper in `backend/interface.go` and are baked into all ROCm Dockerfiles.

---

## Known Issues & Improvements Needed

### Resolved
- [x] TPS now populated from benchmark results
- [x] GPU validation prevents vLLM on Maxwell
- [x] CLI provides easy model management
- [x] ROCm gfx1100 SIGSEGV crashes fixed via `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1`
- [x] Diffusers backend working for image generation on AMD GPUs

### Benchmarking
- [ ] FlexInfer benchmarker needs direct CLI integration
- [ ] No built-in way to trigger benchmarks from command line

### LiteLLM Discovery
- [ ] Service annotations not always applied by controller
- [ ] Need to verify controller is adding `litellm.flexinfer.ai/*` annotations

### Documentation
- [ ] Need end-to-end deployment guide
- [ ] MLC-LLM compilation workflow not documented

---

## Current Deployment Status (January 2026)

### Cluster Layout

#### GPU Nodes

| Node | CPU | GPU | VRAM | Role |
|------|-----|-----|------|------|
| `cblevins-5930k` | Intel i7-5930K | AMD RX 7900 XTX | 24GB | Fast models (8B, 4B) |
| `cblevins-7900xtx` | AMD Zen4 | AMD RX 7900 XTX | 24GB | Quality models (14B, 32B) + ComfyUI |
| `cblevins-gtx980ti` | Intel i7 (legacy) | NVIDIA GTX 980 Ti (Maxwell, sm_52) | 6GB | Legacy / small models (FP32 MLC) |

#### GPUGroups

| GPUGroup | Node | Strategy | Models | Notes |
|----------|------|----------|--------|-------|
| **fast-models** | cblevins-5930k | Exclusive | qwen3-8b-fast (100), qwen3-4b-tiny (80) | Quick responses |
| **quality-models** | cblevins-7900xtx | Exclusive | qwen3-32b-best (100), qwen3-14b-quality (90), deepseek-r1-reasoning (80), sdxl-turbo-fast (50) | High quality |

#### ModelDeployments

| Model | Backend | GPUGroup | Status | TPS | Image |
|-------|---------|----------|--------|-----|-------|
| bge-large-embeddings | tei | - | Running | 69.7 | `ghcr.io/huggingface/text-embeddings-inference:cpu-1.8` |
| qwen3-8b-fast | mlc-llm | fast-models | Running | 106.0 | `registry.harbor.lan/library/mlc-llm:latest` |
| qwen3-4b-tiny | mlc-llm | fast-models | Idle | 144.9 | `registry.harbor.lan/library/mlc-llm:latest` |
| sdxl-turbo-fast | **diffusers** | quality-models | Running | - | `registry.harbor.lan/library/diffusers-api:rocm-latest` |
| qwen3-32b-best | mlc-llm | quality-models | Idle | - | Needs ROCm 6.4 image |
| qwen3-14b-quality | mlc-llm | quality-models | Idle | - | Needs ROCm 6.4 image |
| deepseek-r1-reasoning | mlc-llm | quality-models | Active | - | Needs ROCm 6.4 image |

#### ModelCaches (All Ready)

All MLC model weights are pre-cached on NFS PVC (`mlc-models-nfs`):
- `qwen3-0.6b-mlc`, `qwen3-4b-mlc`, `qwen3-8b-abliterated-mlc`
- `qwen3-14b-mlc`, `qwen3-32b-mlc`
- `deepseek-r1-14b-mlc`
- `sdxl-turbo-nfs`

### ROCm 6.4 MLC-LLM Build Status

ROCm 6.4+ is the stable baseline for gfx1100, but you may choose between:

- a **gfx1100-optimized image** (`build/Dockerfile.mlc-rocm64-gfx1100`) for RX 7900 class GPUs
- a **generic ROCm 6.4 source build** (`build/Dockerfile.mlc-rocm64-full`) when you want a "kitchen sink" build artifact to derive from

#### Available Dockerfiles

| Dockerfile | Purpose | CI Job | Image Tag |
|------------|---------|--------|-----------|
| `build/Dockerfile.mlc-rocm64-gfx1100` | ROCm 6.4 optimized for gfx1100 | (manual/local) | `flexinfer/mlc-llm:rocm64-gfx1100` |
| `build/Dockerfile.mlc-rocm64-full` | ROCm 6.4 source build (generic) | `publish_mlcllm_rocm64` | `library/mlc-llm:rocm64-src` |
| `build/Dockerfile.mlc-cuda-maxwell` | CUDA 11.8 for Maxwell (sm_52) | `publish_mlcllm_maxwell` | `flexinfer/mlc-llm:cuda-maxwell-v7` |
| `build/Dockerfile.mlc-cuda` | CUDA generic backend | `publish_mlcllm_cuda` | `flexinfer/mlc-llm:cuda` |
| `build/Dockerfile.mlc-rocm` | ROCm generic backend | `publish_mlcllm_rocm` | `flexinfer/mlc-llm:rocm` |

#### Building Images

**Local builds** (for testing or when CI is slow):
```bash
# ROCm 6.4 gfx1100 (~3 hours, use 7900xtx docker context)
make build-mlc-rocm64 push-mlc-rocm64

# Maxwell sm_52 (~2 hours)
make build-mlc-maxwell push-mlc-maxwell

# Verify all images exist
make verify-images
```

**CI builds** (manual trigger in GitLab):
- Go to CI/CD > Pipelines > Run Pipeline
- Select `publish_mlcllm_rocm64` or `publish_mlcllm_maxwell`

#### Target Image

Recommended for RX 7900 (gfx1100):

`registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100`

Fallback / base artifact:

`registry.harbor.lan/library/mlc-llm:rocm64-src`

This image is referenced in `values.yaml` under `mlcllm.rocmImage` but doesn't exist yet.

#### Build Command

```bash
cd /Users/cblevins/workspace/services/flexinfer

# GFX1100 optimized (recommended)
docker build -f build/Dockerfile.mlc-rocm64-gfx1100 -t registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100 build/
docker push registry.harbor.lan/flexinfer/mlc-llm:rocm64-gfx1100

# Generic ROCm 6.4 source build (slow, optional)
docker build -f build/Dockerfile.mlc-rocm64-full -t registry.harbor.lan/library/mlc-llm:rocm64-src build/
docker push registry.harbor.lan/library/mlc-llm:rocm64-src
```

### Immediate Issues

1. **No ROCm 6.4 MLC-LLM image**: Quality models (32B, 14B) can't run without this
2. **GPUGroup nodeSelector mismatch**: quality-models configured for wrong node in some places

### Resolution Steps

1. Build and push `registry.harbor.lan/library/mlc-llm:rocm64-src`
2. Update ModelDeployments to use correct image
3. Verify GPUGroup nodeSelectors match intended GPU nodes
4. Re-trigger benchmarks for quality models

---

## Resource Cleanup Procedures

### CRITICAL: Before Deploying to a Node

Before scheduling ANY workload to a GPU node, verify these resources are NOT running:

```bash
# 1. Check for RAM ModelCaches targeting the node
kubectl get modelcache -n flexinfer-system -o custom-columns='NAME:.metadata.name,STRATEGY:.spec.storageStrategy,SELECTOR:.spec.nodeSelector'

# 2. Check for active DaemonSets (RAM syncers)
kubectl get daemonsets -n flexinfer-system

# 3. Check for pending/crashing pods on the node
kubectl get pods -n flexinfer-system -o wide | grep NODE_NAME
```

### Understanding Resource Hierarchy

**IMPORTANT**: Resources are created in a hierarchy. Deleting child resources is useless if parent still exists!

```
ModelDeployment (parent)
├── Deployment
├── Service
├── Benchmark Job
└── references → ModelCache

ModelCache (parent)
├── PVC (for SharedPVC strategy)
├── Job (for download)
└── DaemonSet (for Memory/NodeLocal strategy) ← "ram-syncer"
```

### How to PROPERLY Clean Up

1. **To stop a model deployment**: Delete or scale the **ModelDeployment** (not the Deployment)
   ```bash
   # Scale to 0
   kubectl patch modeldeployment NAME -n flexinfer-system --type=merge -p='{"spec":{"replicas":0}}'

   # Or delete entirely
   kubectl delete modeldeployment NAME -n flexinfer-system
   ```

2. **To stop RAM syncers**: Delete the **ModelCache** with `storageStrategy: Memory`
   ```bash
   # Find Memory-strategy caches
   kubectl get modelcache -n flexinfer-system -o custom-columns='NAME:.metadata.name,STRATEGY:.spec.storageStrategy'

   # Delete the RAM cache (this removes the DaemonSet)
   kubectl delete modelcache NAME-ram -n flexinfer-system
   ```

3. **To clean up benchmark pods**: Delete the **Job** (pods are owned by Job)
   ```bash
   kubectl delete job NAME-benchmark -n flexinfer-system
   ```

### Common Cleanup Mistakes (DON'T DO THIS)

❌ **Wrong**: `kubectl delete daemonset X-ram-syncer` → Controller will recreate it
✅ **Right**: `kubectl delete modelcache X-ram` → DaemonSet gets garbage collected

❌ **Wrong**: `kubectl delete pod X-benchmark-abc` → Job will recreate pod
✅ **Right**: `kubectl delete job X-benchmark` → Pods get garbage collected

❌ **Wrong**: `kubectl scale deployment X --replicas=0` → Controller will reset it
✅ **Right**: `kubectl patch modeldeployment X --type=merge -p='{"spec":{"replicas":0}}'`

### Emergency Node Recovery

If a GPU node crashes due to memory pressure or GPU segfaults:

1. **From your workstation** (before rebooting node):
   ```bash
   # 1. Delete all RAM ModelCaches targeting that node
   kubectl get modelcache -n flexinfer-system -o json | \
     jq -r '.items[] | select(.spec.storageStrategy=="Memory") | select(.spec.nodeSelector["kubernetes.io/hostname"]=="NODE_NAME") | .metadata.name' | \
     xargs -I{} kubectl delete modelcache {} -n flexinfer-system

   # 2. Scale down all ModelDeployments targeting that node
   kubectl get modeldeployment -n flexinfer-system -o json | \
     jq -r '.items[] | select(.spec.nodeSelector["kubernetes.io/hostname"]=="NODE_NAME") | .metadata.name' | \
     xargs -I{} kubectl patch modeldeployment {} -n flexinfer-system --type=merge -p='{"spec":{"replicas":0}}'

   # 3. Force delete any stuck pods
   kubectl delete pods -n flexinfer-system --field-selector spec.nodeName=NODE_NAME --force --grace-period=0
   ```

2. **Reboot the node** (physically or via IPMI/SSH if accessible)

3. **After node recovers**, wait for it to rejoin:
   ```bash
   kubectl get nodes -w
   ```

### GPU Memory Segfault Prevention

When testing new GPU configurations:

1. **Start with minimal resources** - Don't deploy multiple models simultaneously
2. **Use `local` mode for MLC-LLM** - Lower memory footprint than `server` mode
3. **Monitor with `kubectl logs -f`** - Watch for early crash signs
4. **Have cleanup commands ready** - Don't let crashes cascade

### Quick Reference: Targeting Nodes

| Node | Hostname Selector |
|------|-------------------|
| cblevins-5930k | `kubernetes.io/hostname: cblevins-5930k` |
| cblevins-7900xtx | `kubernetes.io/hostname: cblevins-7900xtx` |

```bash
# Find resources targeting a specific node
kubectl get modelcache -n flexinfer-system -o json | jq '.items[] | select(.spec.nodeSelector["kubernetes.io/hostname"]=="cblevins-7900xtx") | .metadata.name'
kubectl get modeldeployment -n flexinfer-system -o json | jq '.items[] | select(.spec.nodeSelector["kubernetes.io/hostname"]=="cblevins-7900xtx") | .metadata.name'
```

---

## Planning
- See `ROADMAP.md` for project status and plans.
