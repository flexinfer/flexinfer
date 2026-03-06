#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WITH_CLEAN_GIT_ENV="${SCRIPT_DIR}/with-clean-git-env.sh"

resolve_common_dir() {
  local common_dir
  common_dir="$("${WITH_CLEAN_GIT_ENV}" git -C "${REPO_ROOT}" -c core.bare=false rev-parse --git-common-dir)"
  if [[ "${common_dir}" != /* ]]; then
    common_dir="$(cd "${REPO_ROOT}/${common_dir}" && pwd)"
  fi
  printf '%s\n' "${common_dir}"
}

install_hook() {
  local hook_name="$1"
  local common_dir="$2"
  install -m 0755 "${REPO_ROOT}/scripts/hooks/${hook_name}" "${common_dir}/hooks/${hook_name}"
}

main() {
  local common_dir

  "${WITH_CLEAN_GIT_ENV}" git -C "${REPO_ROOT}" -c core.bare=false config --local core.bare false
  common_dir="$(resolve_common_dir)"
  mkdir -p "${common_dir}/hooks"
  install_hook pre-commit "${common_dir}"
  install_hook pre-push "${common_dir}"

  echo "git-setup: repo root      ${REPO_ROOT}"
  echo "git-setup: git common dir ${common_dir}"
  echo "git-setup: core.bare=false"
  echo "git-setup: installed hooks pre-commit, pre-push"
}

main "$@"
