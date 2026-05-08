# Implementation Plan: Weaver ↔ Qwen3 (FlexInfer) ↔ HUD/App/Extension/Agent-Context/Mills

**Date**: 2026-05-08
**Research**: `.loom/110-research-weaver-qwen3-integration-2026-05-08.md`
**Product Spec**: `.loom/111-product-spec-weaver-qwen3-integration-2026-05-08.md`

## Execution order (eight slices)

```
S1: aimodels package + role resolver           ┐
S2: GitOps - alias + router model bring-up      ├─ parallel (S1 + S2 must both land before S3)
                                                ┘
        ↓
S3: defaults migration in code (weaver / mills / coordinator / autofix)
        ↓
S4: daemon preflight + degraded surface
        ↓
S5: Mills research → weaver delegation (shadow → on)   ┐
S6: HUD surfaces (WeaverPanel + LiveSessionsCard)      ├─ parallel
S7: iOS + VS Code extension surfaces                   ┘
        ↓
S8: agent-context tie-in + Spectator parity + docs
```

S1 and S2 are independent. S3 depends on both (S1 for the API, S2 for the alias to resolve). S4 must follow S3 to avoid preflight false positives during migration. S5–S7 are independent of each other; S5 depends on S3 (Mills code uses the resolver). S8 lands last and references everything.

Estimated effort: S1 ~1d, S2 ~0.5d, S3 ~1d, S4 ~1d, S5 ~3d (incl. one-week soak), S6 ~1.5d, S7 ~2d, S8 ~1d. Total: ~10 working days plus soak.

---

## S1 — `pkg/aimodels` resolver

**Spec refs**: MR-001, MR-002 (consumer side), MR-005

### Files

**`pkg/aimodels/registry.go`** (new)
- `type Role string` with the six constants in spec MR-001.
- `type RoleSpec struct { Primary string; Fallbacks []string }`.
- `type Resolver struct { roles map[Role]RoleSpec; logger *slog.Logger; metrics resolverMetrics }`.
- `func DefaultResolver() *Resolver` — returns baked-in defaults table from spec.
- `func LoadResolver(path string, logger *slog.Logger) (*Resolver, error)` — reads `~/.config/loom/aimodel-roles.yaml`, falls back to defaults; missing file is not an error.
- `func (r *Resolver) Resolve(role Role) string` — primary or empty.
- `func (r *Resolver) ResolveWithFallbacks(role Role) []string` — primary + fallbacks.
- `func (r *Resolver) ResolveOrDefault(role Role, def string) string`.

**`pkg/aimodels/registry_test.go`** (new)
- Resolves baked-in default for each role.
- Override file replaces primary, keeps fallbacks if not specified.
- Counter increments on each resolve, with `fallback_used` tag.

**`pkg/aimodels/metrics.go`** (new)
- `loom_aimodel_resolution_total{role,resolved_model,fallback_used}` Prometheus counter.

### Tests
- Unit tests for resolver (default, override, missing file, malformed file warning).
- Round-trip YAML test (write a custom roles file, reload, confirm primary/fallbacks).

### Acceptance
- `go test ./pkg/aimodels/...` green.
- `golangci-lint run pkg/aimodels/...` clean.

---

## S2 — GitOps: LiteLLM alias + router model bring-up

**Spec refs**: MR-002, MR-003, PRE-001 (preview)

### Files

**`platform/gitops/k3s/ai/flexinfer/modeldeployments/qwen3-8b-fast.yaml`**
- Add to `spec.litellm.aliases`:
  - `qwen3-8b`
  - `qwen3-default`

**`platform/gitops/k3s/ai/flexinfer/modeldeployments/kustomization.yaml`**
- Add `- qwen3-1p7b-tools.yaml` (new model spec to be created/copied from existing radeonvii spec, pinned `min_replicas: 1`).

