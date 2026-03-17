#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  gitlab_green_loop.sh [options]

Options:
  --project <group/project>         GitLab project path. Defaults to origin remote slug.
  --source-branch <branch>          Source branch. Defaults to current branch.
  --target-branch <branch>          Target branch. Defaults to main.
  --mr-title <title>                Merge request title. Defaults to last commit subject.
  --mr-description <text>           Merge request description text.
  --mr-description-file <path>      Read merge request description from file.
  --commit-message <message>        Commit message to use before pushing.
  --stage-all                       Stage all tracked and untracked changes before commit.
  --stage-tracked                   Stage tracked changes only before commit (default).
  --files <comma,separated,paths>   Stage only the listed paths before commit.
  --verify-command <command>        Command to run before commit/push.
  --skip-local-verify               Skip the verify command.
  --no-push                         Do not push the branch.
  --no-auto-merge                   Do not request auto-merge on the MR.
  --no-wait-for-merge               Do not wait for merged state after CI succeeds.
  --no-remove-source-branch         Keep the source branch after merge.
  --no-squash                       Do not request squash merge.
  --timeout-seconds <n>             CI / merge wait timeout in seconds (default: 1800).
  --poll-interval-seconds <n>       Poll interval in seconds (default: 10).
  -h, --help                        Show this help.

Examples:
  gitlab_green_loop.sh \
    --project services/loom-core \
    --target-branch main \
    --verify-command 'go test ./...' \
    --commit-message 'fix(ci): stabilize gitlab parser' \
    --mr-title 'fix(ci): stabilize gitlab parser' \
    --stage-all

  gitlab_green_loop.sh \
    --mr-title 'fix(ci): rerun on clean fixture state' \
    --no-auto-merge
EOF
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "error: required command not found: $cmd" >&2
    exit 1
  fi
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

urlencode() {
  python3 - "$1" <<'PY'
import sys
from urllib.parse import quote

print(quote(sys.argv[1], safe=""))
PY
}

json_field() {
  local payload="$1"
  local expr="$2"
  python3 - "$payload" "$expr" <<'PY'
import json
import sys

obj = json.loads(sys.argv[1])
expr = sys.argv[2]

value = obj
for part in expr.split("."):
    if isinstance(value, dict):
        value = value.get(part)
    else:
        value = None
        break

if value is None:
    sys.exit(1)
if isinstance(value, bool):
    print("true" if value else "false")
elif isinstance(value, (dict, list)):
    print(json.dumps(value))
else:
    print(value)
PY
}

json_first_open_mr() {
  local payload="$1"
  python3 - "$payload" <<'PY'
import json
import sys

arr = json.loads(sys.argv[1])
if isinstance(arr, list) and arr:
    first = arr[0]
    print(first.get("iid", ""))
    print(first.get("web_url", ""))
    print(first.get("title", ""))
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

extract_pipeline_id() {
  local payload="$1"
  python3 - "$payload" <<'PY'
import json
import sys

arr = json.loads(sys.argv[1])
if isinstance(arr, list) and arr:
    first = arr[0]
    pid = first.get("id")
    if pid is not None:
        print(pid)
PY
}

project=""
source_branch=""
target_branch="main"
mr_title=""
mr_description=""
mr_description_file=""
commit_message=""
verify_command=""
timeout_seconds=1800
poll_interval_seconds=10
push_branch=true
auto_merge=true
wait_for_merge=true
remove_source_branch=true
squash=true
skip_local_verify=false
stage_mode="tracked"
declare -a files=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --project)
      project="${2:-}"
      shift 2
      ;;
    --source-branch)
      source_branch="${2:-}"
      shift 2
      ;;
    --target-branch)
      target_branch="${2:-}"
      shift 2
      ;;
    --mr-title)
      mr_title="${2:-}"
      shift 2
      ;;
    --mr-description)
      mr_description="${2:-}"
      shift 2
      ;;
    --mr-description-file)
      mr_description_file="${2:-}"
      shift 2
      ;;
    --commit-message)
      commit_message="${2:-}"
      shift 2
      ;;
    --verify-command)
      verify_command="${2:-}"
      shift 2
      ;;
    --skip-local-verify)
      skip_local_verify=true
      shift
      ;;
    --stage-all)
      stage_mode="all"
      shift
      ;;
    --stage-tracked)
      stage_mode="tracked"
      shift
      ;;
    --files)
      IFS=',' read -r -a files <<<"${2:-}"
      stage_mode="files"
      shift 2
      ;;
    --no-push)
      push_branch=false
      shift
      ;;
    --no-auto-merge)
      auto_merge=false
      shift
      ;;
    --no-wait-for-merge)
      wait_for_merge=false
      shift
      ;;
    --no-remove-source-branch)
      remove_source_branch=false
      shift
      ;;
    --no-squash)
      squash=false
      shift
      ;;
    --timeout-seconds)
      timeout_seconds="${2:-}"
      shift 2
      ;;
    --poll-interval-seconds)
      poll_interval_seconds="${2:-}"
      shift 2
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

