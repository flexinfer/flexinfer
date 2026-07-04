#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/prune-build-node-disk.sh [options]

Prunes old Docker and BuildKit cache records on FlexInfer build nodes.

Options:
  --local-docker           Prune the active Docker daemon's builder/system cache.
  --buildctl               Prune the remote BuildKit daemon named by BUILDKIT_HOST.
  --all                    Enable local Docker and buildctl pruning.
  --dry-run                Print the commands that would run.
  --no-post-check          Skip the disk headroom check after pruning.
  -h, --help               Show this help.

Environment:
  FLEXINFER_BUILD_PRUNE_UNTIL                  Default age filter (default: 168h).
  FLEXINFER_BUILD_DOCKER_BUILDER_PRUNE_UNTIL   Docker builder prune age.
  FLEXINFER_BUILD_DOCKER_SYSTEM_PRUNE_UNTIL    Docker system prune age.
  FLEXINFER_BUILD_BUILDKIT_PRUNE_UNTIL         BuildKit prune age.
  BUILDKIT_HOST                                buildctl address, e.g. tcp://buildkitd:1234.

This intentionally avoids `docker system prune -a --volumes`.
EOF
}

prune_until="${FLEXINFER_BUILD_PRUNE_UNTIL:-168h}"
docker_builder_until="${FLEXINFER_BUILD_DOCKER_BUILDER_PRUNE_UNTIL:-${prune_until}}"
docker_system_until="${FLEXINFER_BUILD_DOCKER_SYSTEM_PRUNE_UNTIL:-${prune_until}}"
buildkit_until="${FLEXINFER_BUILD_BUILDKIT_PRUNE_UNTIL:-${prune_until}}"

check_local_docker=0
check_buildctl=0
dry_run=0
post_check=1

if [ "$#" -eq 0 ]; then
  check_local_docker=1
  if [ -n "${BUILDKIT_HOST:-}" ]; then
    check_buildctl=1
  fi
fi

while [ "$#" -gt 0 ]; do
  case "$1" in
    --local-docker)
      check_local_docker=1
      shift
      ;;
    --buildctl)
      check_buildctl=1
      shift
      ;;
    --all)
      check_local_docker=1
      check_buildctl=1
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --no-post-check)
      post_check=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "ERROR: unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ "${check_local_docker}" -eq 0 ] && [ "${check_buildctl}" -eq 0 ]; then
  echo "ERROR: no prune target selected; pass --local-docker, --buildctl, or --all" >&2
  exit 2
fi

run_cmd() {
  if [ "${dry_run}" -eq 1 ]; then
    printf 'DRY-RUN:'
    printf ' %q' "$@"
    printf '\n'
    return
  fi
  "$@"
}

prune_local_docker() {
  if [ "${dry_run}" -eq 0 ] && ! command -v docker >/dev/null 2>&1; then
    echo "WARN: docker not found; skipping local Docker prune" >&2
    return
  fi

  echo "build-disk-prune: local Docker builder/system cache"
  if [ "${dry_run}" -eq 0 ]; then
    docker system df || true
  fi

  run_cmd docker builder prune --filter "until=${docker_builder_until}" --force
  run_cmd docker system prune --filter "until=${docker_system_until}" --force

  if [ "${dry_run}" -eq 0 ]; then
    docker system df || true
  fi
}

prune_remote_buildkit() {
  if [ -z "${BUILDKIT_HOST:-}" ]; then
    echo "WARN: BUILDKIT_HOST is unset; skipping buildctl prune" >&2
    return
  fi
  if [ "${dry_run}" -eq 0 ] && ! command -v buildctl >/dev/null 2>&1; then
    echo "WARN: buildctl not found; skipping buildctl prune" >&2
    return
  fi

  echo "build-disk-prune: BuildKit cache for ${BUILDKIT_HOST}"
  if [ "${dry_run}" -eq 0 ]; then
    buildctl --addr "${BUILDKIT_HOST}" du || true
  fi

  run_cmd buildctl --addr "${BUILDKIT_HOST}" prune --filter "until=${buildkit_until}" --force

  if [ "${dry_run}" -eq 0 ]; then
    buildctl --addr "${BUILDKIT_HOST}" du || true
  fi
}

if [ "${check_local_docker}" -eq 1 ]; then
  prune_local_docker
fi
if [ "${check_buildctl}" -eq 1 ]; then
  prune_remote_buildkit
fi

if [ "${post_check}" -eq 1 ] && [ "${dry_run}" -eq 0 ]; then
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  check_args=()
  if [ "${check_local_docker}" -eq 1 ]; then
    check_args+=(--local-docker)
  fi
  if [ "${check_buildctl}" -eq 1 ]; then
    check_args+=(--buildctl-du)
  fi
  "${script_dir}/check-build-node-disk.sh" "${check_args[@]}"
fi

echo "build-disk-prune: OK"
