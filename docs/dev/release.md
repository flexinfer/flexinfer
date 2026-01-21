---
title: Release & images
description: Build/push images and update the Helm chart.
---

# Release & images

FlexInfer uses multiple container images (controller, agent, scheduler, proxy, benchmarker).

## Dockerfiles

Component images:

- `services/flexinfer/build/Dockerfile.manager`
- `services/flexinfer/build/Dockerfile.agent`
- `services/flexinfer/build/Dockerfile.sched`
- `services/flexinfer/build/Dockerfile.proxy`
- `services/flexinfer/build/Dockerfile.bench`

Backend images (examples; not required for the control plane itself):

- `services/flexinfer/build/Dockerfile.mlc-*`
- `services/flexinfer/build/Dockerfile.vllm-*`
- `services/flexinfer/build/Dockerfile.diffusers-rocm`
- `services/flexinfer/build/Dockerfile.comfyui-rocm`

## Build an image

Example (proxy):

```bash
cd services/flexinfer
docker build -f build/Dockerfile.proxy -t ghcr.io/flexinfer/flexinfer-proxy:dev .
```

## Wire images into Helm

Update `services/flexinfer/charts/flexinfer/values.yaml` (or override via `--set`) for:

- `controller.image.repository` / `controller.image.tag`
- `agent.image.repository` / `agent.image.tag`
- `scheduler.image.repository` / `scheduler.image.tag`
- `proxy.image.repository` / `proxy.image.tag`

Then:

```bash
helm upgrade --install flexinfer services/flexinfer/charts/flexinfer \
  --namespace flexinfer-system
```