require_cmd git
require_cmd glab
require_cmd python3

if [[ -n "$mr_description" && -n "$mr_description_file" ]]; then
  echo "error: use only one of --mr-description or --mr-description-file" >&2
  exit 1
fi

if [[ -n "$mr_description_file" ]]; then
  if [[ ! -f "$mr_description_file" ]]; then
    echo "error: description file not found: $mr_description_file" >&2
    exit 1
  fi
  mr_description="$(cat "$mr_description_file")"
fi

if [[ -z "$source_branch" ]]; then
  source_branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
fi

if [[ -z "$source_branch" || "$source_branch" == "HEAD" ]]; then
  echo "error: unable to determine source branch; pass --source-branch explicitly" >&2
  exit 1
fi

origin_url="$(git remote get-url origin 2>/dev/null || true)"
if [[ -z "$origin_url" ]]; then
  echo "error: git remote 'origin' is required" >&2
  exit 1
fi

parsed_origin="$(parse_git_origin "$origin_url" || true)"
provider="$(echo "$parsed_origin" | awk '{print $1}')"
detected_project="$(echo "$parsed_origin" | awk '{print $2}')"

if [[ "$provider" != "gitlab" ]]; then
  echo "error: this helper currently supports GitLab remotes only" >&2
  exit 1
fi

if [[ -z "$project" ]]; then
  project="$detected_project"
fi

if [[ -z "$project" ]]; then
  echo "error: unable to determine project; pass --project explicitly" >&2
  exit 1
fi

if [[ -z "$mr_title" ]]; then
  mr_title="$(git log -1 --pretty=%s 2>/dev/null || true)"
fi

if [[ -z "$mr_title" ]]; then
  mr_title="fix(ci): ${source_branch}"
fi

if [[ "$skip_local_verify" != "true" && -n "$verify_command" ]]; then
  echo "==> Running local verification: $verify_command"
  bash -lc "$verify_command"
fi

if [[ -n "$commit_message" ]]; then
  case "$stage_mode" in
    all)
      echo "==> Staging all changes"
      git add -A
      ;;
    tracked)
      echo "==> Staging tracked changes"
      git add -u
      ;;
    files)
      if [[ "${#files[@]}" -eq 0 ]]; then
        echo "error: --files requires at least one path" >&2
        exit 1
      fi
      echo "==> Staging selected files: ${files[*]}"
      git add -- "${files[@]}"
      ;;
    *)
      echo "error: unknown stage mode: $stage_mode" >&2
      exit 1
      ;;
  esac

  if git diff --cached --quiet; then
    echo "error: no staged changes to commit" >&2
    exit 1
  fi

  echo "==> Creating commit"
  git commit -m "$commit_message"
fi

head_sha="$(git rev-parse HEAD)"
echo "==> Source branch: $source_branch"
echo "==> Target branch: $target_branch"
echo "==> HEAD sha: $head_sha"

if [[ "$push_branch" == "true" ]]; then
  echo "==> Pushing branch"
  git push -u origin "$source_branch"
fi

project_encoded="$(urlencode "$project")"

echo "==> Resolving existing merge request"
existing_json="$(glab api "projects/${project_encoded}/merge_requests?state=opened&source_branch=${source_branch}&target_branch=${target_branch}" 2>/dev/null || echo '[]')"
mr_iid=""
mr_url=""
existing_title=""

if [[ "$(printf '%s' "$existing_json" | python3 -c 'import json,sys; arr=json.load(sys.stdin); print(1 if isinstance(arr,list) and arr else 0)')" == "1" ]]; then
  mapfile -t mr_fields < <(json_first_open_mr "$existing_json")
  mr_iid="${mr_fields[0]:-}"
  mr_url="${mr_fields[1]:-}"
  existing_title="${mr_fields[2]:-}"
  echo "==> Reusing MR !${mr_iid}: ${existing_title}"
