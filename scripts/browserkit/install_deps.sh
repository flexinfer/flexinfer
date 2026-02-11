#!/usr/bin/env bash
set -euo pipefail

PY_BASE="${BROWSERKIT_PYTHON_BASE:-python3}"
VENV_DIR="${BROWSERKIT_VENV_DIR:-$HOME/.config/loom/browserkit-venv}"

if ! command -v "$PY_BASE" >/dev/null 2>&1; then
  echo "ERROR: '$PY_BASE' not found. Set BROWSERKIT_PYTHON_BASE or install python3." >&2
  exit 1
fi

echo "Setting up BrowserKit venv at: $VENV_DIR"
if [ ! -x "$VENV_DIR/bin/python" ] && [ ! -x "$VENV_DIR/bin/python3" ]; then
  set -x
  "$PY_BASE" -m venv "$VENV_DIR"
  set +x
fi

PY="$VENV_DIR/bin/python"
if [ ! -x "$PY" ]; then
  PY="$VENV_DIR/bin/python3"
fi
if [ ! -x "$PY" ]; then
  echo "ERROR: venv python not found under $VENV_DIR/bin" >&2
  exit 2
fi

echo "Using Python: $("$PY" --version 2>/dev/null || true)"

echo "Upgrading pip tooling..."
set -x
"$PY" -m pip install -U pip setuptools wheel
set +x

echo "Installing Python deps for BrowserKit (flexinfer-browser-kit + playwright)..."
set -x
"$PY" -m pip install -U flexinfer-browser-kit playwright
set +x

echo "Installing Playwright Chromium (download)..."
set -x
"$PY" -m playwright install chromium
set +x

echo "Done."
echo ""
echo "Next:"
echo "  export BROWSERKIT_PYTHON=\"$PY\""
echo "  bash scripts/browserkit/check_ready.sh"
