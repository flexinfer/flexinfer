#!/usr/bin/env bash
#
# probe-gemma4-long-context.sh - Verify Gemma4 26B long-context readiness.
#
# This script exercises an OpenAI-compatible chat endpoint with three checks:
# - short sanity: simple arithmetic/chat response
# - medium prompt: long-context retention with a medium repeated-token prompt
# - long prompt: long-context retention with a default ~30k-token prompt
#
# It records run metadata, prompt/completion tokens, elapsed time, and optional
# Kubernetes pod/image/log hints into JSON and Markdown artifacts. The probe
# exits nonzero when the model returns obvious garbage or repetition, or when
# the required marker/output check fails.
#
# Typical usage:
#   kubectl -n ai port-forward svc/litellm 18000:8000
#   ENDPOINT=http://127.0.0.1:18000 ./scripts/probe-gemma4-long-context.sh
#
#   ENDPOINT=http://litellm.ai.svc.cluster.local:8000 \
#   AUTH_TOKEN=sk-litellm-master-key \
#   MODEL=gemma4-26b-a4b-gptq \
#   ./scripts/probe-gemma4-long-context.sh
#
# Environment:
#   ENDPOINT          OpenAI-compatible base URL (default: http://127.0.0.1:18000)
#   OPENAI_BASE_URL   Alternative base URL alias
#   AUTH_TOKEN        Bearer token (default: sk-litellm-master-key)
#   OPENAI_API_KEY    Alternative bearer token alias
#   MODEL             Model name to probe (default: gemma4-26b-a4b-gptq)
#   MODEL_IMAGE       Optional model/backend image name to record
#   SHORT_MAX_TOKENS  Short sanity completion cap (default: 8)
#   MEDIUM_REPEAT     Medium filler repeat count (default: 2000)
#   MEDIUM_MAX_TOKENS Medium completion cap (default: 8)
#   LONG_REPEAT       Long filler repeat count (~30k tokens by default, default: 6000)
#   LONG_MAX_TOKENS   Long completion cap (default: 8)
#   FILLER_SEQUENCE    Repeated filler token block (default: alpha beta gamma delta epsilon)
#   TIMEOUT           Per-request timeout seconds (default: 1800)
#   REPORT_DIR        Artifact root (default: /tmp)
#   POD_NAME          Optional Kubernetes pod name for metadata/log hints
#   POD_SELECTOR      Optional kubectl label selector to discover the probe pod
#   KUBE_NAMESPACE    Namespace for pod discovery/log hints (default: flexinfer-system)
#   CONTAINER_NAME    Optional log container name override
#   KUBECTL_BIN       kubectl binary path (default: kubectl)
#   LOG_TAIL          Log tail lines when hints are collected (default: 200)
#
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

ENDPOINT="${ENDPOINT:-${OPENAI_BASE_URL:-http://127.0.0.1:18000}}"
AUTH_TOKEN="${AUTH_TOKEN:-${OPENAI_API_KEY:-sk-litellm-master-key}}"
MODEL="${MODEL:-gemma4-26b-a4b-gptq}"
MODEL_IMAGE="${MODEL_IMAGE:-${IMAGE:-}}"
SHORT_MAX_TOKENS="${SHORT_MAX_TOKENS:-8}"
MEDIUM_REPEAT="${MEDIUM_REPEAT:-2000}"
MEDIUM_MAX_TOKENS="${MEDIUM_MAX_TOKENS:-8}"
LONG_REPEAT="${LONG_REPEAT:-6000}"
LONG_MAX_TOKENS="${LONG_MAX_TOKENS:-8}"
TEMPERATURE="${TEMPERATURE:-0}"
TIMEOUT="${TIMEOUT:-1800}"
REPORT_DIR="${REPORT_DIR:-/tmp}"
FILLER_SEQUENCE="${FILLER_SEQUENCE:-alpha beta gamma delta epsilon}"

