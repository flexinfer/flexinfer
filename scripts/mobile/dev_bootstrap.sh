#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
LOOM_BIN="${REPO_ROOT}/bin/loom"
APP_PROJECT="${REPO_ROOT}/apps/loom-companion-ios/LoomCompanion.xcodeproj"

HUD_BIND="${HUD_MOBILE_BIND:-0.0.0.0}"
HUD_PORT="${HUD_MOBILE_PORT:-3333}"
HUD_SCOPES="${HUD_MOBILE_OPERATOR_SCOPES:-mobile:read,mobile:session:create,mobile:session:end,mobile:push}"
TOKEN_FILE="${HOME}/.config/loom/mobile-operator-token"
HUD_LOG="${HOME}/.config/loom/logs/mobile-hud.log"

echo "== Loom Mobile Dev Bootstrap =="
echo "Repo: ${REPO_ROOT}"
echo

if [ ! -x "${LOOM_BIN}" ]; then
	echo "bin/loom not found; building with LOOM_BUILD_P=${LOOM_BUILD_P:-1} CGO_ENABLED=${CGO_ENABLED:-0}"
	(
		cd "${REPO_ROOT}"
		make loom LOOM_BUILD_P="${LOOM_BUILD_P:-1}" CGO_ENABLED="${CGO_ENABLED:-0}"
	)
fi

if [ ! -x "${LOOM_BIN}" ]; then
	echo "ERROR: loom binary is not available at ${LOOM_BIN}"
	exit 1
fi

mkdir -p "$(dirname "${TOKEN_FILE}")" "$(dirname "${HUD_LOG}")"

TOKEN="$(openssl rand -hex 32)"
printf "%s\n" "${TOKEN}" > "${TOKEN_FILE}"
chmod 600 "${TOKEN_FILE}"

echo "Generated mobile operator token and saved to ${TOKEN_FILE}"

HUD_PIDS="$(pgrep -f '(^|/| )loom hud( |$)' || true)"
if [ -n "${HUD_PIDS}" ]; then
	echo "Stopping existing loom hud processes (${HUD_PIDS//$'\n'/ })"
	# shellcheck disable=SC2086
	kill ${HUD_PIDS} >/dev/null 2>&1 || true
	sleep 1
fi

# Start HUD detached with the generated token.
nohup "${LOOM_BIN}" hud \
	--bind "${HUD_BIND}" \
	--port "${HUD_PORT}" \
	--mobile-operator-token "${TOKEN}" \
	--mobile-operator-scopes "${HUD_SCOPES}" \
	>"${HUD_LOG}" 2>&1 &
HUD_PID=$!

echo "Started HUD (pid ${HUD_PID})"

ready=0
for _ in $(seq 1 40); do
	if curl -fsS "http://127.0.0.1:${HUD_PORT}/api/health" >/dev/null 2>&1; then
		ready=1
		break
	fi
	sleep 0.25
done

if [ "${ready}" -eq 1 ]; then
	echo "HUD is reachable on :${HUD_PORT}"
else
	echo "WARN: HUD health endpoint did not become ready in time. Check ${HUD_LOG}"
fi

if [ -d "${APP_PROJECT}" ]; then
	open "${APP_PROJECT}"
else
	echo "WARN: iOS project not found at ${APP_PROJECT}"
fi

LAN_IP=""
for iface in en0 en1; do
	candidate="$(ipconfig getifaddr "${iface}" 2>/dev/null || true)"
	if [ -n "${candidate}" ]; then
		LAN_IP="${candidate}"
		break
	fi
done

if [ -z "${LAN_IP}" ]; then
	LAN_IP="127.0.0.1"
fi

SIM_URL="http://127.0.0.1:${HUD_PORT}"
LAN_URL="http://${LAN_IP}:${HUD_PORT}"

if command -v pbcopy >/dev/null 2>&1; then
	printf "%s" "${TOKEN}" | pbcopy
	echo "Token copied to clipboard."
fi

echo
echo "Ready for app login:"
echo "  Simulator URL: ${SIM_URL}"
echo "  iPhone URL:    ${LAN_URL}"
echo "  Bearer Token:  ${TOKEN}"
echo
echo "Optional shell export:"
echo "  export HUD_MOBILE_OPERATOR_TOKEN=\"${TOKEN}\""
echo "  export HUD_MOBILE_OPERATOR_SCOPES=\"${HUD_SCOPES}\""
echo
echo "HUD log: ${HUD_LOG}"
