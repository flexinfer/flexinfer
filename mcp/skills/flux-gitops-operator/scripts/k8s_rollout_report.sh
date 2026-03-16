#!/usr/bin/env bash
set -euo pipefail

# Produce a quick rollout snapshot for a namespace as Markdown on stdout.

NS="${1:-}"
if [[ -z "$NS" ]]; then
  echo "Usage: k8s_rollout_report.sh <namespace>" >&2
  exit 1
fi

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found on PATH" >&2
  exit 1
fi

echo "# Rollout Report: ${NS}"
echo ""
echo "## Workloads"
echo ""
echo "### Deployments"
echo '```'
kubectl -n "$NS" get deploy -o wide || true
echo '```'
echo ""
echo "### StatefulSets"
echo '```'
kubectl -n "$NS" get sts -o wide || true
echo '```'
echo ""
echo "### Pods (top)"
echo '```'
kubectl -n "$NS" get pods -o wide | head -n 80 || true
echo '```'
echo ""
echo "## Events (last 80)"
echo '```'
kubectl -n "$NS" get events --sort-by=.lastTimestamp | tail -n 80 || true
echo '```'
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
