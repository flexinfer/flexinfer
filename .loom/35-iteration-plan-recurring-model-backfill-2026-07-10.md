# Recurring ModelBackfill

**Date:** 2026-07-10
**Roadmap milestone:** keep proven idle-model evaluations producing fresh,
durable artifacts without weakening the foreground-first contract.

## Riskiest assumption + kill-test

**Load-bearing assumption:** repeating a successful background evaluation
produces distinct durable benchmark history while remaining invisible to
foreground-idle accounting and preserving the warm serving process.

**Kill test:** inspect the live Radeon VII evaluation history after multiple
background runs. Pass only when Postgres contains distinct timestamped rows,
the Model `lastActiveTime` is unchanged, and the serving pod retains its UID,
Ready state, and zero restart count.

**Failure mode if the assumption is wrong:** recurrence would repeatedly spend
capacity without retaining useful history, reset the foreground idle window, or
destabilize the warm serving model.

**Status:** passed 2026-07-10. Positive evidence: Postgres contains four
distinct `qwen3-1p7b-tools-radeonvii` rows, including two production-profile
runs at 23.57 and 23.70 tok/s. `lastActiveTime` remained
`2026-07-09T11:43:36Z`; serving pod
`ffb62280-4bd8-4d04-88fd-66fcf452ee32` remained Ready with zero restarts.
Disconfirming evidence: the benchmark ConfigMap contains only the latest 23.70
tok/s sample, so recurrence is opt-in and operators must use an idempotent
template plus durable storage when historical artifacts matter.

## Review

- Both deployed ModelBackfills reached `Succeeded` and now stop permanently
  under the one-shot contract.
- Their Jobs are bounded, background-classified, and proven to yield for
  foreground or gaming demand.
- The gauntlet already writes append-only Postgres rows; its ConfigMap is a
  latest-value projection rather than history.

## Align

### Scope in

- Add optional `spec.repeatAfter` with a one-minute minimum nonzero cooldown.
- Repeat successful attempts only; failed attempts remain terminal.
- Publish `status.nextRunTime` and an explicit `RepeatScheduled` reason.
- Delete the completed owned Job before re-entering normal idle admission.
- Preserve cumulative attempt numbering, foreground/gaming gates, and bounded
  Job deadlines.
- Enable daily recurrence on the deployed Gemma and Radeon VII evaluations.

### Scope out

- Cron syntax, calendars, jitter, catch-up runs, or multiple schedules.
- Automatic retries after failed Jobs.
- Retention policy for Postgres or ConfigMaps.
- Repeating arbitrary non-idempotent templates by default.
- GPU leases, model eviction, or placement changes.

### Acceptance criteria

- Omitting `repeatAfter` preserves existing one-shot terminal behavior.
- A successful Job records `nextRunTime = completionTime + repeatAfter` and
  requeues for the remaining cooldown without polling.
- Before the cooldown expires, no new Job is created.
- When due, the controller removes the prior completed Job, clears per-attempt
  status, then re-enters the existing Ready/idle/admission gates.
- Attempt count increments and Job names remain unique across repeats.
- Failed Jobs do not repeat.
- Negative or sub-minute nonzero intervals are blocked without creating work.
- Suspension and spec changes retain their existing precedence.

### Dependencies and blockers

- Depends on the deployed background request class, ModelBackfill controller,
  per-model profiles, and durable Postgres benchmark store.
- No blocker identified; the live durability/safety kill-test passed.

## Land

1. API/status fields, recurrence validation, controller rearm state machine,
   generated deepcopy/CRDs, and focused reconcile tests.
2. Daily deployed policies plus user/operator documentation and roadmap truth.
3. Roll the controller before resuming the model Kustomization, then observe a
   successful run schedule its next execution.

## Prove

- `go test ./controllers -run 'ModelBackfill|Repeat'`
- `go test -race ./controllers -run 'ModelBackfill|Repeat'`
- `make test`
- `golangci-lint run ./controllers/... ./api/v1alpha2/...`
- `kubectl kustomize deploy/tasks/model-backfill`
- CRD/chart parity and clean generated diff.
- Merge-request and master pipelines to terminal green.
- Live `Succeeded/RepeatScheduled` status with a future `nextRunTime`, unchanged
  serving pod invariants, and Flux fully resumed.

## Handoff/harvest

- Record the first scheduled repeat and its durable row when it executes.
- Consider bounded history retention only after recurring row volume exists.
- Resume the unblocked `ModelExperiment` plan after this utilization loop is
  producing fresh evidence unattended.
