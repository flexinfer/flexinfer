// Package codexwatch tails Codex Desktop session JSONL files
// (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl) and republishes the
// observable activity as canonical loom events (session.start,
// tool.call.start, tool.call.end, session.end).
//
// The GUI Codex.app spawns its app-server with `--listen stdio://`, so
// the App Server JSON-RPC stream is not externally observable. Session
// files on disk are the only stable external surface today; if/when
// Codex Desktop exposes a Unix socket app-server, the source layer here
// can be swapped without changing the mapper or publisher.
//
// Slice 1a of the cross-agent GUI integration plan
// (.loom/23-product-spec-codex-session-tail-2026-05-16.md).
package codexwatch
