# Technical Debt Remediation Plan

## Summary

- Planning date: 2026-02-23 (Cycle 2)
- Previous cycle: 2026-02-23 Cycle 1 (11 items; all 11 completed)
- Scope: code structure, test coverage, configuration hygiene, developer experience
- Total active items: 11
- Scoring model: impact 35%, risk reduction 30%, drag reduction 20%, effort inverse 15%

## Scoring Snapshot

- Ranking artifact: `.loom/tech-debt-priority.md`
- Top-ranked: `DEBT-016` (76), `DEBT-012` (68), `DEBT-015` (64), `DEBT-014` (57), `DEBT-018` (56)

## Wave 1 (Quick Wins + High Leverage)

- Goal: Complete in-progress callpipeline work, eliminate low-effort boilerplate, and do mechanical file splits with no behavior changes.
- Items:
  - `DEBT-016` (score 76) Finish callpipeline hardening — unit tests per failure class, isolate side effects, standardize error mapping.
  - `DEBT-014` (score 57) Decompose `cmd/loom/main.go` — split Cobra commands into `cmd_sync.go`, `cmd_tunnel.go`, `cmd_auth.go`, `cmd_hud.go`, etc.
  - `DEBT-017` (score 56) Decompose devbox K8s backend — split into `k8s_build.go`, `k8s_runtime.go`, `k8s_objects.go`, `k8s_wait.go`.
  - `DEBT-019` (score 49) Extract hardcoded values — replace inline URLs/models/policies with `pkg/env` calls and named constants.
  - `DEBT-020` (score 47) Add validate helpers — `UnmarshalStringSlice`, `UnmarshalBool`, `UnmarshalInt` in `pkg/validate/args.go`.
- Acceptance criteria:
  - Callpipeline has unit tests covering parse, auth, route, connect, transport error classes. Each side effect (audit, cost, cache, metrics) is behind a testable stage helper.
  - `cmd/loom/main.go` drops below 500 LOC; each split file owns one command group; `go build ./cmd/loom` unchanged.
  - Devbox K8s backend split into 4 files; `go test ./internal/devbox/...` unchanged.
  - Zero hardcoded `localhost` URLs remain in `pkg/codebase/service.go`; all use env fallback chains.
  - `pkg/validate` gains at least 3 new helpers; callers in `pkg/codebase/service.go` migrated.
- Risks/mitigations:
  - Risk: mechanical splits could introduce import cycles. Mitigation: keep all split files in same package.
  - Risk: env var changes could break existing setups. Mitigation: all new env vars have same defaults as current hardcoded values.
- Not in this wave:
  - Agentcontext god object split (DEBT-012) — requires Qdrant registry first.
  - Agent contract convergence (DEBT-015) — medium effort, better sequenced after callpipeline stabilizes.

## Wave 2 (Medium Effort / Coupling Reduction)

- Goal: Reduce cross-surface duplication, close test coverage gaps, and introduce Qdrant client registry.
- Items:
  - `DEBT-015` (score 64) Converge agent contracts — split `cmd_agent.go` into `cmd_agent_context.go`, `cmd_agent_session.go`, `cmd_agent_dispatch.go`, `cmd_agent_nudge.go`; split `bridge/agent.go` by feature; dedupe request parsing with `api_agent.go`.
  - `DEBT-018` (score 56) Close test coverage gaps — add tests for `internal/hud/window` (5 untested files), daemon lifecycle paths, devbox Docker + K8s backends.
  - `DEBT-022` (score 50) Introduce `QdrantRegistry` — replace 15 individual `*QdrantClient` fields on `Service` struct with `registry.Get("collection_name")` accessor.
- Acceptance criteria:
  - `cmd_agent.go` drops below 500 LOC; bridge surface has consistent error envelopes per the shared contract model.
  - Test coverage reaches 35%+ (from 30.4%). `internal/hud/window` has at least 1 test per exported function.
  - `QdrantRegistry` manages all 15 collections; `Service` struct drops those 15 fields; adding a new collection requires 1 line in registry init.
- Risks/mitigations:
  - Risk: agent contract refactor could break HUD API compatibility. Mitigation: keep existing handler signatures as thin wrappers during transition; add negative-path integration tests first.
  - Risk: Qdrant registry adds indirection. Mitigation: type-safe `Get[T]()` with compile-time collection name validation via constants.
- Not in this wave:
  - Full agentcontext domain decomposition (DEBT-012).
  - MCP server main.go splits (DEBT-021).

