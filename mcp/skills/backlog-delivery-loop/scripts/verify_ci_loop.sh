#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  verify_ci_loop.sh [--provider auto|gitlab|github] [--project <group/project>] [--repo <owner/repo>] [--pipeline-id <id> | --run-id <id> | --ref <branch>] [--timeout-seconds <n>] [--poll-interval-seconds <n>] [--include-logs|--no-include-logs]

Examples:
  verify_ci_loop.sh --provider auto --ref main
  verify_ci_loop.sh --project services/loom-core --ref main
  verify_ci_loop.sh --provider github --repo owner/repo --ref main
  verify_ci_loop.sh --project services/loom-core --pipeline-id 12345

Notes:
  - Provider defaults to auto-detect from git origin URL.
  - GitLab path uses loom gitlab MCP tools (`gitlab__list_pipelines`, `gitlab__poll_pipeline`).
  - GitHub path uses GitHub CLI (`gh run list/view`), so `gh` auth is required.
  - If no pipeline/run id is provided, branch ref is used to resolve latest run.
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "error: required command not found: $cmd" >&2
    exit 1
  fi
}

json_string() {
  python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$1"
}

extract_first_match() {
  local pattern="$1"
  local payload="$2"
  python3 - "$pattern" "$payload" <<'PY'
import json
import re
import sys

pattern = sys.argv[1]
raw = sys.argv[2]

obj = json.loads(raw)
text = ""
if isinstance(obj, dict):
    content = obj.get("content")
    if isinstance(content, list) and content:
        first = content[0]
        if isinstance(first, dict):
            text = str(first.get("text", ""))

m = re.search(pattern, text, re.MULTILINE)
if m:
    print(m.group(1))
PY
}

extract_pipeline_id() {
  local payload="$1"
  python3 - "$payload" <<'PY'
import json
import re
import sys

raw = sys.argv[1]
obj = json.loads(raw)
text = ""
if isinstance(obj, dict):
    content = obj.get("content")
    if isinstance(content, list) and content:
        first = content[0]
        if isinstance(first, dict):
            text = str(first.get("text", ""))

patterns = [
    r'^\s*id:\s*(\d+)\s*$',
    r'^\s*"[^"]*",\s*(\d+),',
    r'/pipelines/(\d+)',
]

for pat in patterns:
    m = re.search(pat, text, re.MULTILINE)
    if m:
        print(m.group(1))
        break
PY
}

parse_git_origin() {
  local origin_url="$1"
  python3 - "$origin_url" <<'PY'
import re
import sys
from urllib.parse import urlparse

raw = sys.argv[1].strip()
host = ""
path = ""

ssh_match = re.match(r'^(?:ssh://)?git@([^:/]+)[:/](.+?)(?:\.git)?$', raw)
if ssh_match:
    host = ssh_match.group(1).lower()
    path = ssh_match.group(2).strip("/")
else:
    try:
        parsed = urlparse(raw)
        if parsed.scheme in ("http", "https", "ssh", "git"):
            host = (parsed.hostname or "").lower()
            path = parsed.path.strip("/").removesuffix(".git")
    except Exception:
        pass

provider = "unknown"
if "gitlab" in host:
    provider = "gitlab"
elif "github" in host:
    provider = "github"

if path:
    print(f"{provider} {path}")
PY
}

resolve_loom() {
  if [[ -n "${LOOM_BIN:-}" ]]; then
    echo "$LOOM_BIN"
    return
  fi

  if command -v loom >/dev/null 2>&1; then
    command -v loom
    return
  fi

  if [[ -x "./bin/loom" ]]; then
    echo "./bin/loom"
    return
  fi

  echo "error: could not find loom binary (set LOOM_BIN or ensure loom is on PATH)" >&2
  exit 1
}

provider="auto"
project=""
repo=""
pipeline_id=""
run_id=""
ref=""
timeout_seconds=900
poll_interval_seconds=10
include_logs=true

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider)
      provider="${2:-}"
      shift 2
      ;;
    --project)
      project="${2:-}"
      shift 2
      ;;
    --repo)
      repo="${2:-}"
      shift 2
      ;;
    --pipeline-id)
      pipeline_id="${2:-}"
      shift 2
      ;;
    --run-id)
      run_id="${2:-}"
      shift 2
      ;;
    --ref)
      ref="${2:-}"
      shift 2
      ;;
    --timeout-seconds)
      timeout_seconds="${2:-}"
      shift 2
      ;;
    --poll-interval-seconds)
      poll_interval_seconds="${2:-}"
      shift 2
      ;;
    --include-logs)
      include_logs=true
      shift
      ;;
    --no-include-logs)
      include_logs=false
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

if [[ -z "$pipeline_id" && -z "$run_id" && -z "$ref" ]] && command -v git >/dev/null 2>&1; then
  ref="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [[ "$ref" == "HEAD" ]]; then
    ref=""
  fi
fi

require_cmd python3

detected_provider=""
detected_slug=""
if command -v git >/dev/null 2>&1; then
  origin_url="$(git remote get-url origin 2>/dev/null || true)"
  if [[ -n "$origin_url" ]]; then
    parsed="$(parse_git_origin "$origin_url" || true)"
    detected_provider="$(echo "$parsed" | awk '{print $1}')"
    detected_slug="$(echo "$parsed" | awk '{print $2}')"
  fi
fi

if [[ "$provider" == "auto" ]]; then
  provider="$detected_provider"
fi

