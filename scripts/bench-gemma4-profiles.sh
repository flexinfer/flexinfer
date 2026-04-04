#!/usr/bin/env bash
#
# bench-gemma4-profiles.sh - Compare FlexInfer Gemma fast vs long profiles.
#
# Uses a LiteLLM-compatible /v1/chat/completions endpoint and emits a compact
# JSON report with latency and token-usage numbers for a small prompt matrix.
#
# Typical usage:
#   kubectl -n ai port-forward svc/litellm 18000:8000
#   ENDPOINT=http://127.0.0.1:18000 ./scripts/bench-gemma4-profiles.sh
#
# Environment:
#   ENDPOINT          LiteLLM/OpenAI-compatible base URL (default: http://127.0.0.1:18000)
#   AUTH_TOKEN        Bearer token (default: sk-litellm-master-key)
#   FAST_MODEL        Fast profile model id (default: gemma4-e4b-fast)
#   LONG_MODEL        Long profile model id (default: gemma4-e4b-long)
#   DEFAULT_MODEL     Default alias model id (default: gemma4-e4b)
#   SHORT_REPEAT      Repeat count for short/medium prompt (default: 2000)
#   LONG_REPEAT       Repeat count for long-context prompt (default: 6000)
#   MAX_TOKENS        Completion tokens to request (default: 64)
#   WARMUP            Warmup requests per leg before measurement (default: 1)
#   TIMEOUT           Per-request timeout seconds (default: 1800)
#   REPORT_DIR        Output directory (default: /tmp)
#
set -euo pipefail

ENDPOINT="${ENDPOINT:-http://127.0.0.1:18000}"
AUTH_TOKEN="${AUTH_TOKEN:-sk-litellm-master-key}"
DEFAULT_MODEL="${DEFAULT_MODEL:-gemma4-e4b}"
FAST_MODEL="${FAST_MODEL:-gemma4-e4b-fast}"
LONG_MODEL="${LONG_MODEL:-gemma4-e4b-long}"
SHORT_REPEAT="${SHORT_REPEAT:-2000}"
LONG_REPEAT="${LONG_REPEAT:-6000}"
MAX_TOKENS="${MAX_TOKENS:-64}"
WARMUP="${WARMUP:-1}"
TIMEOUT="${TIMEOUT:-1800}"
REPORT_DIR="${REPORT_DIR:-/tmp}"

RUN_ID="gemma4-$(date +%Y%m%dT%H%M%S)-$(openssl rand -hex 3)"
REPORT_JSON="${REPORT_DIR}/bench-gemma4-profiles-${RUN_ID}.json"
API_URL="${ENDPOINT}/v1/chat/completions"

for cmd in curl jq python3; do
    command -v "$cmd" >/dev/null 2>&1 || {
        echo "missing required command: $cmd" >&2
        exit 1
    }
done

prompt() {
    local repeat_count="$1"
    local label="${2:-measure}"
    python3 - "$repeat_count" "$label" <<'PY'
import sys
repeat_count = int(sys.argv[1])
label = sys.argv[2]
text = "alpha beta gamma delta epsilon " * repeat_count
print(
    f"Run label: {label}. Summarize the structure and intent of this repeated token sequence in one short paragraph.\n\n"
    + text
)
PY
}

request_once() {
    local model="$1"
    local repeat_count="$2"
    local label="${3:-measure}"
    local body_file stats_file payload_file
    body_file="$(mktemp)"
    stats_file="$(mktemp)"
    payload_file="$(mktemp)"

    jq -n \
        --arg model "$model" \
        --arg content "$(prompt "$repeat_count" "$label")" \
        --argjson max_tokens "$MAX_TOKENS" \
        '{
          model: $model,
          messages: [{role: "user", content: $content}],
          max_tokens: $max_tokens,
          stream: false
        }' >"$payload_file"

    curl -sS --max-time "$TIMEOUT" \
        -H "Authorization: Bearer ${AUTH_TOKEN}" \
        -H "Content-Type: application/json" \
        -o "$body_file" \
        -w '%{http_code} %{time_total}' \
        -d @"$payload_file" \
        "$API_URL" >"$stats_file"

    python3 - "$model" "$repeat_count" "$body_file" "$stats_file" <<'PY'
import json
import pathlib
import sys

model = sys.argv[1]
repeat_count = int(sys.argv[2])
body_path = pathlib.Path(sys.argv[3])
stats_path = pathlib.Path(sys.argv[4])
http_code, elapsed = stats_path.read_text().strip().split()
elapsed = float(elapsed)

result = {
    "model": model,
    "repeat_count": repeat_count,
    "http_code": int(http_code),
    "elapsed_s": round(elapsed, 3),
}

try:
    body = json.loads(body_path.read_text())
except Exception as exc:
    result["error"] = f"invalid_json: {exc}"
    print(json.dumps(result))
    sys.exit(0)

if int(http_code) != 200:
    result["error"] = body
    print(json.dumps(result))
    sys.exit(0)

usage = body.get("usage", {})
result["prompt_tokens"] = usage.get("prompt_tokens", 0)
result["completion_tokens"] = usage.get("completion_tokens", 0)
prompt_tokens = result["prompt_tokens"]
completion_tokens = result["completion_tokens"]
result["prompt_tps"] = round((prompt_tokens / elapsed), 2) if prompt_tokens and elapsed > 0 else 0.0
result["completion_tps"] = round((completion_tokens / elapsed), 2) if completion_tokens and elapsed > 0 else 0.0
result["total_tps"] = round(((prompt_tokens + completion_tokens) / elapsed), 2) if elapsed > 0 else 0.0

choices = body.get("choices", [])
if choices:
    message = choices[0].get("message", {})
    content = message.get("content", "")
    result["preview"] = content[:180]

print(json.dumps(result))
PY

    rm -f "$body_file" "$stats_file" "$payload_file"
}

warmup() {
    local model="$1"
    local repeat_count="$2"
    local i
    for ((i=0; i<WARMUP; i++)); do
        request_once "$model" "$repeat_count" "warmup-${i}" >/dev/null
    done
}

warmup "$DEFAULT_MODEL" 8
warmup "$FAST_MODEL" "$SHORT_REPEAT"
warmup "$LONG_MODEL" "$SHORT_REPEAT"
warmup "$LONG_MODEL" "$LONG_REPEAT"

default_check="$(request_once "$DEFAULT_MODEL" 8 "measure-default")"
fast_short="$(request_once "$FAST_MODEL" "$SHORT_REPEAT" "measure-fast-short")"
long_short="$(request_once "$LONG_MODEL" "$SHORT_REPEAT" "measure-long-short")"
long_long="$(request_once "$LONG_MODEL" "$LONG_REPEAT" "measure-long-long")"

jq -n \
    --arg endpoint "$ENDPOINT" \
    --arg default_model "$DEFAULT_MODEL" \
    --arg fast_model "$FAST_MODEL" \
    --arg long_model "$LONG_MODEL" \
    --arg run_id "$RUN_ID" \
    --argjson default_check "$default_check" \
    --argjson fast_short "$fast_short" \
    --argjson long_short "$long_short" \
    --argjson long_long "$long_long" \
    '{
      run_id: $run_id,
      endpoint: $endpoint,
      models: {
        default: $default_model,
        fast: $fast_model,
        long: $long_model
      },
      default_alias_check: $default_check,
      fast_short: $fast_short,
      long_short: $long_short,
      long_long: $long_long
    }' | tee "$REPORT_JSON"

echo
echo "saved report: $REPORT_JSON"
