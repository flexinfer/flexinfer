-- +goose Up
-- +goose StatementBegin

-- v2 — Hierarchical Swarm substrate. See .loom/93-product-spec-mills-v2-…md.
-- Append-only migration; nothing in 001 changes except pipeline_runs gains
-- two columns (parent_run_id, depth) for bounded recursion.

-- Squads: configuration mirror of platform/gitops/k3s/mills/squads/<name>.yaml.
-- The loader reflects each manifest into a row on boot + on fsnotify change.
CREATE TABLE squads (
    name              TEXT PRIMARY KEY,
    paths_json        TEXT NOT NULL DEFAULT '[]',  -- glob patterns
    tests_json        TEXT NOT NULL DEFAULT '[]',  -- devbox quality_gate lanes
    gates_json        TEXT NOT NULL DEFAULT '{}',  -- {required:[…], advisory:[…]}
    ensemble_json     TEXT NOT NULL DEFAULT '{}',  -- editor / reviewers / judge
    budget_share      REAL NOT NULL DEFAULT 0,     -- fraction of daily pipeline budget
    recursion_enabled INTEGER NOT NULL DEFAULT 0,  -- bool 0/1
    enabled           INTEGER NOT NULL DEFAULT 1,
    last_loaded_sha   TEXT,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

-- Squad working memory: append-on-merge, prune-by-importance weekly.
CREATE TABLE squad_memory (
    id              INTEGER PRIMARY KEY,
    squad_name      TEXT NOT NULL REFERENCES squads(name) ON DELETE CASCADE,
    kind            TEXT NOT NULL,                 -- merge | tech_debt | convention | followup
    title           TEXT NOT NULL,
    body            TEXT NOT NULL,                 -- markdown
    refs_json       TEXT NOT NULL DEFAULT '[]',    -- file:line + commit refs
    importance      REAL NOT NULL DEFAULT 0.5,     -- 0..1
    created_at      TEXT NOT NULL,
    last_seen_at    TEXT NOT NULL,
    UNIQUE(squad_name, kind, title)
);
CREATE INDEX idx_squad_memory_lookup
    ON squad_memory(squad_name, kind, importance);

-- Squad outcomes: rolling success rate per (squad, path_class, gate set).
CREATE TABLE squad_outcomes (
    id              INTEGER PRIMARY KEY,
    squad_name      TEXT NOT NULL REFERENCES squads(name) ON DELETE CASCADE,
    path_class      TEXT NOT NULL,                 -- glob/path representative
    pipeline_run_id TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    outcome         TEXT NOT NULL,                 -- merged_clean | merged_regressed | failed | self_vetoed
    cost_usd        REAL NOT NULL DEFAULT 0,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL
);
CREATE INDEX idx_squad_outcomes_lookup
    ON squad_outcomes(squad_name, path_class, created_at);
CREATE UNIQUE INDEX idx_squad_outcomes_run
    ON squad_outcomes(pipeline_run_id);

-- Audit findings: independent adversarial scores. Subject is either a
-- council artifact or a pipeline merge; the auditor pool is captured
-- per row so policy rotations are auditable.
CREATE TABLE audit_findings (
    id              INTEGER PRIMARY KEY,
    subject_kind    TEXT NOT NULL,                 -- council_artifact | pipeline_merge
    subject_id      TEXT NOT NULL,
    severity        TEXT NOT NULL,                 -- info | warn | critical
    rubric_id       TEXT NOT NULL,                 -- e.g. audit_v1
    survival_score  REAL NOT NULL,                 -- 0..1
    findings_json   TEXT NOT NULL DEFAULT '[]',
    auditor_pool    TEXT NOT NULL DEFAULT '[]',    -- JSON of model selectors that ran
    cost_usd        REAL NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL
);
CREATE INDEX idx_audit_findings_lookup
    ON audit_findings(subject_kind, subject_id);
CREATE INDEX idx_audit_findings_recent
    ON audit_findings(created_at);

-- Cross-repo run state: spans 2+ repos with atomic merge semantics.
CREATE TABLE cross_repo_runs (
    id                  TEXT PRIMARY KEY,
    backlog_item_id     TEXT NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
    repos_json          TEXT NOT NULL DEFAULT '[]',
    state               TEXT NOT NULL,             -- planning | open | gates_green | merging | merged | reverted | failed
    atomicity_strategy  TEXT NOT NULL DEFAULT 'all_or_revert',
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
CREATE INDEX idx_cross_repo_state ON cross_repo_runs(state);
CREATE INDEX idx_cross_repo_backlog ON cross_repo_runs(backlog_item_id);

-- Council debate transcripts: per-round summaries and artifact deltas.
CREATE TABLE council_debate_rounds (
    id                  INTEGER PRIMARY KEY,
    council_run_id      TEXT NOT NULL REFERENCES council_runs(id) ON DELETE CASCADE,
    round_index         INTEGER NOT NULL,
    role                TEXT NOT NULL,             -- editor_proposes | reviewer_critiques | moderator_decision | editor_revises
    cost_usd            REAL NOT NULL DEFAULT 0,
    summary             TEXT NOT NULL,
    artifact_deltas_json TEXT NOT NULL DEFAULT '[]',
    created_at          TEXT NOT NULL
);
CREATE INDEX idx_debate_run ON council_debate_rounds(council_run_id);

-- Adaptive policy proposals: weekly job emits relax/tighten/rotate suggestions
-- with rationale citing kpi_snapshots / eval_scores / audit_findings / gate_outcomes.
-- The .loom/mills/policy_proposals/<date>.md is the human-facing copy; rows here
-- track lifecycle (pending → applied | rejected | reverted).
CREATE TABLE policy_proposals (
    id              INTEGER PRIMARY KEY,
    proposal_date   TEXT NOT NULL,                 -- YYYY-MM-DD
    kind            TEXT NOT NULL,                 -- relax | tighten | rotate_ensemble
    target          TEXT NOT NULL,                 -- e.g. gates.coverage, squads.hud-frontend.ensemble.editor
    diff            TEXT NOT NULL,                 -- yaml/text diff
    rationale       TEXT NOT NULL,                 -- markdown
    state           TEXT NOT NULL,                 -- pending | applied_human | applied_auto | rejected | reverted
    applied_at      TEXT,
    revert_deadline TEXT,
    created_at      TEXT NOT NULL
);
CREATE INDEX idx_policy_proposal_state ON policy_proposals(state);
CREATE INDEX idx_policy_proposal_date  ON policy_proposals(proposal_date);

-- Pipeline recursion: parent chain + depth. Existing rows backfill to depth=0
-- with NULL parent (top-level runs). The dispatcher refuses subrun creation
-- when (parent.depth + 1) > policy.pipeline.max_recursion_depth.
ALTER TABLE pipeline_runs ADD COLUMN parent_run_id TEXT
    REFERENCES pipeline_runs(id) ON DELETE SET NULL;
ALTER TABLE pipeline_runs ADD COLUMN depth INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_pipeline_runs_parent ON pipeline_runs(parent_run_id);
CREATE INDEX idx_pipeline_runs_depth  ON pipeline_runs(depth);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idx_pipeline_runs_depth;
DROP INDEX IF EXISTS idx_pipeline_runs_parent;
-- SQLite has no direct DROP COLUMN before 3.35; we keep these forward-only.
-- ALTER TABLE pipeline_runs DROP COLUMN depth;
-- ALTER TABLE pipeline_runs DROP COLUMN parent_run_id;
DROP TABLE IF EXISTS policy_proposals;
DROP TABLE IF EXISTS council_debate_rounds;
DROP TABLE IF EXISTS cross_repo_runs;
DROP TABLE IF EXISTS audit_findings;
DROP TABLE IF EXISTS squad_outcomes;
DROP TABLE IF EXISTS squad_memory;
DROP TABLE IF EXISTS squads;
-- +goose StatementEnd
