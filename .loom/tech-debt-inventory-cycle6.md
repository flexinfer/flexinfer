# Technical Debt Inventory — Cycle 6

## Scope

- Product/Service: `services/loom-core`
- Time horizon: next 2-4 delivery cycles
- Owners: runtime/daemon, HUD/mobile, CI/platform, CLI/config generation

## Items

| ID | Component | Debt Statement | Evidence | Impact (1-5) | Risk Reduction (1-5) | Drag Reduction (1-5) | Effort (1-5) | Dependencies | Notes |
|---|---|---|---|---:|---:|---:|---:|---|---|
| DEBT-062 | `cmd/mcp-orchestra`, `pkg/agentcontext`, CI `test:race` | Stabilize race-test coverage when `fi-accel` native headers are unavailable in CI. | Pipeline `#6043` on `main` failed `test:race` (job `60080`) because `fi_accel.h` was missing while building `cmd/mcp-orchestra`; trace shows race-test exclusion is incomplete. | 4 | 5 | 4 | 2 | CI job definition, build tags or package filters, fi-accel dependency boundaries | Highest-leverage feedback-loop repair because it breaks `main`. |
| DEBT-063 | `apps/loom-companion-ios` SwiftPM tests | Repair iOS package-test harness so mobile regression tests can run without full Xcode app-target coupling. | Local `swift test --package-path apps/loom-companion-ios --filter ConnectionViewModelTests` fails on `UIKit` import in `Sources/LoomCompanion/AppDelegate.swift`, non-exhaustive `MockAPIClient`, and actor-isolation drift in `DashboardViewModelTests`. | 4 | 4 | 4 | 3 | Package manifest/test target boundaries, mock upkeep, actor annotations | Blocks fast regression checks for current mobile workstream. |
| DEBT-064 | `internal/hud/app.go` | Split HUD bootstrap/wiring monolith into smaller runtime, monitor, and server setup units. | `internal/hud/app.go` is `730` LOC and appears in the top churn set with `70` touches in the last 90 days; mobile/HUD work keeps routing through this entrypoint. | 4 | 3 | 4 | 3 | Preserve public HUD startup behavior; keep route wiring stable | High-churn architecture debt rather than simple size debt. |
| DEBT-065 | `scripts/ci/check_docs_guardrails.sh`, docs guardrail job | Reduce docs-guardrail false-positive friction for generated assets, contract goldens, and test-only code-facing changes. | Pipeline `#6035` failed `guardrails:docs-cli` (job `59975`) on mobile HUD work until a changelog/doc note was added; trace shows generated frontend dist and golden files trigger the same path as user-facing code changes. | 3 | 2 | 4 | 2 | CI scripts, docs policy owners, generated-file policy | Delivery drag is high even when correctness risk is modest. |
| DEBT-066 | `cmd/mcp-*` server mains | Continue MCP scaffold migration for the largest remaining server entrypoints to shrink repeated lifecycle boilerplate. | Current largest repo-owned Go files include `cmd/mcp-terraform/main.go` (`945`), `cmd/mcp-linear/main.go` (`937`), `cmd/mcp-argocd/main.go` (`904`), `cmd/mcp-neo4j/main.go` (`902`), `cmd/mcp-github/main.go` (`885`). Cycle 5 left the bulk of the migration unfinished. | 3 | 2 | 4 | 3 | `mcpscaffold` parity, per-server smoke checks | Mechanical but broad; still a meaningful drag reducer. |
| DEBT-067 | `cmd/loom/cmd_sync.go` | Split sync/generate CLI command assembly into narrower modules before more profile/config work lands on top. | `cmd/loom/cmd_sync.go` is `880` LOC and continues to aggregate generate, backup, pull, and sync command wiring in one file. Prior cycle explicitly deferred it to Cycle 6. | 3 | 2 | 3 | 3 | Cobra command registration, generator/sync package APIs | Medium-priority simplification that reduces future CLI merge conflicts. |
| DEBT-068 | `pkg/agentcontext/svc_sessions.go` | Decompose session lifecycle service into start/resume/end/list/reaper slices to align with agent-context simplification work. | `pkg/agentcontext/svc_sessions.go` is `796` LOC and owns session lifecycle, persistence, cleanup callbacks, and summary orchestration in one surface. It was deferred from Cycle 5 despite being central to agent coordination. | 3 | 3 | 3 | 3 | Agent-context API compatibility, persistence semantics, session summary flow | Good fit for the roadmap simplification epic, but not as urgent as CI/mobile debt. |

## Source Links

- Incident(s): none captured in this cycle; debt inventory anchored to CI/test and hotspot evidence
- CI failures/flakes:
  - `glab ci get -R services/loom-core -p 6043 -d -F json`
  - `glab ci trace 60080`
  - `glab ci get -R services/loom-core -p 6035 -d -F json`
  - `glab ci trace 59975`
- SLO/metrics regressions: no production regression selected in this cycle
- TODO/FIXME scans:
  - `rg -n "TODO|FIXME|HACK|XXX" --glob '!vendor/**' .`
- Hotspot scan:
  - `git log --since='90 days ago' --name-only --pretty=format: | rg -v '^$' | sort | uniq -c | sort -rn | head -40`
- Large-file scan:
  - `find . \( -path './.git' -o -path './.go' -o -path './.tmp' -o -path './vendor' -o -path './node_modules' -o -path './.worktrees' \) -prune -o -name '*.go' ! -name '*_test.go' -print | xargs wc -l | sort -rn | sed -n '1,25p'`
- Mobile test evidence:
  - `swift test --package-path apps/loom-companion-ios --filter ConnectionViewModelTests`
