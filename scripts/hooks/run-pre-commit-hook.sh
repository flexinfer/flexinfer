#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <pre-commit|pre-push> [hook args...]" >&2
  exit 2
fi

HOOK_TYPE="$1"
shift

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WITH_CLEAN_GIT_ENV="${REPO_ROOT}/scripts/dev/with-clean-git-env.sh"

resolve_hook_dir() {
  local hook_dir
  hook_dir="$("${WITH_CLEAN_GIT_ENV}" git -C "${REPO_ROOT}" -c core.bare=false rev-parse --git-path hooks)"
  if [[ "${hook_dir}" != /* ]]; then
    hook_dir="$(cd "${REPO_ROOT}/${hook_dir}" && pwd)"
  fi
  printf '%s\n' "${hook_dir}"
}

run_pre_commit() {
  local hook_dir runner=()
  hook_dir="$(resolve_hook_dir)"

  if command -v pre-commit >/dev/null 2>&1; then
    runner=(pre-commit)
  elif command -v python3 >/dev/null 2>&1 && python3 -c 'import pre_commit' >/dev/null 2>&1; then
    runner=(python3 -m pre_commit)
  else
    return 127
  fi

  cd "${REPO_ROOT}"
  "${WITH_CLEAN_GIT_ENV}" "${runner[@]}" hook-impl \
    --config=.pre-commit-config.yaml \
    --hook-type "${HOOK_TYPE}" \
    --hook-dir "${hook_dir}" \
    -- "$@"
}

run_pre_commit "$@"
