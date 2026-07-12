#!/usr/bin/env bash
# Verify that an immutable image digest is resident and Ready on every node
# selected by a FlexInfer image-prewarm DaemonSet profile.

set -euo pipefail

PROFILE=""
DIGEST=""
NAMESPACE="flexinfer-system"
TIMEOUT="30m"
KUBECTL="${KUBECTL:-kubectl}"

usage() {
  cat <<'USAGE'
Usage:
  scripts/check-image-prewarm.sh <profile> --digest sha256:<hex> [flags]

Flags:
  --digest <sha256>     Required candidate digest.
  --namespace <name>    Namespace containing the prewarm DaemonSet (default: flexinfer-system).
  --timeout <duration>  Rollout wait timeout (default: 30m).
  -h, --help            Show this help.

The check passes only when the selected DaemonSet references the digest, its
desired/updated/ready/available counts match, and every selected pod reports
the digest in a container image ID.
USAGE
}

fail() {
  echo "ERROR: $*" >&2
  exit 1
}

normalize_digest() {
  local raw="$1"
  if [[ "${raw}" =~ ^sha256:[0-9a-fA-F]{64}$ ]]; then
    printf '%s\n' "${raw,,}"
    return
  fi
  if [[ "${raw}" =~ ^[0-9a-fA-F]{64}$ ]]; then
    printf 'sha256:%s\n' "${raw,,}"
    return
  fi
  fail "digest must be sha256:<64 hex chars>"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --digest)
      [[ $# -ge 2 ]] || fail "--digest requires a value"
      DIGEST="$2"
      shift 2
      ;;
    --namespace)
      [[ $# -ge 2 ]] || fail "--namespace requires a value"
      NAMESPACE="$2"
      shift 2
      ;;
    --timeout)
      [[ $# -ge 2 ]] || fail "--timeout requires a value"
      TIMEOUT="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      fail "unknown flag: $1"
      ;;
    *)
      [[ -z "${PROFILE}" ]] || fail "only one profile may be specified"
      PROFILE="$1"
      shift
      ;;
  esac
done

[[ -n "${PROFILE}" ]] || fail "a prewarm profile is required"
[[ -n "${DIGEST}" ]] || fail "--digest is required"
DIGEST="$(normalize_digest "${DIGEST}")"

selector="app.kubernetes.io/component=flexinfer-image-prewarm,flexinfer.ai/prewarm-profile=${PROFILE}"
mapfile -t daemonsets < <(
  "${KUBECTL}" -n "${NAMESPACE}" get daemonset -l "${selector}" \
    -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
)
[[ "${#daemonsets[@]}" -gt 0 && -n "${daemonsets[0]}" ]] || \
  fail "no image-prewarm DaemonSet found for profile=${PROFILE} namespace=${NAMESPACE}"
[[ "${#daemonsets[@]}" -eq 1 ]] || \
  fail "expected one image-prewarm DaemonSet for profile=${PROFILE}, found ${#daemonsets[@]}"

daemonset="${daemonsets[0]}"
images="$(
  "${KUBECTL}" -n "${NAMESPACE}" get "daemonset/${daemonset}" \
    -o jsonpath='{range .spec.template.spec.containers[*]}{.image}{"\n"}{end}'
)"
grep -Fq "@${DIGEST}" <<<"${images}" || \
  fail "daemonset/${daemonset} does not reference candidate @${DIGEST}"

"${KUBECTL}" -n "${NAMESPACE}" rollout status "daemonset/${daemonset}" --timeout="${TIMEOUT}"

status_counts="$(
  "${KUBECTL}" -n "${NAMESPACE}" get "daemonset/${daemonset}" \
    -o jsonpath='{.status.desiredNumberScheduled} {.status.updatedNumberScheduled} {.status.numberReady} {.status.numberAvailable}'
)"
read -r desired updated ready available <<<"${status_counts}"
[[ "${desired:-0}" -gt 0 ]] || fail "daemonset/${daemonset} has no desired nodes"
[[ "${desired}" == "${updated}" && "${desired}" == "${ready}" && "${desired}" == "${available}" ]] || \
  fail "daemonset/${daemonset} is not fully ready: desired=${desired} updated=${updated} ready=${ready} available=${available}"

pod_rows="$(
  "${KUBECTL}" -n "${NAMESPACE}" get pods -l "${selector}" \
    -o jsonpath='{range .items[*]}{.metadata.name}{"|"}{range .status.containerStatuses[*]}{.imageID}{","}{end}{"\n"}{end}'
)"
pod_count=0
while IFS='|' read -r pod image_ids; do
  [[ -n "${pod}" ]] || continue
  pod_count=$((pod_count + 1))
  grep -Fq "@${DIGEST}" <<<"${image_ids}" || \
    fail "pod/${pod} does not report candidate @${DIGEST}; imageIDs=${image_ids}"
done <<<"${pod_rows}"
[[ "${pod_count}" -eq "${desired}" ]] || \
  fail "daemonset/${daemonset} desired ${desired} pods but observed ${pod_count}"

echo "Image prewarm gate passed"
echo "  profile:   ${PROFILE}"
echo "  namespace: ${NAMESPACE}"
echo "  daemonset: ${daemonset}"
echo "  digest:    ${DIGEST}"
echo "  nodes:     ${desired}"
