# Product Spec: GTM / Lead-Generation Engine ("dogfooded agentic GTM")

**Date:** 2026-06-21
**Status:** Draft for review (rev 2 — pivoted pipeline-of-record to existing Corteza CRM)
**Lineage:** Derived from [brainstorm-platform-monetization-2026-06-21.md](brainstorm-platform-monetization-2026-06-21.md) — Recommended path = **Combination A** (productized agent-enablement sprints, powered by an automated lead-gen engine built on our own platform).

## 1. Problem & Goal

Turn the platform (FlexInfer + Loom + the idle Corteza CRM) into revenue via productized "agent enablement" sprints sold to **SMBs without AI expertise** and **AI-native startups**. The lead generation + management must be **primarily automated**, and the automated engine must double as (a) our own pipeline filler, (b) the flagship demo of "we build agents that create value," and (c) the first productizable SKU.

**Operator decisions (2026-06-21):**
- **Pipeline-of-record = the existing Corteza CRM** at `crm.flexinfer.ai` — deployed via Flux but **never utilized**. Drive it via its REST API + native Workflow engine instead of building a new store. (Supersedes the rev-1 "separate ICC instance" decision.)
- **Outreach channel = email only** for MVP (LinkedIn automation is a ToS landmine; defer).
- **Revenue model = recurring from day one** — design for retainers/managed-runtime (multi-tenant via Corteza namespaces, usage metering, ongoing-pipeline dashboards via Corteza Reporter).

## 2. Riskiest assumption + kill-test (BLOCKING)

> Per workspace policy, the downstream slice plan is BLOCKED until this kill-test passes. It is Slice 0.

**Load-bearing assumption:** Automated outbound/inbound can produce *qualified* B2B leads for a technical service without becoming spam or collapsing into manual founder-led sales. Every slice past Slice 0 inherits this bet.

**Kill test:** Run a 2-week pilot on a single tenant (ourselves). Define one tight ICP; use existing platform tools (Tavily + FlexInfer + Gmail) to source ~50 prospects, generate genuinely personalized outreach, send (with human review of the send step), log to Corteza, and measure whether it books **≥2 qualified discovery calls**. Booked qualified calls is the observable, unambiguous outcome — not emails sent or open rate.

**Failure mode if wrong:** We build a polished agentic-GTM product nobody responds to; the "automated" premise quietly forces a return to manual sales, meaning the real business was content-inbound (brainstorm Combination B) all along.

**Status:** not run (tooling complete + tested 2026-06-24 — see [slice0-pilot/HANDOFF.md](slice0-pilot/HANDOFF.md); execution is operator-gated).

> **2026-06-24 operator decision:** Slice 1 (Corteza CRM enablement) authorized to start in parallel, *overriding* this gate. The kill-test still owns go/no-go for Slices 2–5; Slice 1 is the cheap, reversible CRM-config prerequisite. Rationale: tooling is done, the gate is human-run on the operator's schedule, and Slice 1 unblocks no irreversible spend.

## 3. Existing building blocks (reuse, don't rebuild)

Inventory from codebase exploration (2026-06-21):

| Capability | Existing component | Evidence |
|---|---|---|
| **Pipeline-of-record (CRM)** | **Corteza** 2024.9.7 — full CRM (Leads/Contacts/Accounts/Opportunities/Pipeline), Workflow engine, Reporter dashboards, namespaces (multi-tenant), REST API, JWT auth. Deployed + TLS, **idle, no integration yet**. | `platform/gitops/k3s/crm/corteza.yaml:43-68`, wired via `k3s/flux/apps/services/kustomization.yaml:11` |
| **CRM/leads + outreach-draft precedent** | `mcp-jobsearch` (`jobsearch_leads_*`, `jobsearch_workflow_drafts_*` with confirm-to-send) | `services/loom-core/cmd/mcp-jobsearch/tools_workflow_crm.go:1-232` |
| **Prospect research/enrichment** | `mcp-tavily` (`search`, `search_news`, `extract`, `search_context`) | `services/loom-core/cmd/mcp-tavily/main.go:86-197` |
| **Outreach delivery + call booking** | `mcp-google-workspace` (`gmail_send_message`, `calendar_*`) | `services/loom-core/cmd/mcp-google-workspace/main.go` |
| **Alerts / notifications** | `mcp-slack` (`post_message`, `search_messages`) | `services/loom-core/cmd/mcp-slack/main.go` |
| **Lookalike search + memory** | `mcp-qdrant` (vector search), `mcp-agent-context` | `services/loom-core/cmd/mcp-qdrant/main.go:56-120` |
| **Inference (draft/score) + fine-tune** | FlexInfer chat lanes + Finetune CRD + GPU-lease scheduler (shipped) | flexinfer memory: project_gpu_lease_scheduler, project_f1_training_killtest |
| **New MCP server scaffold** | `pkg/mcpscaffold.NewServer` + registry.yaml + Makefile + `loom sync` | `services/loom-core/pkg/mcpscaffold/scaffold.go:41-82`, `.../mcp/context/registry.yaml:62-87` |

