---
title: Spec Capsule Template
description: Reusable feature contract template and checklist for FlexInfer roadmap slices.
---

# Spec Capsule Template

Use this template before multi-file feature work, operational workflow changes,
or any roadmap slice where implementation details could drift across sessions.
Routine bug fixes and urgent incident response can use a shorter note, but they
should still capture acceptance criteria and validation commands before code
changes begin.

## Copyable Template

````markdown
# <Feature or Slice Name>

Tracking:
- Issue: <link or "none">
- Roadmap item: <link or file reference>
- Owner: <person or agent>
- Status: Draft | Ready | In Progress | Complete

## Goal

What user/operator outcome changes when this slice is done?

## Non-Goals

What is intentionally out of scope for this slice?

## Users / Operators

Who is affected, and what workflow do they need to complete?

## Current Evidence

- Code paths:
- Docs/runbooks:
- Prior incidents or issues:
- Command output or cluster evidence:

## Requirements

- Functional:
- Operational:
- Observability/status:
- Compatibility:
- Security/RBAC:

## Acceptance Criteria

- [ ] Behavior is implemented and documented.
- [ ] Status/errors are actionable for operators.
- [ ] Required generated assets are refreshed.
- [ ] Local validation commands pass.
- [ ] Rollout/backout path is clear.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| 1 | | | | |

## Validation Plan

Run before opening the MR:

```bash
<targeted command>
<broader command>
```

Cluster or runtime checks, when required:

```bash
<kubectl/flux/flexinfer command>
```

## Rollout / Backout

- Rollout:
- Backout:
- Risk controls:

## Open Questions

- [ ] Question:

## Sources

- `path/to/file.go:123`
- `docs/path.md:45`
- Issue/MR:
- Command:
````

## Pre-Implementation Checklist

- [ ] The goal names the operator or user outcome, not only the code change.
- [ ] Non-goals define what this slice will not attempt.
- [ ] Evidence includes at least one source-backed code path, document, issue,
      incident note, or command result.
- [ ] Acceptance criteria are testable by a reviewer.
- [ ] Target files/modules are listed before implementation begins.
- [ ] Validation commands are explicit and runnable from the repo root.
- [ ] Generated artifacts are named when API, CRD, Helm, or docs indexes change.
- [ ] Rollout and backout notes cover Helm, CRD, Flux, and runtime-image changes
      when those surfaces are touched.
- [ ] Parallel ownership boundaries are clear if more than one agent or human may
      work on the slice.

## Example: Controller / API Slice

```markdown
# GPUGroup Preemption Status

Tracking:
- Issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/<id>
- Roadmap item: `ROADMAP.md`
- Status: Ready

## Goal

Operators can see when a model is waiting because another model owns the target
GPUGroup, and they can identify the blocking deployment without reading
controller logs.

## Non-Goals

- Do not change scheduler scoring.
- Do not introduce a new CRD version.

## Current Evidence

- Code paths: `controllers/model_controller.go`, `api/v1alpha2/model_types.go`
- Docs/runbooks: `docs/user/models-v1alpha2.md`
- Command output: `kubectl get model <name> -o yaml`

## Requirements

- Set an actionable status condition when placement is blocked by an active
  GPUGroup owner.
- Preserve existing Ready/Loading semantics.
- Regenerate CRDs after status type changes.

## Acceptance Criteria

- [ ] Controller test covers the blocked-preemption condition.
- [ ] `make manifests` updates CRD schema when needed.
- [ ] `go test ./controllers/... ./api/...` passes.
- [ ] User docs explain the condition and operator next action.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| Status condition | `api/v1alpha2`, `controllers` | Controller/API only | `go test ./controllers/...` | Revert status condition and CRD changes |
| Docs | `docs/user/models-v1alpha2.md` | Docs only | `rg "Preempted" docs` | Revert docs |
```

## Example: Runtime Image Slice

```markdown
# ROCm gfx906 vLLM Runtime Refresh

Tracking:
- Issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/<id>
- Status: Ready

## Goal

Radeon VII nodes can run the custom vLLM image from a reproducible Dockerfile
without source-install shell drift.

## Non-Goals

- Do not upgrade the serving chart.
- Do not promote the image digest to production manifests in this slice.

## Current Evidence

- Code paths: `build/docker/vllm-custom-gfx906.Dockerfile`
- Prior failure: CI job log for the failed image build
- Runtime docs: `build/README-gfx906.md`

## Requirements

- Keep shell branches POSIX-safe under Docker `RUN`.
- Pin package versions or explain why floating source is required.
- Capture a boot-only canary command before promotion.

## Acceptance Criteria

- [ ] Docker build job succeeds.
- [ ] `make test` remains green.
- [ ] Runtime README documents the canary image and validation command.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| Build fix | `build/docker` | Runtime image only | CI image build | Revert Dockerfile change |
| Docs | `build/README-gfx906.md` | Runtime docs only | `rg "gfx906" build` | Revert docs |
```

## Example: Operational-Docs-Only Slice

```markdown
# Longhorn Attachment Recovery Runbook

Tracking:
- Issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/<id>
- Status: Ready

## Goal

Operators can recover a stuck ModelCache after a node death without guessing
which Kubernetes or Longhorn objects to inspect.

## Non-Goals

- Do not change controller reconciliation behavior.
- Do not automate Longhorn cleanup.

## Current Evidence

- Docs/runbooks: `docs/DEVELOPMENT.md`, `docs/user/troubleshooting.md`
- Command output: `kubectl get volumeattachments | rg <pvc-uid>`

## Requirements

- List read-only diagnostic commands first.
- Separate safe remediation from destructive cleanup.
- Include when to stop and escalate.

## Acceptance Criteria

- [ ] Runbook gives ordered commands and expected signals.
- [ ] No production manifest changes are included.
- [ ] Links from planning or user docs point to the runbook.

## Implementation Slices

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| Runbook | `docs/user/troubleshooting.md` | Docs only | `rg "VolumeAttachment" docs` | Revert docs |
```
