---
title: Labels & annotations
description: Node labels, pod labels, and discovery/routing annotations.
---

# Labels & annotations

## Node labels (agent)

The node agent applies GPU capability labels. Common keys:

- `flexinfer.ai/gpu.vendor` → `AMD` / `NVIDIA`
- `flexinfer.ai/gpu.arch` → `gfx1100` / `sm_90` / etc
- `flexinfer.ai/gpu.vram` → `24Gi`
- `flexinfer.ai/gpu.count` → `1`
- `flexinfer.ai/gpu.int4` → `true|false`

See `services/flexinfer/AGENTS.md` for the full list.

## Workload labels (controller)

The controller labels managed pods/services for discovery:

- `flexinfer.ai/model`: model name
- `flexinfer.ai/backend`: backend name
- `flexinfer.ai/gpu-group`: shared group name (v1alpha2 `spec.gpu.shared`)

## LiteLLM discovery annotations

When enabled (`spec.litellm.enabled: true`), the controller adds:

- `litellm.flexinfer.ai/served-model`
- `litellm.flexinfer.ai/aliases` (comma-separated)
- `litellm.flexinfer.ai/copilot-model` (optional)

## Proxy ↔ GPUGroup annotations (v1alpha1)

The proxy writes queue state onto a `GPUGroup` so the controller can decide swaps:

- `flexinfer.ai/queue.<modelName>: "<depth>"`
- `flexinfer.ai/queue-since.<modelName>: "<rfc3339>"`

## Routing annotations

The proxy uses annotations to enable advanced routing strategies for multi-replica models.

### `flexinfer.ai/routing`

Enables direct pod routing instead of Kubernetes Service round-robin. Without this annotation, requests are routed through the Kubernetes Service for standard load balancing.

| Value | Description |
|-------|-------------|
| `session-affinity` | Route requests with the same session ID to the same pod for KV-cache locality |
| `prefix` | Route requests with the same system prompt to the same pod for shared prefix caching |
| `least-loaded` | Route to the pod with the fewest active connections |

**Example:**

```yaml
apiVersion: inference.flexinfer.ai/v1alpha2
kind: Model
metadata:
  name: my-chatbot
  annotations:
    flexinfer.ai/routing: session-affinity
```

**Note:** Models without this annotation use Kubernetes Service DNS for load balancing, which is the recommended default for most workloads.

## Service label routing

Service labels can be attached to a model and used for routing. Relevant fields/annotations:

- v1alpha2: `Model.spec.serviceLabels`
- v1alpha1: `ModelDeployment.spec.serviceLabels`
- proxy uses `ai.flexinfer/active-services` to cache active service labels during GPUGroup swaps