**`platform/gitops/k3s/ai/flexinfer/modeldeployments/qwen3-1p7b-tools.yaml`** (new)
- Copy from existing `qwen3-1p7b-tools-radeonvii` Model spec live in cluster (export via `kubectl get model qwen3-1p7b-tools-radeonvii -o yaml`).
- Set `serverless.minReplicas: 1` so it's always Ready.
- Confirm `nodeSelector: cblevins-radeonvii`.

### Reconcile + verify

```bash
flux reconcile kustomization flexinfer-models -n flux-system
kubectl get model -n flexinfer-system -w   # wait for qwen3-1p7b-tools-radeonvii Phase=Ready
```

Then:
```bash
mcp__loom__flexinfer__flexinfer_proxy_models   # confirm qwen3-8b alias appears
```

### Acceptance
- `qwen3-8b` shown in `/v1/models` of FlexInfer proxy.
- `qwen3-1p7b-tools-radeonvii` Phase=Ready and stays Ready (cold-start gone).

---

## S3 — Code defaults migration

**Spec refs**: MR-004

### Files

**`pkg/weaver/config.go`**
- Replace constants `DefaultRouterModel = "gemma-4-turboquant"` and `DefaultSubagentModel = "gemma-4-turboquant"` with calls into resolver:
  ```go
  func defaultRouterModel(r *aimodels.Resolver) string {
      return r.ResolveOrDefault(aimodels.RoleWeaverRouter, "qwen3-1p7b-tools-radeonvii")
  }
  func defaultSubagentModel(r *aimodels.Resolver) string {
      return r.ResolveOrDefault(aimodels.RoleWeaverSubagent, "qwen3-8b")
  }
  ```
- `LoadConfigFromEnv` accepts an injected `*aimodels.Resolver` (or builds default). Env vars (`WEAVER_ROUTER_MODEL`, `WEAVER_SUBAGENT_MODEL`) still take precedence.

**`pkg/mills/clients/flexinfer.go:64-67`**
- Replace `cfg.JudgeModel = "qwen3-8b-instruct"` with resolver call.
- Same for `cfg.WeaverModel`.

**`internal/hud/coordinator/config.go:65`**
- `DefaultModel:  aimodels.DefaultResolver().Resolve(aimodels.RoleCoordinatorDefault)` (constructed lazily so tests can inject).

**`internal/hud/autofix/autofix.go`**
- Replace literal `"qwen3-8b"` with `aimodels.DefaultResolver().Resolve(aimodels.RoleAutofix)`.

**`internal/daemon/weaver_embed.go`**
- Construct resolver once at daemon start, pass into weaver.LoadConfigFromEnv and into Mills clients (when daemon embeds the operator path).

### Tests
- Update existing weaver/mills/coordinator tests that assert literal model strings — switch to resolver-aware fixtures.
- Add table tests verifying that `WEAVER_ROUTER_MODEL=foo` env override beats resolver primary.

### Acceptance
- `go test ./pkg/weaver/... ./pkg/mills/... ./internal/hud/coordinator/... ./internal/hud/autofix/...` green.
- Manual smoke: launch daemon with `WEAVER_ENABLED=1` and no env overrides → first `weaver__query` succeeds against `qwen3-1p7b-tools-radeonvii` (router) + `qwen3-8b` (subagent).

---

## S4 — Daemon preflight + degraded surface

**Spec refs**: PRE-001, PRE-002, PRE-003

### Files

**`pkg/flexinfer/client.go`**
- Add `func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error)` hitting `/v1/models`. Returns id + ready state (proxy advertises both).

**`internal/daemon/weaver_embed.go`**
- After `weaver.NewRouter`:
  ```go
  models, err := client.ListModels(ctx)
  if err != nil {
      d.logger.Warn("weaver: model listing failed; preflight skipped", "error", err)
  } else {
      d.weaverPreflight = preflight(models, router.Registry(), cfg)
      if d.weaverPreflight.Degraded {
          d.logger.Warn("weaver: degraded", "missing", d.weaverPreflight.MissingModels)
          d.eventBus.Publish("weaver.degraded", d.weaverPreflight)
      }
  }
  ```

