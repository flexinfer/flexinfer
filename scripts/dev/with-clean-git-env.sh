#!/usr/bin/env bash
set -euo pipefail

# Worktree-friendly commands should rely on cwd-based repo discovery, not
# inherited GIT_DIR/GIT_WORK_TREE overrides from parent processes.
unset GIT_DIR
unset GIT_WORK_TREE
unset GIT_COMMON_DIR
unset GIT_IMPLICIT_WORK_TREE
unset GIT_PREFIX

exec "$@"
