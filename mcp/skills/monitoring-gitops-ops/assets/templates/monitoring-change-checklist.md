# Monitoring Change Checklist

- [ ] Scoped change to `k3s/monitoring/**` (or `k3s/flux/monitoring/**` as needed)
- [ ] Captured `/tmp/monitoring-before.md`
- [ ] Verified alert rule metadata quality (`summary`, `description`, `runbook_url`)
- [ ] Committed and pushed Git change
- [ ] Reconciled `monitoring` kustomization
- [ ] Captured `/tmp/monitoring-after.md`
- [ ] Checked active/firing alerts for expected behavior
- [ ] Recorded impact and residual risk in agent context
