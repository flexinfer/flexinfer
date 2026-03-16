# K3s + Harvester Maintenance Workflow

## Scope in This Repo

- App/ops manifests: `k3s/**`
- Harvester VM and infra manifests: `harvester/**`
- Ops maintenance manifests: `k3s/ops/maintenance/**`
- VM + cluster recovery runbooks:
  - `docs/runbooks/widespread-vm-cluster-recovery.md`
  - `docs/runbooks/qdrant-vm-recovery.md`

## Routine Workflow

1. Baseline:
   - `bash ${SKILL_PATH}/scripts/cluster_maintenance_snapshot.sh > /tmp/maintenance-before.md`
2. Edit manifests in Git (`k3s/**` or `harvester/**`).
3. Commit and push.
4. Reconcile impacted kustomizations (usually `apps`, optionally `monitoring`/`system`).
5. Validate node, VM, and workload health.
6. Capture after state and persist notes in agent context.

## Important Repo Convention

If a Harvester VM is stuck in `Running` with unhealthy guest behavior, prefer deleting the VMI so the controller recreates it:

- `kubectl --kubeconfig .kube/harvester-admin.yaml -n default delete vmi <vm-name>`

Avoid relying on `virtctl restart` when it is flaky in this environment.

## External References (Research-Backed)

- Flux Kustomization suspend/resume and reconciliation behavior:
  - https://fluxcd.io/flux/components/kustomize/kustomizations/
- Flux reconcile command behavior:
  - https://fluxcd.io/flux/cmd/flux_reconcile_kustomization/
