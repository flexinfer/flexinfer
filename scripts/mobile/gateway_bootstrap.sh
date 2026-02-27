#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
LOOM_BIN="${REPO_ROOT}/bin/loom"
HUD_ENV_FILE="${HOME}/.config/loom/hud.env"
SECRETS_ENV_FILE="${HOME}/.config/secrets/ai.env"
TOKEN_FILE="${HOME}/.config/loom/mobile-operator-token"
SCOPES_DEFAULT="mobile:read,mobile:session:create,mobile:session:end,mobile:push"

require_cmd() {
  local cmd="$1"
  local hint="$2"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "ERROR: required command '${cmd}' is missing (${hint})" >&2
    exit 1
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
  host="$(kubectl -n loom-hub get ingress loom-gateway -o jsonpath='{.spec.rules[0].host}' 2>/dev/null || true)"
  if [ -n "${host}" ]; then
    printf 'https://%s' "${host}"
  fi
}

upsert_env_key() {
  local file="$1"
  local key="$2"
  local value="$3"
  mkdir -p "$(dirname "${file}")"
  touch "${file}"

  local tmp
  tmp="$(mktemp)"
  awk -v key="${key}" -v value="${value}" '
  BEGIN { updated = 0 }
  {
    if ($0 ~ "^[[:space:]]*(export[[:space:]]+)?" key "=") {
      print key "=" value
      updated = 1
      next
    }
    print
  }
  END {
    if (updated == 0) {
      print key "=" value
    }
  }
  ' "${file}" > "${tmp}"
  mv "${tmp}" "${file}"
  chmod 600 "${file}" || true
}

curl_with_headers() {
  local url="$1"
  local bearer="${2:-}"
  local args=( -sS -m 15 -w '%{http_code}' -o /tmp/loom-mobile-gateway-body )

  if [ -n "${CF_ACCESS_CLIENT_ID:-}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET:-}" ]; then
    args+=( -H "CF-Access-Client-Id: ${CF_ACCESS_CLIENT_ID}" -H "CF-Access-Client-Secret: ${CF_ACCESS_CLIENT_SECRET}" )
  fi
  if [ -n "${bearer}" ]; then
    args+=( -H "Authorization: Bearer ${bearer}" )
  fi

  curl "${args[@]}" "${url}" || true
}

echo "== Loom Mobile Gateway Bootstrap =="
echo "Repo: ${REPO_ROOT}"
echo

require_cmd curl "install curl"
require_cmd openssl "install OpenSSL"
require_cmd kubectl "install kubectl and configure cluster access"
require_cmd rg "install ripgrep"

if [ ! -x "${LOOM_BIN}" ] && ! command -v loom >/dev/null 2>&1; then
  echo "ERROR: loom binary not found (run 'make loom')" >&2
  exit 1
fi

GATEWAY_URL="$(resolve_first_value MOBILE_GATEWAY_URL HUD_MOBILE_GATEWAY_URL)"
if [ -z "${GATEWAY_URL}" ]; then
  GATEWAY_URL="$(detect_gateway_url_from_ingress)"
fi
if [ -z "${GATEWAY_URL}" ]; then
  echo "ERROR: MOBILE_GATEWAY_URL is required (example: https://mcp.flexinfer.ai)" >&2
  exit 1
