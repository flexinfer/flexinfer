#!/usr/bin/env bash
#
# bench-model.sh - Benchmark LLM model via FlexInfer proxy OpenAI-compatible API
#
# Measures prompt processing speed, generation speed, TTFT, and multi-turn
# performance through the proxy's /v1/chat/completions endpoint.
#
# Usage:
#   ./scripts/bench-model.sh              # All phases
#   ./scripts/bench-model.sh short        # Short prompt only
#   ./scripts/bench-model.sh medium       # Medium prompt only
#   ./scripts/bench-model.sh long         # Long prompt only
#   ./scripts/bench-model.sh multiturn    # Multi-turn context scaling
#   ./scripts/bench-model.sh stream       # Streaming TTFT measurement
#
# Environment:
#   ENDPOINT      Proxy base URL (default: http://localhost:8080)
#   MODEL         Model name for routing (default: qwen3-14b-claude-distill)
#   ITERATIONS    Measurement iterations per phase (default: 3)
#   WARMUP        Warmup requests before measurement (default: 1)
#   MAX_TOKENS    Max generation tokens per request (default: 256)
#   TIMEOUT       Per-request curl timeout in seconds (default: 120)
#   REPORT_DIR    Report output directory (default: /tmp)
#
# Output:
#   Human-readable table to stdout
#   JSON report to $REPORT_DIR/bench-model-${MODEL}-${RUN_ID}.json
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
ENDPOINT="${ENDPOINT:-http://localhost:8080}"
MODEL="${MODEL:-qwen3-14b-claude-distill}"
ITERATIONS="${ITERATIONS:-3}"
WARMUP="${WARMUP:-1}"
MAX_TOKENS="${MAX_TOKENS:-256}"
TIMEOUT="${TIMEOUT:-120}"
REPORT_DIR="${REPORT_DIR:-/tmp}"

RUN_ID="bench-$(date +%Y%m%dT%H%M%S)-$(openssl rand -hex 3)"
GIT_SHA="$(git -C "$(dirname "$0")/.." rev-parse --short HEAD 2>/dev/null || echo "unknown")"
# DIRECT=1 bypasses proxy /model/<name> prefix (for port-forwarded pods)
if [[ "${DIRECT:-0}" == "1" ]]; then
    API_URL="${ENDPOINT}/v1/chat/completions"
else
    API_URL="${ENDPOINT}/model/${MODEL}/v1/chat/completions"
fi
REPORT_JSON="${REPORT_DIR}/bench-model-${MODEL}-${RUN_ID}.json"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
log()  { echo -e "${CYAN}[bench]${NC} $*"; }
warn() { echo -e "${YELLOW}[bench]${NC} $*"; }
err()  { echo -e "${RED}[bench]${NC} $*" >&2; }
ok()   { echo -e "${GREEN}[bench]${NC} $*"; }

cleanup() {
    # Remove temp files
    rm -f /tmp/bench-model-curl-*.tmp /tmp/bench-model-stream-*.tmp 2>/dev/null || true
}
trap cleanup EXIT

# Check dependencies
for cmd in curl jq bc; do
    if ! command -v "$cmd" &>/dev/null; then
        err "Required command not found: ${cmd}"
        exit 1
    fi
done

# ---------------------------------------------------------------------------
# Prompt templates
# ---------------------------------------------------------------------------

prompt_short() {
    cat <<'PROMPT'
Write a Python function that checks if a number is prime. Include a docstring.
PROMPT
}

