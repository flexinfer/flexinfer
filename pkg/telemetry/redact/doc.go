// Package redact provides privacy filtering for tool invocation telemetry
// before it is broadcast to the agent telemetry event bus.
//
// Three tiers control disclosure:
//
//   - TierPublic: spectator surfaces (HUD card, mobile companion).
//     Default behavior: drop args entirely; result becomes a size-only stub.
//     Per-tool policies relax this for safe fields (e.g. Read.file_path → basename).
//
//   - TierRedacted: authenticated single-user surfaces (HUD expanded view, CLI spectate).
//     Default behavior: keep values with secret patterns masked; truncate large strings.
//
//   - TierPrivate: in-process trust (persistence to agent_context, in-memory).
//     Default behavior: pass-through.
//
// Redact and Summary are pure functions: same inputs → same outputs, no side
// effects, no panic on nil. Both are safe for concurrent use.
package redact
