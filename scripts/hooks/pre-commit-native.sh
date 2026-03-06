#!/usr/bin/env bash
# Native Git pre-commit hook for loom-core.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
WITH_CLEAN_GIT_ENV="${REPO_ROOT}/scripts/dev/with-clean-git-env.sh"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

cd "${REPO_ROOT}"

echo -e "${YELLOW}Running pre-commit checks...${NC}"

echo -n "Checking flexinfer-site docs integration... "
if ! bash scripts/ci/check_flexinfer_site_integration.sh >/dev/null 2>&1; then
  echo -e "${RED}FAILED${NC}"
  bash scripts/ci/check_flexinfer_site_integration.sh
  exit 1
fi
echo -e "${GREEN}OK${NC}"

STAGED_GO_FILES="$("${WITH_CLEAN_GIT_ENV}" git diff --cached --name-only --diff-filter=ACM | grep '\.go$' || true)"

if [[ -z "${STAGED_GO_FILES}" ]]; then
  echo -e "${GREEN}No Go files staged, skipping checks.${NC}"
  exit 0
fi

check_tool() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo -e "${YELLOW}Warning: $1 not found, skipping $2${NC}"
    return 1
  fi
  return 0
}

FAILED=0

echo -n "Checking gofmt... "
UNFORMATTED="$(gofmt -l ${STAGED_GO_FILES} 2>/dev/null || true)"
if [[ -n "${UNFORMATTED}" ]]; then
  echo -e "${RED}FAILED${NC}"
  echo "The following files need formatting:"
  echo "${UNFORMATTED}"
  echo ""
  echo "Run: gofmt -w ${UNFORMATTED}"
  FAILED=1
else
  echo -e "${GREEN}OK${NC}"
fi

if check_tool goimports "import ordering"; then
  echo -n "Checking goimports... "
  UNIMPORTED="$(goimports -l ${STAGED_GO_FILES} 2>/dev/null || true)"
  if [[ -n "${UNIMPORTED}" ]]; then
    echo -e "${RED}FAILED${NC}"
    echo "The following files have import issues:"
    echo "${UNIMPORTED}"
    echo ""
    echo "Run: goimports -w -local github.com/crb2nu/loom ${UNIMPORTED}"
    FAILED=1
  else
    echo -e "${GREEN}OK${NC}"
  fi
fi

echo -n "Running go vet... "
if ! "${WITH_CLEAN_GIT_ENV}" go vet ./... 2>&1; then
  echo -e "${RED}FAILED${NC}"
  FAILED=1
else
  echo -e "${GREEN}OK${NC}"
fi

echo -n "Checking build... "
if ! "${WITH_CLEAN_GIT_ENV}" go build ./... 2>&1; then
  echo -e "${RED}FAILED${NC}"
  FAILED=1
else
  echo -e "${GREEN}OK${NC}"
fi

if check_tool golangci-lint "linting"; then
  echo -n "Running golangci-lint (fast)... "
  if ! "${WITH_CLEAN_GIT_ENV}" golangci-lint run --fast --timeout 1m ./... 2>&1; then
    echo -e "${RED}FAILED${NC}"
    FAILED=1
  else
    echo -e "${GREEN}OK${NC}"
  fi
fi

echo -n "Checking for debug statements... "
DEBUG_PATTERNS='fmt\.Print|log\.Print|panic\(|os\.Exit'
if grep -l -E "${DEBUG_PATTERNS}" ${STAGED_GO_FILES} 2>/dev/null | grep -v '_test\.go$$' | head -5; then
  echo -e "${YELLOW}WARNING: Found potential debug statements (review before committing)${NC}"
else
  echo -e "${GREEN}OK${NC}"
fi

echo -n "Checking for TODOs... "
TODO_COUNT="$(grep -c -E 'TODO|FIXME|XXX|HACK' ${STAGED_GO_FILES} 2>/dev/null | awk -F: '{sum += $NF} END {print sum}' || echo "0")"
if [[ "${TODO_COUNT}" -gt 0 ]]; then
  echo -e "${YELLOW}Found ${TODO_COUNT} TODO/FIXME comments${NC}"
fi

echo ""
if [[ ${FAILED} -ne 0 ]]; then
  echo -e "${RED}Pre-commit checks failed. Please fix the issues above.${NC}"
  echo "To bypass: git commit --no-verify"
  exit 1
fi

echo -e "${GREEN}All pre-commit checks passed!${NC}"
