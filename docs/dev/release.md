---
title: Release & images
description: Build/push images and update the Helm chart.
---

# Release & images

FlexInfer uses multiple container images (controller, agent, scheduler, proxy, benchmarker).

## Dockerfiles

Component images:

- `services/flexinfer/build/Dockerfile.manager.bin`
- `services/flexinfer/build/Dockerfile.agent.bin`
- `services/flexinfer/build/Dockerfile.sched.bin`
- `services/flexinfer/build/Dockerfile.proxy.bin`
- `services/flexinfer/build/Dockerfile.bench.bin`

The `.bin` Dockerfiles copy a prebuilt binary (build it first with `make build`); see `build/README.md` for the full image map.

Backend images (examples; not required for the control plane itself):

- `services/flexinfer/build/Dockerfile.mlc-*`
- `services/flexinfer/build/Dockerfile.vllm-*`
- `services/flexinfer/build/Dockerfile.diffusers-rocm`
- `services/flexinfer/build/Dockerfile.comfyui-rocm-gfx1100`

## Build an image

Example (proxy):

```bash
cd services/flexinfer
make build
docker build -f build/Dockerfile.proxy.bin -t ghcr.io/flexinfer/flexinfer-proxy:dev .
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

