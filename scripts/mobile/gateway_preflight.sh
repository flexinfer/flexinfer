#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
HUD_ENV_FILE="${HOME}/.config/loom/hud.env"
SECRETS_ENV_FILE="${HOME}/.config/secrets/ai.env"
TOKEN_FILE="${HOME}/.config/loom/mobile-operator-token"

failures=0
TMP_DIR=""
CF_ACCESS_HEADERS_CONFIGURED=0

pass() { echo "PASS: $*"; }
warn() { echo "WARN: $*"; }
fail() {
  echo "FAIL: $*"
  failures=$((failures + 1))
}

cleanup() {
  if [ -n "${TMP_DIR}" ] && [ -d "${TMP_DIR}" ]; then
    rm -rf "${TMP_DIR}"
  fi
}
trap cleanup EXIT

require_cmd() {
  local cmd="$1"
  local hint="$2"
  if command -v "${cmd}" >/dev/null 2>&1; then
    pass "'${cmd}' is available"
  else
    fail "'${cmd}' is not installed (${hint})"
  fi
}

read_env_value() {
  local key="$1"
  local file="$2"
  local raw
  if [ ! -f "${file}" ]; then
    return 0
  fi
  raw="$(sed -n -E "s/^[[:space:]]*(export[[:space:]]+)?${key}=(.*)$/\\2/p" "${file}" | head -n1)"
  raw="${raw#"${raw%%[![:space:]]*}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  if [[ "${raw}" =~ ^\".*\"$ ]]; then
    raw="${raw:1:${#raw}-2}"
  elif [[ "${raw}" =~ ^\'.*\'$ ]]; then
    raw="${raw:1:${#raw}-2}"
  fi
  printf '%s' "${raw}"
}

resolve_config_value() {
  local key="$1"
  local val="${!key:-}"
  if [ -n "${val}" ]; then
    printf '%s' "${val}"
    return 0
  fi
  val="$(read_env_value "${key}" "${HUD_ENV_FILE}")"
  if [ -n "${val}" ]; then
    printf '%s' "${val}"
    return 0
  fi
  val="$(read_env_value "${key}" "${SECRETS_ENV_FILE}")"
  printf '%s' "${val}"
}

resolve_first_value() {
  local key val
  for key in "$@"; do
    val="$(resolve_config_value "${key}")"
    if [ -n "${val}" ]; then
      printf '%s' "${val}"
      return 0
    fi
  done
  printf ''
}

detect_gateway_url_from_ingress() {
  local host
  if ! command -v kubectl >/dev/null 2>&1; then
    return 0
  fi
  host="$(kubectl -n loom-hub get ingress loom-gateway -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || true)"
  if [ -n "${host}" ]; then
    printf 'https://%s' "${host}"
  fi
}

build_cf_access_headers() {
  local id secret
  id="$(resolve_first_value MOBILE_GATEWAY_CF_ACCESS_CLIENT_ID HUD_MOBILE_CF_ACCESS_CLIENT_ID CF_ACCESS_CLIENT_ID CLOUDFLARE_ACCESS_CLIENT_ID)"
  secret="$(resolve_first_value MOBILE_GATEWAY_CF_ACCESS_CLIENT_SECRET HUD_MOBILE_CF_ACCESS_CLIENT_SECRET CF_ACCESS_CLIENT_SECRET CLOUDFLARE_ACCESS_CLIENT_SECRET)"
  if [ -n "${id}" ] && [ -n "${secret}" ]; then
    CF_ACCESS_CLIENT_ID="${id}"
    CF_ACCESS_CLIENT_SECRET="${secret}"
    CF_ACCESS_HEADERS_CONFIGURED=1
    pass "Cloudflare Access service-token headers are configured"
  elif [ -n "${id}" ] || [ -n "${secret}" ]; then
    fail "Cloudflare Access headers are partially configured; set both client id and client secret"
  else
    warn "Cloudflare Access service-token headers are not configured in env/hud.env/ai.env"
  fi
}

is_cf_access_challenge() {
  local location="${1:-}"
  local body="${2:-}"
  if echo "${location}" | rg -qi 'cloudflareaccess\.com|/cdn-cgi/access/login'; then
    return 0
  fi
  if echo "${body}" | rg -qi 'cloudflare access|<title>.*cloudflare access|App AUD|Ray ID'; then
    return 0
  fi
  return 1
}

run_probe() {
  local name="$1"
  local url="$2"
  local auth_token="${3:-}"
  TMP_DIR="$(mktemp -d)"

  local status
  local curl_args=(
    -sS
    -m 15
    -D "${TMP_DIR}/headers"
    -o "${TMP_DIR}/body"
    -w "%{http_code}"
    "${url}"
  )

  if [ -n "${CF_ACCESS_CLIENT_ID:-}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET:-}" ]; then
    curl_args=(
      -H "CF-Access-Client-Id: ${CF_ACCESS_CLIENT_ID}"
      -H "CF-Access-Client-Secret: ${CF_ACCESS_CLIENT_SECRET}"
      "${curl_args[@]}"
    )
  fi
  if [ -n "${auth_token}" ]; then
    curl_args=(
      -H "Authorization: Bearer ${auth_token}"
      "${curl_args[@]}"
    )
  fi

  status="$(curl "${curl_args[@]}" || true)"
  PROBE_STATUS="${status}"
  PROBE_BODY="$(cat "${TMP_DIR}/body" 2>/dev/null || true)"
  PROBE_CONTENT_TYPE="$(awk -F': ' 'tolower($1)=="content-type" {gsub(/\r/,"",$2); print tolower($2); exit}' "${TMP_DIR}/headers")"
  PROBE_LOCATION="$(awk -F': ' 'tolower($1)=="location" {gsub(/\r/,"",$2); print $2; exit}' "${TMP_DIR}/headers")"

  if [ -z "${PROBE_STATUS}" ] || [ "${PROBE_STATUS}" = "000" ]; then
    fail "${name}: ${url} is unreachable (timeout/network)"
    return 1
  fi
  return 0
}

validate_mcp_surface() {
  local url="${GATEWAY_URL%/}/health"
  run_probe "MCP surface" "${url}" || return 0

  case "${PROBE_STATUS}" in
    200)
      pass "MCP surface reachable: ${url}"
      ;;
    302|303|307|308)
      if is_cf_access_challenge "${PROBE_LOCATION}" "${PROBE_BODY}"; then
        if [ "${CF_ACCESS_HEADERS_CONFIGURED}" -eq 1 ]; then
          fail "MCP surface redirects to Cloudflare Access login (${PROBE_LOCATION})"
        else
          warn "MCP surface is Cloudflare Access-protected and service-token headers are not configured; skipping external validation"
        fi
      else
        fail "MCP surface redirected unexpectedly (${PROBE_STATUS}) to ${PROBE_LOCATION}"
      fi
      ;;
    401|403)
      if is_cf_access_challenge "${PROBE_LOCATION}" "${PROBE_BODY}"; then
        if [ "${CF_ACCESS_HEADERS_CONFIGURED}" -eq 1 ]; then
          fail "MCP surface is blocked by Cloudflare Access challenge despite configured service-token headers"
        else
          warn "MCP surface is blocked by Cloudflare Access challenge; skipping external validation until headers are configured"
        fi
      else
        fail "MCP surface returned HTTP ${PROBE_STATUS}; check gateway policy and headers"
      fi
      ;;
    *)
      fail "MCP surface returned HTTP ${PROBE_STATUS}"
      ;;
  esac
}

