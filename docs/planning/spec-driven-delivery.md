---
title: Spec-Driven Delivery
description: Delivery acceleration contract for spec capsules, slice readiness, and runtime promotion evidence.
---

# Spec-Driven Delivery

Tracking:

- Roadmap section: `docs/planning/next-roadmap.md`
- High-level roadmap: `ROADMAP.md`
- SD-3 issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/57
- SD-4 issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/58
- SD-5 issue: https://gitlab.flexinfer.ai/services/flexinfer/-/issues/59
- Canonical runtime evidence: `.loom/60-validation-matrix.md`

This plan keeps multi-agent feature delivery auditable from spec to runtime
evidence. Specs define the operator outcome, slice readiness constrains the
implementation boundary, and the validation matrix records the evidence used to
promote or block runtime canaries.

## Delivery Contract

1. Draft a spec capsule with goal, non-goals, requirements, acceptance criteria,
   validation, rollout/backout, and sources.
2. Pass `docs/planning/slice-readiness-gate.md` before multi-file
   implementation begins.
3. Add agent delegation notes when work may be split across parallel humans or
   agents. Notes must name safe-to-edit files/modules, files/modules to avoid,
   local verification commands, and expected output/signals per workstream.
4. For runtime, canary, quantization, or GPU-specific work, add or update a row
   in `.loom/60-validation-matrix.md`.
5. Reconcile `docs/planning/next-roadmap.md`, `ROADMAP.md`, and issue links
   after the slice merges.
6. Use `docs/planning/roadmap-reconciliation.md` for the post-merge checklist
   and issue closing note so planning state is not trapped in commit messages.

## SD-3: Validation Matrix Contract

Goal: runtime canary promotions on gfx1100 can be audited from a spec or roadmap
entry to concrete runtime evidence without relying on chat history.

Operational gate:

- Runtime digest promotion dry-runs must point operators at
  `.loom/60-validation-matrix.md` before `--apply`.
- The matrix row must include the target digest/ref, canary command and result,
  rollback digest/ref or rollback manifest, hardware lane, and spec/MR/issue
  pointer.
- The matrix must keep the four required lane categories visible even when
  evidence is incomplete: `gfx1100` textgen, `gfx1100` imagegen, `gfx906`
  textgen/quantization, and `gfx906` imagegen/offload.

Owner boundary for SD-3:

- Owned files: `.loom/60-validation-matrix.md`,
  `docs/planning/spec-driven-delivery.md`,
  `docs/planning/next-roadmap.md`.
- Runtime promotion guardrail file: `scripts/promote-runtime-digest.sh`.
- Runtime promotion guardrail test: `scripts/test-promote-runtime-digest.sh`.
- Optional alignment file: `ROADMAP.md`.
- Out of scope: controller code, runtime image builds, CRDs, Helm manifests, and
  live-cluster changes.

Acceptance criteria:

- [x] `.loom/60-validation-matrix.md` is named as the canonical canary and
      runtime-promotion evidence table for gfx1100 work.
- [x] Every matrix row has explicit columns for `artifact`, `context_length`,
      `gpu_class`, `runtime_image` or `oci_ref`, `observed_failure_mode`,
      `spec_roadmap_link`, and `promotion_decision`.
- [x] The matrix defines allowed `promotion_decision` values and the evidence
      required for `promote`, `conditional`, `block`, `fail`, `skip`, and
      `pending`.
- [x] Existing Gemma4 26B/31B and Qwen-family placeholders remain represented,
      with known canary failures and conditional promotions preserved.
- [x] Runtime image digest or OCI ref gaps are visible as follow-up evidence,
      not hidden in prose.
- [x] `docs/planning/next-roadmap.md` links or names
      `.loom/60-validation-matrix.md` as the canonical SD-3 evidence artifact.
- [x] `ROADMAP.md` is aligned with the same SD-3 status and evidence artifact.
- [x] `scripts/promote-runtime-digest.sh` dry-runs remind operators to fill the
      validation matrix gate before applying digest changes.

Validation commands for SD-3:

```bash
git diff --check
rg "artifact|context_length|gpu_class|runtime_image|oci_ref|observed_failure_mode|spec_roadmap_link|promotion_decision" .loom/60-validation-matrix.md docs/planning/spec-driven-delivery.md docs/planning/next-roadmap.md ROADMAP.md
rg ".loom/60-validation-matrix.md|Issue #57|SD-3" docs/planning/spec-driven-delivery.md docs/planning/next-roadmap.md ROADMAP.md
scripts/test-promote-runtime-digest.sh
scripts/check-runtime-profile-consistency.sh
```

Rollout/backout:

- Rollout: merge the docs-only SD-3 contract after diff and targeted `rg`
  validation pass.
