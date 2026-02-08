#!/usr/bin/env bash
set -euo pipefail

PY="${BROWSERKIT_PYTHON:-python3}"
PIP_ARGS="${BROWSERKIT_PIP_ARGS:---user}"

if ! command -v "$PY" >/dev/null 2>&1; then
  echo "ERROR: '$PY' not found. Set BROWSERKIT_PYTHON or install python3." >&2
  exit 1
fi

echo "Installing Python deps for BrowserKit (flexinfer-browser-kit + playwright)..."
set -x
"$PY" -m pip install -U ${PIP_ARGS} flexinfer-browser-kit playwright
set +x

echo "Installing Playwright Chromium (download)..."
set -x
"$PY" -m playwright install chromium
set +x

echo "Done. Run: scripts/browserkit/check_ready.sh"