prompt_medium() {
    cat <<'PROMPT'
You are a senior software engineer. Review the following Go code and suggest improvements for error handling, performance, and readability. Provide specific code examples for each suggestion.

```go
package main

import (
    "database/sql"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "sync"
    "time"
)

type UserService struct {
    db    *sql.DB
    cache map[string]interface{}
    mu    sync.Mutex
}

func (s *UserService) GetUser(w http.ResponseWriter, r *http.Request) {
    id := r.URL.Query().Get("id")
    if id == "" {
        http.Error(w, "missing id", 400)
        return
    }

    s.mu.Lock()
    if cached, ok := s.cache[id]; ok {
        s.mu.Unlock()
        json.NewEncoder(w).Encode(cached)
        return
    }
    s.mu.Unlock()

    row := s.db.QueryRow("SELECT id, name, email, created_at FROM users WHERE id = $1", id)
    var user struct {
        ID        string
        Name      string
        Email     string
        CreatedAt time.Time
    }
    err := row.Scan(&user.ID, &user.Name, &user.Email, &user.CreatedAt)
    if err != nil {
        log.Println(err)
        http.Error(w, "internal error", 500)
        return
    }

    s.mu.Lock()
    s.cache[id] = user
    s.mu.Unlock()

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(user)
}

func (s *UserService) ListUsers(w http.ResponseWriter, r *http.Request) {
    rows, err := s.db.Query("SELECT id, name, email, created_at FROM users ORDER BY created_at DESC LIMIT 100")
    if err != nil {
        log.Println(err)
        http.Error(w, "internal error", 500)
        return
    }
    defer rows.Close()

    var users []struct {
        ID        string
        Name      string
        Email     string
        CreatedAt time.Time
    }
    for rows.Next() {
        var u struct {
            ID        string
            Name      string
            Email     string
            CreatedAt time.Time
        }
        rows.Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt)
        users = append(users, u)
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(users)
}

func main() {
    db, err := sql.Open("postgres", "postgres://localhost/mydb?sslmode=disable")
    if err != nil {
        log.Fatal(err)
    }

    svc := &UserService{
        db:    db,
        cache: make(map[string]interface{}),
    }

    http.HandleFunc("/user", svc.GetUser)
    http.HandleFunc("/users", svc.ListUsers)
    fmt.Println("Server starting on :8080")
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

Focus on: 1) Error handling gaps 2) Cache invalidation strategy 3) SQL injection risks 4) Concurrency improvements 5) Structured logging
PROMPT
}

prompt_long() {
    cat <<'PROMPT'
You are a technical architect. Design a complete microservices architecture for a real-time collaborative document editing system (similar to Google Docs). Include the following sections:

## Requirements

The system must support:
- Real-time collaborative editing with multiple users per document
- Conflict resolution using Operational Transform (OT) or CRDTs
- Document versioning and history
- Rich text formatting (bold, italic, headers, lists, code blocks)
- Comments and suggestions with threading
- User presence (who is viewing/editing)
- Offline editing with sync
- Document sharing with role-based permissions (owner, editor, commenter, viewer)
- Full-text search across all documents
- Real-time notifications
- File attachments and embedded media
- Export to PDF, DOCX, Markdown

## Deliverables

For each microservice, provide:
1. Service name and responsibility
2. API endpoints (REST + WebSocket where needed)
3. Data model (key entities and relationships)
4. Technology stack recommendation
5. Scaling strategy

Also include:
- System context diagram description
- Service interaction patterns (sync vs async)
- Event bus topic design
- Database per service strategy
- Authentication and authorization flow
- Rate limiting and abuse prevention
- Monitoring and alerting strategy
- Disaster recovery plan
- Performance targets (latency SLOs)

## Constraints
- Must handle 100K concurrent users
- Document size up to 10MB
- 99.9% availability SLA
- Sub-100ms latency for keystroke propagation
- GDPR compliant data handling
- SOC 2 Type II compliance requirements

## Additional Context

Consider the following existing infrastructure:
- Kubernetes cluster on AWS EKS
- PostgreSQL for persistent storage
- Redis for caching and pub/sub
- Kafka for event streaming
- S3 for file storage
- CloudFront for CDN
- DataDog for observability
- Auth0 for identity management

Previous architecture decisions:
- Monolith extraction in progress
- Current system handles 10K concurrent users
- TypeScript/Node.js backend, React frontend
- GraphQL API gateway already in place
- CI/CD via GitHub Actions

Provide your complete architecture design with detailed explanations for each decision. Include sequence diagrams for the three most critical flows: document editing, conflict resolution, and permission changes.
PROMPT
}

prompt_stream() {
    cat <<'PROMPT'
Explain the difference between Operational Transform (OT) and Conflict-free Replicated Data Types (CRDTs) for collaborative editing. Include code examples in TypeScript for a simple text CRDT implementation. Cover the trade-offs for real-time document editing at scale.
PROMPT
}

# ---------------------------------------------------------------------------
# Request functions
# ---------------------------------------------------------------------------

# Send a non-streaming chat completion request.
# Outputs raw JSON response body to stdout.
send_chat() {
    local messages="$1"
    local max_tok="${2:-$MAX_TOKENS}"
    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$messages" \
        --argjson max_tokens "$max_tok" \
        '{model: $model, messages: $messages, max_tokens: $max_tokens, stream: false}')

    curl -s --max-time "$TIMEOUT" \
        -H "Content-Type: application/json" \
        -w '\n{"_curl_http_code":%{http_code},"_curl_time_total":%{time_total}}' \
        -d "$payload" \
        "$API_URL"
}

# Parse a non-streaming response into metrics.
# Input: raw curl output (JSON body + curl stats on last line)
# Output: space-separated fields:
#   http_code wall_time_s prompt_tok gen_tok prompt_tps gen_tps
parse_response() {
    local raw="$1"

    # Split body and curl stats. The last line contains _curl_http_code.
    local curl_stats body
    curl_stats=$(echo "$raw" | grep '_curl_http_code' | tail -1)
    body=$(echo "$raw" | grep -v '_curl_http_code')

    local http_code wall_time
    http_code=$(echo "$curl_stats" | jq -r '._curl_http_code')
    wall_time=$(echo "$curl_stats" | jq -r '._curl_time_total')

    if [[ "$http_code" != "200" ]]; then
        echo "${http_code} ${wall_time} 0 0 0 0"
        return
    fi

    # Try llama.cpp timings object first (most accurate)
    local has_timings
    has_timings=$(echo "$body" | jq -r 'if .timings then "yes" else "no" end' 2>/dev/null || echo "no")

    if [[ "$has_timings" == "yes" ]]; then
        echo "$body" | jq -r --arg wt "$wall_time" --arg code "$http_code" '
            .timings as $t |
            ($t.prompt_n // 0) as $pn |
            ($t.prompt_ms // 0) as $pms |
            ($t.predicted_n // 0) as $gn |
            ($t.predicted_ms // 0) as $gms |
            (if $pms > 0 then ($pn / ($pms / 1000)) else 0 end) as $ptps |
            (if $gms > 0 then ($gn / ($gms / 1000)) else 0 end) as $gtps |
            "\($code) \($wt) \($pn) \($gn) \($ptps | . * 100 | round / 100) \($gtps | . * 100 | round / 100)"
        ' 2>/dev/null
        return
    fi

    # Fallback: usage object + wall clock
    local prompt_tok gen_tok
    prompt_tok=$(echo "$body" | jq -r '.usage.prompt_tokens // 0' 2>/dev/null || echo "0")
    gen_tok=$(echo "$body" | jq -r '.usage.completion_tokens // 0' 2>/dev/null || echo "0")

    local gen_tps
    if [[ "$gen_tok" -gt 0 ]] && [[ "$(echo "$wall_time > 0" | bc -l)" == "1" ]]; then
        gen_tps=$(echo "scale=2; $gen_tok / $wall_time" | bc -l)
    else
        gen_tps="0"
    fi

    echo "${http_code} ${wall_time} ${prompt_tok} ${gen_tok} 0 ${gen_tps}"
}

# Send a streaming chat completion request and measure TTFT.
# Output: space-separated: http_code wall_time_s ttft_s gen_tok
send_stream() {
    local messages="$1"
    local max_tok="${2:-$MAX_TOKENS}"
    local payload
    payload=$(jq -n \
        --arg model "$MODEL" \
        --argjson messages "$messages" \
        --argjson max_tokens "$max_tok" \
        '{model: $model, messages: $messages, max_tokens: $max_tokens, stream: true}')

    local tmpfile="/tmp/bench-model-stream-$$.tmp"
    local start_ns wall_ns ttft_ns

    # Record start time (nanoseconds)
    start_ns=$(python3 -c "import time; print(int(time.time()*1e9))")

    # Stream response, capture first data chunk time
    local http_code first_chunk_seen=false token_count=0
    http_code=$(curl -s --max-time "$TIMEOUT" \
        -H "Content-Type: application/json" \
        -o "$tmpfile" \
        -w '%{http_code}' \
        -d "$payload" \
        "$API_URL")

    wall_ns=$(python3 -c "import time; print(int(time.time()*1e9))")

    if [[ "$http_code" != "200" ]]; then
        echo "${http_code} 0 0 0"
        rm -f "$tmpfile"
        return
    fi

    # Parse SSE stream for TTFT and token count.
    # TTFT: We can't measure mid-stream from a file dump, so we use a
    # different approach: pipe curl and timestamp first content chunk.
    # For file-based approach, estimate from chunk structure.
    local wall_time_s ttft_s
    wall_time_s=$(python3 -c "print(f'{($wall_ns - $start_ns) / 1e9:.4f}')")

    # Count tokens from SSE chunks with content
    token_count=$(grep -c '"content"' "$tmpfile" 2>/dev/null || echo "0")

    # Estimate TTFT: not possible from file dump, use streaming curl instead
    rm -f "$tmpfile"

    # Re-run with streaming timestamp measurement
    local ttft_file="/tmp/bench-model-ttft-$$.tmp"
    start_ns=$(python3 -c "import time; print(int(time.time()*1e9))")

    curl -s --max-time "$TIMEOUT" \
        -H "Content-Type: application/json" \
        -N \
        -d "$payload" \
        "$API_URL" 2>/dev/null | while IFS= read -r line; do
        if [[ "$first_chunk_seen" == "false" ]] && echo "$line" | grep -q '"content"'; then
            python3 -c "import time; print(int(time.time()*1e9))" > "$ttft_file"
            first_chunk_seen=true
        fi
    done

    wall_ns=$(python3 -c "import time; print(int(time.time()*1e9))")
    wall_time_s=$(python3 -c "print(f'{($wall_ns - $start_ns) / 1e9:.4f}')")

    if [[ -f "$ttft_file" ]]; then
        ttft_ns=$(cat "$ttft_file")
        ttft_s=$(python3 -c "print(f'{($ttft_ns - $start_ns) / 1e9:.4f}')")
    else
        ttft_s="$wall_time_s"
    fi
    rm -f "$ttft_file"

    echo "${http_code} ${wall_time_s} ${ttft_s} ${token_count}"
}

# ---------------------------------------------------------------------------
# Warmup
# ---------------------------------------------------------------------------
run_warmup() {
    local count="$WARMUP"
    if [[ "$count" -eq 0 ]]; then
        return
    fi
    log "Running ${count} warmup request(s)..."
    local messages
    messages=$(jq -n '[{role: "user", content: "Say hello in one word."}]')
    for i in $(seq 1 "$count"); do
        local raw
        raw=$(send_chat "$messages" 16)
        local parsed http_code
        parsed=$(parse_response "$raw")
        http_code=$(echo "$parsed" | awk '{print $1}')
        if [[ "$http_code" == "200" ]]; then
            ok "  Warmup ${i}/${count}: OK"
        else
            warn "  Warmup ${i}/${count}: HTTP ${http_code}"
        fi
    done
}

# ---------------------------------------------------------------------------
# Result collection
# ---------------------------------------------------------------------------

# Global results array (JSON lines)
RESULTS_JSONL=""

add_result() {
    local phase="$1" iter="$2" http_code="$3" wall_time="$4"
    local prompt_tok="$5" gen_tok="$6" prompt_tps="$7" gen_tps="$8"
    local extra="${9:-}"

    local entry
    entry=$(jq -n \
        --arg phase "$phase" \
        --argjson iter "$iter" \
        --argjson http_code "$http_code" \
        --argjson wall_time "$wall_time" \
        --argjson prompt_tok "$prompt_tok" \
        --argjson gen_tok "$gen_tok" \
        --argjson prompt_tps "$prompt_tps" \
        --argjson gen_tps "$gen_tps" \
        --arg extra "$extra" \
        '{phase: $phase, iteration: $iter, http_code: $http_code,
          wall_time_s: $wall_time, prompt_tokens: $prompt_tok,
          gen_tokens: $gen_tok, prompt_tps: $prompt_tps,
          gen_tps: $gen_tps, extra: $extra}')

    RESULTS_JSONL="${RESULTS_JSONL}${entry}\n"
}

# Compute stats for a phase from RESULTS_JSONL.
# Output: prompt_tps_avg gen_tps_avg wall_avg prompt_tok_avg gen_tok_avg samples
compute_phase_stats() {
    local phase="$1"
    echo -e "$RESULTS_JSONL" | grep -v '^$' | jq -s --arg phase "$phase" '
        [.[] | select(.phase == $phase and .http_code == 200)] |
        if length == 0 then "0 0 0 0 0 0"
        else
            (map(.prompt_tps) | add / length) as $ptps |
            (map(.gen_tps) | add / length) as $gtps |
            (map(.wall_time_s) | add / length) as $wt |
            (map(.prompt_tokens) | add / length) as $pt |
            (map(.gen_tokens) | add / length) as $gt |
            "\($ptps | . * 100 | round / 100) \($gtps | . * 100 | round / 100) \($wt | . * 100 | round / 100) \($pt | round) \($gt | round) \(length)"
        end
    ' -r
}

# ---------------------------------------------------------------------------
# Phases
# ---------------------------------------------------------------------------

phase_short() {
    log "${BOLD}Phase: short${NC} — ~100 token prompt, generation-bound"
    local messages
    messages=$(jq -n --arg content "$(prompt_short)" '[{role: "user", content: $content}]')

    for i in $(seq 1 "$ITERATIONS"); do
        local raw parsed
        raw=$(send_chat "$messages")
        parsed=$(parse_response "$raw")

        local http_code wall_time prompt_tok gen_tok prompt_tps gen_tps
        http_code=$(echo "$parsed" | awk '{print $1}')
        wall_time=$(echo "$parsed" | awk '{print $2}')
        prompt_tok=$(echo "$parsed" | awk '{print $3}')
        gen_tok=$(echo "$parsed" | awk '{print $4}')
        prompt_tps=$(echo "$parsed" | awk '{print $5}')
        gen_tps=$(echo "$parsed" | awk '{print $6}')

        if [[ "$http_code" == "200" ]]; then
            ok "  [${i}/${ITERATIONS}] ${gen_tok} tok in ${wall_time}s (gen: ${gen_tps} tok/s, pp: ${prompt_tps} tok/s)"
        else
            err "  [${i}/${ITERATIONS}] HTTP ${http_code}"
        fi
        add_result "short" "$i" "$http_code" "$wall_time" "$prompt_tok" "$gen_tok" "$prompt_tps" "$gen_tps"
    done
}

phase_medium() {
    log "${BOLD}Phase: medium${NC} — ~1000 token prompt, balanced workload"
    local messages
    messages=$(jq -n --arg content "$(prompt_medium)" '[{role: "user", content: $content}]')

    for i in $(seq 1 "$ITERATIONS"); do
        local raw parsed
        raw=$(send_chat "$messages")
        parsed=$(parse_response "$raw")

        local http_code wall_time prompt_tok gen_tok prompt_tps gen_tps
        http_code=$(echo "$parsed" | awk '{print $1}')
        wall_time=$(echo "$parsed" | awk '{print $2}')
        prompt_tok=$(echo "$parsed" | awk '{print $3}')
        gen_tok=$(echo "$parsed" | awk '{print $4}')
        prompt_tps=$(echo "$parsed" | awk '{print $5}')
        gen_tps=$(echo "$parsed" | awk '{print $6}')

        if [[ "$http_code" == "200" ]]; then
            ok "  [${i}/${ITERATIONS}] ${gen_tok} tok in ${wall_time}s (gen: ${gen_tps} tok/s, pp: ${prompt_tps} tok/s)"
        else
            err "  [${i}/${ITERATIONS}] HTTP ${http_code}"
        fi
        add_result "medium" "$i" "$http_code" "$wall_time" "$prompt_tok" "$gen_tok" "$prompt_tps" "$gen_tps"
    done
}

phase_long() {
    log "${BOLD}Phase: long${NC} — ~4000 token prompt, prompt-processing throughput"
    local messages
    messages=$(jq -n --arg content "$(prompt_long)" '[{role: "user", content: $content}]')

    for i in $(seq 1 "$ITERATIONS"); do
        local raw parsed
        raw=$(send_chat "$messages" 512)
        parsed=$(parse_response "$raw")

        local http_code wall_time prompt_tok gen_tok prompt_tps gen_tps
        http_code=$(echo "$parsed" | awk '{print $1}')
        wall_time=$(echo "$parsed" | awk '{print $2}')
        prompt_tok=$(echo "$parsed" | awk '{print $3}')
        gen_tok=$(echo "$parsed" | awk '{print $4}')
        prompt_tps=$(echo "$parsed" | awk '{print $5}')
        gen_tps=$(echo "$parsed" | awk '{print $6}')

        if [[ "$http_code" == "200" ]]; then
            ok "  [${i}/${ITERATIONS}] pp=${prompt_tok}tok gen=${gen_tok}tok wall=${wall_time}s (pp: ${prompt_tps} tok/s, gen: ${gen_tps} tok/s)"
        else
            err "  [${i}/${ITERATIONS}] HTTP ${http_code}"
        fi
        add_result "long" "$i" "$http_code" "$wall_time" "$prompt_tok" "$gen_tok" "$prompt_tps" "$gen_tps"
    done
}

phase_multiturn() {
    log "${BOLD}Phase: multiturn${NC} — growing context across 4 turns"

    # Build up a conversation
    local turns=(
        "Write a Python class called TaskQueue that implements a priority queue with add, pop, and peek methods."
        "Add a method called 'batch_pop' that removes and returns the top N items. Handle the case where N is larger than the queue size."
        "Now add type hints to all methods and write comprehensive docstrings. Also add a __len__ and __bool__ method."
        "Write unit tests for all methods using pytest. Include edge cases: empty queue, single item, duplicate priorities, batch_pop with N > size."
    )

    local messages='[]'

    for turn_idx in "${!turns[@]}"; do
        local turn_num=$((turn_idx + 1))
        local content="${turns[$turn_idx]}"

        # Add user message
        messages=$(echo "$messages" | jq --arg content "$content" '. + [{role: "user", content: $content}]')

        log "  Turn ${turn_num}/4: sending (context: $(echo "$messages" | jq 'length') messages)..."

        local raw parsed
        raw=$(send_chat "$messages" "$MAX_TOKENS")
        parsed=$(parse_response "$raw")

        local http_code wall_time prompt_tok gen_tok prompt_tps gen_tps
        http_code=$(echo "$parsed" | awk '{print $1}')
        wall_time=$(echo "$parsed" | awk '{print $2}')
        prompt_tok=$(echo "$parsed" | awk '{print $3}')
        gen_tok=$(echo "$parsed" | awk '{print $4}')
        prompt_tps=$(echo "$parsed" | awk '{print $5}')
        gen_tps=$(echo "$parsed" | awk '{print $6}')

        if [[ "$http_code" == "200" ]]; then
            ok "  Turn ${turn_num}: ${gen_tok} tok in ${wall_time}s (gen: ${gen_tps} tok/s, pp: ${prompt_tps} tok/s, ctx: ${prompt_tok} tok)"

            # Extract assistant reply and append to messages for next turn
            local body
            body=$(echo "$raw" | grep -v '_curl_http_code')
            local reply
            reply=$(echo "$body" | jq -r '.choices[0].message.content // ""' 2>/dev/null || echo "")
            if [[ -n "$reply" ]]; then
                messages=$(echo "$messages" | jq --arg reply "$reply" '. + [{role: "assistant", content: $reply}]')
            fi
        else
            err "  Turn ${turn_num}: HTTP ${http_code}"
        fi
        add_result "multiturn" "$turn_num" "$http_code" "$wall_time" "$prompt_tok" "$gen_tok" "$prompt_tps" "$gen_tps" "turn=${turn_num}"
    done
}

phase_stream() {
    log "${BOLD}Phase: stream${NC} — TTFT measurement via streaming"
    local messages
    messages=$(jq -n --arg content "$(prompt_stream)" '[{role: "user", content: $content}]')

    for i in $(seq 1 "$ITERATIONS"); do
        local result http_code wall_time ttft token_count
        result=$(send_stream "$messages" "$MAX_TOKENS")
        http_code=$(echo "$result" | awk '{print $1}')
        wall_time=$(echo "$result" | awk '{print $2}')
        ttft=$(echo "$result" | awk '{print $3}')
        token_count=$(echo "$result" | awk '{print $4}')

        if [[ "$http_code" == "200" ]]; then
            ok "  [${i}/${ITERATIONS}] TTFT: ${ttft}s, wall: ${wall_time}s, chunks: ${token_count}"
        else
            err "  [${i}/${ITERATIONS}] HTTP ${http_code}"
        fi
        add_result "stream" "$i" "$http_code" "$wall_time" "0" "$token_count" "0" "0" "ttft=${ttft}"
    done
}

# ---------------------------------------------------------------------------
# Summary output
# ---------------------------------------------------------------------------

print_summary() {
    echo ""
    log "${BOLD}=== Summary ===${NC}"
    echo ""
    printf "  %-12s %8s %8s %8s %8s %8s %7s\n" \
        "Phase" "PP tok/s" "Gen t/s" "Wall(s)" "PP tok" "Gen tok" "N"
    printf "  %-12s %8s %8s %8s %8s %8s %7s\n" \
        "------------" "--------" "--------" "--------" "--------" "--------" "-------"

    for phase in short medium long multiturn stream; do
        local stats
        stats=$(compute_phase_stats "$phase")
        local ptps gtps wt pt gt n
        ptps=$(echo "$stats" | awk '{print $1}')
        gtps=$(echo "$stats" | awk '{print $2}')
        wt=$(echo "$stats" | awk '{print $3}')
        pt=$(echo "$stats" | awk '{print $4}')
        gt=$(echo "$stats" | awk '{print $5}')
        n=$(echo "$stats" | awk '{print $6}')

        if [[ "$n" == "0" ]]; then
            continue
        fi

        printf "  %-12s %8s %8s %8s %8s %8s %7s\n" \
            "$phase" "$ptps" "$gtps" "$wt" "$pt" "$gt" "$n"
    done

    # Print TTFT stats for stream phase
    local stream_results
    stream_results=$(echo -e "$RESULTS_JSONL" | grep -v '^$' | jq -s '[.[] | select(.phase == "stream" and .http_code == 200)]' 2>/dev/null)
    local stream_count
    stream_count=$(echo "$stream_results" | jq 'length' 2>/dev/null || echo "0")
    if [[ "$stream_count" -gt 0 ]]; then
        local ttft_avg
        ttft_avg=$(echo "$stream_results" | jq -r '
            [.[] | .extra | capture("ttft=(?<t>[0-9.]+)") | .t | tonumber] |
            if length > 0 then (add / length | . * 1000 | round / 1000 | tostring) + "s"
            else "-" end
        ' 2>/dev/null || echo "-")
        echo ""
        log "  Avg TTFT (stream): ${ttft_avg}"
    fi

    echo ""
}

write_json_report() {
    local results_array
    results_array=$(echo -e "$RESULTS_JSONL" | grep -v '^$' | jq -s '.')

    # Build phase summaries
    local summaries='[]'
    for phase in short medium long multiturn stream; do
        local stats
        stats=$(compute_phase_stats "$phase")
        local n
        n=$(echo "$stats" | awk '{print $6}')
        if [[ "$n" == "0" ]]; then
            continue
        fi
        local entry
        entry=$(echo "$stats" | awk -v p="$phase" '{
            printf "{\"phase\":\"%s\",\"prompt_tps_avg\":%s,\"gen_tps_avg\":%s,\"wall_time_avg\":%s,\"prompt_tokens_avg\":%s,\"gen_tokens_avg\":%s,\"samples\":%s}", p, $1, $2, $3, $4, $5, $6
        }')
        summaries=$(echo "$summaries" | jq --argjson e "$entry" '. + [$e]')
    done

    jq -n \
        --arg run_id "$RUN_ID" \
        --arg git_sha "$GIT_SHA" \
        --arg model "$MODEL" \
        --arg endpoint "$ENDPOINT" \
        --argjson max_tokens "$MAX_TOKENS" \
        --argjson iterations "$ITERATIONS" \
        --argjson warmup "$WARMUP" \
        --arg timestamp "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" \
        --argjson results "$results_array" \
        --argjson summaries "$summaries" \
        '{
            run_id: $run_id,
            git_sha: $git_sha,
            model: $model,
            endpoint: $endpoint,
            config: {max_tokens: $max_tokens, iterations: $iterations, warmup: $warmup},
            timestamp: $timestamp,
            summaries: $summaries,
            results: $results
        }' > "$REPORT_JSON"

    ok "JSON report: ${REPORT_JSON}"
}

# ---------------------------------------------------------------------------
# Connectivity check
# ---------------------------------------------------------------------------
check_endpoint() {
    log "Checking endpoint: ${API_URL}"
    local test_payload
    test_payload=$(jq -n --arg model "$MODEL" \
        '{model: $model, messages: [{role: "user", content: "ping"}], max_tokens: 1, stream: false}')

    local http_code
    http_code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 \
        -H "Content-Type: application/json" \
        -d "$test_payload" \
        "$API_URL" 2>/dev/null || echo "000")

    if [[ "$http_code" == "000" ]]; then
        err "Cannot connect to ${ENDPOINT}"
        err "Is the proxy running? Try: ENDPOINT=http://host:port $0"
        exit 1
    elif [[ "$http_code" != "200" ]]; then
        warn "Connectivity check returned HTTP ${http_code} (may be OK if model needs cold start)"
    else
        ok "Endpoint reachable (HTTP ${http_code})"
    fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------
main() {
    local phase="${1:-all}"

    case "$phase" in
        -h|--help)
            echo "Usage: $0 [short|medium|long|multiturn|stream|all]"
            echo ""
            echo "Phases:"
            echo "  short       ~100 token prompt, generation throughput"
            echo "  medium      ~1000 token prompt, balanced workload"
            echo "  long        ~4000 token prompt, prompt processing"
            echo "  multiturn   Growing context across 4 turns"
            echo "  stream      Streaming TTFT measurement"
            echo "  all         Run all phases (default)"
            echo ""
            echo "Environment:"
            echo "  ENDPOINT    Proxy base URL (default: http://localhost:8080)"
            echo "  MODEL       Model name (default: qwen3-14b-claude-distill)"
            echo "  ITERATIONS  Iterations per phase (default: 3)"
            echo "  WARMUP      Warmup requests (default: 1)"
            echo "  MAX_TOKENS  Max generation tokens (default: 256)"
            echo "  TIMEOUT     Per-request timeout seconds (default: 120)"
            echo "  REPORT_DIR  Report output dir (default: /tmp)"
            echo ""
            echo "Output:"
            echo "  JSON report: \$REPORT_DIR/bench-model-\$MODEL-\$RUN_ID.json"
            exit 0
            ;;
        short|medium|long|multiturn|stream|all) ;;
        *)
            err "Unknown phase: ${phase}"
            echo "Usage: $0 [short|medium|long|multiturn|stream|all]"
            exit 1
            ;;
    esac

    log "FlexInfer Model Benchmark"
    log "Run ID:     ${RUN_ID}"
    log "Git SHA:    ${GIT_SHA}"
    log "Model:      ${MODEL}"
    log "Endpoint:   ${ENDPOINT}"
    log "Iterations: ${ITERATIONS}"
    log "Warmup:     ${WARMUP}"
    log "Max tokens: ${MAX_TOKENS}"
    log "Phase:      ${phase}"
    echo ""

    check_endpoint
    run_warmup

    echo ""

    case "$phase" in
        short)     phase_short ;;
        medium)    phase_medium ;;
        long)      phase_long ;;
        multiturn) phase_multiturn ;;
        stream)    phase_stream ;;
        all)
            phase_short;     echo ""
            phase_medium;    echo ""
            phase_long;      echo ""
            phase_multiturn; echo ""
            phase_stream
            ;;
    esac

    print_summary
    write_json_report
}

main "$@"
