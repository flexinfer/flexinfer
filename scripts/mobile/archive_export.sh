#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="${REPO_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)}"
PROJECT="${MOBILE_IOS_PROJECT:-$REPO_ROOT/apps/loom-companion-ios/LoomCompanion.xcodeproj}"
SCHEME="${MOBILE_IOS_SCHEME:-LoomCompanion}"
BUNDLE_ID="${MOBILE_IOS_BUNDLE_ID:-ai.flexinfer.loom.companion}"
CONFIGURATION="${MOBILE_IOS_CONFIGURATION:-Release}"

ARCHIVE_PATH="${MOBILE_IOS_ARCHIVE_PATH:-$REPO_ROOT/build/mobile/LoomCompanion.xcarchive}"
EXPORT_PATH="${MOBILE_IOS_EXPORT_PATH:-$REPO_ROOT/build/mobile/export/app-store}"
EXPORT_OPTIONS="${MOBILE_IOS_EXPORT_OPTIONS:-$REPO_ROOT/build/mobile/ExportOptions.app-store.plist}"

TEAM_ID="${APPLE_TEAM_ID:-}"
PROFILE_SPECIFIER="${IOS_PROVISIONING_PROFILE_NAME:-${IOS_PROVISIONING_PROFILE_UUID:-${IOS_PROVISIONING_PROFILE_SPECIFIER:-}}}"
SIGNING_IDENTITY="${APPLE_SIGNING_IDENTITY:-}"

if [[ -z "$TEAM_ID" ]]; then
	echo "ERROR: APPLE_TEAM_ID is required for iOS archive/export."
	exit 1
fi
if [[ -z "$PROFILE_SPECIFIER" ]]; then
	echo "ERROR: provisioning profile not found. Set APPLE_PROVISIONING_PROFILE and rerun import-certificate."
	exit 1
fi

if ! command -v xcodebuild >/dev/null 2>&1; then
	echo "ERROR: xcodebuild is required."
	exit 1
fi
if ! command -v xcodegen >/dev/null 2>&1; then
	echo "ERROR: xcodegen is required."
	exit 1
fi

mkdir -p "$(dirname "$ARCHIVE_PATH")" "$EXPORT_PATH"

(
	cd "$REPO_ROOT/apps/loom-companion-ios"
	xcodegen generate --use-cache >/tmp/loom-mobile-xcodegen.log 2>&1 || {
		echo "ERROR: xcodegen failed"
		tail -n 40 /tmp/loom-mobile-xcodegen.log || true
		exit 1
	}
)

rm -rf "$ARCHIVE_PATH" "$EXPORT_PATH"
mkdir -p "$EXPORT_PATH"

archive_cmd=(
	xcodebuild
	-project "$PROJECT"
	-scheme "$SCHEME"
	-configuration "$CONFIGURATION"
	-destination "generic/platform=iOS"
	-archivePath "$ARCHIVE_PATH"
	-allowProvisioningUpdates
	CODE_SIGN_STYLE=Manual
	DEVELOPMENT_TEAM="$TEAM_ID"
	PROVISIONING_PROFILE_SPECIFIER="$PROFILE_SPECIFIER"
	archive
)

if [[ -n "$SIGNING_IDENTITY" ]] && [[ "$SIGNING_IDENTITY" =~ (Apple\ Distribution|iPhone\ Distribution|Apple\ Development|iPhone\ Developer) ]]; then
	archive_cmd=( "${archive_cmd[@]:0:${#archive_cmd[@]}-1}" CODE_SIGN_IDENTITY="$SIGNING_IDENTITY" archive )
fi

"${archive_cmd[@]}"

cat >"$EXPORT_OPTIONS" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>method</key>
  <string>app-store</string>
  <key>signingStyle</key>
  <string>manual</string>
  <key>teamID</key>
  <string>${TEAM_ID}</string>
  <key>provisioningProfiles</key>
  <dict>
    <key>${BUNDLE_ID}</key>
    <string>${PROFILE_SPECIFIER}</string>
  </dict>
  <key>stripSwiftSymbols</key>
  <true/>
  <key>compileBitcode</key>
  <false/>
</dict>
</plist>
EOF

xcodebuild -exportArchive \
	-archivePath "$ARCHIVE_PATH" \
	-exportPath "$EXPORT_PATH" \
	-exportOptionsPlist "$EXPORT_OPTIONS"

echo "Archive created at: $ARCHIVE_PATH"
echo "Exported artifacts at: $EXPORT_PATH"
