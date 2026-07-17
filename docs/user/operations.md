---
title: Operations
description: "Common day-2 workflows: inspect, debug, and clean up."
---

# Operations

## Inspect what’s running

```bash
kubectl -n flexinfer-system get deploy,ds,svc
kubectl -n flexinfer-system get pods -o wide
```

## Watch lifecycle state

### v1alpha2

```bash
kubectl -n flexinfer-system get models -w
kubectl -n flexinfer-system describe model <name>
```

### v1alpha1

```bash
kubectl -n flexinfer-system get modeldeployments -w
kubectl -n flexinfer-system describe modeldeployment <name>
```

## Current textgen lanes

The current Gemma 4 split is intentionally simple:

- `gemma4-e4b` and `gemma4-e4b-fast`: default fast lane on `cblevins-7900xtx`
- `gemma4-e4b-long`: long-context TurboQuant lane on `cblevins-5930k`

Useful checks:

```bash
kubectl -n flexinfer-system get models \
  gemma4-e4b-turboquant gemma4-e4b-turboquant-canary -o wide

kubectl -n flexinfer-system get pods -o wide | rg 'gemma4-e4b|flexinfer-runtime-gfx1100'
```

If OpenWebUI or LiteLLM is still surfacing stale model IDs, check the live model
catalog directly:

```bash
kubectl -n ai port-forward svc/litellm 8000:8000
curl -s http://127.0.0.1:8000/v1/models \
  -H "Authorization: Bearer ${LITELLM_MASTER_KEY}" | jq '.data[].id'
```

## Pin a model image to a digest

For reproducible deployments, pin a model's backend image to an immutable
content digest with `spec.imageDigest`. The digest is applied last in the
image-resolution chain, so it pins whatever image wins precedence (the
per-model `spec.image` override, the GPUProfile image, or the backend
default). Any existing tag or digest on the resolved image is replaced with
`@sha256:<digest>`.

Keep the human-readable tag in `spec.image` for documentation and let
`spec.imageDigest` guarantee reproducibility:

```yaml
apiVersion: ai.flexinfer/v1alpha2
kind: Model
spec:
  backend: vllm
  image: registry.harbor.lan/flexinfer/runtime:master   # readable tag
  imageDigest: sha256:d9def647d3f0520390ff8e1addd00762111b07a4bd77eec1108eb430f6cc8ff2
```

The field accepts either the `sha256:<hex>` form or a bare 64-character hex
digest (normalized to `sha256:` automatically). It is validated by the CRD
against `^(sha256:)?[a-f0-9]{64}$`.

Resolve a tag to its current digest with `flexinfer image pin`:

```bash
# Print the pinned reference (repo@sha256:<hex>):
flexinfer image pin registry.harbor.lan/flexinfer/runtime:master
# registry.harbor.lan/flexinfer/runtime@sha256:<hex>
# (stderr also prints the ready-to-paste `spec.imageDigest:` value)

# Print only the digest, e.g. for `kubectl patch`:
flexinfer image pin registry.harbor.lan/flexinfer/runtime:master --quiet
# sha256:<hex>
```

`image pin` queries the Registry v2 API directly (no `docker`/`oras` needed).
Supply credentials with `--username`/`--password` or the
`FLEXINFER_REGISTRY_USERNAME` / `FLEXINFER_REGISTRY_PASSWORD` environment
variables; anonymous access and the Docker Hub bearer-token flow both work.
Use `--insecure` for plain-HTTP (local/test) registries.

### Pin the operator's own component images (Helm)

The Helm chart pins the FlexInfer operator component images
(`controller`, `agent`, `proxy`, `scheduler`) to a digest via an `image.digest`
value. When set, it overrides the tag (`repository@sha256:...`) and the
pull policy auto-detects to `IfNotPresent`:

```yaml
# values.yaml
controller:
  image:
    repository: ghcr.io/flexinfer/flexinfer-controller
    digest: "sha256:<hex>"   # overrides tag when set
agent:    { image: { digest: "sha256:<hex>" } }
proxy:    { image: { digest: "sha256:<hex>" } }
scheduler: { image: { digest: "sha256:<hex>" } }
```

Resolve each digest with `flexinfer image pin <repository>:<tag>`.

Per-model *backend* runtime images (vLLM, llama.cpp, …) are pinned separately
via the Model CR's `spec.imageDigest` (above) or a digest-qualified
GPUProfile/`spec.image` reference.

## Debug a model that won't become ready

1. Check events:
   ```bash
   kubectl -n flexinfer-system describe pod <pod>
   ```