**`internal/daemon/weaver_tools.go` / `weaver_handlers.go`**
- `loom/weaver/status` payload extends with `degraded`, `missing_models`, `ready_models`, `auto_compose`, `spawn_bridge` (per spec PRE-003).

**`internal/hud/domain/weaver/handlers.go`**
- Pass-through; HUD already proxies the daemon response, so the new fields appear without code change in handler. Update `WeaverPanel.svelte` (S6) to render them.

**`pkg/flexinfer/wake.go`** (new)
- `func WakeIfIdle(ctx context.Context, c *Client, model string) (wakeMs int64, err error)` — issues 1-token completion, measures latency. Used only when `WEAVER_PROACTIVE_WAKE=1`.

**`internal/daemon/weaver_embed.go`**
- Wire `WEAVER_PROACTIVE_WAKE` into router's per-call hook. Default off.

### Tests
- `internal/daemon/weaver_embed_test.go` (new): preflight detects missing model, emits SSE event, populates status.
- `pkg/flexinfer/wake_test.go`: stub server, asserts wake call shape and latency capture.

### Acceptance
- Bring up daemon with `WEAVER_ROUTER_MODEL=does-not-exist` → status reports `degraded: true, missing_models: ["does-not-exist"]`.
- HUD WeaverPanel shows yellow banner.

---

## S5 — Mills research → weaver delegation (shadow → on)

**Spec refs**: MW-001, MW-002, MW-003, MW-004

### Files

**`pkg/mills/clients/flexinfer.go`**
- `WeaverClient.Research`:
  ```go
  switch flag.Get("MILLS_RESEARCH_VIA_WEAVER") {
  case "off":
      return w.legacyResearch(ctx, req)
  case "shadow":
      return w.shadowResearch(ctx, req)
  case "on":
      return w.weaverResearch(ctx, req)
  }
  ```
- `weaverResearch` issues `loom/weaver/query` over the daemon socket (operator pod gets a sidecar credential mount or short-lived token).
- `shadowResearch` runs both, picks the legacy result for backward-compat, logs the diff.

**`pkg/mills/audit/triggers.go`** + **`platform/gitops/k3s/mills/configmap-policy.yaml`**
- Replace pool defaults per MW-004:
  - `tech-debt` reviewer model: `qwen3.5-9b → qwen3-8b`
  - audit pool: `qwen3-32b → qwen3-8b`, `llama-4-70b → qwen3-14b-abliterated`
- Update `cmd/loom-mills-operator/main.go` example PoolPolicy comment & defaults.

**`pkg/mills/storage/migrations/`** (new migration)
- Add `pipeline_runs.research_diff TEXT` column (sqlite ALTER TABLE).
- Increment schema version; tests must run migration.

**Feature flag plumbing**
- Add `MILLS_RESEARCH_VIA_WEAVER` env to operator deployment manifest.
- Default ConfigMap value `off`. Flip to `shadow` after S3 lands.

### Tests
- Mills `WeaverClient` test exercises all three modes against fakes.
- Diff-capture path writes to `pipeline_runs.research_diff` and asserts schema.

### Soak
- One week at `shadow`; observe Grafana panel `loom_mills_research_diff_*`.
- Flip to `on` if length variance < 10% and latency < 2x.

### Acceptance
- All `pkg/mills/...` tests green.
- Mills v2 ConfigMap reconciles cleanly; ConfigValid + Schedulable on every Model referenced.
- Shadow soak dashboard available.

---

## S6 — HUD surfaces

**Spec refs**: HUD-001, HUD-002, HUD-003, HUD-004

### Files

**`internal/hud/frontend/src/lib/components/WeaverPanel.svelte`**
- Add health header (`degraded` banner with missing model list).
- Add "Defaults" subview reading `/api/aimodels/roles`.
- Wire SSE subscription for `weaver.*` events; update history without polling.

**`internal/hud/frontend/src/lib/components/LiveSessionsCard.svelte`**
- For sessions whose latest spawn metadata has `weaver_query_id`, render a small chip "↳ from weaver query <hash>" linking to `/weaver?q=<id>`.

