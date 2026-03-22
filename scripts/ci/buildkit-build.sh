#!/bin/sh
# scripts/ci/buildkit-build.sh
# Shared BuildKit build helper for CI image jobs.
#
# Usage:
#   buildkit-build.sh <image-repo> <dockerfile> <cache-name> [extra-build-args...]
#
# Required env vars:
#   BUILDKIT_HOST, CI_PROJECT_DIR, CI_JOB_TOKEN, CI_COMMIT_SHORT_SHA,
#   CI_COMMIT_BRANCH, CI_DEFAULT_BRANCH
# Optional env vars:
#   BUILDKIT_FALLBACK_HOSTS, IMAGE_REGISTRIES, IMAGE_REGISTRY, CI_COMMIT_TAG
set -eu

IMAGE_REPO="$1"
DOCKERFILE="$2"
CACHE_NAME="$3"
shift 3

# --- BuildKit endpoint discovery ---
pick_buildkit_host() {
  CANDIDATES="${BUILDKIT_HOST:-}"
  if [ -n "${BUILDKIT_FALLBACK_HOSTS:-}" ]; then
    CANDIDATES="$CANDIDATES $(echo "$BUILDKIT_FALLBACK_HOSTS" | tr ',' ' ')"
  fi

  if [ -z "$CANDIDATES" ]; then
    echo "No BuildKit endpoint configured."
    return 1
  fi

  SEEN=""
  for HOST in $CANDIDATES; do
    [ -z "$HOST" ] && continue
    case " $SEEN " in
      *" $HOST "*) continue ;;
    esac
    SEEN="$SEEN $HOST"

    echo "Checking BuildKit endpoint: $HOST"
    if buildctl --addr "$HOST" debug info >/dev/null 2>&1; then
      BUILDKIT_HOST="$HOST"
      export BUILDKIT_HOST
      echo "Using BuildKit endpoint: $BUILDKIT_HOST"
      buildctl --addr "$BUILDKIT_HOST" debug info
      return 0
    fi
  done

  echo "No healthy BuildKit endpoint found."
  return 1
}

pick_buildkit_host

# --- Registry failover loop ---
REGISTRY_CANDIDATES="${IMAGE_REGISTRIES:-${IMAGE_REGISTRY}}"
SUCCESS=0
LAST_RC=1

for REGISTRY in $REGISTRY_CANDIDATES; do
  PROBE_LOG="$CI_PROJECT_DIR/.probe-${REGISTRY//[^a-zA-Z0-9_.-]/_}.log"
  rm -f "$PROBE_LOG"
  if command -v wget >/dev/null 2>&1; then
    if ! wget -q --spider --no-check-certificate --timeout=15 --tries=2 "https://$REGISTRY/v2/" >"$PROBE_LOG" 2>&1; then
      if grep -Eqi "401|403|unauthorized|forbidden|certificate" "$PROBE_LOG"; then
        echo "Registry host $REGISTRY is reachable (auth challenge or self-signed cert)."
      else
        echo "Registry host $REGISTRY is unreachable; skipping."
        cat "$PROBE_LOG"
        rm -f "$PROBE_LOG"
        continue
      fi
    fi
  fi
  rm -f "$PROBE_LOG"

  IMAGE="$REGISTRY/$IMAGE_REPO"
  CACHE_REF="$REGISTRY/library/build-cache/$CACHE_NAME"
  IMAGE_NAMES="$IMAGE:$CI_COMMIT_SHORT_SHA"
  if [ "$CI_COMMIT_BRANCH" = "$CI_DEFAULT_BRANCH" ]; then
    IMAGE_NAMES="$IMAGE_NAMES,$IMAGE:latest"
  fi
  if [ -n "${CI_COMMIT_TAG:-}" ]; then
    IMAGE_NAMES="$IMAGE_NAMES,$IMAGE:$CI_COMMIT_TAG"
  fi

  echo "Attempting build/push via registry host: $REGISTRY"
  echo "Building with image names: $IMAGE_NAMES"
  LOG_FILE="$CI_PROJECT_DIR/.buildctl-${REGISTRY//[^a-zA-Z0-9_.-]/_}.log"
  rm -f "$LOG_FILE"
  if buildctl --addr "$BUILDKIT_HOST" --timeout 2700 build \
    --frontend dockerfile.v0 \
    --local context="$CI_PROJECT_DIR" \
    --local dockerfile="$CI_PROJECT_DIR" \
    --secret id=ci_job_token,env=CI_JOB_TOKEN \
    --opt "filename=$DOCKERFILE" \
    --opt "build-arg:RUNTIME_REGISTRY=$REGISTRY" \
    --opt "build-arg:VERSION=${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA}" \
    --import-cache "type=registry,ref=${CACHE_REF}" \
    --export-cache "type=registry,ref=${CACHE_REF},mode=min,image-manifest=true" \
    --output "type=image,\"name=${IMAGE_NAMES}\",push=true" "$@" >"$LOG_FILE" 2>&1; then
    cat "$LOG_FILE"
    rm -f "$LOG_FILE"
    SUCCESS=1
    break
  else
    LAST_RC=$?
  fi
  cat "$LOG_FILE"
  if grep -Eqi "error writing manifest blob|/manifests/buildcache|buildcache" "$LOG_FILE"; then
    echo "Registry cache export failed; retrying without cache flags."
    rm -f "$LOG_FILE"
    LOG_FILE="$CI_PROJECT_DIR/.buildctl-nocache-${REGISTRY//[^a-zA-Z0-9_.-]/_}.log"
    if buildctl --addr "$BUILDKIT_HOST" --timeout 2700 build \
      --frontend dockerfile.v0 \
      --local context="$CI_PROJECT_DIR" \
      --local dockerfile="$CI_PROJECT_DIR" \
      --secret id=ci_job_token,env=CI_JOB_TOKEN \
      --opt "filename=$DOCKERFILE" \
      --opt "build-arg:RUNTIME_REGISTRY=$REGISTRY" \
      --opt "build-arg:VERSION=${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA}" \
      --output "type=image,\"name=${IMAGE_NAMES}\",push=true" "$@" >"$LOG_FILE" 2>&1; then
      cat "$LOG_FILE"
      rm -f "$LOG_FILE"
      SUCCESS=1
      break
    else
      LAST_RC=$?
    fi
    cat "$LOG_FILE"
  fi

  if grep -Eqi "x509|certificate|tls handshake timeout|unknown authority|connection refused|i/o timeout|no route to host|context deadline exceeded|temporary failure in name resolution|unauthorized|denied|failed to list workers|transport: error while dialing|dial tcp|unexpected EOF|connection reset by peer" "$LOG_FILE"; then
    echo "Registry host $REGISTRY failed; trying next configured host."
    rm -f "$LOG_FILE"
    continue
  fi

  rm -f "$LOG_FILE"
  exit "$LAST_RC"
done

if [ "$SUCCESS" -ne 1 ]; then
  echo "All registry hosts failed: $REGISTRY_CANDIDATES"
  exit "$LAST_RC"
fi
