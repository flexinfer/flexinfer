# Enterprise Security Features

Loom Core provides four enterprise security features configurable in the daemon config file (`~/.config/loom/config.yaml`). All features are disabled by default and activate independently.

## Overview

| Feature | Config Key | Purpose |
|---------|-----------|---------|
| RBAC | `rbac` | Restrict tool access per agent role |
| Audit Trail | `audit` | Append-only JSONL log of all tool calls |
| Cost Tracking | `cost` | Usage attribution by agent, server, and tool |
| OAuth 2.1 | `oauth` | Standards-based authorization for remote access |

## RBAC (Role-Based Access Control)

RBAC restricts which tools each agent can invoke. The proxy evaluates access before forwarding tool calls.

### Configuration

```yaml
rbac:
  enabled: true
  default_policy: deny          # "allow" or "deny" when no role matches
  roles:
    developer:
      allow:
        - "git__*"              # All git tools
        - "github__*"           # All GitHub tools
        - "codebase_memory__*"  # Code search
      deny:
        - "k8s__k8s_apply"     # Block destructive K8s ops
    readonly:
      allow:
        - "*__list_*"          # Any list operation
        - "*__get_*"           # Any get operation
        - "*__search_*"        # Any search operation
  bindings:
    - agent_id: "claude-code"
      role: developer
    - agent_type: "codex"       # All Codex agents
      role: readonly
    - agent_id: "*"             # Wildcard fallback
      role: readonly
```

### Binding Resolution

Bindings resolve in priority order:

1. Exact `agent_id` match
2. `agent_type` match
3. Wildcard `agent_id: "*"`

### Access Decision

Tools are qualified as `server__tool` (e.g., `git__git_status`). The enforcer evaluates:

1. Resolve agent's role via bindings
2. Check deny patterns first (deny wins)
3. Check allow patterns
4. Fall back to `default_policy`

Pattern matching uses glob syntax (`path.Match`): `*` matches any sequence within a segment.

## Audit Trail

The audit logger writes a structured JSONL entry for every tool call through the proxy.

### Configuration

```yaml
audit:
  enabled: true
  log_path: /var/log/loom/audit.jsonl   # Default: ~/.config/loom/audit.jsonl
```

### Entry Format

Each line is a JSON object:

```json
{
  "timestamp": "2026-02-16T14:30:00Z",
  "agent_id": "claude-code",
  "agent_type": "claude-code",
  "server": "git",
  "tool": "git_status",
  "duration_ms": 45,
  "status": "success",
  "target": "local",
  "cached": false
}
```

| Field | Type | Description |
|-------|------|-------------|
| `timestamp` | string | ISO 8601 UTC timestamp |
| `agent_id` | string | Agent identifier |
| `agent_type` | string | Agent platform (claude-code, codex, gemini) |
| `server` | string | MCP server name |
| `tool` | string | Tool name |
| `duration_ms` | int | Call duration in milliseconds |
| `status` | string | `success`, `error`, or `denied` |
| `error` | string | Error message (if status is error) |
| `target` | string | `local` or `hub` |
| `cached` | bool | Whether response came from cache |

### SIEM Integration

The JSONL format integrates with standard log ingestion pipelines:

```bash
# Stream to Loki via promtail
tail -f ~/.config/loom/audit.jsonl | promtail --stdin

# Query with jq
cat ~/.config/loom/audit.jsonl | jq 'select(.status == "denied")'

# Count calls per agent
cat ~/.config/loom/audit.jsonl | jq -s 'group_by(.agent_id) | map({agent: .[0].agent_id, count: length})'
```

## Cost Tracking

The cost tracker aggregates tool call usage by agent, server, and tool. It tracks call counts, error rates, byte volumes, and durations.

### Configuration

```yaml
cost:
  enabled: true
```

### Usage Records

Each tool call produces a `UsageRecord` with:

| Field | Description |
|-------|-------------|
| `agent_id` | Calling agent |
| `server` | MCP server |
| `tool` | Tool name |
| `duration_ms` | Call duration |
| `request_bytes` | Request payload size |
| `response_bytes` | Response payload size |
| `status` | `success`, `error`, `denied`, or `cached` |

### Snapshot Endpoint

Query aggregated usage via the daemon API:

```bash
curl -s http://localhost:8088/loom/cost-stats | jq .
```

Response structure:

