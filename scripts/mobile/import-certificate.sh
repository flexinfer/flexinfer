#!/usr/bin/env bash
set -euo pipefail

echo "Preparing Apple code-signing keychain..."

if [[ -z "${APPLE_CERTIFICATE:-}" || -z "${APPLE_CERTIFICATE_PASSWORD:-}" ]]; then
	echo "ERROR: APPLE_CERTIFICATE and APPLE_CERTIFICATE_PASSWORD are required."
	echo "Set them as CI variables (File or Variable type) before calling this script."
	exit 1
fi

KEYCHAIN_PASSWORD="${KEYCHAIN_PASSWORD:-gitlab-runner}"
KEYCHAIN_PATH="${KEYCHAIN_PATH:-$PWD/app-signing.keychain-db}"
BUILD_ENV_PATH="${BUILD_ENV_PATH:-$PWD/build.env}"

# Preserve current keychain search list so cleanup can restore it.
ORIGINAL_KEYCHAINS="$(security list-keychains -d user | sed 's/\"//g' | tr '\n' ' ')"

if [[ -f "$KEYCHAIN_PATH" ]]; then
	echo "Removing stale keychain at $KEYCHAIN_PATH"
	security delete-keychain "$KEYCHAIN_PATH" 2>/dev/null || true
fi

security create-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
security set-keychain-settings "$KEYCHAIN_PATH"
security unlock-keychain -p "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"
security list-keychains -d user -s "$KEYCHAIN_PATH" $ORIGINAL_KEYCHAINS

if [[ -f "$APPLE_CERTIFICATE" ]]; then
	CERT_PATH="$APPLE_CERTIFICATE"
else
	echo "$APPLE_CERTIFICATE" | base64 --decode > certificate.p12
	CERT_PATH="certificate.p12"
fi

security import "$CERT_PATH" -k "$KEYCHAIN_PATH" -P "$APPLE_CERTIFICATE_PASSWORD" -T /usr/bin/codesign -T /usr/bin/security

# Import Apple intermediates (best effort).
curl -fsS -o AppleWWDRCAG3.cer "https://www.apple.com/certificateauthority/AppleWWDRCAG3.cer" || true
if [[ -f AppleWWDRCAG3.cer ]]; then
	security import AppleWWDRCAG3.cer -k "$KEYCHAIN_PATH" -T /usr/bin/codesign || true
fi

security set-key-partition-list -S apple-tool:,apple:,codesign:,productbuild: -s -k "$KEYCHAIN_PASSWORD" "$KEYCHAIN_PATH"

SIGNING_IDENTITY="$(
	security find-identity -v -p codesigning "$KEYCHAIN_PATH" |
		grep -E "Apple Distribution|iPhone Distribution|Apple Development|iPhone Developer|Developer ID Application" |
		head -1 |
		awk -F'"' '{print $2}'
)"

if [[ -z "$SIGNING_IDENTITY" ]]; then
	echo "ERROR: no Apple signing identity found in temporary keychain."
	security find-identity -v -p codesigning "$KEYCHAIN_PATH" || true
	exit 1
fi

PROFILE_UUID=""
PROFILE_NAME=""
if [[ -n "${APPLE_PROVISIONING_PROFILE:-}" ]]; then
	mkdir -p "$HOME/Library/MobileDevice/Provisioning Profiles"
	if [[ -f "$APPLE_PROVISIONING_PROFILE" ]]; then
		PROFILE_PATH="$APPLE_PROVISIONING_PROFILE"
	else
		echo "$APPLE_PROVISIONING_PROFILE" | base64 --decode > provisioning.mobileprovision
		PROFILE_PATH="provisioning.mobileprovision"
	fi

	PROFILE_PLIST="$(security cms -D -i "$PROFILE_PATH" 2>/dev/null || true)"
	if [[ -n "$PROFILE_PLIST" ]]; then
		PROFILE_UUID="$(echo "$PROFILE_PLIST" | /usr/libexec/PlistBuddy -c "Print UUID" /dev/stdin 2>/dev/null || true)"
		PROFILE_NAME="$(echo "$PROFILE_PLIST" | /usr/libexec/PlistBuddy -c "Print Name" /dev/stdin 2>/dev/null || true)"
	fi

	if [[ -z "$PROFILE_UUID" ]]; then
		PROFILE_UUID="$(basename "$PROFILE_PATH" | sed 's/\.mobileprovision$//')"
	fi
	cp "$PROFILE_PATH" "$HOME/Library/MobileDevice/Provisioning Profiles/${PROFILE_UUID}.mobileprovision"
fi

{
	echo "APPLE_SIGNING_IDENTITY='$SIGNING_IDENTITY'"
	echo "KEYCHAIN_PATH='$KEYCHAIN_PATH'"
	echo "ORIGINAL_KEYCHAINS='$ORIGINAL_KEYCHAINS'"
	echo "IOS_PROVISIONING_PROFILE_UUID='$PROFILE_UUID'"
	echo "IOS_PROVISIONING_PROFILE_NAME='$PROFILE_NAME'"
	echo "APPLE_TEAM_ID='${APPLE_TEAM_ID:-}'"
} >"$BUILD_ENV_PATH"

echo "Found signing identity: $SIGNING_IDENTITY"
if [[ -n "$PROFILE_UUID" ]]; then
	echo "Installed provisioning profile: $PROFILE_UUID${PROFILE_NAME:+ ($PROFILE_NAME)}"
fi
echo "Wrote signing environment: $BUILD_ENV_PATH"

rm -f certificate.p12 provisioning.mobileprovision AppleWWDRCAG3.cer
