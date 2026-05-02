# Loom Hive — Operator Runbook

Day-2 procedures for `loom-hive-operator`. For architecture and policy reference, see `docs/HIVE.md`.

All `kubectl` commands assume `KUBECONFIG=~/workspace/platform/gitops/.kube/k3s.yaml` (or `kc-k3s` alias). All Mac CLI examples assume `LOOM_HIVE_OPERATOR_URL` and `LOOM_ADMIN_TOKEN` are exported. The operator runs in namespace `loom-hive`.

## Quick status

```bash
loom hive status                                           # one-liner from Mac
kubectl get deploy,po,pvc -n loom-hive                     # cluster snapshot
kubectl logs -n loom-hive deploy/loom-hive-operator --tail=200
curl -sf $LOOM_HIVE_OPERATOR_URL/readyz                    # 200 once initialized
curl -sf $LOOM_HIVE_OPERATOR_URL/metrics | grep '^loom_hive_'
```

## Pause and resume

The kill switch is `policy.enabled: false`. Edits to the mounted ConfigMap propagate via fsnotify within seconds; in-flight runs continue under their captured policy, but no new runs start.

### Pause

```bash
# Edit platform/gitops/k3s/hive/configmap-policy.yaml in the gitops repo:
#   enabled: false
# Then reconcile:
flux reconcile kustomization apps -n flux-system --with-source

# Wait for hot-reload signal in operator logs:
kubectl logs -n loom-hive deploy/loom-hive-operator --since=2m | grep "policy reloaded"

# Confirm the reconciler is parked:
loom hive status                # expected: enabled=false, queue_depth=0 (no new picks)
```

The reconciler exits cleanly within one tick (≤60s). The HTTP and metrics listeners stay up so HUD reads continue working.

### Resume

Reverse the edit (`enabled: true` or remove the field), reconcile, watch for the next `policy reloaded` log.

### Emergency pause without GitOps

Only when GitOps is unavailable (e.g., Flux is down). Patches the mounted ConfigMap directly; **must** be reverted by a GitOps commit afterwards or Flux will fight you.

```bash
kubectl patch configmap -n loom-hive loom-hive-policy \
  --type merge -p '{"data":{"policy.yaml":"<full yaml with enabled: false>"}}'
# Restore via git/Flux as soon as the issue is resolved.
```

## Force-escalate a stuck pipeline run

