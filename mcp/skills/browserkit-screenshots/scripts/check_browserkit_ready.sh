#!/usr/bin/env bash
set -euo pipefail

PY="${BROWSERKIT_PYTHON:-python3}"

if ! command -v "$PY" >/dev/null 2>&1; then
  echo "ERROR: '$PY' not found. Set BROWSERKIT_PYTHON or install python3." >&2
  exit 1
fi

echo "Python: $("$PY" --version 2>/dev/null || true)"

echo "Checking imports (flexinfer-browser-kit + playwright)..."
"$PY" - <<'PY'
import sys

try:
    from browser_kit.browser import BrowserConfig, BrowserManager  # noqa: F401
except Exception as e:
    print(f"ERROR: failed to import flexinfer-browser-kit: {e}", file=sys.stderr)
    print("Install:\n  pip install flexinfer-browser-kit playwright", file=sys.stderr)
    raise SystemExit(2)

try:
    from playwright.sync_api import sync_playwright  # noqa: F401
except Exception as e:
    print(f"ERROR: failed to import playwright: {e}", file=sys.stderr)
    print("Install:\n  pip install playwright\n  python3 -m playwright install chromium", file=sys.stderr)
    raise SystemExit(2)

print("OK: imports succeeded")
PY

echo "Checking Playwright Chromium install (launch headless)..."
"$PY" - <<'PY'
import sys
from playwright.sync_api import sync_playwright

try:
    p = sync_playwright().start()
    b = p.chromium.launch(headless=True)
    b.close()
    p.stop()
    print("OK: chromium launched successfully")
except Exception as e:
    print(f"ERROR: failed to launch chromium: {e}", file=sys.stderr)
    print("Fix:\n  python3 -m playwright install chromium", file=sys.stderr)
    raise SystemExit(3)
PY

echo "Ready: BrowserKit screenshots should work."
