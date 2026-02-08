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
- `phase-1-controller-api-hardening.md`: PR-sized checklist for the next series of controller hardening work.
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

## Loom context pack

For deeper “planning artifacts” (product spec, implementation plan, decisions/worklog), see:

- `.loom/00-index.md`
