#!/usr/bin/env bash
#
# bench-gemma4-suite.sh - Orchestrate repeatable Gemma benchmark suites
#
# This wraps scripts/bench-model.sh to make Gemma-focused benchmarking easier:
# - fast vs long profile comparison
# - warm vs cold run comparison
# - prompt-length matrix (short / medium / long by default)
# - one combined JSON artifact that points to every child run
#
# The suite does not mutate cluster state on its own. For true cold runs, pass a
# hook that unloads or restarts the target model between cases.
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHILD_BENCH="${ROOT_DIR}/scripts/bench-model.sh"

MODEL="${MODEL:-gemma4-e4b-turboquant}"
ENDPOINT="${ENDPOINT:-http://localhost:8080}"
DIRECT="${DIRECT:-0}"
TIMEOUT="${TIMEOUT:-180}"
REPORT_DIR="${REPORT_DIR:-/tmp}"

PROFILES_CSV="${PROFILES:-fast,long}"
STATES_CSV="${STATES:-warm,cold}"
PHASES_CSV="${PHASES:-short,medium,long}"
EXTRA_PHASES_CSV="${EXTRA_PHASES:-}"

FAST_ITERATIONS="${FAST_ITERATIONS:-2}"
LONG_ITERATIONS="${LONG_ITERATIONS:-4}"
FAST_MAX_TOKENS="${FAST_MAX_TOKENS:-128}"
LONG_MAX_TOKENS="${LONG_MAX_TOKENS:-512}"
FAST_WARMUP="${FAST_WARMUP:-1}"
LONG_WARMUP="${LONG_WARMUP:-1}"

COLD_HOOK="${COLD_HOOK:-}"
WARM_HOOK="${WARM_HOOK:-}"
DRY_RUN=0

SUITE_TS="$(date +%Y%m%dT%H%M%S)"
SUITE_RAND="$(openssl rand -hex 3)"
SUITE_ID="gemma4-suite-${SUITE_TS}-${SUITE_RAND}"
GIT_SHA="$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo "unknown")"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${CYAN}[gemma-bench]${NC} $*"; }
warn() { echo -e "${YELLOW}[gemma-bench]${NC} $*"; }
err()  { echo -e "${RED}[gemma-bench]${NC} $*" >&2; }
ok()   { echo -e "${GREEN}[gemma-bench]${NC} $*"; }

usage() {
    cat <<'EOF'
Usage: ./scripts/bench-gemma4-suite.sh [options]

Options:
  --model NAME              Routed model name (default: gemma4-e4b-turboquant)
  --endpoint URL            Base endpoint (default: http://localhost:8080)
  --profiles CSV            Profiles to run: fast,long (default: fast,long)
  --states CSV              Thermal states: warm,cold (default: warm,cold)
  --phases CSV              Prompt phases: short,medium,long (default: short,medium,long)
  --extra-phases CSV        Optional phases to append, e.g. multiturn,stream
  --report-dir DIR          Artifact root (default: /tmp)
  --direct                  Use DIRECT=1 against /v1/chat/completions
  --cold-hook CMD           Command run before each cold case
  --warm-hook CMD           Command run before each warm case
  --dry-run                 Print planned cases without running them
  -h, --help                Show this help

Environment overrides:
  FAST_ITERATIONS / LONG_ITERATIONS
  FAST_MAX_TOKENS / LONG_MAX_TOKENS
  FAST_WARMUP / LONG_WARMUP
  TIMEOUT

Examples:
  ./scripts/bench-gemma4-suite.sh
  ./scripts/bench-gemma4-suite.sh --profiles fast --states warm
  ./scripts/bench-gemma4-suite.sh --extra-phases multiturn,stream
  COLD_HOOK='kubectl -n flexinfer-system rollout restart deploy/my-model' \
    ./scripts/bench-gemma4-suite.sh --states cold
EOF
}

require_cmd() {
    local cmd="$1"
    if ! command -v "$cmd" >/dev/null 2>&1; then
        err "Required command not found: ${cmd}"
        exit 1
    fi
}

slugify() {
    echo "$1" | tr '/: @' '-----' | tr -cd '[:alnum:]._-\n'
}

split_csv() {
    local csv="$1"
    local outvar="$2"
    local IFS=','
    local items=()
    read -r -a items <<< "$csv"
    eval "$outvar=(\"\${items[@]}\")"
}

append_unique() {
    local item="$1"
    shift
    local existing
    for existing in "$@"; do
        if [[ "$existing" == "$item" ]]; then
            return 1
        fi
    done
    return 0
}

profile_iterations() {
    case "$1" in
        fast) echo "$FAST_ITERATIONS" ;;
        long) echo "$LONG_ITERATIONS" ;;
        *) err "Unknown profile: $1"; exit 1 ;;
    esac
}

