# Longhorn GitOps Ops Workflow

## Scope in This Repo

- Longhorn manifests: `k3s/longhorn/**`
- Flux apps entrypoint: `k3s/flux/apps/kustomization.yaml`
- Related runbooks:
  - `docs/runbooks/longhorn-replica-directory-missing.md`
  - `docs/runbooks/widespread-vm-cluster-recovery.md`

## Repo-Derived Guardrails

- Apply durable changes in Git under `k3s/longhorn/**`, then reconcile Flux.
- Avoid long-lived live edits; if incident mitigation is done live, mirror into Git immediately.
- Prefer explicit storage policy via `storageclass-*.yaml`, `settings*.yaml`, and `node-*.yaml` manifests.

## Standard Change Loop

1. Baseline:
   - `bash ${SKILL_PATH}/scripts/longhorn_health_snapshot.sh > /tmp/longhorn-before.md`
2. Edit manifests in `k3s/longhorn/**`.
3. Commit and push.
4. Reconcile:
   - `flux reconcile kustomization apps -n flux-system --with-source`
5. Validate:
   - `bash ${SKILL_PATH}/scripts/longhorn_health_snapshot.sh > /tmp/longhorn-after.md`
6. Persist:
   - write root cause/change summary in agent context.

## External References (Research-Backed)

- Longhorn best practices (dedicated disks, recurring backups/snapshots, scheduling tradeoffs):
  - https://longhorn.io/docs/latest/best-practices/
- Longhorn volume creation details and scheduler error annotation (`longhorn.io/volume-scheduling-error`):
  - https://longhorn.io/docs/latest/nodes-and-volumes/volumes/create-volumes/
- Longhorn recurring and rebuild operational behavior:
  - https://longhorn.io/docs/latest/advanced-resources/rebuilding/
