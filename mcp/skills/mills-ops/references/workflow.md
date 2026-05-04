# Mills Ops — Reference

Backing references for `mills-ops`. Skill body lives in `mcp/context/skills-registry.yaml` (entry: `mills-ops`).

## Primary docs

- Architecture and policy reference: `docs/MILLS.md`
- Day-2 procedures (pause/resume, escalate, replay, recover, rollout): `docs/MILLS_RUNBOOK.md`
- Forward direction (Mills v2): `.loom/92-research-mills-v2-hierarchical-swarm-2026-05-02.md`, `.loom/93-product-spec-mills-v2-hierarchical-swarm-2026-05-02.md`, `.loom/94-implementation-plan-mills-v2-hierarchical-swarm-2026-05-02.md`

## Source files

- Operator binary: `cmd/loom-mills-operator/`
- Core packages: `pkg/mills/{store,council,pipeline,eval,gates,clients,policy*,reconciler,scheduler,budget,metrics}.go`
- HUD panels: `internal/hud/frontend/src/lib/components/Mills/{CouncilPanel,PipelinesPanel,BacklogPanel,EvalPanel}.svelte`
- Mac CLI: `cmd/loom/cmd_mills*.go`
- MentatLab DAG: `cmd/mcp-mentatlab/templates/mills-default-pipeline.yaml`
- GitOps manifests: `platform/gitops/k3s/mills/` (separate repo)

## Common commands

```bash
loom mills status
loom mills council dryrun
loom mills council run
loom mills backlog list --state=queued
loom mills eval list --subject=council
loom mills pipelines list --state=running

# Read-only cluster snapshot
bash mcp/skills/mills-ops/scripts/mills_status_snapshot.sh > /tmp/mills-status.md
```

## Kill switch

`policy.enabled: false` in `platform/gitops/k3s/mills/configmap-policy.yaml`. Reconcile with `flux reconcile kustomization apps -n flux-system --with-source`. Operator hot-reloads within seconds; in-flight runs continue under their captured policy. See `docs/MILLS_RUNBOOK.md` §"Pause and resume".

## Recovery from corrupted DB

Restore from MinIO `loom-mills-backups/` (nightly). See `docs/MILLS_RUNBOOK.md` §"Recover from a corrupted DB" for the full procedure.

## Eval framework

- Loop A — synchronous artifact judge inline at council run end (`pkg/mills/eval/judge.go`).
- Loop B — per-merge outcome attribution, async (`pkg/mills/eval/outcome_attributor.go`, `pkg/mills/eval/council_roi.go`).
- Loop C — weekly cross-run consistency, scheduled Sunday 0600 UTC (`pkg/mills/eval/cross_run.go`, `pkg/mills/eval/cross_run_scheduler.go`).

## Telemetry

- Metrics namespace: `loom_mills_*` (see `pkg/mills/metrics.go`).
- Grafana dashboard: `platform/gitops/monitoring/dashboards/mills.json`.

## Rollout staging

Local → dev cluster (kc-k3s) → production. One-week soak between flips. Watch `loom_mills_regression_count_total` after each flip; pause via `enabled: false` if non-zero in the first 24h.

V2 staged flips (when v2 ships): squads → audit (advisory) → debate (incident only) → cross-repo (after dogfood) → adaptive policy (manual-apply). Detail in `.loom/94-…2026-05-02.md` Phase 8.
