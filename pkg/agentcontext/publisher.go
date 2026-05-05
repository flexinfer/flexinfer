package agentcontext

import "time"

// Event type strings published by SessionSvc and PresenceSvc. Match the bare
// noun+verb convention used by internal/daemon (e.g. process.start, tool.call).
const (
	EventTypeSessionStart      = "session.start"
	EventTypeSessionEnd        = "session.end"
	EventTypeAgentStatusChange = "agent.status.change"
)

// Publisher is implemented by anything that can broadcast a structured event
// to the daemon EventBus. Kept as a small interface inside agentcontext so
// pkg/agentcontext does not have to import internal/daemon (Go forbids it).
//
// Adapters wrapping internal/daemon.EventBus.Publish live in the wiring layer
// (cmd/loomd, or wherever Service is constructed in-process with the daemon).
//
// All methods must be safe for concurrent use. Implementations must not block;
// the caller may be on the hot path of a tool invocation.
type Publisher interface {
	Publish(eventType string, payload any)
}

// noopPublisher is the default — emits nothing. Used when no wiring layer
// has called SetPublisher, so the publish sites stay safe to call unconditionally.
type noopPublisher struct{}

func (noopPublisher) Publish(string, any) {}

// SetPublisher installs a Publisher into both SessionSvc and PresenceSvc so
// session lifecycle and agent.status.change events flow to the wired bus.
// Pass nil to reset both back to the no-op default.
//
// The wiring layer (e.g. cmd/loomd) builds an adapter around its EventBus
// and calls this once at startup.
func (s *Service) SetPublisher(p Publisher) {
	if s.sess != nil {
		s.sess.SetPublisher(p)
	}
	if s.presence != nil {
		s.presence.SetPublisher(p)
	}
}

// SessionStartEvent is the payload for EventTypeSessionStart.
type SessionStartEvent struct {
	SessionID   string    `json:"session_id"`
	AgentID     string    `json:"agent_id"`
	Namespace   string    `json:"namespace"`
	Project     string    `json:"project,omitempty"`
	Description string    `json:"description,omitempty"`
	WorkingDir  string    `json:"working_dir,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	Resumed     bool      `json:"resumed,omitempty"`
	ParentID    string    `json:"parent_session_id,omitempty"`
	RootID      string    `json:"root_session_id,omitempty"`
}

// SessionEndEvent is the payload for EventTypeSessionEnd.
type SessionEndEvent struct {
	SessionID  string    `json:"session_id"`
	AgentID    string    `json:"agent_id"`
	Namespace  string    `json:"namespace"`
	EndedAt    time.Time `json:"ended_at"`
	DurationMs int64     `json:"duration_ms"`
	EntryCount int       `json:"entry_count"`
	Summarized bool      `json:"summarized"`
}

// AgentStatusChangeEvent is the payload for EventTypeAgentStatusChange.
type AgentStatusChangeEvent struct {
	AgentID   string    `json:"agent_id"`
	OldStatus string    `json:"old_status"`
	NewStatus string    `json:"new_status"`
	ChangedAt time.Time `json:"changed_at"`
}
