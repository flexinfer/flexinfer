# Workspace Snapshot

- Generated: 2026-03-05T18:43:39-05:00
- Root: `/Users/cblevins/workspace/services/flexinfer`
- Git toplevel: `/Users/cblevins/workspace/services/flexinfer`
- Platform: `macOS-26.4-arm64-arm-64bit`
- Python: `3.12.11`

## Git
```
## master...gitlab/fix/proxy-alias-resolution [ahead 18]
 M .loom/00-index.md
 M .loom/00-mcp-inventory.md
 M .loom/00-workspace-snapshot.md
 M .loom/10-research.md
 M .loom/30-implementation-plan.md
 M .loom/50-worklog.md
?? .claude/.loom-skills-manifest.json
?? .claude/commands/
?? .claude/instructions.md
?? .claude/rules/
?? .claude/settings.json
?? .claude/settings.json.tmp
?? .kilocode/
?? docs/roadmap-reconciliation-2026-03-05.md
```

### Remotes
```
github	https://github.com/flexinfer/flexinfer.git (fetch)
github	https://github.com/flexinfer/flexinfer.git (push)
gitlab	https://oauth2:glpat-vFFCVHmo_LOPh6lq1tk3p286MQp1OjEH.01.0w0ycoylq@gitlab.flexinfer.ai/services/flexinfer.git (fetch)
gitlab	https://oauth2:glpat-vFFCVHmo_LOPh6lq1tk3p286MQp1OjEH.01.0w0ycoylq@gitlab.flexinfer.ai/services/flexinfer.git (push)
gitlab-vm	git@gitlab.flexinfer.ai:services/flexinfer.git (fetch)
gitlab-vm	git@gitlab.flexinfer.ai:services/flexinfer.git (push)
origin	https://oauth2:glpat-vFFCVHmo_LOPh6lq1tk3p286MQp1OjEH.01.0w0ycoylq@gitlab.flexinfer.ai/services/flexinfer.git (fetch)
origin	https://oauth2:glpat-vFFCVHmo_LOPh6lq1tk3p286MQp1OjEH.01.0w0ycoylq@gitlab.flexinfer.ai/services/flexinfer.git (push)
```

### HEAD
```
a9ed3af docs: expand architecture docs with 8 Mermaid workflow diagrams
```

## Top-Level Layout

