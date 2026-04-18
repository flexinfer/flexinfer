#!/usr/bin/env bash
#
# Emit a `loom://configure?...` URL that bootstraps the Loom Companion iOS
# app's Gateway-mode connection to the cluster HUD. Reads credentials from
# (in order) environment variables, a local env file, or the k8s secret.
# Prints the URL on stdout with no trailing newline; prints nothing (exit 0)
# when credentials aren't available so callers can conditionally skip
# configuration.
#
# Used by `make mobile-app-run-device` and `make mobile-app-run-sim` to pass
# the URL as a launch argument to the freshly installed build.

set -euo pipefail

NAMESPACE="${MOBILE_GATEWAY_NAMESPACE:-loom-hub}"
SECRET_NAME="${MOBILE_GATEWAY_SECRET_NAME:-loom-secrets}"

HUD_URL_DEFAULT="${HUD_GATEWAY_URL:-https://hud.flexinfer.ai}"
HUD_ENV_FILE="${HUD_ENV_FILE:-${HOME}/.config/loom/hud.env}"

# Read a KEY=value (optionally `export KEY=value`) from an env file.
read_env_value() {
  local key="$1"
  local file="$2"
  if [ ! -f "${file}" ]; then
    return 0
  fi
  local raw
  raw="$(sed -n -E "s/^[[:space:]]*(export[[:space:]]+)?${key}=(.*)$/\\2/p" "${file}" | head -n1)"
  # strip surrounding whitespace + optional quotes
  raw="${raw#"${raw%%[![:space:]]*}"}"
  raw="${raw%"${raw##*[![:space:]]}"}"
  if [[ "${raw}" =~ ^\".*\"$ ]]; then
    raw="${raw:1:${#raw}-2}"
  elif [[ "${raw}" =~ ^\'.*\'$ ]]; then
    raw="${raw:1:${#raw}-2}"
  fi
  printf '%s' "${raw}"
}

read_secret_value() {
  local key="$1"
  if ! command -v kubectl >/dev/null 2>&1; then
    return 0
  fi
  kubectl -n "${NAMESPACE}" get secret "${SECRET_NAME}" \
    -o jsonpath="{.data.${key}}" 2>/dev/null \
    | base64 -d 2>/dev/null \
    || true
}

# Resolution order: env var → env file → k8s secret.
resolve() {
  local key="$1"
  local fallback_env_key="${2:-${key}}"
  local value="${!key:-}"
  if [ -n "${value}" ]; then
    printf '%s' "${value}"
    return 0
  fi
  value="$(read_env_value "${fallback_env_key}" "${HUD_ENV_FILE}" || true)"
  if [ -n "${value}" ]; then
    printf '%s' "${value}"
    return 0
  fi
  read_secret_value "${key}"
}

# URL-encode a string (newline-safe).
urlencode() {
  local s="$1"
  python3 -c 'import sys, urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""), end="")' "${s}"
}

TOKEN="$(resolve HUD_MOBILE_OPERATOR_TOKEN)"
CF_ID="$(resolve CF_ACCESS_CLIENT_ID)"
CF_SECRET="$(resolve CF_ACCESS_CLIENT_SECRET)"

if [ -z "${TOKEN}" ]; then
  # No token → no configure URL. Caller should just skip.
  exit 0
fi

URL_ENC="$(urlencode "${HUD_URL_DEFAULT}")"
TOKEN_ENC="$(urlencode "${TOKEN}")"
QUERY="mode=gateway&url=${URL_ENC}&bearer=${TOKEN_ENC}"
if [ -n "${CF_ID}" ]; then
  QUERY="${QUERY}&cf_id=$(urlencode "${CF_ID}")"
fi
if [ -n "${CF_SECRET}" ]; then
  QUERY="${QUERY}&cf_secret=$(urlencode "${CF_SECRET}")"
fi

printf 'loom://configure?%s' "${QUERY}"