**`internal/hud/domain/aimodels/handlers.go`** (new)
- `GET /api/aimodels/roles` returns the resolver's role map + last 100 resolutions count by role + fallback rate.

**`internal/hud/sse_hub.go`**
- Already a hub; ensure `weaver.*` events are forwarded (likely no code change beyond adding to allowlist).

### Tests
- Svelte component test: WeaverPanel renders degraded banner when status.degraded=true.
- Go handler test for `/api/aimodels/roles`.

### Acceptance
- `make test-hud` (or equivalent frontend test target) green.
- Visual: WeaverPanel shows green-or-yellow status, defaults table, live updates on a manual `weaver__query` call.

---

## S7 — iOS + VS Code extension surfaces

**Spec refs**: IOS-001, IOS-002, IOS-003, VSC-001, VSC-002, VSC-003

### iOS files

**`apps/loom-companion-ios/Sources/LoomCompanionKit/Weaver/`** (new directory)
- `Models.swift` — `WeaverStatus`, `WeaverDomainSummary`, `WeaverHistoryEntry`, `WeaverMetrics`, `WeaverEvent`.
- `WeaverClient.swift` — actor with REST methods + SSE subscription.
- `WeaverEventStream.swift` — bridges existing SSE client to a typed `AsyncStream<WeaverEvent>`.

**`apps/loom-companion-ios/Sources/LoomCompanion/Views/Weaver/`**
- `WeaverView.swift` — read-only UI per IOS-002.
- `WeaverHistoryRow.swift`, `WeaverDomainList.swift`.

**`apps/loom-companion-ios/Sources/LoomCompanion/Views/LiveSessionsView.swift`**
- Add weaver chip (IOS-003).

**`apps/loom-companion-ios/project.yml`** + run `make mobile-ios-project-sync`.

**Tests** — unit test for `WeaverClient.history` against a stubbed URL session (existing test infra).

### VS Code extension files

**`services/loom/src/views/weaverView.ts`** (new)
- `WeaverViewProvider` implementing `vscode.TreeDataProvider`.
- Polls `loom://weaver/status` (or daemon RPC); displays status + domains + recent queries.

**`services/loom/src/commands/weaver.ts`** (new)
- `loom.weaver.runQuery` command — `vscode.window.showInputBox` → daemon RPC → output channel.
- Behind setting `loom.experimental.weaver` (default false).

**`services/loom/src/extension.ts`**
- Register the new view provider + command.
- Add to `package.json` `contributes.views`, `contributes.commands`, `contributes.configuration`.

**`services/loom/src/commands/dashboard.ts`** (or equivalent)
- Add Weaver row to Sync Dashboard.

### Tests
- iOS: existing snapshot suite + unit tests for new client.
- Extension: `services/loom/src/__tests__/weaverView.test.ts` + `weaver.runQuery.test.ts`.

### Acceptance
- iOS smoke: companion app launches, navigates to Operations → Weaver, shows Ready state, history populated by a manual query.
- VS Code: Loom sidebar shows WeaverView, `loom.weaver.runQuery` returns an answer with the experimental setting on.

---

## S8 — Agent-context tie-in + Spectator parity + docs

**Spec refs**: AC-001, AC-002, AC-003, SP-001, SP-002, DOC-001, DOC-002, DOC-003

### Files

**`pkg/weaver/router.go`**
- In `Query()`, when `req.ParentSessionID != ""`:
  - Pre: presence heartbeat with `in_progress="weaver:<query_id>"`.
  - Post: `agent_context_add` with entry type `weaver_query` (gated by `WEAVER_RECORD_TO_CONTEXT`, default true).

**`pkg/weaver/router.go` + `pkg/flexinfer/circuit_breaker.go`**
- Resolver now wraps each model in a CB keyed by `(model, role)`. On open, ResolveWithFallbacks walks fallbacks.

**`cmd/loom/cmd_spectate.go`** (Spectator Phase 6, may already exist or be in-flight)
- Default subscription set extends to include `weaver.*`.

