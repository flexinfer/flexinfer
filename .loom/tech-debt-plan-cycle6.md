# Technical Debt Remediation Plan — Cycle 6

## Summary

- Planning date: 2026-04-02
- Scope: CI feedback-loop debt, mobile testability debt, HUD/runtime hotspots, and deferred Cycle 5 structural debt
- Total items considered: 7

## Scoring Snapshot

- Inventory artifact: `.loom/tech-debt-inventory-cycle6.md`
- Scoring input: `.loom/tech-debt-inventory-cycle6.json`
- Ranking artifact: `.loom/tech-debt-priority-cycle6.md`
- Scoring model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%

## Evidence Summary

- TODO/FIXME debt is not the main problem right now; repo scan is effectively clean.
- CI instability is concrete:
  - `main` pipeline `#6043` failed `test:race` because `cmd/mcp-orchestra` still reached `fi-accel` native headers even after partial race-test exclusions.
  - mobile HUD branch pipeline `#6035` failed `guardrails:docs-cli`, showing docs-guardrail friction around generated frontend artifacts and contract-golden updates.
- Mobile test feedback is degraded:
  - `swift test --package-path apps/loom-companion-ios --filter ConnectionViewModelTests` still fails before targeted tests can run because the package path pulls in `UIKit` app code and stale test fixtures.
- Hotspot scan shows structural drag in actively changing files:
  - `.gitlab-ci.yml` changed `101` times in the last 90 days
  - `internal/hud/app.go` changed `70` times in the last 90 days
  - `pkg/sync/ops.go` and daemon/runtime surfaces remain active churn areas
- Largest repo-owned Go files still cluster in MCP entrypoints and central orchestration surfaces:
  - `cmd/mcp-terraform/main.go` `945`
  - `cmd/mcp-linear/main.go` `937`
  - `cmd/mcp-argocd/main.go` `904`
  - `cmd/mcp-neo4j/main.go` `902`
  - `cmd/loom/cmd_sync.go` `880`
  - `pkg/agentcontext/svc_sessions.go` `796`

## Wave 1 (Immediate)

- Goal: restore fast, trustworthy feedback loops for active mobile and orchestration work while carving down the hottest HUD bootstrap surface.
- Items:
  - `DEBT-062` Stabilize race CI for fi-accel-linked orchestration packages
  - `DEBT-063` Repair iOS SwiftPM mobile test harness
  - `DEBT-064` Split HUD bootstrap and monitor wiring in `internal/hud/app.go`
- Acceptance criteria:
  - `test:race` passes on CI without requiring native `fi-accel` headers
  - targeted mobile package tests run in local/CI package mode without pulling in app-only `UIKit` code
  - `internal/hud/app.go` is reduced to a small composition root with setup moved into focused helpers/files and no route behavior regressions
- Risks/mitigations:
  - race-test exclusions can hide real concurrency bugs if scoped too broadly; mitigate by isolating only `fi-accel`-bound packages behind build tags or explicit package selection tests
  - iOS package/test boundary changes can accidentally alter app-target composition; mitigate with `xcodebuild` plus `swift test`
  - HUD bootstrap split can break startup ordering; mitigate with smoke tests around monitor start, mobile API registration, and SSE wiring
- Not in this wave:
  - bulk CI YAML redesign
  - broad mobile feature changes
  - server scaffold migration batch

## Wave 2 (Near-Term)

- Goal: reduce merge friction in CI/docs flows and simplify high-value runtime surfaces that still bottleneck delivery.
- Items:
  - `DEBT-065` Reduce docs-guardrail false positives for generated/test artifacts
  - `DEBT-068` Decompose session lifecycle service in `pkg/agentcontext/svc_sessions.go`
  - `DEBT-066` Migrate largest remaining MCP servers onto `mcpscaffold`
- Acceptance criteria:
  - docs guardrail distinguishes generated assets / contract-golden churn from genuine user-facing doc drift, or emits a narrower explicit exception path
  - session lifecycle service is split by concern without changing agent-context API behavior
  - selected server migrations preserve existing flags, env handling, tracing, and health behavior while reducing repeated boilerplate
- Risks/mitigations:
  - over-loosening docs checks could lower documentation discipline; mitigate by preserving strict checks for README/ROADMAP/user-facing routes while narrowing generated-file triggers
  - agent-context session changes are coordination-sensitive; mitigate with persistence and summary-flow tests
  - batch scaffold migrations can create review fatigue; mitigate with 3-5 server slices per MR instead of a giant batch
- Not in this wave:
  - full agent-context simplification epic
  - remaining long-tail server migrations after the first selected batch

## Wave 3 (Strategic)

- Goal: simplify the CLI config surface before the next round of platform/profile generation work lands.
- Items:
  - `DEBT-067` Split `cmd/loom/cmd_sync.go` into generate, sync, pull, and backup slices
- Acceptance criteria:
  - command wiring is split into smaller files with no CLI flag or help-text regressions
  - sync/generate command ownership becomes obvious enough to support future platform-profile work without single-file merge conflicts
- Risks/mitigations:
  - Cobra registration drift can silently break commands; mitigate with CLI smoke tests and `loom` help snapshots
- Not in this wave:
  - generator package redesign
  - registry/schema refactors outside CLI command assembly

## Backlog Conversion

| Debt ID | Backlog ID | Owner | Target Sprint/Milestone | Status |
|---|---|---|---|---|
| DEBT-062 | TD-C6-01 | CI/platform + orchestra | Next sprint | Ready |
| DEBT-063 | TD-C6-02 | Mobile companion | Next sprint | Ready |
| DEBT-064 | TD-C6-03 | HUD/runtime | Next sprint | Ready |
| DEBT-065 | TD-C6-04 | CI/platform | Sprint +1 | Ready |
| DEBT-068 | TD-C6-05 | Agent-context | Sprint +1 | Ready |
| DEBT-066 | TD-C6-06 | MCP platform | Sprint +1 | Ready |
| DEBT-067 | TD-C6-07 | CLI/config generation | Sprint +2 | Ready |

