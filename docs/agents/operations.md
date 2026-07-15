# FlexInfer Operations Runbook

Operational workflows, CLI reference, cleanup procedures, and deployment status. For the quick-start overview, see [AGENTS.md](../../AGENTS.md).

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
  -H "Authorization: Bearer ${LITELLM_MASTER_KEY}" \
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
| `flexinfer benchmark <name>` | Trigger a fresh benchmark run (recreates benchmark Job + results ConfigMap) |
| `flexinfer autotune <name>` | Coordinate-descent search over the vLLM config space; keeps faster configs, rolls back worse ones. See the Goodhart guard below. |
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

# Trigger a new benchmark run (updates TPS)
flexinfer benchmark qwen3-8b-amd

# Delete a deployment
flexinfer delete qwen3-8b-amd

# View model caches and their storage strategies
flexinfer cache status
NAME           STRATEGY   PATH                             READY  SOURCE
qwen3-8b-ram   Memory     /dev/shm/flexinfer/qwen3-8b-ram  Ready  HF://mlc-ai/...
qwen3-3b-ram   Memory     /dev/shm/flexinfer/qwen3-3b-ram  Ready  HF://mlc-ai/...
```

### Autotune & the Goodhart guard

`flexinfer autotune` runs coordinate descent over the vLLM config space, keeping
configs that improve benchmark throughput and rolling back ones that don't. By
default it optimizes a single aggregate tok/s number — which can be **gamed**.

The 2026-06-26 kill-test proved the failure live: enabling n-gram speculative
decoding on `gemma4-26b-a4b-gptq` lifted aggregate decode throughput **+27%**
while a long-form generation workload **regressed −47%** (n-gram SD accelerates
prompt-copy workloads but wastes draft+verify compute on novel generation; it is
lossless, so this is a throughput regression, not a quality one). A throughput-only
tuner would happily accept it. Evidence:
`.loom/killtest-autotune-goodhart-2026-06-26.md`.

The **Goodhart guard** (`--quality-guard`) defends against this. It probes the
model's chat endpoint for **per-workload-class** decode tok/s (a prompt-copy
`lookup` class and an open-ended `novel` class) and vetoes any candidate that
improves aggregate throughput while regressing a protected class beyond a
tolerance — even though the proxy went up. Vetoed steps roll back and are recorded
as `quality_vetoed` in the `<model>-autotune-log` ConfigMap (`results.tsv`,
`quality_note` column).

#### n-gram speculative decoding is guard-gated

The search space includes a `speculativeConfig` parameter that toggles n-gram
(prompt-lookup) speculative decoding **on/off** (off = no `--speculative-config`;
on = `{"method":"ngram","num_speculative_tokens":7,"prompt_lookup_max":6,"prompt_lookup_min":1}`).
This is the single parameter whose value is the n-gram-SD lever from the kill-test,
so it is **only tuned when `--quality-guard` is set**. Without the guard the CLI
drops it from the search space (and prints a one-line notice), because tuning it on
the aggregate-throughput proxy alone re-introduces the exact Goodhart trap above —
the throughput-only tuner would accept the +27% aggregate gain and silently ship the
−47% long-form regression. With the guard, n-gram SD is accepted for copy-heavy
serving (where it is a real win) and vetoed for long-form-heavy serving.

```bash
# Throughput-only autotune (legacy default — guard off)
flexinfer autotune qwen3-14b-gptq -n flexinfer-system

# With the Goodhart guard (recommended for any model where serving includes
# long-form generation), 10% per-class regression tolerance by default:
flexinfer autotune qwen3-14b-gptq -n flexinfer-system --quality-guard

# Tighter tolerance and more canary repeats:
flexinfer autotune qwen3-14b-gptq --quality-guard --quality-tolerance 5 --quality-repeats 3
```

| Flag | Default | Meaning |
|------|---------|---------|
| `--quality-guard` | `false` | Enable the guard. Off = legacy throughput-only behavior **and** n-gram speculative-decoding tuning is skipped (it is unsafe to tune without the guard). |
| `--quality-tolerance` | `10` | Per-workload-class throughput regression tolerated (percent) before veto. |
| `--quality-repeats` | `2` | Repeats per workload class in the quality canary (median is used). |

The guard is implemented in `pkg/goodhart` (`WorkloadRegression` + online
overoptimization detectors, ported from the RewardSpy project) and wired through
`agents/autotune` via an optional `QualityFunc`. A live-validation harness lives
in `agents/autotune/quality_live_test.go` (`TestLiveQualityProbe`, env-gated).

### Flags

| Flag | Description |
|------|-------------|
| `-n, --namespace` | Kubernetes namespace (default: `flexinfer-system`) |
| `-A, --all-namespaces` | List across all namespaces |
| `--kubeconfig` | Path to kubeconfig file |

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

## Known Issues & Improvements Needed

### Resolved
- [x] TPS now populated from benchmark results
- [x] GPU validation prevents vLLM on Maxwell
- [x] CLI provides easy model management
- [x] ROCm gfx1100 SIGSEGV crashes fixed via `TORCH_ROCM_AOTRITON_ENABLE_EXPERIMENTAL=1`
- [x] Diffusers backend working for image generation on AMD GPUs
- [x] FlexInfer benchmarker has direct CLI integration (`flexinfer benchmark`)
- [x] Benchmarks can be triggered from command line (`flexinfer benchmark <name>`)
- [x] Controller applies LiteLLM service annotations (`litellm.flexinfer.ai/*`)
- [x] LiteLLM annotations are covered by controller tests
- [x] End-to-end deployment guide documented (`docs/INSTALL.md`)
- [x] MLC-LLM compilation workflow documented (`build/README-rocm.md`)

### Open Focus
- [ ] Dependency refresh and upgrade rollout tracked in [Issue #9](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/9)
- [ ] Roadmap tracking reconciliation and next-slice definition tracked in [Issue #1](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/1)

---

## Deployment Status Snapshot (January 2026)

> Historical snapshot — verify against the live cluster before relying on it.

### Cluster Layout

#### GPU Nodes

| Node | CPU | GPU | VRAM | Role |
|------|-----|-----|------|------|
| `cblevins-5930k` | Intel i7-5930K | AMD RX 7900 XTX | 24GB | Fast models (8B, 4B) |
| `cblevins-7900xtx` | AMD Zen4 | AMD RX 7900 XTX | 24GB | Quality models (14B, 32B) + ComfyUI |
| `cblevins-gtx980ti` | Intel i7 (legacy) | NVIDIA GTX 980 Ti (Maxwell, sm_52) | 6GB | Image generation (SD 1.5 / Dreamshaper 8 via CUDA diffusers) — sole GPU tenant |

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

### Immediate Issues (at snapshot time)

1. **No ROCm 6.4 MLC-LLM image**: Quality models (32B, 14B) can't run without this
2. **GPUGroup nodeSelector mismatch**: quality-models configured for wrong node in some places

### Resolution Steps

1. Build and push `registry.harbor.lan/library/mlc-llm:rocm64-src`
2. Update ModelDeployments to use correct image
3. Verify GPUGroup nodeSelectors match intended GPU nodes
4. Re-trigger benchmarks for quality models
