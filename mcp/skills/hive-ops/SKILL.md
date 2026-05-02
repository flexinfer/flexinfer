---
name: hive-ops
description: "Day-2 operations for the loom-hive-operator deployed in k3s: inspect status, pause/resume autonomy, replay council runs, audit merges, hot-reload policy, recover from a corrupted DB, and stage feature flag rollouts. Use when an operator asks about hive state, a council run is paused/stuck, audit findings need triage, policy needs a hot-reload, or a v2 feature flag needs to flip."
version: 0.1.0
---

# Hive Ops

> **Note:** This source SKILL.md mirrors the registry entry in
> `mcp/context/skills-registry.yaml` (entry: `hive-ops`). The published
> per-platform SKILL.md bundles (Codex / Claude / Gemini / Kilocode) are
> generated from the registry by `loom sync`. Edit the registry's
> `instructions:` block, then regenerate.

## Purpose

Day-2 operations for the always-on `loom-hive-operator` cluster controller
that runs the Loom Hive Council + Pipeline. Audience is a cluster operator
working with the `loom hive` CLI on a Mac and the operator deployment in
k3s under namespace `loom-hive`. Read-only inspection is safe; every
mutating endpoint (`/run`, `/escalate`, `/sync`, kill switch edits, DB
restore Jobs) is gated behind explicit admin confirmation and, where
possible, GitOps.

## When to use

- User asks about hive status, council/pipeline progress, or eval scores.
- A council run is paused, stuck, or producing regressions.
- A pipeline run is hung in `running` and needs a force-escalate.
- Audit findings (Loop A artifact judge, Loop B merge attribution) need
  triage or a replay against a different ensemble.
- Policy needs to change (kill switch, ensemble, gates, budgets) and
  reload via fsnotify without restarting the pod.
- A v2 feature flag (`squads`, `audit`, `debate`, `cross-repo`,
  `adaptive-policy`) is staging from local → dev → prod.
- Operator state file is suspected corrupt and needs MinIO restore.

## Workflow

1. **Read current state.** Capture a snapshot before changing anything:
   - `loom hive status`
   - `loom hive pipelines list --state=running`
   - `loom hive eval list --subject=council`
   - `bash mcp/skills/hive-ops/scripts/hive_status_snapshot.sh > /tmp/hive-status.md`
2. **Inspect the deployment in k3s.** Use `kc-k3s` (i.e. `KUBECONFIG=platform/gitops/.kube/k3s.yaml`):
   - `kubectl -n loom-hive get pods,cm,pvc,svc`
   - `kubectl -n loom-hive logs deploy/loom-hive-operator --tail=200`
   - Look for the `policy reloaded` log line after any ConfigMap change.
3. **Decide action class.** Match symptom → action:
   - autonomy too aggressive → **pause** via kill switch
   - council outputs regressing → **replay** with a different ensemble
   - merge needs review → **audit** by MR iid (Loop B attribution)
   - pipeline run stuck → **force-escalate** the specific run
   - state file corrupt → **recover** from MinIO backup
   - new v2 capability ready → **rotate** feature flag (staged rollout)
4. **Apply via GitOps.** All policy edits live in
   `platform/gitops/k3s/hive/configmap-policy.yaml`. Edit, commit, push,
   then `flux reconcile kustomization apps -n flux-system --with-source`.
   **Never `kubectl edit configmap`** — Flux will revert it on the next
   reconcile and you will fight the controller.
5. **Verify.** Operator hot-reloads policy via fsnotify within seconds.
   Confirm the new posture: status snapshot, log line, and (for
   v2 flips) `loom_hive_regression_count_total` over the next 24h.

## Common scenarios

**Pause autonomy (kill switch)**
```bash
# Edit platform/gitops/k3s/hive/configmap-policy.yaml: policy.enabled: false
git -C platform/gitops commit -am "ops(hive): pause autonomy"
git -C platform/gitops push
flux reconcile kustomization apps -n flux-system --with-source
kubectl -n loom-hive logs deploy/loom-hive-operator --tail=20 | grep "policy reloaded"
```

**Replay council with new ensemble**
```bash
loom hive council dryrun                 # safe: scratch DB, no commits
# Edit policy.council.ensemble in configmap-policy.yaml, commit, reconcile
loom hive council run                    # real replay under new policy
# Compare in HUD: Hive → Eval panel (Loop A scores)
```

**Audit a merged change by MR iid**
```bash
curl -sf -H "Authorization: Bearer $LOOM_ADMIN_TOKEN" \
  "$LOOM_HIVE_OPERATOR_URL/api/hive/pipeline/runs?mr_iid=<iid>"
# Loop B attribution returns originating council run id; look up Loop A
# score via /api/hive/eval?subject_kind=council_run&subject_id=<id>
```

**Force-escalate a stuck pipeline run**
```bash
loom hive pipelines list --state=running
curl -X POST -H "Authorization: Bearer $LOOM_ADMIN_TOKEN" \
  "$LOOM_HIVE_OPERATOR_URL/api/hive/pipeline/runs/<run_id>/escalate"
# Reconciler does not auto-retry escalated items; an operator owns the next move.
```

**Recover state from MinIO backup**
```bash
# 1) Confirm corruption inside the pod
kubectl -n loom-hive exec deploy/loom-hive-operator -- \
  sqlite3 /var/lib/loom-hive/state.db 'PRAGMA integrity_check;'
# 2) Scale to 0, run the restore Job from docs/HIVE_RUNBOOK.md, scale to 1
# Items committed since the last nightly backup must be re-run; council
# is idempotent and the backlog regenerates from .loom/backlog/*.yaml.
```

**Rollout staging (v2 feature flag flip)**
Local → dev cluster (kc-k3s) → production. One-week soak between flips.
Watch `loom_hive_regression_count_total` after each flip; pause via
`enabled: false` if non-zero in the first 24h. Sequence is
`squads → audit (advisory) → debate (incident only) → cross-repo →
adaptive policy (manual-apply)`. Detail in
`.loom/94-implementation-plan-hive-v2-hierarchical-swarm-2026-05-02.md`
Phase 8.

## Reference

- Architecture and policy reference: `docs/HIVE.md`
- Full day-2 procedures (pause/resume, escalate, replay, recover, rollout):
  `docs/HIVE_RUNBOOK.md` — do not duplicate, refer.
- Companion skill notes (sources, common commands, telemetry, kill
  switch, eval framework, rollout staging): `mcp/skills/hive-ops/references/workflow.md`
- Read-only snapshot script: `mcp/skills/hive-ops/scripts/hive_status_snapshot.sh`
- v2 plan and acceptance criteria:
  `.loom/94-implementation-plan-hive-v2-hierarchical-swarm-2026-05-02.md`

## Out of scope

Implementation work for hive v2 (Phase 4+ slices: squads, audit
follow-ups, debate, cross-repo, adaptive policy) is owned by
`feature-dev` or `parallel-slice-ship`, not by `hive-ops`. This skill
covers operating what is already deployed; landing new code goes
through the standard worktree → tests → MR loop.
