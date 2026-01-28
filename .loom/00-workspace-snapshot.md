# Workspace Snapshot

- Generated: 2026-01-28T12:15:12-05:00
- Root: `/Users/cblevins/workspace/services/flexinfer`
- Git toplevel: `/Users/cblevins/workspace/services/flexinfer`
- Platform: `macOS-26.3-arm64-arm-64bit`
- Python: `3.12.11`

## Git
```
## master...github/master
?? .loom/
```

### Remotes
```
github	https://github.com/flexinfer/flexinfer.git (fetch)
github	https://github.com/flexinfer/flexinfer.git (push)
gitlab	https://gitlab.flexinfer.ai/services/flexinfer.git (fetch)
gitlab	https://gitlab.flexinfer.ai/services/flexinfer.git (push)
origin	https://gitlab.flexinfer.ai/services/flexinfer.git (fetch)
origin	https://gitlab.flexinfer.ai/services/flexinfer.git (push)
origin	https://github.com/flexinfer/flexinfer.git (push)
```

### HEAD
```
0653324 model(v1alpha2): preserve deployment selector
```

## Top-Level Layout

### Directories
- `.claude/`
- `.git/`
- `.githooks/`
- `.gocache/`
- `.golangci-lint-cache/`
- `.gotmp/`
- `.loom/`
- `.vscode/`
- `agents/`
- `api/`
- `assets/`
- `backend/`
- `bin/`
- `build/`
- `charts/`
- `cmd/`
- `config/`
- `controllers/`
- `docs/`
- `examples/`
- `hack/`
- `internal/`
- `pkg/`
- `scheduler/`
- `scripts/`
- `specs/`

### Files
- `.dockerignore`
- `.gitignore`
- `.gitlab-ci.yml`
- `AGENTS.md`
- `CODE_OF_CONDUCT.md`
- `CONTRIBUTING.md`
- `cover.out`
- `coverage.out`
- `flexinfer-agent`
- `flexinfer-bench`
- `flexinfer-manager`
- `flexinfer-proxy`
- `go.mod`
- `go.sum`
- `LICENSE`
- `logo.png`
- `Makefile`
- `README.md`
- `ROADMAP.md`
- `SECURITY.md`
- `seed-issues`
- `setup.sh`

## Key Files Detected
- `README.md`
- `AGENTS.md`
- `go.mod`
- `Makefile`

