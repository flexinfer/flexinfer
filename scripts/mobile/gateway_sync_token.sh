#!/usr/bin/env bash

set -euo pipefail

NAMESPACE="${MOBILE_GATEWAY_NAMESPACE:-loom-hub}"
SECRET_NAME="${MOBILE_GATEWAY_SECRET_NAME:-loom-secrets}"
TOKEN_KEY="${MOBILE_GATEWAY_TOKEN_KEY:-HUD_MOBILE_OPERATOR_TOKEN}"
SCOPES_KEY="${MOBILE_GATEWAY_SCOPES_KEY:-HUD_MOBILE_OPERATOR_SCOPES}"
SECRETS_ENV_FILE="${HOME}/.config/secrets/ai.env"
HUD_ENV_FILE="${HOME}/.config/loom/hud.env"
TOKEN_FILE="${HOME}/.config/loom/mobile-operator-token"

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

decode_secret_key() {
  local key="$1"
  local raw decoded

  raw="$(kubectl -n "${NAMESPACE}" get secret "${SECRET_NAME}" -o jsonpath="{.data.${key}}" 2>/dev/null || true)"
  if [ -z "${raw}" ]; then
    printf ''
    return 0
  fi

  if decoded="$(printf '%s' "${raw}" | base64 --decode 2>/dev/null)"; then
    printf '%s' "${decoded}"
    return 0
  fi
  if decoded="$(printf '%s' "${raw}" | base64 -D 2>/dev/null)"; then
    printf '%s' "${decoded}"
    return 0
  fi

  printf '%s' "${raw}"
}

require_cmd kubectl "install kubectl and configure cluster access"
require_cmd base64 "install coreutils/base64"

if ! kubectl -n "${NAMESPACE}" get secret "${SECRET_NAME}" >/dev/null 2>&1; then
  echo "ERROR: secret ${NAMESPACE}/${SECRET_NAME} not found" >&2
  exit 1
fi

live_token="$(decode_secret_key "${TOKEN_KEY}")"
if [ -z "${live_token}" ]; then
  echo "ERROR: ${TOKEN_KEY} is missing in secret ${NAMESPACE}/${SECRET_NAME}" >&2
  exit 1
fi

live_scopes="$(decode_secret_key "${SCOPES_KEY}")"

current_token="$(read_env_value "HUD_MOBILE_OPERATOR_TOKEN" "${SECRETS_ENV_FILE}")"
if [ "${current_token}" = "${live_token}" ]; then
  echo "Token already in sync with ${NAMESPACE}/${SECRET_NAME}"
else
  echo "Token drift detected; syncing local HUD_MOBILE_OPERATOR_TOKEN from ${NAMESPACE}/${SECRET_NAME}"
fi

upsert_env_key "${SECRETS_ENV_FILE}" "HUD_MOBILE_OPERATOR_TOKEN" "${live_token}"
upsert_env_key "${HUD_ENV_FILE}" "HUD_MOBILE_OPERATOR_TOKEN" "${live_token}"
if [ -n "${live_scopes}" ]; then
  upsert_env_key "${SECRETS_ENV_FILE}" "HUD_MOBILE_OPERATOR_SCOPES" "${live_scopes}"
  upsert_env_key "${HUD_ENV_FILE}" "HUD_MOBILE_OPERATOR_SCOPES" "${live_scopes}"
fi

mkdir -p "$(dirname "${TOKEN_FILE}")"
printf '%s\n' "${live_token}" > "${TOKEN_FILE}"
chmod 600 "${TOKEN_FILE}" || true

echo "Synced local mobile token file and env from ${NAMESPACE}/${SECRET_NAME}"
echo "  HUD_MOBILE_OPERATOR_TOKEN length: ${#live_token}"
if [ -n "${live_scopes}" ]; then
  echo "  HUD_MOBILE_OPERATOR_SCOPES: ${live_scopes}"
fi
