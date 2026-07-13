# Quantization Progress Telemetry — Iteration Plan

- **Date:** 2026-07-13
- **Lineage:** `.loom/30-implementation-plan.md` progress reporting lane

## Outcome

Make `ModelCache.status.quantization` report actual structured work progress and
last activity time instead of presenting elapsed-time/timeout ratio as percent
complete.

## Riskiest assumption and kill test

The load-bearing assumption is that the active quantizer emits recent,
timestamped JSON progress within the same 2,000-line log window the controller
already reads for completed-layer telemetry.

Live positive evidence on the Qwen3.5 35B job showed `percent=42.0`, `layer 17`,
and a current timestamp in that window while the CR incorrectly reported 21%.
Negative tests feed noisy TQDM output, malformed/out-of-range JSON, out-of-order
events, and transient read loss; none may replace or regress trustworthy status.

Failure mode: a dashboard reports timeout consumption as work completion, or a
temporary Kubelet log error moves progress backwards.

Status: live kill-test passed; controller and live-status proof remain.

## Contract

- One pod-log read supplies both progress status and completed-layer metrics.
- Newest valid `event=progress`, `phase=quantizing` telemetry is authoritative.
- `progressSource` distinguishes telemetry from `elapsed-estimate` fallback.
- `lastProgressAt` records the source event timestamp.
- Last trustworthy telemetry survives transient log-read failures.
- Older images without telemetry retain bounded elapsed-time estimates.

## Validation

- Parser tests for real/noisy/malformed/out-of-order telemetry.
- Active reconcile tests for telemetry preference and fallback preservation.
- Full Go tests, vet, CRD sync, Helm lint, and live status correction.

## Rollback

Revert the controller/API commit. Existing status fields remain backward
compatible and older clients ignore the additive fields.
