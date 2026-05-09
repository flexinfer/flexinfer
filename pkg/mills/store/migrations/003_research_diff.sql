-- 003_research_diff.sql — adds the research_diff column to pipeline_runs.
--
-- Used by ResearchModeShadow in pkg/mills/clients/flexinfer.go: every
-- shadow-mode research call captures a structured JSON object (legacy
-- vs. delegated path: char counts, costs, length-delta-percent,
-- per-side errors) so we can compare quality before flipping
-- MILLS_RESEARCH_VIA_WEAVER from "shadow" to "on".
--
-- Spec: services/loom-core/.loom/111-product-spec-weaver-qwen3-
-- integration-2026-05-08.md (MW-003).
--
-- Append-only: nothing in 001/002 changes. The column is nullable so
-- pipeline runs created before this migration (or runs with
-- MILLS_RESEARCH_VIA_WEAVER unset/off) leave it empty.

ALTER TABLE pipeline_runs ADD COLUMN research_diff TEXT;

-- Optional partial index for diff dashboards. Only rows with a non-
-- null diff land here, so the index stays cheap when shadow mode is
-- off.
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_research_diff
  ON pipeline_runs(id)
  WHERE research_diff IS NOT NULL;

-- ----- Reverse (manual) -----
-- SQLite has no DROP COLUMN before 3.35; restore the table by
-- recreating without the column. Reverse is only needed if a soak
-- exposes a destabilizing problem and we want to roll forward by
-- removing the column entirely.
-- ALTER TABLE pipeline_runs DROP COLUMN research_diff;
-- DROP INDEX IF EXISTS idx_pipeline_runs_research_diff;