validate_mobile_surface() {
  local url="${GATEWAY_URL%/}/api/mobile/v1/ping"
  run_probe "Mobile surface" "${url}" || return 0

  case "${PROBE_STATUS}" in
    200)
      if echo "${PROBE_BODY}" | rg -q '"ok"\s*:\s*true' && echo "${PROBE_BODY}" | rg -q '"pong"\s*:\s*true'; then
        pass "Mobile surface reachable with bearer token: ${url}"
      elif echo "${PROBE_BODY}" | rg -q '"ok"\s*:\s*true'; then
        pass "Mobile surface reachable and returned JSON envelope: ${url}"
      else
        fail "Mobile surface returned HTTP 200 without mobile envelope"
      fi
      ;;
    401)
      if echo "${PROBE_BODY}" | rg -q '"ok"\s*:\s*false' && echo "${PROBE_BODY}" | rg -q '"code"\s*:\s*"(unauthorized|not_configured|token_revoked)"'; then
        pass "Mobile route exists and returned auth envelope: ${url}"
      elif is_cf_access_challenge "${PROBE_LOCATION}" "${PROBE_BODY}"; then
        if [ "${CF_ACCESS_HEADERS_CONFIGURED}" -eq 1 ]; then
          fail "Mobile route is blocked by Cloudflare Access challenge despite configured service-token headers"
        else
          warn "Mobile route is Cloudflare Access-protected and headers are not configured; skipping external mobile probe"
        fi
      else
        fail "Mobile route returned HTTP 401 without mobile API envelope"
      fi
      ;;
    404)
      fail "Mobile route missing: ${url} returned 404 (gateway path split not configured)"
      ;;
    302|303|307|308)
      if is_cf_access_challenge "${PROBE_LOCATION}" "${PROBE_BODY}"; then
        if [ "${CF_ACCESS_HEADERS_CONFIGURED}" -eq 1 ]; then
          fail "Mobile route redirects to Cloudflare Access login (${PROBE_LOCATION})"
        else
          warn "Mobile route redirects to Cloudflare Access and headers are not configured; skipping external mobile probe"
        fi
      else
        fail "Mobile route redirected unexpectedly (${PROBE_STATUS}) to ${PROBE_LOCATION}"
      fi
      ;;
    *)
      if is_cf_access_challenge "${PROBE_LOCATION}" "${PROBE_BODY}"; then
        if [ "${CF_ACCESS_HEADERS_CONFIGURED}" -eq 1 ]; then
          fail "Mobile route returned non-API Cloudflare Access response (HTTP ${PROBE_STATUS})"
        else
          warn "Mobile route is Access-gated and returned non-API HTML; skipping external mobile probe"
        fi
      else
        fail "Mobile route returned HTTP ${PROBE_STATUS} (expected 200/401 mobile JSON envelope)"
      fi
      ;;
  esac
}

