# ModelExperiment MVP — Iteration Plan

- **Date:** 2026-07-10
- **Canonical plan:** `plan-modelexperiment-mvp-ab03ee`
- **Lineage:** `.loom/30-implementation-plan-experiment-platform-2026-06-15.md`

## Outcome

Ship a v1alpha2 `ModelExperiment` resource and controller that owns an isolated
candidate `Model`, waits for it to serve, runs a same-namespace CronJob-derived
gauntlet against exactly that candidate, records a durable pass/fail verdict,
and deletes the GPU canary after terminal evaluation.

## Riskiest assumption and kill test

The load-bearing runtime assumption already passed live on 2026-06-17: current
vLLM served the Qwen3.5 MoE currency canary coherently on gfx1100 using
`deploy/debug/qwen36-currency-canary-model.yaml`.

The remaining controller kill test is deliberately two-sided:

1. Positive: a hardware experiment reaches `Succeeded`, records
   `status.verdict.pass=true`, persists benchmark evidence, and removes its
   owned candidate Model.
2. Negative: an impossible coherence threshold reaches `Failed`, records
   `status.verdict.pass=false`, and still removes its candidate Model.

Automatic promotion and mutation of Flux-owned Models are forbidden in this
slice.

## Contract

- `spec.candidate` is a complete `ModelSpec`; the controller assigns child name
  and ownership.
- `spec.gauntlet.templateRef` names a CronJob copied into an owned Job.
- `spec.gauntlet.env` supplies threshold overrides. `MODELS` is
  controller-owned and always targets `<candidate-name>=<backend>`.
- `spec.timeout` bounds the experiment from candidate creation through verdict.
- `spec.suspend` prevents or aborts active work.
- Terminal evidence remains in status and the owned Job; the GPU Model is
  removed.
- A spec generation change restarts with fresh owned resources.
- Deleting the experiment removes all owned resources before finalization.

## Deferred

Build hooks, automatic primary promotion, mutation of Flux-owned Models, MCP
tools, recurring experiments, and cross-namespace references.