## Wave 3 (Strategic Refactors)

- Goal: Break apart the two largest god objects and establish scalable patterns for MCP server structure.
- Items:
  - `DEBT-012` (score 68) Split `pkg/agentcontext/service.go` into domain modules: `sessions/`, `presence/`, `memory/`, `graph/`, `workflow/`, each with own service struct and tests.
  - `DEBT-013` (score 54) Split `pkg/codebase/service.go` into `indexer.go`, `watcher.go`, `embed_config.go`, `search_handlers.go`.
  - `DEBT-021` (score 40) Split MCP server main.go files — `cmd/mcp-gitlab/` gets per-resource files (`projects.go`, `issues.go`, `pipelines.go`, etc.); apply same pattern to linkedin and k8s.
- Acceptance criteria:
  - `pkg/agentcontext/service.go` drops below 500 LOC. Each domain module has its own `*Manager` struct, unit tests, and clear interface boundary. `Service` becomes a facade composing managers.
  - `pkg/codebase/service.go` drops below 600 LOC. Indexing, watching, and search are independently testable.
  - MCP server main.go files each drop below 500 LOC. Tool handlers are grouped by resource/domain.
- Risks/mitigations:
  - Risk: agentcontext decomposition is the largest item; cross-cutting concerns (Qdrant access, embeddings, locking) require careful interface design. Mitigation: DEBT-022 (Qdrant registry) must land first; design interfaces before moving code.
  - Risk: codebase split could break the watch/index coordination. Mitigation: preserve existing function signatures; split by file only first, then extract interfaces.
- Dependencies:
  - DEBT-012 depends on DEBT-022 (Qdrant registry from Wave 2).
  - DEBT-013 and DEBT-021 are independent.

## Backlog Conversion