profile_max_tokens() {
    case "$1" in
        fast) echo "$FAST_MAX_TOKENS" ;;
        long) echo "$LONG_MAX_TOKENS" ;;
        *) err "Unknown profile: $1"; exit 1 ;;
    esac
}

profile_warmup() {
    case "$1" in
        fast) echo "$FAST_WARMUP" ;;
        long) echo "$LONG_WARMUP" ;;
        *) err "Unknown profile: $1"; exit 1 ;;
    esac
}

validate_value() {
    local kind="$1"
    local value="$2"
    case "$kind" in
        profile)
            case "$value" in fast|long) ;; *) err "Invalid profile: ${value}"; exit 1 ;; esac
            ;;
        state)
            case "$value" in warm|cold) ;; *) err "Invalid state: ${value}"; exit 1 ;; esac
            ;;
        phase)
            case "$value" in short|medium|long|multiturn|stream) ;; *) err "Invalid phase: ${value}"; exit 1 ;; esac
            ;;
        *)
            err "Unknown validation kind: ${kind}"
            exit 1
            ;;
    esac
}

run_hook() {
    local hook="$1"
    local profile="$2"
    local state="$3"
    local phase="$4"
    local case_dir="$5"

    if [[ -z "$hook" ]]; then
        return 0
    fi

    log "Running ${state} hook for ${profile}/${phase}"
    CASE_PROFILE="$profile" \
    CASE_STATE="$state" \
    CASE_PHASE="$phase" \
    CASE_DIR="$case_dir" \
    MODEL="$MODEL" \
    ENDPOINT="$ENDPOINT" \
    DIRECT="$DIRECT" \
    bash -lc "$hook"
}

collect_case_entry() {
    local profile="$1"
    local state="$2"
    local phase="$3"
    local case_id="$4"
    local case_dir="$5"
    local iterations="$6"
    local warmup="$7"
    local max_tokens="$8"
    local exit_code="$9"
    local status="${10}"
    local stdout_log="${11}"
    local note="${12:-}"

    local report_file report_json summary_json child_run_id
    report_file="$(find "$case_dir" -maxdepth 1 -type f -name 'bench-model-*.json' | sort | tail -1)"
    report_json='null'
    summary_json='null'
    child_run_id='null'

    if [[ -n "$report_file" ]]; then
        report_json="$(jq -c '.' "$report_file")"
        summary_json="$(jq -c --arg phase "$phase" '.summaries[]? | select(.phase == $phase)' "$report_file" | head -1)"
        child_run_id="$(jq -r '.run_id // empty' "$report_file")"
        if [[ -z "$summary_json" ]]; then
            summary_json='null'
        fi
        if [[ -z "$child_run_id" ]]; then
            child_run_id='null'
        else
            child_run_id="$(jq -Rn --arg v "$child_run_id" '$v')"
        fi
    fi

    jq -n \
        --arg case_id "$case_id" \
        --arg profile "$profile" \
        --arg state "$state" \
        --arg phase "$phase" \
        --arg status "$status" \
        --arg endpoint "$ENDPOINT" \
        --arg model "$MODEL" \
        --arg stdout_log "$stdout_log" \
        --arg report_path "${report_file:-}" \
        --arg note "$note" \
        --argjson direct "$DIRECT" \
        --argjson iterations "$iterations" \
        --argjson warmup "$warmup" \
        --argjson max_tokens "$max_tokens" \
        --argjson timeout "$TIMEOUT" \
        --argjson exit_code "$exit_code" \
        --argjson child_run_id "$child_run_id" \
        --argjson summary "$summary_json" \
        --argjson report "$report_json" \
        '{
            case_id: $case_id,
            profile: $profile,
            state: $state,
            phase: $phase,
            status: $status,
            exit_code: $exit_code,
            endpoint: $endpoint,
            model: $model,
            direct: $direct,
            config: {
                iterations: $iterations,
                warmup: $warmup,
                max_tokens: $max_tokens,
                timeout: $timeout
            },
            artifacts: {
                stdout_log: $stdout_log,
                report_path: $report_path
            },
            note: $note,
            child_run_id: $child_run_id,
            summary: $summary,
            report: $report
        }'
}