### Directories
- `.claude/`
- `.codex/`
- `.git/`
- `.githooks/`
- `.gocache/`
- `.golangci-lint-cache/`
- `.gotmp/`
- `.kilocode/`
- `.loom/`
- `.vscode/`
- `.vscode-mcp/`
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
- `e2e/`
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
- `.golangci.yml`
- `ADOPTERS.md`
- `AGENTS.md`
- `CODE_OF_CONDUCT.md`
- `CONTRIBUTING.md`
- `go.mod`
- `go.sum`
- `GOVERNANCE.md`
- `LICENSE`
- `logo.png`
- `Makefile`
- `README.md`
- `renovate.json`
- `ROADMAP.md`
- `SECURITY.md`
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
- `.golangci.yml`
- `.loom/00-index.md`
- `.loom/00-mcp-inventory.md`
- `.loom/00-workspace-snapshot.md`
- `.loom/10-research.md`
- `.loom/20-product-spec.md`
- `.loom/30-implementation-plan.md`
- `.loom/40-decisions.md`
- `.loom/50-worklog.md`
- `.loom/tech-debt-inventory.json`
- `.loom/tech-debt-plan.md`
- `.loom/tech-debt-priority.md`
- `.vscode/settings.json`
- `ADOPTERS.md`
- `AGENTS.md`
- `CODE_OF_CONDUCT.md`
- `CONTRIBUTING.md`
- `GOVERNANCE.md`
- `LICENSE`
- `Makefile`
- `README.md`
- `ROADMAP.md`
- `SECURITY.md`
- `agents/agent/agent.go`
- `agents/agent/agent_test.go`
- `agents/agent/amd_metrics_linux_test.go`
- `agents/agent/collect_node_metrics_test.go`
- `agents/agent/drain.go`
- `agents/agent/testdata/nvidia-smi-memory.txt`
- `agents/agent/testdata/rocm-smi-5.7-vram.json`
- `agents/agent/testdata/rocm-smi-6.0-vram.json`
- `agents/agent/testdata/rocm-smi-6.4-vram.json`
- `agents/agent/vram_linux.go`
- `agents/agent/vram_other.go`
- `agents/benchmarker/benchmarker.go`
- `agents/benchmarker/benchmarker_test.go`
- `agents/benchmarker/configmap_store.go`
- `agents/benchmarker/postgres_store.go`
- `agents/benchmarker/schema.sql`
- `agents/benchmarker/store.go`
- `agents/benchmarker/store_test.go`
- `agents/termination/aws.go`
- `agents/termination/azure.go`
- `agents/termination/detector.go`
- `agents/termination/detector_test.go`
- `agents/termination/gcp.go`
- `agents/termination/generic.go`
- `agents/termination/harvester.go`
- `agents/termination/metadata.go`
- `agents/termination/metadata_test.go`
- `api/v1alpha1/gpugroup_types.go`
- `api/v1alpha1/groupversion_info.go`
- `api/v1alpha1/modelcache_types.go`
- `api/v1alpha1/types.go`
- `api/v1alpha1/zz_generated.deepcopy.go`
- `api/v1alpha2/catalog_types.go`
- `api/v1alpha2/cluster_types.go`
- `api/v1alpha2/cluster_types_test.go`
- `api/v1alpha2/federatedmodel_types.go`
- `api/v1alpha2/federatedmodel_types_test.go`
- `api/v1alpha2/globalproxy_types.go`
- `api/v1alpha2/globalproxy_types_test.go`
- `api/v1alpha2/groupversion_info.go`
- `api/v1alpha2/lora_types.go`
- `api/v1alpha2/model_types.go`
- `api/v1alpha2/model_types_test.go`
- `api/v1alpha2/zz_generated.deepcopy.go`
- `assets/banner.png`
- `assets/header.svg`
- `assets/icon.png`
- `backend/comfyui.go`
- `backend/comfyui_test.go`
- `backend/diffusers.go`
- `backend/diffusers_test.go`
- `backend/gpu_compat.go`
- `backend/gpu_compat_test.go`
- `backend/interface.go`
- `backend/interface_test.go`
- `backend/llamacpp.go`
- `backend/llamacpp_test.go`
- `backend/mlc_llm.go`
- `backend/mlc_llm_test.go`
- `backend/ollama.go`
- `backend/ollama_test.go`
- `backend/registry.go`
- `backend/registry_test.go`
- `backend/vllm.go`
- `backend/vllm_omni.go`
- `backend/vllm_omni_test.go`
- `backend/vllm_test.go`
- `backend/vram_estimate.go`
- `backend/vram_estimate_test.go`
- `build/Dockerfile.agent`
- `build/Dockerfile.agent.bin`
- `build/Dockerfile.bench`
- `build/Dockerfile.bench.bin`
- `build/Dockerfile.comfyui-rocm`
- `build/Dockerfile.comfyui-rocm-gfx1100`
- `build/Dockerfile.comfyui-rocm-gfx906`
- `build/Dockerfile.diffusers-cuda`
- `build/Dockerfile.diffusers-rocm`
- `build/Dockerfile.diffusers-rocm-gfx1100`
- `build/Dockerfile.diffusers-rocm-gfx906`
- `build/Dockerfile.flash-loader`
- `build/Dockerfile.flash-loader.bin`
- `build/Dockerfile.llamacpp-cuda-maxwell`
- `build/Dockerfile.llamacpp-rocm-gfx1100`
- `build/Dockerfile.llamacpp-rocm-gfx906`
- `build/Dockerfile.manager`
- `build/Dockerfile.manager.bin`
- `build/Dockerfile.mlc-cuda`
- `build/Dockerfile.mlc-cuda-maxwell`
- `build/Dockerfile.mlc-rocm`
- `build/Dockerfile.mlc-rocm64-build`
- `build/Dockerfile.mlc-rocm64-full`
- `build/Dockerfile.mlc-rocm64-gfx1100`
- `build/Dockerfile.mlc-rocm64-gfx906`
- `build/Dockerfile.mlc-rocm64-hipblas`
- `build/Dockerfile.ollama-cuda-maxwell`
- `build/Dockerfile.proxy`
- `build/Dockerfile.proxy.bin`
- `build/Dockerfile.quantizer-awq`
- `build/Dockerfile.quantizer-awq-rocm`
- `build/Dockerfile.quantizer-gguf`
- `build/Dockerfile.quantizer-gptq`
- `build/Dockerfile.sched`
- `build/Dockerfile.sched.bin`
- `build/Dockerfile.vllm-nightly-rocm-gfx1100`
- `build/Dockerfile.vllm-omni-rocm`
- `build/Dockerfile.vllm-omni-rocm-gfx1100`
- `build/Dockerfile.vllm-rocm`
- `build/Dockerfile.vllm-rocm-gfx1100`
- `build/Dockerfile.vllm-rocm-gfx1100-fa`
- `build/Dockerfile.vllm-rocm-gfx906`
- `build/Dockerfile.vllm-rocm-gfx906-fa`
- `build/README-gfx906.md`
- `build/README-maxwell.md`
- `build/README-rocm.md`
- `build/patch-hipmemgetinfo.sh`
- `build/requirements-diffusers-rocm.txt`
- `build/vllm-omni-shims/registry.py`
- `charts/flexinfer/Chart.yaml`
- `charts/flexinfer/crds/ai.flexinfer_clusters.yaml`
- `charts/flexinfer/crds/ai.flexinfer_federatedmodels.yaml`
- `charts/flexinfer/crds/ai.flexinfer_globalproxies.yaml`
- `charts/flexinfer/crds/ai.flexinfer_gpugroups.yaml`
- `charts/flexinfer/crds/ai.flexinfer_loraadapters.yaml`
- `charts/flexinfer/crds/ai.flexinfer_modelcaches.yaml`
- `charts/flexinfer/crds/ai.flexinfer_modelcatalogs.yaml`
- `charts/flexinfer/crds/ai.flexinfer_modeldeployments.yaml`
- `charts/flexinfer/crds/ai.flexinfer_models.yaml`
- `charts/flexinfer/templates/NOTES.txt`
- `charts/flexinfer/templates/_helpers.tpl`
- `charts/flexinfer/templates/activator-deployment.yaml`
- `charts/flexinfer/templates/activator-service.yaml`
- `charts/flexinfer/templates/benchmarker-configmap.yaml`
- `charts/flexinfer/templates/daemonset.yaml`
- `charts/flexinfer/templates/deployment.yaml`
- `charts/flexinfer/templates/grafana-dashboard-global-routing.yaml`
- `charts/flexinfer/templates/grafana-dashboard-gpugroup.yaml`
- `charts/flexinfer/templates/grafana-dashboard-modelcache.yaml`
- `charts/flexinfer/templates/grafana-dashboard-proxy.yaml`
- `charts/flexinfer/templates/grafana-dashboard.yaml`
- `charts/flexinfer/templates/networkpolicy.yaml`
- `charts/flexinfer/templates/poddisruptionbudget.yaml`
- `charts/flexinfer/templates/prometheusrule.yaml`
- `charts/flexinfer/templates/rbac.yaml`
- `charts/flexinfer/templates/scheduler-configmap.yaml`
- `charts/flexinfer/templates/scheduler-deployment.yaml`
- `charts/flexinfer/templates/scheduler-service.yaml`
- `charts/flexinfer/templates/securitycontext.yaml`
- `charts/flexinfer/templates/service.yaml`
- `charts/flexinfer/templates/tenancy-admission-policies.yaml`
- `charts/flexinfer/templates/tenancy-baseline.yaml`
- `charts/flexinfer/values.yaml`
- `cmd/flexinfer-agent/main.go`
- `cmd/flexinfer-agent/main_test.go`
- `cmd/flexinfer-bench/main.go`
- `cmd/flexinfer-bench/main_test.go`
- `cmd/flexinfer-flash-loader/fadvise_linux.go`
- `cmd/flexinfer-flash-loader/fadvise_other.go`
- `cmd/flexinfer-flash-loader/main.go`
- `cmd/flexinfer-flash-loader/main_test.go`
- `cmd/flexinfer-global-proxy/main.go`
- `cmd/flexinfer-global-proxy/main_test.go`
- `cmd/flexinfer-manager/main.go`
- `cmd/flexinfer-manager/main_test.go`
- `cmd/flexinfer-proxy/main.go`
- `cmd/flexinfer-sched/main.go`
- `cmd/flexinfer-sched/main_test.go`
- `cmd/flexinfer/commands/benchmark.go`
- `cmd/flexinfer/commands/cache.go`
- `cmd/flexinfer/commands/catalog.go`
- `cmd/flexinfer/commands/cli_commands_test.go`
- `cmd/flexinfer/commands/delete.go`
- `…`

## AGENTS.md Files
- `AGENTS.md`

### AGENTS.md Contents (head)

#### `AGENTS.md`
```
# FlexInfer Agents & Runtime Components

## Tracking
- [Roadmap tracking issue](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/1)


FlexInfer is split into **six** cooperating executables (all written in Go).
This document explains what each agent does, how they communicate, and which options you can tune.

| Component | Binary | Runs on | Key responsibility |
|-----------|--------|---------|--------------------| 
| Node Agent | `flexinfer-agent` | Every GPU-capable node | Detect hardware & emit labels |
| Benchmarker | `flexinfer-bench` | Job pod (ephemeral) | Measure tokens/s per model-device pair |
| Controller Manager | `flexinfer-manager` | Control-plane | Reconciles `Model` (v1alpha2) and legacy v1alpha1 CRDs |
| Scheduler Extender | `flexinfer-sched` | Control-plane | Filters & scores nodes during scheduling |
| Global Proxy | `flexinfer-global-proxy` | Control-plane | Routes traffic across healthy cluster-local proxies |
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
…
```

## Notes
- Add MCP inventory via the plan-loom-core workflow (see `.loom/00-mcp-inventory.md`).
