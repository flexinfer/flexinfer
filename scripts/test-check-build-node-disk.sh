#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
script="${root}/scripts/check-build-node-disk.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
out_file="${tmp_dir}/out"

pass_output="$(
  FLEXINFER_BUILD_MIN_FREE_GIB=0 \
  FLEXINFER_BUILD_MAX_USED_PCT=100 \
  "${script}" --path "${tmp_dir}"
)"

if ! grep -q "build-disk: OK" <<<"${pass_output}"; then
  echo "expected successful disk check" >&2
  echo "${pass_output}" >&2
  exit 1
fi

if FLEXINFER_BUILD_MIN_FREE_GIB=999999999 "${script}" --path "${tmp_dir}" >"${out_file}" 2>&1; then
  echo "expected disk check to fail with unreachable free-space threshold" >&2
  cat "${out_file}" >&2
  exit 1
fi

if ! grep -q "below 999999999GiB" "${out_file}"; then
  echo "expected low-free-space diagnostic" >&2
  cat "${out_file}" >&2
  exit 1
fi

if "${script}" --path "${tmp_dir}/missing" >"${out_file}" 2>&1; then
  echo "expected missing path check to fail" >&2
  cat "${out_file}" >&2
  exit 1
fi

if ! grep -q "local path does not exist" "${out_file}"; then
  echo "expected missing-path diagnostic" >&2
  cat "${out_file}" >&2
  exit 1
fi

echo "check-build-node-disk self-test: OK"
