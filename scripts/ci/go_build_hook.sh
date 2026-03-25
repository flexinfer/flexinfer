#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WITH_CLEAN_GIT_ENV="${REPO_ROOT}/scripts/dev/with-clean-git-env.sh"

cd "${REPO_ROOT}"

# Hook helper for go-build:
# - skip pre-push runs when no Go files changed in the push range
# - avoid repeated no-space linker failures by proactively cleaning Go caches
#   when free disk drops below threshold.

ZERO_SHA="0000000000000000000000000000000000000000"
MIN_FREE_MB="${GO_BUILD_MIN_FREE_MB:-4096}"

free_mb() {
  df -Pk . | awk 'NR==2 {print int($4/1024)}'
}

cleanup_go_caches_if_low_space() {
  local before after
  before="$(free_mb)"
  if (( before >= MIN_FREE_MB )); then
    return 0
  fi

  echo "go-build: low free space (${before}MiB < ${MIN_FREE_MB}MiB); cleaning Go caches..."
  go clean -cache -testcache -modcache || true

  after="$(free_mb)"
  echo "go-build: free space after cleanup: ${after}MiB"
  if (( after < MIN_FREE_MB )); then
    echo "go-build: insufficient free space after cleanup (${after}MiB < ${MIN_FREE_MB}MiB)"
    echo "go-build: free disk space and retry (tip: GO_BUILD_MIN_FREE_MB can tune threshold)"
    return 1
  fi
}

has_go_changes_in_range() {
  local range="${1:-}"
  if [[ -z "$range" ]]; then
    return 0
  fi
  "${WITH_CLEAN_GIT_ENV}" git diff --name-only "$range" -- '*.go' | grep -q .
}

push_range() {
  local from="${PRE_COMMIT_FROM_REF:-}"
  local to="${PRE_COMMIT_TO_REF:-}"
  local upstream base

  if [[ -n "$from" && -n "$to" && "$from" != "$ZERO_SHA" && "$to" != "$ZERO_SHA" ]]; then
    echo "${from}..${to}"
    return 0
  fi

  upstream="$("${WITH_CLEAN_GIT_ENV}" git rev-parse --abbrev-ref --symbolic-full-name '@{upstream}' 2>/dev/null || true)"
  if [[ -n "$upstream" ]]; then
    base="$("${WITH_CLEAN_GIT_ENV}" git merge-base HEAD "$upstream" 2>/dev/null || true)"
    if [[ -n "$base" ]]; then
      echo "${base}..HEAD"
      return 0
    fi
  fi

  if "${WITH_CLEAN_GIT_ENV}" git rev-parse --verify origin/main >/dev/null 2>&1; then
    base="$("${WITH_CLEAN_GIT_ENV}" git merge-base HEAD origin/main 2>/dev/null || true)"
    if [[ -n "$base" ]]; then
      echo "${base}..HEAD"
      return 0
    fi
  fi

  if "${WITH_CLEAN_GIT_ENV}" git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
    echo "HEAD~1..HEAD"
    return 0
  fi

  echo ""
}

is_pre_push=false
if [[ -n "${PRE_COMMIT_FROM_REF:-}" || -n "${PRE_COMMIT_TO_REF:-}" ]]; then
  is_pre_push=true
fi

if [[ "$is_pre_push" == "true" ]]; then
  range="$(push_range)"
  if [[ -n "$range" ]] && ! has_go_changes_in_range "$range"; then
    echo "go-build: no Go changes in push range (${range}); skipping."
    exit 0
  fi
fi

cleanup_go_caches_if_low_space

# Respect caller's CGO_ENABLED; default to Makefile convention (0) for speed.
# When CGO_ENABLED=1 is desired (e.g., fi-accel acceleration), set it
# explicitly: CGO_ENABLED=1 git commit ...
export CGO_ENABLED="${CGO_ENABLED:-0}"
echo "go-build: running go build ./... (CGO_ENABLED=${CGO_ENABLED})"
"${WITH_CLEAN_GIT_ENV}" go build ./...
