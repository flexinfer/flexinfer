# Rename: `hive` → `mills` (final naming map)

**Date:** 2026-05-04
**Branch (loom-core):** `refactor/hive-to-mills`
**Branch (platform/gitops):** `refactor/hive-to-mills` (companion, separate MR)
**Status:** authoritative — pin this for downstream agents (e.g. `millsTransport.ts`)

## Case rules

| From | To | Used in |
|---|---|---|
| `hive` | `mills` | package names, kebab-case, snake_case identifiers |
| `Hive` | `Mills` | exported Go types, constructors, PascalCase symbols |
| `HIVE` | `MILLS` | env var prefixes, SCREAMING_SNAKE constants |
| `loom-hive` | `loom-mills` | namespace, image, secret prefixes, K8s app labels |
| `loom_hive` | `loom_mills` | Prometheus metric prefix |

Substring `hive` inside `archive`/`Archive`/`CHIVE` is **excluded** via word boundaries.

## Public-surface contract (all flip in one loom-core MR)

### REST paths (~30)

`/api/hive/*` → `/api/mills/*`

Includes: `status`, `kpis`, `cost-preview`, `audit/findings`, `audit/run`, `backlog`, `backlog/sync`, `council/runs`, `council/run`, `council/dryrun`, `cross-repo/runs`, `cross-repo/runs/{id}/abort`, `eval/run-cross`, `eval/scores`, `pipeline/runs`, `policy`, `policy/proposals`, `alerts/regression`.

### Env vars (18)

| Old | New |
|---|---|
| `LOOM_HIVE_ADMIN_TOKEN` | `LOOM_MILLS_ADMIN_TOKEN` |
| `LOOM_HIVE_BACKLOG_ID` | `LOOM_MILLS_BACKLOG_ID` |
| `LOOM_HIVE_DB_PATH` | `LOOM_MILLS_DB_PATH` |
| `LOOM_HIVE_DEBUG` | `LOOM_MILLS_DEBUG` |
| `LOOM_HIVE_ENABLED` | `LOOM_MILLS_ENABLED` |
| `LOOM_HIVE_HTTP_ADDR` | `LOOM_MILLS_HTTP_ADDR` |
| `LOOM_HIVE_METRICS_ADDR` | `LOOM_MILLS_METRICS_ADDR` |
| `LOOM_HIVE_NS` | `LOOM_MILLS_NS` |
| `LOOM_HIVE_OPERATOR_IMAGE` | `LOOM_MILLS_OPERATOR_IMAGE` |
| `LOOM_HIVE_OPERATOR_TOKEN` | `LOOM_MILLS_OPERATOR_TOKEN` |
| `LOOM_HIVE_OPERATOR_URL` | `LOOM_MILLS_OPERATOR_URL` |
| `LOOM_HIVE_POLICY_PATH` | `LOOM_MILLS_POLICY_PATH` |
| `LOOM_HIVE_REPO_ROOT` | `LOOM_MILLS_REPO_ROOT` |
| `LOOM_HIVE_RUN_ID` | `LOOM_MILLS_RUN_ID` |
| `LOOM_HIVE_SQUADS_PATH` | `LOOM_MILLS_SQUADS_PATH` |
| `LOOM_HIVE_STAGE` | `LOOM_MILLS_STAGE` |
| `LOOM_HIVE_TOKEN` | `LOOM_MILLS_TOKEN` |
| `LOOM_HIVE_WORKTREE` | `LOOM_MILLS_WORKTREE` |

### Prometheus metrics (28)

All `loom_hive_*` → `loom_mills_*`. Full list:

