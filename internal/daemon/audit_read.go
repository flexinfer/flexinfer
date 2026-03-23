package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"time"

	"gitlab.flexinfer.ai/libs/fi-accel/go/fiaccel"
)

// AuditReadOptions controls optional filtering while reading audit entries.
type AuditReadOptions struct {
	Limit int
}

// Path returns the backing audit log path, if available.
func (a *AuditLogger) Path() string {
	if a == nil {
		return ""
	}
	return a.path
}

// ReadEntries loads audit entries from the logger's backing file.
func (a *AuditLogger) ReadEntries(options AuditReadOptions) ([]AuditEntry, error) {
	if a == nil || a.path == "" {
		return nil, nil
	}
	return ReadAuditEntries(a.path, options)
}

// Summary returns an event-style summary of the logger's backing file.
func (a *AuditLogger) Summary(options AuditReadOptions) (fiaccel.EventSummary, error) {
	if a == nil || a.path == "" {
		return fiaccel.EventSummary{}, nil
	}
	return SummarizeAuditEntries(a.path, options)
}

// ReadAuditEntries reads an audit JSONL file into structured entries.
func ReadAuditEntries(path string, options AuditReadOptions) ([]AuditEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return readAuditEntriesBytes(data, options)
}

// SummarizeAuditEntries reads an audit JSONL file and returns aggregate counts.
func SummarizeAuditEntries(path string, options AuditReadOptions) (fiaccel.EventSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fiaccel.EventSummary{}, err
	}
	return summarizeAuditEntriesBytes(data, options)
}

func readAuditEntriesBytes(data []byte, options AuditReadOptions) ([]AuditEntry, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}

	if fiaccel.RuntimeCapabilities().Eventlog {
		entries, err := readAuditEntriesFast(data, options)
		if err == nil {
			return entries, nil
		}
		if !errors.Is(err, fiaccel.ErrNotAvailable) {
			return nil, err
		}
	}

	return readAuditEntriesFallback(data, options)
}

func summarizeAuditEntriesBytes(data []byte, options AuditReadOptions) (fiaccel.EventSummary, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return fiaccel.EventSummary{}, nil
	}

	if fiaccel.RuntimeCapabilities().Eventlog {
		summary, err := fiaccel.SummarizeEventlog(data, auditEventProjectionConfig(options))
		if err == nil {
			return summary, nil
		}
		if !errors.Is(err, fiaccel.ErrNotAvailable) {
			return fiaccel.EventSummary{}, err
		}
	}

	entries, err := readAuditEntriesFallback(data, options)
	if err != nil {
		return fiaccel.EventSummary{}, err
	}
	return summarizeAuditEntriesFallback(entries), nil
}

func auditEventProjectionConfig(options AuditReadOptions) *fiaccel.EventProjectionConfig {
	cfg := &fiaccel.EventProjectionConfig{
		EventTypeField: "status",
	}
	if options.Limit > 0 {
		cfg.Limit = options.Limit
	}
	return cfg
}

func readAuditEntriesFast(data []byte, options AuditReadOptions) ([]AuditEntry, error) {
	events, err := fiaccel.ProjectEventlog(data, auditEventProjectionConfig(options))
	if err != nil {
		return nil, err
	}

	entries := make([]AuditEntry, 0, len(events))
	for _, event := range events {
		entry, err := decodeProjectedAuditEvent(event)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func decodeProjectedAuditEvent(event fiaccel.EventRecord) (AuditEntry, error) {
	var entry AuditEntry
	if len(event.Data) > 0 {
		if err := json.Unmarshal(event.Data, &entry); err != nil {
			return AuditEntry{}, err
		}
	}

	entry.Status = event.EventType
	if event.ActorID != nil {
		entry.AgentID = *event.ActorID
	}
	if event.ActorType != nil {
		entry.AgentType = *event.ActorType
	}
	if event.Timestamp != nil {
		ts, err := parseAuditTimestamp(*event.Timestamp)
		if err != nil {
			return AuditEntry{}, err
		}
		entry.Timestamp = ts
	}
	return entry, nil
}

func parseAuditTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, nil
	}
	return time.Parse(time.RFC3339, value)
}

func readAuditEntriesFallback(data []byte, options AuditReadOptions) ([]AuditEntry, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var entries []AuditEntry
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var entry AuditEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return nil, err
		}
		entries = appendAuditEntry(entries, entry, options.Limit)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func appendAuditEntry(entries []AuditEntry, entry AuditEntry, limit int) []AuditEntry {
	entries = append(entries, entry)
	if limit > 0 && len(entries) > limit {
		copy(entries, entries[len(entries)-limit:])
		entries = entries[:limit]
	}
	return entries
}

func summarizeAuditEntriesFallback(entries []AuditEntry) fiaccel.EventSummary {
	summary := fiaccel.EventSummary{
		TotalEvents: len(entries),
		ByEventType: make(map[string]int),
		ByActorID:   make(map[string]int),
		ByActorType: make(map[string]int),
	}

	for _, entry := range entries {
		summary.ByEventType[entry.Status]++
		if entry.AgentID != "" {
			summary.ByActorID[entry.AgentID]++
		}
		if entry.AgentType != "" {
			summary.ByActorType[entry.AgentType]++
		}

		if entry.Timestamp.IsZero() {
			continue
		}
		timestamp := entry.Timestamp.UTC().Format(time.RFC3339Nano)
		if summary.OldestTimestamp == nil || timestamp < *summary.OldestTimestamp {
			summary.OldestTimestamp = &timestamp
		}
		if summary.NewestTimestamp == nil || timestamp > *summary.NewestTimestamp {
			summary.NewestTimestamp = &timestamp
		}
	}

	return summary
}
