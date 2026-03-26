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

has_pre_commit_config() {
  [[ -f "${REPO_ROOT}/.pre-commit-config.yaml" ]]
}

should_prefer_native_hook() {
  local pre_commit_cache="${PRE_COMMIT_HOME:-${XDG_CACHE_HOME:-${HOME}/.cache}/pre-commit}"
  local go_build_cache="${GOCACHE:-${HOME}/Library/Caches/go-build}"
  local go_mod_cache_parent="${GOMODCACHE:-${HOME}/go/pkg/mod/cache}"

  if [[ "${LOOM_HOOK_FORCE_NATIVE:-}" == "1" ]]; then
    return 0
  fi

  if { [[ -d "${pre_commit_cache}" ]] && [[ ! -w "${pre_commit_cache}" ]]; } || { [[ ! -d "${pre_commit_cache}" ]] && [[ ! -w "$(dirname "${pre_commit_cache}")" ]]; }; then
    return 0
  fi
  if { [[ -d "${go_build_cache}" ]] && [[ ! -w "${go_build_cache}" ]]; } || { [[ ! -d "${go_build_cache}" ]] && [[ ! -w "$(dirname "${go_build_cache}")" ]]; }; then
    return 0
  fi
  if { [[ -d "${go_mod_cache_parent}" ]] && [[ ! -w "${go_mod_cache_parent}" ]]; } || { [[ ! -d "${go_mod_cache_parent}" ]] && [[ ! -w "$(dirname "${go_mod_cache_parent}")" ]]; }; then
    return 0
  fi

  return 1
}
run_pre_commit() {
  local hook_dir runner=()
  hook_dir="$(resolve_hook_dir)"

  if ! has_pre_commit_config; then
    return 127
  fi
  if should_prefer_native_hook; then
    return 127
  fi

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
