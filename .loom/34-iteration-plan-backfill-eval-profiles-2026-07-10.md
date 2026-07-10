# Per-model backfill evaluation profiles

**Date:** 2026-07-10
**Roadmap milestone:** turn additional idle warm-model lanes into safe,
model-appropriate evaluation capacity.

## Riskiest assumption + kill-test

**Load-bearing assumption:** the Radeon VII Qwen lane can run the shared
gauntlet safely as background work when the declaration can specialize the
model target and coherence contract.

**Kill test:** run the published gauntlet image against
`qwen3-1p7b-tools-radeonvii=llamacpp` with the chat API and a literal `READY`
probe. Pass only when the verdict is 3/3, `lastActiveTime` is unchanged, and the
serving pod keeps the same UID, readiness, and restart count.

**Failure mode if the assumption is wrong:** a production declaration would
either create false-negative artifacts or consume serving capacity in a way
that resets the foreground idle window.

**Status:** passed 2026-07-10. Positive evidence: the tailored profile passed
3/3 and persisted a Radeon VII benchmark while pod
`ffb62280-4bd8-4d04-88fd-66fcf452ee32` remained Ready with zero restarts and
`lastActiveTime` stayed `2026-07-09T11:43:36Z`. Disconfirming evidence: the
stock arithmetic contract failed because this deployed Qwen chat template
answered `2`, proving one global probe is not truthful across model lanes.

## Review

- The chat-aware shared gauntlet is deployed and proven background-safe.
- `ModelBackfill` currently copies the template verbatim, so its model list and
  probe contract cannot be specialized per declaration.
- The Radeon VII Qwen model is warm, healthy, and otherwise idle enough to
  contribute evaluation artifacts without a cold start.

## Align

### Scope in

- Add bounded literal environment overrides to `ModelBackfill`.
- Replace existing literal or `valueFrom` entries deterministically.
- Reject invalid names and reserve `FLEXINFER_WORKLOAD_CLASS` for the
  controller.
- Deploy a Radeon VII Qwen evaluation declaration using the shared template.
- Update CRDs, chart copies, docs, and roadmap truth.

### Scope out

- Secret or ConfigMap-backed override values.
- Per-container selectors or command/argument mutation.
- GPU resource requests, leases, model eviction, or placement changes.
- Weakening foreground/gaming preemption semantics.

### Acceptance criteria

- `spec.env` supports at most 32 literal key/value overrides.
- Overrides apply to every regular copied Job container in sorted order and
  clear an existing `valueFrom` source.
- Invalid names or the reserved workload-class key produce a stable Blocked
  status without creating a Job.
- The controller still injects background workload classification into every
  init and regular container.
- The deployed Radeon VII declaration reaches a truthful terminal verdict
  without changing foreground activity or restarting the serving model.

## Land

1. API, generated CRD/deepcopy, controller merge/validation logic, and tests.
2. Radeon VII declaration plus operator documentation and roadmap update.
3. Published controller rollout followed by Flux model reconciliation.

## Prove

- `go test ./controllers -run ModelBackfill`
- `go test -race ./controllers -run ModelBackfill`
- `make test`
- `kubectl kustomize deploy`
- CRD/chart parity and clean generated diff.
- Merge-request and master pipelines to terminal green.
- Live ModelBackfill status, Job verdict, model `lastActiveTime`, serving pod
  UID/readiness/restarts, and published image digest.

## Handoff/harvest

- Record the Radeon VII artifact and safety evidence in roadmap current truth.
- Treat new model lanes as profile additions, not shared-template forks.
- Consider typed evaluation-profile CRDs only if literal environment maps
  become too broad or need cross-declaration reuse.
