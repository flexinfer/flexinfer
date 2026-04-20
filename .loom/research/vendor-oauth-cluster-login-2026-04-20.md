# Vendor OAuth cluster-login research (2026-04-20)

Scope: can we ship `loom auth cluster-login` that writes refresh-capable subscription tokens for `claude` and `codex` CLIs into a K8s Secret, bypassing the Mac Keychain / `~/.codex/auth.json`?

## Executive summary

- **Partially shippable, with a clear split per vendor.** For Claude, use the officially documented `claude setup-token` → `CLAUDE_CODE_OAUTH_TOKEN` path (1-year token, no refresh logic). For Codex, use the officially documented "copy `~/.codex/auth.json` to trusted private CI" path; Codex refreshes itself in-process.
- **Do not build our own OAuth authorize/PKCE flow.** Anthropic's Usage Policy update (Feb 2026) explicitly prohibits using Pro/Max OAuth tokens "in any other product, tool, or service — including the Agent SDK" ([Anthropic news 2026-02-20](https://www.anthropic.com/news/usage-policy-update), summarised in [The Register 2026-02-20](https://www.theregister.com/2026/02/20/anthropic_clarifies_ban_third_party_claude_access/)). A loom-operated PKCE replica would fall on the wrong side of that line even if the tokens are driven through the real `claude` binary.
- **Standalone `mcp-auth-refresher` is unnecessary for the shippable path.** Claude's `setup-token` tokens are valid ~1 year ([authentication docs](https://code.claude.com/docs/en/authentication)); Codex self-refreshes against `auth.openai.com/oauth/token` during normal operation ([Codex auth](https://developers.openai.com/codex/auth), [CI/CD auth](https://developers.openai.com/codex/auth/ci-cd-auth)). Both slot into the existing `cluster-agent-auth` secret already mounted at `internal/hud/spawn.go:1298-1381`.

## Per-vendor findings

### 1. Anthropic Claude Code