POD_NAME="${POD_NAME:-}"
POD_SELECTOR="${POD_SELECTOR:-}"
KUBE_NAMESPACE="${KUBE_NAMESPACE:-flexinfer-system}"
CONTAINER_NAME="${CONTAINER_NAME:-}"
KUBECTL_BIN="${KUBECTL_BIN:-kubectl}"
LOG_TAIL="${LOG_TAIL:-200}"

DRY_RUN=0

RUN_TS="$(date +%Y%m%dT%H%M%S)"
RUN_RAND="$(openssl rand -hex 3)"
RUN_ID="gemma4-long-context-${RUN_TS}-${RUN_RAND}"
ARTIFACT_DIR="${REPORT_DIR%/}/${RUN_ID}"
RAW_DIR="${ARTIFACT_DIR}/raw"
LOG_DIR="${ARTIFACT_DIR}/logs"
REPORT_JSON="${ARTIFACT_DIR}/${RUN_ID}.json"
REPORT_MD="${ARTIFACT_DIR}/${RUN_ID}.md"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${CYAN}[gemma-probe]${NC} $*"; }
warn() { echo -e "${YELLOW}[gemma-probe]${NC} $*"; }
err()  { echo -e "${RED}[gemma-probe]${NC} $*" >&2; }
ok()   { echo -e "${GREEN}[gemma-probe]${NC} $*"; }

usage() {
    cat <<'EOF'
Usage: ./scripts/probe-gemma4-long-context.sh [options]

Options:
  --endpoint URL            OpenAI-compatible base URL
  --auth-token TOKEN        Bearer token
  --model NAME              Model name to probe
  --medium-repeat N         Medium prompt repeat count
  --long-repeat N           Long prompt repeat count
  --short-max-tokens N      Short sanity completion cap
  --medium-max-tokens N     Medium completion cap
  --long-max-tokens N       Long completion cap
  --report-dir DIR         Artifact root (default: /tmp)
  --pod-name NAME          Kubernetes pod name for metadata/log hints
  --pod-selector SELECTOR  kubectl label selector to discover the pod
  --namespace NS           Namespace for pod discovery/log hints
  --container NAME         Log container name override
  --dry-run                Show planned checks and exit
  -h, --help               Show this help

Environment aliases:
  OPENAI_BASE_URL, OPENAI_API_KEY

Examples:
  kubectl -n ai port-forward svc/litellm 18000:8000
  ENDPOINT=http://127.0.0.1:18000 ./scripts/probe-gemma4-long-context.sh

  ENDPOINT=http://litellm.ai.svc.cluster.local:8000 \
    AUTH_TOKEN=sk-litellm-master-key \
    MODEL=gemma4-26b-a4b-gptq \
    POD_SELECTOR='app=gemma4-26b-a4b' \
    ./scripts/probe-gemma4-long-context.sh
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

short_prompt() {
    cat <<'EOF'
What is 2 + 2? Answer with only the number.
EOF
}

long_prompt() {
    local repeat_count="$1"
    local marker="$2"
    local label="$3"
    python3 - "$repeat_count" "$marker" "$label" "$FILLER_SEQUENCE" <<'PY'
import sys

repeat_count = int(sys.argv[1])
marker = sys.argv[2]
label = sys.argv[3]
filler = sys.argv[4]

filler_block = (filler.strip() + " ") * repeat_count
print(
    f"You are verifying long-context retention for {label}.\n"
    f"Remember this verification code exactly:\n"
    f"VERIFICATION CODE: {marker}\n\n"
    f"Read the full filler block below before answering.\n\n"
    f"{filler_block}\n"
    f"Now answer with the verification code only."
)
PY
}

build_payload() {
    local prompt_text="$1"
    local max_tokens="$2"
    jq -nc \
        --arg model "$MODEL" \
        --arg prompt "$prompt_text" \
        --argjson max_tokens "$max_tokens" \
        --argjson temperature "$TEMPERATURE" \
        '{
            model: $model,
            messages: [
                {role: "system", content: "Return only the requested answer."},
                {role: "user", content: $prompt}
            ],
            temperature: $temperature,
            max_tokens: $max_tokens,
            stream: false
        }'
}

