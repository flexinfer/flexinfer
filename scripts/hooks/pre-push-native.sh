#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WITH_CLEAN_GIT_ENV="${REPO_ROOT}/scripts/dev/with-clean-git-env.sh"

cd "${REPO_ROOT}"

"${WITH_CLEAN_GIT_ENV}" bash scripts/ci/go_build_hook.sh "$@"
"${WITH_CLEAN_GIT_ENV}" go test -short ./...
