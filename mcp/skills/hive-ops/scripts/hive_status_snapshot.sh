#!/usr/bin/env bash
# hive_status_snapshot.sh
#
# Read-only snapshot of loom-hive-operator state. Combines:
#   - cluster pod/PVC/ConfigMap status
#   - operator REST /api/hive/status
#   - last 50 council + pipeline runs
#   - latest eval scores per loop
#   - rolling KPIs (cost, gate pass rate, regression rate)
#
# Output is markdown to stdout. Safe to run from a Mac with kubeconfig +
# admin token configured. No mutations.
#
# Required env:
#   LOOM_HIVE_OPERATOR_URL   e.g. http://hive.loom.lan or cluster service URL
#   LOOM_ADMIN_TOKEN         admin bearer token
#   KUBECONFIG               path to k3s kubeconfig (defaults to k3s.yaml under
#                            the platform/gitops .kube dir if available)

set -euo pipefail

OPERATOR_URL="${LOOM_HIVE_OPERATOR_URL:-}"
TOKEN="${LOOM_ADMIN_TOKEN:-}"

if [[ -z "${OPERATOR_URL}" ]]; then
  echo "error: LOOM_HIVE_OPERATOR_URL is not set" >&2
  exit 2
fi
if [[ -z "${TOKEN}" ]]; then
  echo "error: LOOM_ADMIN_TOKEN is not set" >&2
  exit 2
fi

NS="${LOOM_HIVE_NS:-loom-hive}"

now() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }

curl_op() {
  curl -sf -H "Authorization: Bearer ${TOKEN}" "${OPERATOR_URL}$1"
}

printf '# Loom Hive Status Snapshot\n\n'
printf '_Captured: %s_\n\n' "$(now)"

printf '## Cluster\n\n'
if command -v kubectl >/dev/null 2>&1; then
  printf '```\n'
  kubectl get deploy,pvc,cm,svc -n "${NS}" 2>&1 || true
  printf '```\n\n'
  printf '### Recent operator log (last 30 lines)\n\n'
  printf '```\n'
  kubectl logs -n "${NS}" deploy/loom-hive-operator --tail=30 2>&1 || true
  printf '```\n\n'
else
  printf '_kubectl not found; skipping cluster section_\n\n'
fi

printf '## Operator status\n\n'
printf '```json\n'
curl_op /api/hive/status | (command -v jq >/dev/null && jq . || cat) || \
  printf '%s\n' "(unreachable)"
printf '\n```\n\n'

printf '## Recent council runs (50)\n\n'
printf '```json\n'
curl_op '/api/hive/council/runs?limit=50' | (command -v jq >/dev/null && jq . || cat) || \
  printf '%s\n' "(unreachable)"
printf '\n```\n\n'

printf '## Recent pipeline runs (50)\n\n'
printf '```json\n'
curl_op '/api/hive/pipeline/runs?limit=50' | (command -v jq >/dev/null && jq . || cat) || \
  printf '%s\n' "(unreachable)"
printf '\n```\n\n'

printf '## Eval scores (last 20 per loop)\n\n'
for subj in council_run pipeline_run cross_run; do
  printf '### %s\n\n' "${subj}"
  printf '```json\n'
  curl_op "/api/hive/eval?subject_kind=${subj}&limit=20" | \
    (command -v jq >/dev/null && jq . || cat) || \
    printf '%s\n' "(unreachable)"
  printf '\n```\n\n'
done

printf '## Headline KPIs\n\n'
printf '```\n'
curl -sf "${OPERATOR_URL}/metrics" 2>/dev/null | grep -E '^loom_hive_(pipeline_cost|merge_to_main|gate_decisions|regression|budget)' || \
  printf '%s\n' "(metrics unreachable)"
printf '\n```\n'