request_case() {
    local case_name="$1"
    local prompt_text="$2"
    local expected_kind="$3"
    local expected_value="$4"
    local max_tokens="$5"

    local slug body_file stats_file payload_file
    slug="$(slugify "$case_name")"
    body_file="${RAW_DIR}/${slug}.response.json"
    stats_file="${RAW_DIR}/${slug}.curl.txt"
    payload_file="${RAW_DIR}/${slug}.request.json"

    build_payload "$prompt_text" "$max_tokens" >"$payload_file"

    set +e
    curl -sS --max-time "$TIMEOUT" \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        -H "Content-Type: application/json" \
        -o "$body_file" \
        -w '%{http_code} %{time_total}' \
        -d @"$payload_file" \
        "${ENDPOINT%/}/v1/chat/completions" >"$stats_file"
    set -e

    local result_json
    result_json="$(
        python3 - "$case_name" "$expected_kind" "$expected_value" "$body_file" "$stats_file" "$prompt_text" "$MODEL" "$max_tokens" <<'PY'
import collections
import json
import pathlib
import re
import sys

case_name = sys.argv[1]
expected_kind = sys.argv[2]
expected_value = sys.argv[3]
body_path = pathlib.Path(sys.argv[4])
stats_path = pathlib.Path(sys.argv[5])
prompt_text = sys.argv[6]
model = sys.argv[7]
max_tokens = int(sys.argv[8])

stats_text = stats_path.read_text().strip() if stats_path.exists() else ""
stats_parts = stats_text.split()
http_code = int(stats_parts[0]) if len(stats_parts) > 0 and stats_parts[0].isdigit() else 0
elapsed = float(stats_parts[1]) if len(stats_parts) > 1 else 0.0

result = {
    "case_name": case_name,
    "expected_kind": expected_kind,
    "expected_value": expected_value,
    "model_requested": model,
    "max_tokens_requested": max_tokens,
    "http_code": http_code,
    "elapsed_s": round(elapsed, 3),
    "request_prompt_tokens_estimate": len(prompt_text.split()),
}

body = None
if body_path.exists():
    raw = body_path.read_text()
    if raw.strip():
        try:
            body = json.loads(raw)
        except Exception as exc:
            result["status"] = "invalid_json"
            result["error"] = f"invalid_json: {exc}"
            result["content_preview"] = raw[:240]
            print(json.dumps(result, separators=(",", ":")))
            sys.exit(0)

if http_code != 200 or body is None:
    result["status"] = "request_failed"
    if body is not None:
        result["error"] = body
    elif body_path.exists():
        result["error"] = body_path.read_text()[:400]
    print(json.dumps(result, separators=(",", ":")))
    sys.exit(0)

usage = body.get("usage", {})
choices = body.get("choices", [])
content = ""
if choices:
    message = choices[0].get("message", {})
    content = message.get("content", "") or ""

result["status"] = "ok"
result["model_returned"] = body.get("model", "")
result["prompt_tokens"] = usage.get("prompt_tokens", 0)
result["completion_tokens"] = usage.get("completion_tokens", 0)
result["total_tokens"] = usage.get("total_tokens", result["prompt_tokens"] + result["completion_tokens"])
result["content_preview"] = content[:240]
result["content"] = content

tokens = re.findall(r"[A-Za-z0-9_']+|[^\s]", content.lower())
normalized = re.sub(r"\s+", " ", content.strip())
normalized_lower = normalized.lower()
result["token_count"] = len(tokens)

repetition_reasons = []
if content.strip():
    if len(tokens) >= 8:
        counts = collections.Counter(tokens)
        top_count = counts.most_common(1)[0][1]
        unique_ratio = len(counts) / len(tokens)
        longest_run = 1
        current_run = 1
        for idx in range(1, len(tokens)):
            if tokens[idx] == tokens[idx - 1]:
                current_run += 1
            else:
                longest_run = max(longest_run, current_run)
                current_run = 1
        longest_run = max(longest_run, current_run)
        if top_count / len(tokens) >= 0.45 and len(tokens) >= 16:
            repetition_reasons.append("top_token_dominates")
        if unique_ratio <= 0.35 and len(tokens) >= 20:
            repetition_reasons.append("low_unique_ratio")
        if longest_run >= 6:
            repetition_reasons.append("long_token_run")
    if re.search(r"(\b\w+\b(?:\s+\b\w+\b){0,3})(?:\s+\1){3,}", normalized_lower):
        repetition_reasons.append("repeated_phrase")

refusal_markers = [
    "i can't",
    "i cannot",
    "cannot help",
    "unable to",
    "as an ai",
    "i'm unable",
]
refusal_detected = any(marker in normalized_lower for marker in refusal_markers)

expected_ok = False
if expected_kind == "arithmetic":
    expected_ok = bool(re.search(r"(?<!\d)4(?!\d)", normalized))
elif expected_kind == "marker":
    cleaned = normalized.strip("`'\".,:;!? ")
    expected_ok = cleaned == expected_value or re.search(rf"(?<!\w){re.escape(expected_value)}(?!\w)", content) is not None
else:
    expected_ok = True

result["repetition_detected"] = bool(repetition_reasons)
result["repetition_reasons"] = repetition_reasons
result["refusal_detected"] = refusal_detected
result["expected_output_ok"] = expected_ok
result["passed"] = bool(expected_ok and not repetition_reasons and not refusal_detected)

if not result["passed"]:
    failure_reasons = []
    if not expected_ok:
        failure_reasons.append("expected_output_mismatch")
    if repetition_reasons:
        failure_reasons.extend(repetition_reasons)
    if refusal_detected:
        failure_reasons.append("refusal_detected")
    result["failure_reasons"] = failure_reasons

print(json.dumps(result, separators=(",", ":")))
PY
    )"

    echo "$result_json"
}

