#!/usr/bin/env bash
set -euo pipefail

BUILD_ENV_PATH="${BUILD_ENV_PATH:-$PWD/build.env}"

if [[ -f "$BUILD_ENV_PATH" ]]; then
	# shellcheck disable=SC1090
	source "$BUILD_ENV_PATH"
fi

if [[ -n "${KEYCHAIN_PATH:-}" ]]; then
	security delete-keychain "$KEYCHAIN_PATH" 2>/dev/null || true
fi

if [[ -n "${ORIGINAL_KEYCHAINS:-}" ]]; then
	security list-keychains -d user -s $ORIGINAL_KEYCHAINS || true
fi

rm -f "$BUILD_ENV_PATH"

echo "Signing keychain cleanup complete."
