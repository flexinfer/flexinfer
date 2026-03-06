# Implementation Plan: CI Pipeline And k3s Rollout (2026-03-06)

## Goal

Land the smallest repo changes that materially reduce CI overhead, then rebuild loom locally and roll the updated custom-server image through `loom-hub`.

## Non-Goals

- Rework the global GitLab runner HelmRelease defaults.
- Change Flux source-of-truth workflow beyond updating the existing `loom-hub` kustomization tag.
- Redesign the integration smoke suite scope.

## Acceptance Criteria

- CI no longer clones `../../libs/*` inside job bootstrap.
- CI can fetch private module versions directly with job-token auth.
- `build:binaries` parallelizes MCP builds and heavy jobs request explicit CPU overrides.
- `test:unit` runs coverage without also paying race-detector cost on every branch pipeline.
- `make dev-reload` completes successfully from this workspace.
- A new `registry.harbor.lan/mcp/custom-server` image is built, pushed, and applied to `loom-hub`.
- `loom-hub` deployments converge on the new image tag.

## Implemented Changes

1. Dependency normalization
- Pinned `mcp-go`, `fi-mcp-kit`, and `fi-accel/go/fiaccel` to concrete versions in `go.mod`.
- Added `go.work` for local workspace overlays.

2. CI pipeline simplification
- Forced `GOWORK=off` in CI so jobs use pinned module versions, not local workspace overlays.
- Replaced sibling repo cloning with Git URL rewrite using `CI_JOB_TOKEN`.
- Reused cached `golangci-lint` when present.
- Added explicit CPU/memory overrides to prepare, lint, build, unit, integration, and race jobs.
- Parallelized MCP binary fan-out in `build:binaries`.
- Switched `test:unit` to coverage-only, leaving race testing in the dedicated `test:race` job.

3. Local Docker rollout path
- Reworked `docker-build-custom-server` to use the repo root as the main Docker context plus a named `libs` context.
- Updated `Dockerfile.custom-server.local` to:
  - match Go `1.25.7`
  - use private-module env settings
  - inject container-local `go mod edit -replace` mappings for the sibling libs

4. Cluster rollout
- Updated `loom-hub` kustomization to `registry.harbor.lan/mcp/custom-server:20260306-ci-fix`.
- Applied the updated manifest tree to k3s and verified rollout completion.

## Verification

- `GOWORK=off GOPRIVATE=gitlab.flexinfer.ai/* GONOSUMDB=gitlab.flexinfer.ai/* GOSUMDB=off GOPROXY=direct go build -buildvcs=false ./cmd/loom ./cmd/loomd ./cmd/custom-server`
- `GOPRIVATE=gitlab.flexinfer.ai/* GONOSUMDB=gitlab.flexinfer.ai/* GOSUMDB=off go build -buildvcs=false ./cmd/loom ./cmd/loomd ./cmd/custom-server`
- `GOFLAGS=-buildvcs=false GOWORK=off GOPRIVATE=gitlab.flexinfer.ai/* GONOSUMDB=gitlab.flexinfer.ai/* GOSUMDB=off GOPROXY=direct go test ./cmd/loom/... ./cmd/loomd/...`
- `GOFLAGS=-buildvcs=false GOPRIVATE=gitlab.flexinfer.ai/* GONOSUMDB=gitlab.flexinfer.ai/* GOSUMDB=off go test ./internal/integration/...`
- `GOFLAGS=-buildvcs=false GOPRIVATE=gitlab.flexinfer.ai/* GONOSUMDB=gitlab.flexinfer.ai/* GOSUMDB=off make dev-reload`
- `IMAGE_TAG=20260306-ci-fix make docker-push-custom-server`
- `kubectl apply -k /Users/cblevins/workspace/platform/gitops/k3s/loom-hub/servers`
- `kubectl -n loom-hub rollout status deployment/k8s-apps-k3s --timeout=120s`

## Sources

- `go.mod:35`
- `go.work:1`
- `.gitlab-ci.yml:61`
- `.gitlab-ci.yml:125`
- `.gitlab-ci.yml:168`
- `.gitlab-ci.yml:340`
- `.gitlab-ci.yml:398`
- `Dockerfile.custom-server.local:10`
- `Dockerfile.custom-server.local:32`
- `Makefile:979`
- `/Users/cblevins/workspace/platform/gitops/k3s/loom-hub/servers/kustomization.yaml:215`
