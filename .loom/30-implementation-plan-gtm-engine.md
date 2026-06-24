# Implementation Plan: GTM / Lead-Generation Engine

**Date:** 2026-06-21
**Status:** rev 2 — pipeline-of-record = existing Corteza CRM (not a new ICC instance)
**Spec:** [20-product-spec-gtm-engine.md](20-product-spec-gtm-engine.md)
**Brainstorm:** [brainstorm-platform-monetization-2026-06-21.md](brainstorm-platform-monetization-2026-06-21.md)

## Sequencing principle

Slice 0 is the kill-test and **blocks every later slice** (workspace spec-riskiest-assumption policy). Build the *minimum* to run it — no new server, no new modules beyond what Corteza already gives us. Only invest in real tooling (Slices 1+) if Slice 0 books ≥2 qualified calls. Multi-tenancy (Corteza namespaces) is designed in from Slice 1, but the kill-test runs single-tenant ("flexinfer-self").

---

## Slice 0 — KILL-TEST (gate) · existing tools + idle Corteza · ~2 weeks
**Goal:** prove automated sourcing + personalized outreach books ≥2 qualified discovery calls.

- Write ICP for one tight segment (e.g. "seed–Series-A AI-native startups, 5–30 eng, no in-house infra/ML-ops") → `.loom/gtm-icp-v1.md`.
- Source ~50 prospects with `mcp-tavily` → contacts + companies.
- Enrich + draft per prospect with FlexInfer (existing chat lane) from a personalization template.
- **Human-review each draft**, send via `mcp-google-workspace` `gmail_send_message`.
- Log leads + outreach + replies into Corteza by hand (UI) or quick API calls — it's already deployed, so this exercises the real backend at zero build cost.
- Measure booked qualified calls over 14 days.

**Completion criterion:** ≥2 qualified discovery calls → unblock Slice 1. Else → STOP, pivot to content-inbound (Combination B), set spec §2 Status = FAILED with evidence.
**Deliverables:** ICP doc, sourcing+outreach script, results memo updating spec §2 Status.

---

## Slice 1 — Corteza CRM enablement (config, not deploy) · `platform/gitops/k3s/crm`
**Goal:** Corteza usable as a programmatic, multi-tenant pipeline-of-record.

- **Verified 2026-06-21 (on-LAN):** Corteza + Postgres pods healthy (img 2024.9.7, 128d), but install is FRESH — 0 namespaces, 0 modules, no human admin (only 3 system users). So this slice MUST: (i) **create the initial admin user** (first-run init was never done); (ii) build or import the GTM namespace + modules via Compose.
- **Cloudflare Access gate (confirmed via probe):** `crm.flexinfer.ai` sits behind `flexinfer.cloudflareaccess.com`. Reach the API in-cluster via `http://corteza.crm.svc.cluster.local` (bypasses CF), or provision a CF Access service token for any out-of-cluster client. `mcp-gtm` will deploy in-cluster (Slice 2) for this reason.
- Determine whether the shipped **CRM namespace** is imported. If yes, adopt its modules; if no, build a lean GTM namespace via Compose: `Lead`, `Company`, `Opportunity` (with pipeline stages), `OutreachStep`. Add fields `icp_score`, `source`, `last_touch`.
- Create a Corteza **service user + API credential**; store via SOPS in `k3s/crm/secrets.yaml`.
- Smoke-test the Compose REST API: authenticate → create Lead → transition Opportunity stage → list records. Document the auth flow + endpoints in `.loom/00-mcp-inventory.md`.
- Create the dogfood namespace "flexinfer-self".

**Completion:** authenticated API round-trip (create→transition→list) works against Corteza; modules + pipeline stages exist; service credential in SOPS.

---

## Slice 2 — `mcp-gtm` MCP server · `services/loom-core/cmd/mcp-gtm`
**Goal:** agent-facing tool surface wrapping the Corteza API + orchestrating tavily/flexinfer/google-workspace.

