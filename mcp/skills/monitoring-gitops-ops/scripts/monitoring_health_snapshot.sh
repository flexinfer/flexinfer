#!/usr/bin/env bash
set -euo pipefail

# Read-only snapshot for monitoring stack health.

NS="${1:-monitoring}"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "ERROR: kubectl not found on PATH" >&2
  exit 1
fi

echo "# Monitoring Health Snapshot"
echo ""
echo "- Timestamp: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "- Namespace: ${NS}"
echo ""
echo "## Flux Kustomization: monitoring"
echo '```'
kubectl -n flux-system get kustomizations monitoring -o wide || true
echo '```'
echo ""
echo "## Monitoring Pods"
echo '```'
kubectl -n "${NS}" get pods -o wide || true
echo '```'
echo ""
echo "## HelmReleases"
echo '```'
kubectl -n "${NS}" get helmreleases.helm.toolkit.fluxcd.io -o wide || true
echo '```'
echo ""
echo "## Prometheus Rules"
echo '```'
kubectl -n "${NS}" get prometheusrules.monitoring.coreos.com || true
echo '```'
echo ""
echo "## ServiceMonitors and PodMonitors"
echo '```'
kubectl -n "${NS}" get servicemonitors.monitoring.coreos.com,podmonitors.monitoring.coreos.com || true
echo '```'
echo ""
echo "## Recent Events"
echo '```'
kubectl -n "${NS}" get events --sort-by=.lastTimestamp | tail -n 120 || true
echo '```'
