# FlexInfer Roadmap

> Last Updated: 2026-07-14
> Tier: 1 (see workspace AGENTS.md "Portfolio Tiers")
> Tracking Issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/63

<!--
Convention (portfolio-refresh 2026-H2, see libs/STANDARDS.md "Roadmap & Backlog"):
- This file states CURRENT TRUTH, derived from git activity and deployed state —
  never re-date stale content. Each refresh MR must cite its evidence (git-log
  window inspected, deploy-state query used).
- Backlog lives in GitLab issues (P1/P2/P3 labels + milestones), NOT in this file.
  This file links the backlog; it does not duplicate it.
- If a live plan exists in the agent-context plan store, reference its plan_id
  here; the store is canonical and this file is a rendered summary.
- Staleness SLO: Tier 1/2 repos must have this file dated within 90 days.
  `bin/portfolio-inventory --roadmaps` reports conformance.
-->

## Current Status

FlexInfer is the production Kubernetes AI-inference operator running the homelab
LLM stack: CRD-driven model deployments (models, GPU groups, `LoRAAdapter`,
`ModelCatalog`), an OpenAI-compatible serverless proxy with scale-to-zero,
KV-cache-aware routing, quantization pipelines with a quality gate, and
multi-cluster support. Core phases 1–5 plus the advanced-feature wave (KV-cache
tiering, dynamic multi-LoRA, OCI model registry, flash-loader, spot resilience,
CNCF-prep) shipped by 2026-03; the full completed-phase history lives in git and
`docs/planning/`. Recent activity (git log `master`, 2026-06-25..2026-07-14): the
gaming-mode program landed — Steam client + persistent game storage on the
7900xtx gaming node, hostNetwork/Sunshine ports, runbook + node-mode metrics +
opt-in idle auto-revert, and crash supervision with degraded-session reporting —
alongside model-routing fixes keeping gemma4-26b primary on the 5930k card. The
benchmark gauntlet now runs weekly and after publish with throughput plus
chat-aware coherence verdicts while retaining a raw-completions compatibility
mode ([#27](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/27)).
The next hardware-utilization frontier is also implemented: `ModelBackfill`
runs bounded CPU-side Jobs against an already-warm model only after a continuous
foreground-idle window, and cancels them for foreground demand or gaming intent
without unloading the serving model. Successful evaluations can recur after an
opt-in cooldown, while failures remain terminal. Qwen3.5 recovery safeguards,
build-node disk controls, and Renovate dashboard validation also landed in this
window. The experiment-platform frontier now has an end-to-end
`ModelExperiment`: it owns an isolated candidate, runs the gauntlet as a
run-fenced Job, records a typed verdict, and releases hardware without mutating
production or Flux-owned Models. Successful experiments can opt into recurring
certification with bounded verdict and Job history; failures remain terminal.
The clean, non-abliterated Qwen3.5 35B GPTQ artifact is now the dual-RX-7900-XTX
workhorse: both replicas expose 128K context with graph mode, FP8 KV cache, and
the same immutable ROCm runtime. Shared `workhorse-128k` traffic now selects the
Ready Model with the fewest active proxy connections and round-robins ties; the
live busy-member kill-test passed 4/4 with explicit decision and target metrics.
Routine proxy rollouts can still interrupt an in-flight long request; graceful
draining is tracked as [#65](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/65).

- **Plan**: `.loom/32-iteration-plan-model-backfill-2026-07-09.md` (frontier iteration; kill-test passed)
- **Follow-on**: `.loom/33-iteration-plan-chat-gauntlet-2026-07-10.md` (live chat-vs-completions kill-test passed)
- **Frontier profile**: `.loom/34-iteration-plan-backfill-eval-profiles-2026-07-10.md` (Radeon VII profile kill-test passed)
- **Recurring frontier**: `.loom/35-iteration-plan-recurring-model-backfill-2026-07-10.md` (durable-history kill-test passed)
- **Experiment frontier**: `.loom/36-iteration-plan-model-experiment-mvp-2026-07-10.md` (canonical plan `plan-modelexperiment-mvp-ab03ee`)
- **Recurring experiments**: `.loom/37-iteration-plan-recurring-model-experiment-2026-07-10.md` (run-fencing kill test)
- **128K workhorse routing**: `.loom/40-iteration-plan-label-group-least-loaded-2026-07-14.md` (live busy-member kill-test passed)
- **Deployed**: K3s GPU cluster via Flux (flexinfer stack incl. fi-mcp-gateway)
- **CI**: custom (bespoke `.gitlab-ci.yml` + `.gitlab/ci/` includes: BuildKit image matrix, Go build/test, Trivy, Helm)

## Now

- Operate the two clean Qwen3.5 35B GPTQ replicas as the shared 128K workhorse
  for Loom, Mills, and Council traffic. Preserve exact artifact/runtime parity,
  watch per-Model active connections plus label-group target hits, and keep
  long-context coherence—not nominal window size—as the promotion gate.

## Next

- [ ] Stage major docker dependency updates (Python 3.14 / CUDA 12.9 / ROCm 6.4) in a controlled rollout ([#21](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/21))
- [ ] Gracefully drain in-flight proxy requests during rollouts ([#65](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/65))

## Later

- Gaming-mode hardening follow-ups as usage feedback lands (idle auto-revert default-on, session telemetry)
- Renovate-driven dependency hygiene via the Dependency Dashboard ([#9](https://gitlab.flexinfer.ai/services/flexinfer/-/issues/9))
- CNCF Sandbox submission (prep artifacts shipped; submit when adoption evidence justifies it)

## Backlog

Full backlog: [P1 issues](https://gitlab.flexinfer.ai/services/flexinfer/-/issues?label_name[]=P1) ·
[P2](https://gitlab.flexinfer.ai/services/flexinfer/-/issues?label_name[]=P2) ·
[P3](https://gitlab.flexinfer.ai/services/flexinfer/-/issues?label_name[]=P3) ·
[Milestones](https://gitlab.flexinfer.ai/services/flexinfer/-/milestones)
