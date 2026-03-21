package workflow

import (
	"log/slog"
	"net/http"
)

// Deps defines the dependencies the workflow domain needs from the host App.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	Logger() *slog.Logger
	BroadcastAgentEvent(eventType string, payload any)
	WorkflowMonitor() WorkflowMonitorOps
}

// WorkflowMonitorOps abstracts the workflow monitor interface.
type WorkflowMonitorOps interface {
	Workflows() []WorkflowSummary
	Detail(id string) (*WorkflowDetail, error)
	ApproveStep(workflowID, stepID string) error
	RejectStep(workflowID, stepID string) error
	CancelWorkflow(id string) error
	Refresh()
}

// WorkflowSummary is a compact view of a workflow for listing.
type WorkflowSummary struct {
	ID          string
	Name        string
	Status      string
	CurrentStep string
	CreatedAt   string
	Progress    float64
	Error       string
}

// WorkflowDetail is the full workflow status including steps and events.
type WorkflowDetail struct {
	ID          string
	Name        string
	Status      string
	CurrentStep string
	Progress    float64
	CreatedAt   string
	StartedAt   string
	CompletedAt string
	Error       string
	Steps       []WorkflowStep
	Events      []WorkflowEvent
}

// WorkflowStep describes a single step within a workflow.
type WorkflowStep struct {
	ID     string
	Name   string
	Status string
	Type   string
}

// WorkflowEvent is a single workflow execution event.
type WorkflowEvent struct {
	ID        string
	EventType string
	Timestamp string
	StepID    string
	Details   map[string]any
}
