#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <command> [args...]" >&2
  exit 2
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WITH_CLEAN_GIT_ENV="${REPO_ROOT}/scripts/dev/with-clean-git-env.sh"

cd "${REPO_ROOT}"

before_stash="$("${WITH_CLEAN_GIT_ENV}" git rev-parse -q --verify refs/stash 2>/dev/null || true)"
"${WITH_CLEAN_GIT_ENV}" git stash push --keep-index --include-untracked --quiet --message "loom-hook-temporary" >/dev/null 2>&1 || true
after_stash="$("${WITH_CLEAN_GIT_ENV}" git rev-parse -q --verify refs/stash 2>/dev/null || true)"
stash_created=false

if [[ -n "${after_stash}" && "${after_stash}" != "${before_stash}" ]]; then
  stash_created=true
fi

restore_stash() {
  if [[ "${stash_created}" == "true" ]]; then
    "${WITH_CLEAN_GIT_ENV}" git stash pop --quiet >/dev/null
  fi
}

trap restore_stash EXIT

"$@"
