# Monitoring GitOps Ops Workflow

## Scope in This Repo

- Flux monitoring kustomization: `clusters/k3s/flux-system/kustomization-monitoring.yaml`
- Flux source path: `k3s/flux/monitoring/kustomization.yaml`
- Monitoring manifests: `k3s/monitoring/**`

## Standard Change Loop

1. Baseline:
   - `bash ${SKILL_PATH}/scripts/monitoring_health_snapshot.sh > /tmp/monitoring-before.md`
2. Edit target manifests in `k3s/monitoring/**`.
3. Optional metadata check:
   - `bash ${SKILL_PATH}/scripts/check_prometheus_rule_annotations.sh`
4. Commit/push.
5. Reconcile:
   - `flux reconcile kustomization monitoring -n flux-system --with-source`
6. Validate:
   - `bash ${SKILL_PATH}/scripts/monitoring_health_snapshot.sh > /tmp/monitoring-after.md`
   - check alerts and rules via MCP tools (`prometheus`, `grafana`, `alertmanager`) if available.

## Rule Quality Baseline

Prometheus alerting docs treat annotations as non-identifying metadata for responder context, including runbook links.

- Ensure each alert has:
  - `summary`
  - `description`
  - `runbook_url`

## External References (Research-Backed)

- Flux Kustomization behavior (`prune`, `suspend`, `wait`, `timeout`, `dependsOn`):
  - https://fluxcd.io/flux/components/kustomize/kustomizations/
- Flux reconcile command semantics:
  - https://fluxcd.io/flux/cmd/flux_reconcile_kustomization/
- Prometheus alerting rule annotations:
  - https://prometheus.io/docs/prometheus/latest/configuration/alerting_rules/