2. Check backend logs:
   ```bash
   kubectl -n flexinfer-system logs <pod> -c model --tail=200
   ```
3. Confirm GPU resources:
   ```bash
   kubectl get nodes -o wide
   kubectl describe node <node> | rg -n "nvidia.com/gpu|amd.com/gpu"
   ```

## NVIDIA GPU requirements

### Why `runtimeClassName: nvidia` is required

NVIDIA GPU workloads require the `nvidia` container runtime to function. FlexInfer automatically sets `runtimeClassName: nvidia` on pods requesting `nvidia.com/gpu` resources.

Without this runtime class:
- The pod may schedule successfully (it requests `nvidia.com/gpu` and a node has capacity)
- But `/dev/nvidia*` device nodes won't be mounted into the container
- CUDA will report no devices available

### Verifying NVIDIA runtime is working

1. Check if the runtime class exists:
   ```bash
   kubectl get runtimeclass nvidia
   ```

2. Verify devices are visible inside the pod:
   ```bash
   kubectl -n flexinfer-system exec <pod> -- ls /dev/nvidia*
   # Should show: /dev/nvidia0 /dev/nvidiactl /dev/nvidia-uvm ...
   ```

3. Check CUDA availability:
   ```bash
   kubectl -n flexinfer-system exec <pod> -- python -c "import torch; print(torch.cuda.is_available())"
   # Should print: True
   ```

### Common failure symptoms

| Symptom | Likely Cause |
|---------|--------------|
| `torch.cuda.is_available() == False` | Missing `runtimeClassName: nvidia` or NVIDIA driver not installed |
| Pod stuck in `ContainerCreating` | RuntimeClass `nvidia` doesn't exist on the cluster |
| `CUDA error: no CUDA-capable device is detected` | Device nodes not mounted; check runtime class |
| Pod runs but inference is slow | Fell back to CPU; check device availability |

### Cluster prerequisites

1. NVIDIA device plugin must be deployed (creates `nvidia.com/gpu` resources)
2. NVIDIA container runtime must be installed on GPU nodes
3. RuntimeClass `nvidia` must exist:
   ```yaml
   apiVersion: node.k8s.io/v1
   kind: RuntimeClass
   metadata:
     name: nvidia
   handler: nvidia
   ```

## Clean up (important: delete parents)

FlexInfer resources are hierarchical. Delete the parent, not the children.

- v1alpha2: delete the `Model`
  ```bash
  kubectl -n flexinfer-system delete model <name>
  ```
- v1alpha1: delete the `ModelDeployment` (not the Deployment)
  ```bash
  kubectl -n flexinfer-system delete modeldeployment <name>
  ```

For detailed cleanup guidance (including RAM caches and stuck Jobs), see the "Resource Cleanup Procedures" section in `services/flexinfer/AGENTS.md`.

## AMD ROCm GPU requirements

### Container setup

AMD GPUs require ROCm-compatible container images. FlexInfer uses ROCm variants automatically when AMD GPUs are detected.

```bash
# Verify ROCm device visibility
kubectl -n flexinfer-system exec <pod> -- ls /dev/dri/
# Should show: card0 renderD128 (or similar)

kubectl -n flexinfer-system exec <pod> -- rocm-smi
# Should show GPU(s) with temperature, utilization, etc.
```

### Common AMD issues

| Symptom | Likely Cause |
|---------|--------------|
| `No GPU detected` | Missing ROCm container toolkit or device plugin |
| `HSA_STATUS_ERROR_OUT_OF_RESOURCES` | Insufficient GPU memory; reduce batch size |
| Slow inference | Using CPU fallback; check `/dev/kfd` visibility |

### Cluster prerequisites for AMD

1. AMD device plugin deployed (creates `amd.com/gpu` resources)
2. ROCm drivers installed on GPU nodes (6.0+ recommended)
3. Container runtime configured for AMD GPUs

## Backend-specific quirks

### Ollama

- **Model naming**: Ollama uses `model:tag` format (e.g., `llama3.2:1b`)
- **Pull on first use**: Model downloads on first request if not cached
- **Memory**: Ollama manages its own memory; set `OLLAMA_NUM_PARALLEL` for concurrency

### vLLM

- **Memory configuration**: vLLM pre-allocates GPU memory
  ```yaml
  spec:
    config:
      gpu-memory-utilization: "0.9"  # Use 90% of GPU memory
  ```
