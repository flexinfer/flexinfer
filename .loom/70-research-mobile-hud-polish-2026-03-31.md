# Research: Mobile App + HUD Polish (2026-03-31)

## Question

What is the highest-leverage UX and functionality polish plan for the Loom iOS companion app and the HUD, given the current codebase, roadmap, and runtime/tooling constraints?

## Findings

### F1: The roadmap already treats HUD polish as shipped baseline and points the next value to orchestration, catalog, and unified visibility

- The core HUD overhaul is marked complete, including shared primitives, grouped navigation, accessibility, and panel migrations. [S1]
- The active roadmap gaps now emphasize mobile scope discipline, fleet orchestration UX, catalog/discovery, and the "Unify Visibility" simplification epic. [S2] [S3] [S4] [S5]

Implication:
- The next polish round should improve clarity and operator flow around those open areas, not restart a generic visual refresh.

### F2: The mobile backend surface has expanded well beyond the original session-only v1 framing

- The mobile domain now registers a wide read surface plus mutation routes for sessions, push registration, sandbox, workflows, and agent spawn operations. [S6]
- The iOS client mirrors that breadth in `Endpoint`, including control-plane, pipelines, handoffs, sandbox, spawn, topology, graph, reasoning, and audit endpoints. [S7]
- The current mobile security doc still frames the v1 freeze around monitoring plus session lifecycle, with broader parity-wave endpoints remaining read-only and high-risk actions denied. [S8]

Implication:
- The product already has more capability than its original scope narrative, so the main planning problem is now information architecture and control staging, not missing backend endpoints.

### F3: The iOS app already covers the core operator loop, but its advanced surfaces are concentrated into a few oversized views

- `ContentView` provides an authenticated shell with `Dashboard`, `Agents`, `Ops`, `Alerts`, and `Connection` tabs on iPhone, plus a split-view variant on iPad. [S9]
- `DashboardView` already delivers health, fleet summary, active work, timeline, SSE-driven refresh, and live activity integration. [S10]
- `AgentsListView` plus `SessionDetailView` provide strong drill-down flows for session-centric operator work, including create-session entry, deep links, collapsible summaries, and session termination. [S11] [S12]
- `OpsView` is still a large multi-concern surface, and `OpsViewModel` loads tasks, workflows, presence, memory, stream, topology, graph, reasoning, control-plane, sandbox, and pipeline data in one place. [S13] [S14]
- `ConnectionDiagnosticsView` mixes connection health, remediation, push-token registration, policy snapshot, test connection, and disconnect into a single screen. [S15]

Implication:
- Mobile polish should focus on decomposing oversized operator surfaces and clarifying what belongs in quick triage versus advanced tooling.

### F4: The HUD has a solid shell and shared primitives, but key operator surfaces still centralize too much state and present too much information at once

- `App.svelte` still owns bootstrapping, global fetches, keyboard shortcuts, and top-level navigation behavior for the HUD. [S16]
- The router and shared shells give the HUD a strong grouping model across Agents, Servers, Tasks, Knowledge, Activity, and Sandbox. [S17] [S18] [S19]
- `OverviewPanel` still acts like a broad mini-dashboard that fetches KPIs plus OTel status and starts cost/RBAC/coordination polling while rendering many tiles at once. [S20]
- `DispatchPanel` and `CatalogPanel` exist and are roadmap-aligned, but they still read as basic table-first implementations rather than the primary workflow surfaces called for by the roadmap. [S21] [S22]

Implication:
- HUD polish should shift from "more panels" toward "better prioritization and actionability" in overview, dispatch, and catalog.

### F5: Shared coordination language already exists across backend, HUD, and mobile

- The roadmap notes recent agent contract convergence across HUD/CLI/bridge and explicit project/pipeline/workflow identity propagation into mobile task projections. [S23]
- Mobile dashboard contracts already include coordination summaries such as risky namespaces, shared branches, and conflict counts. [S24]
- The HUD coordination store already normalizes attention agents, risky namespaces, blockers, and relations from `/api/fleet`. [S25]

Implication:
- The next polish slice can unify terminology and affordances across mobile and HUD without inventing a new data model.

### F6: Contract and test coverage is strongest around the core mobile loop, not the newly broadened advanced surface

- Golden contract coverage currently guards the mobile envelope plus dashboard, agents, sessions, and tasks endpoint shapes. [S26]
- The repository already has a healthy Swift test suite for connection, SSE churn, dashboard, ops, sessions, and session detail view models. [S27]

