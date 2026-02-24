#!/usr/bin/env bash
#
# bench-image-swap.sh - Benchmark GPU hot-swap latency for FlexInfer shared groups
#
# Measures warm inference, cold-start swap, swap-back, and burst concurrency
# for the 7900xtx-image shared GPU group (sdxl-turbo-imagegen, sdxl-inpainting,
# instruct-pix2pix).
#
# Usage:
#   ./scripts/bench-image-swap.sh              # All phases
#   ./scripts/bench-image-swap.sh warm         # Phase 1 only
#   ./scripts/bench-image-swap.sh cold         # Phase 2 only
#   ./scripts/bench-image-swap.sh swapback     # Phase 3 only
#   ./scripts/bench-image-swap.sh burst        # Phase 4 only
#
# Environment:
#   KUBECONFIG           Path to kubeconfig (default: platform/gitops/.kube/k3s.yaml)
#   WARMUP_ITERATIONS    Warmup requests before measurement (default: 1)
#
# Output:
#   /tmp/bench-image-swap-report.md
#   ConfigMap: flexinfer-swap-bench-results in flexinfer-system
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
KUBECONFIG="${KUBECONFIG:-$HOME/workspace/platform/gitops/.kube/k3s.yaml}"
export KUBECONFIG
NAMESPACE="flexinfer-system"
PROXY_SVC="flexinfer-proxy"
PROXY_PORT=18081
PROXY_SVC_PORT=80

MODEL_TEXT2IMG="sdxl-turbo-imagegen"
MODEL_INPAINT="sdxl-inpainting"
MODEL_PIX2PIX="instruct-pix2pix"
SHARED_GROUP="7900xtx-image"

REPORT="/tmp/bench-image-swap-report.md"
WATCHER_LOG="/tmp/bench-swap-watcher.log"
TEST_IMAGE="/tmp/bench-test-512.png"
TEST_MASK="/tmp/bench-test-mask-512.png"

CURL_TIMEOUT=600
POLL_INTERVAL=2

# Run metadata
RUN_ID="swap-$(date +%Y%m%dT%H%M%S)-$(openssl rand -hex 3)"
GIT_SHA="$(git -C "$(dirname "$0")/.." rev-parse --short HEAD 2>/dev/null || echo "unknown")"
SWAP_BENCH_CM="flexinfer-swap-bench-results"
WARMUP_ITERATIONS="${WARMUP_ITERATIONS:-1}"

# Populated by report_header()
GPU_NODE=""
DEVICE_CLASS=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
log()  { echo -e "${CYAN}[bench]${NC} $*"; }
warn() { echo -e "${YELLOW}[bench]${NC} $*"; }
err()  { echo -e "${RED}[bench]${NC} $*" >&2; }
ok()   { echo -e "${GREEN}[bench]${NC} $*"; }

timestamp_ms() {
    python3 -c "import time; print(int(time.time()*1000))"
}

ms_to_sec() {
    python3 -c "print(f'{$1 / 1000:.2f}')"
}

PF_PID=""

cleanup() {
    if [[ -n "$PF_PID" ]] && kill -0 "$PF_PID" 2>/dev/null; then
        kill "$PF_PID" 2>/dev/null || true
        wait "$PF_PID" 2>/dev/null || true
    fi
    # Clean up watcher if running
    if [[ -n "${WATCHER_PID:-}" ]] && kill -0 "$WATCHER_PID" 2>/dev/null; then
        kill "$WATCHER_PID" 2>/dev/null || true
        wait "$WATCHER_PID" 2>/dev/null || true
    fi
}
trap cleanup EXIT

setup_port_forward() {
    if curl -sf -o /dev/null "http://localhost:${PROXY_PORT}/healthz" 2>/dev/null; then
        log "Port-forward already active on :${PROXY_PORT}"
        return
    fi
    log "Starting port-forward to ${PROXY_SVC}..."
    kubectl port-forward "svc/${PROXY_SVC}" "${PROXY_PORT}:${PROXY_SVC_PORT}" \
        -n "$NAMESPACE" >/dev/null 2>&1 &
    PF_PID=$!
    # Wait for it to become ready
    for i in $(seq 1 15); do
        if curl -sf -o /dev/null "http://localhost:${PROXY_PORT}/healthz" 2>/dev/null; then
            ok "Port-forward ready on :${PROXY_PORT}"
            return
        fi
        sleep 1
    done
    err "Port-forward failed to start"
    exit 1
}

check_port_forward() {
    if ! curl -sf -o /dev/null "http://localhost:${PROXY_PORT}/healthz" 2>/dev/null; then
        warn "Port-forward died, restarting..."
        setup_port_forward
    fi
}

ensure_test_images() {
    if [[ -f "$TEST_IMAGE" && -f "$TEST_MASK" ]]; then
        return
    fi
    log "Generating test images (512x512)..."
    python3 -c "
from PIL import Image
# Solid blue test image
img = Image.new('RGB', (512, 512), color=(64, 128, 200))
img.save('${TEST_IMAGE}')
# White center mask (256x256 region in center)
mask = Image.new('L', (512, 512), color=0)
for y in range(128, 384):
    for x in range(128, 384):
        mask.putpixel((x, y), 255)
mask.save('${TEST_MASK}')
print('Created test image and mask')
"
}

