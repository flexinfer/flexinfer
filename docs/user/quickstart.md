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

