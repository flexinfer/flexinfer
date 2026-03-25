#!/usr/bin/env bash
set -euo pipefail

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "docs-guardrail: no git repository available, skipping"
  exit 0
fi

latest_msg="$(git log -1 --pretty=%B 2>/dev/null || true)"
if printf '%s' "$latest_msg" | grep -qi '\[skip-docs-check\]'; then
  echo "docs-guardrail: skipped via [skip-docs-check]"
  exit 0
fi

remote="${DOCS_CHECK_REMOTE:-origin}"
base_ref="${DOCS_CHECK_BASE_REF:-}"

if [[ -z "$base_ref" ]]; then
  if [[ -n "${CI_MERGE_REQUEST_TARGET_BRANCH_NAME:-}" ]]; then
    base_ref="${remote}/${CI_MERGE_REQUEST_TARGET_BRANCH_NAME}"
  elif [[ -n "${GITHUB_BASE_REF:-}" ]]; then
    base_ref="${remote}/${GITHUB_BASE_REF}"
  elif [[ -n "${CI_DEFAULT_BRANCH:-}" ]]; then
    base_ref="${remote}/${CI_DEFAULT_BRANCH}"
  elif git rev-parse --verify "${remote}/main" >/dev/null 2>&1; then
    base_ref="${remote}/main"
  elif git rev-parse --verify HEAD~1 >/dev/null 2>&1; then
    base_ref="HEAD~1"
  fi
fi

if [[ -z "$base_ref" ]]; then
  echo "docs-guardrail: unable to determine base ref, skipping"
  exit 0
fi

if ! git rev-parse --verify "$base_ref" >/dev/null 2>&1; then
  for fallback in "${CI_MERGE_REQUEST_TARGET_BRANCH_NAME:-}" "${GITHUB_BASE_REF:-}" "${CI_DEFAULT_BRANCH:-}" "HEAD~1"; do
    if [[ -n "$fallback" ]] && git rev-parse --verify "$fallback" >/dev/null 2>&1; then
      base_ref="$fallback"
      break
    fi
  done
fi

if ! git rev-parse --verify "$base_ref" >/dev/null 2>&1; then
  echo "docs-guardrail: base ref '$base_ref' not available, skipping"
  exit 0
fi

if git merge-base "$base_ref" HEAD >/dev/null 2>&1; then
  changed_files="$(git diff --name-only "$base_ref"...HEAD)"
else
  changed_files="$(git diff --name-only "$base_ref" HEAD)"
fi

# Local development ergonomics: include uncommitted staged/unstaged changes
# so `make ci-guardrails` reflects the working tree, not only committed range.
if [[ -z "${CI:-}" ]]; then
  wt_changes="$(git diff --name-only; git diff --cached --name-only)"
  changed_files="$(printf '%s\n%s\n' "$changed_files" "$wt_changes" | awk 'NF' | sort -u)"
fi

if [[ -z "$changed_files" ]]; then
  echo "docs-guardrail: no changed files"
  exit 0
fi

code_pattern='^(cmd/|internal/|pkg/|scripts/|Makefile$|go\.mod$|go\.sum$|\.gitlab-ci\.yml$|\.github/workflows/)'
docs_pattern='^(README\.md$|CHANGELOG\.md$|ROADMAP\.md$|AGENTS\.md$|docs/)'

code_changes="$(printf '%s\n' "$changed_files" | grep -E "$code_pattern" || true)"
docs_changes="$(printf '%s\n' "$changed_files" | grep -E "$docs_pattern" || true)"

if [[ -n "$code_changes" && -z "$docs_changes" ]]; then
  echo "docs-guardrail: code-facing changes detected without doc updates"
  echo ""
  echo "Changed code-facing files:"
  printf '%s\n' "$code_changes"
  echo ""
  echo "Expected at least one change in README.md, CHANGELOG.md, ROADMAP.md, AGENTS.md, or docs/."
  echo "If this is intentional, include [skip-docs-check] in the commit message."
  exit 1
fi

echo "docs-guardrail: passed"
