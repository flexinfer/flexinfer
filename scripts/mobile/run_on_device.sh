#!/usr/bin/env bash
#
# Build, install, and launch the Loom Companion iOS app on a connected
# iPhone, with the Gateway bootstrap URL passed as a launch argument so the
# freshly installed build lands already paired to the cluster HUD.
#
# Replaces the previous inline `mobile-app-run-device` recipe in the
# Makefile. Reasons for the extraction:
#
#  1. The previous recipe used `$( [ -n ... ] && echo "--args '$URL'" )` to
#     conditionally append `--args 'URL'` to ios-deploy. Shell command
#     substitution preserves the literal single quotes in argv, so the iOS
#     app's launch-argument scanner saw `'loom://configure?...'` (with
#     quotes) and the `hasPrefix("loom://")` check failed. The dead
#     ios-deploy path is also gone here — the tool isn't installed on this
#     machine and the recipe was relying on a silent failure to fall through.
#
#  2. After Xcode reinstalls the app, `UserDefaults` (where the iOS app
#     stores baseURL + connection mode + CF tokens) is reset. The keychain
#     bearer token survives but is useless without the rest. Without a
#     reliable way to deliver the configure URL on first launch, the user
#     has to retype every secret manually after every dev push.
#
#  3. Pre-validate credentials against `/api/mobile/v1/ping` BEFORE the
#     install/launch so we don't reinstall the app with broken auth. If
#     pair() fails on the device after install, applyConfigureSpec has
#     already logout()'d the existing creds, leaving the app in a worse
#     state than before.
#
# Args (positional, all required):
#   $1 — MOBILE_IOS_PROJECT path
#   $2 — MOBILE_IOS_SCHEME
#   $3 — MOBILE_IOS_CONFIGURATION
#   $4 — MOBILE_IOS_DERIVED_DATA
#   $5 — MOBILE_IOS_APP_NAME
#   $6 — MOBILE_IOS_BUNDLE_ID
#
# Optional env:
#   APPLE_TEAM_ID                 — set DEVELOPMENT_TEAM for code signing
#   HUD_GATEWAY_URL               — defaults to https://hud.flexinfer.ai
#   HUD_MOBILE_OPERATOR_TOKEN     — bearer token (resolved via build_configure_url.sh)
#   CF_ACCESS_CLIENT_ID/SECRET    — Cloudflare Access service-token pair
#   MOBILE_SKIP_VALIDATION=1      — skip the pre-launch /ping curl

set -uo pipefail

PROJECT="$1"
SCHEME="$2"
CONFIGURATION="$3"
DERIVED_DATA="$4"
APP_NAME="$5"
BUNDLE_ID="$6"

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
HUD_URL="${HUD_GATEWAY_URL:-https://hud.flexinfer.ai}"
BOOTSTRAP_URL_FILE="${HOME}/.config/loom/last-mobile-bootstrap.url"

echo "== Loom Companion → iPhone =="

# ─────────────────────────────────────────────────────────────────────────
# 1. Find connected device
# ─────────────────────────────────────────────────────────────────────────
DEVICE_LINE="$(xcodebuild -project "$PROJECT" \
	-scheme "$SCHEME" \
	-showdestinations 2>/dev/null \
	| grep 'platform:iOS,' | grep -v 'Simulator' | head -1)"
if [ -z "$DEVICE_LINE" ]; then
	echo "ERROR: No connected iPhone found." >&2
	echo "Connect your iPhone via USB, unlock it, and ensure it is trusted." >&2
	exit 1
fi
DEVICE_ID="$(echo "$DEVICE_LINE" | sed -n 's/.*id:\([^,}]*\).*/\1/p' | tr -d ' ')"
DEVICE_NAME="$(echo "$DEVICE_LINE" | sed -n 's/.*name:\([^,}]*\).*/\1/p' | sed 's/^ *//;s/ *$//')"
echo "Device: $DEVICE_NAME ($DEVICE_ID)"

# ─────────────────────────────────────────────────────────────────────────
# 2. Resolve + validate bootstrap creds BEFORE rebuilding
# ─────────────────────────────────────────────────────────────────────────
CONFIGURE_URL="$("$REPO_ROOT/scripts/mobile/build_configure_url.sh" 2>/dev/null || true)"
if [ -n "$CONFIGURE_URL" ]; then
	# Sanitize for echo: keep mode + url, mask everything else
	SAFE_URL="$(printf '%s' "$CONFIGURE_URL" | sed -E 's/(bearer=)[^&]+/\1<token>/; s/(cf_secret=)[^&]+/\1<secret>/')"
	echo "Bootstrap URL resolved: $SAFE_URL"

	if [ "${MOBILE_SKIP_VALIDATION:-0}" != "1" ]; then
		# Parse + URL-decode the bearer/CF tokens out of CONFIGURE_URL for
		# the validation curl. Use a temp file so f-strings + quoting in
		# the python don't fight bash's heredoc rules.
		PARSE_PY="$(mktemp -t loom-parse-url-XXXXXX.py)"
		trap 'rm -f "$PARSE_PY"' EXIT
		cat > "$PARSE_PY" <<'PY'
import sys, urllib.parse, shlex
qs = urllib.parse.urlparse(sys.stdin.read()).query
p = urllib.parse.parse_qs(qs)
def emit(name, key):
    val = p.get(key, [""])[0]
    print(f"{name}={shlex.quote(val)}")
