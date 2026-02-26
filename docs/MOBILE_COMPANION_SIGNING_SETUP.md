# Loom Companion iOS Signing Setup

This setup mirrors the signing/secrets pattern used in `../streamslate` so we can share operational muscle memory across repos.

## Required Secrets

### Certificate + Team

| Secret | Description | Notes |
|---|---|---|
| `APPLE_CERTIFICATE` | `.p12` cert (base64 string or CI File variable path) | Apple Development / Apple Distribution cert export |
| `APPLE_CERTIFICATE_PASSWORD` | Password for `.p12` | Required |
| `APPLE_TEAM_ID` | Apple Developer Team ID | Recommended for deterministic signing |

### Provisioning Profile (recommended for CI archives/exports)

| Secret | Description | Notes |
|---|---|---|
| `APPLE_PROVISIONING_PROFILE` | `.mobileprovision` (base64 or CI File variable path) | Installed to `~/Library/MobileDevice/Provisioning Profiles/` by helper script |

### TestFlight / App Store Connect (for upload lanes)

| Secret | Description |
|---|---|
| `APPLE_API_ISSUER` | App Store Connect API issuer ID |
| `APPLE_API_KEY` | App Store Connect API key ID |
| `APPLE_API_KEY_BASE64` | Base64 `.p8` API key contents |

## Helper Scripts

We now include signing helpers aligned with streamslate conventions:

- `scripts/mobile/import-certificate.sh`
- `scripts/mobile/cleanup-signing.sh`

`import-certificate.sh` will:

1. Create a temporary keychain.
2. Import `APPLE_CERTIFICATE`.
3. Detect and export signing identity to `build.env`.
4. Install provisioning profile when `APPLE_PROVISIONING_PROFILE` is present.

`cleanup-signing.sh` will:

1. Delete the temporary keychain.
2. Restore original user keychain search list.
3. Remove `build.env`.

## Local Validation

Run:

```bash
make mobile-signing-check
```

To test cert import/cleanup locally:

```bash
make mobile-signing-prepare
make mobile-signing-cleanup
```

To run archive + app-store export after signing prepare:

```bash
make mobile-app-archive-export
```

This command requires `APPLE_TEAM_ID` and a provisioning profile resolved by `import-certificate.sh` (`APPLE_PROVISIONING_PROFILE` secret).

## CI Usage Pattern

Typical job steps:

```bash
./scripts/mobile/import-certificate.sh
set -a && source build.env && set +a
# run xcodebuild archive/export/upload steps
./scripts/mobile/cleanup-signing.sh
```

## Source

- Pattern copied/adapted from:
  - `/Users/cblevins/workspace/services/streamslate/scripts/import-certificate.sh`
  - `/Users/cblevins/workspace/services/streamslate/docs/code-signing-setup.md`
