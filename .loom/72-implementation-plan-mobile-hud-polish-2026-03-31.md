# Implementation Plan: Mobile App + HUD Polish (2026-03-31)

## Outcome

Land a bounded polish program that improves operator flow across the mobile app and HUD by clarifying hierarchy, decomposing oversized surfaces, and tightening shared interaction patterns before adding more capability. [S1] [S2]

## Phase 0: Baseline Guardrails

1. Keep this work on the current planning track and stage only `.loom/` artifacts until the first concrete UX slice is chosen.
2. Treat pre-existing uncommitted changes as baseline context; do not couple this polish track to unrelated roadmap/doc drift. [S3]
3. Use loom-mode inventory and direct file reads as the planning baseline, because frontend/mobile code is not semantically indexed in the current session. [S4] [S5]

## Phase 1: Decide The Shared Operator Hierarchy

Goal:
- Define which concepts are first-class across both surfaces and which remain secondary.

Actions:
1. Create a simple cross-surface hierarchy document for:
   - attention
   - blockers/conflicts
   - session state
   - health/connectivity
   - approvals/pipelines
2. Map each concept to:
   - mobile home surface
   - HUD home surface
   - drill-down/detail destination
3. Explicitly label advanced mobile capabilities as one of:
   - first-class now
   - advanced but supported
   - deferred/hidden

Why first:
- The backend and client surface have already outgrown the older "monitoring + sessions only" mental model, so IA decisions should happen before any UI reshaping. [S6] [S7] [S8]

## Phase 2: Mobile Shell + Flow Decomposition

### Slice A: Re-scope `OpsView`

Target files:
- `apps/loom-companion-ios/Sources/LoomCompanion/Views/Ops/OpsView.swift`
- `apps/loom-companion-ios/Sources/LoomCompanionKit/ViewModels/OpsViewModel.swift`
- Related detail views under `apps/loom-companion-ios/Sources/LoomCompanion/Views/Ops/`

Goals:
- Break the current multi-concern `OpsView` into smaller, staged sections.
- Replace one giant eager load with more intentional loading by section or by demand.
- Make the difference between "safe/high-value control" and "advanced/operator-lab tooling" visible in the IA.

Suggested decomposition:
1. Work and approvals
2. Pipelines and workflow state
3. Sandbox/spawn operations
4. Knowledge/context views

### Slice B: Simplify Connection And Settings

Target files:
- `apps/loom-companion-ios/Sources/LoomCompanion/Views/Connection/ConnectionDiagnosticsView.swift`
- `apps/loom-companion-ios/Sources/LoomCompanionKit/ViewModels/ConnectionViewModel.swift`
- `apps/loom-companion-ios/Sources/LoomCompanionKit/Services/ConnectionHealthMonitor.swift`

Goals:
- Keep profile, health, and remediation primary.
- Move push registration/policy visibility into a clearer advanced/settings section if it stays in-app.
- Preserve LAN/gateway diagnostic clarity while reducing cognitive load.

### Slice C: Strengthen Mobile Home Surfaces

Target files:
- `apps/loom-companion-ios/Sources/LoomCompanion/ContentView.swift`
- `apps/loom-companion-ios/Sources/LoomCompanion/Views/Dashboard/DashboardView.swift`
- `apps/loom-companion-ios/Sources/LoomCompanion/Views/Agents/AgentsListView.swift`
- `apps/loom-companion-ios/Sources/LoomCompanion/Views/SessionDetail/SessionDetailView.swift`

Goals:
- Keep dashboard, agents, and session detail as the shortest path for triage and safe action.
- Improve cross-tab navigation from alerts and deep links so the app feels like one continuous operator flow.
- Make "what do I do next?" more obvious from the dashboard and session detail surfaces.

## Phase 3: HUD Attention-First Follow-Through

### Slice A: Overview Refocus

Target files:
- `internal/hud/frontend/src/lib/components/OverviewPanel.svelte`
- `internal/hud/frontend/src/App.svelte`

Goals:
- Recast overview as an attention-first landing surface instead of a dense mini-dashboard of everything.
- Reduce equal visual weight across subsystems.
- Promote next actions and drill-down links for blockers, risky namespaces, degraded servers, and active workflows.

### Slice B: Dispatch Upgrade

Target files:
- `internal/hud/frontend/src/lib/components/DispatchPanel.svelte`
- `internal/hud/frontend/src/lib/stores/coordination.svelte.ts`
- Shared primitives where needed:
  - `internal/hud/frontend/src/lib/components/shared/FilterBar.svelte`
  - `internal/hud/frontend/src/lib/components/shared/PanelShell.svelte`
  - `internal/hud/frontend/src/lib/components/shared/ViewShell.svelte`