# Get model status phase: Idle, Pending, Loading, Ready, Preempted, Failed
get_model_phase() {
    local model="$1"
    kubectl get mdl "$model" -n "$NAMESPACE" -o jsonpath='{.status.phase}' 2>/dev/null
}

# Get shared group state: Active, Queued, Preempted
get_model_group_state() {
    local model="$1"
    kubectl get mdl "$model" -n "$NAMESPACE" -o jsonpath='{.status.sharedGroup.state}' 2>/dev/null
}

# Get preemptedAt timestamp (ISO 8601)
get_preempted_at() {
    local model="$1"
    kubectl get mdl "$model" -n "$NAMESPACE" -o jsonpath='{.status.sharedGroup.preemptedAt}' 2>/dev/null
}

# Find which model in the group is currently Active
get_active_model() {
    for m in "$MODEL_TEXT2IMG" "$MODEL_INPAINT" "$MODEL_PIX2PIX"; do
        local state
        state=$(get_model_group_state "$m")
        if [[ "$state" == "Active" ]]; then
            echo "$m"
            return
        fi
    done
    echo ""
}

# Wait for a model to reach a target phase, polling every POLL_INTERVAL seconds
wait_for_phase() {
    local model="$1"
    local target="$2"
    local timeout="${3:-300}"
    local elapsed=0
    while [[ $elapsed -lt $timeout ]]; do
        local phase
        phase=$(get_model_phase "$model")
        if [[ "$phase" == "$target" ]]; then
            return 0
        fi
        sleep "$POLL_INTERVAL"
        elapsed=$((elapsed + POLL_INTERVAL))
    done
    err "Timeout waiting for ${model} to reach phase ${target} (last: $(get_model_phase "$model"))"
    return 1
}

# Wait until swap cooldown has elapsed for a model.
# sharedSwapCooldown is 5 min from preemptedAt.
wait_for_cooldown() {
    local model="$1"
    log "Checking cooldown for ${model}..."
    while true; do
        local preempted_at
        preempted_at=$(get_preempted_at "$model")
        if [[ -z "$preempted_at" ]]; then
            log "No preemptedAt set for ${model}, cooldown clear"
            return
        fi
        local preempted_epoch
        preempted_epoch=$(python3 -c "
from datetime import datetime, timezone
ts = '${preempted_at}'.rstrip('Z')
# Handle fractional seconds
if '.' in ts:
    dt = datetime.strptime(ts, '%Y-%m-%dT%H:%M:%S.%f').replace(tzinfo=timezone.utc)
else:
    dt = datetime.strptime(ts, '%Y-%m-%dT%H:%M:%S').replace(tzinfo=timezone.utc)
print(int(dt.timestamp()))
")
        local now_epoch
        now_epoch=$(python3 -c "import time; print(int(time.time()))")
        local elapsed=$((now_epoch - preempted_epoch))
        if [[ $elapsed -ge 300 ]]; then
            log "Cooldown elapsed (${elapsed}s since preemption)"
            return
        fi
        local remaining=$((300 - elapsed))
        log "Cooldown active, ${remaining}s remaining. Waiting..."
        sleep 15
    done
}

# Send text2img request. Outputs curl time_total (seconds) and HTTP status.
send_text2img() {
    local model="$1"
    local url="http://localhost:${PROXY_PORT}/model/${model}/v1/images/generations"
    curl -s -w '\n%{http_code} %{time_total}' \
        --max-time "$CURL_TIMEOUT" \
        -H "Content-Type: application/json" \
        -d '{"prompt":"a red fox in a snowy forest, digital art","size":"512x512","n":1}' \
        "$url"
}

# Send inpainting request (multipart form). Outputs curl time_total and HTTP status.
send_inpaint() {
    local model="$1"
    local url="http://localhost:${PROXY_PORT}/model/${model}/v1/images/edits"
    curl -s -w '\n%{http_code} %{time_total}' \
        --max-time "$CURL_TIMEOUT" \
        -F "image=@${TEST_IMAGE}" \
        -F "mask=@${TEST_MASK}" \
        -F "prompt=a golden retriever sitting in a park" \
        -F "size=512x512" \
        "$url"
}

# Send request to any model (dispatches based on model name)
send_request() {
    local model="$1"
    case "$model" in
        "$MODEL_INPAINT")
            send_inpaint "$model"
            ;;
        *)
            send_text2img "$model"
            ;;
    esac
}

# Parse curl output: last line is "HTTP_CODE TIME_TOTAL"
parse_curl_result() {
    local output="$1"
    local last_line
    last_line=$(echo "$output" | tail -1)
    echo "$last_line"
}

# ---------------------------------------------------------------------------
# Prometheus metric helpers
# ---------------------------------------------------------------------------

