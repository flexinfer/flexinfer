#!/usr/bin/env bash
set -euo pipefail

# Worktree-friendly commands should rely on cwd-based repo discovery, not
# inherited GIT_DIR/GIT_WORK_TREE overrides from parent processes.
unset GIT_DIR
unset GIT_WORK_TREE
unset GIT_COMMON_DIR
unset GIT_IMPLICIT_WORK_TREE
unset GIT_PREFIX

# In git worktrees the go.work file references sibling libs via relative paths
# (../../libs/*) that don't resolve from the worktree location. Disable the
# workspace overlay so go commands use go.mod alone. Also disable CGo since
# the fi-accel C header is not available on most machines.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  wt_root="$(git rev-parse --show-toplevel 2>/dev/null || true)"
  git_dir="$(git rev-parse --git-dir 2>/dev/null || true)"
  if [[ -n "$wt_root" && -n "$git_dir" ]]; then
    # In the main repo, --git-dir is ".git" (relative) or "$toplevel/.git".
    # In a worktree, --git-dir points to "$main_repo/.git/worktrees/<name>".
    resolved_git_dir="$(cd "$git_dir" && pwd)"
    if [[ "$resolved_git_dir" != "$wt_root/.git" ]]; then
      export GOWORK=off
      export CGO_ENABLED=0
    fi
  fi
fi

exec "$@"
