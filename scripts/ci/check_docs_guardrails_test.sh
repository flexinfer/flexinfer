#!/usr/bin/env bash
#
# Regression harness for scripts/ci/check_docs_guardrails.sh.
#
# Exercises the guardrail against a fresh temp git repo by seeding a base
# commit + a candidate HEAD commit whose diff contains only the files named
# by each case, then asserts the guardrail's exit code matches the expectation.
#
# Cases cover:
#   - pure test-only diffs (*_test.go, mocks, Python tests) must PASS
#   - pure generated artifacts (dist/, testdata/, _golden., *.snap) must PASS
#   - a user-facing code change with no docs update must FAIL
#   - a user-facing code change paired with CHANGELOG must PASS
#
# Run: bash scripts/ci/check_docs_guardrails_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GUARDRAIL="$SCRIPT_DIR/check_docs_guardrails.sh"

if [[ ! -x "$GUARDRAIL" ]]; then
  chmod +x "$GUARDRAIL" || true
fi

pass_count=0
fail_count=0

# run_case <name> <expected_exit_code> <comma-separated-file-list>
run_case() {
  local name="$1"
  local expected="$2"
  local files_csv="$3"

  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  git init -q "$tmp"
  git -C "$tmp" config user.email "test@example.com"
  git -C "$tmp" config user.name "test"
  git -C "$tmp" commit -q --allow-empty -m "base"

  local IFS=','
  for f in $files_csv; do
    mkdir -p "$tmp/$(dirname "$f")"
    printf 'content\n' >> "$tmp/$f"
  done
  git -C "$tmp" add -A
  git -C "$tmp" commit -q -m "candidate change"

  local actual=0
  ( cd "$tmp" && CI=1 DOCS_CHECK_BASE_REF="HEAD~1" bash "$GUARDRAIL" >/tmp/docs-guard-out.txt 2>&1 ) || actual=$?

  if [[ "$actual" -eq "$expected" ]]; then
    printf 'PASS  %-60s (exit=%d)\n' "$name" "$actual"
    pass_count=$((pass_count + 1))
  else
    printf 'FAIL  %-60s (want exit=%d, got=%d)\n' "$name" "$expected" "$actual"
    echo "----- guardrail output -----"
    cat /tmp/docs-guard-out.txt
    echo "----- /guardrail output -----"
    fail_count=$((fail_count + 1))
  fi

  rm -rf "$tmp"
  trap - RETURN
}

# Test-only changes should NOT require docs.
run_case "go test file only"             0 "pkg/foo/bar_test.go"
run_case "go mock file only"             0 "pkg/foo/bar_mock.go"
run_case "mocks directory only"          0 "internal/svc/mocks/mock_client.go"
run_case "python pytest file only"       0 "tools/release/test_release.py"
run_case "python unittest-style only"    0 "tools/release/release_test.py"

# Generated/build artifacts should NOT require docs.
run_case "frontend dist bundle only"     0 "internal/hud/frontend/dist/index-abc.js"
run_case "testdata fixture only"         0 "pkg/foo/testdata/fixture.json"
run_case "contract golden only"          0 "pkg/generator/config_codex_golden.yaml"
run_case "frontend snapshot only"        0 "internal/hud/frontend/__snapshots__/App.spec.snap"

# Genuine user-facing code changes still FAIL without a doc touch.
run_case "pkg code without docs"         1 "pkg/foo/bar.go"
run_case "cmd code without docs"         1 "cmd/loom/cmd_new.go"

# Pairing a user-facing change with CHANGELOG should PASS.
run_case "pkg code with CHANGELOG"       0 "pkg/foo/bar.go,CHANGELOG.md"

# Mixed: user-facing code + test file = still needs docs because of the code.
run_case "pkg code + test, no docs"      1 "pkg/foo/bar.go,pkg/foo/bar_test.go"
run_case "pkg code + test + CHANGELOG"   0 "pkg/foo/bar.go,pkg/foo/bar_test.go,CHANGELOG.md"

echo ""
echo "Summary: $pass_count passed, $fail_count failed"

if [[ "$fail_count" -gt 0 ]]; then
  exit 1
fi
