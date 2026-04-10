#!/usr/bin/env bash
# devbox-sync.sh — Sync workspace to the live devbox NFS backing store.
#
# Usage:
#   ./scripts/devbox-sync.sh              # Full workspace sync
#   ./scripts/devbox-sync.sh loom-core    # Sync only loom-core project
#   ./scripts/devbox-sync.sh --watch      # Watch mode (continuous sync)
#
# The devbox PVC in K3s mounts:
#   192.168.50.211:/srv/nas/pilot3/nas-media-bulk/devbox-ws
# For local syncs on macOS, prefer mounting the parent export with
# scripts/mount-devbox-nfs.sh and writing through /Volumes/nas-media-bulk/devbox-ws.
# If that mount is unavailable, this script falls back to rsync-over-SSH.
set -euo pipefail

WORKSPACE="${HOME}/workspace"
NFS_HOST="${DEVBOX_NFS_HOST:-cblevins@192.168.50.211}"
NFS_PATH="${DEVBOX_NFS_PATH:-/srv/nas/pilot3/nas-media-bulk/devbox-ws}"
LOCAL_MOUNT_ROOT="${DEVBOX_LOCAL_MOUNT_ROOT:-/Volumes/nas-media-bulk}"
LOCAL_SYNC_ROOT="${DEVBOX_LOCAL_SYNC_ROOT:-${LOCAL_MOUNT_ROOT}/devbox-ws}"

RSYNC_OPTS=(-rlpgoDz --omit-dir-times)

RSYNC_EXCLUDES=(
  --exclude='._*'
  --exclude='.git'
  --exclude='node_modules'
  --exclude='.devbox-build'
  --exclude='.direnv'
  --exclude='vendor/'
  --exclude='.cache'
  --exclude='.gocache'
  --exclude='.go'
  --exclude='.go-build'
  --exclude='.gotmp'
  --exclude='.golangci-lint-cache'
  --exclude='__pycache__'
  --exclude='.venv'
  --exclude='.mypy_cache'
  --exclude='.pytest_cache'
  --exclude='.ruff_cache'
  --exclude='.loom'
  --exclude='.zed'
  --exclude='.opencode'
  --exclude='.worktrees'
  --exclude='.build/'
  --exclude='.next'
  --exclude='dist/'
  --exclude='tmp/'
  --exclude='.tmp/'
  --exclude='.sandbox-policy.json'
  --exclude='coverage.out'
  --exclude='gosec-report.json'
  --exclude='*.pyc'
  --exclude='*.test'
  --exclude='.DS_Store'
)

target_path() {
  if [ -d "${LOCAL_MOUNT_ROOT}" ] && mount | grep -q "on ${LOCAL_MOUNT_ROOT} "; then
    printf '%s\n' "${LOCAL_SYNC_ROOT}"
  else
    printf '%s:%s\n' "${NFS_HOST}" "${NFS_PATH}"
  fi
}

ensure_target_dir() {
  local dst="$1"
  if [[ "${dst}" == *:* ]]; then
    local remote_host="${dst%%:*}"
    local remote_path="${dst#*:}"
    ssh "${remote_host}" "mkdir -p '${remote_path}'"
  else
    mkdir -p "${dst}"
  fi
}

sync_full() {
  local dst
  dst="$(target_path)"
  ensure_target_dir "${dst}"
  echo "Syncing full workspace to ${dst}..."
  rsync "${RSYNC_OPTS[@]}" --delete "${RSYNC_EXCLUDES[@]}" "${WORKSPACE}/" "${dst}/"
  echo "Done."
}

sync_project() {
  local project="$1"
  local src="${WORKSPACE}/services/${project}/"
  local root
  local dst

  if [ ! -d "${src}" ]; then
    # Try libs/ if not in services/
    src="${WORKSPACE}/libs/${project}/"
    root="$(target_path)"
    dst="${root}/libs/${project}/"
  else
    root="$(target_path)"
    dst="${root}/services/${project}/"
  fi

  if [ ! -d "${src}" ]; then
    echo "Error: project '${project}' not found in services/ or libs/"
    exit 1
  fi

  ensure_target_dir "${dst}"
  echo "Syncing ${project} to ${dst}..."
  rsync "${RSYNC_OPTS[@]}" "${RSYNC_EXCLUDES[@]}" "${src}" "${dst}"
  echo "Done."
}

watch_mode() {
  echo "Watching ${WORKSPACE} for changes (Ctrl+C to stop)..."
  if ! command -v fswatch &>/dev/null; then
    echo "Error: fswatch not installed. Install with: brew install fswatch"
    exit 1
  fi

  # Debounced sync: aggregate changes over 2 seconds
  fswatch -o -l 2 \
    --exclude='\.git' \
    --exclude='node_modules' \
    --exclude='\.devbox-build' \
    --exclude='vendor/' \
    "${WORKSPACE}" | while read -r _; do
      dst="$(target_path)"
      ensure_target_dir "${dst}"
      echo "[$(date +%H:%M:%S)] Change detected, syncing..."
      rsync "${RSYNC_OPTS[@]}" --delete "${RSYNC_EXCLUDES[@]}" "${WORKSPACE}/" "${dst}/" 2>/dev/null
      echo "[$(date +%H:%M:%S)] Sync complete."
    done
}

case "${1:-}" in
  --watch|-w)
    watch_mode
    ;;
  "")
    sync_full
    ;;
  *)
    sync_project "$1"
    ;;
esac
