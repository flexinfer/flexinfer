#!/usr/bin/env bash
# auto-quality-gate.sh — Language-aware automated quality verification
# Detects project language, runs fmt -> lint -> test -> diff-check.
# Exit 0 = all pass. Exit 1 = failure with structured report.
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || echo "$PWD")"
cd "$REPO_ROOT"

FAILURES=()
WARNINGS=()

# --- Language Detection ---
detect_language() {
    if [ -f "go.mod" ]; then echo "go"
    elif [ -f "Cargo.toml" ]; then echo "rust"
    elif [ -f "package.json" ]; then echo "typescript"
    elif [ -f "pyproject.toml" ] || [ -f "setup.py" ] || [ -f "requirements.txt" ]; then echo "python"
    elif [ -f "Makefile" ]; then echo "make"
    else echo "unknown"
    fi
}

LANG="$(detect_language)"
echo "=== Auto Quality Gate ==="
echo "Language: ${LANG}"
echo "Root: ${REPO_ROOT}"
echo ""

# --- Step 1: Format ---
echo "--- Step 1: Format Check ---"
case "$LANG" in
    go)
        if command -v gofmt >/dev/null 2>&1; then
            UNFORMATTED=$(gofmt -l . 2>/dev/null | head -20)
            if [ -n "$UNFORMATTED" ]; then
                FAILURES+=("FORMAT: Unformatted Go files: ${UNFORMATTED}")
                echo "FAIL: Unformatted files found"
            else
                echo "PASS: All Go files formatted"
            fi
        else
            WARNINGS+=("FORMAT: gofmt not found")
            echo "SKIP: gofmt not available"
        fi
        ;;
    rust)
        if command -v cargo >/dev/null 2>&1; then
            if ! cargo fmt --check 2>/dev/null; then
                FAILURES+=("FORMAT: Rust formatting check failed")
                echo "FAIL: cargo fmt --check failed"
            else
                echo "PASS: Rust files formatted"
            fi
        fi
        ;;
    typescript)
        if [ -f "node_modules/.bin/prettier" ]; then
            if ! npx prettier --check "src/**/*.{ts,tsx}" 2>/dev/null; then
                FAILURES+=("FORMAT: Prettier check failed")
                echo "FAIL: prettier --check failed"
            else
                echo "PASS: TypeScript files formatted"
            fi
        elif [ -f "Makefile" ] && grep -q "^fmt:" Makefile 2>/dev/null; then
            echo "SKIP: Using Makefile fmt target (run separately)"
        else
            echo "SKIP: No formatter configured"
        fi
        ;;
    python)
        if command -v black >/dev/null 2>&1; then
            if ! black --check . 2>/dev/null; then
                FAILURES+=("FORMAT: Black formatting check failed")
                echo "FAIL: black --check failed"
            else
                echo "PASS: Python files formatted"
            fi
        fi
        ;;
    make)
        if grep -q "^fmt:" Makefile 2>/dev/null; then
            if ! make fmt 2>&1; then
                FAILURES+=("FORMAT: make fmt failed")
                echo "FAIL: make fmt failed"
            else
                echo "PASS: make fmt succeeded"
            fi
        fi
        ;;
    *)
        echo "SKIP: Unknown language, no formatter configured"
        ;;
esac
echo ""

# --- Step 2: Lint ---
echo "--- Step 2: Lint Check ---"
case "$LANG" in
    go)
        if command -v golangci-lint >/dev/null 2>&1; then
            if ! golangci-lint run --timeout 120s ./... 2>&1; then
                FAILURES+=("LINT: golangci-lint failed")
                echo "FAIL: golangci-lint found issues"
            else
                echo "PASS: golangci-lint clean"
            fi
        elif command -v go >/dev/null 2>&1; then
            if ! go vet ./... 2>&1; then
                FAILURES+=("LINT: go vet failed")
                echo "FAIL: go vet found issues"
            else
                echo "PASS: go vet clean"
            fi
        fi
        ;;
    rust)
        if command -v cargo >/dev/null 2>&1; then
            if ! cargo clippy -- -D warnings 2>&1; then
                FAILURES+=("LINT: cargo clippy failed")
                echo "FAIL: clippy found issues"
            else
                echo "PASS: clippy clean"
            fi
        fi
        ;;
    typescript)
        if [ -f "node_modules/.bin/eslint" ]; then
            if ! npx eslint src/ 2>&1; then
                FAILURES+=("LINT: ESLint found issues")
                echo "FAIL: ESLint failed"
            else
                echo "PASS: ESLint clean"
            fi
        fi
        ;;
    python)
        if command -v ruff >/dev/null 2>&1; then
            if ! ruff check . 2>&1; then
                FAILURES+=("LINT: ruff found issues")
                echo "FAIL: ruff check failed"
            else
                echo "PASS: ruff clean"
            fi
        fi
        ;;
    make)
        if grep -q "^lint:" Makefile 2>/dev/null; then
            if ! make lint 2>&1; then
                FAILURES+=("LINT: make lint failed")
                echo "FAIL: make lint failed"
            else
                echo "PASS: make lint succeeded"
            fi
        fi
        ;;
    *)
        echo "SKIP: No linter configured"
        ;;
esac
echo ""

# --- Step 3: Test ---
echo "--- Step 3: Test Suite ---"
case "$LANG" in
    go)
        if ! go test ./... 2>&1; then
            FAILURES+=("TEST: go test ./... failed")
            echo "FAIL: Tests failed"
        else
            echo "PASS: All tests pass"
        fi
        ;;
    rust)
        if ! cargo test 2>&1; then
            FAILURES+=("TEST: cargo test failed")
            echo "FAIL: Tests failed"
        else
            echo "PASS: All tests pass"
        fi
        ;;
    typescript)
        if [ -f "package.json" ] && grep -q '"test"' package.json 2>/dev/null; then
            if ! npm test 2>&1; then
                FAILURES+=("TEST: npm test failed")
                echo "FAIL: Tests failed"
            else
                echo "PASS: All tests pass"
            fi
        fi
        ;;
    python)
        if ! pytest 2>&1; then
            FAILURES+=("TEST: pytest failed")
            echo "FAIL: Tests failed"
        else
            echo "PASS: All tests pass"
        fi
        ;;
    make)
        if grep -q "^test:" Makefile 2>/dev/null; then
            if ! make test 2>&1; then
                FAILURES+=("TEST: make test failed")
                echo "FAIL: make test failed"
            else
                echo "PASS: make test succeeded"
            fi
        fi
        ;;
    *)
        echo "SKIP: No test framework detected"
        ;;
esac
echo ""

# --- Step 4: Diff Check ---
echo "--- Step 4: Diff Check ---"
if git diff --check 2>/dev/null; then
    echo "PASS: No whitespace errors"
else
    WARNINGS+=("DIFF: Whitespace errors detected")
    echo "WARN: Whitespace errors in diff"
fi
echo ""

# --- Report ---
echo "=== Quality Gate Report ==="
if [ ${#WARNINGS[@]} -gt 0 ]; then
    echo "Warnings:"
    for w in "${WARNINGS[@]}"; do
        echo "  ! $w"
    done
fi

if [ ${#FAILURES[@]} -gt 0 ]; then
    echo "Failures:"
    for f in "${FAILURES[@]}"; do
        echo "  x $f"
    done
    echo ""
    echo "RESULT: FAIL (${#FAILURES[@]} failures)"
    echo ""
    echo "Self-correction hint: Fix the failures above and re-run the quality gate."
    exit 1
else
    echo "RESULT: PASS"
    echo "All quality checks passed."
    exit 0
fi
