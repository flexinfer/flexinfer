#!/usr/bin/env bash
# devbox-sync.sh — Sync workspace to NFS for K8s devbox sandbox pods.
#
# Usage:
#   ./scripts/devbox-sync.sh              # Full workspace sync
#   ./scripts/devbox-sync.sh loom-core    # Sync only loom-core project
#   ./scripts/devbox-sync.sh --watch      # Watch mode (continuous sync)
#
# The NFS server is cblevins-5930k (192.168.50.151).
# NFS export: /srv/nfs/devbox-workspace
# K8s PV: devbox-workspace-nfs (mounted at /workspace in pods)
set -euo pipefail

NFS_HOST="cblevins@192.168.50.217"
NFS_PATH="/srv/nfs/nas-media-bulk/devbox-workspace"
WORKSPACE="${HOME}/workspace"

RSYNC_OPTS=(-rlpgoDz --omit-dir-times)

RSYNC_EXCLUDES=(
  --exclude='.git'
  --exclude='node_modules'
  --exclude='.devbox-build'
  --exclude='vendor/'
  --exclude='.cache'
  --exclude='__pycache__'
  --exclude='.venv'
  --exclude='.mypy_cache'
  --exclude='.loom'
  --exclude='.zed'
  --exclude='.build/'
  --exclude='*.pyc'
  --exclude='.DS_Store'
)

sync_full() {
  echo "Syncing full workspace to ${NFS_HOST}:${NFS_PATH}..."
  rsync "${RSYNC_OPTS[@]}" --delete "${RSYNC_EXCLUDES[@]}" "${WORKSPACE}/" "${NFS_HOST}:${NFS_PATH}/"
  echo "Done."
}

sync_project() {
  local project="$1"
  local src="${WORKSPACE}/services/${project}/"
  local dst="${NFS_HOST}:${NFS_PATH}/services/${project}/"

  if [ ! -d "${src}" ]; then
    # Try libs/ if not in services/
    src="${WORKSPACE}/libs/${project}/"
    dst="${NFS_HOST}:${NFS_PATH}/libs/${project}/"
  fi

  if [ ! -d "${src}" ]; then
    echo "Error: project '${project}' not found in services/ or libs/"
    exit 1
  fi

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
    echo "[$(date +%H:%M:%S)] Change detected, syncing..."
    rsync "${RSYNC_OPTS[@]}" --delete "${RSYNC_EXCLUDES[@]}" "${WORKSPACE}/" "${NFS_HOST}:${NFS_PATH}/" 2>/dev/null
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
