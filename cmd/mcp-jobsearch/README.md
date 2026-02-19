# mcp-jobsearch

Go-native MCP server for JobSearch backend API integration.

## Environment

- `JOBSEARCH_API_URL` (required)
- `JOBSEARCH_API_TOKEN` (required, fallback: `JOBSEARCH_BEARER_TOKEN`)
- `JOBSEARCH_CF_ACCESS_CLIENT_ID` (fallback: `CF_ACCESS_CLIENT_ID`)
- `JOBSEARCH_CF_ACCESS_CLIENT_SECRET` (fallback: `CF_ACCESS_CLIENT_SECRET`)
- `JOBSEARCH_TIMEOUT_SECONDS` (default: `30`)
- `JOBSEARCH_MAX_RESPONSE_BYTES` (default: `2097152`)

Cloudflare Access headers are automatically injected on all outbound API calls when both CF vars are set.

## Tooling Model

- Explicit Workflow + CRM tools for common operations.
- `jobsearch_api_call` for guarded full-route passthrough.
- Mutating passthrough calls require `confirm_write=true`.
- Destructive explicit operations require `confirm=true`.
