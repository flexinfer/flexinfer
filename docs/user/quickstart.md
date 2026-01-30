---
title: Quickstart
description: Install FlexInfer and deploy your first model.
---

# Quickstart

This guide gets FlexInfer installed and serving a model in a Kubernetes cluster.

## Prerequisites

- Kubernetes 1.25+
- At least one GPU-capable node (NVIDIA or AMD) with drivers + device plugin installed
- `kubectl` and `helm` v3

## Install (Helm)

From the repo root:

```bash
helm upgrade --install flexinfer services/flexinfer/charts/flexinfer \
  --namespace flexinfer-system \
  --create-namespace
```

### Verify install

```bash
kubectl -n flexinfer-system get pods
kubectl get crds | rg 'flexinfer' || true
```

You should see (names vary by chart release name):

- Controller: `*-controller`
- Proxy: `*-proxy`
- Scheduler extender: `*-scheduler`
- Node agent: `*-agent` (DaemonSet)

## Deploy a model (recommended: v1alpha2 `Model`)

Start with the minimal example:

```bash
kubectl apply -n flexinfer-system -f services/flexinfer/examples/v1alpha2/model-basic.yaml
```

Watch it progress:

```bash
kubectl -n flexinfer-system get models -w
kubectl -n flexinfer-system describe model llama3-8b
```

## List models (proxy)

Port-forward the proxy Service:

```bash
kubectl -n flexinfer-system port-forward svc/flexinfer-proxy 8080:80
```

Then:

```bash
curl -s http://127.0.0.1:8080/v1/models | jq .
```

## Send a request (OpenAI-style)

The proxy can infer the target model from the OpenAI-style JSON body:

```bash
curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "llama3-8b",
    "messages": [{ "role": "user", "content": "Say hello in one sentence." }]
  }' | jq .
```

If the model is scaled-to-zero, the proxy queues the request, triggers activation, and forwards once ready.

## Clean up

```bash
kubectl -n flexinfer-system delete model llama3-8b
```

---

## Backend-Specific Quickstarts

FlexInfer supports multiple inference backends. Choose one based on your GPU and requirements.

### Ollama (Simplest)

Best for: Quick deployments, auto-downloading models, NVIDIA or AMD GPUs.

```bash
kubectl apply -n flexinfer-system -f examples/quickstart-ollama.yaml
```

**Test:**
```bash
curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "llama31-8b-ollama",
    "messages": [{"role": "user", "content": "Say hello in one sentence."}]
  }'
```

**Characteristics:**
- Auto-pulls models from Ollama registry
- Cold start: 5-15 seconds
- Works with NVIDIA and AMD GPUs

### vLLM (High-Throughput)

Best for: Production workloads, NVIDIA GPUs, high concurrency.

```bash
kubectl apply -n flexinfer-system -f examples/quickstart-vllm.yaml
```

**Test:**
```bash
curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen25-7b-vllm",
    "messages": [{"role": "user", "content": "Explain quantum computing in 2 sentences."}],
    "max_tokens": 100
  }'
```

**Characteristics:**
- PagedAttention for efficient memory usage
- Cold start: 1-3 minutes (CUDA kernel compilation)
- NVIDIA GPUs only
- Best tokens/second throughput

### MLC-LLM (AMD + NVIDIA)

Best for: AMD ROCm GPUs, optimized performance on both vendors.

```bash
kubectl apply -n flexinfer-system -f examples/quickstart-mlc-llm.yaml
```

**Test:**
```bash
curl -s http://127.0.0.1:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen3-8b-mlc",
    "messages": [{"role": "user", "content": "Write a haiku about programming."}]
  }'
```

**Characteristics:**
- Works with both NVIDIA and AMD GPUs
- Pre-quantized models (q4f16, q4f32)
- Cold start: 10-30 seconds
- Best for AMD ROCm environments

---

## Troubleshooting

### Model stuck in "Pending" phase

1. Check GPU availability:
   ```bash
   kubectl describe nodes | grep -A5 "nvidia.com/gpu\|amd.com/gpu"
   ```

2. Check pod events:
   ```bash
   kubectl describe pods -l flexinfer.ai/model=<model-name> -n flexinfer-system
   ```

### Model fails to start

1. Check model logs:
   ```bash
   kubectl logs -l flexinfer.ai/model=<model-name> -n flexinfer-system --tail=100
   ```

2. Common issues:
   - **OOM**: Reduce `gpuMemoryUtilization` or use smaller model
   - **CUDA errors**: Ensure NVIDIA driver matches container CUDA version
   - **ROCm errors**: Use `q4f32` quantization instead of `q4f16`

### Requests timeout during cold start

Increase the cold start timeout in your Model spec:

```yaml
spec:
  serverless:
    coldStartTimeout: 300s  # 5 minutes
```

Or disable serverless for always-on:

```yaml
spec:
  serverless:
    enabled: false
```

