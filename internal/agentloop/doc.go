// Package agentloop is the reusable ReAct loop engine behind the F4
// "tool-loop-as-prefix" client.
//
// The F4-tool-loop-as-prefix kill-test (PASSED 2026-06-01, see
// .loom/ralph-f4-tool-loop-as-prefix-2026-06-01.md) proved that an
// append-only chat loop against the APC-enabled gemma4 canary re-renders
// each turn as a block-aligned prefix extension of the previous turn:
// per-turn prefill cost tracks only the new tail, prefix-cache hit rate
// stays >90%, and TTFT stays flat while the prompt grows several-fold.
//
// This package turns that proven layout into a real client. The cardinal
// rule — the one that makes the cache pay off — is that the conversation is
// MUTABILITY-ORDERED and APPEND-ONLY:
//
//	[ system + tool schemas ]   immutable, sent first, never rewritten
//	[ user → assistant → tool ] appended each round, never reordered
//
// Any reordering, timestamp injection, or rewrite of an earlier message
// busts vLLM's exact-token-prefix match and silently collapses the cache
// (the LangChain/Assistants-API pathology the brainstorm warns about). The
// Conversation type enforces append-only history so callers cannot violate
// the invariant by accident.
//
// The engine is deliberately transport-light: it speaks OpenAI-compatible
// /v1/chat/completions, pins prefix-consistent routing via the
// X-Flexinfer-Cache-Key header (session_id), and reads the proxy's
// per-turn instrumentation headers (X-Flexinfer-Upstream-Ms,
// -Cached-Tokens, -Prompt-Tokens, -Finish-Reason) so a session reports the
// same prefix-hit / TTFT-flatness signals the kill-test measured.
//
// cmd/agent-loop is the CLI front end. The same engine shape is intended to
// be mirrored by a loom-core MCP tool (the queued next slice), which is why
// nothing here depends on CLI flags or process globals.
package agentloop
