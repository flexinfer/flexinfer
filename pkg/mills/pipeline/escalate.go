package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// FailureRecord summarises why a pipeline run was escalated. It is the
// payload of the human-facing GitLab issue + agent handoff.
type FailureRecord struct {
	BacklogID     string              `json:"backlog_id"`
	PipelineRunID string              `json:"pipeline_run_id"`
	Reason        string              `json:"reason"`
	State         store.PipelineState `json:"state"`
	CostUSD       float64             `json:"cost_usd"`
	Attempts      int                 `json:"attempts"`
	StageStack    []FailureStage      `json:"stage_stack"`
	GateVerdicts  []FailureGate       `json:"gate_verdicts"`
	LastLogTail   string              `json:"last_log_tail,omitempty"`
	GeneratedAt   time.Time           `json:"generated_at"`
}

// FailureStage is one row of the per-stage history attached to a record.
type FailureStage struct {
	Stage    string  `json:"stage"`
	Attempt  int     `json:"attempt"`
	Outcome  string  `json:"outcome,omitempty"`
	CostUSD  float64 `json:"cost_usd"`
	Duration string  `json:"duration,omitempty"`
}

// FailureGate is one row of the gate-verdict history.
type FailureGate struct {
	Gate       string   `json:"gate"`
	AfterStage string   `json:"after_stage"`
	Outcome    string   `json:"outcome"`
	Reasons    []string `json:"reasons,omitempty"`
}

// IssueClient opens a GitLab issue for the failure record. Implementations
// wrap mcp-gitlab.create_issue; tests inject a fake.
type IssueClient interface {
	CreateIssue(ctx context.Context, req IssueRequest) (IssueResponse, error)
}

// IssueRequest carries the issue title + description + labels.
type IssueRequest struct {
	BacklogID   string
	Title       string
	Description string
	Labels      []string
}

// IssueResponse reports the new issue URL + iid.
type IssueResponse struct {
	IID int64
	URL string
}

// HandoffClient creates an agent_handoff record. Production wraps
// mcp-agent-context's agent_handoff_create.
type HandoffClient interface {
	CreateHandoff(ctx context.Context, req HandoffRequest) (HandoffResponse, error)
}

// HandoffRequest is the bundle we send to agent_handoff_create.
type HandoffRequest struct {
	From        string         // "loom-mills-operator"
	To          string         // e.g. "human-on-call"
	Reason      string         // human-readable summary
	Context     map[string]any // structured failure record + run links
	BacklogID   string
	PipelineRun string
	IssueURL    string
}

// HandoffResponse reports the handoff id for audit.
type HandoffResponse struct {
	HandoffID string
}

// EscalationHandler is the contract Runner + Integrator call after they
// transition a run to PipelineEscalated. The handler should be best-effort
// (an issue-creation failure must not undo the escalated state).
type EscalationHandler interface {
	Handle(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem, reason string) error
}

// Escalator builds a failure record and posts the human-handoff
// artifacts. It is wired onto Runner and Integrator by the operator at
// startup; absence of an Escalator falls back to the bare state
// transition the runner already does.
type Escalator struct {
	Store   *store.Store
	Issue   IssueClient
	Handoff HandoffClient
	Project string // GitLab project slug or numeric id; recorded in issue labels
	HandTo  string // e.g. "human-on-call"; default "human"
	Logger  *slog.Logger
	Clock   func() time.Time
	// LogTailMaxLines caps how many trailing lines we attach. Default 200.
	LogTailMaxLines int
}

// NewEscalator constructs an Escalator with sensible defaults.
func NewEscalator(s *store.Store, issue IssueClient, handoff HandoffClient) *Escalator {
	return &Escalator{
		Store:           s,
		Issue:           issue,
		Handoff:         handoff,
		Logger:          slog.Default(),
		Clock:           time.Now,
		LogTailMaxLines: 200,
		HandTo:          "human",
	}
}

// Handle satisfies EscalationHandler.
func (e *Escalator) Handle(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem, reason string) error {
	if e == nil || e.Store == nil {
		return errors.New("escalator: not configured")
	}
	rec, err := e.BuildRecord(ctx, run, item, reason)
	if err != nil {
		return fmt.Errorf("build failure record: %w", err)
	}

	issueURL := ""
	if e.Issue != nil {
		title := fmt.Sprintf("[mills] %s escalated: %s", run.ID, truncate(reason, 80))
		req := IssueRequest{
			BacklogID:   item.ID,
			Title:       title,
			Description: e.renderIssueBody(rec),
			Labels:      []string{"mills-escalation", "kind/incident", "priority/" + string(item.Priority)},
		}
		resp, ierr := e.Issue.CreateIssue(ctx, req)
		if ierr != nil {
			e.logger().Warn("escalator: create issue failed", "error", ierr, "run", run.ID)
		} else {
			issueURL = resp.URL
			mills.EscalationIssueCreatedTotal.Inc()
		}
	}

	if e.Handoff != nil {
		hto := e.HandTo
		if hto == "" {
			hto = "human"
		}
		req := HandoffRequest{
			From:        "loom-mills-operator",
			To:          hto,
			Reason:      reason,
			Context:     map[string]any{"failure_record": rec, "issue_url": issueURL},
			BacklogID:   item.ID,
			PipelineRun: run.ID,
			IssueURL:    issueURL,
		}
		if _, herr := e.Handoff.CreateHandoff(ctx, req); herr != nil {
			e.logger().Warn("escalator: create handoff failed", "error", herr, "run", run.ID)
		} else {
			mills.EscalationHandoffCreatedTotal.Inc()
		}
	}

	if e.Store.Events != nil {
		_ = e.Store.Events.Append(ctx, &store.Event{
			Actor: "escalator",
			Kind:  "pipeline.escalation.published",
			Payload: map[string]any{
				"run":       run.ID,
				"backlog":   item.ID,
				"issue_url": issueURL,
				"reason":    reason,
				"outcome":   "ok",
			},
		})
	}
	return nil
}