- **Public OAuth endpoints exist but are internal.** Reverse-engineered details (for reference, not implementation): client_id `9d1c250a-e61b-44d9-88ed-5944d1962f5e`, authorize `https://claude.ai/oauth/authorize`, token `https://console.anthropic.com/v1/oauth/token` (also seen as `https://platform.claude.com/v1/oauth/token`), PKCE S256, scopes `user:profile user:inference`, refresh-token rotation on every refresh ([akashmohan.com reverse-engineering post](https://akashmohan.com/writings/claude-code-oauth), [issue #47754](https://github.com/anthropics/claude-code/issues/47754)).
- **Building our own client against these endpoints is a TOS violation.** Anthropic's update states OAuth tokens obtained through Free/Pro/Max accounts may not be used in "any other product, tool, or service — including the Agent SDK" (article linked above). Running the real `claude` binary with the token is fine; building loom's own token minter that talks to those endpoints is not.
- **Officially supported headless path: `claude setup-token`.** Documented in [Authentication – Claude Code Docs](https://code.claude.com/docs/en/authentication): "For CI pipelines, scripts, or other environments where interactive browser login isn't available, generate a one-year OAuth token with `claude setup-token`… copy it and set it as the `CLAUDE_CODE_OAUTH_TOKEN` environment variable." Requires Pro/Max/Team/Enterprise; inference-only scope; does not support Remote Control sessions; bare mode ignores it (falls back to `ANTHROPIC_API_KEY`).
- **Known headless-refresh hazards if we try the raw refresh path.** [Issue #47754](https://github.com/anthropics/claude-code/issues/47754) documents Cloudflare WAF blocking refresh requests from Linux VPS hosts (HTTP 403 / 429), with no official workaround. `CLAUDE_CODE_OAUTH_TOKEN` sidesteps this entirely — no refresh needed for a year. [Issue #8938](https://github.com/anthropics/claude-code/issues/8938) notes `CLAUDE_CODE_OAUTH_TOKEN` historically still prompted for theme/auth choice; worth validating against the Claude Code version we pin in the agent image before we bet on it.
- **Subscription identity is per-user, not per-workspace.** Claude Console has multi-user orgs with Claude Code role API keys, but subscription OAuth still attributes inference to an individual ([Claude Console authentication](https://code.claude.com/docs/en/authentication#claude-console-authentication)). A "cluster identity" plan must either (a) use a dedicated Anthropic Pro/Max account for the cluster, or (b) use a Console Claude-Code-role API key via `ANTHROPIC_API_KEY`.

### 2. OpenAI Codex CLI

- **Officially supported headless path: copy `auth.json` to trusted CI.** [Codex CI/CD auth docs](https://developers.openai.com/codex/auth/ci-cd-auth) explicitly permit this for "enterprise and other trusted private automation", subject to three conditions quoted verbatim by the docs: runner is trusted private infra; the refreshed `auth.json` is persisted between runs; "only one machine or serialized job stream will use a given `auth.json` copy." Public/open-source and concurrent sharing are prohibited.
- **Self-refresh is automatic and documented.** Same page: "Codex already knows how to refresh a ChatGPT-managed session… if `last_refresh` is older than about 8 days, Codex refreshes the token bundle before the run continues." Refresh target is `https://auth.openai.com/oauth/token` ([Codex auth](https://developers.openai.com/codex/auth)).
- **Device code flow exists (beta).** `codex login --device-auth` is documented on [Codex auth](https://developers.openai.com/codex/auth); lets the user complete auth on a second machine. Reasonable UX for the `loom auth cluster-login` command: trigger device-auth, write resulting `auth.json` into the secret.
- **Refresh-token endpoint/client-id are not officially published.** `deepwiki` and OpenAI dev-community posts reference them, but OpenAI's own docs intentionally do not. Same TOS caveat as Claude: we should not build a parallel OAuth client; let the real `codex` binary refresh the file in-place.
- **Concurrency constraint is the real design constraint.** Our current plan shares one secret across many pods; the Codex docs forbid concurrent use of one `auth.json` ("only one machine or serialized job stream"). We either (a) serialize Codex-backed agent pods onto one replica, (b) cut per-pod `auth.json` copies with the same refresh token (not safe — rotation invalidates siblings), or (c) fall back to API-key auth for Codex at scale.

### 3. Refresh-only subset

- Feasible for Claude only, and only under our own TOS risk: drive the documented reverse-engineered refresh endpoint from a `mcp-auth-refresher` CronJob. Given the Anthropic Feb 2026 policy language this is **not recommended**.
- For Codex, a CronJob refresher adds nothing — the CLI self-refreshes as long as `auth.json` lands on the pod filesystem.

### 4. Blast radius / TOS

- Anthropic: Pro/Max subscription OAuth is per-human and cannot be "shared across non-interactive workloads" via third-party code ([Anthropic Usage Policy update](https://www.anthropic.com/news/usage-policy-update)). A dedicated Anthropic account purchased for the cluster is the cleanest path to avoid attribution drift; otherwise prefer Console API keys.
- OpenAI: ChatGPT Plus/Pro TOS permits personal multi-device use; the Codex CI/CD doc adds the explicit "one serialized job stream" rule for `auth.json` reuse. Sharing a subscription across a fleet of concurrent pods is not permitted — API keys (Commercial Terms) are the correct pattern for scale.

## Recommendation

**Defer full Slice 2b.2 (loom-operated OAuth authorize + in-cluster refresher). Ship a narrower Slice 2b.2a that uses vendor-sanctioned headless paths only.**

Concrete plan:

1. `loom auth cluster-login --agent claude`: shell out to `claude setup-token` on the user's Mac, capture the printed `sk-ant-oat01-…` token, write it into the existing `cluster-agent-auth` secret as `claude-oauth-token` (not `claude-oauth-json`). Update `internal/hud/spawn.go:1298-1381` to mount it as `CLAUDE_CODE_OAUTH_TOKEN` env instead of a JSON credentials file.
2. `loom auth cluster-login --agent codex`: drive `codex login --device-auth` (user completes in browser), then read the resulting `~/.codex/auth.json` and stuff it into `cluster-agent-auth` under `codex-auth-json`. Pod mounts it at `~/.codex/auth.json`; Codex refreshes in place. Enforce single-replica dispatch for Codex-backed weaver domains until we add per-pod token minting.
3. Explicitly drop `mcp-auth-refresher` from the plan for now. Claude token is good for a year; Codex self-refreshes. Revisit only if Anthropic changes `setup-token` TTL or adds an enterprise device-flow.
4. Update `.loom/86-research-session-spawning-weaver-integration-2026-04-19.md §5 item 1` to note Path A is blocked on TOS (not endpoint availability); Path B (API keys) remains the safe default; the narrow headless subscription-token path above is the middle ground.

### What would unblock a real `cluster-login` OAuth flow

- Anthropic ships an enterprise/admin-grade OAuth flow (device-code or workspace-scoped client) that explicitly permits cluster use. No public signal this is coming; [Claude Console](https://code.claude.com/docs/en/authentication#claude-console-authentication) is today's answer and it's API-key-based.
- OpenAI publishes an official client_id + refresh schema for Codex with a cluster-use license. Again, no public signal; docs currently point at API keys ([CI/CD auth](https://developers.openai.com/codex/auth/ci-cd-auth)).
- If either ships, the `mcp-auth-refresher` CronJob design already sketched in `.loom/86 §5 D2` can be resurrected against the new endpoints.

## Sources

- [Authentication – Claude Code Docs](https://code.claude.com/docs/en/authentication)
- [Anthropic Usage Policy update (2026-02)](https://www.anthropic.com/news/usage-policy-update)
- [The Register — Anthropic clarifies ban on third-party tool access (2026-02-20)](https://www.theregister.com/2026/02/20/anthropic_clarifies_ban_third_party_claude_access/)
- [Authentication – Codex / OpenAI Developers](https://developers.openai.com/codex/auth)
- [Maintain Codex account auth in CI/CD](https://developers.openai.com/codex/auth/ci-cd-auth)
- [Reverse Engineering Signin with Claude from Claude Code — akashmohan.com](https://akashmohan.com/writings/claude-code-oauth)
- [claude-code issue #47754 — Cloudflare WAF blocks refresh on headless Linux](https://github.com/anthropics/claude-code/issues/47754)
- [claude-code issue #8938 — CLAUDE_CODE_OAUTH_TOKEN insufficient for non-interactive auth](https://github.com/anthropics/claude-code/issues/8938)
- [codex issue #17265 — routed MCP OAuth tokens not auto-refreshed](https://github.com/openai/codex/issues/17265)
- Workspace code references: `internal/hud/spawn.go:1298-1381` (existing `cluster-agent-auth` mount), `.loom/86-research-session-spawning-weaver-integration-2026-04-19.md:205-224` (Path A/B design).
