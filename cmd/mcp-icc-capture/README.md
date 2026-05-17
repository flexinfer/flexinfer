# mcp-icc-capture

MCP server that owns note-capture tools destined for
`icc-project-workspaces/projects/<slug>/...`. Pasted Slack threads,
email extracts, meeting notes, etc.

## Status

Slice E-2 (Round 10): operator-driven archive / unarchive wrappers
ship alongside the existing capture, promote, and demote tools.
`icc_archive_raw` and `icc_unarchive_raw` wire up the two new
`/api/captures/{archive,unarchive}` endpoints from Slice E-1. Archive
is idempotent (the server returns `already_archived=true` for repeat
calls); unarchive is not (it always restores or refuses). No backend
changes — the endpoints already exist.

Slice D-1 (Round 9) shipped three more pure formatters plus their
convenience composers alongside the C-2 wire tools:
`icc_format_email_extract`, `icc_format_meeting_notes`, and
`icc_format_standup` mirror the `icc_format_slack_paste` shape;
`icc_capture_email`, `icc_capture_meeting`, and `icc_capture_standup`
compose each formatter with `icc_write_capture` the same way
`icc_capture_slack` does. No new HTTP plumbing — the ICC client and
`/api/captures` endpoint from C-2 already accept these sources.

The original pure-local helpers (format + lint) still work without ICC
reachable.

## Tools

| Name | Purpose |
|---|---|
| `icc_format_slack_paste` | Format a raw Slack thread paste into the canonical Markdown + YAML frontmatter shape. Returns `{markdown, suggested_filename, suggested_path}`. Pure function, no I/O. |
| `icc_format_email_extract` | Format raw email text (RFC 822, Gmail paste, or web-rendered) into structured Markdown for `projects/<slug>/email/`. Detects `From` / `Subject` / `Date` headers and "On X wrote:" reply boundaries. Returns markdown + suggested filename/path + detected fields + warnings. |
| `icc_format_meeting_notes` | Format meeting notes (Gemini auto-notes or freeform) into structured Markdown for `projects/<slug>/meetings/`. Filename pattern `YYYY-MM-DD-<participants>-<topic>.md`. |
| `icc_format_standup` | Format standup notes (personal prep or team standup) into structured Markdown. Standups have no dedicated source folder in `STRUCTURE.md`, so output lands under `projects/<slug>/research/`. Personal prep defaults to filename `YYYY-MM-DD-standup-prep.md`; team standups use `YYYY-MM-DD-<team>-standup.md`. |
| `icc_lint_notes` | Walk `projects/<slug>/<source>/` directories and validate frontmatter + filename conventions. Reports findings; with `fix=true`, applies the safe-fix subset (default classification, missing source from folder, missing `captured_at` from mtime). |
| `icc_write_capture` | Write a pre-formatted capture (markdown + frontmatter) via `POST /api/captures`. Atomic: file + code_ref + artifact land together, or nothing. Validates `source` and `mode` client-side before hitting the network. |
| `icc_promote_to_artifact` | Promote a raw-only code_ref to a full artifact (`POST /api/code/refs/promote`). Idempotent — re-promoting an already-promoted ref returns the existing artifact with `already_promoted=true` / `fresh=false`. |
| `icc_demote_artifact` | Soft-delete an artifact, optionally keep the underlying code_ref (`POST /api/artifacts/<id>/demote`). Requires a non-empty `reason`. |
| `icc_archive_raw` | Archive a raw-only code_ref's underlying file: moves it from `projects/<slug>/<source>/` to `notes/archive/YYYY/MM/` and updates the code_ref (`POST /api/captures/archive`). Idempotent — re-archiving returns `already_archived=true` / `fresh=false`. Operator-driven; requires a non-empty `reason`. |
| `icc_unarchive_raw` | Reverse of `icc_archive_raw`: move a file from `notes/archive/` back to a project source folder (`POST /api/captures/unarchive`). Destination must be inside the workspace allowlist and not already exist. |
| `icc_capture_slack` | One-shot convenience tool — composes `icc_format_slack_paste` + `icc_write_capture` so a Slack paste lands in one MCP call. |
| `icc_capture_email` | One-shot composer — `icc_format_email_extract` + `icc_write_capture`. Default mode `both`. |
| `icc_capture_meeting` | One-shot composer — `icc_format_meeting_notes` + `icc_write_capture`. Default mode `both`. Requires `participants`. |
| `icc_capture_standup` | One-shot composer — `icc_format_standup` + `icc_write_capture`. **Default mode `ingest`** (not `both`) because standups are ephemeral; the DB is their canonical surface and writing files by default would just add filesystem noise. |

## Environment

| Variable | Purpose | Required |
|---|---|---|
| `ICC_BASE_URL` | Base URL for the ICC backend (e.g. `https://icc.lan`). Required at tool-call time for any network-backed tool; pure-local tools still work without it. | For network tools |
| `ICC_API_URL` | Historical alias for `ICC_BASE_URL` (Slice B name); used as a fallback when `ICC_BASE_URL` is unset. | No |
| `ICC_TIMEOUT_SECONDS` | HTTP client timeout for ICC calls. Defaults to `30`. | No |
| `MCP_LOG_FORMAT` | `json` for structured logs (set by k8s base patch). | No |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTel collector endpoint. Noop when unset. | No |

## Auth model

The MCP server is a trusted-context caller. Every ICC request goes out
with:

```
Content-Type: application/json
X-Requested-With: integration-command-center
Origin: <ICC_BASE_URL>
```

That satisfies the current ICC auth contract. HMAC signing is a future
hardening slice (intentionally not implemented in C-2).

