---
title: Backends
description: How the backend plugin system works and how to extend it.
---

# Backends

Backends are a small plugin surface that centralizes:

- container image selection (by GPU vendor/arch)
- ports
- args/env/probes
- default idle timeouts and startup timeouts

The goal is to keep controller logic backend-agnostic.

## Where it lives

- Interface: `services/flexinfer/backend/interface.go`
- Registry: `services/flexinfer/backend/registry.go`
- Implementations: `services/flexinfer/backend/*.go`

## Adding a new backend

1. Create a new file in `services/flexinfer/backend/` implementing `backend.Backend`.
2. Register it in an `init()` function:

```go
func init() {
  backend.MustRegister(&MyBackend{})
}
```

3. Ensure:
   - `Name()` returns a stable canonical name
   - `Aliases()` covers common spelling variants
   - `Image()` returns sensible defaults for NVIDIA/AMD (and CPU if supported)
4. Add/update tests in `services/flexinfer/backend/registry_test.go` as needed.

## Image overrides

The Helm chart exposes values for backend images and operator-level defaults.
See `services/flexinfer/charts/flexinfer/values.yaml`.