fi
if ! [[ "${GATEWAY_URL}" =~ ^https:// ]]; then
  echo "ERROR: MOBILE_GATEWAY_URL must start with https:// (got ${GATEWAY_URL})" >&2
  exit 1
fi

CF_ACCESS_CLIENT_ID="$(resolve_first_value MOBILE_GATEWAY_CF_ACCESS_CLIENT_ID HUD_MOBILE_CF_ACCESS_CLIENT_ID CF_ACCESS_CLIENT_ID)"
CF_ACCESS_CLIENT_SECRET="$(resolve_first_value MOBILE_GATEWAY_CF_ACCESS_CLIENT_SECRET HUD_MOBILE_CF_ACCESS_CLIENT_SECRET CF_ACCESS_CLIENT_SECRET)"
if [ -n "${CF_ACCESS_CLIENT_ID}" ] && [ -z "${CF_ACCESS_CLIENT_SECRET}" ]; then
  echo "ERROR: Cloudflare Access client secret is missing" >&2
  exit 1
fi
if [ -n "${CF_ACCESS_CLIENT_SECRET}" ] && [ -z "${CF_ACCESS_CLIENT_ID}" ]; then
  echo "ERROR: Cloudflare Access client id is missing" >&2
  exit 1
fi
if [ -n "${CF_ACCESS_CLIENT_ID}" ]; then
  echo "Using Cloudflare Access service-token headers from local config"
else
  echo "WARN: Cloudflare Access service-token headers are not configured locally"
fi

if ! kubectl -n loom-hub get deploy mobile-hud >/dev/null 2>&1; then
  echo "ERROR: deployment loom-hub/mobile-hud not found. Apply GitOps changes first." >&2
  exit 1
fi
if ! kubectl -n loom-hub get secret loom-secrets >/dev/null 2>&1; then
  echo "ERROR: secret loom-hub/loom-secrets not found" >&2
  exit 1
fi

TOKEN="$(openssl rand -hex 32)"
SCOPES="$(resolve_first_value HUD_MOBILE_OPERATOR_SCOPES MOBILE_HUD_OPERATOR_SCOPES)"
if [ -z "${SCOPES}" ]; then
  SCOPES="${SCOPES_DEFAULT}"
fi

mkdir -p "$(dirname "${TOKEN_FILE}")"
printf '%s\n' "${TOKEN}" > "${TOKEN_FILE}"
chmod 600 "${TOKEN_FILE}"

upsert_env_key "${HUD_ENV_FILE}" "HUD_MOBILE_GATEWAY_URL" "${GATEWAY_URL}"
upsert_env_key "${HUD_ENV_FILE}" "HUD_MOBILE_OPERATOR_TOKEN" "${TOKEN}"
upsert_env_key "${HUD_ENV_FILE}" "HUD_MOBILE_OPERATOR_SCOPES" "${SCOPES}"
upsert_env_key "${SECRETS_ENV_FILE}" "MOBILE_GATEWAY_URL" "${GATEWAY_URL}"
upsert_env_key "${SECRETS_ENV_FILE}" "HUD_MOBILE_OPERATOR_TOKEN" "${TOKEN}"
upsert_env_key "${SECRETS_ENV_FILE}" "HUD_MOBILE_OPERATOR_SCOPES" "${SCOPES}"
if [ -n "${CF_ACCESS_CLIENT_ID}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET}" ]; then
  upsert_env_key "${SECRETS_ENV_FILE}" "MOBILE_GATEWAY_CF_ACCESS_CLIENT_ID" "${CF_ACCESS_CLIENT_ID}"
  upsert_env_key "${SECRETS_ENV_FILE}" "MOBILE_GATEWAY_CF_ACCESS_CLIENT_SECRET" "${CF_ACCESS_CLIENT_SECRET}"
fi

CF_API_TOKEN_VAL="$(resolve_config_value CF_API_TOKEN)"
CF_ACCOUNT_ID_VAL="$(resolve_config_value CF_ACCOUNT_ID)"

b64() {
  printf '%s' "$1" | base64 | tr -d '\n'
}

PATCH_FILE="$(mktemp)"
cat > "${PATCH_FILE}" <<PATCH
{"data":{
  "HUD_MOBILE_OPERATOR_TOKEN":"$(b64 "${TOKEN}")",
  "HUD_MOBILE_OPERATOR_SCOPES":"$(b64 "${SCOPES}")"$( [ -n "${CF_API_TOKEN_VAL}" ] && printf ',\n  "CF_API_TOKEN":"%s",\n  "cf-api-token":"%s"' "$(b64 "${CF_API_TOKEN_VAL}")" "$(b64 "${CF_API_TOKEN_VAL}")" )$( [ -n "${CF_ACCOUNT_ID_VAL}" ] && printf ',\n  "CF_ACCOUNT_ID":"%s",\n  "cf-account-id":"%s"' "$(b64 "${CF_ACCOUNT_ID_VAL}")" "$(b64 "${CF_ACCOUNT_ID_VAL}")" )
}}
PATCH

kubectl -n loom-hub patch secret loom-secrets --type merge --patch-file "${PATCH_FILE}" >/dev/null
rm -f "${PATCH_FILE}"

echo "Patched secret loom-hub/loom-secrets with fresh mobile token/scopes"

kubectl -n loom-hub rollout restart deployment/mobile-hud >/dev/null
if ! kubectl -n loom-hub rollout status deployment/mobile-hud --timeout=180s >/dev/null; then
  echo "ERROR: deployment/mobile-hud did not become ready" >&2
  kubectl -n loom-hub get pods -l app=mobile-hud -o wide >&2 || true
  for pod in $(kubectl -n loom-hub get pods -l app=mobile-hud -o jsonpath='{.items[*].metadata.name}' 2>/dev/null || true); do
    echo "--- ${pod} loomed ---" >&2
    kubectl -n loom-hub logs "${pod}" -c loomed --tail=60 >&2 || true
    echo "--- ${pod} hud ---" >&2
    kubectl -n loom-hub logs "${pod}" -c hud --tail=60 >&2 || true
  done
  echo "Hint: ensure registry image registry.harbor.lan/mcp/loom-core:latest includes mobile HUD flags and /api/mobile/v1 support." >&2
  exit 1
fi

echo "mobile-hud restarted and rollout completed"

ready=0
for _ in $(seq 1 30); do
  mcp_status="$(curl_with_headers "${GATEWAY_URL%/}/health")"
  if [ "${mcp_status}" = "200" ]; then
    break
  fi
  sleep 1
done
if [ "${mcp_status}" = "200" ]; then
  echo "Gateway MCP surface is reachable (${GATEWAY_URL%/}/health)"
else
  echo "WARN: gateway MCP surface probe returned HTTP ${mcp_status}"
fi

for _ in $(seq 1 40); do
  ping_status="$(curl_with_headers "${GATEWAY_URL%/}/api/mobile/v1/ping" "${TOKEN}")"
  ping_body="$(cat /tmp/loom-mobile-gateway-body 2>/dev/null || true)"
  if [ "${ping_status}" = "200" ] && echo "${ping_body}" | rg -q '"ok"\s*:\s*true'; then
    ready=1
    break
  fi
  sleep 1
done

if [ "${ready}" -ne 1 ]; then
  echo "ERROR: mobile ping did not become ready at ${GATEWAY_URL%/}/api/mobile/v1/ping" >&2
  echo "Last HTTP status: ${ping_status:-unknown}" >&2
  if [ -n "${ping_body:-}" ]; then
    echo "Last response body: ${ping_body}" >&2
  fi
  exit 1
fi

if command -v pbcopy >/dev/null 2>&1; then
  printf '%s' "${TOKEN}" | pbcopy
  echo "Token copied to clipboard."
fi

echo
cat <<OUT
Ready for app login (Gateway mode):
  Gateway URL:  ${GATEWAY_URL}
  Bearer Token: ${TOKEN}

Saved locally:
  ${TOKEN_FILE}
  ${HUD_ENV_FILE}
  ${SECRETS_ENV_FILE}
OUT

if [ -n "${CF_ACCESS_CLIENT_ID}" ] && [ -n "${CF_ACCESS_CLIENT_SECRET}" ]; then
  cat <<OUT

Cloudflare Access headers (for app Gateway mode):
  CF-Access-Client-Id:     ${CF_ACCESS_CLIENT_ID}
  CF-Access-Client-Secret: [configured in local env files]
OUT
fi