```
loom_hive_audit_cost_usd_total
loom_hive_audit_findings_total
loom_hive_audit_survival_rate
loom_hive_backlog_id
loom_hive_backlog_items_total
loom_hive_budget
loom_hive_budget_remaining_usd
loom_hive_council_artifacts_total
loom_hive_council_cost_usd_total
loom_hive_council_debate_cost_usd_total
loom_hive_council_debate_rounds_total
loom_hive_council_runs_total
loom_hive_cross_repo_atomicity_violations_total
loom_hive_cross_repo_runs_total
loom_hive_gitlab_sync_lag_seconds
loom_hive_merge_to_main_total
loom_hive_pipeline_cost_usd_total
loom_hive_pipeline_gate_decisions_total
loom_hive_pipeline_recursion_depth_histogram
loom_hive_pipeline_runs_total
loom_hive_pipeline_stage_duration_seconds
loom_hive_policy_proposals_total
loom_hive_regression
loom_hive_regression_count_total
loom_hive_squad_budget_usd_total
loom_hive_squad_runs_total
loom_hive_squad_success_rate
loom_hive_stage
```

### CLI

`loom hive ...` → `loom mills ...` (hard cut, no deprecation alias)

Subcommands preserved: `status`, `council`, `crossrepo`, `pipelines`, `squads`.

### MCP skill

`mcp/skills/hive-ops/` → `mcp/skills/mills-ops/`

### Go packages

- `github.com/crb2nu/loom/pkg/hive` (and 12 subpackages: `clients`, `pipeline`, `council`, `store`, `squads`, `eval`, `audit`, `crossrepo`, `gates`, `adaptive`, `runner`, `budget`) → `.../pkg/mills/...`
- `github.com/crb2nu/loom/internal/hud/domain/hive` → `.../internal/hud/domain/mills`

### Operator

- Binary: `loom-hive-operator` → `loom-mills-operator`
- Source: `cmd/loom-hive-operator/` → `cmd/loom-mills-operator/`
- Dockerfile: `Dockerfile.loom-hive-operator` → `Dockerfile.loom-mills-operator`
- Image: `registry.harbor.lan/mcp/loom-hive-operator` → `registry.harbor.lan/mcp/loom-mills-operator`
- Old image stays in registry untouched; new image built from this MR's SHA.

## Lockstep — platform/gitops (separate MR)

Resources renamed in companion `refactor/hive-to-mills` MR (lands within minutes of loom-core MR):

| Old | New |
|---|---|
| Namespace `loom-hive` | `loom-mills` |
| ConfigMap `loom-hive-policy` | `loom-mills-policy` |
| ConfigMap `loom-hive-squads` | `loom-mills-squads` |
| ConfigMap `loom-hive-repos` | `loom-mills-repos` |
| Secret `loom-hive-gitlab` | `loom-mills-gitlab` |
| Secret `loom-hive-hud` | `loom-mills-hud` |
| PVC `loom-hive-state` | `loom-mills-state` (drain → snapshot → restore) |
| Mount path `/etc/loom-hive/repos` | `/etc/loom-mills/repos` |
| Grafana dashboard `services-loom-hive-dashboard` | `services-loom-mills-dashboard` |
| Prometheus alerts referencing `loom_hive_*` | rewritten to `loom_mills_*` |
| Runbook `loom-hive-operator.md` | `loom-mills-operator.md` |

## Docs treatment

In-place rewrite:
- `docs/HIVE.md` → `docs/MILLS.md`
- `docs/HIVE_RUNBOOK.md` → `docs/MILLS_RUNBOOK.md`
- `docs/HIVE_V2_ROLLBACK.md` → `docs/MILLS_V2_ROLLBACK.md` (keep `V2` versioning — refers to architecture phase, not name)
- `.loom/9*-{research,product-spec,implementation-plan,implementation-status}-hive-v2-...` rewritten in place; filenames updated
- `CHANGELOG.md` historical entries: rewrite identifiers/paths to current names (commit-level history is preserved by git)

## Out of scope

- Old image tags in registry (left as-is)
- External consumers' scripts (CLI is hard-cut; downstream owners notified separately)
- iOS app store builds older than this release (companion app rebuilt and resubmitted)
