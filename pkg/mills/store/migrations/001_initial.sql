-- +goose Up
-- +goose StatementBegin

-- Source of intent (extracted from ROADMAP.md).
CREATE TABLE roadmap_intents (
    id                          INTEGER PRIMARY KEY,
    theme                       TEXT NOT NULL,
    priority                    INTEGER NOT NULL,
    summary                     TEXT NOT NULL,
    constraints_json            TEXT NOT NULL DEFAULT '{}',
    last_seen_in_roadmap_sha    TEXT,
    created_at                  TEXT NOT NULL,
    updated_at                  TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_roadmap_theme_summary
    ON roadmap_intents(theme, summary);

-- Council runs.
CREATE TABLE council_runs (
    id                          TEXT PRIMARY KEY,
    trigger                     TEXT NOT NULL,
    started_at                  TEXT NOT NULL,
    ended_at                    TEXT,
    outcome                     TEXT NOT NULL,
    cost_frontier_usd           REAL NOT NULL DEFAULT 0,
    cost_local_usd              REAL NOT NULL DEFAULT 0,
    artifacts_json              TEXT NOT NULL DEFAULT '[]',
    backlog_deltas_json         TEXT NOT NULL DEFAULT '{}',
    sidecar_json                TEXT NOT NULL DEFAULT '{}',
    branch_name                 TEXT,
    commit_sha                  TEXT,
    notes                       TEXT
);
CREATE INDEX idx_council_started ON council_runs(started_at);

-- Backlog items (canonical).
CREATE TABLE backlog_items (
    id                          TEXT PRIMARY KEY,
    gitlab_issue_iid            INTEGER,
    title                       TEXT NOT NULL,
    labels_json                 TEXT NOT NULL DEFAULT '[]',
    state                       TEXT NOT NULL,
    priority                    TEXT NOT NULL,
    spec_doc                    TEXT,
    spec_anchor                 TEXT,
    success_json                TEXT NOT NULL DEFAULT '{}',
    budget_json                 TEXT NOT NULL DEFAULT '{}',
    policy_json                 TEXT NOT NULL DEFAULT '{}',
    slices_json                 TEXT NOT NULL DEFAULT '[]',
    dependencies_json           TEXT NOT NULL DEFAULT '[]',
    council_run_id              TEXT REFERENCES council_runs(id) ON DELETE SET NULL,
    created_by                  TEXT NOT NULL,
    created_at                  TEXT NOT NULL,
    updated_at                  TEXT NOT NULL
);
CREATE INDEX idx_backlog_state    ON backlog_items(state);
CREATE INDEX idx_backlog_council  ON backlog_items(council_run_id);
CREATE INDEX idx_backlog_gitlab   ON backlog_items(gitlab_issue_iid);

-- Pipeline runs.
CREATE TABLE pipeline_runs (
    id                          TEXT PRIMARY KEY,
    backlog_id                  TEXT NOT NULL REFERENCES backlog_items(id) ON DELETE CASCADE,
    template                    TEXT NOT NULL,
    state                       TEXT NOT NULL,
    current_stage               TEXT,
    attempts                    INTEGER NOT NULL DEFAULT 0,
    worktree_path               TEXT,
    mr_iid                      INTEGER,
    started_at                  TEXT NOT NULL,
    ended_at                    TEXT,
    cost_usd                    REAL NOT NULL DEFAULT 0,
    parent_session_id           TEXT,
    UNIQUE(backlog_id, attempts)
);
CREATE INDEX idx_pipeline_state    ON pipeline_runs(state);
CREATE INDEX idx_pipeline_backlog  ON pipeline_runs(backlog_id);

-- Stage results (one per stage execution; idempotent on retry).
CREATE TABLE stage_results (
    id                          INTEGER PRIMARY KEY,
    pipeline_run_id             TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    stage                       TEXT NOT NULL,
    attempt                     INTEGER NOT NULL,
    started_at                  TEXT NOT NULL,
    ended_at                    TEXT,
    outcome                     TEXT,
    spawn_id                    TEXT,
    cost_usd                    REAL NOT NULL DEFAULT 0,
    artifacts_json              TEXT NOT NULL DEFAULT '{}',
    log_tail                    TEXT
);
CREATE INDEX idx_stage_pipeline ON stage_results(pipeline_run_id);
CREATE UNIQUE INDEX idx_stage_unique
    ON stage_results(pipeline_run_id, stage, attempt);

-- Gate decisions (one per gate evaluation).
CREATE TABLE gate_outcomes (
    id                          INTEGER PRIMARY KEY,
    pipeline_run_id             TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    after_stage                 TEXT NOT NULL,
    gate_name                   TEXT NOT NULL,
    outcome                     TEXT NOT NULL,
    reasons_json                TEXT NOT NULL DEFAULT '[]',
    judged_by                   TEXT NOT NULL,
    evaluated_at                TEXT NOT NULL
);
CREATE INDEX idx_gate_pipeline ON gate_outcomes(pipeline_run_id);

-- KPI snapshots (rolled-up).
CREATE TABLE kpi_snapshots (
    id                          INTEGER PRIMARY KEY,
    snapshot_at                 TEXT NOT NULL,
    window_seconds              INTEGER NOT NULL,
    metrics_json                TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_kpi_snapshot ON kpi_snapshots(snapshot_at);

-- Evaluation scores (council artifact + pipeline outcome eval).
CREATE TABLE eval_scores (
    id                          INTEGER PRIMARY KEY,
    subject_kind                TEXT NOT NULL,
    subject_id                  TEXT NOT NULL,
    rubric                      TEXT NOT NULL,
    score                       REAL NOT NULL,
    breakdown_json              TEXT NOT NULL DEFAULT '{}',
    judged_by                   TEXT NOT NULL,
    evaluated_at                TEXT NOT NULL,
    notes                       TEXT
);
CREATE INDEX idx_eval_subject ON eval_scores(subject_kind, subject_id);
CREATE INDEX idx_eval_evaluated ON eval_scores(evaluated_at);

-- Generic event log (audit + debug).
CREATE TABLE events (
    id                          INTEGER PRIMARY KEY,
    occurred_at                 TEXT NOT NULL,
    actor                       TEXT NOT NULL,
    kind                        TEXT NOT NULL,
    subject_kind                TEXT,
    subject_id                  TEXT,
    payload_json                TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_events_subject  ON events(subject_kind, subject_id);
CREATE INDEX idx_events_occurred ON events(occurred_at);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS eval_scores;
DROP TABLE IF EXISTS kpi_snapshots;
DROP TABLE IF EXISTS gate_outcomes;
DROP TABLE IF EXISTS stage_results;
DROP TABLE IF EXISTS pipeline_runs;
DROP TABLE IF EXISTS backlog_items;
DROP TABLE IF EXISTS council_runs;
DROP TABLE IF EXISTS roadmap_intents;
-- +goose StatementEnd
