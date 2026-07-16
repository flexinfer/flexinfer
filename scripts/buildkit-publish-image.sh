#!/bin/sh
set -eu

usage() {
  cat <<'EOF'
usage: scripts/buildkit-publish-image.sh <name> <dockerfile> <tag> [tag...]

Builds and pushes one image tag per BuildKit invocation while retaining plain
progress logs and image metadata for CI diagnostics.

Environment:
  BUILDKIT_HOST                    Required buildkitd address.
  BUILDKIT_OBSERVABILITY_DIR       Output directory (default: .buildkit-observability).
  BUILDKIT_PUBLISH_ATTEMPTS        Attempts per tag (default: 5).
  BUILDKIT_PUBLISH_INITIAL_DELAY   Initial retry delay in seconds (default: 2).
  BUILDKIT_PUBLISH_TIMEOUT         buildctl timeout in seconds (default: 2700).
  BUILDKIT_IMPORT_CACHE_REF        Optional registry cache reference.
  BUILDKIT_BUILD_ARG               Optional single Docker build arg (KEY=VALUE).
  BUILDKIT_EXPORT_INLINE_CACHE     Set to 1 to export inline cache metadata.
EOF
}

if [ "$#" -lt 3 ]; then
  usage >&2
  exit 2
fi

name="$1"
dockerfile="$2"
shift 2

if [ -z "${BUILDKIT_HOST:-}" ]; then
  echo "ERROR: BUILDKIT_HOST is required" >&2
  exit 2
fi

observability_dir="${BUILDKIT_OBSERVABILITY_DIR:-.buildkit-observability}"
attempts="${BUILDKIT_PUBLISH_ATTEMPTS:-5}"
initial_delay="${BUILDKIT_PUBLISH_INITIAL_DELAY:-2}"
timeout_seconds="${BUILDKIT_PUBLISH_TIMEOUT:-2700}"

validate_non_negative_integer() {
  value_name="$1"
  value="$2"
  case "${value}" in
    ''|*[!0-9]*)
      echo "ERROR: ${value_name} must be a non-negative integer, got ${value}" >&2
      exit 2
      ;;
  esac
}

validate_non_negative_integer attempts "${attempts}"
validate_non_negative_integer initial_delay "${initial_delay}"
validate_non_negative_integer timeout_seconds "${timeout_seconds}"
if [ "${attempts}" -eq 0 ]; then
  echo "ERROR: attempts must be greater than zero" >&2
  exit 2
fi

mkdir -p "${observability_dir}"

current_fifo=""
cleanup_stream() {
  if [ -n "${current_fifo}" ]; then
    rm -f "${current_fifo}"
    current_fifo=""
  fi
}
trap cleanup_stream EXIT
trap 'cleanup_stream; exit 129' HUP
trap 'cleanup_stream; exit 130' INT
trap 'cleanup_stream; exit 143' TERM

safe_component() {
  printf '%s' "$1" | sed 's/[^A-Za-z0-9._-]/_/g'
}

count_matches() {
  pattern="$1"
  file="$2"
  count="$(grep -Ec "${pattern}" "${file}" 2>/dev/null || true)"
  printf '%s' "${count:-0}"
}

metadata_digest() {
  metadata_file="$1"
  if [ ! -s "${metadata_file}" ]; then
    printf '%s' unavailable
    return
  fi

  digest="$(
    sed -n 's/.*"containerimage.digest"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      "${metadata_file}" | sed -n '1p'
  )"
  printf '%s' "${digest:-unavailable}"
}

