# Product Spec: Mobile App + HUD Polish (2026-03-31)

## Goal

Make the Loom mobile app and HUD feel like one coherent operator product by improving information architecture, attention management, and usability of existing high-value capabilities without reopening broad feature-surface expansion. [S1] [S2] [S3]

## Users

- Operators supervising multiple agents, sessions, workflows, and pipelines across desktop and mobile surfaces. [S4]
- Contributors who need to move quickly between triage, drill-down, and safe intervention flows without re-learning different mental models per surface. [S5] [S6]

## In Scope

- Mobile shell and workflow polish around the current `Dashboard`, `Agents`, `Ops`, `Alerts`, and `Connection` experience.
- HUD polish around overview prioritization plus roadmap-aligned dispatch and catalog flows.
- Shared terminology and hierarchy for attention, blockers, coordination, health, and session lifecycle across mobile and HUD.
- Contract and validation improvements for any mobile surfaces that become first-class during this polish round.

## Non-Goals

- Replacing the shipped HUD design system foundation.
- Introducing arbitrary tool execution from mobile.
- Promoting every advanced mobile endpoint to a first-class surface just because the backend already exposes it.
- Large architectural rewrites unrelated to operator flow or visibility.

## Product Requirements

### R1: Attention-First Operator Hierarchy

- Both mobile and HUD should foreground the same top-level questions:
  - what needs attention now,
  - what is blocked,
  - what can I safely act on from here,
  - where do I drill deeper next.
- Coordination concepts already present in backend contracts should be the shared vocabulary: risky namespaces, attention agents, blockers, conflicts, session state, and connection health. [S7] [S8]

### R2: Mobile Core vs Advanced Separation

- The mobile app must keep the current strong core loop easy to reach:
  - dashboard triage,
  - session/agent drill-down,
  - session create/end,
  - alerts and connection health. [S9] [S10] [S11]
- Advanced operator capabilities currently concentrated in `OpsView` and connection diagnostics must be regrouped into clearer, smaller surfaces instead of a single catch-all screen. [S12] [S13]
- Existing scope discipline remains the default: mobile should prefer monitoring plus safe session lifecycle actions unless a capability is intentionally elevated in the spec and validation plan. [S14]

### R3: HUD Overview As Command Surface

- `OverviewPanel` should stop behaving like an everything-at-once telemetry wall and instead become a prioritized landing zone for attention, blockers, status deltas, and next actions. [S15]
- Secondary system summaries should be reachable without competing visually with the primary operator queue.

### R4: Dispatch and Catalog Become First-Class

- Dispatch and catalog flows should be treated as flagship roadmap surfaces, not secondary tables. [S16] [S17]
- Dispatch must make attention reasons, blockers, and action affordances easy to parse and act on.
- Catalog must make server state, capability context, and enable/disable flows understandable without requiring registry-level expertise.

### R5: Shared Interaction Patterns

- Shared UI patterns should converge where the product semantics match:
  - searchable/filterable lists,
  - priority/status badges,
  - empty/loading/error states,
  - detail drill-down affordances,
  - confirmation language for safe mutations.
- Mobile and HUD do not need identical layouts, but they should feel like the same product family.

### R6: Contract Safety For First-Class Mobile Surfaces

- If this polish round makes additional mobile surfaces first-class, those surfaces must gain stable contract coverage comparable to the existing dashboard/agents/sessions/tasks golden set. [S18]
- UI polish should not outrun the API stability story for advanced/operator-facing routes.

## Acceptance Criteria

- A dated research/spec/implementation trio exists for the mobile + HUD polish track and is linked from `.loom/00-index.md`.
- The implementation plan names a bounded first slice for mobile and a bounded first slice for HUD.
- The plan explicitly distinguishes:
  - core first-class mobile flows,
  - advanced mobile/operator-lab flows,
  - first-class HUD roadmap surfaces.
- The validation plan includes both existing UI test harnesses and contract/golden checks where needed.

## Success Signals

- Operators can identify attention, blockers, and safe next actions faster on both mobile and HUD.
- The mobile app feels less like "dashboard plus giant ops dump" and more like a staged operator companion.
- The HUD overview, dispatch, and catalog surfaces better support the roadmap’s orchestration/discovery goals.
- Future implementation slices can improve mobile/HUD ergonomics without reopening scope confusion about what belongs where.

## Assumptions

- The next implementation slice should preserve current mobile security and scope guardrails unless the team explicitly chooses to expand them.
- Some advanced backend capability may remain available but intentionally secondary in the UX.

## Open Questions

- Whether mobile workflow approvals should be promoted into the first-class control loop.
- Whether the HUD overview should preserve a broad KPI strip or reduce to a smaller, more curated summary set.
- Whether advanced mobile diagnostics/push controls belong in the main app shell or behind an explicit advanced mode.

## Sources

- [S1] `.loom/70-research-mobile-hud-polish-2026-03-31.md`
- [S2] `ROADMAP.md:167`
- [S3] `ROADMAP.md:219`
- [S4] `ROADMAP.md:13`
- [S5] `apps/loom-companion-ios/Sources/LoomCompanion/ContentView.swift:108`
- [S6] `internal/hud/frontend/src/App.svelte:184`
- [S7] `internal/contracts/testdata/mobile_dashboard.golden:4`
- [S8] `internal/hud/frontend/src/lib/stores/coordination.svelte.ts:3`
- [S9] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Dashboard/DashboardView.swift:31`
- [S10] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Agents/AgentsListView.swift:30`
- [S11] `apps/loom-companion-ios/Sources/LoomCompanion/Views/SessionDetail/SessionDetailView.swift:28`
- [S12] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Ops/OpsView.swift:54`
- [S13] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Connection/ConnectionDiagnosticsView.swift:21`
- [S14] `docs/MOBILE_COMPANION_SECURITY.md:146`
- [S15] `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:212`
- [S16] `internal/hud/frontend/src/lib/components/DispatchPanel.svelte:61`
- [S17] `internal/hud/frontend/src/lib/components/CatalogPanel.svelte:69`
- [S18] `docs/CONTRACT_TESTING.md:24`