| Debt ID | Backlog ID | Owner | Wave | Status |
|---|---|---|---|---|
| DEBT-016 | TD-PIPELINE-01 | daemon runtime | Wave 1 | **done** (roadmap #20) |
| DEBT-014 | TD-CLI-01 | CLI | Wave 1 | pending |
| DEBT-017 | TD-DEVBOX-01 | devbox | Wave 1 | **done** (roadmap #23) |
| DEBT-019 | TD-CONFIG-01 | cross-cutting | Wave 1 | pending |
| DEBT-020 | TD-VALIDATE-01 | pkg/validate | Wave 1 | **done** |
| DEBT-015 | TD-AGENT-01 | CLI + HUD | Wave 2 | pending (roadmap #21) |
| DEBT-018 | TD-COVERAGE-01 | cross-cutting | Wave 2 | pending (roadmap #2) |
| DEBT-022 | TD-QDRANT-01 | agentcontext | Wave 2 | pending |
| DEBT-012 | TD-AGENTCTX-01 | agentcontext | Wave 3 | pending |
| DEBT-013 | TD-CODEBASE-01 | codebase | Wave 3 | pending |
| DEBT-021 | TD-MCP-01 | MCP servers | Wave 3 | pending |

## Backlog-Ready Slices

### TD-PIPELINE-01 (DEBT-016)

- Problem statement: Callpipeline in `internal/daemon/callpipeline.go` has Stage 1 orchestration (commit `8c2c50d`) but lacks comprehensive failure-class tests and has side effects (audit, cost, cache, metrics) still coupled to the main call path. This blocks gateway policy hooks (#25) and observability work (#12).
- Acceptance criteria:
  - Unit tests cover all 5 failure classes: parse error, auth failure, route miss, connect failure, transport error.
  - Each side effect (audit log, cost tracking, cache update, metrics emit) is behind a testable stage helper function.
  - Standardized error envelope per stage with consistent codes.
  - `callpipeline_test.go` covers stage-boundary regression cases (no full daemon boot required).
- Test/verification strategy:
  - `go test ./internal/daemon/ -run Pipeline` covers all failure classes.
  - Manually verify error envelopes match existing behavior (no breaking changes to proxy clients).
- Rollback/safety notes:
  - Additive refactor; existing `handleCall` path preserved during transition.

### TD-CLI-01 (DEBT-014)

- Problem statement: `cmd/loom/main.go` at 1,907 LOC with 18 functions (~106 lines/func) packs all Cobra command registration into one file, making navigation and targeted changes difficult.
- Acceptance criteria:
  - Split into: `cmd_sync.go` (sync/generate commands), `cmd_tunnel.go` (tunnel commands), `cmd_auth.go` (auth commands), `cmd_hud.go` (hud commands), `cmd_daemon.go` (daemon control), `cmd_completion.go` (shell completion).
  - `main.go` reduced to <300 LOC (root command + command group wiring only).
  - Same package, no API changes. `go build ./cmd/loom` identical binary.
- Test/verification strategy:
  - `go build ./cmd/loom` succeeds; binary SHA matches pre-refactor (modulo build metadata).
  - `loom --help` output unchanged.
  - `golangci-lint run ./cmd/loom/...` clean.
- Rollback/safety notes:
  - Pure mechanical refactor; revert is a single commit.

### TD-DEVBOX-01 (DEBT-017)

- Problem statement: `internal/devbox/backend/k8s.go` (760 LOC) mixes build pod orchestration, runtime lifecycle, Kubernetes object construction, and wait/poll logic in a single file.
- Acceptance criteria:
  - Split into: `k8s_build.go` (image build orchestration), `k8s_runtime.go` (start/stop/exec/status), `k8s_objects.go` (pod/configmap spec builders), `k8s_wait.go` (watch/poll helpers).
  - Same package; no behavior changes; all existing tests pass unchanged.
- Test/verification strategy:
  - `go test ./internal/devbox/...` before and after; identical results.
  - `golangci-lint run ./internal/devbox/...` clean.
- Rollback/safety notes:
  - Mechanical refactor; single-commit revert.

### TD-CONFIG-01 (DEBT-019)

- Problem statement: Multiple hardcoded URLs, model names, and retention policies are scattered across service files instead of using `pkg/env` helpers.
- Acceptance criteria:
  - `pkg/codebase/service.go`: replace hardcoded `http://localhost:8080`, `http://localhost:11434`, model names with `env.String()` calls with same defaults.
  - `pkg/agentcontext/memory_hierarchy.go`: extract retention policy values (24h TTL, 4h compression, 100k token limit) to named constants with env overrides.
  - `cmd/loom/main.go`: version uses `ldflags` injection pattern (already may exist; verify).
- Test/verification strategy:
  - Existing tests pass with no env vars set (defaults match current hardcoded values).
  - Setting env vars changes behavior as expected (manual verification).
- Rollback/safety notes:
  - Defaults match current values; zero behavioral change unless env vars are set.

### TD-VALIDATE-01 (DEBT-020)

- Problem statement: Type assertion boilerplate for extracting `[]string`, `bool`, `int` from `map[string]any` args is repeated 8+ times across `pkg/codebase/service.go` and `pkg/agentcontext/service.go`.
- Acceptance criteria:
  - Add to `pkg/validate`: `StringSliceFromArgs(args map[string]any, key string) []string`, `BoolFromArgs(args, key, default)`, `IntFromArgs(args, key, default)`.
  - Migrate at least the 8 instances in `pkg/codebase/service.go`.
  - Add unit tests for each new helper.
- Test/verification strategy:
  - `go test ./pkg/validate/...` covers new helpers.
  - Existing tests in `pkg/codebase/` pass unchanged.
- Rollback/safety notes:
  - Additive to pkg/validate; callers migrated one at a time.

### TD-AGENT-01 (DEBT-015)

- Problem statement: Three large files (`cmd_agent.go` 1,706 LOC, `bridge/agent.go` 1,993 LOC, `api_agent.go` 989 LOC) implement the same agent operations with duplicated request parsing and divergent error envelopes.
- Acceptance criteria:
  - `cmd_agent.go` split into 4 files by feature group (context, session, dispatch, nudge).
  - `bridge/agent.go` split into feature modules retaining single exported bridge API.
  - Duplicate request parsing replaced with shared contract functions from `agent_contracts.go`.
  - Error envelopes consistent across CLI and HUD paths.
  - Negative-path tests covering both HTTP and daemon fallback for each shared contract.
- Test/verification strategy:
  - `go test ./cmd/loom/ -run Agent` passes.
  - `go test ./internal/hud/... -run Agent` passes.
  - Manual: HUD API returns same error shape as CLI for matching failure cases.
- Rollback/safety notes:
  - Additive; existing handler signatures preserved as thin wrappers during transition.

### TD-COVERAGE-01 (DEBT-018)

- Problem statement: Test coverage is at 30.4% (target: 40%). Three packages have zero test files; daemon lifecycle and devbox backend paths are undertested.
- Acceptance criteria:
  - `internal/hud/window`: at least 1 test per exported function.
  - Daemon: lifecycle tests for flock contention, socket cleanup, proxy autostart.
  - Devbox: integration tests for Docker and K8s backends under monorepo layouts.
  - Overall coverage reaches 35%+.
- Test/verification strategy:
  - `go test ./...` with `-coverprofile` shows 35%+ total.
  - New test files exist in previously untested packages.
- Rollback/safety notes:
  - Additive; no production code changes.

### TD-QDRANT-01 (DEBT-022)

- Problem statement: `Service` struct in `pkg/agentcontext/service.go` has 15 individual `*QdrantClient` fields. Adding a new Qdrant collection requires modifying the struct definition, init function, and handler wiring.
- Acceptance criteria:
  - New `QdrantRegistry` type manages all collection clients via `registry.Get("collection_name")`.
  - `Service` struct replaces 15 client fields with single `*QdrantRegistry` field.
  - Adding a new collection requires only 1 line in registry initialization.
  - All existing tests pass without modification.
- Test/verification strategy:
  - `go test ./pkg/agentcontext/...` passes.
  - New tests verify `QdrantRegistry.Get()` for all 15 collections.
- Rollback/safety notes:
  - Internal refactor; no external API changes. Single-commit revert possible.

### TD-AGENTCTX-01 (DEBT-012)

- Problem statement: `pkg/agentcontext/service.go` is a 1,829 LOC god object with 50+ struct fields mixing 5 unrelated domains (sessions, presence, memory, knowledge graph, workflows).
- Acceptance criteria:
  - Split into domain modules: `sessions/manager.go`, `presence/manager.go`, `memory/manager.go`, `graph/manager.go`, `workflow/manager.go`.
  - Each module has its own struct, constructor, and unit tests.
  - `Service` becomes a thin facade composing the 5 managers.
  - `service.go` drops below 500 LOC.
- Test/verification strategy:
  - `go test ./pkg/agentcontext/...` passes.
  - Each new manager has isolated unit tests.
  - Integration tests verify cross-domain operations still work through the facade.
- Rollback/safety notes:
  - Requires DEBT-022 (Qdrant registry) first to avoid passing 15 clients into each manager.
  - Phased approach: move one domain at a time to limit blast radius.

### TD-CODEBASE-01 (DEBT-013)

- Problem statement: `pkg/codebase/service.go` at 2,140 LOC is the largest file in the project, mixing indexing orchestration, file watching, embedding provider configuration, and search tool handlers.
- Acceptance criteria:
  - Split into: `indexer.go` (index orchestration), `watcher.go` (watch/poll loop), `embed_config.go` (provider selection and initialization), `search_handlers.go` (MCP tool handlers for search/definition/references).
  - `service.go` reduced to <600 LOC (Service struct + constructor + coordination).
  - Remove `//nolint:noctx` suppressions by using context-aware `exec.CommandContext`.
- Test/verification strategy:
  - `go test ./pkg/codebase/...` passes.
  - `golangci-lint run ./pkg/codebase/...` clean (no nolint needed).
- Rollback/safety notes:
  - Mechanical split first; interface extraction in follow-up if needed.

### TD-MCP-01 (DEBT-021)

- Problem statement: MCP server entrypoints are monolithic: `cmd/mcp-gitlab/main.go` (1,594 LOC, 28 funcs), `cmd/mcp-linkedin/main.go` (1,403 LOC, 58 funcs), `cmd/mcp-k8s/main.go` (1,089 LOC).
- Acceptance criteria:
  - `cmd/mcp-gitlab/`: split into `projects.go`, `issues.go`, `pipelines.go`, `merge_requests.go`, `main.go` (wiring only).
  - Apply same pattern to linkedin and k8s servers.
  - Each main.go drops below 500 LOC.
- Test/verification strategy:
  - `go build ./cmd/mcp-gitlab` succeeds; `go test ./cmd/mcp-gitlab/...` passes.
  - Same for linkedin and k8s.
- Rollback/safety notes:
  - Mechanical; same package, no API changes. Single-commit revert per server.

## Deferred / Not In Scope

- HUD frontend component splits (Svelte files) — tracked separately in roadmap #22.
- New MCP transport features beyond current Streamable HTTP scope.
- Session lease architecture implementation — design complete, sequenced after callpipeline stabilizes.
- Broad MCP server refactors for servers not in the top 3 by size.
- HUD UI modernization tasks outside runtime reliability.
