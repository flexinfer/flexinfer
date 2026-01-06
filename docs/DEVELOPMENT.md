# FlexInfer Development Guide

## Prerequisites

- Go 1.25+
- Docker
- Make

## Getting Started

1. **Setup Environment**:
   Run the setup target to install necessary tools (`controller-gen`, `kustomize`, `envtest`) and download dependencies.

   ```bash
   make setup
   ```

2. **Generate Manifests**:
   If you modify `api/...` or `controllers/...`, regenerate CRDs and RBAC:

   ```bash
   make manifests
   ```

3. **Run Tests**:
   Use unit tests for fast feedback and integration tests for controller behavior:

   ```bash
   make test-unit
   make test-integration
   ```

   To run everything with code generation and envtest setup:

   ```bash
   make test
   ```

4. **Build**:
   Build the manager binary:
   ```bash
   make build
   ```

## Scale-to-Zero (Serverless)

The project includes a serverless "Activator" proxy.

- Code: `cmd/flexinfer-proxy`
- Code: `cmd/flexinfer-proxy`
- Tests: `go test ./cmd/flexinfer-proxy/...`

## Model Management

When working on Model Caching features:

- **CRD**: `api/v1alpha1/modelcache_types.go`
- **Controller**: `controllers/modelcache_controller.go`
- **Verification**:
  - Requires a cluster with a default StorageClass (or configured one).
  - Use `kubectl get modelcache` to debug the Provisioning phase.
  - The Downloader Job (`<cache-name>-downloader`) logs are the source of truth for download failures.

## Common Issues

### `controller-gen` errors

If you see errors about "invalid array length" or toolchain issues, verify you are using the latest `controller-gen`. The `make setup` command should handle this.
