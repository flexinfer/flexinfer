# Technical Debt Priority Ranking

Scored using weighted model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%.

| Rank | ID | Title | Component | Impact | Risk | Drag | Effort | Score |
|---:|---|---|---|---:|---:|---:|---:|---:|
| 1 | DEBT-001 | ConfigBool silently fails on string values from CRD config | backend/interface.go | 1.00 | 0.80 | 0.60 | 1.0 | 86.00 |
| 2 | DEBT-004 | Proxy model resolution only matched K8s resource name | internal/proxy/resolver.go | 1.00 | 0.80 | 0.80 | 3.0 | 84.00 |
| 3 | DEBT-002 | IfNotPresent pullPolicy causes stale image cache on tag reuse | deployment/pullPolicy | 0.80 | 0.60 | 1.00 | 2.0 | 78.00 |
| 4 | DEBT-003 | Diffusers image depends on unpinned base image and transitive deps | build/Dockerfile.diffusers-rocm | 0.60 | 0.80 | 0.60 | 2.0 | 69.00 |
| 5 | DEBT-005 | Controller reconciliation produces no logs on deployment updates | controllers/model_controller.go | 0.60 | 0.40 | 0.80 | 1.0 | 64.00 |
| 6 | DEBT-007 | Docker and K3s containerd use separate image stores | platform/gitops + Docker/containerd | 0.60 | 0.40 | 0.80 | 3.0 | 58.00 |
| 7 | DEBT-008 | GPU device misidentification: pod scheduled on iGPU instead of dGPU | controllers/model_controller.go | 0.60 | 0.60 | 0.40 | 3.0 | 56.00 |
| 8 | DEBT-006 | No fp16-fix VAE bundled for SDXL on ROCm | build/Dockerfile.diffusers-rocm | 0.60 | 0.40 | 0.40 | 2.0 | 53.00 |

## Suggested Cut Lines

- Wave 1: top 20-30% by score, low dependency risk
- Wave 2: next 30-40%, medium effort and moderate coupling
- Wave 3: remaining strategic refactors with cross-team dependencies
