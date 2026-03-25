#!/usr/bin/env bash
set -euo pipefail

# Dev upgrade routine: build, atomically install binaries, regen+sync configs and skills
# in loom mode, and (optionally) restart the daemon only when idle.
#
# Path safety:
# - Always installs to INSTALL_DIR (default ~/.local/bin)
# - Also installs to the currently active `loom` PATH directory when user-writable
#   (prevents stale binaries when PATH prefers e.g. ~/go/bin/loom)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
RESTART_DAEMON="${RESTART_DAEMON:-auto}" # auto|always|never
HUD_URL_SCRIPT="$ROOT/scripts/dev/detect_hud_url.sh"
LAUNCHD_DAEMON_PLIST="$HOME/Library/LaunchAgents/com.loom.daemon.plist"

cd "$ROOT"

resolve_dir() {
  local d="$1"
  mkdir -p "$d"
  (cd "$d" && pwd -P)
}

dir_in_list() {
  local needle="$1"
  shift
  local d
  for d in "$@"; do
    if [[ "$d" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

list_proxy_pids() {
  ps -Ao pid=,command= | awk -v self="$$" '
    {
      pid = $1
      $1 = ""
      sub(/^[[:space:]]+/, "", $0)
      if (pid != self && $0 ~ /(^|\/)loom proxy([[:space:]]|$)/) {
        print pid
      }
    }
  '
}

reap_proxy_processes() {
  local pids=()
  local pid
  local remaining=()
  local attempt

  while IFS= read -r pid; do
    [[ -n "$pid" ]] && pids+=("$pid")
  done < <(list_proxy_pids)

  if [[ "${#pids[@]}" -eq 0 ]]; then
    echo "No existing loom proxy clients to reap"
    return 0
  fi

  echo "Reaping loom proxy clients: ${pids[*]}"
  kill "${pids[@]}" 2>/dev/null || true

  remaining=("${pids[@]}")
  for attempt in 1 2 3 4 5; do
    sleep 0.2
    remaining=()
    for pid in "${pids[@]}"; do
      if kill -0 "$pid" 2>/dev/null; then
        remaining+=("$pid")
      fi
    done
    if [[ "${#remaining[@]}" -eq 0 ]]; then
      echo "Proxy cleanup complete"
      return 0
    fi
  done

  echo "Force-killing stubborn loom proxy clients: ${remaining[*]}"
  kill -9 "${remaining[@]}" 2>/dev/null || true
}

get_mobile_operator_token() {
  if [[ -n "${HUD_MOBILE_OPERATOR_TOKEN:-}" ]]; then
    printf '%s\n' "$HUD_MOBILE_OPERATOR_TOKEN"
    return 0
  fi

  local hud_env="$HOME/.config/loom/hud.env"
  local token=""
  if [[ -f "$hud_env" ]]; then
    token="$(awk -F= '/^HUD_MOBILE_OPERATOR_TOKEN=/{print substr($0, index($0, "=") + 1); exit}' "$hud_env")"
    if [[ -n "$token" ]]; then
      printf '%s\n' "$token"
      return 0
    fi
  fi

  local token_file="${HUD_MOBILE_OPERATOR_TOKEN_FILE:-$HOME/.config/loom/mobile-operator-token}"
  if [[ -f "$token_file" ]]; then
    sed -n '1s/[[:space:]]*$//p' "$token_file"
  fi
}

get_hud_port() {
  local port_file="${XDG_CONFIG_HOME:-$HOME/.config}/loom/hud.port"
  local port="${HUD_PORT:-}"

  if [[ -f "$port_file" ]]; then
    port="$(tr -d '[:space:]' < "$port_file")"
  fi
  if [[ -z "$port" || ! "$port" =~ ^[0-9]+$ ]]; then
    port="3333"
  fi

  printf '%s\n' "$port"
}

detect_hud_base_url() {
  local port="${1:-$(get_hud_port)}"
  if [[ -x "$HUD_URL_SCRIPT" ]]; then
    "$HUD_URL_SCRIPT" "$port"
    return 0
  fi
  printf 'http://127.0.0.1:%s\n' "$port"
}

launchd_daemon_loaded() {
  [[ -f "$LAUNCHD_DAEMON_PLIST" ]] || return 1
  launchctl print "gui/$(id -u)/com.loom.daemon" >/dev/null 2>&1
}

probe_hud_status() {
  local base_url="${1:-$(detect_hud_base_url)}"
  [[ -z "$base_url" ]] && return 1

  python3 - "$base_url" <<'PY'
import json
import sys
import urllib.error
import urllib.request

req = urllib.request.Request(f"{sys.argv[1]}/api/status")
try:
    with urllib.request.urlopen(req, timeout=3) as resp:
        payload = json.load(resp)
except (urllib.error.URLError, TimeoutError, json.JSONDecodeError, ValueError):
    raise SystemExit(1)

if isinstance(payload, dict) and payload.get("running") is False:
    raise SystemExit(1)

raise SystemExit(0)
PY
}

probe_mobile_endpoint() {
  local token="$1"
  local endpoint="${2:-ping}"
  local base_url="${3:-$(detect_hud_base_url)}"
  [[ -z "$token" || -z "$base_url" ]] && return 1

  python3 - "$token" "$endpoint" "$base_url" <<'PY'
import json
import sys
import urllib.error
import urllib.request

req = urllib.request.Request(
    f"{sys.argv[3]}/api/mobile/v1/{sys.argv[2]}",
    headers={"Authorization": f"Bearer {sys.argv[1]}"},
)
try:
    with urllib.request.urlopen(req, timeout=3) as resp:
        payload = json.load(resp)
except (urllib.error.URLError, TimeoutError, json.JSONDecodeError, ValueError):
    raise SystemExit(1)

ok = payload.get("ok") is True
if not ok:
    raise SystemExit(1)

if sys.argv[2] == "ping":
    raise SystemExit(0 if payload.get("data", {}).get("pong") is True else 1)

raise SystemExit(0)
PY
}

wait_for_hud_status() {
  local attempts="${1:-8}"
  local delay="${2:-2}"
  local i

  for ((i = 1; i <= attempts; i++)); do
    if probe_hud_status "$(detect_hud_base_url)"; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

wait_for_mobile_endpoint() {
  local token="$1"
  local endpoint="${2:-ping}"
  local attempts="${3:-8}"
  local delay="${4:-2}"
  local i

  for ((i = 1; i <= attempts; i++)); do
    if probe_mobile_endpoint "$token" "$endpoint" "$(detect_hud_base_url)"; then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

wait_for_daemon_lock_release() {
  local attempts="${1:-50}"
  local delay="${2:-0.2}"
  local lock_path="${XDG_CONFIG_HOME:-$HOME/.config}/loom/loomd.lock"
  local i

  for ((i = 1; i <= attempts; i++)); do
    if python3 - "$lock_path" <<'PY'
import fcntl
import os
import sys

path = sys.argv[1]
os.makedirs(os.path.dirname(path), exist_ok=True)
with open(path, "a+", encoding="utf-8") as handle:
    try:
        fcntl.flock(handle.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        raise SystemExit(1)
    fcntl.flock(handle.fileno(), fcntl.LOCK_UN)
raise SystemExit(0)
PY
    then
      return 0
    fi
    sleep "$delay"
  done
  return 1
}

start_direct_daemon_fallback() {
  local registry_path="$1"
  local log_dir="$HOME/.config/loom/logs"
  local daemon_log="$log_dir/daemon.log"
  local daemon_err="$log_dir/daemon.err"
  local hud_env="$HOME/.config/loom/hud.env"
  local hud_port="${2:-$(get_hud_port)}"
  local reason="${3:-Launchd daemon restart came back unhealthy; switching to direct daemon fallback}"

  echo "$reason"

  if [[ -f "$LAUNCHD_DAEMON_PLIST" ]]; then
    launchctl unload "$LAUNCHD_DAEMON_PLIST" 2>/dev/null || true
  fi

  "$RUN_LOOM" stop >/dev/null 2>&1 || true
  if ! wait_for_daemon_lock_release 60 0.2; then
    echo "WARNING: daemon lock still held after stop request; continuing with direct restart" >&2
  fi

  if [[ -f "$hud_env" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$hud_env"
    set +a
  fi

  export CACHE_BACKEND="${CACHE_BACKEND:-redis}"
  export REDIS_URL="${REDIS_URL:-redis://localhost:6379}"
  export HUD_BIND_ADDRESS="${HUD_BIND_ADDRESS:-0.0.0.0}"
  export HUD_PIPELINE_PROJECTS="${HUD_PIPELINE_PROJECTS:-services/loom-core}"

  mkdir -p "$log_dir"
  python3 - "$RUN_LOOMD" "$registry_path" "$daemon_log" "$daemon_err" "$hud_port" <<'PY'
import os
import subprocess
import sys

daemon, registry, stdout_path, stderr_path, hud_port = sys.argv[1:6]
with open(stdout_path, "ab", buffering=0) as stdout, open(stderr_path, "ab", buffering=0) as stderr:
    proc = subprocess.Popen(
        [daemon, "--registry", registry, "--hud-port", hud_port],
        stdin=subprocess.DEVNULL,
        stdout=stdout,
        stderr=stderr,
        start_new_session=True,
        close_fds=True,
        env=os.environ.copy(),
    )
print(proc.pid)
PY
  if wait_for_hud_status 60 1; then
    echo "Embedded HUD listener recovered via direct daemon fallback"
  else
    echo "WARNING: embedded HUD listener still not ready after direct daemon fallback"
  fi
}

restart_daemon_for_dev() {
  local registry_path="$1"
  local hud_port="${2:-$(get_hud_port)}"

  if launchd_daemon_loaded; then
    if "$RUN_LOOM" restart --registry "$registry_path"; then
      return 0
    fi
    start_direct_daemon_fallback "$registry_path" "$hud_port" \
      "Launchd-managed daemon restart failed; switching to direct daemon restart with embedded HUD"
    return 0
  fi

  if [[ -f "$LAUNCHD_DAEMON_PLIST" ]]; then
    start_direct_daemon_fallback "$registry_path" "$hud_port" \
      "Launchd daemon plist is present but not loaded; using direct daemon restart with embedded HUD"
    return 0
  fi

  start_direct_daemon_fallback "$registry_path" "$hud_port" \
    "Launchd daemon service is not installed; using direct daemon restart with embedded HUD"
}

echo "== Build =="
make loom loomd mcp-hub-wrapper >/dev/null

echo "== Install (atomic) =="
chmod +x scripts/install_atomic.sh
PRIMARY_DIR="$(resolve_dir "$INSTALL_DIR")"
declare -a INSTALL_TARGET_DIRS=("$PRIMARY_DIR")

ACTIVE_LOOM_BIN="$(command -v loom 2>/dev/null || true)"
if [[ -n "${ACTIVE_LOOM_BIN:-}" ]]; then
  ACTIVE_LOOM_DIR="$(resolve_dir "$(dirname "$ACTIVE_LOOM_BIN")")"
  if ! dir_in_list "$ACTIVE_LOOM_DIR" "${INSTALL_TARGET_DIRS[@]}"; then
    case "$ACTIVE_LOOM_DIR" in
      "$HOME"|"$HOME"/*)
        if [[ -w "$ACTIVE_LOOM_DIR" ]]; then
          INSTALL_TARGET_DIRS+=("$ACTIVE_LOOM_DIR")
        else
          echo "WARNING: active loom dir not writable, skipping extra install: $ACTIVE_LOOM_DIR"
        fi
        ;;
      *)
        echo "WARNING: active loom dir is outside HOME, skipping extra install: $ACTIVE_LOOM_DIR"
        ;;
    esac
  fi
fi

for dir in "${INSTALL_TARGET_DIRS[@]}"; do
  scripts/install_atomic.sh "$ROOT/bin/loom"  "$dir/loom"
  scripts/install_atomic.sh "$ROOT/bin/loomd" "$dir/loomd"
  scripts/install_atomic.sh "$ROOT/bin/mcp-hub-wrapper" "$dir/mcp-hub-wrapper"
done

RUN_LOOM="$PRIMARY_DIR/loom"
RUN_LOOMD="$PRIMARY_DIR/loomd"

echo "Installed targets:"
for dir in "${INSTALL_TARGET_DIRS[@]}"; do
  echo "  - $dir/loom"
done
echo "Versions:"
"$RUN_LOOM" --version || true
"$RUN_LOOMD" --version 2>/dev/null || true

if [[ -n "${ACTIVE_LOOM_BIN:-}" ]]; then
  ACTIVE_VER="$("$ACTIVE_LOOM_BIN" --version 2>/dev/null || true)"
  EXPECTED_VER="$("$RUN_LOOM" --version 2>/dev/null || true)"
  if [[ -n "$ACTIVE_VER" && -n "$EXPECTED_VER" && "$ACTIVE_VER" != "$EXPECTED_VER" ]]; then
    echo "ERROR: active PATH loom is stale after install."
    echo "  command -v loom -> $ACTIVE_LOOM_BIN"
    echo "  active version  -> $ACTIVE_VER"
    echo "  expected        -> $EXPECTED_VER"
    echo "Fix PATH order or set INSTALL_DIR to the active loom directory."
    exit 3
  fi
fi

echo "== Regen + Sync (loom mode) =="
"$RUN_LOOM" sync all --regen --loom-mode --loom-binary "$RUN_LOOM"
"$RUN_LOOM" sync skills all

echo "== Daemon =="
DAEMON_RESTARTED=false
REGISTRY_PATH="$HOME/.config/loom/registry.yaml"
HUD_PORT_VALUE="$(get_hud_port)"
case "$RESTART_DAEMON" in
  never)
    echo "Skipping daemon restart (RESTART_DAEMON=never)"
    ;;
  always)
    restart_daemon_for_dev "$REGISTRY_PATH" "$HUD_PORT_VALUE"
    DAEMON_RESTARTED=true
    ;;
  auto)
    # Prefer drain_ready field from status output (added in TD-SESSION-05).
    # Falls back to legacy "Connections: X active" parsing for older daemons.
    if out=$("$RUN_LOOM" status 2>/dev/null); then
      drain_ready="$(echo "$out" | awk -F'drain_ready=' '/drain_ready=/{print $2}' | tr -d '[:space:]' || true)"
      if [[ "$drain_ready" == "true" ]]; then
        restart_daemon_for_dev "$REGISTRY_PATH" "$HUD_PORT_VALUE"
        DAEMON_RESTARTED=true
      elif [[ "$drain_ready" == "false" ]]; then
        echo "Daemon not drain-ready (in-flight RPCs); skipping restart (set RESTART_DAEMON=always to force)"
      else
        # Legacy fallback: parse "Connections: X active, Y idle"
        active="$(echo "$out" | awk '/^Connections:/{print $2}' | tr -d '[:space:]' || true)"
        if [[ -n "${active:-}" && "${active:-0}" =~ ^[0-9]+$ && "${active:-0}" -eq 0 ]]; then
          restart_daemon_for_dev "$REGISTRY_PATH" "$HUD_PORT_VALUE"
          DAEMON_RESTARTED=true
        else
          echo "Daemon has active connections (${active:-unknown}); skipping restart (set RESTART_DAEMON=always to force)"
        fi
      fi
    else
      echo "Could not query daemon status; skipping restart"
    fi
    ;;
  *)
    echo "Unknown RESTART_DAEMON=$RESTART_DAEMON (expected auto|always|never)" >&2
    exit 2
    ;;
esac

if [[ "$DAEMON_RESTARTED" == "true" ]]; then
  echo "== Proxy Cleanup =="
  reap_proxy_processes
fi

echo "== HUD =="
echo "HUD is now embedded in loomd (--hud-port). Daemon restart covers HUD restart."
# Clean up legacy HUD plist if present.
HUD_PLIST="$HOME/Library/LaunchAgents/com.loom.hud.plist"
if [ -f "$HUD_PLIST" ]; then
  launchctl stop com.loom.hud 2>/dev/null || true
  echo "Stopped legacy HUD launchd agent (consider removing $HUD_PLIST)"
fi

if [[ "$DAEMON_RESTARTED" == "true" ]]; then
  echo "== Proxy Cleanup (post-restart check) =="
  reap_proxy_processes
fi

echo "== Smoke (proxy initialize) =="
python3 - "$RUN_LOOM" <<'PY'
import json, subprocess, sys
msg = {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"upgrade-smoke","version":"0"}}}
p = subprocess.Popen([sys.argv[1], "proxy"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
out, err = p.communicate(json.dumps(msg) + "\n", timeout=5)
if p.returncode != 0:
  raise SystemExit(f"proxy exited {p.returncode}: {err[:400]}")
if not out.strip():
  raise SystemExit("proxy produced no output")
resp = json.loads(out.splitlines()[0])
assert resp.get("result", {}).get("serverInfo", {}).get("name") == "loom", resp
print("OK:", resp["result"]["serverInfo"]["name"], "v"+resp["result"]["serverInfo"]["version"])
PY

if [[ "$DAEMON_RESTARTED" == "true" ]]; then
  echo "== Mobile Smoke =="
  MOBILE_OPERATOR_TOKEN="$(get_mobile_operator_token || true)"
  if [[ -n "${MOBILE_OPERATOR_TOKEN:-}" ]]; then
    if wait_for_mobile_endpoint "$MOBILE_OPERATOR_TOKEN" ping 6 2; then
      echo "Mobile API endpoint OK"
    else
      start_direct_daemon_fallback "$REGISTRY_PATH" "$HUD_PORT_VALUE" \
        "Daemon restart finished without a healthy embedded HUD/mobile API; switching to direct daemon restart with embedded HUD"
      if wait_for_mobile_endpoint "$MOBILE_OPERATOR_TOKEN" ping 24 2; then
        echo "Mobile API endpoint recovered via direct daemon fallback"
      else
        echo "WARNING: mobile API endpoint still unhealthy after direct daemon fallback"
      fi
    fi
    if wait_for_mobile_endpoint "$MOBILE_OPERATOR_TOKEN" tasks 4 2; then
      echo "Mobile task feed OK"
    else
      echo "Mobile task feed still warming; continuing"
    fi
  else
    echo "Skipped mobile smoke (no operator token found)"
  fi
fi

echo "OK"
