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
set -euo pipefail

# Force plain progress output so the streamed trace lines are line-buffered and
# survive PodActiveDeadline kills. Use the env var (honored by buildctl v0.12.x)
# rather than the --progress flag, which v0.12.5 in our central buildkitd does
# not define ("flag provided but not defined: -progress").
export BUILDKIT_PROGRESS=plain

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

# Optional fallback token for fetching private Go modules whose source repo's
# inbound CI/CD job-token allowlist either rejects services/loom-core's
# CI_JOB_TOKEN or which require a permissions level the job token doesn't have
# (e.g. libs/fi-mcp-kit, fetched as a private module from GitLab once
# 20c66ada dropped the local workspace replace). Configured via the masked
# GITLAB_TOKEN CI/CD variable (a project access token with read_repository
# scope on the target repo). When unset the build falls back to CI_JOB_TOKEN
# alone, matching pre-2026-05 behavior.
GITLAB_TOKEN_SECRET=""
if [ -n "${GITLAB_TOKEN:-}" ]; then
  GITLAB_TOKEN_SECRET="--secret id=gitlab_token,env=GITLAB_TOKEN"
fi

# Same timestamp across the failover loop so retries against a fallback registry
# don't end up with a different `:YYYYMMDD-HHMMSS` tag than the primary push.
# This tag enables Flux Image Automation: ImagePolicy filters
# `^[0-9]{8}-[0-9]{6}$` and sorts alphabetically-descending to select the
# latest image. See platform/gitops/k3s/flux/image-automation/PLAN.md and the
# flexinfer-site/streamslate-site/fi-fhir policies for the pattern this enables.
TIMESTAMP_TAG="$(date -u +%Y%m%d-%H%M%S)"

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
  IMAGE_NAMES="$IMAGE:$CI_COMMIT_SHORT_SHA,$IMAGE:$TIMESTAMP_TAG"
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
  # Stream output live to the trace via tee so that GitLab's PodActiveDeadline
  # killing the pod mid-build still leaves visible buildctl progress. Without
  # this, the trace ends at "Building with image names: ..." and we can't tell
  # whether compile, layer export, or push is the slow step.
  if buildctl --addr "$BUILDKIT_HOST" --timeout 2700 build \
    --frontend dockerfile.v0 \
    --local context="$CI_PROJECT_DIR" \
    --local dockerfile="$CI_PROJECT_DIR" \
    --secret id=ci_job_token,env=CI_JOB_TOKEN $GITLAB_TOKEN_SECRET \
    --opt "filename=$DOCKERFILE" \
    --opt "build-arg:RUNTIME_REGISTRY=$REGISTRY" \
    --opt "build-arg:VERSION=${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA}" \
    --import-cache "type=registry,ref=${CACHE_REF}" \
    --export-cache "type=registry,ref=${CACHE_REF},mode=min,image-manifest=true" \
    --output "type=image,\"name=${IMAGE_NAMES}\",push=true" "$@" 2>&1 | tee "$LOG_FILE"; then
    rm -f "$LOG_FILE"
    SUCCESS=1
    break
  else
    LAST_RC=$?
  fi
  if grep -Eqi "error writing manifest blob|/manifests/buildcache|buildcache" "$LOG_FILE"; then
    echo "Registry cache export failed; retrying without cache flags."
    rm -f "$LOG_FILE"
    LOG_FILE="$CI_PROJECT_DIR/.buildctl-nocache-${REGISTRY//[^a-zA-Z0-9_.-]/_}.log"
    if buildctl --addr "$BUILDKIT_HOST" --timeout 2700 build \
      --frontend dockerfile.v0 \
      --local context="$CI_PROJECT_DIR" \
      --local dockerfile="$CI_PROJECT_DIR" \
      --secret id=ci_job_token,env=CI_JOB_TOKEN $GITLAB_TOKEN_SECRET \
      --opt "filename=$DOCKERFILE" \
      --opt "build-arg:RUNTIME_REGISTRY=$REGISTRY" \
      --opt "build-arg:VERSION=${CI_COMMIT_TAG:-$CI_COMMIT_SHORT_SHA}" \
      --output "type=image,\"name=${IMAGE_NAMES}\",push=true" "$@" 2>&1 | tee "$LOG_FILE"; then
      rm -f "$LOG_FILE"
      SUCCESS=1
      break
    else
      LAST_RC=$?
    fi
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
