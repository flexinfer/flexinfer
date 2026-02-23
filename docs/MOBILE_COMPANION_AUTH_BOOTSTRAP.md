# Mobile Companion Auth Bootstrap

This document consolidates the auth bootstrap decision, flow descriptions, deployment mode comparison, threat model, and v1 implementation status for the Loom Companion mobile app.

Status: **MBL-1 delivered** (2026-02-23). Decision gate closed.

## Tracking

- [MBL-1: Auth bootstrap decision gate (M0)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/30)
- [MBL-2: Token lifecycle hardening (M1)](https://gitlab.flexinfer.ai/services/loom-core/-/issues/31)

## Decision Summary

**Date:** 2026-02-19

The v1 default bootstrap mode is **native OAuth authorization code + PKCE** using an external browser/system auth session. **Device-code pairing** is the fallback path, used only when direct browser-mediated auth is not practical for a given connection profile.

Key constraints:

- Flow selection must be explicit per profile/policy. Implicit or silent fallback is not allowed.
- UI/profile flows must display which bootstrap mode is active and why fallback is being used.
- Full OAuth 2.1 token lifecycle (PKCE + refresh rotation) is deferred to M2.

**Rationale:**

- RFC 8252 and OAuth 2.1 security guidance (RFC 9700) align on browser-mediated native auth with PKCE as the safest default.
- A constrained-input fallback is required for remote operator scenarios where a full browser auth flow is impractical.
- Explicit fallback selection reduces phishing and downgrade risk from accidental flow switching.

**Alternatives considered:**

- Device-code-first default for all profiles.
- Hybrid auto-failover without explicit profile selection.
- Deferring pairing fallback until post-v1.

Source: `.loom/40-decisions.md:3-30`

## Bootstrap Flows

### Default: OAuth Authorization Code + PKCE

```
┌─────────┐    ┌──────────────┐    ┌────────────┐    ┌───────────┐
│  Mobile  │───>│  System      │───>│  Auth      │───>│  Loom     │
│  App     │    │  Browser     │    │  Server    │    │  Backend  │
└─────────┘    └──────────────┘    └────────────┘    └───────────┘
     │                │                    │                │
     │  1. Generate PKCE verifier+challenge               │
     │  2. Open system browser ──────────>│                │
     │                │  3. User authenticates             │
     │                │  4. Auth code redirect ──>│        │
     │  5. Receive auth code via redirect │                │
     │  6. Exchange code + verifier ──────────────────────>│
     │  7. Receive access token + refresh token <──────────│
     │  8. API calls with bearer token ───────────────────>│
```

Properties:

- Uses external user-agent (system browser or ASWebAuthenticationSession on iOS).
- Mitigates embedded-webview credential capture.
- Short-lived access tokens plus rotating refresh tokens (M2).
- PKCE prevents authorization code interception.

### Fallback: Device-Code Pairing

```
┌─────────┐    ┌───────────┐    ┌──────────────┐
│  Mobile  │───>│  Loom     │    │  Operator    │
│  App     │    │  Backend  │    │  (Desktop)   │
└─────────┘    └───────────┘    └──────────────┘
     │                │                   │
     │  1. Request device code ──>│       │
     │  2. Receive user_code + device_code│
     │  3. Display user_code      │       │
     │  4. Poll for token ──────>│        │
     │                │    5. Operator enters code ──>│
     │                │    6. Operator approves       │
     │  7. Receive access token <─│       │
     │  8. API calls with bearer  │       │
```

Properties:

- Used only when browser-mediated auth is impractical (constrained remote operator workflows).
- Must be explicitly selected by profile/policy — never auto-selected.
- Requires hardening controls (see Fallback Hardening below).

## LAN vs Gateway Mode: Bootstrap Comparison

Both connectivity modes use the same auth bootstrap flows. The differences are in transport and trust posture.

| Aspect | LAN Mode | Gateway Mode |
|---|---|---|
| **Network** | Trusted local/private network | Remote/untrusted network |
| **Transport** | TLS strongly recommended; HTTP allowed for local debug | TLS mandatory (HTTPS only) |
| **Auth flow** | Same OAuth+PKCE default / device-code fallback | Same OAuth+PKCE default / device-code fallback |
| **Token validation** | Bearer token, constant-time comparison | Bearer token, constant-time comparison |
| **Endpoint permissions** | Same `mobile_operator` allowlist | Same `mobile_operator` allowlist |
| **Certificate validation** | Validates when HTTPS is used | Strictly enforced; self-signed rejected |
| **Trust assumption** | Network is private; auth still required | Zero-trust; no network-level trust |
| **Bootstrap UX** | Profile shows LAN mode + active auth flow | Profile shows Gateway mode + active auth flow |

Key points:

- Gateway mode must not assume LAN trust. All gateway connections enforce TLS and certificate validation.
- LAN mode still requires authenticated API access; network locality does not bypass auth.
- The iOS app enforces gateway HTTPS-only at the application layer (`ConnectionViewModel.pair()`).
- Mode selection is per connection profile; operators choose based on their use case.

Source: `docs/MOBILE_COMPANION_SECURITY.md:63-78`, `.loom/40-decisions.md:31-49`

## Threat Model (Auth-Specific)

Primary threats addressed by the bootstrap design:

| Threat | Control |
|---|---|
| Embedded-webview credential capture | External user-agent/system auth sessions required (no embedded webviews) |
| Authorization code interception | PKCE challenge/verifier binding |
| Credential replay | Short-lived access tokens + rotating refresh tokens (M2) |
| Pairing brute-force | Short code TTLs, attempt caps, rate limits |
| Phishing via device-code flow | Anti-phishing UX guidance, explicit endpoint confirmation |
| Silent auth downgrade | Explicit profile/policy selection; no implicit fallback |
| Stolen/compromised device token | Token revocation, short expiry, device-aware audit logging |
| MITM on untrusted networks | TLS required for gateway mode; strongly recommended for LAN |

Source: `docs/MOBILE_COMPANION_SECURITY.md:36-50`, `.loom/40-decisions.md:13-16`

## Fallback Hardening Requirements

When device-code pairing is used as the fallback bootstrap path:

1. **Short TTLs** — Device/user codes must expire quickly (minutes, not hours).
2. **Bounded retry windows** — Polling and code entry must have attempt caps.
3. **Brute-force controls** — Pairing endpoints must enforce per-actor rate limits and attempt caps.
4. **Anti-phishing UX** — Pairing flow must display explicit endpoint confirmation and guidance to help operators verify they are authorizing the correct device.

Source: `docs/MOBILE_COMPANION_SECURITY.md:94-98`

## v1 Implementation Status

The v1 iOS app uses a **static bearer token** (manual paste at pairing time) as a pragmatic MVP approach:

- Token is configured via `--mobile-operator-token` CLI flag or `HUD_MOBILE_OPERATOR_TOKEN` env var.
- Validation uses constant-time comparison (`crypto/subtle`).
- Token revocation is available at runtime via `POST /api/mobile/v1/admin/revoke`.
- Per-actor rate limiting is enforced on all endpoints.
- Audit logging captures actor, device ID, mode, and action for all mutations.

Full OAuth 2.1 (PKCE + refresh rotation) is deferred to **M2**, tracked by [MBL-2](https://gitlab.flexinfer.ai/services/loom-core/-/issues/31). The static token approach is acceptable for M0/M1 because:

- Token is manually provisioned (not embedded in app builds).
- Revocation is immediate and runtime (no restart required).
- All other security controls (scope checks, audit, rate limits, TLS) are active.

## Token Claims (Target for M2)

When full OAuth is implemented, tokens must include:

| Claim | Purpose |
|---|---|
| `sub` | Actor identity |
| `scope` or role claims | Endpoint authorization |
| `device_id` | Device/session binding |
| `exp` | Token expiry |
| `mode` or `aud` (optional) | Endpoint partitioning by connectivity mode |

Source: `docs/MOBILE_COMPANION_SECURITY.md:99-104`

## Acceptance Criteria Verification (MBL-1)

| Criterion | Status | Evidence |
|---|---|---|
| Decision recorded for v1 default mode | Met | `.loom/40-decisions.md:3-30` |
| Decision includes threat model and operational fallback path | Met | `docs/MOBILE_COMPANION_SECURITY.md:36-51, 82-98` |
| API and UI contract references selected mode and fallback | Met | `docs/MOBILE_COMPANION_API.md:48-60`, `docs/MOBILE_COMPANION_SECURITY.md:82-87` |

Checklist items from issue #30:

- [x] Compare bootstrap options against LAN/gateway deployment modes — See [LAN vs Gateway comparison](#lan-vs-gateway-mode-bootstrap-comparison) above
- [x] Document decision + rationale — See [Decision Summary](#decision-summary) above
- [x] Update API and UX contract docs — `MOBILE_COMPANION_API.md:48-60`, `MOBILE_COMPANION_SECURITY.md:82-98`

## Cross-References

- [Mobile Companion Security Model](MOBILE_COMPANION_SECURITY.md) — Full threat model, authorization matrix, hardening checklist
- [Mobile Companion API v1](MOBILE_COMPANION_API.md) — API contract, endpoint specs, auth model
- [Mobile Credential Revocation Runbook](MOBILE_CREDENTIAL_REVOCATION_RUNBOOK.md) — Incident response for token compromise
- `.loom/40-decisions.md` — Decision record (auth bootstrap + connectivity modes)
- `.loom/20-product-spec.md:111-118` — R10: Pairing and authentication bootstrap requirement
- `.loom/30-implementation-plan.md:36-38` — M0 auth bootstrap task
- [RFC 8252: OAuth 2.0 for Native Apps](https://datatracker.ietf.org/doc/rfc8252/)
- [RFC 9700: OAuth 2.1](https://datatracker.ietf.org/doc/html/rfc9700)
- [RFC 8628: Device Authorization Grant](https://datatracker.ietf.org/doc/html/rfc8628)