Implication:
- If advanced mobile surfaces become first-class in the UX, they should gain comparable contract coverage before large UI polish depends on them.

### F7: Planning/runtime tooling is healthy, but frontend/mobile exploration is still mostly manual

- Loom-mode is active in this session, with `46` registered servers and `498` aggregated tools exposed through `loom://` resources. [S28]
- `codebase_memory` is healthy for Go (`7861` chunks) but still reports `0` TypeScript and `0` JavaScript chunks, so HUD/mobile frontend work is not semantically indexed. [S29]

Implication:
- The safest next step is a bounded polish plan around a small set of known files instead of a wide UI refactor.

## Synthesis

The codebase is no longer blocked on basic mobile or HUD capability. The real opportunity is to make both surfaces feel like one coherent operator product:

1. Mobile should privilege quick triage and safe high-confidence actions, while advanced controls move into clearly marked secondary flows.
2. HUD should foreground attention, blockers, and dispatch/catalog workflows instead of acting like a wall of equally weighted telemetry.
3. Shared contracts should define which cross-surface concepts are first-class: attention, blockers, coordination, approvals, pipeline state, and connection health.

## Assumptions

- This planning round should keep the existing mobile security/scope guardrails unless the product spec explicitly chooses to re-baseline them.
- "Polish" means improving information architecture, usability, and reliability of existing capabilities before adding major new ones.
- The next implementation slice should stay small enough to validate with the current direct-file-read workflow instead of requiring a full frontend indexing initiative first.

## Open Questions

- Should mobile workflow approvals remain an advanced/operator-lab surface, or become part of the first-class mobile control loop?
- Should the mobile shell keep the current five top-level tabs, or collapse advanced diagnostics/configuration into fewer entry points?
- Should the HUD overview continue to expose many subsystem summaries simultaneously, or pivot to an attention-first command surface with fewer but deeper modules?

## Sources

- [S1] `ROADMAP.md:36`
- [S2] `ROADMAP.md:167`
- [S3] `ROADMAP.md:219`
- [S4] `ROADMAP.md:226`
- [S5] `ROADMAP.md:244`
- [S6] `internal/hud/domain/mobile/mobile.go:22`
- [S7] `apps/loom-companion-ios/Sources/LoomCompanionKit/Networking/Endpoint.swift:4`
- [S8] `docs/MOBILE_COMPANION_SECURITY.md:118`
- [S9] `apps/loom-companion-ios/Sources/LoomCompanion/ContentView.swift:24`
- [S10] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Dashboard/DashboardView.swift:31`
- [S11] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Agents/AgentsListView.swift:30`
- [S12] `apps/loom-companion-ios/Sources/LoomCompanion/Views/SessionDetail/SessionDetailView.swift:28`
- [S13] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Ops/OpsView.swift:54`
- [S14] `apps/loom-companion-ios/Sources/LoomCompanionKit/ViewModels/OpsViewModel.swift:128`
- [S15] `apps/loom-companion-ios/Sources/LoomCompanion/Views/Connection/ConnectionDiagnosticsView.swift:21`
- [S16] `internal/hud/frontend/src/App.svelte:36`
- [S17] `internal/hud/frontend/src/lib/stores/router.svelte.ts:22`
- [S18] `internal/hud/frontend/src/lib/components/shared/ViewShell.svelte:23`
- [S19] `internal/hud/frontend/src/lib/components/shared/PanelShell.svelte:39`
- [S20] `internal/hud/frontend/src/lib/components/OverviewPanel.svelte:29`
- [S21] `internal/hud/frontend/src/lib/components/DispatchPanel.svelte:61`
- [S22] `internal/hud/frontend/src/lib/components/CatalogPanel.svelte:69`
- [S23] `ROADMAP.md:143`
- [S24] `internal/contracts/testdata/mobile_dashboard.golden:4`
- [S25] `internal/hud/frontend/src/lib/stores/coordination.svelte.ts:95`
- [S26] `docs/CONTRACT_TESTING.md:22`
- [S27] `apps/loom-companion-ios/Tests/LoomCompanionKitTests/ViewModels/OpsViewModelTests.swift`
- [S28] Tool calls: `list_mcp_resources`, `read_mcp_resource(server="loom", uri="loom://config")`, `read_mcp_resource(server="loom", uri="loom://tools/index")` (2026-03-31)
- [S29] Tool call: `codebase_memory__codebase_stats(repo_id="loom-core")` (2026-03-31)
