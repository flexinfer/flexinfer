#!/usr/bin/env bash
set -euo pipefail

# Verify platform/gitops dev-env.sh is sourced (KUBECONFIG points at the repo's .kube).

ROOT="${1:-.}"
cd "$ROOT"

if [[ ! -f platform/gitops/dev-env.sh ]]; then
  echo "ERROR: expected platform/gitops/dev-env.sh under: $ROOT" >&2
  exit 1
fi

expected="$PWD/platform/gitops/.kube/k3s.yaml"
current="${KUBECONFIG:-}"

if [[ "$current" != "$expected" ]]; then
  echo "Dev env not configured (or different context):" >&2
  echo "- expected KUBECONFIG: $expected" >&2
  echo "- current  KUBECONFIG: ${current:-<unset>}" >&2
  echo "" >&2
  echo "Run: source platform/gitops/dev-env.sh" >&2
  exit 2
fi

echo "OK: dev env configured (KUBECONFIG=$current)"
<<<<<<< Updated upstream

=======
>>>>>>> Stashed changes