- Scaffold with `mcpscaffold.NewServer` (`pkg/mcpscaffold/scaffold.go:41`); modular `tools_*.go` (jobsearch/agent-context pattern).
- Corteza client (`pkg/httpclient`): JWT auth, Compose record CRUD, namespace selection (tenant).
- Tools: `gtm_icp_define/get`; `gtm_source_prospects`; `gtm_enrich_lead`; `gtm_score_lead`; `gtm_lead_list/get/create/update/transition`; `gtm_opportunity_*`; `gtm_outreach_draft/approve/send` (send = `confirm=true` gate); `gtm_followup_due`; `gtm_book_call`; `gtm_pipeline_funnel`; `gtm_usage_report`.
- Register: `registry.yaml` (env: `CORTEZA_BASE_URL`, `CORTEZA_API_TOKEN`, `TAVILY_API_KEY`; `always_allow` = read tools only) + `Makefile` + build target → `make servers && loom sync claude --regen`.
- **Deploy in-cluster** in mcp-hub (model after `k3s/mcp-hub/servers/jobsearch/deployment.yaml`) so `CORTEZA_BASE_URL=http://corteza.crm.svc.cluster.local` bypasses Cloudflare Access.
- Tests: per-tool handlers with mocked Corteza; send-gate refuses without `confirm=true`.

**Completion:** `mcp-gtm` builds, registers, shows in `loom sync status`; full source→draft→send (gated)→record-in-Corteza works via MCP.

---

## Slice 3 — `gtm-leadgen-loop` skill + managed runtime · `services/loom-core` + Corteza Workflows
**Goal:** the automated daily loop + per-tenant cron; push in-CRM reactions to native Corteza Workflows.

- Skill in `skills-registry.yaml`: source → enrich → score → upsert-to-Corteza → draft → queue-for-approval → send-approved → track-replies → book-calls → update-pipeline.
- Reply tracking: poll Gmail via google-workspace; transition Corteza records on reply; create follow-up tasks.
- Native **Corteza Workflows**: stage-transition side-effects, follow-up reminders, Slack/email notifications (`mcp-slack` for external alerts).
- Per-tenant cron (Loom daemon) running the loop on schedule.
- Tests: loop dry-run (no send) produces correct action plan; idempotent re-runs don't double-send.

**Completion:** loop runs unattended for "flexinfer-self"; drafts queue, approved sends fire, replies update the Corteza pipeline.

---

## Slice 4 — Metering + retainer dashboards · Corteza Reporter
**Goal:** the recurring-revenue value artifacts (mostly Reporter config, minimal build).

- Reporter: per-tenant funnel (sourced→contacted→replied→booked→won) + pipeline dashboard.
- Metering: record-activity rollups → billable units (sourced/sent/booked); add a `UsageEvent` module only if Reporter rollups are insufficient.
- Auto weekly per-tenant digest (Corteza Workflow scheduled report or skill-emitted summary) = the recurring deliverable clients receive.
- Optional: scrape Corteza/Reporter metrics into Prometheus for engine health.

**Completion:** per-tenant weekly digest + funnel + usage rollup generated automatically.

---

## Slice 5 — Productization + conversion-feedback LoRA · onboarding + FlexInfer
**Goal:** turn the engine into a sellable SKU; build the moat.

- Tenant onboarding: new Corteza namespace, define ICP, connect tenant Gmail/Calendar via OAuth (`loom auth google`), seed templates.
- Templated outreach libraries per ICP segment.
- **Conversion-feedback LoRA:** fine-tune on replied/booked outcomes (pulled from Corteza) via the Finetune CRD + GPU-lease scheduler; serve as `gtm-outreach`.
- Packaging: define the sprint SKU (scope, price, deliverable) referencing the engine as the artifact.

**Completion:** a second (real client) tenant onboarded as a Corteza namespace; conversion LoRA serves; sprint SKU documented.

---

## Cross-cutting
- **Testing:** Go (`go test -race -cover ./...`) for mcp-gtm; prefer devbox sandbox. Regression test per bug fix.
- **Deploy:** GitOps only (Flux). Corteza already deployed; changes = SOPS secrets + namespace config, not new manifests (until Reporter/UsageEvent require any).
- **Auto-ship:** scoped commits, branch push, MR + auto-merge per workspace policy.
- **Dependencies:** Slice 0 → (gate) → Slice 1 → 2 → 3; Slice 4 depends on 1+3; Slice 5 depends on 2+3+4 and FlexInfer Finetune lane.

## Estimated shape (post-gate)
| Slice | Service(s) | Rough size |
|---|---|---|
| 0 | scripts + .loom docs + Corteza UI | days (the gate) |
| 1 | gitops/k3s/crm config + SOPS | Corteza namespace/modules + service token, no new deploy |
| 2 | loom-core/cmd/mcp-gtm | new server, ~6 tool files + Corteza client |
| 3 | loom-core skills + Corteza Workflows + daemon | skill + cron + reply tracking |
| 4 | Corteza Reporter | dashboards + rollups, low-code |
| 5 | onboarding + flexinfer finetune | OAuth + LoRA + SKU packaging |
