package agentcontext

import (
	"testing"
	"time"
)

func TestAnnotationToPayload(t *testing.T) {
	now := time.Now()
	ann := CodeAnnotation{
		ID:             "ann-123",
		SessionID:      "session-456",
		AgentID:        "agent-789",
		FilePath:       "/path/to/file.go",
		LineStart:      10,
		LineEnd:        20,
		AnnotationType: AnnotationTypeTodo,
		Content:        "Fix this",
		CreatedAt:      now,
		UpdatedAt:      now,
		TokenCount:     50,
	}

	payload := annotationToPayload(ann)

	if payload["id"] != ann.ID {
		t.Errorf("payload id = %v, want %v", payload["id"], ann.ID)
	}
	if payload["file_path"] != ann.FilePath {
		t.Errorf("payload file_path = %v, want %v", payload["file_path"], ann.FilePath)
	}
	if payload["annotation_type"] != string(ann.AnnotationType) {
		t.Errorf("payload annotation_type = %v, want %v", payload["annotation_type"], ann.AnnotationType)
	}
}

func TestPayloadToAnnotation(t *testing.T) {
	now := time.Now()
	payload := map[string]any{
		"id":              "ann-123",
		"file_path":       "/path/to/file.go",
		"line_start":      float64(10),
		"line_end":        float64(20),
		"annotation_type": "todo",
		"content":         "Fix this",
		"created_at":      now.Format(time.RFC3339Nano),
		"updated_at":      now.Format(time.RFC3339Nano),
	}

	ann, err := payloadToAnnotation(payload)
	if err != nil {
		t.Fatalf("payloadToAnnotation() error = %v", err)
	}

	if ann.ID != "ann-123" {
		t.Errorf("annotation ID = %v, want ann-123", ann.ID)
	}
	if ann.FilePath != "/path/to/file.go" {
		t.Errorf("annotation FilePath = %v, want /path/to/file.go", ann.FilePath)
	}
	if ann.AnnotationType != AnnotationTypeTodo {
		t.Errorf("annotation Type = %v, want todo", ann.AnnotationType)
	}
}

func TestPayloadToAnnotation_NilPayload(t *testing.T) {
	_, err := payloadToAnnotation(nil)
	if err == nil {
		t.Error("payloadToAnnotation(nil) should return error")
	}
}
