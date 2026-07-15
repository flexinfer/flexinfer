#!/bin/sh
set -eu

script_dir="$(CDPATH='' cd "$(dirname "$0")" && pwd)"
root="$(CDPATH='' cd "${script_dir}/.." && pwd)"
script="${root}/scripts/buildkit-publish-image.sh"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT HUP INT TERM

fake_bin="${tmp_dir}/bin"
observability_dir="${tmp_dir}/observability"
command_log="${tmp_dir}/buildctl-commands.log"
state_file="${tmp_dir}/first-attempt-failed"
mkdir -p "${fake_bin}"

cat >"${fake_bin}/buildctl" <<'EOF'
#!/bin/sh
set -eu

printf 'buildctl' >>"${FLEXINFER_FAKE_COMMAND_LOG}"
printf ' %s' "$@" >>"${FLEXINFER_FAKE_COMMAND_LOG}"
printf '\n' >>"${FLEXINFER_FAKE_COMMAND_LOG}"

metadata_file=""
tag=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --metadata-file)
      metadata_file="$2"
      shift 2
      ;;
    --output)
      tag="$(printf '%s' "$2" | sed -n 's/.*name=\([^,]*\).*/\1/p')"
      shift 2
      ;;
    *)
      shift
      ;;
  esac
done

echo '#1 [internal] load build definition from Dockerfile'
echo '#1 CACHED'
echo '#2 extracting sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'

if [ ! -e "${FLEXINFER_FAKE_STATE_FILE}" ]; then
  : >"${FLEXINFER_FAKE_STATE_FILE}"
  echo '#2 ERROR: simulated extraction failure' >&2
  exit 42
fi

echo '#2 DONE 0.1s'
printf '{"containerimage.digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","tag":"%s"}\n' \
  "${tag}" >"${metadata_file}"
EOF
chmod +x "${fake_bin}/buildctl"

output_file="${tmp_dir}/output.log"
PATH="${fake_bin}:${PATH}" \
FLEXINFER_FAKE_COMMAND_LOG="${command_log}" \
FLEXINFER_FAKE_STATE_FILE="${state_file}" \
BUILDKIT_HOST="tcp://buildkitd.example:1234" \
BUILDKIT_OBSERVABILITY_DIR="${observability_dir}" \
BUILDKIT_PUBLISH_ATTEMPTS=2 \
BUILDKIT_PUBLISH_INITIAL_DELAY=0 \
  sh "${script}" example Dockerfile.example \
    registry.example/example:commit registry.example/example:stable \
    >"${output_file}" 2>&1

assert_contains() {
  pattern="$1"
  file="$2"
  if ! grep -Eq -- "${pattern}" "${file}"; then
    echo "expected pattern '${pattern}' in ${file}" >&2
    cat "${file}" >&2
    exit 1
  fi
}

assert_contains 'simulated extraction failure' "${output_file}"
assert_contains 'status=failed exit_code=42 .*cached_steps=1 .*extraction_events=1' "${output_file}"
assert_contains 'tag=registry.example/example:commit attempt=2 status=success exit_code=0 .*digest=sha256:bbbb' "${output_file}"
assert_contains 'tag=registry.example/example:stable attempt=1 status=success exit_code=0' "${output_file}"
assert_contains 'retrying in 0s' "${output_file}"
assert_contains '--progress=plain' "${command_log}"
assert_contains '--metadata-file' "${command_log}"

log_count="$(find "${observability_dir}" -type f -name '*.log' | wc -l | tr -d ' ')"
metadata_count="$(find "${observability_dir}" -type f -name '*.metadata.json' | wc -l | tr -d ' ')"
if [ "${log_count}" -ne 3 ]; then
  echo "expected three attempt logs, got ${log_count}" >&2
  find "${observability_dir}" -type f -print >&2
  exit 1
fi
if [ "${metadata_count}" -ne 2 ]; then
  echo "expected two successful metadata files, got ${metadata_count}" >&2
  find "${observability_dir}" -type f -print >&2
  exit 1
fi

if BUILDKIT_HOST='' sh "${script}" example Dockerfile.example registry.example/example:test \
  >/dev/null 2>&1; then
  echo "expected missing BUILDKIT_HOST to fail" >&2
  exit 1
fi

echo "buildkit publish observability self-test: OK"
