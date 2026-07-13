# ModelExperiment Artifact Gate — Iteration Plan

- **Date:** 2026-07-13
- **Lineage:** `.loom/36-iteration-plan-model-experiment-mvp-2026-07-10.md`

## Outcome

Allow a `ModelExperiment` to be declared while a long-running `ModelCache`
pipeline is active, without creating a candidate or consuming GPU capacity
until strict artifact evidence is ready.

## Riskiest assumption and kill test

The load-bearing assumption is that `ModelCache` status contains enough durable
evidence to open the gate without inferring success from a Job or filesystem
marker.

The kill test checks both sides:

1. Positive: a `Ready` cache with timestamped successful validation, completed
   OCI publish, valid digest, and matching candidate source creates a candidate
   annotated with the cache UID, generation, and digest.
2. Negative: missing, running, failed, unvalidated, digestless, or source-
   mismatched caches create no candidate and report a specific blocked reason.

Failure mode: launching against a partial or mutable artifact, or polling a
blocked experiment continuously for a multi-hour quantization job.

Status: passed in the controller test suite; live proof follows CRD/controller
rollout by applying the staged Qwen3.5 64K experiment while quantization runs.

## Contract

- `spec.artifactGate.modelCacheRef` is same-namespace.
- Validation and immutable digest evidence are independently opt-in.
- Gate waiting does not start `spec.timeout`.
- An indexed `ModelCache` watch wakes only matching experiments.
- Candidate annotations preserve the exact cache UID and digest evidence.
- The gate synchronizes launch; it does not promote or mutate production Models.

## Validation

- Focused positive and negative controller tests.
- Generated DeepCopy, RBAC, and CRD manifests.
- Full Go test suite and Helm lint.
- Live blocked experiment with zero candidate resources while quantization runs.

## Rollback

Delete the staged experiment, then revert the controller/API commit. Existing
experiments without `artifactGate` retain their current behavior.
