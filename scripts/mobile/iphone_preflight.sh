#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
APP_DIR="${REPO_ROOT}/apps/loom-companion-ios"
APP_PROJECT="${APP_DIR}/LoomCompanion.xcodeproj"

failures=0

pass() {
	echo "PASS: $*"
}

warn() {
	echo "WARN: $*"
}

fail() {
	echo "FAIL: $*"
	failures=$((failures + 1))
}

require_cmd() {
	local cmd="$1"
	local install_hint="$2"
	if command -v "${cmd}" >/dev/null 2>&1; then
		pass "'${cmd}' is available"
	else
		fail "'${cmd}' is not installed (${install_hint})"
	fi
}

echo "== Loom Companion iPhone Preflight =="
echo "Repo: ${REPO_ROOT}"
echo

require_cmd xcodebuild "install Xcode from the App Store"
require_cmd xcrun "install Xcode command line tools"

if [ ! -d "${APP_DIR}" ]; then
	fail "Missing iOS app directory: ${APP_DIR}"
fi

if [ "${failures}" -eq 0 ]; then
	if [ -d "${APP_PROJECT}" ]; then
		pass "Found app project at ${APP_PROJECT}"
	else
		fail "Missing Xcode app project at ${APP_PROJECT}"
	fi
fi

if [ "${failures}" -eq 0 ]; then
	if xcodebuild -project "${APP_PROJECT}" -list 2>/dev/null | grep -q "LoomCompanion"; then
		pass "Xcode sees LoomCompanion scheme"
	else
		fail "Xcode cannot find LoomCompanion scheme in ${APP_PROJECT}"
	fi
fi

if [ "${failures}" -eq 0 ]; then
	destinations="$(xcodebuild -project "${APP_PROJECT}" -scheme LoomCompanion -showdestinations 2>&1 || true)"
	if printf "%s\n" "${destinations}" | grep -Eq "error:iOS .* is not installed"; then
		fail "iOS platform runtime is missing in Xcode (Xcode > Settings > Components > iOS)"
	elif printf "%s\n" "${destinations}" | grep -q "platform:iOS"; then
		pass "iOS destination support is available"
	else
		warn "Could not confirm iOS destinations; verify Xcode iOS components are installed"
	fi
fi

if [ "${failures}" -eq 0 ]; then
	build_settings="$(xcodebuild -project "${APP_PROJECT}" -scheme LoomCompanion -destination 'generic/platform=iOS Simulator' -showBuildSettings 2>/dev/null || true)"
	if printf "%s\n" "${build_settings}" | grep -q "PRODUCT_TYPE = com.apple.product-type.application" &&
		printf "%s\n" "${build_settings}" | grep -q "WRAPPER_EXTENSION = app" &&
		printf "%s\n" "${build_settings}" | grep -q "PRODUCT_BUNDLE_IDENTIFIER = "; then
		pass "LoomCompanion scheme is configured as an iOS app bundle with a bundle identifier"
	else
		fail "LoomCompanion scheme is not configured as an iOS app bundle (.app with PRODUCT_BUNDLE_IDENTIFIER)"
	fi
fi

devices="$(xcrun xctrace list devices 2>/dev/null || true)"
online_devices="$(
	printf "%s\n" "${devices}" | awk '
		/^== Devices ==$/ { in_online=1; next }
		/^== Devices Offline ==$/ { in_online=0; next }
		in_online { print }
	'
)"
if printf "%s\n" "${online_devices}" | grep -Eiq "iPhone"; then
	pass "Detected at least one online iPhone in Xcode device list"
else
	warn "No online iPhone detected. Connect your iPhone via USB, unlock it, and tap Trust."
fi

if [ -x "${REPO_ROOT}/bin/loom" ]; then
	pass "Found local loom binary at ${REPO_ROOT}/bin/loom"
elif command -v loom >/dev/null 2>&1; then
	pass "Found loom in PATH"
else
	warn "loom binary not found; run 'make loom' before launching HUD"
fi

echo
if [ "${failures}" -gt 0 ]; then
	echo "Preflight failed with ${failures} blocking issue(s)."
	exit 1
fi

echo "Preflight passed."
echo "Next steps:"
echo "  1) Start daemon: ./bin/loomd"
echo "  2) Export token: export HUD_MOBILE_OPERATOR_TOKEN=\"\$(openssl rand -hex 32)\""
echo "  3) Launch HUD: make mobile-hud"
echo "  4) Open app in Xcode: open apps/loom-companion-ios/LoomCompanion.xcodeproj"