print_plan() {
    local profile state phase iterations warmup max_tokens
    echo ""
    log "${BOLD}Planned suite${NC}"
    printf "  %-6s %-5s %-10s %10s %8s %11s\n" "Profile" "State" "Phase" "Iterations" "Warmup" "MaxTokens"
    printf "  %-6s %-5s %-10s %10s %8s %11s\n" "------" "-----" "----------" "----------" "------" "-----------"
    for profile in "${PROFILES[@]}"; do
        for state in "${STATES[@]}"; do
            iterations="$(profile_iterations "$profile")"
            warmup="$(profile_warmup "$profile")"
            max_tokens="$(profile_max_tokens "$profile")"
            if [[ "$state" == "cold" ]]; then
                warmup=0
            fi
            for phase in "${PHASES[@]}"; do
                printf "  %-6s %-5s %-10s %10s %8s %11s\n" \
                    "$profile" "$state" "$phase" "$iterations" "$warmup" "$max_tokens"
            done
        done
    done
    echo ""
}

run_case() {
    local profile="$1"
    local state="$2"
    local phase="$3"

    local iterations warmup max_tokens case_id case_dir stdout_log hook note
    iterations="$(profile_iterations "$profile")"
    warmup="$(profile_warmup "$profile")"
    max_tokens="$(profile_max_tokens "$profile")"
    note=""

    if [[ "$state" == "cold" ]]; then
        warmup=0
        hook="$COLD_HOOK"
        if [[ -z "$hook" ]]; then
            note="No cold hook configured; result is only cold relative to suite warmup behavior."
            warn "$note"
        fi
    else
        hook="$WARM_HOOK"
    fi

    case_id="${profile}-${state}-${phase}"
    case_dir="${SUITE_DIR}/${case_id}"
    stdout_log="${case_dir}/stdout.log"
    mkdir -p "$case_dir"

    run_hook "$hook" "$profile" "$state" "$phase" "$case_dir"

    log "${BOLD}Case ${case_id}${NC} — iter=${iterations} warmup=${warmup} max_tokens=${max_tokens}"

    set +e
    (
        cd "$ROOT_DIR"
        env \
            MODEL="$MODEL" \
            ENDPOINT="$ENDPOINT" \
            DIRECT="$DIRECT" \
            ITERATIONS="$iterations" \
            WARMUP="$warmup" \
            MAX_TOKENS="$max_tokens" \
            TIMEOUT="$TIMEOUT" \
            REPORT_DIR="$case_dir" \
            "$CHILD_BENCH" "$phase"
    ) | tee "$stdout_log"
    local bench_exit="${PIPESTATUS[0]}"
    set -e

    local status="success"
    if [[ "$bench_exit" -ne 0 ]]; then
        status="failed"
        warn "Case ${case_id} failed with exit code ${bench_exit}"
    else
        ok "Case ${case_id} completed"
    fi

    collect_case_entry \
        "$profile" "$state" "$phase" "$case_id" "$case_dir" \
        "$iterations" "$warmup" "$max_tokens" "$bench_exit" "$status" \
        "$stdout_log" "$note" >> "$CASES_JSONL"
}