collect_pod_metadata() {
    local pod_name="$1"
    local pod_json image_list node_name phase restart_count log_file log_hint_json pod_selector="$POD_SELECTOR"
    pod_json="$("$KUBECTL_BIN" -n "$KUBE_NAMESPACE" get pod "$pod_name" -o json 2>/dev/null || true)"
    if [[ -z "$pod_json" ]]; then
        jq -nc --arg pod_name "$pod_name" --arg namespace "$KUBE_NAMESPACE" --arg selector "$pod_selector" '
            {
                available: false,
                namespace: $namespace,
                pod_name: $pod_name,
                selector: $selector
            }'
        return 0
    fi

    image_list="$(jq -c '[.spec.initContainers[]?.image, .spec.containers[]?.image] | map(select(. != null and . != "")) | unique' <<<"$pod_json")"
    node_name="$(jq -r '.spec.nodeName // empty' <<<"$pod_json")"
    phase="$(jq -r '.status.phase // empty' <<<"$pod_json")"
    restart_count="$(jq -r '[.status.containerStatuses[]?.restartCount // 0] | add // 0' <<<"$pod_json")"

    log_file="${LOG_DIR}/${pod_name}.log"
    set +e
    if [[ -n "$CONTAINER_NAME" ]]; then
        "$KUBECTL_BIN" -n "$KUBE_NAMESPACE" logs "$pod_name" -c "$CONTAINER_NAME" --tail="$LOG_TAIL" >"$log_file" 2>&1
    else
        "$KUBECTL_BIN" -n "$KUBE_NAMESPACE" logs "$pod_name" --all-containers=true --tail="$LOG_TAIL" >"$log_file" 2>&1
    fi
    set -e

    log_hint_json="$(
        python3 - "$log_file" <<'PY'
import json
import pathlib
import re
import sys

log_path = pathlib.Path(sys.argv[1])
patterns = [
    r"segfault",
    r"SIGSEGV",
    r"core dumped",
    r"fatal error",
    r"out of memory",
    r"oom",
    r"OOMKilled",
    r"hip error",
    r"cuda error",
    r"gpu fault",
    r"illegal instruction",
    r"abort",
]

result = {
    "available": log_path.exists(),
    "matches": [],
    "log_path": str(log_path),
}

if not log_path.exists():
    print(json.dumps(result, separators=(",", ":")))
    sys.exit(0)

try:
    lines = log_path.read_text(errors="replace").splitlines()
except Exception as exc:
    result["error"] = str(exc)
    print(json.dumps(result, separators=(",", ":")))
    sys.exit(0)

for line in lines:
    lower = line.lower()
    if any(re.search(pattern, lower, re.IGNORECASE) for pattern in patterns):
        result["matches"].append(line[:240])

result["matches"] = result["matches"][:12]
print(json.dumps(result, separators=(",", ":")))
PY
    )"

    jq -nc \
        --arg namespace "$KUBE_NAMESPACE" \
        --arg pod_name "$pod_name" \
        --arg selector "$pod_selector" \
        --arg node_name "$node_name" \
        --arg phase "$phase" \
        --argjson restart_count "$restart_count" \
        --argjson image_list "$image_list" \
        --arg log_file "$log_file" \
        --argjson log_hint "$log_hint_json" \
        '{
            available: true,
            namespace: $namespace,
            pod_name: $pod_name,
            selector: $selector,
            node_name: $node_name,
            phase: $phase,
            restart_count: $restart_count,
            images: $image_list,
            log_file: $log_file,
            log_hints: $log_hint
        }'
}

