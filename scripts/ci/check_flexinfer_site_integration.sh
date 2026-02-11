#!/usr/bin/env bash
set -euo pipefail

DOC_FILE="docs/FLEXINFER_SITE_INTEGRATION.md"
SITE_REPO="${FLEXINFER_SITE_REPO:-../flexinfer-site}"
SYNC_SCRIPT="${SITE_REPO}/scripts/sync-docs.mjs"

if [[ ! -f "${DOC_FILE}" ]]; then
  echo "flexinfer-site-guardrail: missing ${DOC_FILE}"
  exit 1
fi

required_doc_patterns=(
  "services/flexinfer-site"
  "pnpm sync:loom-core-docs"
  "content/loom-core-docs"
  "/docs/loom-core"
)

for pattern in "${required_doc_patterns[@]}"; do
  if ! grep -q "${pattern}" "${DOC_FILE}"; then
    echo "flexinfer-site-guardrail: ${DOC_FILE} is missing required text: ${pattern}"
    exit 1
  fi
done

if [[ -f "${SYNC_SCRIPT}" ]]; then
  required_sync_patterns=(
    "name: 'loom-core'"
    "source: '../loom-core/docs'"
    "target: 'content/loom-core-docs'"
  )

  for pattern in "${required_sync_patterns[@]}"; do
    if ! grep -q "${pattern}" "${SYNC_SCRIPT}"; then
      echo "flexinfer-site-guardrail: ${SYNC_SCRIPT} is missing expected mapping: ${pattern}"
      exit 1
    fi
  done

  echo "flexinfer-site-guardrail: passed (verified docs + local flexinfer-site sync mapping)"
  exit 0
fi

echo "flexinfer-site-guardrail: passed (verified docs; local flexinfer-site repo not present, skipped mapping check)"
