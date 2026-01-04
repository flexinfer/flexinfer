# FlexInfer Assistant Notes

This repo is a Go-based Kubernetes operator + runtime agents for GPU-aware LLM inference scheduling.

## Key Docs

- `README.md`: project overview and current status
- `AGENTS.md`: component responsibilities and runtime flags
- `ROADMAP.md`: prioritized feature work

## Code Entry Points (Binaries)

- `cmd/flexinfer-agent/main.go`: node GPU/CPU discovery + labeling daemon
- `cmd/flexinfer-bench/main.go`: benchmarking job binary (invoked by controller)
- `cmd/flexinfer-manager/main.go`: controller-manager for `ModelDeployment`/`ModelCache`
- `cmd/flexinfer-sched/main.go`: scheduler extender HTTP server
- `cmd/flexinfer-proxy/main.go`: scale-to-zero proxy (WIP)

## Common Dev Commands

- Build controller: `make build` (writes `bin/manager`)
- Unit/integration tests: `make test`
- Render Helm chart: `helm template flexinfer charts/flexinfer --namespace flexinfer-system`

## Deployment Notes

- The Helm chart lives in `charts/flexinfer/` and now bundles CRDs in `charts/flexinfer/crds/`.
- The custom scheduler name used by workloads is `flexinfer-scheduler` (set by the controller).
