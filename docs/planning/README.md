# Planning

This folder is the home for **forward-looking planning** for FlexInfer (feature inventory, roadmap slices, and implementation plans).

If you’re looking for “what exists today” docs, start here instead:

- Project overview: `README.md`
- Roadmap (high-level): `ROADMAP.md`
- Implementation status (detailed): `docs/IMPLEMENTATION_STATUS.md`
- Architecture overview: `docs/dev/architecture.md`
- CRD / contract docs: `docs/specs/README.md`
- v1alpha2 user docs: `docs/user/models-v1alpha2.md`

## How to use this folder

- `feature-inventory.md`: what features exist, what’s stable, what’s experimental, and what’s missing.
- `next-roadmap.md`: the next series of features/enhancements to implement (prioritized).
- `spec-capsule-template.md`: reusable source-backed template, checklist, and examples for new feature slices.
- `slice-readiness-gate.md`: ready-for-implementation gate for target modules, agent delegation notes, validation, generated artifacts, and rollback notes.
- `roadmap-reconciliation.md`: post-merge checklist for keeping roadmap docs, `.loom` context, and GitLab issues aligned.
- `context-curve-benchmark.md`: spec capsule for the reporting-only long-context benchmark curve MVP.
- `rocm-gfx1100-deploy-swap-tracing-slice.md`: PR-2 readiness/proof capsule for gfx1100 deploy/swap controls, latency metrics, and opt-in tracing.
- `context-aware-router-execution.md`: execution checklist for issue `#8` (context-aware routing).
- `phase-1-controller-api-hardening.md`: PR-sized checklist for the next series of controller hardening work.
- `../design/multi-tenancy.md`: M1 design for namespace-isolated tenancy baseline.
- `phase-5-multi-cluster.md`: forward-looking checklist for multi-cluster federation (future).

## Current Focus (February 2026)

- Mixed-vendor homelab clusters (k3s) with AMD ROCm `gfx1100` (RX 7900) and NVIDIA Maxwell `sm_52` (GTX 980 Ti).
- Making scheduling decisions "obviously correct" by keeping node labels + telemetry stable:
  - `flexinfer.ai/gpu.vendor`, `flexinfer.ai/gpu.arch`, `flexinfer.ai/gpu.vram`
  - `flexinfer.ai/gpu-free-memory` (scheduler headroom scoring)
- Backend guardrails and docs for the two problem-child GPU classes:
  - ROCm gfx1100 guide: `docs/user/backends-rocm-gfx1100.md`
  - Maxwell guide: `docs/user/backends-maxwell.md` and `build/README-maxwell.md`

Reference homelab topology used while hardening these paths:

- `cblevins-7900xtx`: AMD RX 7900 XTX (ROCm `gfx1100`)
- `cblevins-5930k`: AMD RX 7900 XTX (ROCm `gfx1100`)
- `cblevins-gtx980ti`: NVIDIA GTX 980 Ti (Maxwell `sm_52`)

Operational note (k3s homelab): if a node goes NotReady/unreachable, old FlexInfer pods can get stuck `Terminating` on that node and block Helm rollouts. Force-delete the stuck pods in `flexinfer-system` to unblock upgrades.

## Planning checklist

Before implementation starts on a multi-file feature or operational workflow
change, copy `spec-capsule-template.md`, then use `slice-readiness-gate.md` to
mark it `Ready` only after filling in:

- Goal, non-goals, users/operators, and current evidence.
- Requirements and acceptance criteria.
- PR-sized implementation slices with target files/modules.
- Agent delegation notes for parallel work, including safe-to-edit modules,
  files/modules to avoid, local verification, and expected output signals.
- Validation commands and generated artifacts.
- Rollout/backout notes for Helm, CRD, Flux, and runtime-image changes.
- After merge, run `roadmap-reconciliation.md` before closing tracking issues.

## Loom context pack

For deeper “planning artifacts” (product spec, implementation plan, decisions/worklog), see:

- `.loom/00-index.md`
