#!/usr/bin/env bash
set -euo pipefail

ran_any=0

run_cmd() {
  echo "==> $*"
  "$@"
  ran_any=1
}

has_make_target() {
  local target="$1"
  make -n "$target" >/dev/null 2>&1
}

run_make_sequence() {
  if ! command -v make >/dev/null 2>&1 || [[ ! -f Makefile ]]; then
    return
  fi

  # Prefer repo-defined aggregate quality gates first.
  if has_make_target verify; then
    run_cmd make verify
    return
  fi
  if has_make_target ci; then
    run_cmd make ci
    return
  fi
  if has_make_target check; then
    run_cmd make check
    return
  fi

  # Fall back to common split targets.
  if has_make_target test; then
    run_cmd make test
  fi
  if has_make_target lint; then
    run_cmd make lint
  fi
}

run_precommit() {
  if [[ -f .pre-commit-config.yaml ]]; then
    if command -v pre-commit >/dev/null 2>&1; then
      run_cmd pre-commit run -a
    else
      echo "warning: .pre-commit-config.yaml found but pre-commit is not installed" >&2
    fi
  fi
}

run_language_fallbacks() {
  # Go
  if [[ -f go.mod ]] && command -v go >/dev/null 2>&1; then
    run_cmd go test ./...
    if command -v golangci-lint >/dev/null 2>&1; then
      run_cmd golangci-lint run
    fi
  fi

  # Node
  if [[ -f package.json ]] && command -v npm >/dev/null 2>&1; then
    run_cmd npm test --if-present
    run_cmd npm run lint --if-present
  fi

  # Python
  if [[ -f pyproject.toml || -f requirements.txt ]] && command -v pytest >/dev/null 2>&1; then
    run_cmd pytest -q
  fi

  # Rust
  if [[ -f Cargo.toml ]] && command -v cargo >/dev/null 2>&1; then
    run_cmd cargo test
    if command -v cargo-clippy >/dev/null 2>&1; then
      run_cmd cargo clippy --all-targets -- -D warnings
    fi
  fi
}

main() {
  run_precommit
  run_make_sequence

  # If no repo-defined checks ran, fall back to language heuristics.
  if [[ "$ran_any" -eq 0 ]]; then
    run_language_fallbacks
  fi

  if [[ "$ran_any" -eq 0 ]]; then
    echo "warning: no verification commands were detected for this repository" >&2
    exit 1
  fi
}

main "$@"
