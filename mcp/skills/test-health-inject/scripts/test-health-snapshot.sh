#!/usr/bin/env bash
# test-health-snapshot.sh — Quick test suite health snapshot
# Detects language, runs test suite with timeout, emits structured JSON systemMessage.
# Designed to run during SessionStart — keeps total runtime under 30s.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || echo "$PWD")"
cd "$REPO_ROOT"
TIMEOUT="${TEST_HEALTH_TIMEOUT:-30}"

# --- Language Detection ---
detect_language() {
    if [ -f "go.mod" ]; then echo "go"
    elif [ -f "Cargo.toml" ]; then echo "rust"
    elif [ -f "package.json" ]; then echo "typescript"
    elif [ -f "pyproject.toml" ] || [ -f "setup.py" ] || [ -f "requirements.txt" ]; then echo "python"
    else echo "unknown"
    fi
}

LANG="$(detect_language)"
LAST_COMMIT="$(git log -1 --oneline 2>/dev/null || echo 'unknown')"

# --- Run Tests with Timeout ---
run_tests() {
    local cmd=""
    case "$LANG" in
        go)      cmd="go test ./... -count=1 -timeout ${TIMEOUT}s" ;;
        rust)    cmd="cargo test" ;;
        typescript) cmd="npm test -- --forceExit" ;;
        python)  cmd="pytest --timeout=${TIMEOUT} -q" ;;
        *)       echo '{"systemMessage":"Test health: unknown project language, skipping test discovery."}'; exit 0 ;;
    esac

    local start_time
    start_time=$(date +%s)

    # Run with system timeout as safety net
    local output exit_code
    output=$(timeout "$TIMEOUT" bash -c "$cmd" 2>&1) || exit_code=$?
    exit_code=${exit_code:-0}

    local end_time elapsed
    end_time=$(date +%s)
    elapsed=$((end_time - start_time))

    echo "$output" "$exit_code" "$elapsed"
}

# --- Parse Results ---
parse_go_results() {
    local output="$1" exit_code="$2" elapsed="$3"
    local total=0 passed=0 failed=0 skipped=0
    local failed_names=""

    total=$(echo "$output" | grep -c "^---" 2>/dev/null || echo 0)
    passed=$(echo "$output" | grep -c "^--- PASS" 2>/dev/null || echo 0)
    failed=$(echo "$output" | grep -c "^--- FAIL" 2>/dev/null || echo 0)
    skipped=$(echo "$output" | grep -c "^--- SKIP" 2>/dev/null || echo 0)
    failed_names=$(echo "$output" | grep "^--- FAIL" | sed 's/^--- FAIL: //' | cut -d' ' -f1 | head -5 | tr '\n' ', ' | sed 's/,$//')

    # If no individual test markers, try package-level
    if [ "$total" -eq 0 ]; then
        total=$(echo "$output" | grep -cE "^(ok|FAIL|---)" 2>/dev/null || echo 0)
        passed=$(echo "$output" | grep -c "^ok " 2>/dev/null || echo 0)
        failed=$(echo "$output" | grep -c "^FAIL" 2>/dev/null || echo 0)
    fi

    local build_status="OK"
    if echo "$output" | grep -q "build failed\|cannot find\|compilation error" 2>/dev/null; then
        build_status="FAIL"
    fi

    local msg="Project health: ${total} test groups, ${passed} passed, ${failed} failed"
    if [ -n "$failed_names" ]; then
        msg="${msg} (${failed_names})"
    fi
    if [ "$skipped" -gt 0 ]; then
        msg="${msg}, ${skipped} skipped"
    fi
    msg="${msg}. Build: ${build_status}. Runtime: ${elapsed}s. Last commit: ${LAST_COMMIT}"

    printf '{"systemMessage":"%s"}\n' "$msg"
}

parse_generic_results() {
    local output="$1" exit_code="$2" elapsed="$3"
    local status="PASS"
    if [ "$exit_code" -ne 0 ]; then
        status="FAIL"
    fi
    printf '{"systemMessage":"Project health (%s): tests %s (exit %s). Runtime: %ss. Last commit: %s"}\n' \
        "$LANG" "$status" "$exit_code" "$elapsed" "$LAST_COMMIT"
}

# --- Main ---
RESULT=$(run_tests)
# Split result: output is everything before last two words, exit_code and elapsed are last two
EXIT_CODE=$(echo "$RESULT" | tail -1 | awk '{print $(NF-1)}')
ELAPSED=$(echo "$RESULT" | tail -1 | awk '{print $NF}')
OUTPUT=$(echo "$RESULT" | head -n -1)

# Fallback parsing if splitting fails
if ! [[ "$EXIT_CODE" =~ ^[0-9]+$ ]]; then
    EXIT_CODE=1
    ELAPSED=0
    OUTPUT="$RESULT"
fi

case "$LANG" in
    go) parse_go_results "$OUTPUT" "$EXIT_CODE" "$ELAPSED" ;;
    *)  parse_generic_results "$OUTPUT" "$EXIT_CODE" "$ELAPSED" ;;
esac
