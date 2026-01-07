# FlexInfer Development Guide

## Prerequisites

- Go 1.25+
- Docker
- Make

## Getting Started

1. **Setup Environment**:
   Run the setup target to install necessary tools (`controller-gen`, `kustomize`, `envtest`) and download dependencies.

   ```bash
   make setup
   ```

2. **Generate Manifests**:
   If you modify `api/...` or `controllers/...`, regenerate CRDs and RBAC:

   ```bash
   make manifests
   ```

3. **Run Tests**:
   Use unit tests for fast feedback and integration tests for controller behavior:

   ```bash
   make test-unit
   make test-integration
   ```

   To run everything with code generation and envtest setup:

   ```bash
   make test
   ```

4. **Build**:
   Build the manager binary:
   ```bash
   make build
   ```

## Scale-to-Zero (Serverless)

The project includes a serverless "Activator" proxy.

- Code: `cmd/flexinfer-proxy`
- Code: `cmd/flexinfer-proxy`
- Tests: `go test ./cmd/flexinfer-proxy/...`

## Model Management

When working on Model Caching features:

- **CRD**: `api/v1alpha1/modelcache_types.go`
- **Controller**: `controllers/modelcache_controller.go`
- **Verification**:
  - Requires a cluster with a default StorageClass (or configured one).
  - Use `kubectl get modelcache` to debug the Provisioning phase.
  - The Downloader Job (`<cache-name>-downloader`) logs are the source of truth for download failures.

## MLC-LLM Backend

MLC-LLM provides high-performance inference for AMD GPUs via ROCm. It uses TVM for JIT compilation.

### Supported Model Formats

MLC-LLM requires pre-compiled models from the `mlc-ai` organization on HuggingFace:
- Format: `mlc-ai/<ModelName>-<quantization>-MLC`
- Example: `mlc-ai/Qwen3-0.6B-q4f16_1-MLC`

### ModelCache with MLC-LLM

Use `ModelCache` to pre-download MLC models to shared storage:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelCache
metadata:
  name: qwen3-0.6b-mlc
  namespace: flexinfer-system
spec:
  source: mlc://mlc-ai/Qwen3-0.6B-q4f16_1-MLC
  existingClaimName: mlc-models-nfs  # Use existing NFS PVC
  modelPath: Qwen3-0.6B-q4f16_1-MLC
  storageStrategy: SharedPVC
```

Then reference it in a `ModelDeployment`:

```yaml
apiVersion: ai.flexinfer/v1alpha1
kind: ModelDeployment
metadata:
  name: qwen3-mlc
  namespace: flexinfer-system
spec:
  backend: mlc-llm
  model: Qwen3-0.6B-q4f16_1-MLC
  modelCacheRef: qwen3-0.6b-mlc
  replicas: 1
  resources:
    limits:
      amd.com/gpu: "1"
      memory: 8Gi
```

### Performance Notes

Tested on AMD 7900 XTX (24GB VRAM):

| Model | Quantization | TPS | Memory |
|-------|--------------|-----|--------|
| Qwen3-0.6B | q4f16_1 | 377 | ~3.8GB |

MLC-LLM uses `--mode local` by default to reduce memory footprint. For high-throughput scenarios, set `--mode server` via environment variable.

### JIT Compilation

First startup takes ~15s to JIT-compile kernels for the target GPU architecture. Compiled libraries are cached at `/root/.cache/mlc_llm/`.

## Common Issues

### `controller-gen` errors

If you see errors about "invalid array length" or toolchain issues, verify you are using the latest `controller-gen`. The `make setup` command should handle this.

### Benchmark Job Stuck Running

The benchmark job uses a sidecar pattern. If the LLM backend container doesn't terminate, the job won't complete. The benchmarker signals the backend to shut down via a shared volume sentinel file. If stuck, check:

```bash
kubectl logs <benchmark-pod> -c llm-backend
kubectl delete job <name>-benchmark -n flexinfer-system
```
