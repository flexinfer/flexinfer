---
title: flexinfer-config (Playground)
description: The YAML configuration schema validated by flexinfer-site’s playground.
---

# flexinfer-config (Playground)

`flexinfer-config.yaml` is a human-friendly configuration format intended for:

- quickly describing a model + runtime needs
- validating config in the `flexinfer-site` playground
- acting as a stable input to future tooling (CLI → CRDs)

This format is separate from the Kubernetes CRDs (`Model`, `ModelDeployment`, etc.).

## Schema overview

Top-level keys:

- `version` (required): semver-ish string, e.g. `1.0`
- `model` (required): which model + backend to run
- `resources` (optional): GPU/CPU/memory and replica count
- `serving` (optional): port, concurrency, timeouts
- `logging` (optional): level and format

## Example

```yaml
version: "1.0"
model:
  name: meta-llama/Llama-3.1-8B-Instruct
  backend: vllm
  quantization: awq
  context_length: 8192
resources:
  gpu:
    type: nvidia
    count: 1
    memory: 24Gi
  replicas: 1
  cpu: "4000m"
  memory: 32Gi
serving:
  port: 8080
  max_concurrent: 50
  timeout: 60s
logging:
  level: info
  format: json
```

## Field reference

### `model`

- `name` (required): model name or HuggingFace model ID
- `backend` (required): `vllm` | `llamacpp` | `tgi` | `ollama`
- `quantization` (optional): `awq` | `gptq` | `fp16` | `fp32` | `int8` | `int4`
- `context_length` (optional): integer
- `max_batch_size` (optional): integer

### `resources`

- `gpu.type` (optional): `amd` | `nvidia` | `cpu`
- `gpu.count` (optional): integer
- `gpu.memory` (optional): string like `24Gi`
- `replicas` (optional): integer
- `cpu` (optional): string like `4000m` or `4`
- `memory` (optional): string like `32Gi`

### `serving`

- `port` (optional): integer
- `max_concurrent` (optional): integer
- `timeout` (optional): string like `60s`, `5m`
- `health_check` (optional): boolean

### `logging`

- `level` (optional): `debug` | `info` | `warn` | `error`
- `format` (optional): `json` | `text`

## Source of truth (today)

The validator for this schema currently lives in:

- `services/flexinfer-site/lib/schemas/flexinfer-config.ts`

The corresponding JSON Schema artifact lives in:

- `services/flexinfer/specs/jsonschema/flexinfer-config.schema.json`

As the FlexInfer CLI evolves, this spec should become a shared artifact (generated schema + docs) so the playground and CLI don’t drift.
