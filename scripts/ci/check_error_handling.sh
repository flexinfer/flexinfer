#!/usr/bin/env bash
set -euo pipefail

# Error handling guardrail: ensure "return nil, err" / "return nil, fmt.Errorf"
# count in MCP handler files does not increase above the baseline.
#
# MCP handlers should return (mcp.ErrorResult(err), nil), not (nil, err).
# Helper/utility functions may legitimately return (nil, err) so this is a
# ratchet, not a hard zero-tolerance check.

BASELINE=${ERROR_HANDLING_BASELINE:-318}

count=$(grep -rn 'return nil, \(err\|fmt\.Errorf\)' cmd/mcp-*/main.go 2>/dev/null | wc -l | tr -d ' ')

if [[ "$count" -gt "$BASELINE" ]]; then
  echo "error-handling-guardrail: FAILED"
  echo ""
  echo "  return nil, err/fmt.Errorf count: $count (baseline: $BASELINE)"
  echo "  New handler code should use mcp.ErrorResult(err) instead."
  echo ""
  echo "  To update the baseline after migrating existing code:"
  echo "    ERROR_HANDLING_BASELINE=$count make ci-guardrails"
  exit 1
fi

echo "error-handling-guardrail: passed ($count <= $BASELINE baseline)"
