#!/usr/bin/env bash
set -euo pipefail

# Heuristic guardrail for Prometheus rule metadata quality.
# Verifies each rules file has at least as many summary/description/runbook_url lines as alerts.

ROOT="${1:-k3s/monitoring}"

has_rg=0
if command -v rg >/dev/null 2>&1; then
  has_rg=1
fi

if (( has_rg == 1 )); then
  mapfile -t files < <(rg --files "${ROOT}" | rg 'prometheus-rules-.*\.ya?ml$' | sort || true)
else
  mapfile -t files < <(find "${ROOT}" -type f \( -name 'prometheus-rules-*.yaml' -o -name 'prometheus-rules-*.yml' \) | sort || true)
fi

if [[ ${#files[@]} -eq 0 ]]; then
  echo "No prometheus-rules-*.yaml files found under ${ROOT}" >&2
  exit 2
fi

fail=0

echo "Checking Prometheus rule annotation coverage in ${#files[@]} files..."
for f in "${files[@]}"; do
  if (( has_rg == 1 )); then
    alerts=$(rg -c '^\s*-\s*alert:\s*' "$f" || true)
    summary=$(rg -c '^\s+summary:\s*' "$f" || true)
    description=$(rg -c '^\s+description:\s*' "$f" || true)
    runbook=$(rg -c '^\s+runbook_url:\s*' "$f" || true)
  else
    alerts=$(grep -Ec '^[[:space:]]*-[[:space:]]*alert:[[:space:]]*' "$f" || true)
    summary=$(grep -Ec '^[[:space:]]+summary:[[:space:]]*' "$f" || true)
    description=$(grep -Ec '^[[:space:]]+description:[[:space:]]*' "$f" || true)
    runbook=$(grep -Ec '^[[:space:]]+runbook_url:[[:space:]]*' "$f" || true)
  fi

  printf '%s\n' "- $f"
  printf '  alerts=%s summary=%s description=%s runbook_url=%s\n' "$alerts" "$summary" "$description" "$runbook"

  if (( alerts > 0 )) && (( summary < alerts || description < alerts || runbook < alerts )); then
    echo "  FAIL: annotation counts are lower than alert count"
    fail=1
  else
    echo "  OK"
  fi
done

if (( fail == 1 )); then
  echo "\nOne or more files appear to be missing alert metadata annotations." >&2
  exit 3
fi

echo "\nAll files passed heuristic metadata checks."
