# Loom Mills v2 — Rollback Playbook

When a v2 feature misbehaves in production, the cheapest mitigation is almost always a single ConfigMap edit + Flux reconcile — the operator hot-reloads policy on every reconcile tick (60s default). This playbook walks the operator through the three rollback layers: feature flag disable, policy proposal revert, and (last resort) canonical-DB restore from MinIO.

## When to roll back

| Symptom | First-line action |
|---|---|
| Squad routing degrading merge rate | [Disable squads (8.3-1)](#disable-squads) |
| Audit pool flooding noise / blocking merges | [Disable audit OR flip back to advisory_only (8.3-2)](#disable-audit) |
| Council debate burning budget without quality gain | [Disable debate for that trigger (8.3-3)](#disable-debate) |
| Cross-repo coordinator stuck mid-merge | [Disable cross_repo + force-revert atomic merge (8.3-4)](#disable-cross-repo) |
| Adaptive proposal applied a bad policy | [Revert the proposal (slice 7.2)](#revert-an-applied-policy-proposal) |
| Operator boot loop / DB integrity check fails | [Restore DB from MinIO backup](MILLS_RUNBOOK.md#recover-from-a-corrupted-db) |

## Layer 1 — feature flag disable

All v2 features are gated behind a single `enabled: bool` field in `platform/gitops/k3s/mills/configmap-policy.yaml`. The operator reads policy on every reconcile tick, so a `false` lands on the next tick (≤60s) without restart.

### Disable squads

```bash
# In platform/gitops:
git checkout -b ops/disable-squads
yq -i '.policy.squads.enabled = false' k3s/mills/configmap-policy.yaml
git commit -am "ops(mills): disable squads (incident YYYY-MM-DD)"
git push
flux reconcile kustomization apps -n flux-system
# Confirm hot-reload:
kubectl logs -n loom-mills deploy/loom-mills-operator --tail=20 | grep "policy reloaded"
```

After disable, in-flight squad-routed runs continue to terminal state — disable affects routing decisions for *new* backlog items only. To kill in-flight runs, force-escalate them per [MILLS_RUNBOOK.md → Force-escalate a stuck pipeline run](MILLS_RUNBOOK.md#force-escalate-a-stuck-pipeline-run).

### Disable audit

Two modes — the lighter one keeps audits running but takes them out of the merge gate:

```bash
# Lighter: flip to advisory_only (audits still emit findings but never block):
yq -i '.policy.audit.advisory_only = true' k3s/mills/configmap-policy.yaml

# Heavier: stop running audits at all:
yq -i '.policy.audit.enabled = false' k3s/mills/configmap-policy.yaml
```

Prefer `advisory_only = true` first — you keep the visibility (HUD's Audit panel still populates) without the merge-blocking blast radius. Flip `enabled = false` only if the audit pool itself is the problem (e.g. wedged dispatcher, runaway cost).

### Disable debate

Debate is per-trigger (cron / roadmap / incident). Disable the offending trigger only:

```bash
# Disable debate on the cron-fired Sunday council:
yq -i '.policy.council.debate.enabled.cron = false' k3s/mills/configmap-policy.yaml

# Or fully:
yq -i '.policy.council.debate.enabled.cron = false' k3s/mills/configmap-policy.yaml
yq -i '.policy.council.debate.enabled.roadmap = false' k3s/mills/configmap-policy.yaml
yq -i '.policy.council.debate.enabled.incident = false' k3s/mills/configmap-policy.yaml
```

In-flight debates run to budget; the next council will use the single-pass (non-debate) ensemble.

### Disable cross-repo

Cross-repo is the riskiest v2 feature because it can leave half-merged state across two MRs. Disable + abort:

```bash
# 1. Stop accepting new cross-repo runs:
yq -i '.policy.cross_repo.enabled = false' k3s/mills/configmap-policy.yaml
git commit -am "ops(mills): disable cross_repo (incident YYYY-MM-DD)"
git push && flux reconcile kustomization apps -n flux-system

# 2. Find in-flight cross-repo runs:
curl -sf "$LOOM_MILLS_OPERATOR_URL/api/mills/cross-repo/runs?state=running" | jq

# 3. Abort each one (admin token required):
curl -X POST -H "Authorization: Bearer $LOOM_MILLS_ADMIN_TOKEN" \
  "$LOOM_MILLS_OPERATOR_URL/api/mills/cross-repo/runs/<id>/abort"

# 4. The integrator's revert path runs automatically when abort is called
#    on a run that already merged one side; verify:
curl -sf "$LOOM_MILLS_OPERATOR_URL/api/mills/cross-repo/runs/<id>" | jq '.state'
# expect: "reverted" within ~60s, or "revert_failed" requiring manual cleanup.
```

If `revert_failed`: the per-MR rollback plan is in `MILLS_RUNBOOK.md → Force-revert a cross-repo merge`. Each side is a regular MR — `git revert` + open a follow-up MR to drop the change cleanly.

### Disable adaptive proposal job

Adaptive runs once per week (Sunday 05:00 UTC). To prevent the next firing:

```bash
yq -i '.policy.adaptive_policy.enabled = false' k3s/mills/configmap-policy.yaml
```

The Sunday scheduler observes `policy.adaptive_policy.enabled == false` on its next tick and short-circuits without writing rows. Existing `policy_proposals` rows are unaffected (revert them via Layer 2).

## Layer 2 — revert an applied policy proposal

Adaptive proposals get a 24-hour `revert_deadline` stamped on `apply` (slice 7.2). The actual revert is a manual ConfigMap edit because the operator deliberately does NOT auto-write to the gitops repo — that's a safety boundary so a runaway operator can't ship arbitrary policy changes through GitOps.

> **Note**: The DAO exposes `Revert(id)` (`pkg/mills/store/dao_policy_proposal.go`) but no REST endpoint is wired in v2.0. A future slice may surface it on the HUD card; for now, the manual flow below is canonical.

### Revert procedure (works within and outside the 24h window)

```bash
# 1. Inspect the original diff in the proposal row:
ID=42
curl -sf "$LOOM_MILLS_OPERATOR_URL/api/mills/policy/proposals/$ID" | jq -r .Diff
# example output: "policy.budgets.pipeline.max_usd_per_run: 5.0 → 6.0"

# 2. Reverse the diff in the gitops ConfigMap directly:
git checkout -b ops/revert-policy-proposal-$ID
yq -i '.policy.budgets.pipeline.max_usd_per_run = 5.0' \
   platform/gitops/k3s/mills/configmap-policy.yaml
git commit -am "ops(mills): revert policy proposal $ID"
git push && flux reconcile kustomization apps -n flux-system

# 3. Mark the proposal rejected so adaptive doesn't keep proposing the
#    same delta on next Sunday (slice 7.2 endpoint, admin-token):
curl -X POST -H "Authorization: Bearer $LOOM_MILLS_ADMIN_TOKEN" \
  "$LOOM_MILLS_OPERATOR_URL/api/mills/policy/proposals/$ID/reject"
```

The operator hot-reloads the live policy on the next reconcile tick (≤60s); behaviour reverts immediately. The proposal row's `state` stays `applied_human` (or whatever it was) — that's by design so post-incident review can see "this proposal was applied then unwound."

## Layer 3 — DB restore

Last resort. See [MILLS_RUNBOOK.md → Recover from a corrupted DB](MILLS_RUNBOOK.md#recover-from-a-corrupted-db). The procedure scales operator to 0, restores from the nightly MinIO snapshot, and brings the operator back up. Items committed since the last snapshot are lost in a restore — `roadmap_intents` and `.loom/backlog/*.yaml` self-heal because they're regenerated from the canonical store, but in-flight pipeline runs whose attempts started since the snapshot have to be re-driven by hand.

## Verifying a rollback landed

Every rollback path has the same verification surface:

```bash
# 1. Operator hot-reloaded the new policy:
kubectl logs -n loom-mills deploy/loom-mills-operator --tail=50 | grep -i "policy reloaded"

# 2. Live policy matches the gitops repo:
curl -sf "$LOOM_MILLS_OPERATOR_URL/api/mills/policy" | jq '.squads.enabled'
# expect: false

# 3. Behaviour confirms — for a squads disable, the next reconcile tick
#    routes a backlog item via the fallback path and SquadOutcome rows
#    stop landing:
curl -sf "$LOOM_MILLS_OPERATOR_URL/api/mills/squads" | jq '.[].LastOutcomeAt' | sort -u
```

## Decision tree summary

```
v2 feature misbehaving
├── Audit / squads / debate / adaptive — disable via ConfigMap (≤60s)
├── Cross-repo — disable + abort in-flight + verify revert
├── Specific bad proposal applied — revert via REST (within 24h)
└── DB / canonical-state corruption — restore from MinIO (operator down ~5min)
```

## Sources

- [docs/MILLS.md](MILLS.md) — v1 architecture + v2 forward references
- [docs/MILLS_RUNBOOK.md](MILLS_RUNBOOK.md) — operator runbook (DB restore, force-escalate, audit a merged change)
- `.loom/93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md` §"Failure modes"
- `.loom/94-implementation-plan-mills-v2-hierarchical-swarm-2026-05-02.md` Phase 8 slices 8.1–8.3
- `pkg/mills/policy.go` — `enabled` field on every v2 policy struct
- `pkg/mills/store/dao_policy_proposal.go` — `Apply` / `Reject` / `Revert` DAO methods
- `cmd/loom-mills-operator/handlers_policy_proposals.go` — REST endpoints (Phase 7 slice 7.2)
