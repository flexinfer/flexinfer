#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/check-build-node-disk.sh [options]

Checks disk headroom for FlexInfer Docker/BuildKit builder nodes.

Options:
  --path PATH              Check a local filesystem path. Repeatable.
  --local-docker           Check the active Docker daemon's DockerRootDir.
  --kubernetes-buildkit    Check a BuildKit pod via kubectl exec.
  --buildctl-du            Print remote BuildKit cache usage with buildctl du.
  --all                    Enable local Docker, Kubernetes BuildKit, and buildctl du.
  -h, --help               Show this help.

Threshold environment:
  FLEXINFER_BUILD_MIN_FREE_GIB   Minimum free GiB required (default: 120).
  FLEXINFER_BUILD_MAX_USED_PCT   Maximum filesystem usage percent (default: 85).

Kubernetes BuildKit environment:
  BUILDKIT_NAMESPACE             Namespace for buildkitd (default: ci-build).
  BUILDKIT_POD_SELECTOR          Pod selector (default: app=buildkitd-central).
  BUILDKIT_CONTAINER             Optional container name for kubectl exec.
  BUILDKIT_DF_PATH               Path to check in the pod (default: /var/lib/buildkit).

Buildctl environment:
  BUILDKIT_HOST                  buildctl address, e.g. tcp://buildkitd:1234.
EOF
}

min_free_gib="${FLEXINFER_BUILD_MIN_FREE_GIB:-120}"
max_used_pct="${FLEXINFER_BUILD_MAX_USED_PCT:-85}"

paths=()
check_local_docker=0
check_kubernetes_buildkit=0
check_buildctl_du=0

if [ "$#" -eq 0 ]; then
  check_local_docker=1
  if [ -n "${BUILDKIT_HOST:-}" ]; then
    check_buildctl_du=1
  fi
  if command -v kubectl >/dev/null 2>&1; then
    check_kubernetes_buildkit=1
  fi
fi

while [ "$#" -gt 0 ]; do
  case "$1" in
    --path)
      if [ "$#" -lt 2 ]; then
        echo "ERROR: --path requires a value" >&2
        exit 2
      fi
      paths+=("$2")
      shift 2
      ;;
    --local-docker)
      check_local_docker=1
      shift
      ;;
    --kubernetes-buildkit)
      check_kubernetes_buildkit=1
      shift
      ;;
    --buildctl-du)
      check_buildctl_du=1
      shift
      ;;
    --all)
      check_local_docker=1
      check_kubernetes_buildkit=1
      check_buildctl_du=1
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

failures=0
checks=0
observations=0

check_df_output() {
  local label="$1"
  local df_output="$2"
  local line total_kib used_kib avail_kib used_pct mount free_gib

  line="$(printf '%s\n' "${df_output}" | awk 'NR == 2 {print $2, $3, $4, $5, $6}')"
  if [ -z "${line}" ]; then
    echo "ERROR: ${label}: could not parse df output" >&2
    failures=$((failures + 1))
    return
  fi

  read -r total_kib used_kib avail_kib used_pct mount <<<"${line}"
  used_pct="${used_pct%%%}"
  free_gib="$(
    awk -v kib="${avail_kib}" 'BEGIN { printf "%.1f", kib / 1048576 }'
  )"

  printf 'build-disk: %-28s free=%sGiB used=%s%% mount=%s total=%sGiB\n' \
    "${label}" "${free_gib}" "${used_pct}" "${mount}" \
    "$(awk -v kib="${total_kib}" 'BEGIN { printf "%.1f", kib / 1048576 }')"

  checks=$((checks + 1))

  if awk -v free="${free_gib}" -v min="${min_free_gib}" 'BEGIN { exit !(free < min) }'; then
    echo "ERROR: ${label}: free space ${free_gib}GiB is below ${min_free_gib}GiB" >&2
    failures=$((failures + 1))
  fi

  if [ "${used_pct}" -ge "${max_used_pct}" ]; then
    echo "ERROR: ${label}: filesystem usage ${used_pct}% is at or above ${max_used_pct}%" >&2
    failures=$((failures + 1))
  fi
}

check_path() {
  local path="$1"
  if [ ! -e "${path}" ]; then
    echo "ERROR: local path does not exist: ${path}" >&2
    failures=$((failures + 1))
    return
  fi
  check_df_output "${path}" "$(df -Pk "${path}")"
}

check_docker_root() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "WARN: docker not found; skipping local Docker root check" >&2
    return
  fi

  local docker_root
  docker_root="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || true)"
  if [ -z "${docker_root}" ]; then
    echo "WARN: docker daemon unavailable; skipping local Docker root check" >&2
    return
  fi

  check_path "${docker_root}"
  docker system df || true
}

check_kubernetes_buildkit_root() {
  if ! command -v kubectl >/dev/null 2>&1; then
    echo "WARN: kubectl not found; skipping Kubernetes BuildKit check" >&2
    return
  fi

  local namespace selector container df_path pod
  namespace="${BUILDKIT_NAMESPACE:-ci-build}"
  selector="${BUILDKIT_POD_SELECTOR:-app=buildkitd-central}"
  container="${BUILDKIT_CONTAINER:-}"
  df_path="${BUILDKIT_DF_PATH:-/var/lib/buildkit}"

  pod="$(
    kubectl -n "${namespace}" get pod -l "${selector}" \
      -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true
  )"
  if [ -z "${pod}" ]; then
    echo "WARN: no BuildKit pod found for ${namespace}/${selector}; skipping Kubernetes BuildKit check" >&2
    return
  fi

  local exec_args=(-n "${namespace}" exec "${pod}")
  if [ -n "${container}" ]; then
    exec_args+=(-c "${container}")
  fi
  exec_args+=(-- df -Pk "${df_path}")

  check_df_output "k8s:${namespace}/${pod}:${df_path}" "$(kubectl "${exec_args[@]}")"
}

print_buildctl_du() {
  if [ -z "${BUILDKIT_HOST:-}" ]; then
    echo "WARN: BUILDKIT_HOST is unset; skipping buildctl du" >&2
    return
  fi
  if ! command -v buildctl >/dev/null 2>&1; then
    echo "WARN: buildctl not found; skipping buildctl du" >&2
    return
  fi

  echo "build-disk: BuildKit cache usage for ${BUILDKIT_HOST}"
  if buildctl --addr "${BUILDKIT_HOST}" du -v; then
    observations=$((observations + 1))
    return
  fi
  if buildctl --addr "${BUILDKIT_HOST}" du; then
    observations=$((observations + 1))
    return
  fi

  echo "ERROR: buildctl du failed for ${BUILDKIT_HOST}" >&2
  failures=$((failures + 1))
}

for path in "${paths[@]}"; do
  check_path "${path}"
done

if [ "${check_local_docker}" -eq 1 ]; then
  check_docker_root
fi
if [ "${check_kubernetes_buildkit}" -eq 1 ]; then
  check_kubernetes_buildkit_root
fi
if [ "${check_buildctl_du}" -eq 1 ]; then
  print_buildctl_du
fi

if [ "${checks}" -eq 0 ] && [ "${observations}" -eq 0 ]; then
  echo "ERROR: no disk checks ran; pass --path, --local-docker, or --kubernetes-buildkit" >&2
  exit 2
fi

if [ "${failures}" -gt 0 ]; then
  echo "build-disk: ${failures} threshold failure(s)" >&2
  exit 1
fi

echo "build-disk: OK"
