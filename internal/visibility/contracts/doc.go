// Package contracts holds typed DTOs for Loom's visibility surfaces (HUD,
// CLI, mobile, SSE) shared between the daemon, HUD server, and downstream
// readers (loom VS Code extension, loom-zed, mobile companion).
//
// Subpackages group DTOs by visibility category:
//
//   - status:    PlatformStatus + composing daemon/agent/HUD snapshots
//     surfaced by `loom status` and the HUD landing surface.
//   - health:    Per-server health/divergence DTOs returned from loom/health.
//   - cost:      Cost/usage telemetry DTOs returned from loom/cost-stats.
//   - rbac:      Lightweight RBAC snapshot DTOs (full shape lifted later).
//   - catalog:   MCP catalog/server inventory DTOs (full shape lifted later).
//   - sessions:  Agent-session DTOs surfaced through agent_session_* tools.
//   - tasks:     Agent-task DTOs surfaced through agent_task_* tools.
//   - presence:  Agent presence/heartbeat DTOs.
//   - mobile/v1: Frozen mobile companion v1 wire shapes (see
//     docs/MOBILE_COMPANION_API.md). The byte format here is locked.
//
// # Stability
//
// Status: pre-1.0, additive-only within minor versions; promoted from
// internal/hud/bridge during EPIC 2 (#66). Existing bridge type names remain
// available as type aliases that re-export the canonical types here, so this
// package can absorb the visibility surface in stages without breaking the
// HUD, CLI, or mobile golden tests.
//
// Wire format guarantees:
//
//   - JSON tags on these DTOs are part of the public contract. Renaming a tag
//     is a breaking change.
//   - Mobile v1 shapes (mobile/v1) are frozen; see
//     docs/MOBILE_COMPANION_API.md and internal/contracts/testdata/mobile_*.golden
//     for the byte-identity proof.
//   - Adding fields with `omitempty` is the safe additive change pattern.
package contracts
