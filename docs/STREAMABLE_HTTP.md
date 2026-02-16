# Streamable HTTP Transport

MCP v1.0 introduced Streamable HTTP as the standard remote transport. This document covers setup, configuration, and usage of the Streamable HTTP listener in `loomd`.

## Architecture

```
Current (local):
  IDE -> stdio -> [loom proxy] -> unix socket -> [loomd] -> stdio -> [mcp-server]

With HTTP (remote):
  IDE -> stdio -> [loom proxy --remote] -> HTTPS POST -> [loomd /mcp]
                                                  ^
  Web client --------------------------------- HTTPS POST -> [loomd /mcp]
```

The Unix socket path remains untouched. Streamable HTTP is an additive parallel listener on a separate port.

## Quick Start

### Local Development (No Auth)

Start the daemon with HTTP on localhost:

```bash
loomd --http-addr localhost:8088 --registry /path/to/registry.yaml
```

Test with curl:

```bash
curl -X POST http://localhost:8088/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1.0"}}}'
```

Expected output:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {},
    "serverInfo": {"name": "loom", "version": "0.1.0"},
    "instructions": "Loom daemon - unified MCP hub management"
  }
}
```

The response includes an `Mcp-Session-Id` header for subsequent requests.

### Team/Shared Daemon (With Auth)

1. Generate a bearer token:

```bash
loom auth token-generate
# Token: loom_a1b2c3...
# Stored as: LOOM_HTTP_TOKEN (in macOS Keychain)
```

2. Configure the daemon (`~/.config/loom/config.yaml`):

```yaml
http:
  auth:
    type: token
    token_secret_key: LOOM_HTTP_TOKEN
  tls_cert_file: /path/to/cert.pem
  tls_key_file: /path/to/key.pem
```

3. Start the daemon:

```bash
loomd --http-addr :8088 --registry /path/to/registry.yaml
```

4. Connect from a developer machine:

```bash
loom proxy --remote https://shared-server:8088/mcp --remote-token loom_a1b2c3...
```

Or set the token via environment variable:

```bash
export LOOM_REMOTE_TOKEN=loom_a1b2c3...
loom proxy --remote https://shared-server:8088/mcp
```

## Configuration Reference

### Daemon Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--http-addr` | Address for Streamable HTTP listener | (disabled) |

### File Config (`~/.config/loom/config.yaml`)

```yaml
http:
  # Session management
  session_timeout_minutes: 30    # Idle session expiry
  max_sessions: 1000             # Max concurrent sessions

  # Origin restriction (DNS rebinding protection)
  allowed_origins:
    - "https://app.example.com"

  # TLS (required for non-localhost)
  tls_cert_file: /path/to/cert.pem
  tls_key_file: /path/to/key.pem

  # Authentication
  auth:
    type: token                  # token | oidc | mtls
    token_secret_key: LOOM_HTTP_TOKEN

    # For type: oidc
    oidc_issuer: https://auth.example.com
    oidc_client_id: loom-daemon

    # For type: mtls
    tls_client_ca: /path/to/ca.pem
    allowed_common_names:
      - "developer.example.com"
```

### Authentication Types

| Type | Description | Use Case |
|------|-------------|----------|
| `token` | Static bearer token | Small teams, dev environments |
| `oidc` | JWT via OIDC provider | Enterprise SSO integration |
| `mtls` | Mutual TLS with client certs | Zero-trust infrastructure |

When binding to localhost, auth is optional. When binding to a non-localhost address, auth is **required** and the daemon refuses to start without it.

## Security

- **Localhost binding**: Auth disabled by default. The daemon warns but allows unauthenticated access.
- **Non-localhost binding**: Auth required. The daemon refuses to start without `http.auth.type` configured.
- **Non-localhost without TLS**: The daemon logs a warning. Strongly recommend TLS for any non-localhost deployment.
- **Session management**: Sessions expire after configurable idle timeout. A background reaper cleans up expired sessions every minute.

## Protocol Details

The Streamable HTTP transport follows the [MCP Streamable HTTP specification](https://modelcontextprotocol.io/specification/2025-03-26/basic/transports#streamable-http):

- **POST /mcp**: Send JSON-RPC messages. Returns `application/json` for requests, `202 Accepted` for notifications.
- **GET /mcp**: Returns `405 Method Not Allowed` (server-initiated messages not yet implemented).
- **DELETE /mcp**: Terminates a session. Requires `Mcp-Session-Id` header.
- **Mcp-Session-Id**: Set by the server on initialize response. Required for all subsequent requests.

### Request Headers

| Header | Required | Description |
|--------|----------|-------------|
| `Content-Type` | Yes | Must be `application/json` |
| `Accept` | Recommended | Should include `application/json` |
| `Mcp-Session-Id` | After init | Session ID from initialize response |
| `Authorization` | If auth enabled | `Bearer <token>` |

## CLI Commands

### Token Management

```bash
loom auth token-generate              # Generate and store a new token
loom auth token-generate --key MY_KEY # Use custom secret key
loom auth token-show                  # Display current token
loom auth token-revoke                # Delete token
```

### Remote Proxy

```bash
loom proxy --remote https://host:8088/mcp --remote-token TOKEN
loom proxy --remote https://host:8088/mcp  # Uses LOOM_REMOTE_TOKEN env
```

## Endpoints

| Path | Auth | Description |
|------|------|-------------|
| `/mcp` | Required (if configured) | MCP Streamable HTTP endpoint |
| `/health` | No | Health check for load balancer probes |