A run can wedge if a stage worker hangs (e.g., `ci_watch` waiting on a CI job that's been canceled). Force-escalation transitions the run to `escalated`, records a failure record in `events`, and (if configured) opens a GitLab issue + agent-context handoff.

```bash
# Find the run
loom hive pipelines list --state=running

# Escalate
curl -sf -X POST -H "Authorization: Bearer $LOOM_ADMIN_TOKEN" \
  $LOOM_HIVE_OPERATOR_URL/api/hive/pipeline/runs/<run_id>/escalate

# Confirm
loom hive pipelines list --state=escalated
```

The reconciler will not auto-retry escalated items; a human must edit the linked YAML or close the GitLab issue with a deliberate `human-resolved` label to unblock.

## Replay a council run

Useful when you want to verify a fix to the brief assembler, swap an ensemble member, or compare A/B outputs.

### Dry-run (safe)

Runs the full council pipeline against a scratch DB; nothing is committed and no GitLab mutations happen. The sidecar + plan paths are returned for inspection.

```bash
loom hive council dryrun
# Output: sidecar JSON path, .loom/<NN>-… draft paths, total cost
```

### Real replay with a different ensemble

```bash
# 1. Edit policy.council.ensemble in platform/gitops/k3s/hive/configmap-policy.yaml
#    e.g., swap editor.model from claude-opus to codex-gpt5.
# 2. flux reconcile.
# 3. Wait for "policy reloaded" log line.
# 4. Trigger:
loom hive council run

# 5. Inspect the new run:
loom hive eval list --subject=council
# HUD: Hive → Eval panel; sort by created_at desc.
```

When the run lands, compare cost + Loop A score + downstream Loop B attribution against the previous run with the same brief content (same `roadmap_intents` snapshot).

V2.1 will add first-class A/B replay UI; until then, the manual flow above is the supported path.

## Audit a merged change

Find which council brief produced the slice that produced the merge:

```bash
# Get the pipeline run by MR iid:
curl -sf -H "Authorization: Bearer $LOOM_ADMIN_TOKEN" \
  "$LOOM_HIVE_OPERATOR_URL/api/hive/pipeline/runs?mr_iid=<iid>"

# Eval Loop B attribution shows the originating council run:
curl -sf "$LOOM_HIVE_OPERATOR_URL/api/hive/eval?subject_kind=pipeline_run&subject_id=<run_id>"

# Loop A score for the council run that planned it:
curl -sf "$LOOM_HIVE_OPERATOR_URL/api/hive/eval?subject_kind=council_run&subject_id=<council_run_id>"
```

For deeper inspection, the `events` table has the full per-stage trace (slog rows are also written via JSON to stderr; aggregated in Loki).

## Recover from a corrupted DB

The canonical SQLite DB lives on a Longhorn RWO PVC. WAL replay handles most operator restarts; nothing should be needed for a clean kill. The procedures below are for the rare cases where the DB is unrecoverable.

### Symptom: `database is locked` or schema check fails on boot

1. Capture diagnostics:

   ```bash
   kubectl logs -n loom-hive deploy/loom-hive-operator --previous --tail=500 > /tmp/hive-prev-logs.txt
   kubectl exec -n loom-hive deploy/loom-hive-operator -- sqlite3 /var/lib/loom-hive/state.db "PRAGMA integrity_check;"
   ```

2. If `integrity_check` returns anything other than `ok`, restore from the most recent nightly backup in MinIO:

   ```bash
   # List backups (nightly CronJob writes one per day):
   mc ls minio/loom-hive-backups/

   # Pick a backup to restore:
   BACKUP=2026-04-30T06-00-00.db

   # Scale operator to 0 to release the PVC:
   kubectl scale deploy -n loom-hive loom-hive-operator --replicas=0

   # Restore via a one-shot Job (the manifest lives at
   # platform/gitops/k3s/hive/jobs/restore-from-backup.yaml; render with the
   # chosen filename):
   kubectl create -n loom-hive -f - <<EOF
   apiVersion: batch/v1
   kind: Job
   metadata:
     generateName: hive-restore-
   spec:
     template:
       spec:
         restartPolicy: Never
         containers:
           - name: restore
             image: registry.harbor.lan/library/loom-hive-operator:stable
             command: ["/bin/sh", "-euxc"]
             args:
               - |
                 mc cp minio/loom-hive-backups/$BACKUP /var/lib/loom-hive/state.db
                 sqlite3 /var/lib/loom-hive/state.db 'PRAGMA integrity_check;'
             volumeMounts:
               - { name: state, mountPath: /var/lib/loom-hive }
         volumes:
           - name: state
             persistentVolumeClaim:
               claimName: hive-state
   EOF

   # Watch the restore Job to completion:
   kubectl logs -n loom-hive job/<job-name> -f

   # Bring the operator back up:
   kubectl scale deploy -n loom-hive loom-hive-operator --replicas=1
   kubectl rollout status deploy -n loom-hive loom-hive-operator
   ```

3. After restore, run a council dryrun to confirm the brief assembler still works against the restored canonical state. Then resume normal operation.

### Loss-window note

The nightly CronJob captures one snapshot per day. Items committed since the last snapshot are lost in a restore. Re-run the council to reproduce — `roadmap_intents` is idempotent and `.loom/backlog/*.yaml` is regenerated from the canonical store, so most state self-heals.

## Inspect what the operator is doing right now

```bash
# Active runs (council + pipeline):
curl -sf "$LOOM_HIVE_OPERATOR_URL/api/hive/status" | jq

# What gates fired in the last hour:
curl -sf "$LOOM_HIVE_OPERATOR_URL/api/hive/pipeline/runs?since=1h" | \
  jq '[.runs[].gate_outcomes[]] | group_by(.gate) | map({gate: .[0].gate, count: length})'

# What the budget enforcer thinks:
curl -sf "$LOOM_HIVE_OPERATOR_URL/metrics" | grep '^loom_hive_budget'
```

## Production rollout staging

The default-on flip (slice 6.6) was landed for the in-binary policy default. Each cluster overlay still owns whether the hive is enabled in *its* ConfigMap. Stage rollouts in this order; never flip more than one environment per week.

### Stage 1 — Local

A developer can run the operator against a local SQLite file and a local policy YAML for the smallest possible smoke test:

```bash
make build/loom-hive-operator
mkdir -p /tmp/loom-hive
./bin/loom-hive-operator \
  --db-path     /tmp/loom-hive/state.db \
  --policy-path testdata/policy.yaml \
  --listen      :8090 \
  --metrics-addr :9090

curl -sf localhost:9090/healthz                            # 200
curl -sf -H "Authorization: Bearer $LOOM_ADMIN_TOKEN" \
  localhost:8090/api/hive/status | jq
```

Local runs use the FakeReviewer/FakeEditor (`cmd/loom-hive-operator/main.go: buildCouncilRunner`) when no FlexInfer or HUD spawn is configured; this is sufficient for handler smoke tests but does not exercise real model calls.

### Stage 2 — Dev cluster (kc-k3s)

```bash
# In the platform/gitops repo on a dev branch:
# Edit platform/gitops/k3s/hive/configmap-policy.yaml: enabled: true
# Commit, push, open MR, merge.

flux reconcile kustomization apps -n flux-system --with-source
kubectl rollout status deploy -n loom-hive loom-hive-operator
```

Soak for one week. Verify each acceptance criterion against the deployed cluster:

| Criterion | Check |
|---|---|
| Council dryrun produces sidecar + 3 markdown docs in <8 min for <$5 with eval ≥0.7 | `loom hive council dryrun` and inspect cost/score in HUD |
| End-to-end backlog → merged MR via fixture run | `loom hive backlog list --state=merged` after seeding a fixture |
| Eval Loops A/B/C populated | `loom hive eval list` returns recent rows for all three subject kinds |
| Idle-throttle drops reconciler cadence | `loom hive status` shows `next_tick_in_s` ≥ 240 when queue is empty |
| HUD `Hive` view renders four panels | Visit `<HUD>/hive` |
| Backups produced nightly | `mc ls minio/loom-hive-backups/` shows last 7 entries |

### Stage 3 — Production cluster

Only after dev has been green for 7 consecutive days. Same edit + reconcile flow, but watch the regression KPI:

```bash
watch -n 30 'curl -sf $LOOM_HIVE_OPERATOR_URL/metrics | grep loom_hive_regression'
```

If `loom_hive_regression_count_total` increases above zero in the first 24h, pause and investigate before any further flips. The kill switch (`enabled: false`) is the safest and lowest-blast-radius rollback.

## v2 rollout staging (preview)

When `.loom/94-implementation-plan-hive-v2-hierarchical-swarm-2026-05-02.md` Phase 8 lands, each v2 feature flips behind its own policy flag with a 1-week soak between flips. Order:

1. `policy.squads.enabled: true`
2. `policy.audit.enabled: true` (advisory-only by default)
3. `policy.council.debate.enabled.incident: true`
4. `policy.cross_repo.enabled: true` — only after 3 successful loom-core+loom dogfood atomic merges
5. `policy.adaptive_policy.enabled: true` (manual-apply only)

Detail per slice in `.loom/94-…2026-05-02.md` Phase 8. The rollback playbook for v2 lives in `docs/HIVE_V2_ROLLBACK.md` (created with v2 phase 8).

## Useful loops

```bash
# Watch council runs as they complete:
watch -n 30 'loom hive eval list --subject=council | head -10'

# Watch escalation rate:
watch -n 60 'kubectl logs -n loom-hive deploy/loom-hive-operator --since=10m | jq -r "select(.msg==\"escalation\") | [.time, .item_id] | @tsv"'

# Diff the live policy vs. git:
kubectl get configmap -n loom-hive loom-hive-policy -o jsonpath='{.data.policy\.yaml}' | \
  diff - platform/gitops/k3s/hive/configmap-policy.yaml
```

## Sources

- `cmd/loom-hive-operator/main.go` (lifecycle, env vars, fakes vs. wired clients)
- `cmd/loom-hive-operator/auth.go` (admin token loading)
- `pkg/hive/policy.go` (`enabled`, `IsEnabled`, kill-switch semantics)
- `pkg/hive/policy_manager.go` (fsnotify hot-reload)
- `pkg/hive/reconciler.go` (one-tick exit on disable)
- `pkg/hive/scheduler.go` (cron + idle throttle)
- `pkg/hive/eval/{judge,outcome_attributor,council_roi,cross_run}.go` (Loops A/B/C)
- `pkg/hive/store/migrate.go` (migrations on boot; safe replay)
- `platform/gitops/k3s/hive/` (manifests; not in this repo — lives in the gitops repo)
- `docs/HIVE.md` (architecture + policy reference)
- `.loom/91-implementation-plan-agent-swarm-council-pipeline-2026-04-25.md` §"Default-off rollout"
