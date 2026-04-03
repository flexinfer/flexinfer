#!/usr/bin/env bash
#
# bench-runtime-warmstart.sh - Measure managed runtime warm restart for a model
#
# Deletes the unified runtime pod serving a model's target node, waits for the
# replacement pod and model to recover, then extracts startup timings from the
# new runtime logs. This exercises the real CRD/controller/runtime path.
#
# Usage:
#   MODEL=gemma4-e4b-turboquant ./scripts/bench-runtime-warmstart.sh
#   MODEL=gemma4-e4b-turboquant NODE=cblevins-7900xtx ./scripts/bench-runtime-warmstart.sh
#
# Environment:
#   MODEL        Model CR name (required)
#   NAMESPACE    Kubernetes namespace (default: flexinfer-system)
#   NODE         Target node name. Defaults to spec.nodeSelector["kubernetes.io/hostname"].
#   TIMEOUT      Overall wait timeout in seconds (default: 900)
#   POLL         Poll interval in seconds (default: 5)
#
set -euo pipefail

MODEL="${MODEL:?MODEL is required}"
NAMESPACE="${NAMESPACE:-flexinfer-system}"
NODE="${NODE:-}"
TIMEOUT="${TIMEOUT:-900}"
POLL="${POLL:-5}"
RUNTIME_LABEL="app.kubernetes.io/component=flexinfer-runtime"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()  { echo -e "${CYAN}[warmstart]${NC} $*"; }
warn() { echo -e "${YELLOW}[warmstart]${NC} $*"; }
err()  { echo -e "${RED}[warmstart]${NC} $*" >&2; }
ok()   { echo -e "${GREEN}[warmstart]${NC} $*"; }

for cmd in kubectl python3; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        err "Required command not found: $cmd"
        exit 1
    fi
done

if [[ -z "$NODE" ]]; then
    NODE="$(kubectl -n "$NAMESPACE" get model "$MODEL" -o jsonpath='{.spec.nodeSelector.kubernetes\.io/hostname}' 2>/dev/null || true)"
fi

if [[ -z "$NODE" ]]; then
    err "Unable to infer target node from Model/$MODEL. Set NODE explicitly."
    exit 1
fi

runtime_pod_for_node() {
    kubectl -n "$NAMESPACE" get pods -l "$RUNTIME_LABEL" \
        --field-selector "spec.nodeName=${NODE}" \
        -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.phase}{"\t"}{range .status.conditions[?(@.type=="Ready")]}{.status}{"\n"}{end}{end}' \
    | awk '$2=="Running" && $3=="True" {print $1; exit}'
}

wait_for_runtime_pod() {
    local exclude="$1"
    local elapsed=0
    while [[ "$elapsed" -lt "$TIMEOUT" ]]; do
        local pod
        pod="$(runtime_pod_for_node || true)"
        if [[ -n "$pod" && "$pod" != "$exclude" ]]; then
            echo "$pod"
            return 0
        fi
        sleep "$POLL"
        elapsed=$((elapsed + POLL))
    done
    return 1
}

wait_for_model_ready() {
    local elapsed=0
    while [[ "$elapsed" -lt "$TIMEOUT" ]]; do
        local phase ready reason
        phase="$(kubectl -n "$NAMESPACE" get model "$MODEL" -o jsonpath='{.status.phase}' 2>/dev/null || true)"
        ready="$(kubectl -n "$NAMESPACE" get model "$MODEL" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)"
        reason="$(kubectl -n "$NAMESPACE" get model "$MODEL" -o jsonpath='{.status.conditions[?(@.type=="Ready")].reason}' 2>/dev/null || true)"
        if [[ "$phase" == "Ready" && "$ready" == "True" ]]; then
            ok "Model/$MODEL is Ready (${reason})"
            return 0
        fi
        log "Waiting for Model/$MODEL to become Ready (phase=${phase:-unknown} reason=${reason:-unknown})"
        sleep "$POLL"
        elapsed=$((elapsed + POLL))
    done
    return 1
}

extract_timings() {
    local pod="$1"
    local logfile
    logfile="$(mktemp "/tmp/${MODEL}-${pod}-warmstart.XXXXXX.log")"
    kubectl -n "$NAMESPACE" logs "$pod" >"$logfile"
    python3 - "$logfile" <<'PY'
import json
import re
import sys

path = sys.argv[1]
patterns = {
    "weights_seconds": re.compile(r"Loading weights took ([0-9.]+) seconds"),
    "compile_warmup_seconds": re.compile(r"torch\.compile and initial profiling/warmup run together took ([0-9.]+) s"),
    "engine_init_seconds": re.compile(r"init engine .* took ([0-9.]+) seconds"),
    "cache_hit_seconds": re.compile(r"Directly load the compiled graph\(s\).* took ([0-9.]+) s"),
    "gpu_kv_tokens": re.compile(r"GPU KV cache size: ([0-9,]+) tokens"),
}

results = {}
with open(path, "r", encoding="utf-8", errors="replace") as fh:
    for line in fh:
        for key, pattern in patterns.items():
            match = pattern.search(line)
            if not match:
                continue
            value = match.group(1).replace(",", "")
            results[key] = float(value) if "." in value else int(value)

print(json.dumps(results, indent=2, sort_keys=True))
PY
    rm -f "$logfile"
}

old_pod="$(runtime_pod_for_node || true)"
if [[ -z "$old_pod" ]]; then
    err "No ready runtime pod found on node ${NODE}"
    exit 1
fi

log "Model: ${MODEL}"
log "Node: ${NODE}"
log "Current runtime pod: ${old_pod}"
log "Deleting runtime pod to force managed warm restart..."
kubectl -n "$NAMESPACE" delete pod "$old_pod" --wait=false >/dev/null

log "Waiting for replacement runtime pod on ${NODE}..."
new_pod="$(wait_for_runtime_pod "$old_pod")" || {
    err "Timed out waiting for replacement runtime pod on ${NODE}"
    exit 1
}
ok "Replacement runtime pod: ${new_pod}"

if ! wait_for_model_ready; then
    err "Timed out waiting for Model/${MODEL} to recover"
    exit 1
fi

log "Startup timing summary from ${new_pod}:"
extract_timings "$new_pod"
