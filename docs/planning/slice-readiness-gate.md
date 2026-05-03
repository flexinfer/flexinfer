---
title: Slice Readiness Gate
description: Ready-for-implementation checklist for FlexInfer feature slices.
---

# Slice Readiness Gate

Use this gate after drafting a spec capsule and before starting multi-file
implementation. The goal is not ceremony; it is to make the next commit small,
reviewable, reversible, and easy to validate from repo evidence.

A slice is ready when a reviewer or another agent can answer these questions
without reading chat history:

- What outcome changes?
- Which files or modules may change?
- Which files or modules are explicitly off-limits for nearby parallel work?
- Which commands prove the slice?
- What output or signal should those commands produce?
- Which generated assets must be refreshed?
- How do we roll back if the slice misbehaves after merge?

## Ready States

| State | Meaning | Allowed next action |
|-------|---------|---------------------|
| Draft | Goal exists, but target files or validation are incomplete. | Keep planning. |
| Ready | Scope, evidence, validation, and rollback notes are complete. | Start implementation. |
| In Progress | Implementation has started against a Ready slice. | Keep plan updated as evidence changes. |
| Blocked | A required decision, dependency, cluster state, or artifact is missing. | Record blocker and next action. |
| Complete | Code/docs are merged and validation evidence is linked. | Close issue and reconcile roadmap. |

## Gate Checklist

- [ ] Goal names the operator or user outcome.
- [ ] Non-goals make this slice small enough for one MR.
- [ ] Evidence links to source paths, docs, issue/MR, prior incident, or command output.
- [ ] Requirements are split by functional, operational, observability/status,
      compatibility, and security/RBAC concerns when relevant.
- [ ] Acceptance criteria are reviewable and testable.
- [ ] Target files/modules are named before implementation starts.
- [ ] Owner boundary is clear, especially when parallel agents may work nearby.
- [ ] Agent delegation notes are present when the work may be split across more
      than one person or agent.
- [ ] Delegation notes name safe-to-edit files/modules, files/modules to avoid,
      local verification commands, and expected output/signals for each
      workstream.
- [ ] Validation commands are explicit and runnable from the repo root.
- [ ] Generated artifacts are listed when APIs, CRDs, Helm templates, docs indexes,
      or runtime assets change.
- [ ] Rollout/backout notes cover every affected operational surface.
- [ ] Open questions are resolved or intentionally marked non-blocking.

## Required Slice Table

Every multi-file feature plan should include a table with these columns:

| Slice | Target files/modules | Owner boundary | Validation | Rollback/backout |
|-------|----------------------|----------------|------------|------------------|
| Example | `controllers/...`, `api/...` | Controller/API only | `go test ./controllers/...` | Revert controller/API commit and CRD output |

Rules:

- `Target files/modules` must be specific enough to catch accidental scope creep.
- `Owner boundary` should name the subsystem, not just the person.
- `Validation` should include the narrowest useful command and any broader command.
- `Rollback/backout` should name the revert path and any live-cluster action.

## Required Agent Delegation Notes

Add this section when the slice is likely to be split across parallel agents or
reviewers. Each row should be independently assignable without requiring a chat
thread to recover the boundaries.

| Workstream | Safe-to-edit files/modules | Do not touch | Local verification | Expected output/signals |
|------------|----------------------------|--------------|--------------------|-------------------------|
| Example | `controllers/...` | `charts/`, runtime Dockerfiles | `go test ./controllers/...` | Tests pass; CRD output unchanged |

Rules:

- `Safe-to-edit files/modules` must be narrow enough for separate branches or
  linked worktrees.
- `Do not touch` must name nearby shared contracts, generated files, or
  production manifests that belong to another workstream.
- `Local verification` must be runnable from the repo root unless the row
  explicitly says it needs a cluster or CI-only environment.
- `Expected output/signals` should name the success condition a reviewer can
  check quickly, such as "tests pass", "only CRD schema changes", "Helm renders
  without env var drift", or "docs include the rollback command".
- If two workstreams share an API, CRD, Helm value, image tag, or status field,
  add coordination notes naming the shared contract and merge order.

## Surface-Specific Requirements

### API / CRD

Before implementation:

- Name API packages, controller packages, and generated CRD paths.
- Decide whether conversion, defaults, validation, or status schema changes are in scope.
- List generated commands, usually `make manifests`.

Validation:

```bash
make manifests
go test ./api/... ./controllers/...
git diff -- config/crd
```

Rollback/backout:

- Revert the API/controller commit and regenerated CRDs together.
- If CRDs were applied to a cluster, document whether rollback requires
  `kubectl apply -f charts/flexinfer/crds/` or a forward-fix migration.

### Helm / Flux

Before implementation:

- Name values, templates, chart metadata, and Flux-managed manifests.
- Identify whether Helm upgrades, manual CRD apply, or Flux reconciliation are required.
- Record default behavior for existing installs.

Validation:

```bash
helm lint charts/flexinfer
helm template flexinfer charts/flexinfer --namespace flexinfer-system
```

Rollback/backout:

- Revert chart/template changes.
- For deployed clusters, document the exact HelmRelease or Flux reconciliation command
  that returns the cluster to the prior revision.

### Runtime Images

Before implementation:

- Name Dockerfiles, build scripts, runtime docs, and image tags/digests.
- Decide whether this slice builds, publishes, or only prepares the runtime.
- Include a boot-only or canary command before promotion.

Validation:

```bash
make test
docker build <args>
```

Rollback/backout:

- Revert image references or restore the prior digest.
- Leave the previous known-good runtime tag in the spec or deployment notes.

### Controller / Scheduler / Proxy Behavior

Before implementation:

- Name the reconciler, scheduler, proxy, or routing package boundaries.
- List status/metrics/logging changes separately from behavior changes.
- Identify E2E or cluster checks when behavior depends on Kubernetes state.

Validation:

```bash
go test ./controllers/... ./scheduler/... ./internal/proxy/...
make test
```

Rollback/backout:

- Revert behavior and status/metrics changes together when they form one contract.
- If a live rollout happened, document scale-down, image rollback, or Flux reconcile steps.

### Operational Docs Only

Before implementation:

- Name the docs and index links that will change.
- Confirm no production manifests, scripts, or runtime assets are in scope.

Validation:

```bash
git diff --check
rg "<new runbook term>" docs
```

Rollback/backout:

- Revert the docs commit.
- Remove stale links from planning or user-facing indexes.

## Ready Declaration

Add this short block to the spec capsule when the gate is satisfied:

```markdown
## Readiness

Status: Ready

- Target files/modules:
- Owner boundary:
- Agent delegation notes:
- Validation commands:
- Generated artifacts:
- Rollout/backout:
- Non-blocking open questions:
```

If any required field is unknown, keep the slice in `Draft` or `Blocked`.
Implementation can still proceed during incidents, but the MR should explain why
the gate was bypassed and what validation was performed instead.
