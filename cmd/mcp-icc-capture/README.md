# mcp-icc-capture

MCP server that owns note-capture tools destined for
`icc-project-workspaces/projects/<slug>/...`. Pasted Slack threads,
email extracts, meeting notes, etc.

## Status

Scaffold + two starter tools. **No ICC HTTP calls** and **no file
writes** in this slice. Both shipped tools are pure local helpers; the
caller is responsible for deciding what to do with the returned
markdown.

## Tools

| Name | Purpose |
|---|---|
| `icc_format_slack_paste` | Format a raw Slack thread paste into the canonical Markdown + YAML frontmatter shape. Returns `{markdown, suggested_filename, suggested_path}`. Pure function, no I/O. |
| `icc_lint_notes` | Walk `projects/<slug>/<source>/` directories and validate frontmatter + filename conventions. Reports findings; with `fix=true`, applies the safe-fix subset (default classification, missing source from folder, missing `captured_at` from mtime). |

## Environment

| Variable | Purpose | Required |
|---|---|---|
| `ICC_API_URL` | Base URL for the ICC backend. Reserved for future slices. | No (no current tool calls ICC) |
| `ICC_API_KEY_ID` / `ICC_API_KEY` | HMAC key id. Reserved. | No |
| `ICC_API_SECRET` / `ICC_API_KEY_SECRET` | HMAC shared secret. Reserved. | No |
| `MCP_LOG_FORMAT` | `json` for structured logs (set by k8s base patch). | No |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTel collector endpoint. Noop when unset. | No |

## Develop

```bash
# Build the binary
make mcp-icc-capture

# Run tests for just this server
go test ./cmd/mcp-icc-capture/...

# Run lint (the repo standard)
make ci-lint
```

## Frontmatter contract

Slack pastes render with this frontmatter, in this order (Slice A
documents the canonical spec):

```yaml
---
project: <project_slug>      # use "_inbox" when unattributed
source: slack
classification: possible_phi # default floor for note-capture
captured_at: <ISO 8601>
channel: <slack channel>
participants: ["alice", "bob"]
---
```

## Roadmap (later slices, separate sessions)

- Wire the stubbed HMAC HTTP client (`http_client.go`) to ICC so tools
  can resolve project slugs, create code refs, and post artifacts.
- Add file-write capability behind a `dry_run=true` default.
- Add `icc_format_email_paste`, `icc_format_meeting_notes`, etc.
- Promote `_inbox` notes once attribution is decided.