## Usage

### One-shot: capture a Slack paste

```jsonc
// Tool: icc_capture_slack
{
  "text":         "alice  10:14 AM\nHey team, anyone seen the cohere volume report?",
  "project_id":   "prj_abc",
  "project_slug": "vendor-x",
  "channel":      "pmt-integrity-tech",
  "mode":         "both"  // raw | ingest | both
}
```

Server returns the formatted markdown, the suggested filename, and the
captures-endpoint result (code_ref / artifact / path_written).

### Two-step: inspect or edit between format and write

```jsonc
// 1) Format only.
{
  "tool": "icc_format_slack_paste",
  "args": { "text": "...", "project_slug": "vendor-x", "channel": "general" }
}

// 2) Optionally edit the returned markdown, then write it.
{
  "tool": "icc_write_capture",
  "args": {
    "project_id":     "prj_abc",
    "source":         "slack",
    "markdown":       "<edited markdown including frontmatter>",
    "suggested_path": "/workspace/icc-project-workspaces/projects/vendor-x/slack/2026-05-17-...md",
    "mode":           "both"
  }
}
```

### One-shot: capture an email

```jsonc
// Tool: icc_capture_email
{
  "text":         "From: alice@example.com\nSubject: Weekly Audit\nDate: Thu, 14 May 2026 09:15:00 -0400\n\nbody",
  "project_id":   "prj_abc",
  "project_slug": "vendor-x"
  // mode defaults to "both"
}
```

The formatter detects `From` / `Subject` / `Date` headers (plus
`Sent:` for web-rendered pastes) within the first 20 lines, and splits
on "On <date> <person> wrote:" boundaries to render each reply as a
separate `### From <name> · <date>` block. Pass `subject` to override
the detected subject or `topic` to override the filename slug.

### One-shot: capture meeting notes

```jsonc
// Tool: icc_capture_meeting
{
  "text":         "# 1:1 — Cody & Nadia\n\nQuick recap...",
  "project_id":   "prj_abc",
  "project_slug": "vendor-x",
  "participants": ["Cody Blevins", "Nadia Patel"],
  "topic":        "1on1"
  // mode defaults to "both"
}
```

`participants` is required (unlike slack/email, where participants fall
out of the message headers). Filename pattern is
`YYYY-MM-DD-<participants>-<topic>.md` (e.g.
`2026-05-12-cody-nadia-1on1.md`).

### One-shot: capture standup notes

```jsonc
// Tool: icc_capture_standup
{
  "text":         "Yesterday: shipped slice C-2.\nToday: slice D-1.\nBlocked: nothing.",
  "project_id":   "prj_abc",
  "project_slug": "_inbox"
  // mode defaults to "ingest" (DB only, no file write)
}
```

For team standups, pass `team` and (optionally) `participants`. Files
go to `projects/<slug>/research/` since `STRUCTURE.md` does not define
a `standup/` source folder — standups are a kind of research artifact.

### Promote / demote

```jsonc
// Promote a raw-only code_ref into a full artifact.
{ "tool": "icc_promote_to_artifact", "args": { "code_ref_id": "cref_123" } }

// Reverse it.
{
  "tool": "icc_demote_artifact",
  "args": { "artifact_id": "art_456", "reason": "Promoted in error", "keep_code_ref": true }
}
```

### Archive / unarchive

```jsonc
// Archive a raw-only code_ref. Moves the file to
// /workspace/notes/archive/YYYY/MM/<filename>.md and updates the row.
// Idempotent — re-archiving returns already_archived=true unchanged.
{
  "tool": "icc_archive_raw",
  "args": { "code_ref_id": "cref_123", "reason": "Project closed" }
}

// Reverse it. Destination must be inside the workspace allowlist and
// not already exist.
{
  "tool": "icc_unarchive_raw",
  "args": {
    "code_ref_id":      "cref_123",
    "destination_path": "/workspace/icc-project-workspaces/projects/vendor-x/slack/2026-05-17-hey-team.md"
  }
}
```

### Capture lifecycle

```
raw → captured (icc_capture_<source>) → promoted (icc_promote_to_artifact)
                                     ↘ archived (icc_archive_raw)
                                     ↗ restored (icc_unarchive_raw)
                                     ↘ demoted (icc_demote_artifact)
```

## Develop

```bash
# Build the binary
make mcp-icc-capture

# Run tests for just this server (hermetic — uses httptest, no live ICC)
go test ./cmd/mcp-icc-capture/...

# Run lint (the repo standard)
golangci-lint run ./cmd/mcp-icc-capture/...
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

Email:

```yaml
---
project: <project_slug>
source: email
classification: possible_phi
captured_at: <ISO 8601>
subject: <detected or overridden Subject>
participants: ["alice@example.com", "bob@example.com"]
---
```

Meeting:

```yaml
---
project: <project_slug>
source: meeting
classification: possible_phi
captured_at: <ISO 8601>
participants: ["Cody Blevins", "Nadia Patel"]
---
```

Standup:

```yaml
---
project: <project_slug>      # "_inbox" for personal prep
source: standup
classification: possible_phi
captured_at: <ISO 8601>
participants: [...]          # optional; empty list when omitted
---
```

## Roadmap (later slices, separate sessions)

- Slice F: `enforce_archive` auto-sweeper that walks
  `list_archive_candidates` and calls `icc_archive_raw` per ref —
  operator-gated by an env flag (parallel to the existing
  `ICC_RETENTION_AUTO`). Until then, archive remains operator-driven.
- HMAC signing on the ICC client for production hardening.
- Promote `_inbox` notes once attribution is decided.
