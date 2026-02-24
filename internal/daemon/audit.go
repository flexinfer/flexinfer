// Package daemon provides structured audit logging for tool calls.
package daemon

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditConfig controls structured audit logging.
type AuditConfig struct {
	// Enabled activates audit logging. When false, no audit entries are written.
	Enabled bool `yaml:"enabled"`

	// LogPath is the file path for audit log output.
	// Default: ~/.config/loom/audit.jsonl
	LogPath string `yaml:"log_path,omitempty"`
}

// DefaultAuditConfig returns a disabled audit configuration.
func DefaultAuditConfig() AuditConfig {
	return AuditConfig{
		Enabled: false,
	}
}

// AuditEntry is a structured record of a single tool call.
type AuditEntry struct {
	Timestamp        time.Time `json:"timestamp"`
	AgentID          string    `json:"agent_id"`
	AgentType        string    `json:"agent_type,omitempty"`
	Server           string    `json:"server"`
	Tool             string    `json:"tool"`
	DurationMs       int64     `json:"duration_ms"`
	Status           string    `json:"status"` // "success", "error", "denied"
	Error            string    `json:"error,omitempty"`
	Target           string    `json:"target,omitempty"` // "local" or "hub"
	Cached           bool      `json:"cached,omitempty"`
	PipelineStage    string    `json:"pipeline_stage,omitempty"`
	PolicyRuleID     string    `json:"policy_rule_id,omitempty"`
	PolicyReasonCode string    `json:"policy_reason_code,omitempty"`
}

// AuditLogger writes structured audit entries to an append-only JSONL file.
type AuditLogger struct {
	mu     sync.Mutex
	file   *os.File
	enc    *json.Encoder
	logger *slog.Logger
}

// NewAuditLogger creates an audit logger. Returns nil if auditing is disabled.
func NewAuditLogger(cfg AuditConfig, logger *slog.Logger) (*AuditLogger, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if logger == nil {
		logger = slog.Default()
	}

	logPath := cfg.LogPath
	if logPath == "" {
		home, _ := os.UserHomeDir()
		logPath = filepath.Join(home, ".config", "loom", "audit.jsonl")
	}

	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		return nil, fmt.Errorf("create audit log directory: %w", err)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open audit log: %w", err)
	}

	logger.Info("audit logging enabled", "path", logPath)

	return &AuditLogger{
		file:   f,
		enc:    json.NewEncoder(f),
		logger: logger,
	}, nil
}

// Log writes an audit entry to the log file.
func (a *AuditLogger) Log(entry AuditEntry) {
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := a.enc.Encode(entry); err != nil {
		a.logger.Warn("failed to write audit entry", "error", err)
	}
}

// Close closes the audit log file.
func (a *AuditLogger) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.file.Close()
}
