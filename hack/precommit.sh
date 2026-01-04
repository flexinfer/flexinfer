#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

warn() { echo "precommit: $*" >&2; }

# Keep Go build cache inside the repo to avoid permission issues and reduce global cache churn.
mkdir -p .gocache .gotmp
export GOCACHE="${GOCACHE:-$root/.gocache}"
export GOTMPDIR="${GOTMPDIR:-$root/.gotmp}"

staged_go_files() {
  git diff --cached --name-only --diff-filter=ACMR | grep -E '\.go$' || true
}

run_gofmt_on_staged() {
  local files
  files="$(staged_go_files)"
  if [ -z "${files}" ]; then
    warn "no staged Go files; skipping gofmt"
    return 0
  fi

  warn "running gofmt on staged Go files"
  # shellcheck disable=SC2086
  gofmt -w ${files}
  # shellcheck disable=SC2086
  git add ${files}
}

run_gofmt_check_all() {
  local files
  files="$(git ls-files '*.go')"
  if [ -z "${files}" ]; then
    warn "no Go files tracked; skipping gofmt check"
    return 0
  fi
  # shellcheck disable=SC2086
  unformatted="$(gofmt -l ${files})"
  if [ -n "${unformatted}" ]; then
    echo "Go code is not formatted:" >&2
    echo "${unformatted}" >&2
    return 1
  fi
}

run_go_tests_fast() {
  warn "running unit tests (SKIP_ENVTEST=1)"
  SKIP_ENVTEST=1 go test ./... -short
}

run_go_vet() {
  warn "running go vet"
  go vet ./...
}

run_golangci_lint_if_available() {
  if command -v golangci-lint >/dev/null 2>&1; then
    warn "running golangci-lint"
    golangci-lint run ./... || warn "golangci-lint failed (fix or run manually before pushing)"
  else
    warn "golangci-lint not installed; skipping"
  fi
}

mode="${1:-}"
case "${mode}" in
  --check)
    run_gofmt_check_all
    run_go_vet
    run_go_tests_fast
    run_golangci_lint_if_available
    ;;
  "")
    run_gofmt_on_staged
    run_go_vet
    run_go_tests_fast
    ;;
  *)
    echo "usage: $0 [--check]" >&2
    exit 2
    ;;
esac