# Capture a snapshot of proxy metrics for a model.
# Returns space-separated: requests_total dur_sum dur_count swap_signals qwait_sum
capture_metric_snapshot() {
    local model="$1"
    local group="$SHARED_GROUP"
    curl -sf "http://localhost:${PROXY_PORT}/metrics" 2>/dev/null | python3 -c "
import sys
model = '${model}'
group = '${group}'
m = {'req': 0, 'dur_s': 0, 'dur_c': 0, 'swap': 0, 'qw': 0}
for line in sys.stdin:
    line = line.strip()
    if line.startswith('#'):
        continue
    if line.startswith('flexinfer_proxy_requests_total') and f'model=\"{model}\"' in line and 'status=\"200\"' in line:
        m['req'] = float(line.rsplit(' ', 1)[1])
    elif line.startswith('flexinfer_proxy_request_duration_seconds_sum') and f'model=\"{model}\"' in line:
        m['dur_s'] = float(line.rsplit(' ', 1)[1])
    elif line.startswith('flexinfer_proxy_request_duration_seconds_count') and f'model=\"{model}\"' in line:
        m['dur_c'] = float(line.rsplit(' ', 1)[1])
    elif line.startswith('flexinfer_proxy_gpugroup_swap_signals_total') and f'model=\"{model}\"' in line:
        m['swap'] = float(line.rsplit(' ', 1)[1])
    elif line.startswith('flexinfer_proxy_queue_wait_duration_seconds_sum') and f'model=\"{model}\"' in line:
        m['qw'] = float(line.rsplit(' ', 1)[1])
print(f\"{m['req']} {m['dur_s']} {m['dur_c']} {m['swap']} {m['qw']}\")
" 2>/dev/null || echo "0 0 0 0 0"
}

# Compute deltas between two metric snapshots.
# Args: before_snapshot after_snapshot
# Returns space-separated: req_delta dur_sum_delta dur_count_delta swap_delta qwait_delta proxy_avg
compute_metric_deltas() {
    local before="$1"
    local after="$2"
    python3 -c "
b = '${before}'.split()
a = '${after}'.split()
req = float(a[0]) - float(b[0])
dur_s = float(a[1]) - float(b[1])
dur_c = float(a[2]) - float(b[2])
swap = float(a[3]) - float(b[3])
qw = float(a[4]) - float(b[4])
avg = dur_s / dur_c if dur_c > 0 else 0
print(f'{req:.0f} {dur_s:.4f} {dur_c:.0f} {swap:.0f} {qw:.4f} {avg:.2f}')
"
}

# ---------------------------------------------------------------------------
# Warmup
# ---------------------------------------------------------------------------

# Send warmup requests, discarding results (mirrors Go benchmarker pattern).
run_warmup() {
    local model="$1"
    local count="${2:-$WARMUP_ITERATIONS}"
    if [[ "$count" -eq 0 ]]; then
        return
    fi
    log "Running ${count} warmup iteration(s) for ${model}..."
    for i in $(seq 1 "$count"); do
        local output result http_code
        output=$(send_request "$model")
        result=$(parse_curl_result "$output")
        http_code=$(echo "$result" | awk '{print $1}')
        if [[ "$http_code" == "200" ]]; then
            ok "  Warmup ${i}/${count}: OK"
        else
            warn "  Warmup ${i}/${count}: HTTP ${http_code}"
        fi
    done
}

# ---------------------------------------------------------------------------
# ConfigMap persistence
# ---------------------------------------------------------------------------

# Store phase results in a ConfigMap for cross-run comparison.
# Args: phase model avg_duration samples
store_configmap_results() {
    local phase="$1" model="$2" avg_duration="$3" samples="$4"
    local backend="diffusers"
    local hash_input="${backend}|${model}|${DEVICE_CLASS}|${phase}"
    local key_hash
    key_hash=$(echo -n "$hash_input" | shasum -a 256 | head -c 32)
    local data_key="swap_${key_hash}"
    local meta_key="meta_${key_hash}"

    local patch_json
    patch_json=$(python3 -c "
import json
meta = {
    'runId': '${RUN_ID}',
    'gitSha': '${GIT_SHA}',
    'phase': '${phase}',
    'model': '${model}',
    'backend': '${backend}',
    'deviceClass': '${DEVICE_CLASS}',
    'sharedGroup': '${SHARED_GROUP}',
    'avgDurationSeconds': float('${avg_duration}'),
    'samples': int('${samples}'),
    'warmupIterations': int('${WARMUP_ITERATIONS}'),
    'timestamp': '$(date -u '+%Y-%m-%dT%H:%M:%SZ')',
    'node': '${GPU_NODE}'
}
patch = {'data': {'${data_key}': '${avg_duration}', '${meta_key}': json.dumps(meta)}}
print(json.dumps(patch))
")

    # Create ConfigMap if it doesn't exist, then patch
    if ! kubectl get cm "$SWAP_BENCH_CM" -n "$NAMESPACE" >/dev/null 2>&1; then
        kubectl create configmap "$SWAP_BENCH_CM" -n "$NAMESPACE" 2>/dev/null || true
    fi
    kubectl patch cm "$SWAP_BENCH_CM" -n "$NAMESPACE" --type merge -p "$patch_json" 2>/dev/null || {
        warn "Failed to store results in ConfigMap"
        return
    }
    log "Stored results in ConfigMap ${SWAP_BENCH_CM} (key: ${data_key})"
}

# ---------------------------------------------------------------------------
# State watcher
# ---------------------------------------------------------------------------

# Start background model state watcher, logs timestamped state changes
start_watcher() {
    local target_model="$1"
    > "$WATCHER_LOG"
    (
        local prev_phase="" prev_state="" prev_replicas=""
        while true; do
            local ts
            ts=$(timestamp_ms)
            local phase state replicas
            phase=$(get_model_phase "$target_model" 2>/dev/null || echo "unknown")
            state=$(get_model_group_state "$target_model" 2>/dev/null || echo "unknown")
            replicas=$(kubectl get deploy "${target_model}" -n "$NAMESPACE" \
                -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo "0")
            replicas="${replicas:-0}"

            if [[ "$phase" != "$prev_phase" || "$state" != "$prev_state" || "$replicas" != "$prev_replicas" ]]; then
                echo "${ts} phase=${phase} state=${state} replicas=${replicas}" >> "$WATCHER_LOG"
                prev_phase="$phase"
                prev_state="$state"
                prev_replicas="$replicas"
            fi
            sleep "$POLL_INTERVAL"
        done
    ) &
    WATCHER_PID=$!
}

stop_watcher() {
    if [[ -n "${WATCHER_PID:-}" ]] && kill -0 "$WATCHER_PID" 2>/dev/null; then
        kill "$WATCHER_PID" 2>/dev/null || true
        wait "$WATCHER_PID" 2>/dev/null || true
    fi
    WATCHER_PID=""
}

# Parse watcher log for timing breakdown
# Returns: T_active T_ready T_replicas (milliseconds)
parse_watcher_log() {
    local t_active="" t_ready="" t_replicas=""
    while IFS= read -r line; do
        local ts
        ts=$(echo "$line" | awk '{print $1}')
        if [[ -z "$t_active" ]] && echo "$line" | grep -q "state=Active"; then
            t_active="$ts"
        fi
        if [[ -z "$t_ready" ]] && echo "$line" | grep -q "phase=Ready"; then
            t_ready="$ts"
        fi
        if [[ -z "$t_replicas" ]] && echo "$line" | grep -q "replicas=1"; then
            t_replicas="$ts"
        fi
    done < "$WATCHER_LOG"
    echo "${t_active:-0} ${t_ready:-0} ${t_replicas:-0}"
}

# ---------------------------------------------------------------------------
# Report helpers
# ---------------------------------------------------------------------------
REPORT_PARTS=()

report_header() {
    # Detect GPU node using FlexInfer canonical labels
    GPU_NODE=$(kubectl get nodes -l flexinfer.ai/gpu.vendor \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || echo "unknown")

    # Build device class from node labels (matches benchmarker.go:208-217)
    if [[ "$GPU_NODE" != "unknown" ]]; then
        DEVICE_CLASS=$(kubectl get node "$GPU_NODE" -o json 2>/dev/null | python3 -c "
import sys, json
node = json.load(sys.stdin)
labels = node.get('metadata', {}).get('labels', {})
parts = []
for key, name in [
    ('flexinfer.ai/gpu.vendor', 'vendor'),
    ('flexinfer.ai/gpu.arch', 'arch'),
    ('flexinfer.ai/gpu.vram', 'vram'),
    ('flexinfer.ai/gpu.count', 'count'),
    ('flexinfer.ai/gpu.int4', 'int4'),
]:
    parts.append(f'{name}={labels.get(key, \"unknown\")}')
print(','.join(parts))
" 2>/dev/null || echo "unknown")
    else
        DEVICE_CLASS="unknown"
    fi

    local date_str
    date_str=$(date -u '+%Y-%m-%d %H:%M UTC')

    REPORT_PARTS+=("# FlexInfer GPU Swap Benchmark")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("| Key | Value |")
    REPORT_PARTS+=("|-----|-------|")
    REPORT_PARTS+=("| Date | ${date_str} |")
    REPORT_PARTS+=("| Run ID | \`${RUN_ID}\` |")
    REPORT_PARTS+=("| Git SHA | \`${GIT_SHA}\` |")
    REPORT_PARTS+=("| Node | ${GPU_NODE} |")
    REPORT_PARTS+=("| Device Class | \`${DEVICE_CLASS}\` |")
    REPORT_PARTS+=("| Shared Group | ${SHARED_GROUP} |")
    REPORT_PARTS+=("| Warmup Iterations | ${WARMUP_ITERATIONS} |")
    REPORT_PARTS+=("")
}

write_report() {
    # Metadata footer
    REPORT_PARTS+=("---")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("**Run ID:** \`${RUN_ID}\`  |  **Git SHA:** \`${GIT_SHA}\`  |  **Device Class:** \`${DEVICE_CLASS}\`")
    REPORT_PARTS+=("**ConfigMap:** \`${SWAP_BENCH_CM}\` in \`${NAMESPACE}\`")
    REPORT_PARTS+=("")

    printf '%s\n' "${REPORT_PARTS[@]}" > "$REPORT"
    ok "Report written to ${REPORT}"
}

# ---------------------------------------------------------------------------
# Phase 1: Warm Inference
# ---------------------------------------------------------------------------
phase_warm() {
    log "${BOLD}Phase 1: Warm Inference Latency${NC}"
    check_port_forward

    local active
    active=$(get_active_model)
    if [[ -z "$active" ]]; then
        err "No active model in shared group. Cannot run warm test."
        return 1
    fi
    local phase
    phase=$(get_model_phase "$active")
    if [[ "$phase" != "Ready" ]]; then
        log "Waiting for ${active} to become Ready (currently ${phase})..."
        wait_for_phase "$active" "Ready" 300
    fi

    ok "Active model: ${active} (phase=Ready)"

    # Warmup (discard results, warms diffusers pipeline)
    run_warmup "$active"

    # Capture pre-measurement metric snapshot
    local snap_before
    snap_before=$(capture_metric_snapshot "$active")

    log "Sending 3 warm inference requests..."

    local times=()
    for i in 1 2 3; do
        local output result http_code time_total
        output=$(send_request "$active")
        result=$(parse_curl_result "$output")
        http_code=$(echo "$result" | awk '{print $1}')
        time_total=$(echo "$result" | awk '{print $2}')

        if [[ "$http_code" != "200" ]]; then
            err "Request ${i} failed with HTTP ${http_code}"
            continue
        fi
        times+=("$time_total")
        ok "  Request ${i}: ${time_total}s (HTTP ${http_code})"
    done

    if [[ ${#times[@]} -eq 0 ]]; then
        err "All warm requests failed"
        return 1
    fi

    # Capture post-measurement metric snapshot
    local snap_after
    snap_after=$(capture_metric_snapshot "$active")

    # Compute proxy-reported average from histogram delta
    local deltas proxy_avg
    deltas=$(compute_metric_deltas "$snap_before" "$snap_after")
    proxy_avg=$(echo "$deltas" | awk '{print $6}')

    # Compute min/avg/max from curl timings
    local times_csv
    times_csv=$(IFS=,; echo "${times[*]}")
    local stats
    stats=$(python3 -c "
times = [${times_csv}]
print(f'{min(times):.2f} {sum(times)/len(times):.2f} {max(times):.2f}')
")
    local min_t avg_t max_t
    min_t=$(echo "$stats" | awk '{print $1}')
    avg_t=$(echo "$stats" | awk '{print $2}')
    max_t=$(echo "$stats" | awk '{print $3}')

    REPORT_PARTS+=("## Warm Inference Latency")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("| Model | Min | Avg | Max | Proxy Avg | Warmup | Samples |")
    REPORT_PARTS+=("|-------|-----|-----|-----|-----------|--------|---------|")
    REPORT_PARTS+=("| ${active} | ${min_t}s | ${avg_t}s | ${max_t}s | ${proxy_avg}s | ${WARMUP_ITERATIONS} | ${#times[@]} |")
    REPORT_PARTS+=("")

    ok "Warm inference: min=${min_t}s avg=${avg_t}s max=${max_t}s proxy_avg=${proxy_avg}s"

    store_configmap_results "warm" "$active" "$avg_t" "${#times[@]}"
}

# ---------------------------------------------------------------------------
# Phase 2: Cold-Start Swap
# ---------------------------------------------------------------------------
phase_cold() {
    log "${BOLD}Phase 2: Cold-Start Swap${NC}"
    check_port_forward
    ensure_test_images

    local active
    active=$(get_active_model)
    if [[ -z "$active" ]]; then
        err "No active model found"
        return 1
    fi

    # Pick a target model that is NOT currently active
    local target=""
    for m in "$MODEL_INPAINT" "$MODEL_PIX2PIX" "$MODEL_TEXT2IMG"; do
        if [[ "$m" != "$active" ]]; then
            target="$m"
            break
        fi
    done
    if [[ -z "$target" ]]; then
        err "Could not find a non-active target model"
        return 1
    fi

    ok "Active: ${active} | Target (cold): ${target}"

    # Check cooldown
    wait_for_cooldown "$target"

    # Start state watcher
    start_watcher "$target"

    # Capture pre-request metric snapshot
    local snap_before
    snap_before=$(capture_metric_snapshot "$target")

    # Record T0 and send request
    local t0
    t0=$(timestamp_ms)
    log "T0=${t0}: Sending request to ${target} (triggers swap)..."

    local output result http_code time_total
    output=$(send_request "$target")
    local t_response
    t_response=$(timestamp_ms)

    result=$(parse_curl_result "$output")
    http_code=$(echo "$result" | awk '{print $1}')
    time_total=$(echo "$result" | awk '{print $2}')

    stop_watcher

    # Capture post-request metric snapshot
    local snap_after
    snap_after=$(capture_metric_snapshot "$target")

    if [[ "$http_code" != "200" ]]; then
        err "Cold-start request failed with HTTP ${http_code}"
        # Still report what we can
        REPORT_PARTS+=("## Cold-Start Swap")
        REPORT_PARTS+=("")
        REPORT_PARTS+=("**FAILED** -- ${active} -> ${target}: HTTP ${http_code}")
        REPORT_PARTS+=("")
        return 1
    fi

    ok "Response received: ${time_total}s (HTTP ${http_code})"

    # Compute metric deltas
    local deltas swap_signals_delta qwait_delta
    deltas=$(compute_metric_deltas "$snap_before" "$snap_after")
    swap_signals_delta=$(echo "$deltas" | awk '{print $4}')
    qwait_delta=$(echo "$deltas" | awk '{print $5}')

    # Parse watcher for timing breakdown
    local watcher_data t_active t_ready t_replicas
    watcher_data=$(parse_watcher_log)
    t_active=$(echo "$watcher_data" | awk '{print $1}')
    t_ready=$(echo "$watcher_data" | awk '{print $2}')
    t_replicas=$(echo "$watcher_data" | awk '{print $3}')

    # Compute durations
    local swap_detect_ms="-" model_load_ms="-" inference_ms="-"
    local total_ms=$((t_response - t0))

    if [[ "$t_active" != "0" ]]; then
        swap_detect_ms=$((t_active - t0))
    fi
    if [[ "$t_ready" != "0" && "$t_active" != "0" ]]; then
        model_load_ms=$((t_ready - t_active))
    fi
    if [[ "$t_ready" != "0" ]]; then
        inference_ms=$((t_response - t_ready))
    fi

    # Format for report
    local fmt_swap fmt_load fmt_infer fmt_total
    fmt_total=$(ms_to_sec "$total_ms")
    if [[ "$swap_detect_ms" != "-" ]]; then
        fmt_swap=$(ms_to_sec "$swap_detect_ms")
    else
        fmt_swap="-"
    fi
    if [[ "$model_load_ms" != "-" ]]; then
        fmt_load=$(ms_to_sec "$model_load_ms")
    else
        fmt_load="-"
    fi
    if [[ "$inference_ms" != "-" ]]; then
        fmt_infer=$(ms_to_sec "$inference_ms")
    else
        fmt_infer="-"
    fi

    log "Timing breakdown:"
    log "  Swap detection:  ${fmt_swap}s"
    log "  Model loading:   ${fmt_load}s"
    log "  Inference:       ${fmt_infer}s"
    log "  Proxy queue wait: ${qwait_delta}s"
    ok  "  Total cold start: ${fmt_total}s"

    REPORT_PARTS+=("## Cold-Start Swap")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("Swap: **${active}** -> **${target}**")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("| Phase | Duration |")
    REPORT_PARTS+=("|-------|----------|")
    REPORT_PARTS+=("| Swap detection | ${fmt_swap}s |")
    REPORT_PARTS+=("| Model loading | ${fmt_load}s |")
    REPORT_PARTS+=("| Inference | ${fmt_infer}s |")
    REPORT_PARTS+=("| Proxy queue wait | ${qwait_delta}s |")
    REPORT_PARTS+=("| Proxy swap signals | ${swap_signals_delta} |")
    REPORT_PARTS+=("| **Total** | **${fmt_total}s** |")
    REPORT_PARTS+=("")

    # Dump watcher log for debugging
    REPORT_PARTS+=("<details><summary>State transitions</summary>")
    REPORT_PARTS+=("")
    REPORT_PARTS+=('```')
    while IFS= read -r line; do
        REPORT_PARTS+=("$line")
    done < "$WATCHER_LOG"
    REPORT_PARTS+=('```')
    REPORT_PARTS+=("</details>")
    REPORT_PARTS+=("")

    store_configmap_results "cold" "$target" "$time_total" "1"
}

# ---------------------------------------------------------------------------
# Phase 3: Swap-Back
# ---------------------------------------------------------------------------
phase_swapback() {
    log "${BOLD}Phase 3: Swap-Back${NC}"
    check_port_forward
    ensure_test_images

    local active
    active=$(get_active_model)
    if [[ -z "$active" ]]; then
        err "No active model found"
        return 1
    fi

    # We want to swap back to the highest-priority model.
    # If it's already active, we need to trigger a swap first.
    local target="$MODEL_TEXT2IMG"
    if [[ "$active" == "$target" ]]; then
        # Already on highest priority model — pick inpaint to trigger swap away first
        log "Highest-priority model already active. Triggering swap away first..."
        target="$MODEL_INPAINT"
        wait_for_cooldown "$target"
        log "Sending request to ${target} to swap away..."
        local output
        output=$(send_request "$target")
        local result
        result=$(parse_curl_result "$output")
        local http_code
        http_code=$(echo "$result" | awk '{print $1}')
        if [[ "$http_code" != "200" ]]; then
            err "Failed to trigger swap away: HTTP ${http_code}"
            return 1
        fi
        ok "Swap away complete. Now ${target} is active."
        active="$target"
        target="$MODEL_TEXT2IMG"
    fi

    ok "Active: ${active} | Swap-back target: ${target}"

    # Wait for cooldown
    wait_for_cooldown "$target"

    # Start watcher
    start_watcher "$target"

    # Capture pre-request metric snapshot
    local snap_before
    snap_before=$(capture_metric_snapshot "$target")

    local t0
    t0=$(timestamp_ms)
    log "T0=${t0}: Sending swap-back request to ${target}..."

    local output result http_code time_total
    output=$(send_request "$target")
    local t_response
    t_response=$(timestamp_ms)

    result=$(parse_curl_result "$output")
    http_code=$(echo "$result" | awk '{print $1}')
    time_total=$(echo "$result" | awk '{print $2}')

    stop_watcher

    # Capture post-request metric snapshot
    local snap_after
    snap_after=$(capture_metric_snapshot "$target")

    if [[ "$http_code" != "200" ]]; then
        err "Swap-back request failed with HTTP ${http_code}"
        REPORT_PARTS+=("## Swap-Back")
        REPORT_PARTS+=("")
        REPORT_PARTS+=("**FAILED** -- ${active} -> ${target}: HTTP ${http_code}")
        REPORT_PARTS+=("")
        return 1
    fi

    ok "Swap-back response: ${time_total}s (HTTP ${http_code})"

    # Compute metric deltas
    local deltas swap_signals_delta qwait_delta
    deltas=$(compute_metric_deltas "$snap_before" "$snap_after")
    swap_signals_delta=$(echo "$deltas" | awk '{print $4}')
    qwait_delta=$(echo "$deltas" | awk '{print $5}')

    local watcher_data t_active t_ready t_replicas
    watcher_data=$(parse_watcher_log)
    t_active=$(echo "$watcher_data" | awk '{print $1}')
    t_ready=$(echo "$watcher_data" | awk '{print $2}')

    local total_ms=$((t_response - t0))
    local swap_detect_ms="-" model_load_ms="-" inference_ms="-"

    if [[ "$t_active" != "0" ]]; then
        swap_detect_ms=$((t_active - t0))
    fi
    if [[ "$t_ready" != "0" && "$t_active" != "0" ]]; then
        model_load_ms=$((t_ready - t_active))
    fi
    if [[ "$t_ready" != "0" ]]; then
        inference_ms=$((t_response - t_ready))
    fi

    local fmt_swap fmt_load fmt_infer fmt_total
    fmt_total=$(ms_to_sec "$total_ms")
    [[ "$swap_detect_ms" != "-" ]] && fmt_swap=$(ms_to_sec "$swap_detect_ms") || fmt_swap="-"
    [[ "$model_load_ms" != "-" ]] && fmt_load=$(ms_to_sec "$model_load_ms") || fmt_load="-"
    [[ "$inference_ms" != "-" ]] && fmt_infer=$(ms_to_sec "$inference_ms") || fmt_infer="-"

    log "Swap-back breakdown:"
    log "  Swap detection:  ${fmt_swap}s"
    log "  Model loading:   ${fmt_load}s"
    log "  Inference:       ${fmt_infer}s"
    log "  Proxy queue wait: ${qwait_delta}s"
    ok  "  Total swap-back: ${fmt_total}s"

    REPORT_PARTS+=("## Swap-Back")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("Swap: **${active}** -> **${target}**")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("| Phase | Duration |")
    REPORT_PARTS+=("|-------|----------|")
    REPORT_PARTS+=("| Swap detection | ${fmt_swap}s |")
    REPORT_PARTS+=("| Model loading | ${fmt_load}s |")
    REPORT_PARTS+=("| Inference | ${fmt_infer}s |")
    REPORT_PARTS+=("| Proxy queue wait | ${qwait_delta}s |")
    REPORT_PARTS+=("| Proxy swap signals | ${swap_signals_delta} |")
    REPORT_PARTS+=("| **Total** | **${fmt_total}s** |")
    REPORT_PARTS+=("")

    REPORT_PARTS+=("<details><summary>State transitions</summary>")
    REPORT_PARTS+=("")
    REPORT_PARTS+=('```')
    while IFS= read -r line; do
        REPORT_PARTS+=("$line")
    done < "$WATCHER_LOG"
    REPORT_PARTS+=('```')
    REPORT_PARTS+=("</details>")
    REPORT_PARTS+=("")

    store_configmap_results "swapback" "$target" "$time_total" "1"
}

# ---------------------------------------------------------------------------
# Phase 4: Burst Test
# ---------------------------------------------------------------------------
phase_burst() {
    local n=5
    log "${BOLD}Phase 4: Burst Test (N=${n})${NC}"
    check_port_forward
    ensure_test_images

    local active
    active=$(get_active_model)
    if [[ -z "$active" ]]; then
        err "No active model found"
        return 1
    fi

    # Pick a target that is NOT active
    local target=""
    for m in "$MODEL_INPAINT" "$MODEL_PIX2PIX" "$MODEL_TEXT2IMG"; do
        if [[ "$m" != "$active" ]]; then
            target="$m"
            break
        fi
    done
    if [[ -z "$target" ]]; then
        err "Could not find a non-active target model"
        return 1
    fi

    ok "Active: ${active} | Burst target (cold): ${target}"
    wait_for_cooldown "$target"

    # Capture pre-burst metric snapshot
    local snap_before
    snap_before=$(capture_metric_snapshot "$target")

    log "Firing ${n} concurrent requests to ${target}..."
    local t0
    t0=$(timestamp_ms)

    # Fire N background curls
    local pids=()
    local result_files=()
    for i in $(seq 1 "$n"); do
        local rfile="/tmp/bench-burst-${i}.txt"
        result_files+=("$rfile")
        (
            local output
            output=$(send_request "$target")
            echo "$output" > "$rfile"
        ) &
        pids+=($!)
    done

    # Wait for all
    local all_ok=true
    for pid in "${pids[@]}"; do
        if ! wait "$pid"; then
            all_ok=false
        fi
    done

    local t_end
    t_end=$(timestamp_ms)
    local total_ms=$((t_end - t0))

    # Capture post-burst metric snapshot
    local snap_after
    snap_after=$(capture_metric_snapshot "$target")

    # Compute metric deltas
    local deltas swap_signals_delta qwait_delta
    deltas=$(compute_metric_deltas "$snap_before" "$snap_after")
    swap_signals_delta=$(echo "$deltas" | awk '{print $4}')
    qwait_delta=$(echo "$deltas" | awk '{print $5}')

    # Collect results
    local http_codes=() times=() first_time="" last_time=""
    local success_count=0 fail_count=0

    for rfile in "${result_files[@]}"; do
        if [[ ! -f "$rfile" ]]; then
            fail_count=$((fail_count + 1))
            continue
        fi
        local result
        result=$(parse_curl_result "$(cat "$rfile")")
        local code time_s
        code=$(echo "$result" | awk '{print $1}')
        time_s=$(echo "$result" | awk '{print $2}')
        http_codes+=("$code")
        times+=("$time_s")
        if [[ "$code" == "200" ]]; then
            success_count=$((success_count + 1))
        else
            fail_count=$((fail_count + 1))
        fi
    done

    # Compute spread
    local stats all_succeeded spread first_resp last_resp avg_resp
    if [[ ${#times[@]} -gt 0 ]]; then
        local times_csv
        times_csv=$(IFS=,; echo "${times[*]}")
        stats=$(python3 -c "
times = [${times_csv}]
print(f'{min(times):.2f} {max(times):.2f} {max(times)-min(times):.2f} {sum(times)/len(times):.2f}')
")
        first_resp=$(echo "$stats" | awk '{print $1}')
        last_resp=$(echo "$stats" | awk '{print $2}')
        spread=$(echo "$stats" | awk '{print $3}')
        avg_resp=$(echo "$stats" | awk '{print $4}')
    else
        first_resp="-"
        last_resp="-"
        spread="-"
        avg_resp="0"
    fi

    if [[ $fail_count -eq 0 ]]; then
        all_succeeded="yes"
    else
        all_succeeded="no (${fail_count} failed)"
    fi

    ok "Burst complete: ${success_count}/${n} succeeded"
    log "  First response: ${first_resp}s | Last: ${last_resp}s | Spread: ${spread}s"
    log "  Total proxy queue wait: ${qwait_delta}s | Swap signals: ${swap_signals_delta}"

    REPORT_PARTS+=("## Burst Test (N=${n})")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("Target: **${target}** (cold swap from **${active}**)")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("| Metric | Value |")
    REPORT_PARTS+=("|--------|-------|")
    REPORT_PARTS+=("| All succeeded | ${all_succeeded} |")
    REPORT_PARTS+=("| Success / Total | ${success_count}/${n} |")
    REPORT_PARTS+=("| First response | ${first_resp}s |")
    REPORT_PARTS+=("| Last response | ${last_resp}s |")
    REPORT_PARTS+=("| Response spread | ${spread}s |")
    REPORT_PARTS+=("| Wall clock | $(ms_to_sec "$total_ms")s |")
    REPORT_PARTS+=("| Proxy queue wait (total) | ${qwait_delta}s |")
    REPORT_PARTS+=("| Proxy swap signals | ${swap_signals_delta} |")
    REPORT_PARTS+=("")

    # Individual results
    REPORT_PARTS+=("<details><summary>Individual results</summary>")
    REPORT_PARTS+=("")
    REPORT_PARTS+=("| # | HTTP | Time |")
    REPORT_PARTS+=("|---|------|------|")
    for i in $(seq 0 $((${#http_codes[@]} - 1))); do
        REPORT_PARTS+=("| $((i+1)) | ${http_codes[$i]} | ${times[$i]}s |")
    done
    REPORT_PARTS+=("</details>")
    REPORT_PARTS+=("")

    # Cleanup
    for rfile in "${result_files[@]}"; do
        rm -f "$rfile"
    done

    store_configmap_results "burst" "$target" "$avg_resp" "${success_count}"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
    local phase="${1:-all}"

    case "$phase" in
        -h|--help)
            echo "Usage: $0 [warm|cold|swapback|burst|all]"
            echo ""
            echo "Phases:"
            echo "  warm      Warm inference latency (active model, 3 samples)"
            echo "  cold      Cold-start swap time with breakdown"
            echo "  swapback  Swap-back to original model after cooldown"
            echo "  burst     N=5 concurrent requests during cold start"
            echo "  all       Run all phases sequentially (default)"
            echo ""
            echo "Environment:"
            echo "  KUBECONFIG           Path to kubeconfig (default: platform/gitops/.kube/k3s.yaml)"
            echo "  WARMUP_ITERATIONS    Warmup requests before measurement (default: 1)"
            echo ""
            echo "Output:"
            echo "  Report:    ${REPORT}"
            echo "  ConfigMap: ${SWAP_BENCH_CM} in ${NAMESPACE}"
            exit 0
            ;;
        warm|cold|swapback|burst|all) ;;
        *)
            err "Unknown phase: ${phase}"
            echo "Usage: $0 [warm|cold|swapback|burst|all]"
            exit 1
            ;;
    esac

    log "FlexInfer GPU Swap Benchmark"
    log "Run ID: ${RUN_ID}"
    log "Git SHA: ${GIT_SHA}"
    log "Shared group: ${SHARED_GROUP}"
    log "Warmup iterations: ${WARMUP_ITERATIONS}"
    log "Phase: ${phase}"
    echo ""

    setup_port_forward
    report_header

    case "$phase" in
        warm)
            phase_warm
            ;;
        cold)
            phase_cold
            ;;
        swapback)
            phase_swapback
            ;;
        burst)
            phase_burst
            ;;
        all)
            phase_warm
            echo ""
            phase_cold
            echo ""
            phase_swapback
            echo ""
            phase_burst
            ;;
    esac

    write_report
}

main "$@"
