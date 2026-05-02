# Hive Ops — Reference

Backing references for `hive-ops`. Skill body lives in `mcp/context/skills-registry.yaml` (entry: `hive-ops`).

## Primary docs

- Architecture and policy reference: `docs/HIVE.md`
- Day-2 procedures (pause/resume, escalate, replay, recover, rollout): `docs/HIVE_RUNBOOK.md`
- Forward direction (Hive v2): `.loom/92-research-hive-v2-hierarchical-swarm-2026-05-02.md`, `.loom/93-product-spec-hive-v2-hierarchical-swarm-2026-05-02.md`, `.loom/94-implementation-plan-hive-v2-hierarchical-swarm-2026-05-02.md`

## Source files

- Operator binary: `cmd/loom-hive-operator/`
- Core packages: `pkg/hive/{store,council,pipeline,eval,gates,clients,policy*,reconciler,scheduler,budget,metrics}.go`
- HUD panels: `internal/hud/frontend/src/lib/components/Hive/{CouncilPanel,PipelinesPanel,BacklogPanel,EvalPanel}.svelte`
- Mac CLI: `cmd/loom/cmd_hive*.go`
- MentatLab DAG: `cmd/mcp-mentatlab/templates/hive-default-pipeline.yaml`
- GitOps manifests: `platform/gitops/k3s/hive/` (separate repo)

## Common commands

```bash
loom hive status
loom hive council dryrun
loom hive council run
loom hive backlog list --state=queued
loom hive eval list --subject=council
loom hive pipelines list --state=running

# Read-only cluster snapshot
bash mcp/skills/hive-ops/scripts/hive_status_snapshot.sh > /tmp/hive-status.md
```

## Kill switch

`policy.enabled: false` in `platform/gitops/k3s/hive/configmap-policy.yaml`. Reconcile with `flux reconcile kustomization apps -n flux-system --with-source`. Operator hot-reloads within seconds; in-flight runs continue under their captured policy. See `docs/HIVE_RUNBOOK.md` §"Pause and resume".

## Recovery from corrupted DB

Restore from MinIO `loom-hive-backups/` (nightly). See `docs/HIVE_RUNBOOK.md` §"Recover from a corrupted DB" for the full procedure.

## Eval framework

- Loop A — synchronous artifact judge inline at council run end (`pkg/hive/eval/judge.go`).
- Loop B — per-merge outcome attribution, async (`pkg/hive/eval/outcome_attributor.go`, `pkg/hive/eval/council_roi.go`).
- Loop C — weekly cross-run consistency, scheduled Sunday 0600 UTC (`pkg/hive/eval/cross_run.go`, `pkg/hive/eval/cross_run_scheduler.go`).

## Telemetry

- Metrics namespace: `loom_hive_*` (see `pkg/hive/metrics.go`).
- Grafana dashboard: `platform/gitops/monitoring/dashboards/hive.json`.

## Rollout staging

Local → dev cluster (kc-k3s) → production. One-week soak between flips. Watch `loom_hive_regression_count_total` after each flip; pause via `enabled: false` if non-zero in the first 24h.

V2 staged flips (when v2 ships): squads → audit (advisory) → debate (incident only) → cross-repo (after dogfood) → adaptive policy (manual-apply). Detail in `.loom/94-…2026-05-02.md` Phase 8.