// BuildRecord assembles a FailureRecord from store rows. Exported for
// tests + the HUD's escalation drawer (slice 5.x).
func (e *Escalator) BuildRecord(ctx context.Context, run *store.PipelineRun, item *store.BacklogItem, reason string) (*FailureRecord, error) {
	stages, err := e.Store.Pipeline.ListStages(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	gates, err := e.Store.Pipeline.ListGates(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	rec := &FailureRecord{
		BacklogID:     item.ID,
		PipelineRunID: run.ID,
		Reason:        reason,
		State:         run.State,
		CostUSD:       run.CostUSD,
		Attempts:      run.Attempts,
		GeneratedAt:   e.now(),
	}
	for _, sr := range stages {
		fs := FailureStage{
			Stage:   sr.Stage,
			Attempt: sr.Attempt,
			CostUSD: sr.CostUSD,
		}
		if sr.Outcome != nil {
			fs.Outcome = string(*sr.Outcome)
		}
		if sr.EndedAt != nil && !sr.EndedAt.IsZero() {
			fs.Duration = sr.EndedAt.Sub(sr.StartedAt).Round(time.Millisecond).String()
		}
		rec.StageStack = append(rec.StageStack, fs)
	}
	for _, g := range gates {
		rec.GateVerdicts = append(rec.GateVerdicts, FailureGate{
			Gate:       g.GateName,
			AfterStage: g.AfterStage,
			Outcome:    string(g.Outcome),
			Reasons:    g.Reasons,
		})
	}
	rec.LastLogTail = e.lastLogTail(stages)
	return rec, nil
}

// lastLogTail returns the trailing N lines from the most recent stage
// result that has a LogTail. The runner truncates upstream so this is
// usually a no-op cap.
func (e *Escalator) lastLogTail(stages []*store.StageResult) string {
	if len(stages) == 0 {
		return ""
	}
	for i := len(stages) - 1; i >= 0; i-- {
		if stages[i].LogTail == "" {
			continue
		}
		return capLines(stages[i].LogTail, e.LogTailMaxLines)
	}
	return ""
}

func capLines(s string, max int) string {
	if max <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= max {
		return s
	}
	return strings.Join(lines[len(lines)-max:], "\n")
}

func truncate(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// renderIssueBody is a tiny markdown renderer for the GitLab issue body.
func (e *Escalator) renderIssueBody(rec *FailureRecord) string {
	var b strings.Builder
	b.WriteString("## Pipeline Escalation\n\n")
	fmt.Fprintf(&b, "- **Backlog item**: `%s`\n", rec.BacklogID)
	fmt.Fprintf(&b, "- **Pipeline run**: `%s`\n", rec.PipelineRunID)
	fmt.Fprintf(&b, "- **State**: `%s`\n", rec.State)
	fmt.Fprintf(&b, "- **Cost so far**: $%.2f\n", rec.CostUSD)
	fmt.Fprintf(&b, "- **Reason**: %s\n\n", rec.Reason)

	b.WriteString("### Stage history\n\n")
	b.WriteString("| Stage | Attempt | Outcome | Cost | Duration |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, s := range rec.StageStack {
		fmt.Fprintf(&b, "| `%s` | %d | %s | $%.2f | %s |\n",
			s.Stage, s.Attempt, s.Outcome, s.CostUSD, s.Duration)
	}

	if len(rec.GateVerdicts) > 0 {
		b.WriteString("\n### Gate verdicts\n\n")
		for _, g := range rec.GateVerdicts {
			fmt.Fprintf(&b, "- `%s` after `%s` → **%s**", g.Gate, g.AfterStage, g.Outcome)
			if len(g.Reasons) > 0 {
				fmt.Fprintf(&b, ": %s", strings.Join(g.Reasons, "; "))
			}
			b.WriteString("\n")
		}
	}

	if rec.LastLogTail != "" {
		b.WriteString("\n### Last log tail\n\n```\n")
		b.WriteString(rec.LastLogTail)
		b.WriteString("\n```\n")
	}
	return b.String()
}

func (e *Escalator) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

func (e *Escalator) logger() *slog.Logger {
	if e.Logger != nil {
		return e.Logger
	}
	return slog.Default()
}
