#!/usr/bin/env bash
set -euo pipefail

# Pre-commit hook wrapper for goimports.
#
# Uses check-only mode (-l) instead of in-place rewrite (-w) to avoid a
# known conflict between pre-commit's stash/unstash mechanism and file-
# modifying hooks. When a file has both staged and unstaged changes,
# pre-commit stashes the unstaged portion, the hook rewrites the staged
# version, and then stash-pop produces merge-conflict markers.
#
# By only checking (never writing), we sidestep the stash race entirely.
# On failure the developer gets a copy-pasteable fix command.

if [ "$#" -eq 0 ]; then
  exit 0
fi

GOIMPORTS="${HOME}/go/bin/goimports"
LOCAL_PREFIX="github.com/crb2nu/loom"

if [ ! -x "$GOIMPORTS" ]; then
  echo "goimports not found at $GOIMPORTS — skipping"
  exit 0
fi

needs_fmt=$("$GOIMPORTS" -local "$LOCAL_PREFIX" -l "$@" 2>/dev/null || true)
if [ -z "$needs_fmt" ]; then
  exit 0
fi

echo "goimports: the following files have incorrect import grouping:"
echo "$needs_fmt" | sed 's/^/  /'
echo ""
echo "Fix with:"
echo "  ~/go/bin/goimports -local $LOCAL_PREFIX -w $needs_fmt"
exit 1