emit("TOKEN", "bearer")
emit("CF_ID_RAW", "cf_id")
emit("CF_SECRET_RAW", "cf_secret")
PY
		eval "$(printf '%s' "$CONFIGURE_URL" | python3 "$PARSE_PY")"
		rm -f "$PARSE_PY"
		trap - EXIT

		echo -n "Pre-validating creds against $HUD_URL/api/mobile/v1/ping ... "
		HEADER_ARGS=(-H "Authorization: Bearer $TOKEN")
		if [ -n "$CF_ID_RAW" ]; then
			HEADER_ARGS+=(-H "CF-Access-Client-Id: $CF_ID_RAW")
		fi
		if [ -n "$CF_SECRET_RAW" ]; then
			HEADER_ARGS+=(-H "CF-Access-Client-Secret: $CF_SECRET_RAW")
		fi
		HTTP="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 15 \
			"${HEADER_ARGS[@]}" \
			"$HUD_URL/api/mobile/v1/ping" 2>/dev/null || echo "000")"
		unset TOKEN CF_ID_RAW CF_SECRET_RAW HEADER_ARGS
		if [ "$HTTP" = "200" ]; then
			echo "OK"
		else
			echo "FAILED (HTTP $HTTP)"
			echo "ERROR: bootstrap URL credentials are not accepted by HUD." >&2
			echo "  Refusing to install — pair() would call logout() on the device" >&2
			echo "  and leave the app with no auth. Fix the secrets first:" >&2
			echo "    make mobile-gateway-sync-token   # rotate operator token" >&2
			echo "    make mobile-gateway-preflight    # verify gateway routes" >&2
			echo "  Or run with MOBILE_SKIP_VALIDATION=1 to install anyway." >&2
			exit 1
		fi
	fi

	# Persist the URL so the user can manually paste from clipboard if the
	# launch-arg path silently fails (the iOS deep-link channel via
	# `devicectl process launch` has been intermittently flaky in the past).
	mkdir -p "$(dirname "$BOOTSTRAP_URL_FILE")"
	printf '%s' "$CONFIGURE_URL" > "$BOOTSTRAP_URL_FILE"
	chmod 600 "$BOOTSTRAP_URL_FILE"
	echo "  (also saved to $BOOTSTRAP_URL_FILE for manual paste fallback)"
else
	echo "WARNING: No gateway credentials found — app will install without auth." >&2
	echo "  Set HUD_MOBILE_OPERATOR_TOKEN + CF_ACCESS_CLIENT_ID/SECRET, or" >&2
	echo "  ensure kubectl can read loom-hub/loom-secrets." >&2
fi

# ─────────────────────────────────────────────────────────────────────────
# 3. Build
# ─────────────────────────────────────────────────────────────────────────
echo "Building $SCHEME ($CONFIGURATION)..."
TEAM_FLAG=""
if [ -n "${APPLE_TEAM_ID:-}" ]; then
	TEAM_FLAG="DEVELOPMENT_TEAM=$APPLE_TEAM_ID"
fi

if ! xcodebuild -project "$PROJECT" \
	-scheme "$SCHEME" \
	-destination "id=$DEVICE_ID" \
	-configuration "$CONFIGURATION" \
	-derivedDataPath "$DERIVED_DATA" \
	-allowProvisioningUpdates \
	$TEAM_FLAG \
	build 2>&1 | tail -n 20; then
	echo "" >&2
	echo "ERROR: Build failed." >&2
	echo "Common fixes:" >&2
	echo "  - Set APPLE_TEAM_ID: make mobile-app-run-device APPLE_TEAM_ID=XXXXXXXXXX" >&2
	echo "  - Open Xcode and configure signing: make mobile-app-open" >&2
	exit 1
fi

APP_PATH="$DERIVED_DATA/Build/Products/$CONFIGURATION-iphoneos/$APP_NAME.app"
if [ ! -d "$APP_PATH" ]; then
	echo "ERROR: app bundle not found at $APP_PATH" >&2
	exit 1
fi

# ─────────────────────────────────────────────────────────────────────────
# 4. Install + launch (devicectl path; ios-deploy retired — not installed
#    on this workstation and the previous quoting wrapper was broken)
# ─────────────────────────────────────────────────────────────────────────
echo ""
echo "Installing on $DEVICE_NAME..."
if ! xcrun devicectl device install app --device "$DEVICE_ID" "$APP_PATH"; then
	echo "ERROR: install failed." >&2
	echo "Manual fallback: open Xcode (make mobile-app-open) and Cmd+R." >&2
	exit 1
fi

echo ""
echo "Launching $BUNDLE_ID on $DEVICE_NAME..."
if [ -n "$CONFIGURE_URL" ]; then
	# `xcrun devicectl device process launch --device ID BUNDLE_ID ARG…`
	# passes positional args after the bundle ID as command-line args to
	# the iOS process. The Companion app's
	# LoomCompanionApp.deepLinkFromLaunchArgs() scans
	# ProcessInfo.processInfo.arguments for the first arg starting with
	# `loom://` (or `--configure-url=loom://...`).
	xcrun devicectl device process launch \
		--device "$DEVICE_ID" \
		"$BUNDLE_ID" \
		"$CONFIGURE_URL"
else
	xcrun devicectl device process launch --device "$DEVICE_ID" "$BUNDLE_ID"
fi

LAUNCH_RC=$?
if [ $LAUNCH_RC -eq 0 ]; then
	echo ""
	echo "Launched $BUNDLE_ID on $DEVICE_NAME"
	if [ -n "$CONFIGURE_URL" ]; then
		echo "If the app still shows 'No server configured' after launch,"
		echo "open this on the iPhone (e.g., paste in Notes and tap):"
		echo "  $BOOTSTRAP_URL_FILE"
	fi
	exit 0
fi

echo "" >&2
echo "ERROR: launch failed (exit $LAUNCH_RC). App is installed but not running." >&2
echo "  Open the app from Springboard, or run:" >&2
echo "    xcrun devicectl device process launch --device $DEVICE_ID $BUNDLE_ID" >&2
exit $LAUNCH_RC
