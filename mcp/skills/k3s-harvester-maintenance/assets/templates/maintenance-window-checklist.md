# Maintenance Window Checklist

- [ ] Defined maintenance scope (node, VM, storage, monitoring, or policy)
- [ ] Captured `/tmp/maintenance-before.md`
- [ ] Applied only Git-backed manifest changes
- [ ] Committed and pushed change set
- [ ] Reconciled affected Flux kustomizations
- [ ] Captured `/tmp/maintenance-after.md`
- [ ] Verified node readiness, VM health, and workload stability
- [ ] Removed temporary mitigation manifests if no longer needed
- [ ] Logged outcomes and follow-ups in agent context
