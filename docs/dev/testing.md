---
title: Testing
description: Unit tests, integration tests (envtest), and targeted suites.
---

# Testing

## Quick unit tests

```bash
cd services/flexinfer
make test-unit
```

## Integration tests (envtest)

Runs controller tests with `envtest` assets:

```bash
cd services/flexinfer
make test-integration
```

## Full suite

Includes codegen + fmt/vet:

```bash
cd services/flexinfer
make test
```

## Targeted suites

- GPUGroup tests:
  ```bash
  cd services/flexinfer
  make test-gpugroup
  ```
- Proxy tests:
  ```bash
  cd services/flexinfer
  make test-proxy
  ```