- **Tensor parallelism**: For multi-GPU, set `tensor-parallel-size`
- **Known issue**: vLLM 0.4+ requires specific CUDA versions; check compatibility

### MLC-LLM

- **Model format**: Requires MLC-compiled models (`.mlc` format)
- **Source URI**: Use `HF://mlc-ai/<model>-MLC` for pre-compiled models
- **Maxwell GPUs (sm_52)**: Use the Maxwell-specific image variant
  ```yaml
  spec:
    image: registry.harbor.lan/flexinfer/mlc-llm:cuda-maxwell-v7
  ```

### llama.cpp

- **Model format**: Requires GGUF format models
- **CPU fallback**: Works without GPU; useful for testing
- **Memory mapping**: Uses mmap by default; can reduce memory usage
  ```yaml
  spec:
    config:
      n-gpu-layers: "35"  # Number of layers to offload to GPU
  ```

### ComfyUI / Diffusers

- **Image generation**: These backends are for image models, not LLMs
- **VRAM requirements**: Typically need 8GB+ VRAM for image models
- **Workflow files**: ComfyUI requires workflow JSON in the request

## Troubleshooting decision tree

```
Model not becoming Ready?
├── Check phase: kubectl describe model <name>
│   ├── Pending → No matching nodes (check GPU labels, node selector)
│   ├── Downloading → Network issue or invalid source URI
│   ├── Creating → Check pod events and logs
│   └── Error → Check conditions for specific reason
│
├── Pod not starting?
│   ├── ImagePullBackOff → Check image name/registry access
│   ├── ContainerCreating → Check RuntimeClass, volume mounts
│   └── CrashLoopBackOff → Check container logs
│
└── Pod running but model not responding?
    ├── Check model container logs
    ├── Verify port-forward to pod directly
    └── Check health endpoint: /health or /v1/models
```

## Metrics and monitoring

FlexInfer exposes Prometheus metrics:

```bash
# Scrape metrics from controller
kubectl -n flexinfer-system port-forward deploy/flexinfer-controller 8080:8080
curl localhost:8080/metrics

# Scrape metrics from proxy
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 8080:8080
curl localhost:8080/metrics
```

Key metrics:
- `flexinfer_models_total{phase}` - Models by phase
- `flexinfer_proxy_requests_total{model,status}` - Request counts
- `flexinfer_proxy_queue_depth{model}` - Pending requests per model

## Verify rollout draining

The proxy drains in-flight requests during rollouts instead of dropping
them (issue #65). See [Proxy → Rollout draining](proxy.md#rollout-draining)
for how the contract works (readiness flip → drain delay → bounded
`server.Shutdown` → `terminationGracePeriodSeconds`). This is the live
kill-test.

1. **Start a long request and hold the connection open.** Pick a model on
   the proxy and issue a completion large enough to run for tens of
   seconds:

   ```bash
   kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 8080:80 &
   curl -sS http://localhost:8080/v1/chat/completions \
     -H 'Content-Type: application/json' \
     -d '{"model":"<model>","max_tokens":2048,
          "messages":[{"role":"user","content":"Write a long essay."}]}' \
     > /tmp/held-request.json &
   HELD=$!
   ```

2. **Restart the proxy while the request is in flight:**

   ```bash
   kubectl -n flexinfer-system rollout restart deployment/flexinfer-proxy
   ```

3. **The held request must complete.** `wait $HELD` returns success and
   `/tmp/held-request.json` contains a full completion — the old pod
   drained it rather than dropping the connection.

4. **Probes through the label group keep succeeding.** New traffic routed
   through the Service is served by the new (or still-Ready) pods; the
   draining pod is already out of the endpoints because `/readyz` returned
   `503`:

   ```bash
   for i in $(seq 1 20); do
     curl -sf -o /dev/null -w '%{http_code}\n' http://localhost:8080/v1/models
     sleep 1
   done
   # expect: all 200
   ```

5. **Confirm the completed-drain metric increments.** Scrape the *old* pod
   before it exits (or check the recording rule):

   ```bash
   kubectl -n flexinfer-system exec <old-proxy-pod> -- \
     wget -qO- localhost:8080/metrics | grep flexinfer_proxy_shutdowns_total
   # flexinfer_proxy_shutdowns_total{result="started"}   1
   # flexinfer_proxy_shutdowns_total{result="completed"} 1
   ```

   A `result="timeout"` increment instead means an in-flight request
   outlasted `PROXY_GRACEFUL_SHUTDOWN_TIMEOUT`; raise the timeout (and
   `terminationGracePeriodSeconds` with it) for that lane.