**`internal/spectator/parity_test.go`** (new or existing)
- Asserts a `weaver.query.complete` event reaches HUD, iOS, and CLI subscribers within 1s of emission.

**`mcp/skills/mills-ops/SKILL.md`** — add a "Research stage uses weaver" section.
**`services/loom-core/docs/weaver.md`** — full operator guide.
**`services/loom-core/docs/runbooks/weaver-degraded.md`** — runbook for degraded state.

### Tests
- Integration: weaver query with `ParentSessionID` records a `weaver_query` entry in agent-context.
- Parity test as above.

### Acceptance
- `agent_context_search` returns `weaver_query` entries for sessions that ran queries.
- `loom spectate <session>` shows interleaved `weaver.*` and session events.
- New runbook linked from `/api/weaver/status` degraded banner (HUD + iOS).

---

## Cross-cutting concerns

### Backwards compatibility

- `WEAVER_ROUTER_MODEL` / `WEAVER_SUBAGENT_MODEL` env vars **still win** over the resolver. Operators with custom values keep working.
- Mills' existing `WeaverClient.Research` keeps the same return signature; only the body changes.
- Coordinator's external behavior unchanged; only `DefaultModel` resolution.
- HUD `/api/weaver/status` adds fields; never removes any. iOS/extension can adopt incrementally.

### Telemetry

New metrics:
- `loom_aimodel_resolution_total{role,resolved_model,fallback_used}` (S1)
- `loom_weaver_preflight_status{state="ready"|"degraded"}` (S4)
- `loom_weaver_proactive_wake_ms` histogram (S4, only when feature on)
- `loom_mills_research_diff_*` (S5 shadow soak)
- `loom_weaver_circuit_breaker_state{role,model}` (S8)

New SSE events:
- `weaver.degraded` (S4)
- `weaver.preflight.complete` (S4)
- `weaver.query.{start,domain.start,domain.end,complete}` already emitted by router; ensure they reach HUD + iOS + spectator (S6, S7, SP-001).

### Feature flags

- `WEAVER_PROACTIVE_WAKE` (env, default off) — S4
- `WEAVER_RECORD_TO_CONTEXT` (env, default on) — S8
- `MILLS_RESEARCH_VIA_WEAVER` (env / configmap, off|shadow|on) — S5
- `loom.experimental.weaver` (VS Code setting, default false) — S7

### Rollout sequencing

1. **Day 0**: S1 + S2 land (parallel MRs).
2. **Day 1**: S3 lands (depends on S1+S2 reconciled).
3. **Day 2**: S4 lands. Daemon preflight live; degraded banner in HUD.
4. **Day 3–4**: S6 lands (HUD surfaces).
5. **Day 4–5**: S7 lands (iOS + extension).
6. **Day 5**: S5 starts in `shadow` mode. Soak begins.
7. **Day 12**: S5 flips to `on` if dashboards green.
8. **Day 13**: S8 lands.

### Branch / MR convention

Branches: `feat/weaver-qwen3-<slice>` (e.g., `feat/weaver-qwen3-s1-aimodels`). One MR per slice. CI green required before next slice opens.

### Risks (delta from spec)

- Daemon socket exposed to Mills operator pod requires a service account or shared cluster secret; if not already in place, add to S5 prep.
- iOS SwiftPM project sync (`make mobile-ios-project-sync`) is required after S7 file additions.
- LiteLLM alias add (S2) requires the FlexInfer controller to pick up the change without a model restart; verify in staging first.

## Sources

All sources from research + spec; plus:

- `internal/daemon/weaver_embed.go:14-87` — current init flow that S4 extends
- `pkg/flexinfer/client.go` — base client to gain `ListModels`
- `apps/loom-companion-ios/Sources/LoomCompanionKit/Mills/` — pattern for iOS module structure (S7)
- `services/loom/src/commands/index.ts` — extension command registration pattern (S7)
- `cmd/loom/cmd_spectate.go` (or in-flight `98`/`99` Phase 6 plan) — spectator integration point (S8)
