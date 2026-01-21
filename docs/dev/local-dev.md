---
title: Local development
description: Fast build/test loops and running components locally.
---

# Local development

## Prereqs

- Go (see `services/flexinfer/go.mod`)
- Docker (optional, for image builds)
- `kubectl` (for talking to a cluster)

## One-time setup

Installs local tools (`controller-gen`, `kustomize`, `envtest`) into `services/flexinfer/bin/`:

```bash
cd services/flexinfer
make setup
```

## Build binaries

```bash
cd services/flexinfer
make build-all
```

Outputs go to `services/flexinfer/bin/`.

## Run tests

Fast (unit) loop:

```bash
cd services/flexinfer
make test-unit
```

Integration tests (envtest):

```bash
cd services/flexinfer
make test-integration
```

## Regenerate CRDs / RBAC

If you change anything under `services/flexinfer/api/` or controller markers:

```bash
cd services/flexinfer
make manifests
```

Generated CRDs land in `services/flexinfer/config/crd/`.

## Run components locally

### Controller manager

Runs against your current kubeconfig:

```bash
cd services/flexinfer
make run
```

### Scheduler extender

```bash
cd services/flexinfer
go run ./cmd/flexinfer-sched --port 8082
```

### Proxy

```bash
cd services/flexinfer
go run ./cmd/flexinfer-proxy --port 8080 --log-level debug
```

## Install CRDs into a cluster

```bash
cd services/flexinfer
make install
```

