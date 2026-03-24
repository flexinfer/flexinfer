// deps.go defines the Deps interface for alerting domain handlers.
package alerting

import (
	"context"
	"net/http"

	"github.com/crb2nu/loom/internal/hud/alerting"
	"github.com/crb2nu/loom/internal/hud/autofix"
)

// Deps exposes the subset of App capabilities that alerting handlers need.
type Deps interface {
	WriteJSON(w http.ResponseWriter, status int, v any)
	WriteError(w http.ResponseWriter, status int, msg string, err error)
	RequireAdminToken(w http.ResponseWriter, r *http.Request) bool

	AlertEngine() AlertEngineOps
	AutoFixEngine() AutoFixEngineOps
}

// AlertEngineOps is the subset of AlertEngine methods used by handlers.
type AlertEngineOps interface {
	ListAlerts(limit int) []alerting.Alert
	ListRules() []alerting.AlertRule
	UpdateRules(rules []alerting.AlertRule)
	AckAlert(id, ackedBy string) error
}

// AutoFixEngineOps is the subset of AutoFixEngine methods used by handlers.
type AutoFixEngineOps interface {
	DiagnoseFailure(ctx context.Context, project string, pipelineID int) (*autofix.Diagnosis, error)
	ProposeAutoFix(diag autofix.Diagnosis) (*autofix.AutoFixProposal, error)
	ExecuteAutoFix(ctx context.Context, proposal autofix.AutoFixProposal) (*autofix.AutoFixExecution, error)
	GetExecution(id string) (*autofix.AutoFixExecution, error)
	GetProposal(id string) (*autofix.AutoFixProposal, error)
	ListProposals() []autofix.AutoFixProposal
	ListExecutions() []autofix.AutoFixExecution
	RejectProposal(proposalID string) error
}
