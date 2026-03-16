#!/usr/bin/env bash
set -euo pipefail

# Read-only k3s + Harvester maintenance snapshot.

HARV_KUBECONFIG="${HARV_KUBECONFIG:-.kube/harvester-admin.yaml}"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found on PATH" >&2
  exit 1
fi

echo "# K3s + Harvester Maintenance Snapshot"
echo ""
echo "- Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "- Harvester kubeconfig: ${HARV_KUBECONFIG}"
echo ""
echo "## K3s Nodes"
echo '```'
kubectl get nodes -o wide || true
echo '```'
echo ""
echo "## K3s Node Pressure Conditions"
echo '```'
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{range .status.conditions[*]}{.type}{"="}{.status}{";"}{end}{"\n"}{end}' | rg 'DiskPressure|MemoryPressure|PIDPressure|Ready' || true
echo '```'
echo ""
echo "## Flux Kustomizations"
echo '```'
kubectl -n flux-system get kustomizations || true
echo '```'
echo ""
echo "## Ops Maintenance Workloads"
echo '```'
kubectl -n ops get cronjobs,jobs,pods || true
echo '```'
echo ""
echo "## Harvester VMs"
echo '```'
kubectl --kubeconfig "${HARV_KUBECONFIG}" -n default get vm -o wide || true
echo '```'
echo ""
echo "## Harvester VMIs"
echo '```'
kubectl --kubeconfig "${HARV_KUBECONFIG}" -n default get vmi -o wide || true
echo '```'