- Backout: revert the docs commit. No live cluster, CRD, Helm, or runtime image
  rollback is required.

## SD-4: Agent-Ready Delegation Contract

Goal: feature plans can be split into parallel workstreams without relying on
chat history to recover ownership boundaries, validation expectations, or merge
order.

Owner boundary for SD-4:

- Owned files: `docs/planning/spec-capsule-template.md`,
  `docs/planning/slice-readiness-gate.md`,
  `docs/planning/spec-driven-delivery.md`,
  `docs/planning/next-roadmap.md`, and `ROADMAP.md`.
- Optional alignment file: `docs/planning/README.md`.
- Out of scope: product feature implementation, controller code, runtime image
  builds, CRDs, Helm manifests, live-cluster changes, and issue triage beyond
  the SD-4 tracking issue.

Acceptance criteria:

- [x] Spec capsules include an `Agent Delegation Notes` section for parallel
      work.
- [x] Delegation notes name safe-to-edit files/modules and files/modules to
      avoid for each workstream.
- [x] Delegation notes include local verification commands and expected
      output/signals per workstream.
- [x] The slice readiness gate requires delegation notes when work is split
      across more than one human or agent.
- [x] Examples cover controller/API, runtime image, and operational-docs-only
      slices.
- [x] The active ROCm/gfx1100 deploy-swap + tracing plan has an agent delegation
      table with disjoint workstreams and expected verification signals.
- [x] `docs/planning/next-roadmap.md` and `ROADMAP.md` mark SD-4 complete.

Validation commands for SD-4:

```bash
git diff --check
rg "Agent Delegation Notes|Safe-to-edit files/modules|Do not touch|Expected output/signals" docs/planning/spec-capsule-template.md docs/planning/slice-readiness-gate.md docs/planning/spec-driven-delivery.md docs/planning/rocm-gfx1100-deploy-swap-tracing-slice.md
rg "SD-4|Issue #58|agent-ready" docs/planning/spec-driven-delivery.md docs/planning/next-roadmap.md ROADMAP.md
```

Rollout/backout:

- Rollout: merge the docs-only SD-4 contract after diff and targeted `rg`
  validation pass.
- Backout: revert the docs commit. No live cluster, CRD, Helm, or runtime image
  rollback is required.

## SD-5: Roadmap Reconciliation Discipline

Goal: roadmap docs, `.loom` context, and GitLab tracking issues stay aligned
after planning changes, so the next work item can be selected from durable repo
state instead of chat history.

Owner boundary for SD-5:

- Owned files: `docs/planning/roadmap-reconciliation.md`,
  `docs/planning/spec-driven-delivery.md`,
  `docs/planning/next-roadmap.md`, `docs/planning/README.md`,
  `ROADMAP.md`, and `.loom/00-index.md`.
- Optional issue updates: GitLab issues `#1` and `#59`.
- Out of scope: production manifests, controller/runtime code, CRDs, Helm
  changes, and generated validation evidence.

Acceptance criteria:

- [x] A reusable roadmap reconciliation checklist exists under
      `docs/planning/`.
- [x] The checklist names `ROADMAP.md`, `docs/planning/next-roadmap.md`,
      `docs/planning/spec-driven-delivery.md`, `.loom/00-index.md`, and GitLab
      issues as required sync surfaces.
- [x] The checklist includes completion-note guidance for closing issues after
      MRs merge.
- [x] `ROADMAP.md`, `docs/planning/next-roadmap.md`, and `.loom/00-index.md`
      record that SD-1 through SD-5 are complete as tracked planning contracts.
- [x] GitLab issue `#59` is updated and closed after the MR lands.

Validation commands for SD-5:

```bash
git diff --check
rg "roadmap-reconciliation|SD-5|Issue #59|#59" ROADMAP.md docs/planning/next-roadmap.md docs/planning/spec-driven-delivery.md docs/planning/README.md docs/planning/roadmap-reconciliation.md .loom/00-index.md
rg "ROADMAP.md|docs/planning/next-roadmap.md|.loom/00-index.md|GitLab issues" docs/planning/roadmap-reconciliation.md
```

Rollout/backout:

- Rollout: merge the docs-only SD-5 contract after diff and targeted `rg`
  validation pass, then close issue `#59` with the MR and validation summary.
- Backout: revert the docs commit and reopen `#59` if the reconciliation
  checklist is wrong or incomplete. No live cluster, CRD, Helm, or runtime image
  rollback is required.

## Remaining Spec-Driven Delivery Items

All SD-1 through SD-5 delivery-acceleration contracts are now represented in
tracked planning docs. Future planning changes should use
`docs/planning/roadmap-reconciliation.md` after each merge.
