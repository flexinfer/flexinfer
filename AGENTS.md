# FlexInfer Agents & Runtime Components

## Tracking
- [Roadmap tracking issue](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/1)
- See `ROADMAP.md` for project status and plans.

FlexInfer is split into **six** cooperating executables (all written in Go).

| Component | Binary | Runs on | Key responsibility |
|-----------|--------|---------|--------------------|
| Node Agent | `flexinfer-agent` | Every GPU-capable node | Detect hardware & emit labels |
| Benchmarker | `flexinfer-bench` | Job pod (ephemeral) | Measure tokens/s per model-device pair |
| Controller Manager | `flexinfer-manager` | Control-plane | Reconciles `Model` (v1alpha2) and legacy v1alpha1 CRDs |
| Scheduler Extender | `flexinfer-sched` | Control-plane | Filters & scores nodes during scheduling |
| Global Proxy | `flexinfer-global-proxy` | Control-plane | Routes traffic across healthy cluster-local proxies |
| Metrics Exporter | built-in | All components | Collects Prometheus metrics for all of the above |

## Detailed Reference Docs

Read these on demand — they hold the full tables, examples, and runbooks:

| Topic | Doc |
|-------|-----|
| Per-component deep dive (labels, flags, env vars, scoring, metrics, communication flow, troubleshooting) | [docs/agents/components.md](docs/agents/components.md) |
| Model CRDs (`ModelDeployment`, `ModelCache`), storage strategies, RAM cache, MLC-LLM config, benchmark results | [docs/agents/model-crds.md](docs/agents/model-crds.md) |
| Operations runbook: deploy workflow, chart/CRD updates, LiteLLM, CLI reference, cleanup procedures, emergency recovery, build/deployment status | [docs/agents/operations.md](docs/agents/operations.md) |
| GPU compatibility matrix + per-arch configs (gfx1100, gfx906, Maxwell) | [docs/agents/gpu-compatibility.md](docs/agents/gpu-compatibility.md) |
| End-to-end install guide | [docs/INSTALL.md](docs/INSTALL.md) |
| gfx906 hardware notes | `build/README-gfx906.md` |

## Build, Test, Local Dev

```bash
make build              # Build all binaries
make build-agent        # Or per component: build-controller, build-scheduler,
                        # build-benchmarker, build-global-proxy, build-cli
make test               # Run all tests
go test ./controllers/...   # Component-specific (also ./agents/agent/..., ./scheduler/...)

# Run controller locally
export KUBECONFIG=~/.kube/config
go run cmd/flexinfer-manager/main.go --log-level=debug
```

CRD/chart change flow: edit types → `make manifests` → `make test` → bump `charts/flexinfer/Chart.yaml` → `cp config/crd/*.yaml charts/flexinfer/crds/` → commit/push → `flux reconcile` → **`kubectl apply -f charts/flexinfer/crds/` manually (Helm doesn't auto-update CRDs)**. Full steps: [operations.md](docs/agents/operations.md#updating-chartcrds).

## Critical Conventions (read before touching the cluster)

- **Resource hierarchy**: act on the parent CR, never the child. Scale/delete the **ModelDeployment** (not the Deployment), delete the **ModelCache** (not the ram-syncer DaemonSet), delete the **Job** (not the benchmark pod) — the controller recreates children. Details: [operations.md](docs/agents/operations.md#resource-cleanup-procedures).
- **ROCm gfx1100** (RX 7900 XTX): PyTorch backends need `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1` (+ `HSA_OVERRIDE_GFX_VERSION=11.0.0`, `PYTORCH_ROCM_ARCH=gfx1100`) or attention ops SIGSEGV. Injected by `ROCmEnvVars()` in `backend/interface.go`.
- **ROCm gfx906** (Radeon VII): needs `HSA_ENABLE_SDMA=0` or you get memory-aperture violations. Do NOT set `HSA_OVERRIDE_GFX_VERSION` or the AOTriton flag on gfx906. Also via `ROCmEnvVars()`.
- **MLC-LLM quantization**: use `q4f32_1` (NOT `q4f16`) for Qwen3 on ROCm — TVM segfault bug ([mlc-ai/mlc-llm#3283](https://github.com/mlc-ai/mlc-llm/issues/3283)). Production uses pre-compiled `modelLibPath` + `jitPolicy: "OFF"`; JIT adds 2-5 min startup.
- **Stuck Helm upgrades / rollouts**: a NotReady node can leave pods stuck `Terminating`, blocking rollouts. Force-delete the stuck pods in `flexinfer-system`.

## GPU Compatibility (summary)

| Backend | RDNA3 (7900XTX) | Vega20 (Radeon VII) | Maxwell (980Ti) |
|---------|-----------------|---------------------|-----------------|
| Ollama | ✅ | ✅ | ✅ |
| vLLM | ✅ | ✅ (BUILD_FA=0 image) | ❌ |
| MLC-LLM | ✅ | ✅ | ⚠️ pre-compiled lib only |
| llama.cpp | ✅ | ✅ | ✅ |
| Diffusers | ✅ | ⚠️ experimental | ⚠️ experimental (SD 1.5 only, CUDA 11.8 image) |
| ComfyUI | ✅ | ⚠️ experimental | ❌ |

GPU nodes: `cblevins-5930k` (7900XTX), `cblevins-7900xtx` (7900XTX), `cblevins-gtx980ti` (980 Ti, sm_52, 6GB — image-gen lane: SD 1.5 / Dreamshaper 8). Per-arch config blocks: [gpu-compatibility.md](docs/agents/gpu-compatibility.md).

## Model Deployment (quick shape)

Models deploy declaratively via `ModelDeployment` + optional `ModelCache` CRs in `flexinfer-system` (backends: `mlc-llm`, `ollama`, `vllm`, `llama-cpp`; storage strategies: `SharedPVC`, `NodeLocal`, `Memory`/tmpfs). The `flexinfer` CLI (`make build-cli`) wraps list/status/logs/scale/benchmark/cache-status. Full YAML examples, MLC memory tuning, and CLI reference: [model-crds.md](docs/agents/model-crds.md) and [operations.md](docs/agents/operations.md).

```bash
kubectl get modeldeployment -n flexinfer-system   # what's deployed
kubectl get modelcache -n flexinfer-system        # cache status
flexinfer list                                    # CLI equivalent with TPS/GPU info
```

<!-- BEGIN LOOM:AGENT-SAFETY -->
## Loom Agent Safety Policy (Generated)

- Pre-existing uncommitted/untracked files are baseline context, not an automatic blocker.
- Continue on the current branch/worktree by default.
- Stage and commit only files intentionally changed for the active task.
- Escalate only when new unexpected changes appear in files you are editing, or when a branch/worktree switch is explicitly requested.
- Dirty-worktree mode: `continue_scoped_commits`.

Canonical nudge for CLI hooks:
> Dirty worktree detected. Treat pre-existing changes as baseline context, continue work, and stage/commit only files for the active task. Escalate only if new unexpected changes appear in files you are editing.

<!-- END LOOM:AGENT-SAFETY -->
