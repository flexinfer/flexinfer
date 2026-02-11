#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE' >&2
Usage:
  install_atomic.sh <src> <dst> [--no-backup]

Installs <src> to <dst> using an atomic rename to avoid transient "missing binary"
windows and to avoid `cp`-over-in-use issues on macOS.

By default, if <dst> exists, a copy is saved to <dst>.prev before replacement.
USAGE
}

if [[ $# -lt 2 ]]; then
  usage
  exit 2
fi

src="$1"
dst="$2"
shift 2

backup=1
if [[ ${1:-} == "--no-backup" ]]; then
  backup=0
  shift
fi
if [[ $# -ne 0 ]]; then
  usage
  exit 2
fi

if [[ ! -f "$src" ]]; then
  echo "install_atomic: src not found: $src" >&2
  exit 1
fi

dst_dir="$(dirname "$dst")"
mkdir -p "$dst_dir"

if [[ $backup -eq 1 && -f "$dst" ]]; then
  # Keep a single previous copy; do not accumulate backups (disk-friendly).
  cp -p "$dst" "${dst}.prev" || true
fi

tmp="${dst}.tmp.$$"
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT

cp -p "$src" "$tmp"
chmod +x "$tmp" 2>/dev/null || true
mv -f "$tmp" "$dst"