summarize_attempt() {
  tag="$1"
  attempt="$2"
  status="$3"
  exit_code="$4"
  elapsed_seconds="$5"
  log_file="$6"
  metadata_file="$7"

  cached_steps="$(count_matches '(^|[[:space:]])CACHED([[:space:]]|$)' "${log_file}")"
  completed_steps="$(count_matches '(^|[[:space:]])DONE([[:space:]]|$)' "${log_file}")"
  extraction_events="$(count_matches 'extracting sha256:' "${log_file}")"
  digest="$(metadata_digest "${metadata_file}")"

  printf '%s\n' \
    "buildkit_publish_summary name=${name} tag=${tag} attempt=${attempt} status=${status} exit_code=${exit_code} elapsed_seconds=${elapsed_seconds} cached_steps=${cached_steps} completed_steps=${completed_steps} extraction_events=${extraction_events} digest=${digest} log=${log_file} metadata=${metadata_file}"
}

stream_build() {
  tag="$1"
  log_file="$2"
  metadata_file="$3"

  current_fifo="${log_file}.pipe"
  rm -f "${current_fifo}" "${metadata_file}"
  mkfifo "${current_fifo}"

  tee "${log_file}" <"${current_fifo}" &
  tee_pid="$!"

  set -- buildctl --addr "${BUILDKIT_HOST}" --timeout "${timeout_seconds}" build \
    --frontend dockerfile.v0 \
    --local context=. \
    --local dockerfile=build \
    --opt filename="${dockerfile}" \
    --opt platform=linux/amd64 \
    --progress=plain \
    --metadata-file "${metadata_file}"
  if [ -n "${BUILDKIT_IMPORT_CACHE_REF:-}" ]; then
    set -- "$@" --import-cache "type=registry,ref=${BUILDKIT_IMPORT_CACHE_REF}"
  fi
  if [ -n "${BUILDKIT_BUILD_ARG:-}" ]; then
    set -- "$@" --opt "build-arg:${BUILDKIT_BUILD_ARG}"
  fi
  if [ "${BUILDKIT_EXPORT_INLINE_CACHE:-0}" = "1" ]; then
    set -- "$@" --export-cache "type=inline"
  fi
  set -- "$@" --output "type=image,name=${tag},push=true"

  if "$@" >"${current_fifo}" 2>&1; then
    build_status=0
  else
    build_status="$?"
  fi

  tee_status=0
  wait "${tee_pid}" || tee_status="$?"
  cleanup_stream

  if [ "${build_status}" -ne 0 ]; then
    return "${build_status}"
  fi
  return "${tee_status}"
}

for tag in "$@"; do
  delay="${initial_delay}"
  safe_name="$(safe_component "${name}")"
  safe_tag="$(safe_component "${tag}")"
  published=0

  for attempt in $(seq 1 "${attempts}"); do
    log_file="${observability_dir}/${safe_name}-${safe_tag}-attempt-${attempt}.log"
    metadata_file="${observability_dir}/${safe_name}-${safe_tag}-attempt-${attempt}.metadata.json"
    started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    started_epoch="$(date +%s)"

    echo "buildkit_publish_start name=${name} tag=${tag} attempt=${attempt}/${attempts} started_at=${started_at} dockerfile=${dockerfile}"

    if stream_build "${tag}" "${log_file}" "${metadata_file}"; then
      exit_code=0
      elapsed_seconds=$(( $(date +%s) - started_epoch ))
      summarize_attempt "${tag}" "${attempt}" success "${exit_code}" \
        "${elapsed_seconds}" "${log_file}" "${metadata_file}"
      published=1
      break
    else
      exit_code="$?"
      elapsed_seconds=$(( $(date +%s) - started_epoch ))
      summarize_attempt "${tag}" "${attempt}" failed "${exit_code}" \
        "${elapsed_seconds}" "${log_file}" "${metadata_file}"
    fi

    if [ "${attempt}" -eq "${attempts}" ]; then
      echo "ERROR: failed to publish ${name} (${tag}) after ${attempts} attempts" >&2
      exit "${exit_code}"
    fi

    echo "WARN: publish ${tag} failed (${attempt}/${attempts}); retrying in ${delay}s..." >&2
    sleep "${delay}"
    delay=$((delay * 2))
  done

  if [ "${published}" -ne 1 ]; then
    echo "ERROR: publish loop ended without publishing ${name} (${tag})" >&2
    exit 1
  fi
done
