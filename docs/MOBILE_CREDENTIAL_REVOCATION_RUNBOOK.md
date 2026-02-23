# Mobile Credential Revocation Runbook

Procedures for revoking mobile operator credentials during security incidents.

## Purpose

Immediately invalidate a compromised mobile bearer token so that no further authenticated requests succeed. The revocation takes effect in-memory without restarting the HUD server.

## Prerequisites

- `HUD_ADMIN_TOKEN` (or `--admin-token` flag) configured on the running HUD instance.
- Network access to the HUD server's `/api/mobile/v1/admin/revoke` endpoint.
- The compromised mobile operator token value (or the ability to rotate it).

## Procedure

### Step 1: Revoke the Token

Send a POST request to the admin revoke endpoint. This adds the token's SHA-256 hash to the in-memory revocation list. All subsequent requests using this token receive HTTP 401 with error code `token_revoked`.

```bash
curl -X POST https://<hud-host>:<port>/api/mobile/v1/admin/revoke \
  -H "X-Admin-Token: <admin-token>" \
  -H "Content-Type: application/json" \
  -d '{"token": "<compromised-mobile-token>"}'
```

Expected response:

```json
{
  "ok": true,
  "data": { "revoked": true },
  "meta": { "request_id": "req_...", "timestamp": "..." }
}
```

### Step 2: Verify Revocation

Confirm the revoked token is rejected by sending an authenticated request with it:

```bash
curl -s -o /dev/null -w "%{http_code}" \
  https://<hud-host>:<port>/api/mobile/v1/ping \
  -H "Authorization: Bearer <compromised-mobile-token>"
```

Expected: HTTP `401`. The response body includes `"code": "token_revoked"`.

### Step 3: Rotate the Mobile Operator Token

The revocation list is in-memory and does not persist across HUD restarts. To ensure permanent invalidation, rotate the token:

1. Generate a new token value.
2. Update the HUD configuration:
   - Environment variable: `HUD_MOBILE_OPERATOR_TOKEN=<new-token>`
   - Or CLI flag: `--mobile-operator-token <new-token>`
3. Restart the HUD server. The old token is no longer valid because the server now expects the new one.

### Step 4: Re-pair Mobile Devices

After token rotation, all mobile devices lose access. For each authorized device:

1. Open the Loom Companion app.
2. Navigate to Connection settings.
3. Tap "Disconnect" to clear the stored token.
4. Re-pair with the new token.

### Step 5: Verify Audit Trail

Check HUD structured logs for the revocation event and any post-revocation access attempts:

```bash
# Find the revocation audit entry
grep '"action":"token_revoke"' <hud-log-file>

# Find rejected requests using the revoked token
grep '"code":"token_revoked"' <hud-log-file>
```

Audit entries for the revocation include:

| Field | Value |
|-------|-------|
| `source` | `mobile` |
| `action` | `token_revoke` |
| `endpoint` | `POST /api/mobile/v1/admin/revoke` |
| `outcome` | `success` |
| `remote_addr` | IP of the admin performing revocation |

### Step 6: Check for Unauthorized Activity

Review mobile mutation audit logs for the period the token may have been compromised:

```bash
# Session creates from mobile
grep '"action":"session_create"' <hud-log-file> | grep '"source":"mobile"'

# Session ends from mobile
grep '"action":"session_end"' <hud-log-file> | grep '"source":"mobile"'
```

Look for:
- Unexpected `remote_addr` values (unfamiliar IPs).
- Unexpected `device_id` values (unknown devices).
- High-frequency mutation bursts.
- Activity outside normal operating hours.

## Rollback

If a token was revoked in error:

1. The in-memory revocation list has no un-revoke API. Restart the HUD server to clear it.
2. If the token was also rotated, restore the original `HUD_MOBILE_OPERATOR_TOKEN` value and restart.

## Limitations

- **In-memory only**: The revocation list does not persist across HUD restarts. A restart clears all revocations. Token rotation (Step 3) provides permanent invalidation.
- **Single token model**: The current v1 implementation uses a single shared mobile operator token. Per-device token issuance is planned for M2 (OAuth 2.1 flow).
- **No push invalidation**: Revocation is server-side only. The mobile app discovers revocation on its next API call (HTTP 401) and does not receive a push notification.

## References

- `internal/hud/mobile_revoke.go` -- `MobileTokenRevocationList` implementation
- `internal/hud/api_mobile.go` -- Admin revoke endpoint handler, `requireMobileScope` auth flow
- `internal/hud/app.go` -- Revocation list initialization
- `cmd/loom/hud.go` -- CLI flags (`--mobile-operator-token`, `--admin-token`)
- `docs/MOBILE_COMPANION_SECURITY.md` -- Security model and threat analysis