```json
{
  "timestamp": "2026-02-16T14:30:00Z",
  "by_agent": [
    {
      "agent_id": "claude-code",
      "call_count": 142,
      "error_count": 3,
      "total_duration_ms": 45200,
      "total_request_bytes": 28400,
      "total_response_bytes": 156000
    }
  ],
  "by_server": [
    {
      "server": "git",
      "call_count": 89,
      "error_count": 1
    }
  ],
  "totals": {
    "call_count": 284,
    "error_count": 5,
    "denied_count": 2,
    "cached_count": 41
  }
}
```

## OAuth 2.1 Authorization Server

The built-in OAuth 2.1 server enables standards-based authorization for remote MCP access. It implements PKCE (S256), dynamic client registration, and token revocation.

### Configuration

```yaml
oauth:
  enabled: true
  issuer: https://loom.example.com     # Default: derived from --http-addr
  token_ttl_minutes: 60                # Access token lifetime (default: 60)
  auth_code_ttl_seconds: 600           # Authorization code lifetime (default: 600)
  allow_dynamic_registration: true     # RFC 7591 dynamic registration (default: true)
```

### Endpoints

| Path | Method | Standard | Description |
|------|--------|----------|-------------|
| `/.well-known/oauth-authorization-server` | GET | RFC 8414 | Authorization server metadata |
| `/.well-known/oauth-protected-resource` | GET | RFC 9728 | Protected resource metadata |
| `/oauth2/register` | POST | RFC 7591 | Dynamic client registration |
| `/oauth2/authorize` | GET | RFC 6749 | Authorization endpoint (auto-approves) |
| `/oauth2/token` | POST | RFC 6749 | Token exchange (PKCE S256 required) |
| `/oauth2/revoke` | POST | RFC 7009 | Token revocation |

### PKCE Flow

```bash
# 1. Register a client
curl -X POST https://loom.example.com/oauth2/register \
  -H "Content-Type: application/json" \
  -d '{"redirect_uris": ["http://localhost:9999/callback"], "client_name": "my-agent"}'
# Returns: client_id, client_secret

# 2. Generate PKCE verifier and challenge
VERIFIER=$(openssl rand -base64 32 | tr -d '=+/' | head -c 43)
CHALLENGE=$(echo -n "$VERIFIER" | openssl dgst -sha256 -binary | openssl base64 | tr -d '=' | tr '+/' '-_')

# 3. Request authorization code
curl -G "https://loom.example.com/oauth2/authorize" \
  --data-urlencode "response_type=code" \
  --data-urlencode "client_id=$CLIENT_ID" \
  --data-urlencode "redirect_uri=http://localhost:9999/callback" \
  --data-urlencode "code_challenge=$CHALLENGE" \
  --data-urlencode "code_challenge_method=S256" \
  --data-urlencode "scope=mcp"
# Redirects to: http://localhost:9999/callback?code=AUTH_CODE

# 4. Exchange code for token
curl -X POST https://loom.example.com/oauth2/token \
  -d "grant_type=authorization_code" \
  -d "code=$AUTH_CODE" \
  -d "client_id=$CLIENT_ID" \
  -d "redirect_uri=http://localhost:9999/callback" \
  -d "code_verifier=$VERIFIER"
# Returns: access_token, token_type, expires_in

# 5. Use token for MCP calls
curl -X POST https://loom.example.com/mcp \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

### Token Revocation

```bash
curl -X POST https://loom.example.com/oauth2/revoke \
  -d "token=$ACCESS_TOKEN"
```

## Full Configuration Example

All enterprise features enabled:

```yaml
# ~/.config/loom/config.yaml
http:
  session_timeout_minutes: 30
  tls_cert_file: /etc/loom/cert.pem
  tls_key_file: /etc/loom/key.pem

rbac:
  enabled: true
  default_policy: deny
  roles:
    full:
      allow: ["*"]
    readonly:
      allow: ["*__list_*", "*__get_*", "*__search_*"]
    developer:
      allow: ["git__*", "github__*", "codebase_memory__*"]
      deny: ["k8s__k8s_apply", "docker__docker_exec"]
  bindings:
    - agent_id: "admin-agent"
      role: full
    - agent_type: "claude-code"
      role: developer
    - agent_id: "*"
      role: readonly

audit:
  enabled: true
  log_path: /var/log/loom/audit.jsonl

cost:
  enabled: true

oauth:
  enabled: true
  token_ttl_minutes: 60
  allow_dynamic_registration: true
```