write_suite_report() {
    local cases_json
    cases_json="$(jq -s '.' "$CASES_JSONL")"

    jq -n \
        --arg suite_id "$SUITE_ID" \
        --arg timestamp "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
        --arg git_sha "$GIT_SHA" \
        --arg model "$MODEL" \
        --arg endpoint "$ENDPOINT" \
        --arg report_dir "$SUITE_DIR" \
        --arg cold_hook "$COLD_HOOK" \
        --arg warm_hook "$WARM_HOOK" \
        --argjson direct "$DIRECT" \
        --argjson profiles "$(printf '%s\n' "${PROFILES[@]}" | jq -R . | jq -s .)" \
        --argjson states "$(printf '%s\n' "${STATES[@]}" | jq -R . | jq -s .)" \
        --argjson phases "$(printf '%s\n' "${PHASES[@]}" | jq -R . | jq -s .)" \
        --argjson cases "$cases_json" \
        --argjson fast_iterations "$FAST_ITERATIONS" \
        --argjson long_iterations "$LONG_ITERATIONS" \
        --argjson fast_max_tokens "$FAST_MAX_TOKENS" \
        --argjson long_max_tokens "$LONG_MAX_TOKENS" \
        --argjson fast_warmup "$FAST_WARMUP" \
        --argjson long_warmup "$LONG_WARMUP" \
        'def case_summary:
            .cases
            | map(select(.summary != null and .status == "success"));
         def warm_cold:
            [case_summary[]
             | {profile, phase, state, gen_tps: (.summary.gen_tps_avg // 0), prompt_tps: (.summary.prompt_tps_avg // 0), wall_s: (.summary.wall_time_avg // 0)}]
            | group_by(.profile + "|" + .phase)
            | map(select(length >= 2))
            | map(
                (map(select(.state == "warm")) | .[0]) as $warm |
                (map(select(.state == "cold")) | .[0]) as $cold |
                select($warm != null and $cold != null) |
                {
                    profile: $warm.profile,
                    phase: $warm.phase,
                    warm: $warm,
                    cold: $cold,
                    gen_tps_delta: (($warm.gen_tps - $cold.gen_tps) * 100 | round / 100),
                    prompt_tps_delta: (($warm.prompt_tps - $cold.prompt_tps) * 100 | round / 100),
                    wall_s_delta: (($warm.wall_s - $cold.wall_s) * 100 | round / 100)
                }
            );
         def fast_long:
            [case_summary[]
             | {profile, phase, state, gen_tps: (.summary.gen_tps_avg // 0), prompt_tps: (.summary.prompt_tps_avg // 0), wall_s: (.summary.wall_time_avg // 0)}]
            | group_by(.state + "|" + .phase)
            | map(select(length >= 2))
            | map(
                (map(select(.profile == "fast")) | .[0]) as $fast |
                (map(select(.profile == "long")) | .[0]) as $long |
                select($fast != null and $long != null) |
                {
                    state: $fast.state,
                    phase: $fast.phase,
                    fast: $fast,
                    long: $long,
                    gen_tps_delta: (($fast.gen_tps - $long.gen_tps) * 100 | round / 100),
                    prompt_tps_delta: (($fast.prompt_tps - $long.prompt_tps) * 100 | round / 100),
                    wall_s_delta: (($fast.wall_s - $long.wall_s) * 100 | round / 100)
                }
            );
         {
            suite_run_id: $suite_id,
            timestamp: $timestamp,
            git_sha: $git_sha,
            model: $model,
            endpoint: $endpoint,
            direct: $direct,
            suite_dir: $report_dir,
            config: {
                profiles: $profiles,
                states: $states,
                phases: $phases,
                profile_defaults: {
                    fast: {
                        iterations: $fast_iterations,
                        max_tokens: $fast_max_tokens,
                        warmup: $fast_warmup
                    },
                    long: {
                        iterations: $long_iterations,
                        max_tokens: $long_max_tokens,
                        warmup: $long_warmup
                    }
                },
                cold_hook_configured: ($cold_hook != ""),
                warm_hook_configured: ($warm_hook != "")
            },
            comparisons: {
                warm_vs_cold: warm_cold,
                fast_vs_long: fast_long
            },
            cases: $cases
         }' > "$SUITE_REPORT_JSON"
}

print_final_summary() {
    echo ""
    log "${BOLD}Suite summary${NC}"
    printf "  %-18s %-8s %-10s %10s %10s %10s\n" "Case" "Status" "Phase" "PP tok/s" "Gen tok/s" "Wall(s)"
    printf "  %-18s %-8s %-10s %10s %10s %10s\n" "------------------" "--------" "----------" "----------" "----------" "----------"

    jq -r '
        .cases[]
        | [
            .case_id,
            .status,
            .phase,
            (.summary.prompt_tps_avg // 0),
            (.summary.gen_tps_avg // 0),
            (.summary.wall_time_avg // 0)
          ]
        | @tsv
    ' "$SUITE_REPORT_JSON" | while IFS=$'\t' read -r case_id status phase prompt_tps gen_tps wall_s; do
        printf "  %-18s %-8s %-10s %10s %10s %10s\n" \
            "$case_id" "$status" "$phase" "$prompt_tps" "$gen_tps" "$wall_s"
    done

    echo ""
    ok "Suite JSON: ${SUITE_REPORT_JSON}"
    ok "Artifacts:  ${SUITE_DIR}"
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --model)
                MODEL="$2"
                shift 2
                ;;
            --endpoint)
                ENDPOINT="$2"
                shift 2
                ;;
            --profiles)
                PROFILES_CSV="$2"
                shift 2
                ;;
            --states)
                STATES_CSV="$2"
                shift 2
                ;;
            --phases)
                PHASES_CSV="$2"
                shift 2
                ;;
            --extra-phases)
                EXTRA_PHASES_CSV="$2"
                shift 2
                ;;
            --report-dir)
                REPORT_DIR="$2"
                shift 2
                ;;
            --cold-hook)
                COLD_HOOK="$2"
                shift 2
                ;;
            --warm-hook)
                WARM_HOOK="$2"
                shift 2
                ;;
            --direct)
                DIRECT=1
                shift
                ;;
            --dry-run)
                DRY_RUN=1
                shift
                ;;
            -h|--help)
                usage
                exit 0
                ;;
            *)
                err "Unknown argument: $1"
                usage
                exit 1
                ;;
        esac
    done
}

