---
name: flux-gitops-operator
description: "Operate Flux/Kustomize GitOps in platform/gitops: follow dev-env.sh context rules (kc-k3s/kc-harv), reconcile safely (no hot-edits), and verify rollouts with repeatable checklists and snapshots. Use when changing Kubernetes manifests, troubleshooting Flux drift, or validating deployments."
---

# Flux GitOps Operator

Standardize safe GitOps operations in `platform/gitops` (Flux-managed clusters), including reconcile workflows and rollout verification.

## Quick Start

1. From workspace root:
   - `cd platform/gitops`
2. Configure environment (required before cluster commands):
   - `source dev-env.sh`
3. Run status / troubleshoot:
   - `make status` / `make troubleshoot`

## Guardrails (Non-Negotiable)

- Prefer Git changes + Flux reconciliation; avoid hot-editing live objects (`kubectl edit`, `kubectl set env`) unless handling an incident.
- If an emergency hotfix is applied live, mirror it back into `platform/gitops/**` and commit immediately so Flux stops fighting you.

## Core Workflow

### 1) Make a Change

- Edit manifests in `platform/gitops/**`
- Commit with a clear message
- Push

### 2) Reconcile

- Reconcile the relevant kustomization (often `apps`):
  - `flux reconcile kustomization apps -n flux-system`
- Or use the wrapper:
  - `bash $CODEX_HOME/skills/flux-gitops-operator/scripts/flux_reconcile_kustomization.sh apps flux-system`

### 3) Verify Rollout

- Use the checklist: `assets/templates/rollout-checklist.md`
- Produce a quick namespace snapshot:
  - `bash $CODEX_HOME/skills/flux-gitops-operator/scripts/k8s_rollout_report.sh <namespace> > /tmp/rollout.md`

## References / Templates

- Workflow notes: `references/workflow.md`
- Rollout checklist: `assets/templates/rollout-checklist.md`

## Bundled Resources

- `scripts/ensure_dev_env.sh`
- `scripts/flux_reconcile_kustomization.sh`
- `scripts/k8s_rollout_report.sh`
- `references/workflow.md`
- `assets/templates/rollout-checklist.md`