## Backlog-Ready Slices

### TD-C6-01 — Stabilize race CI for fi-accel-linked orchestration packages

- Problem statement: the `test:race` lane on `main` is not reliable because `cmd/mcp-orchestra` still traverses a native `fi-accel` dependency path that is unavailable in CI.
- Acceptance criteria:
  - pipeline race lane no longer fails on missing `fi_accel.h`
  - orchestration packages that truly require native headers are either tagged, stubbed, or excluded intentionally and transparently
  - documentation/comments in the CI job explain the boundary so future packages do not regress it
- Verification strategy:
  - reproduce the failing package selection locally in a headerless environment if possible
  - run the repo race-test command locally
  - validate the next CI `test:race` job on branch and on `main`
- Rollback/safety notes:
  - avoid blanket skipping of large orchestration surfaces
  - prefer a narrow, reviewable package boundary over silent global exclusions

### TD-C6-02 — Repair iOS SwiftPM mobile test harness

- Problem statement: package-mode tests are currently blocked by app-target `UIKit` coupling and stale test fixtures, which prevents fast regression coverage for mobile dashboard work.
- Acceptance criteria:
  - `swift test --package-path apps/loom-companion-ios --filter ConnectionViewModelTests` runs successfully
  - `MockAPIClient` stays exhaustive against current `Endpoint`
  - `DashboardViewModelTests` are aligned with current actor isolation
- Verification strategy:
  - run targeted `swift test`
  - run `swift build --package-path apps/loom-companion-ios --target LoomCompanionKit`
  - run `xcodebuild` app build to confirm app-target behavior is preserved
- Rollback/safety notes:
  - keep app-only lifecycle code separated from package-testable kit code
  - avoid package manifest changes that force Xcode-only behavior

### TD-C6-03 — Split HUD bootstrap and monitor wiring

- Problem statement: `internal/hud/app.go` remains a high-churn composition hotspot, making mobile/HUD delivery riskier than it should be.
- Acceptance criteria:
  - `internal/hud/app.go` becomes a small composition root
  - monitor setup, transport setup, and standalone runtime glue move into focused files
  - no public HUD endpoints or startup flags change
- Verification strategy:
  - targeted Go tests for HUD packages
  - `pnpm --dir internal/hud/frontend build`
  - HUD smoke launch via existing local workflow
- Rollback/safety notes:
  - keep moves mostly mechanical first
  - do not mix behavior changes with the structural split

### TD-C6-04 — Reduce docs-guardrail false positives

- Problem statement: the current docs guardrail creates avoidable MR churn when generated assets, contract goldens, or test-only changes trigger the same path as real user-facing changes.
- Acceptance criteria:
  - guardrail distinguishes generated/test artifacts from documentation-significant product changes, or provides a smaller explicit override path
  - failure output clearly explains which changed file classes require docs
  - true user-facing changes still fail without doc updates
- Verification strategy:
  - local script runs against representative diffs
  - validate on a branch with generated/frontend/golden-only deltas
- Rollback/safety notes:
  - preserve strictness for docs-worthy API, CLI, and UX changes
  - prefer allowlist refinement over broad skip behavior

### TD-C6-05 — Decompose session lifecycle service

- Problem statement: `pkg/agentcontext/svc_sessions.go` combines start/resume/end/persist/reap/summary coordination in one file, slowing review and complicating the simplification epic.
- Acceptance criteria:
  - file is split by lifecycle concern
  - existing session IDs, persistence semantics, and summary triggers remain unchanged
  - service tests still cover lifecycle edge cases
- Verification strategy:
  - run agent-context package tests
  - validate session start/end flows through existing tool handlers
- Rollback/safety notes:
  - keep exported behavior stable
  - avoid interleaving schema or storage changes in the same slice

### TD-C6-06 — Continue MCP scaffold migration batch

- Problem statement: the largest remaining MCP server entrypoints still carry repeated lifecycle/logging/tracing boilerplate that the scaffold already solves elsewhere.
- Acceptance criteria:
  - first batch migrates 3-5 of the largest remaining servers
  - binaries preserve existing tool registration and env behavior
  - boilerplate drops materially in each migrated main
- Verification strategy:
  - build migrated binaries
  - run existing package tests or smoke checks per server
  - compare generated startup behavior with pre-migration commands
- Rollback/safety notes:
  - migrate in small batches to keep diffs reviewable
  - do not mix server feature work with scaffold migration

### TD-C6-07 — Split `cmd_sync.go`

- Problem statement: `cmd/loom/cmd_sync.go` still concentrates multiple command families in one large file, which raises review cost and future merge pressure on platform-config work.
- Acceptance criteria:
  - generate, sync, pull, and backup wiring live in separate files or submodules
  - CLI help output and flags remain stable
  - ownership boundaries are obvious to future contributors
- Verification strategy:
  - `go test ./cmd/loom/...`
  - smoke test `loom generate`, `loom sync`, `loom pull`, and `loom backup` help/flag paths
- Rollback/safety notes:
  - treat this as structural movement only
  - preserve command constructors and flag names

## Deferred / Not In Scope

- Full `.gitlab-ci.yml` architecture redesign beyond the specific race/docs pain points
- Remaining MCP scaffold migrations beyond the first selected batch
- Additional large-file cleanup outside the chosen cut line, including `internal/daemon/bulk_tools.go` and broader generator package decomposition
- New feature work hidden inside debt slices
