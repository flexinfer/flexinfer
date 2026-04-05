#!/usr/bin/env bash
# session-retro.sh — Automated session retrospective
# Extracts failures, novel solutions, and friction points from the session,
# writes structured findings to .loom/local/retro-<timestamp>.md,
# and appends to .loom/local/retro-queue.md for batch review.
set -euo pipefail

LOOM_BIN="${LOOM_BINARY:-loom}"
AGENT_ID="${AGENT_ID:-unknown}"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || echo "$PWD")"
RETRO_DIR="${REPO_ROOT}/.loom/local"
RETRO_FILE="${RETRO_DIR}/retro-${TIMESTAMP}.md"
QUEUE_FILE="${RETRO_DIR}/retro-queue.md"

mkdir -p "$RETRO_DIR"

# Get session summary from agent context
SESSION_JSON=$("$LOOM_BIN" agent session --agent-id "$AGENT_ID" --quiet 2>/dev/null || echo '{}')
SESSION_ID=$(echo "$SESSION_JSON" | jq -r '.session.id // "unknown"' 2>/dev/null || echo "unknown")
SUMMARY=$(echo "$SESSION_JSON" | jq -r '.session.summary // "No summary available"' 2>/dev/null || echo "No summary available")
NAMESPACE=$(echo "$SESSION_JSON" | jq -r '.session.namespace // "unknown"' 2>/dev/null || echo "unknown")

# Write individual retro file
cat > "$RETRO_FILE" << EOF
# Session Retrospective

- **Session:** ${SESSION_ID}
- **Agent:** ${AGENT_ID}
- **Namespace:** ${NAMESPACE}
- **Timestamp:** ${TIMESTAMP}

## Session Summary

${SUMMARY}

## Extraction Prompts

> Review the session summary above and identify:
> 1. **Failures encountered** — what broke, what was the root cause?
> 2. **Novel solutions** — patterns or approaches worth recording as recipes
> 3. **Workflow friction** — where did the process slow down or confuse?
> 4. **Instruction gaps** — what should AGENTS.md or skill instructions say that they don't?

## Proposed Actions

<!-- Fill in during batch review -->
- [ ] Add recipe for: ...
- [ ] Update skill instructions for: ...
- [ ] Fix workflow step: ...
EOF

# Append to rolling queue
cat >> "$QUEUE_FILE" << EOF

---
### ${TIMESTAMP} | ${NAMESPACE} | ${AGENT_ID}
Session: ${SESSION_ID}
Summary: $(echo "$SUMMARY" | head -3)
Retro: ${RETRO_FILE}
EOF

echo "Retrospective written to ${RETRO_FILE}"
echo "Queued in ${QUEUE_FILE}"
