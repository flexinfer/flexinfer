#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
cd "$root"

warn() { echo "precommit: $*" >&2; }

# Keep Go build cache inside the repo to avoid permission issues and reduce global cache churn.
mkdir -p .gocache .gotmp .golangci-lint-cache
export GOCACHE="${GOCACHE:-$root/.gocache}"
export GOTMPDIR="${GOTMPDIR:-$root/.gotmp}"
export GOLANGCI_LINT_CACHE="${GOLANGCI_LINT_CACHE:-$root/.golangci-lint-cache}"

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
  local lint_bin
  lint_bin="$(command -v golangci-lint 2>/dev/null || true)"
  if [ -z "${lint_bin}" ]; then
    lint_bin="$(go env GOPATH)/bin/golangci-lint"
  fi

  if [ -x "${lint_bin}" ]; then
    warn "running golangci-lint"
    # golangci-lint v2 requires a versioned config file, while CI currently
    # uses v1.x. Keep both configs and choose based on installed version.
    local cfg="$root/.golangci.yml"
    local lint_version
    lint_version="$("${lint_bin}" version 2>/dev/null | head -n 1 || true)"
    if echo "${lint_version}" | grep -qE '\bversion 2\b'; then
      if [ -f "$root/.golangci.v2.yml" ]; then
        cfg="$root/.golangci.v2.yml"
      fi
    fi
    "${lint_bin}" run --config "${cfg}" ./... || warn "golangci-lint failed (fix or run manually before pushing)"
  else
    warn "golangci-lint not installed; skipping"
  fi
}

run_helm_lint_if_available() {
  if [ "${SKIP_HELM:-}" = "1" ]; then
    warn "SKIP_HELM=1; skipping helm checks"
    return 0
  fi

  if [ ! -f "$root/charts/flexinfer/Chart.yaml" ]; then
    warn "no charts/flexinfer; skipping helm checks"
    return 0
  fi

  if ! command -v helm >/dev/null 2>&1; then
    warn "helm not installed; skipping helm checks"
    return 0
  fi

  warn "running helm lint/template"
  helm lint charts/flexinfer
  local rendered
  rendered="$(helm template flexinfer charts/flexinfer --namespace flexinfer-system)"
  if echo "$rendered" | grep -q 'flexinfer.ai/chart-version'; then
    echo "chart version must not be embedded in pod templates; it causes fleet-wide rollouts on metadata-only chart upgrades" >&2
    return 1
  fi
}

mode="${1:-}"
case "${mode}" in
  --check)
    run_gofmt_check_all
    run_go_vet
    run_go_tests_fast
    run_golangci_lint_if_available
    run_helm_lint_if_available
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