**Conclusion:** ~80% of the engine already exists, and the CRM is already deployed. Net-new work = one orchestration MCP server (`mcp-gtm`) wrapping the Corteza API + existing tools, one workflow skill, and Corteza configuration (namespace + modules + a service account). No new datastore, no schema migrations to author from scratch.

**Live probe (2026-06-21, partial):**
- Corteza is deployed and served publicly via Cloudflare at `crm.flexinfer.ai` (HTTP/2, responds).
- **NEW infra fact — Corteza is gated by Cloudflare Access (Zero Trust):** all requests 302-redirect to `flexinfer.cloudflareaccess.com/cdn-cgi/access/login/crm.flexinfer.ai` with `service_token_status:false`. The public hostname is therefore NOT directly callable by an API client without a **CF Access service token** (`CF-Access-Client-Id` / `CF-Access-Client-Secret` headers + a Zero-Trust policy allowing it). Implication for §4.2: prefer running `mcp-gtm` **in-cluster** so it hits the Corteza Service ClusterIP directly and bypasses Cloudflare entirely (this is what `mcp-jobsearch` already does in mcp-hub — `platform/gitops/k3s/mcp-hub/servers/jobsearch/deployment.yaml`).
- **RESOLVED 2026-06-21 (on-LAN, queried Postgres `corteza` DB directly via `kubectl exec`):** `corteza` pod `1/1 Running` (img `2024.9.7`, 128d uptime), `postgres` `1/1 Running` — both healthy. **But the install is completely FRESH:** `compose_namespace` = **0 rows**, `compose_module` = **0 rows**, `users` = only **3 system accounts** (`corteza-provisioner`/`corteza-service`/`corteza-federation`), **no human admin**. So the shipped CRM namespace is **NOT pre-imported** AND first-run init was never done.

**Slice 1 scope (now confirmed):** (a) **create the initial admin user** (corteza-server provisioning / `ADMIN_*` env / setup wizard) — first-run init was never done; then (b) build or import the GTM namespace + modules (Leads, Companies, Opportunities, OutreachSteps) via Compose — low-code, no service deploy.

## 4. Architecture — tooling by service/lib

```
┌──────────────── mcp-gtm (NEW, services/loom-core/cmd/mcp-gtm) ────────────────┐
│  Orchestration + agent-facing tools. Drives Corteza REST API; calls            │
│  tavily / flexinfer / google-workspace. Confirm-to-send gate on outreach.      │
└──────┬───────────────────┬──────────────────┬───────────────────┬─────────────┘
       │                   │                  │                   │
 ┌─────▼──────┐    ┌───────▼──────┐   ┌───────▼──────┐    ┌───────▼────────┐
 │  Corteza   │    │  mcp-tavily  │   │  FlexInfer   │    │ mcp-google-    │
 │  CRM       │    │ (enrichment) │   │ (draft/score │    │ workspace      │
 │ (records,  │    └──────────────┘   │  /fine-tune) │    │ (send + book)  │
 │  workflow, │                       └──────────────┘    └────────────────┘
 │  reporter, │
 │  namespaces│   gtm-leadgen-loop skill (loom-core skills-registry) drives the
 │  =tenants) │   daily loop; per-tenant cron = "managed runtime". Corteza
 └────────────┘   Workflows handle native routing/stage-transition/reminders.
```

### 4.1 Corteza CRM — pipeline-of-record (config, not build) · `platform/gitops/k3s/crm`
Already deployed and idle. Work is **configuration + a service account**, not a service build:
- **Namespace = tenant.** One Corteza Compose namespace per engagement. Dogfood tenant = "flexinfer-self". Multi-tenancy is native.
- **Modules (reuse CRM app if imported, else create via Compose):** `Lead` (name, email, title, company, source, icp_score, status), `Company` (name, domain, industry, size_band, enrichment), `Opportunity` (stage/pipeline, amount, close target), `OutreachStep` (lead ref, channel=email, sequence_step, status drafted/approved/sent/replied, body, sent_at). Add custom fields `icp_score`, `source`, `last_touch` as needed.
- **Pipeline stages:** Corteza Opportunity pipeline = new → qualified → contacted → replied → meeting_booked → won/lost.
- **Workflow engine (native automation):** lead routing, stage transitions on reply, follow-up reminders, Slack/email notifications — author as Corteza Workflows where they fit, reducing what mcp-gtm/skill must orchestrate.
- **Reporter:** funnel + per-tenant pipeline dashboards = the recurring-revenue value artifact (no custom dashboard build).
- **Access:** create a Corteza service user + API credential (JWT); store via SOPS in `k3s/crm/secrets.yaml`. The REST API has two surfaces — System API (auth/users) and Compose API (records/modules/namespaces).
- **Metering:** derive billable units (sourced/sent/booked) from Corteza record activity, surfaced via Reporter; optionally a `UsageEvent` module if record-activity rollups are insufficient.