main() {
    parse_args "$@"

    require_cmd jq
    require_cmd curl
    require_cmd openssl
    require_cmd tee

    if [[ ! -x "$CHILD_BENCH" ]]; then
        err "Expected executable child benchmark script at ${CHILD_BENCH}"
        exit 1
    fi

    split_csv "$PROFILES_CSV" PROFILES
    split_csv "$STATES_CSV" STATES
    split_csv "$PHASES_CSV" PHASES

    if [[ -n "$EXTRA_PHASES_CSV" ]]; then
        split_csv "$EXTRA_PHASES_CSV" EXTRA_PHASES
        local extra
        for extra in "${EXTRA_PHASES[@]}"; do
            validate_value phase "$extra"
            if append_unique "$extra" "${PHASES[@]}"; then
                PHASES+=("$extra")
            fi
        done
    fi

    local profile state phase
    for profile in "${PROFILES[@]}"; do
        validate_value profile "$profile"
    done
    for state in "${STATES[@]}"; do
        validate_value state "$state"
    done
    for phase in "${PHASES[@]}"; do
        validate_value phase "$phase"
    done

    SUITE_DIR="${REPORT_DIR}/$(slugify "${MODEL}")-${SUITE_ID}"
    SUITE_REPORT_JSON="${SUITE_DIR}/suite.json"
    CASES_JSONL="${SUITE_DIR}/cases.jsonl"

    mkdir -p "$SUITE_DIR"
    : > "$CASES_JSONL"

    log "Gemma benchmark suite"
    log "Suite ID:  ${SUITE_ID}"
    log "Git SHA:   ${GIT_SHA}"
    log "Model:     ${MODEL}"
    log "Endpoint:  ${ENDPOINT}"
    log "Profiles:  ${PROFILES_CSV}"
    log "States:    ${STATES_CSV}"
    log "Phases:    $(IFS=, ; echo "${PHASES[*]}")"
    log "Artifacts: ${SUITE_DIR}"

    print_plan

    if [[ "$DRY_RUN" -eq 1 ]]; then
        log "Dry run only; no benchmark cases executed."
        exit 0
    fi

    for profile in "${PROFILES[@]}"; do
        for state in "${STATES[@]}"; do
            for phase in "${PHASES[@]}"; do
                run_case "$profile" "$state" "$phase"
                echo ""
            done
        done
    done

    write_suite_report
    print_final_summary
}

main "$@"
