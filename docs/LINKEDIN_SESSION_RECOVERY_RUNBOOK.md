# LinkedIn Session Recovery Runbook

## Purpose

Recover LinkedIn MCP experimental messaging sessions when LinkedIn invalidates cookies or issues a challenge.

## Symptoms

- `linkedin_*messaging*` tools return auth challenge/forbidden errors
- `linkedin_session_health` returns `challenge` or `logged_out`
- Daemon logs show challenge-triggered recovery attempts

## Prerequisites

1. BrowserKit runtime on host:
- `pip install flexinfer-browser-kit playwright`
- `python3 -m playwright install chromium`

2. Secrets configured:
- `LINKEDIN_SESSION_COOKIE`
- `LINKEDIN_JSESSIONID`
- `LINKEDIN_LOGIN_USERNAME`
- `LINKEDIN_LOGIN_PASSWORD`

## Recovery Procedure

1. Check status:
- `loom tools call linkedin__linkedin_auth_status --args '{}' --json`

2. Force live health probe:
- `loom tools call linkedin__linkedin_session_health --args '{"refresh":true}' --json`

3. Attempt assisted recovery:
- `loom tools call linkedin__linkedin_session_recover --args '{"mode":"interactive"}' --json`

4. Re-check health:
- `loom tools call linkedin__linkedin_session_health --args '{"refresh":true}' --json`

5. Smoke test messaging read:
- `loom tools call linkedin__linkedin_list_conversations --args '{"start":0,"count":5}' --json`

## Expected Outcomes

- Recovery returns `state=healthy`
- Refreshed `li_at` and `JSESSIONID` persist into Loom secrets
- Messaging calls succeed without manual cookie paste

## Failure Modes

- BrowserKit dependencies missing: install prerequisites above
- Cooldown active: wait for `LINKEDIN_SESSION_RECOVERY_COOLDOWN_SECONDS`
- Checkpoint/CAPTCHA unresolved: complete manually in interactive browser and re-run recovery
- Persistent challenge: rotate session credentials and retry

## Safety Constraints

- Stealth/browser automation is best-effort and may still be challenged
- Avoid aggressive retry loops; this MCP performs one retry after successful recovery
- Keep `linkedin_session_recover` out of registry `always_allow`