### 4.2 `services/loom-core` → new `cmd/mcp-gtm` server + `gtm-leadgen-loop` skill
Scaffold via `mcpscaffold.NewServer` (`pkg/mcpscaffold/scaffold.go:41`). Register in `mcp/context/registry.yaml` + `Makefile` `MCP_SERVERS`; `make servers && loom sync claude --regen`.

**Tools (mirror jobsearch precedent + orchestration; Corteza is the backend):**
- ICP: `gtm_icp_define`, `gtm_icp_get` (stored as a Corteza record or workspace config)
- Sourcing/enrichment: `gtm_source_prospects` (Tavily → candidates), `gtm_enrich_lead` (Tavily extract + FlexInfer summarize), `gtm_score_lead` (FlexInfer/rules → `icp_score`)
- Pipeline CRUD (Corteza Compose API wrappers): `gtm_lead_list/get/create/update`, `gtm_lead_transition`, `gtm_opportunity_*`
- Outreach: `gtm_outreach_draft` (FlexInfer personalized), `gtm_outreach_approve`, `gtm_outreach_send` (**confirm=true gate**, via `google_workspace_gmail_send_message`; writes OutreachStep to Corteza)
- Cadence/booking: `gtm_followup_due`, `gtm_book_call` (google-workspace calendar)
- Reporting/metering: `gtm_pipeline_funnel`, `gtm_usage_report` (read Corteza/Reporter)

**Skill `gtm-leadgen-loop`** (skills-registry.yaml): source → enrich → score → upsert-to-Corteza → draft → queue-for-approval → send-approved → track-replies → book-calls → update-pipeline. Per-tenant cron (Loom daemon) = the **managed runtime**. Native Corteza Workflows handle in-CRM reactions.

**Deployment placement (decided by probe):** run `mcp-gtm` **in-cluster** in mcp-hub (model: `k3s/mcp-hub/servers/jobsearch/`) so it reaches Corteza at `http://corteza.crm.svc.cluster.local` and bypasses the Cloudflare Access gate on the public hostname. If a local/loomd deployment is ever needed, provision a CF Access service token instead.

### 4.3 `services/flexinfer` → serving lanes (mostly config)
- **MVP:** alias an existing instruct lane as `gtm-outreach` (draft/score) + reuse the live `bge` embedding lane for lookalike. No new infra.
- **Slice 5 (moat):** conversion-feedback LoRA fine-tuned on replied/booked outcomes via the existing Finetune CRD + GPU-lease scheduler. "Our agents learn your winning messaging."

### 4.4 libs
- **No new cross-language lib for MVP.** Corteza is the system-of-record (its module schema is the source-of-truth); `mcp-gtm` (Go) talks to it via REST.
- **Flag (defer):** `libs/gtm-schema` only if a third consumer appears.

## 5. Recurring-revenue design (day-one hooks)
- **Multi-tenant native:** one Corteza namespace per engagement; ICP/pipeline/metering tenant-scoped from Slice 1.
- **Dashboards:** Corteza Reporter → per-tenant funnel + pipeline = the recurring deliverable, no custom build.
- **Metering:** record-activity rollups (sourced/sent/booked) → billable units; supports usage-based or flat retainers.
- **Managed runtime:** per-tenant cron running `gtm-leadgen-loop` + native Corteza Workflows — the thing a retainer pays for.

## 6. Non-goals (MVP)
- LinkedIn / non-email channels (ToS risk) — email only.
- Fully autonomous send with no human in the loop (deliverability/spam risk — send stays gated).
- A bespoke sales UI — use Corteza's UI + Reporter.
- Selling raw GPU capacity / SLA-bound inference (highest-regret per brainstorm tension #7).
- Migrating ICC or co-mingling any existing ICC/work data — out of scope entirely now.

## 7. Risks & mitigations
| Risk | Mitigation |
|---|---|
| Automated outreach = spam / deliverability damage | Human-gated send (`confirm=true`); low volume; warm domain; Slice 0 proves response first |
| Kill-test fails | It's Slice 0 and gates everything — fail cheap in 2 weeks, pivot to content-inbound (Combination B) |
| Corteza CRM app not pre-imported / API friction | Slice 1 verifies; fallback = build lean GTM namespace via Compose (low-code, no deploy) |
| Corteza version/API drift (2024.9.7) | Pin image (already pinned); smoke-test Compose API round-trip in Slice 1 |
| Per-tenant ops burden (consulting trap) | Native Corteza Workflows + managed-runtime automation + Reporter dashboards |
| LLM-personalized outreach reads generic | Slice 5 conversion-feedback LoRA; ICP-specific templates; enrichment depth |

## 8. Open decisions (non-blocking)
- ICP scoring rules-based vs LLM-based for MVP — start rules + LLM-assist; measure.
- Billing mechanics (usage-based vs flat retainer) — Corteza metering supports both; decide post-validation.
- Whether to use Corteza's shipped CRM namespace vs a purpose-built lean GTM namespace — resolve in Slice 1.

## 9. Handoff
Implementation plan: [30-implementation-plan-gtm-engine.md](30-implementation-plan-gtm-engine.md). Slice 0 (kill-test) gates all subsequent slices.
