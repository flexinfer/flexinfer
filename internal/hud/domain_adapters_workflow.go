// domain_adapters_workflow.go provides workflow domain adapters.
package hud

import (
	"log/slog"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/domain/workflow"
	"github.com/crb2nu/loom/internal/hud/monitor"
)

// --- Workflow domain Deps adapter ---

type workflowDepsAdapter struct {
	app *App
}

func (w *workflowDepsAdapter) WriteJSON(wr http.ResponseWriter, status int, v any) {
	w.app.WriteJSON(wr, status, v)
}

func (w *workflowDepsAdapter) WriteError(wr http.ResponseWriter, status int, msg string, err error) {
	w.app.WriteError(wr, status, msg, err)
}

func (w *workflowDepsAdapter) Logger() *slog.Logger { return w.app.Logger() }

func (w *workflowDepsAdapter) BroadcastAgentEvent(eventType string, payload any) {
	w.app.BroadcastAgentEvent(eventType, payload)
}

func (w *workflowDepsAdapter) WorkflowMonitor() workflow.WorkflowMonitorOps {
	return &workflowMonitorAdapter{mon: w.app.workflowMonitor}
}

// workflowMonitorAdapter converts between monitor types and workflow domain types.
type workflowMonitorAdapter struct {
	mon *monitor.WorkflowMonitor
}

func (a *workflowMonitorAdapter) Workflows() []workflow.WorkflowSummary {
	infos := a.mon.Workflows()
	out := make([]workflow.WorkflowSummary, len(infos))
	for i, wf := range infos {
		out[i] = workflow.WorkflowSummary{
			ID:          wf.ID,
			Name:        wf.Name,
			Status:      wf.Status,
			CurrentStep: wf.CurrentStep,
			CreatedAt:   wf.CreatedAt,
			Progress:    wf.Progress,
			Error:       wf.Error,
		}
	}
	return out
}

func (a *workflowMonitorAdapter) Detail(id string) (*workflow.WorkflowDetail, error) {
	detail, err := a.mon.Detail(id)
	if err != nil {
		return nil, err
	}
	steps := make([]workflow.WorkflowStep, len(detail.Steps))
	for i, s := range detail.Steps {
		steps[i] = workflow.WorkflowStep{
			ID:     s.ID,
			Name:   s.Name,
			Status: s.Status,
			Type:   s.Type,
		}
	}
	events := make([]workflow.WorkflowEvent, len(detail.Events))
	for i, e := range detail.Events {
		events[i] = workflow.WorkflowEvent{
			ID:        e.ID,
			EventType: e.EventType,
			Timestamp: e.Timestamp,
			StepID:    e.StepID,
			Details:   e.Details,
		}
	}
	return &workflow.WorkflowDetail{
		ID:          detail.ID,
		Name:        detail.Name,
		Status:      detail.Status,
		CurrentStep: detail.CurrentStep,
		Progress:    detail.Progress,
		CreatedAt:   detail.CreatedAt,
		StartedAt:   detail.StartedAt,
		CompletedAt: detail.CompletedAt,
		Error:       detail.Error,
		Steps:       steps,
		Events:      events,
	}, nil
}

func (a *workflowMonitorAdapter) ApproveStep(workflowID, stepID string) error {
	return a.mon.ApproveStep(workflowID, stepID)
}

func (a *workflowMonitorAdapter) RejectStep(workflowID, stepID string) error {
	return a.mon.RejectStep(workflowID, stepID)
}

func (a *workflowMonitorAdapter) CancelWorkflow(id string) error {
	return a.mon.CancelWorkflow(id)
}

func (a *workflowMonitorAdapter) Refresh() {
	_ = a.mon.Refresh()
}
