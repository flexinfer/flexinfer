package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DebateDAO records per-round transcripts of Council Debate Mode runs.
// One council_run_id can have multiple rounds; each round can have multiple
// roles (editor_proposes, reviewer_critiques, moderator_decision,
// editor_revises). The (council_run_id, round_index, role) tuple is not
// unique because a round can include N reviewer_critiques (one per reviewer).
type DebateDAO struct {
	db *sql.DB
}

const debateColumns = `id, council_run_id, round_index, role, cost_usd,
		summary, artifact_deltas_json, created_at`

// AppendRound writes one debate round entry.
func (d *DebateDAO) AppendRound(ctx context.Context, r *CouncilDebateRound) error {
	if r == nil || r.CouncilRunID == "" {
		return errors.New("debate: CouncilRunID required")
	}
	if r.Role == "" {
		return errors.New("debate: Role required")
	}
	if r.RoundIndex < 0 {
		return errors.New("debate: RoundIndex must be >= 0")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	deltas, err := jsonField(r.ArtifactDeltas)
	if err != nil {
		return fmt.Errorf("artifact_deltas: %w", err)
	}
	res, err := d.db.ExecContext(ctx, `
		INSERT INTO council_debate_rounds (council_run_id, round_index, role,
			cost_usd, summary, artifact_deltas_json, created_at)
		VALUES (?,?,?,?,?,?,?)
	`,
		r.CouncilRunID, r.RoundIndex, string(r.Role),
		r.CostUSD, r.Summary, deltas, timeRFC3339(r.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("debate append: %w", err)
	}
	id, _ := res.LastInsertId()
	r.ID = id
	return nil
}

// ListByRun returns every debate round entry for a council run, ordered by
// round_index ASC, then id ASC (preserves emission order within a round).
func (d *DebateDAO) ListByRun(ctx context.Context, councilRunID string) ([]*CouncilDebateRound, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT `+debateColumns+` FROM council_debate_rounds
		WHERE council_run_id = ?
		ORDER BY round_index ASC, id ASC
	`, councilRunID)
	if err != nil {
		return nil, fmt.Errorf("debate list: %w", err)
	}
	defer rows.Close()
	var out []*CouncilDebateRound
	for rows.Next() {
		r, err := scanDebateRound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TotalCost sums cost_usd for all rounds of a council run. Used by the
// budget cap enforcer in Council Debate Mode.
func (d *DebateDAO) TotalCost(ctx context.Context, councilRunID string) (float64, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0) FROM council_debate_rounds
		WHERE council_run_id = ?
	`, councilRunID)
	var total float64
	if err := row.Scan(&total); err != nil {
		return 0, fmt.Errorf("debate sum-cost: %w", err)
	}
	return total, nil
}

// SumCostSince returns the total debate spend across all council runs
// whose rounds were recorded at-or-after the given timestamp. Mirrors
// CouncilDAO.SumCostSince + PipelineDAO.SumCostSince so the budget
// enforcer can ask "how much have we spent on debate today" without
// joining through council_runs (debate cost lives in the per-round
// rows; council_runs.cost_*_usd already aggregates it via the runner's
// post-debate stamp, but a direct read is useful for HUD telemetry +
// future debate-tier daily caps).
func (d *DebateDAO) SumCostSince(ctx context.Context, since time.Time) (float64, error) {
	row := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(cost_usd), 0) FROM council_debate_rounds
		WHERE created_at >= ?
	`, timeRFC3339(since))
	var total float64
	if err := row.Scan(&total); err != nil {
		return 0, fmt.Errorf("debate sum-cost-since: %w", err)
	}
	return total, nil
}

func scanDebateRound(s scanner) (*CouncilDebateRound, error) {
	var (
		r         CouncilDebateRound
		role      string
		deltas    string
		createdAt string
	)
	err := s.Scan(
		&r.ID, &r.CouncilRunID, &r.RoundIndex, &role,
		&r.CostUSD, &r.Summary, &deltas, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("debate scan: %w", err)
	}
	r.Role = DebateRole(role)
	if err := jsonInto(deltas, &r.ArtifactDeltas); err != nil {
		return nil, fmt.Errorf("artifact_deltas: %w", err)
	}
	if r.CreatedAt, err = parseTime(createdAt); err != nil {
		return nil, fmt.Errorf("created_at: %w", err)
	}
	return &r, nil
}
