#!/usr/bin/env bash
set -euo pipefail

# Read-only snapshot for Longhorn health and scheduling signals.

NS="${1:-longhorn-system}"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found on PATH" >&2
  exit 1
fi

echo "# Longhorn Health Snapshot"
echo ""
echo "- Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "- Namespace: ${NS}"
echo ""
echo "## Flux Kustomizations"
echo '```'
kubectl -n flux-system get kustomizations || true
echo '```'
echo ""
echo "## Longhorn Pods"
echo '```'
kubectl -n "${NS}" get pods -o wide || true
echo '```'
echo ""
echo "## Volume Robustness Counts"
echo '```'
kubectl -n "${NS}" get volumes.longhorn.io -o custom-columns=NAME:.metadata.name,STATE:.status.state,ROBUSTNESS:.status.robustness,NODE:.spec.nodeID --no-headers | awk '{print $3}' | sort | uniq -c || true
echo '```'
echo ""
echo "## Volume Scheduler Error Annotations"
echo '```'
kubectl get pv -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.annotations.longhorn\.io/volume-scheduling-error}{"\n"}{end}' | rg -v '\t$' || true
echo '```'
echo ""
echo "## Recent Longhorn Events"
echo '```'
kubectl -n "${NS}" get events --sort-by=.lastTimestamp | tail -n 120 || true
echo '```'
echo ""
echo "## Recent Longhorn Manager Error Signals"
echo '```'
kubectl -n "${NS}" logs -l app=longhorn-manager --since=10m | rg 'Error running start replica command|Failed to schedule replica|no available disk|HardNodeAffinity|volume-scheduling-error' || true
echo '```'