discover_pod_name() {
    if [[ -n "$POD_NAME" ]]; then
        echo "$POD_NAME"
        return 0
    fi

    if [[ -z "$POD_SELECTOR" ]]; then
        echo ""
        return 0
    fi

    if ! command -v "$KUBECTL_BIN" >/dev/null 2>&1; then
        echo ""
        return 0
    fi

    "$KUBECTL_BIN" -n "$KUBE_NAMESPACE" get pods -l "$POD_SELECTOR" -o json 2>/dev/null | \
        jq -r '.items as $items | ($items | map(select(.status.phase == "Running"))[0].metadata.name // $items[0].metadata.name // empty)'
}

generate_markdown() {
    local json_path="$1"
    local md_path="$2"
    python3 - "$json_path" "$md_path" <<'PY'
import json
import pathlib
import sys

json_path = pathlib.Path(sys.argv[1])
md_path = pathlib.Path(sys.argv[2])
report = json.loads(json_path.read_text())

lines = []
lines.append("# Gemma4 Long-Context Probe")
lines.append("")
lines.append(f"- Run ID: `{report['run_id']}`")
lines.append(f"- Status: `{report['status']}`")
    lines.append(f"- Endpoint: `{report['endpoint']}`")
    lines.append(f"- Model: `{report['model']}`")
    if report.get("model_image"):
        lines.append(f"- Model image: `{report['model_image']}`")
    lines.append(f"- Started: `{report['started_at']}`")
lines.append(f"- Finished: `{report['finished_at']}`")
lines.append(f"- Git SHA: `{report.get('git_sha', 'unknown')}`")
lines.append("")

cluster = report.get("cluster", {})
if cluster and (cluster.get("available") or cluster.get("pod_name") or cluster.get("selector")):
    lines.append("## Cluster Metadata")
    lines.append("")
    lines.append(f"- Namespace: `{cluster.get('namespace', '')}`")
    lines.append(f"- Pod: `{cluster.get('pod_name', '')}`")
    lines.append(f"- Node: `{cluster.get('node_name', '')}`")
    images = cluster.get("images", [])
    if images:
        lines.append(f"- Images: `{', '.join(images)}`")
    if cluster.get("log_file"):
        lines.append(f"- Log tail: `{cluster['log_file']}`")
    hints = cluster.get("log_hints", {})
    if hints.get("matches"):
        lines.append("- Log hints:")
        for match in hints["matches"][:8]:
            lines.append(f"  - {match}")
    lines.append("")

lines.append("## Cases")
lines.append("")
lines.append("| Case | Status | HTTP | Prompt tok | Completion tok | Elapsed | Notes |")
lines.append("|------|--------|------|------------|----------------|---------|-------|")
for case in report.get("cases", []):
    notes = ", ".join(case.get("failure_reasons", [])) if case.get("failure_reasons") else "ok"
    lines.append(
        f"| `{case['case_name']}` | `{case['status']}` | `{case.get('http_code', 0)}` | "
        f"`{case.get('prompt_tokens', 0)}` | `{case.get('completion_tokens', 0)}` | "
        f"`{case.get('elapsed_s', 0)}` | {notes} |"
    )

lines.append("")
lines.append("## Artifacts")
lines.append("")
lines.append(f"- JSON: `{report['report_json']}`")
lines.append(f"- Markdown: `{report['report_md']}`")
lines.append(f"- Raw directory: `{report['raw_dir']}`")
lines.append("")

if any(case.get("content_preview") for case in report.get("cases", [])):
    lines.append("## Output Previews")
    lines.append("")
    for case in report.get("cases", []):
        preview = case.get("content_preview", "")
        if preview:
            lines.append(f"### {case['case_name']}")
            lines.append("")
            lines.append("```text")
            lines.append(preview)
            lines.append("```")
            lines.append("")

md_path.write_text("\n".join(lines).rstrip() + "\n")
PY
}