## Tracked / Indexed Files (sample)
- `.claude/hookify.force-overwrite.local.md`
- `.githooks/pre-commit`
- `.githooks/pre-push`
- `.gitignore`
- `.gitlab-ci.yml`
- `.vscode/settings.json`
- `AGENTS.md`
- `CODE_OF_CONDUCT.md`
- `CONTRIBUTING.md`
- `LICENSE`
- `Makefile`
- `README.md`
- `ROADMAP.md`
- `SECURITY.md`
- `agents/agent/agent.go`
- `agents/agent/agent_test.go`
- `agents/benchmarker/benchmarker.go`
- `agents/benchmarker/benchmarker_test.go`
- `api/v1alpha1/gpugroup_types.go`
- `api/v1alpha1/groupversion_info.go`
- `api/v1alpha1/modelcache_types.go`
- `api/v1alpha1/types.go`
- `api/v1alpha1/zz_generated.deepcopy.go`
- `api/v1alpha2/groupversion_info.go`
- `api/v1alpha2/model_types.go`
- `api/v1alpha2/model_types_test.go`
- `api/v1alpha2/zz_generated.deepcopy.go`
- `assets/banner.png`
- `assets/header.svg`
- `assets/icon.png`
- `backend/comfyui.go`
- `backend/diffusers.go`
- `backend/interface.go`
- `backend/llamacpp.go`
- `backend/mlc_llm.go`
- `backend/mlc_llm_test.go`
- `backend/ollama.go`
- `backend/registry.go`
- `backend/registry_test.go`
- `backend/vllm.go`
- `backend/vllm_omni.go`
- `build/Dockerfile.agent`
- `build/Dockerfile.bench`
- `build/Dockerfile.comfyui-rocm`
- `build/Dockerfile.diffusers-cuda`
- `build/Dockerfile.diffusers-rocm`
- `build/Dockerfile.manager`
- `build/Dockerfile.mlc-cuda`
- `build/Dockerfile.mlc-cuda-maxwell`
- `build/Dockerfile.mlc-rocm`
- `build/Dockerfile.mlc-rocm64-build`
- `build/Dockerfile.mlc-rocm64-full`
- `build/Dockerfile.mlc-rocm64-hipblas`
- `build/Dockerfile.proxy`
- `build/Dockerfile.sched`
- `build/Dockerfile.vllm-omni-rocm`
- `build/Dockerfile.vllm-rocm`
- `build/Dockerfile.vllm-rocm-gfx1100`
- `build/README-maxwell.md`
- `build/README-rocm.md`
- `charts/flexinfer/Chart.yaml`
- `charts/flexinfer/crds/ai.flexinfer_gpugroups.yaml`
- `charts/flexinfer/crds/ai.flexinfer_modelcaches.yaml`
- `charts/flexinfer/crds/ai.flexinfer_modeldeployments.yaml`
- `charts/flexinfer/crds/ai.flexinfer_models.yaml`
- `charts/flexinfer/templates/_helpers.tpl`
- `charts/flexinfer/templates/activator-deployment.yaml`
- `charts/flexinfer/templates/activator-service.yaml`
- `charts/flexinfer/templates/benchmarker-configmap.yaml`
- `charts/flexinfer/templates/daemonset.yaml`
- `charts/flexinfer/templates/deployment.yaml`
- `charts/flexinfer/templates/grafana-dashboard.yaml`
- `charts/flexinfer/templates/networkpolicy.yaml`
- `charts/flexinfer/templates/poddisruptionbudget.yaml`
- `charts/flexinfer/templates/rbac.yaml`
- `charts/flexinfer/templates/scheduler-configmap.yaml`
- `charts/flexinfer/templates/scheduler-deployment.yaml`
- `charts/flexinfer/templates/scheduler-service.yaml`
- `charts/flexinfer/templates/service.yaml`
- `charts/flexinfer/values.yaml`
- `cmd/flexinfer-agent/main.go`
- `cmd/flexinfer-bench/main.go`
- `cmd/flexinfer-manager/main.go`
- `cmd/flexinfer-proxy/main.go`
- `cmd/flexinfer-proxy/proxy_test.go`
- `cmd/flexinfer-sched/main.go`
- `cmd/flexinfer/commands/cache.go`
- `cmd/flexinfer/commands/delete.go`
- `cmd/flexinfer/commands/list.go`
- `cmd/flexinfer/commands/logs.go`
- `cmd/flexinfer/commands/root.go`
- `cmd/flexinfer/commands/scale.go`
- `cmd/flexinfer/commands/status.go`
- `cmd/flexinfer/main.go`
- `config/crd/ai.flexinfer_gpugroups.yaml`
- `config/crd/ai.flexinfer_modelcaches.yaml`
- `config/crd/ai.flexinfer_modeldeployments.yaml`
- `config/crd/ai.flexinfer_models.yaml`
- `config/rbac/role.yaml`
- `controllers/backend_test.go`
- `controllers/event_recording_test.go`
- `controllers/finalizer_test.go`
- `controllers/gpu_resource_test.go`
- `controllers/gpugroup_controller.go`
- `controllers/gpugroup_controller_test.go`
- `controllers/gpugroup_integration_test.go`
- `controllers/job_creation_test.go`
- `controllers/model_controller.go`
- `controllers/model_controller_test.go`
- `controllers/modelcache_controller.go`
- `controllers/modeldeployment_controller.go`
- `controllers/modeldeployment_controller_test.go`
- `controllers/status_test.go`
- `controllers/suite_test.go`
- `controllers/testutil/gpugroup_fixtures.go`
- `controllers/types_test.go`
- `docs/CONFIGURATION.md`
- `docs/DEPLOYMENT_RUNBOOK.md`
- `docs/DEVELOPMENT.md`
- `docs/IMPLEMENTATION_STATUS.md`
- `docs/INSTALL.md`
- `docs/MODEL_MANAGEMENT_RESEARCH.md`
- `docs/README.md`
- `docs/archive/research-2025-07-27.md`
- `docs/archive/research-2025-08-28.md`
- `docs/dev/README.md`
- `docs/dev/architecture.md`
- `docs/dev/backends.md`
- `docs/dev/local-dev.md`
- `docs/dev/release.md`
- `docs/dev/testing.md`
- `docs/nav.yaml`
- `docs/reference.md`
- `docs/specs/README.md`
- `docs/specs/crds.md`
- `docs/specs/flexinfer-config.md`
- `docs/specs/labels-and-annotations.md`
- `docs/specs/metrics.md`
- `docs/specs/proxy-api.md`
- `docs/specs/scheduler-extender.md`
- `docs/user/README.md`
- `docs/user/caching.md`
- `docs/user/gpu-sharing.md`
- `docs/user/legacy-v1alpha1.md`
- `docs/user/models-v1alpha2.md`
- `docs/user/operations.md`
- `docs/user/proxy.md`
- `docs/user/quickstart.md`
- `examples/README.md`
- `examples/cpu-llama-7b.yaml`
- `examples/dark-champion-moe-amd.yaml`
- `examples/gpugroup-multi-model.yaml`
- `examples/llama3-8b-amd.yaml`
- `examples/llama3-8b.yaml`
- `examples/maxwell-qwen3-0.6b.yaml`
- `examples/phi3-mini-nvidia.yaml`
- `examples/qwen25-7b-abliterated-mlc-amd.yaml`
- `examples/qwen3-8b-abliterated-mlc-amd.yaml`
- `examples/ram-cached-models.yaml`
- `examples/serverless-multi-backend.yaml`
- `examples/v1alpha2/model-amd-rocm.yaml`
- `examples/v1alpha2/model-basic.yaml`
- `examples/v1alpha2/model-image-gen.yaml`
- `examples/v1alpha2/model-shared-gpu.yaml`
- `go.mod`
- `go.sum`
- `hack/boilerplate.go.txt`
- `hack/install-githooks.sh`
- `hack/kind-mixed-gpu.yaml`
- `hack/precommit.sh`
- `hack/seed-issues.go`
- `hack/seed-issues.sh`
- `internal/cache/cache.go`
- `logo.png`
- `pkg/metrics/exporter.go`
- `scheduler/scheduler.go`
- `scheduler/scheduler_test.go`
- `scripts/compile-qwen3-abliterated-rocm.sh`
- `setup.sh`
- `specs/jsonschema/flexinfer-config.schema.json`
- `specs/openapi/flexinfer-proxy.openapi.yaml`

## AGENTS.md Files
- `AGENTS.md`

### AGENTS.md Contents (head)

#### `AGENTS.md`
```
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
…
```

## Notes
- Add MCP inventory via the plan-loom-core workflow (see `.loom/00-mcp-inventory.md`).
