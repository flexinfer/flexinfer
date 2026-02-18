# mcp-linkedin

LinkedIn personal account MCP server with hybrid transport hardening:
- Primary path: Voyager HTTP API calls (fast path)
- Fallback path: BrowserKit stealth session health + assisted recovery

## Modes

- `LINKEDIN_MODE=auto|official|experimental`
- `LINKEDIN_BROWSERKIT_MODE=auto|required|off`

`LINKEDIN_MODE` controls feature lane behavior.
`LINKEDIN_BROWSERKIT_MODE` controls session health/recovery behavior.

## Required Auth Inputs

- Official lane: `LINKEDIN_ACCESS_TOKEN`
- Experimental messaging lane: `LINKEDIN_SESSION_COOKIE` (`li_at`; fallback: `LINKEDIN_LI_AT`, `LI_AT`)
- Messaging sends: `LINKEDIN_JSESSIONID` (fallback: `JSESSIONID`)

## BrowserKit Session Hardening Config

- `LINKEDIN_BROWSERKIT_PYTHON` (fallback: `BROWSERKIT_PYTHON`, then `python3`)
- `LINKEDIN_BROWSERKIT_STORAGE_DIR` (default: `~/.config/loom/linkedin-browserkit`)
- `LINKEDIN_BROWSERKIT_SESSION_ID` (default: `primary`)
- `LINKEDIN_SESSION_HEALTH_TTL_SECONDS` (default: `1200`)
- `LINKEDIN_SESSION_RECOVERY_COOLDOWN_SECONDS` (default: `300`)

Recovery credentials (stored in Loom secrets):
- `LINKEDIN_LOGIN_USERNAME`
- `LINKEDIN_LOGIN_PASSWORD`

Compatibility fallbacks:
- `LINKEDIN_USERNAME` -> used if `LINKEDIN_LOGIN_USERNAME` is unset
- `LINKEDIN_PASSWORD` -> used if `LINKEDIN_LOGIN_PASSWORD` is unset

## Tools

- `linkedin_auth_status`
- `linkedin_session_health`
- `linkedin_session_recover`
- `linkedin_get_profile`
- `linkedin_list_conversations`
- `linkedin_get_conversation_messages`
- `linkedin_send_message`

Tool output behavior:
- `linkedin_get_profile`, `linkedin_list_conversations`, `linkedin_get_conversation_messages`, and `linkedin_send_message` return compact normalized JSON by default.
- Set `include_raw=true` on those tools to include full upstream Voyager payloads when needed.

## Recovery Behavior

- Messaging calls run on-demand health checks (TTL-gated)
- On auth challenge (`401/403` + challenge markers), server attempts one-time recovery
- Recovery updates in-memory session cookies and persists refreshed values to Loom secrets
- Retry happens once per request path after successful recovery

## Operational Notes

- BrowserKit stealth is best-effort, not guaranteed undetectable.
- No background keepalive loop is used (on-demand checks only).
- Mutating tools should remain approval-gated in registry policy.

See runbook: `/Users/cblevins/workspace/services/loom-core/docs/LINKEDIN_SESSION_RECOVERY_RUNBOOK.md`