verify_cluster_objects() {
  if ! command -v kubectl >/dev/null 2>&1; then
    warn "kubectl not available; skipping cluster object checks"
    return
  fi
  pass "'kubectl' is available"

  if kubectl -n loom-hub get deploy mobile-hud >/dev/null 2>&1; then
    pass "Cluster deployment exists: loom-hub/mobile-hud"
  else
    fail "Cluster deployment not found: loom-hub/mobile-hud"
  fi

  if kubectl -n loom-hub get svc mobile-hud >/dev/null 2>&1; then
    pass "Cluster service exists: loom-hub/mobile-hud"
  else
    fail "Cluster service not found: loom-hub/mobile-hud"
  fi

  if kubectl -n loom-hub get ingress loom-gateway >/dev/null 2>&1; then
    local has_mobile_path
    has_mobile_path="$(kubectl -n loom-hub get ingress loom-gateway -o jsonpath='{.spec.rules[0].http.paths[?(@.path=="/api/mobile/v1")].path}' 2>/dev/null || true)"
    if [ "${has_mobile_path}" = "/api/mobile/v1" ]; then
      pass "Ingress path split includes /api/mobile/v1"
    else
      fail "Ingress loom-gateway is missing /api/mobile/v1 path"
    fi
  else
    fail "Ingress not found: loom-hub/loom-gateway"
  fi
}

echo "== Loom Companion Gateway Preflight =="
echo "Repo: ${REPO_ROOT}"
echo

require_cmd curl "install curl"
require_cmd openssl "install OpenSSL (macOS builtin is fine)"
require_cmd rg "install ripgrep"

if [ -x "${REPO_ROOT}/bin/loom" ]; then
  pass "Found local loom binary at ${REPO_ROOT}/bin/loom"
elif command -v loom >/dev/null 2>&1; then
  pass "Found loom in PATH"
else
  fail "loom binary not found; run 'make loom'"
fi

GATEWAY_URL="$(resolve_first_value MOBILE_GATEWAY_URL HUD_MOBILE_GATEWAY_URL)"
if [ -z "${GATEWAY_URL}" ]; then
  AUTO_GATEWAY_URL="$(detect_gateway_url_from_ingress)"
  if [ -n "${AUTO_GATEWAY_URL}" ]; then
    GATEWAY_URL="${AUTO_GATEWAY_URL}"
    warn "MOBILE_GATEWAY_URL not set; auto-detected ${GATEWAY_URL} from ingress loom-hub/loom-gateway"
  fi
fi

if [ -z "${GATEWAY_URL}" ]; then
  fail "MOBILE_GATEWAY_URL is required (example: https://mcp.flexinfer.ai)"
elif [[ "${GATEWAY_URL}" =~ ^https:// ]]; then
  pass "Gateway URL is HTTPS: ${GATEWAY_URL}"
else
  fail "Gateway URL must start with https:// (got: ${GATEWAY_URL})"
fi

build_cf_access_headers
verify_cluster_objects
validate_mcp_surface
validate_mobile_surface

EXISTING_TOKEN="$(resolve_config_value HUD_MOBILE_OPERATOR_TOKEN)"
if [ -z "${EXISTING_TOKEN}" ] && [ -f "${TOKEN_FILE}" ]; then
  EXISTING_TOKEN="$(tr -d '[:space:]' < "${TOKEN_FILE}")"
fi
if [ -n "${EXISTING_TOKEN}" ]; then
  run_probe "Mobile auth" "${GATEWAY_URL%/}/api/mobile/v1/ping" "${EXISTING_TOKEN}" || true
  if [ "${PROBE_STATUS}" = "200" ] && echo "${PROBE_BODY}" | rg -q '"ok"\s*:\s*true'; then
    pass "Existing mobile token is currently valid for gateway ping"
  elif [ "${PROBE_STATUS}" = "401" ]; then
    warn "Existing mobile token was rejected; run 'make mobile-gateway-dev' to rotate"
  else
    warn "Existing token probe returned HTTP ${PROBE_STATUS}"
  fi
fi

echo
if [ "${failures}" -gt 0 ]; then
  echo "Preflight failed with ${failures} blocking issue(s)."
  exit 1
fi

echo "Preflight passed."
echo "Next steps:"
echo "  1) Rotate + rollout mobile token: make mobile-gateway-dev"
echo "  2) In app select Gateway mode"
echo "  3) Use the printed Gateway URL and Bearer token"