else
  echo "==> Creating merge request"
  create_args=(
    api
    -X POST
    "projects/${project_encoded}/merge_requests"
    -f "source_branch=${source_branch}"
    -f "target_branch=${target_branch}"
    -f "title=${mr_title}"
  )
  if [[ -n "$mr_description" ]]; then
    create_args+=(-f "description=${mr_description}")
  fi
  if [[ "$remove_source_branch" == "true" ]]; then
    create_args+=(-f "remove_source_branch=true")
  fi

  create_json="$(glab "${create_args[@]}")"
  mr_iid="$(json_field "$create_json" "iid")"
  mr_url="$(json_field "$create_json" "web_url")"
  echo "==> Created MR !${mr_iid}: ${mr_url}"
fi

if [[ -z "$mr_iid" ]]; then
  echo "error: unable to determine merge request iid" >&2
  exit 1
fi

if [[ "$auto_merge" == "true" ]]; then
  echo "==> Requesting GitLab auto-merge"
  merge_args=(
    api
    -X PUT
    "projects/${project_encoded}/merge_requests/${mr_iid}/merge"
    -f "auto_merge=true"
    -f "sha=${head_sha}"
  )
  if [[ "$squash" == "true" ]]; then
    merge_args+=(-f "squash=true")
  fi
  if [[ "$remove_source_branch" == "true" ]]; then
    merge_args+=(-f "should_remove_source_branch=true")
  fi

  if ! glab "${merge_args[@]}" >/dev/null; then
    echo "warning: unable to request auto-merge. The MR may need approvals or manual intervention." >&2
  fi
fi

LOOM="$(resolve_loom)"

echo "==> Resolving latest pipeline for ${project} ref=${source_branch}"
pipeline_json="$(glab api "projects/${project_encoded}/pipelines?ref=${source_branch}&per_page=1")"
pipeline_id="$(extract_pipeline_id "$pipeline_json" || true)"

if [[ -z "$pipeline_id" ]]; then
  echo "error: unable to determine latest pipeline id for branch ${source_branch}" >&2
  exit 1
fi

echo "==> Polling pipeline ${pipeline_id}"
project_json="$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$project")"
poll_args="{\"project\":${project_json},\"pipeline_id\":${pipeline_id},\"timeout_seconds\":${timeout_seconds},\"poll_interval_seconds\":${poll_interval_seconds},\"include_job_logs\":true}"
poll_out="$("$LOOM" tools call gitlab__poll_pipeline --args "$poll_args" --json)"
echo "$poll_out"

status="$(
  python3 - "$poll_out" <<'PY'
import json
import re
import sys

obj = json.loads(sys.argv[1])
text = ""
if isinstance(obj, dict):
    content = obj.get("content")
    if isinstance(content, list) and content:
        first = content[0]
        if isinstance(first, dict):
            text = str(first.get("text", ""))

m = re.search(r'^\s*status:\s*([A-Za-z_]+)\s*$', text, re.MULTILINE)
if m:
    print(m.group(1).lower())
PY
)"

if [[ "$status" != "success" ]]; then
  echo "==> Pipeline ${pipeline_id} did not succeed (status=${status:-unknown}); fetching summary"
  summary_args="{\"project\":${project_json},\"pipeline_id\":${pipeline_id},\"include_failed_job_logs\":true,\"include_test_report\":true}"
  "$LOOM" tools call gitlab__pipeline_summary --args "$summary_args" --json || true
  exit 1
fi

echo "==> Pipeline ${pipeline_id} succeeded"

if [[ "$wait_for_merge" != "true" ]]; then
  echo "==> Not waiting for merged state"
  echo "MR: ${mr_url}"
  exit 0
fi

echo "==> Waiting for MR !${mr_iid} to merge"
start_epoch="$(date +%s)"

while true; do
  mr_json="$(glab api "projects/${project_encoded}/merge_requests/${mr_iid}")"
  mr_state="$(json_field "$mr_json" "state" || true)"
  detailed_status="$(json_field "$mr_json" "detailed_merge_status" || true)"
  web_url="$(json_field "$mr_json" "web_url" || true)"

  echo "state=${mr_state:-unknown} detailed_merge_status=${detailed_status:-unknown} url=${web_url:-$mr_url}"

  if [[ "$mr_state" == "merged" ]]; then
    echo "==> Merge request merged successfully"
    exit 0
  fi

  now_epoch="$(date +%s)"
  elapsed=$((now_epoch - start_epoch))
  if (( elapsed >= timeout_seconds )); then
    echo "error: timed out waiting for merge request !${mr_iid} to merge" >&2
    exit 1
  fi

  sleep "$poll_interval_seconds"
done
