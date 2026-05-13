#!/usr/bin/env bash
# check_mirror_drift.sh — pre-commit gate.
#
# When mcp/context/registry.yaml or mcp/context/skills-registry.yaml change in
# services/loom-core, the platform/gitops/mcp/context/ mirror must match.
# Fails the commit with a clear remediation command if drift is present.
#
# Skipped (exit 0) when the platform/gitops checkout isn't present locally,
# so developers without the GitOps repo don't get blocked.

set -euo pipefail

LOOM_BIN="${LOOM_BIN:-loom}"

if ! command -v "$LOOM_BIN" >/dev/null 2>&1; then
  echo "[mirror-drift] $LOOM_BIN not found in PATH; skipping (run 'go install ./cmd/loom')" >&2
  exit 0
fi

# Locate the workspace root by walking up from cwd until we find services/loom-core.
ws=""
dir="$PWD"
for _ in 1 2 3 4 5 6; do
  if [ -d "$dir/services/loom-core" ]; then
    ws="$dir"
    break
  fi
  parent="$(dirname "$dir")"
  [ "$parent" = "$dir" ] && break
  dir="$parent"
done

if [ -z "$ws" ]; then
  echo "[mirror-drift] could not locate workspace root from $PWD; skipping" >&2
  exit 0
fi

if [ ! -d "$ws/platform/gitops/mcp/context" ]; then
  echo "[mirror-drift] platform/gitops not cloned at $ws/platform/gitops; skipping" >&2
  exit 0
fi

# The gate compares canonical services/loom-core/mcp/context against the
# platform/gitops mirror. Worktree commits don't reach canonical until the
# worktree branch merges, so the comparison would falsely flag drift on
# feature branches. Skip on non-main branches; the gate is meant to fire
# on `main` after merge so the mirror is synced before pushing further.
branch="$(git symbolic-ref --short HEAD 2>/dev/null || echo)"
if [ -n "$branch" ] && [ "$branch" != "main" ] && [ "$branch" != "master" ]; then
  echo "[mirror-drift] on branch '$branch'; gate only enforces on main/master. Skipping (run 'loom sync mirror --apply' after merge)." >&2
  exit 0
fi

if ! "$LOOM_BIN" sync mirror --check; then
  cat >&2 <<EOF

[mirror-drift] mcp/context registries diverged from platform/gitops mirror.
[mirror-drift] Fix:
[mirror-drift]   $LOOM_BIN sync mirror --apply
[mirror-drift]   cd $ws/platform/gitops && git add mcp/context && git commit
[mirror-drift] Then re-run the commit on this repo.

EOF
  exit 1
fi
