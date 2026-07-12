#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_ROOT="$(mktemp -d)"
trap 'rm -rf "${TMP_ROOT}"' EXIT

DIGEST="sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
FAKE_KUBECTL="${TMP_ROOT}/kubectl"

cat >"${FAKE_KUBECTL}" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
args="$*"
digest="${FAKE_DIGEST}"
image="${FAKE_IMAGE:-registry.example/flexinfer/runtime@${digest}}"
case "${args}" in
  *"get daemonset -l"*)
    printf 'flexinfer-image-prewarm-test\n'
    ;;
  *"get daemonset/flexinfer-image-prewarm-test -o jsonpath={range .spec"*)
    printf '%s\n' "${image}"
    ;;
  *"rollout status daemonset/flexinfer-image-prewarm-test"*)
    echo 'daemon set successfully rolled out'
    ;;
  *"get daemonset/flexinfer-image-prewarm-test -o jsonpath={.status"*)
    printf '1 1 1 1\n'
    ;;
  *"get pods -l"*)
    printf 'prewarm-pod|registry.example/flexinfer/runtime@%s,\n' "${digest}"
    ;;
  *)
    echo "unexpected kubectl args: ${args}" >&2
    exit 1
    ;;
esac
FAKE
chmod +x "${FAKE_KUBECTL}"

if ! FAKE_DIGEST="${DIGEST}" KUBECTL="${FAKE_KUBECTL}" \
  "${SCRIPT_DIR}/check-image-prewarm.sh" test --digest "${DIGEST}" --timeout 1s \
  >"${TMP_ROOT}/pass.log" 2>&1; then
  cat "${TMP_ROOT}/pass.log" >&2
  exit 1
fi
grep -F "Image prewarm gate passed" "${TMP_ROOT}/pass.log" >/dev/null

if FAKE_DIGEST="${DIGEST}" FAKE_IMAGE="registry.example/flexinfer/runtime:mutable" \
  KUBECTL="${FAKE_KUBECTL}" \
  "${SCRIPT_DIR}/check-image-prewarm.sh" test --digest "${DIGEST}" --timeout 1s \
  >"${TMP_ROOT}/fail.log" 2>&1; then
  echo "mutable DaemonSet image unexpectedly passed" >&2
  exit 1
fi
grep -F "does not reference candidate" "${TMP_ROOT}/fail.log" >/dev/null

echo "check-image-prewarm tests passed"