if [[ "$provider" != "gitlab" && "$provider" != "github" ]]; then
  echo "error: unable to detect provider from origin; set --provider gitlab|github explicitly" >&2
  exit 1
fi

if [[ "$provider" == "gitlab" ]]; then
  LOOM="$(resolve_loom)"
  if [[ -z "$project" ]]; then
    project="$detected_slug"
  fi
  if [[ -z "$project" ]]; then
    echo "error: --project is required for gitlab mode" >&2
    exit 1
  fi
  if [[ -z "$pipeline_id" && -z "$ref" ]]; then
    echo "error: provide --pipeline-id or --ref for gitlab mode" >&2
    exit 1
  fi

  project_json="$(json_string "$project")"

  if [[ -z "$pipeline_id" ]]; then
    ref_json="$(json_string "$ref")"
    list_args="{\"project\":${project_json},\"ref\":${ref_json},\"per_page\":1,\"page\":1}"

    echo "==> [gitlab] Resolving latest pipeline for ${project} ref=${ref}"
    list_out="$("$LOOM" tools call gitlab__list_pipelines --args "$list_args" --json)"

    pipeline_id="$(extract_pipeline_id "$list_out" || true)"
    if [[ -z "$pipeline_id" ]]; then
      echo "error: unable to resolve pipeline id from gitlab__list_pipelines output" >&2
      echo "$list_out"
      exit 1
    fi
  fi

  echo "==> [gitlab] Polling pipeline ${pipeline_id} (timeout=${timeout_seconds}s interval=${poll_interval_seconds}s)"
  poll_args="{\"project\":${project_json},\"pipeline_id\":${pipeline_id},\"timeout_seconds\":${timeout_seconds},\"poll_interval_seconds\":${poll_interval_seconds},\"include_job_logs\":${include_logs}}"
  poll_out="$("$LOOM" tools call gitlab__poll_pipeline --args "$poll_args" --json)"
  echo "$poll_out"

  status="$(extract_first_match '^\s*status:\s*([A-Za-z_]+)\s*$' "$poll_out" || true)"
  status="${status,,}"

  if [[ "$status" == "success" ]]; then
    echo "==> [gitlab] Pipeline ${pipeline_id} succeeded"
    exit 0
  fi

  echo "==> [gitlab] Pipeline ${pipeline_id} did not succeed (status=${status:-unknown}); fetching summary"
  summary_args="{\"project\":${project_json},\"pipeline_id\":${pipeline_id},\"include_failed_job_logs\":${include_logs},\"include_test_report\":true}"
  summary_out="$("$LOOM" tools call gitlab__pipeline_summary --args "$summary_args" --json || true)"
  echo "$summary_out"
  exit 1
fi

# GitHub mode
require_cmd gh
if [[ -z "$repo" ]]; then
  repo="$detected_slug"
fi
if [[ -z "$repo" ]]; then
  echo "error: --repo is required for github mode" >&2
  exit 1
fi
if [[ -z "$run_id" && -z "$ref" ]]; then
  echo "error: provide --run-id or --ref for github mode" >&2
  exit 1
fi

if [[ -z "$run_id" ]]; then
  echo "==> [github] Resolving latest workflow run for ${repo} ref=${ref}"
  if [[ -n "$ref" ]]; then
    list_json="$(gh run list --repo "$repo" --branch "$ref" --limit 1 --json databaseId,status,conclusion,headBranch,url)"
  else
    list_json="$(gh run list --repo "$repo" --limit 1 --json databaseId,status,conclusion,headBranch,url)"
  fi
  run_id="$(python3 - "$list_json" <<'PY'
import json
import sys
arr = json.loads(sys.argv[1])
if isinstance(arr, list) and arr:
    rid = arr[0].get("databaseId")
    if rid is not None:
        print(rid)
PY
)"
  if [[ -z "$run_id" ]]; then
    echo "error: unable to resolve GitHub workflow run id from gh output" >&2
    echo "$list_json"
    exit 1
  fi
fi

echo "==> [github] Polling workflow run ${run_id} (timeout=${timeout_seconds}s interval=${poll_interval_seconds}s)"
start_epoch="$(date +%s)"

while true; do
  view_json="$(gh run view "$run_id" --repo "$repo" --json status,conclusion,url,headBranch,workflowName,displayTitle)"
  read -r gh_status gh_conclusion gh_url <<<"$(python3 - "$view_json" <<'PY'
import json
import sys
obj = json.loads(sys.argv[1])
status = (obj.get("status") or "").strip()
conclusion = (obj.get("conclusion") or "").strip()
url = (obj.get("url") or "").strip()
print(status, conclusion, url)
PY
)"
  gh_status="${gh_status,,}"
  gh_conclusion="${gh_conclusion,,}"
  echo "status=${gh_status} conclusion=${gh_conclusion:-n/a} url=${gh_url}"

  if [[ "$gh_status" == "completed" ]]; then
    if [[ "$gh_conclusion" == "success" ]]; then
      echo "==> [github] Workflow run ${run_id} succeeded"
      exit 0
    fi
    echo "==> [github] Workflow run ${run_id} failed (conclusion=${gh_conclusion:-unknown})"
    if [[ "$include_logs" == "true" ]]; then
      gh run view "$run_id" --repo "$repo" --log-failed || true
    fi
    exit 1
  fi

  now_epoch="$(date +%s)"
  elapsed=$((now_epoch - start_epoch))
  if (( elapsed >= timeout_seconds )); then
    echo "error: [github] timed out waiting for run ${run_id}" >&2
    exit 1
  fi
  sleep "$poll_interval_seconds"
done
