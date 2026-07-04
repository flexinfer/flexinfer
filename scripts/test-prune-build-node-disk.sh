#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
script="${root}/scripts/prune-build-node-disk.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fake_bin="${tmp_dir}/bin"
mkdir -p "${fake_bin}"
log_file="${tmp_dir}/commands.log"

cat >"${fake_bin}/docker" <<'EOF'
#!/usr/bin/env bash
printf 'docker' >>"${FLEXINFER_FAKE_COMMAND_LOG}"
printf ' %s' "$@" >>"${FLEXINFER_FAKE_COMMAND_LOG}"
printf '\n' >>"${FLEXINFER_FAKE_COMMAND_LOG}"
exit 0
EOF

cat >"${fake_bin}/buildctl" <<'EOF'
#!/usr/bin/env bash
printf 'buildctl' >>"${FLEXINFER_FAKE_COMMAND_LOG}"
printf ' %s' "$@" >>"${FLEXINFER_FAKE_COMMAND_LOG}"
printf '\n' >>"${FLEXINFER_FAKE_COMMAND_LOG}"
exit 0
EOF

chmod +x "${fake_bin}/docker" "${fake_bin}/buildctl"

dry_output="$(
  BUILDKIT_HOST=tcp://buildkitd.example:1234 \
  "${script}" --dry-run --local-docker --buildctl --no-post-check
)"

if ! grep -q "DRY-RUN: docker builder prune" <<<"${dry_output}"; then
  echo "expected dry-run Docker builder prune command" >&2
  echo "${dry_output}" >&2
  exit 1
fi

if ! grep -q "DRY-RUN: buildctl --addr tcp://buildkitd.example:1234 prune" <<<"${dry_output}"; then
  echo "expected dry-run buildctl prune command" >&2
  echo "${dry_output}" >&2
  exit 1
fi

PATH="${fake_bin}:${PATH}" \
FLEXINFER_FAKE_COMMAND_LOG="${log_file}" \
FLEXINFER_BUILD_PRUNE_UNTIL=24h \
BUILDKIT_HOST=tcp://buildkitd.example:1234 \
  "${script}" --all --no-post-check >/dev/null

if ! grep -q "docker builder prune --filter until=24h --force" "${log_file}"; then
  echo "expected Docker builder prune command" >&2
  cat "${log_file}" >&2
  exit 1
fi

if ! grep -q "docker system prune --filter until=24h --force" "${log_file}"; then
  echo "expected Docker system prune command" >&2
  cat "${log_file}" >&2
  exit 1
fi

if ! grep -q "buildctl --addr tcp://buildkitd.example:1234 prune --filter until=24h --force" "${log_file}"; then
  echo "expected BuildKit prune command" >&2
  cat "${log_file}" >&2
  exit 1
fi

if "${script}" --no-post-check >/dev/null 2>&1; then
  echo "expected explicit no-target run to fail" >&2
  exit 1
fi

echo "prune-build-node-disk self-test: OK"
