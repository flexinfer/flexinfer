# Mobile Companion: iPhone Test Runbook

Use this runbook to build and run Loom Companion on a physical iPhone and validate the mobile API end-to-end.

## Goal

- Launch HUD with mobile auth enabled.
- Run the iOS app from Xcode on your iPhone.
- Verify pairing, dashboard reads, and session actions.

## Prerequisites

- macOS with Xcode installed.
- iOS platform components installed in Xcode (`Xcode > Settings > Components`).
- iPhone connected to your Mac (USB recommended for first run), unlocked, and trusted.
- This repo checked out locally.
- For CI/distribution signing setup, see `docs/MOBILE_COMPANION_SIGNING_SETUP.md`.

## Quick Start (LAN Mode)

Single-command bootstrap (recommended):

```bash
make mobile-dev
```

This generates a fresh token, restarts HUD with mobile auth, opens the iOS app project in Xcode, and prints copy/paste-ready URL + token values.

Manual step-by-step:

1. Run preflight checks.

```bash
make mobile-iphone-preflight
```

2. Build `loom` (if not already built) and start daemon.

```bash
make loom
./bin/loomd
```

3. In a new terminal, set a mobile token and scopes.

```bash
export HUD_MOBILE_OPERATOR_TOKEN="$(openssl rand -hex 32)"
export HUD_MOBILE_OPERATOR_SCOPES="mobile:read,mobile:session:create,mobile:session:end,mobile:push"
```

4. Launch HUD for iPhone access.

```bash
make mobile-hud
```

This serves HUD on `http://0.0.0.0:3333` with mobile routes enabled.

5. Find your Mac LAN IP (use the interface your Mac is on).

```bash
ipconfig getifaddr en0 || ipconfig getifaddr en1
```

6. Open the iOS app project in Xcode.

```bash
make mobile-app-open
```

7. In Xcode:
- Select scheme `LoomCompanion`.
- Select your physical iPhone as the run destination.
- Set your Team under Signing if prompted.
- Tap Run.

For simulator-only quick runs from CLI:

```bash
make mobile-app-run-sim
```

8. In the app:
- Mode: `LAN`
- Server URL: `http://<your-mac-lan-ip>:3333`
- Bearer Token: value from `HUD_MOBILE_OPERATOR_TOKEN`
- Tap `Connect`.

## Validation Checklist

- Pairing succeeds (`/api/mobile/v1/ping`).
- Dashboard loads (`/api/mobile/v1/dashboard`).
- Sessions list loads (`/api/mobile/v1/sessions`).
- Create/end session works from mobile UI.
- Optional: push registration succeeds when `mobile:push` scope is enabled.

## Gateway Mode (Remote)

Use this when testing over the shared gateway host (`mcp.flexinfer.ai`) instead of LAN.

Quick bootstrap (recommended):

```bash
make mobile-gateway-dev
```

`make mobile-gateway-dev` now performs cluster bootstrap:
- rotates `HUD_MOBILE_OPERATOR_TOKEN`,
- patches `loom-hub/loom-secrets`,
- rollout-restarts `deployment/mobile-hud`,
- verifies `GET https://<gateway>/api/mobile/v1/ping`.

Recommended local defaults (`~/.config/secrets/ai.env`):

```bash
MOBILE_GATEWAY_URL=https://mcp.flexinfer.ai
# Optional when Cloudflare Access service tokens are required:
# MOBILE_GATEWAY_CF_ACCESS_CLIENT_ID=...
# MOBILE_GATEWAY_CF_ACCESS_CLIENT_SECRET=...
```

Preflight only:

```bash
make mobile-gateway-preflight
```

In app:
1. Choose `Gateway` mode.
2. Use the printed `Gateway URL` and `Bearer Token`.
3. If the gateway is Cloudflare Access-protected, fill:
   - `CF-Access-Client-Id`
   - `CF-Access-Client-Secret`

Notes:
- Gateway mode requires HTTPS; the app rejects non-HTTPS URLs.
- The ingress uses cert-manager TLS (`loom-gateway-tls`).
- Gateway path split routes:
  - `/ws`, `/hosts`, `/health`, `/ready` -> MCP gateway
  - `/api/mobile/v1/*` -> `mobile-hud` service

## Common Failures

- `unknown flag: --serve`: use `loom hud --bind ... --port ...` (there is no `--serve` flag).
- `iOS ... is not installed`: install iOS platform in Xcode Components.
- `missing bundleID for main bundle NSBundle ... Debug-iphonesimulator`: open/run `apps/loom-companion-ios/LoomCompanion.xcodeproj` (not `Package.swift`).
- Pairing fails in LAN mode: verify Local Network permission in iOS settings.
- `[unauthorized] invalid token`: ensure app token exactly matches HUD token.
- `[forbidden]`: missing scope in `HUD_MOBILE_OPERATOR_SCOPES`.
- `mobile-hud` rollout fails with `unknown flag: --bind` or missing `/api/mobile/v1`: the deployed `registry.harbor.lan/mcp/loom-core` image is too old; publish/update the image tag, then redeploy.
