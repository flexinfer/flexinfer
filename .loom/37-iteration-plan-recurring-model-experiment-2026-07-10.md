# Recurring ModelExperiment Certification — Iteration Plan

- **Date:** 2026-07-10
- **Lineage:** `.loom/36-iteration-plan-model-experiment-mvp-2026-07-10.md`

## Outcome

Extend `ModelExperiment` with opt-in successful-run recurrence so a proven
candidate/runtime tuple can be re-certified without hand-creating new objects.
Each run gets generation-and-run-fenced children, the current verdict stays
typed in status, bounded prior verdicts remain visible, and failed runs remain
terminal. Production Models and aliases are never mutated.

## Riskiest assumption and kill test

The load-bearing assumption is that a successful terminal experiment can start
a fresh run without a retained Job or delayed cache event from the prior run
being accepted as current evidence.

The controller kill test is a two-run lifecycle: finish run 1, wait until its
declared repeat time, start run 2 with distinct child names and labels, then
present run-1 evidence while run 2 is active. Run 2 must remain uncompleted,
run 1 must be present in bounded history, and a run-2 verdict must be accepted
only from the run-2 Job.

## Contract

- `spec.repeatAfter` is opt-in and schedules another run only after success.
- `spec.historyLimit` bounds completed prior runs retained in status.
- `status.run` and child labels fence both spec generation and run identity.
- Run-1 child names stay upgrade-compatible; recurring child names include
  generation and run identity, and all new children carry both fence labels.
- The current verdict remains in `status.verdict`; it moves into
  `status.history` only when the next run starts.
- A failed run, invalid spec, suspension, or deletion never schedules more work.
- Existing one-shot declarations retain their behavior.

## Validation

- Focused controller lifecycle and stale-run tests.
- API/CRD generation checks and full Go test suite.
- Helm CRD synchronization and chart lint.

## Rollback

Revert the feature commit. Existing objects without recurrence fields remain
one-shot; no production Model state or external data migration is involved.
