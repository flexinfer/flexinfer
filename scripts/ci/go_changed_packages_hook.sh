#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <vet|lint>" >&2
  exit 2
fi

MODE="$1"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WITH_CLEAN_GIT_ENV="${REPO_ROOT}/scripts/dev/with-clean-git-env.sh"

cd "${REPO_ROOT}"

mapfile -t packages < <(
  "${WITH_CLEAN_GIT_ENV}" git diff --cached --name-only --diff-filter=ACM -- '*.go' \
    | xargs -n1 dirname 2>/dev/null \
    | awk '!seen[$0]++ { print ($0 == "." ? "./" : "./" $0) }'
)

if [[ ${#packages[@]} -eq 0 ]]; then
  exit 0
fi

case "${MODE}" in
  vet)
    exec "${WITH_CLEAN_GIT_ENV}" go vet "${packages[@]}"
    ;;
  lint)
    exec "${WITH_CLEAN_GIT_ENV}" bash -c '$HOME/go/bin/golangci-lint run "$@"' -- "${packages[@]}"
    ;;
  *)
    echo "unsupported mode: ${MODE}" >&2
    exit 2
    ;;
esac