Goals:
- Turn dispatch into a primary orchestration surface.
- Add stronger filtering, clearer attention explanations, and richer relation/blocker drill-down.
- Prefer shared primitives over one-off panel behavior when possible.

### Slice C: Catalog Upgrade

Target files:
- `internal/hud/frontend/src/lib/components/CatalogPanel.svelte`
- Supporting catalog store/components as needed

Goals:
- Improve browse/enable/disable ergonomics.
- Make server description, category, running state, and action consequences easier to parse.
- Align catalog with the roadmap’s discovery/activation intent instead of a raw table dump.

## Phase 4: Contract + Validation Tightening

1. Decide which mobile advanced surfaces become first-class in this round.
2. For each promoted surface, add or expand contract coverage under `internal/contracts/`.
3. Keep existing network/reliability harnesses in play for mobile changes.

Validation commands:
- `swift test --package-path apps/loom-companion-ios`
- `pnpm --dir internal/hud/frontend build`
- `go test ./internal/hud/... -count=1`
- `go test ./internal/contracts/... -count=1`

Optional broader validation when contracts or shared HUD data shapes move:
- `go test ./internal/tui/... -count=1`
- `go test ./...`

## Recommended First Implementation Slice

Start with:
1. Mobile `OpsView` decomposition and connection/settings simplification.
2. HUD overview refocus.

Why:
- These are the two biggest "everything at once" surfaces in the current user experience. [S9] [S10]
- They improve clarity without requiring immediate backend expansion.
- They make later dispatch/catalog and advanced mobile-control polish easier because the shell hierarchy will already be cleaner.

## 2026-04-01 Continuation Addendum

Chosen continuation slice:
1. Enrich the mobile dashboard attention-lane contract with route, label, and severity metadata so quick-open guidance is explicit in the backend payload.
2. Surface those lanes in the iOS dashboard as a dedicated "Next Attention" card that routes operators straight to `People` or `Work`.
3. Refocus the HUD overview rail from generic priority links toward a first-pass "open this next" lane list, while keeping lower-friction quick links available underneath.

Why this slice:
- It uses the shared coordination vocabulary already present in the backend instead of inventing another product language. [S12] [S13]
- It improves both surfaces without reopening risky mobile mutation scope or requiring a broader backend expansion. [S14] [S15]
- It is small enough to validate with the current direct-file workflow and contract coverage footprint. [S16] [S17]

Validation notes for this continuation slice:
- `go test ./internal/contracts/... ./internal/hud/... -count=1` — pass.
- `pnpm --dir internal/hud/frontend build` — pass (with pre-existing Svelte accessibility warnings in unrelated shared components).
- `swift build --package-path apps/loom-companion-ios --target LoomCompanionKit` — pass.
- `swift test --package-path apps/loom-companion-ios` — blocked in this environment because the package-wide `LoomCompanion` executable target imports `UIKit` from `Sources/LoomCompanion/AppDelegate.swift`, which is not available to the macOS SwiftPM runner used here. This is an environment/build-target issue, not a failure in the new `LoomCompanionKit` model code.

## Risks

- Reorganizing mobile IA without a clear core/advanced boundary will create another generation of scope drift.
- Reworking HUD overview before aligning on the attention model can just reshuffle the noise.
- Advanced mobile routes may look tempting to polish, but contract coverage is still centered on the core loop. [S11]
- Because HUD/mobile frontend files are not semantically indexed in this session, large refactors will be slower and riskier than small, deliberate slices. [S5]

## Sources

- [S1] `.loom/71-product-spec-mobile-hud-polish-2026-03-31.md`
- [S2] `.loom/70-research-mobile-hud-polish-2026-03-31.md`
- [S3] `.loom/00-workspace-snapshot.md:9`
- [S4] `.loom/00-mcp-inventory.md`
- [S5] Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-31)
- [S6] `internal/hud/domain/mobile/mobile.go:22`
- [S7] `apps/loom-companion-ios/Sources/LoomCompanionKit/Networking/Endpoint.swift:4`
- [S8] `docs/MOBILE_COMPANION_SECURITY.md:118`
- [S9] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Ops/OpsView.swift:54`
- [S10] `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:212`
- [S11] `docs/CONTRACT_TESTING.md:24`
- [S12] `internal/hud/domain/mobile/helpers.go:323`
- [S13] `apps/loom-companion-ios/Sources/LoomCompanionKit/Models/DashboardData.swift:15`
- [S14] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Dashboard/DashboardView.swift:16`
- [S15] `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:203`
- [S16] `apps/loom-companion-ios/Tests/LoomCompanionKitTests/Models/DashboardDataTests.swift:8`
- [S17] `apps/loom-companion-ios/Sources/LoomCompanion/ContentView.swift:125`