main() {
    local argv=("$@")
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --endpoint)
                ENDPOINT="$2"
                shift 2
                ;;
            --auth-token)
                AUTH_TOKEN="$2"
                shift 2
                ;;
            --model)
                MODEL="$2"
                shift 2
                ;;
            --medium-repeat)
                MEDIUM_REPEAT="$2"
                shift 2
                ;;
            --long-repeat)
                LONG_REPEAT="$2"
                shift 2
                ;;
            --short-max-tokens)
                SHORT_MAX_TOKENS="$2"
                shift 2
                ;;
            --medium-max-tokens)
                MEDIUM_MAX_TOKENS="$2"
                shift 2
                ;;
            --long-max-tokens)
                LONG_MAX_TOKENS="$2"
                shift 2
                ;;
            --report-dir)
                REPORT_DIR="$2"
                shift 2
                ;;
            --pod-name)
                POD_NAME="$2"
                shift 2
                ;;
            --pod-selector)
                POD_SELECTOR="$2"
                shift 2
                ;;
            --namespace)
                KUBE_NAMESPACE="$2"
                shift 2
                ;;
            --container)
                CONTAINER_NAME="$2"
                shift 2
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
                err "Unknown option: $1"
                usage
                exit 1
                ;;
        esac
    done

    require_cmd curl
    require_cmd jq
    require_cmd python3
    require_cmd openssl

    mkdir -p "$RAW_DIR" "$LOG_DIR"

    local git_sha started_at pod_name cluster_json case_results_json overall_status=0
    git_sha="$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
    started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    pod_name="$(discover_pod_name)"
    if [[ -n "$pod_name" ]] && command -v "$KUBECTL_BIN" >/dev/null 2>&1; then
        cluster_json="$(collect_pod_metadata "$pod_name")"
    else
        cluster_json="$(jq -nc --arg namespace "$KUBE_NAMESPACE" --arg pod_name "$pod_name" --arg selector "$POD_SELECTOR" '
            {
                available: false,
                namespace: $namespace,
                pod_name: $pod_name,
                selector: $selector
            }')"
    fi

    if [[ "$DRY_RUN" -eq 1 ]]; then
        log "Dry run for ${MODEL}"
        echo "endpoint=${ENDPOINT}"
        echo "model=${MODEL}"
        echo "short_sanity: 2+2"
        echo "medium_repeat=${MEDIUM_REPEAT}"
        echo "long_repeat=${LONG_REPEAT}"
        echo "artifact_dir=${ARTIFACT_DIR}"
        echo "pod_name=${pod_name:-<none>}"
        exit 0
    fi

    log "Starting probe: ${MODEL} -> ${ENDPOINT}"
    log "Artifacts: ${ARTIFACT_DIR}"

    local short_result medium_result long_result
    short_result="$(request_case \
        "short-sanity" \
        "$(short_prompt)" \
        "arithmetic" \
        "4" \
        "$SHORT_MAX_TOKENS")"
    medium_result="$(request_case \
        "medium-context" \
        "$(long_prompt "$MEDIUM_REPEAT" "gemma4-medium-ok" "medium-context")" \
        "marker" \
        "gemma4-medium-ok" \
        "$MEDIUM_MAX_TOKENS")"
    long_result="$(request_case \
        "long-context" \
        "$(long_prompt "$LONG_REPEAT" "gemma4-long-ok" "long-context")" \
        "marker" \
        "gemma4-long-ok" \
        "$LONG_MAX_TOKENS")"

    case_results_json="$(
        printf '%s\n' "$short_result" "$medium_result" "$long_result" | jq -s '.'
    )"

    local finished_at
    finished_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    local total_passed
    total_passed="$(jq '[.[] | select(.passed == true)] | length' <<<"$case_results_json")"
    if [[ "$total_passed" -ne 3 ]]; then
        overall_status=1
    fi

    jq -n \
        --arg run_id "$RUN_ID" \
        --arg git_sha "$git_sha" \
        --arg started_at "$started_at" \
        --arg finished_at "$finished_at" \
        --arg endpoint "$ENDPOINT" \
        --arg model "$MODEL" \
        --arg model_image "$MODEL_IMAGE" \
        --arg report_json "$REPORT_JSON" \
        --arg report_md "$REPORT_MD" \
        --arg raw_dir "$RAW_DIR" \
        --argjson medium_repeat "$MEDIUM_REPEAT" \
        --argjson long_repeat "$LONG_REPEAT" \
        --argjson short_max_tokens "$SHORT_MAX_TOKENS" \
        --argjson medium_max_tokens "$MEDIUM_MAX_TOKENS" \
        --argjson long_max_tokens "$LONG_MAX_TOKENS" \
        --argjson cases "$case_results_json" \
        --argjson cluster "$cluster_json" \
        '{
            run_id: $run_id,
            git_sha: $git_sha,
            started_at: $started_at,
            finished_at: $finished_at,
            endpoint: $endpoint,
            model: $model,
            model_image: $model_image,
            config: {
                short_max_tokens: $short_max_tokens,
                medium_repeat: $medium_repeat,
                medium_max_tokens: $medium_max_tokens,
                long_repeat: $long_repeat,
                long_max_tokens: $long_max_tokens
            },
            cluster: $cluster,
            cases: $cases,
            report_json: $report_json,
            report_md: $report_md,
            raw_dir: $raw_dir,
            status: (if ([ $cases[] | select(.passed == false) ] | length) == 0 then "pass" else "fail" end)
        }' >"$REPORT_JSON"

    generate_markdown "$REPORT_JSON" "$REPORT_MD"

    log "Saved JSON report: ${REPORT_JSON}"
    log "Saved Markdown report: ${REPORT_MD}"

    if [[ "$overall_status" -ne 0 ]]; then
        err "Probe failed: one or more cases returned unexpected output, refusal, or repetition"
        jq -r '.cases[] | select(.passed == false) | "- \(.case_name): \(.failure_reasons | join(", "))"' "$REPORT_JSON" >&2
        exit 1
    fi

    ok "Probe passed"
}

main "$@"
