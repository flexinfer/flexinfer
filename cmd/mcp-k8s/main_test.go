package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestMain(m *testing.M) {
	// Force JSON output so we can parse CallToolResult.Content[0].Text as JSON.
	os.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// boundedEventListResult tests
// ---------------------------------------------------------------------------

func TestBoundedEventListResult_EmptyEvents(t *testing.T) {
	events := []map[string]any{}

	result, err := boundedEventListResult(events, 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}
	if len(result.Content) == 0 {
		t.Fatal("expected at least one content block")
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	evts, ok := payload["events"]
	if !ok {
		t.Fatal("expected 'events' key in payload")
	}
	arr, ok := evts.([]any)
	if !ok {
		t.Fatalf("expected events to be an array, got %T", evts)
	}
	if len(arr) != 0 {
		t.Errorf("expected 0 events, got %d", len(arr))
	}

	count, ok := payload["count"]
	if !ok {
		t.Fatal("expected 'count' key in payload")
	}
	if count.(float64) != 0 {
		t.Errorf("expected count=0, got %v", count)
	}

	// Should NOT be truncated
	if _, hasTruncated := payload["truncated"]; hasTruncated {
		t.Error("empty events should not have 'truncated' field")
	}
}

func TestBoundedEventListResult_SmallPayload(t *testing.T) {
	events := []map[string]any{
		{"type": "Normal", "reason": "Started", "message": "Container started"},
		{"type": "Normal", "reason": "Pulled", "message": "Image pulled"},
	}

	// Use a generous limit so nothing gets truncated.
	result, err := boundedEventListResult(events, 1024*1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
		return
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	arr := payload["events"].([]any)
	if len(arr) != 2 {
		t.Errorf("expected 2 events, got %d", len(arr))
	}

	count := payload["count"].(float64)
	if count != 2 {
		t.Errorf("expected count=2, got %v", count)
	}

	// Should NOT be truncated
	if _, hasTruncated := payload["truncated"]; hasTruncated {
		t.Error("small payload should not be truncated")
	}
}

func TestBoundedEventListResult_LargePayload(t *testing.T) {
	// Build a large set of events that will exceed the maxBytes limit.
	events := make([]map[string]any, 200)
	for i := range events {
		events[i] = map[string]any{
			"type":    "Warning",
			"reason":  "BackOff",
			"message": strings.Repeat("x", 500), // ~500 bytes per event
		}
	}

	maxBytes := 4096 // Only allow ~4 KB
	result, err := boundedEventListResult(events, maxBytes)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Content[0].Text
	if len(text) > maxBytes {
		t.Errorf("result text length %d exceeds maxBytes %d", len(text), maxBytes)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Must be marked truncated
	truncated, ok := payload["truncated"]
	if !ok {
		t.Fatal("expected 'truncated' key in payload")
	}
	if truncated != true {
		t.Errorf("expected truncated=true, got %v", truncated)
	}

	totalCount := payload["total_event_count"].(float64)
	if int(totalCount) != 200 {
		t.Errorf("expected total_event_count=200, got %v", totalCount)
	}

	maxResponseBytes := payload["max_response_bytes"].(float64)
	if int(maxResponseBytes) != maxBytes {
		t.Errorf("expected max_response_bytes=%d, got %v", maxBytes, maxResponseBytes)
	}

	// The returned events slice should be smaller than the original.
	arr := payload["events"].([]any)
	if len(arr) >= 200 {
		t.Errorf("expected fewer than 200 events after truncation, got %d", len(arr))
	}
	if len(arr) == 0 {
		t.Error("expected at least some events even after truncation")
	}

	// "note" field should be present for truncated results.
	if _, hasNote := payload["note"]; !hasNote {
		t.Error("expected 'note' field in truncated result")
	}
}

func TestBoundedEventListResult_NoLimit(t *testing.T) {
	events := make([]map[string]any, 50)
	for i := range events {
		events[i] = map[string]any{
			"type":    "Normal",
			"reason":  "Scheduled",
			"message": strings.Repeat("y", 200),
		}
	}

	// maxBytes <= 0 means no limit.
	for _, maxBytes := range []int{0, -1, -100} {
		result, err := boundedEventListResult(events, maxBytes)
		if err != nil {
			t.Fatalf("maxBytes=%d: unexpected error: %v", maxBytes, err)
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
			t.Fatalf("maxBytes=%d: failed to parse result JSON: %v", maxBytes, err)
		}

		arr := payload["events"].([]any)
		if len(arr) != 50 {
			t.Errorf("maxBytes=%d: expected 50 events (no limit), got %d", maxBytes, len(arr))
		}

		if _, hasTruncated := payload["truncated"]; hasTruncated {
			t.Errorf("maxBytes=%d: should not be truncated when there is no limit", maxBytes)
		}
	}
}

// ---------------------------------------------------------------------------
// formatAge tests
// ---------------------------------------------------------------------------

func TestFormatAge(t *testing.T) {
	tests := []struct {
		name     string
		t        time.Time
		contains string
	}{
		{"zero time", time.Time{}, "unknown"},
		{"seconds ago", time.Now().Add(-30 * time.Second), "s"},
		{"minutes ago", time.Now().Add(-5 * time.Minute), "m"},
		{"hours ago", time.Now().Add(-3 * time.Hour), "h"},
		{"days ago", time.Now().Add(-48 * time.Hour), "d"},
		{"years ago", time.Now().Add(-400 * 24 * time.Hour), "y"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatAge(tc.t)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("formatAge(%v) = %q, want substring %q", tc.t, got, tc.contains)
			}
		})
	}
}

func TestFormatAge_Values(t *testing.T) {
	// Verify specific expected output for known durations.
	if got := formatAge(time.Time{}); got != "unknown" {
		t.Errorf("expected 'unknown' for zero time, got %q", got)
	}

	// 2 days ago should produce "2d"
	got := formatAge(time.Now().Add(-2 * 24 * time.Hour))
	if got != "2d" {
		t.Errorf("expected '2d', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// getRestarts tests
// ---------------------------------------------------------------------------

func TestGetRestarts(t *testing.T) {
	tests := []struct {
		name       string
		containers []corev1.ContainerStatus
		want       int32
	}{
		{"nil", nil, 0},
		{"empty", []corev1.ContainerStatus{}, 0},
		{"single container no restarts", []corev1.ContainerStatus{
			{RestartCount: 0},
		}, 0},
		{"single container with restarts", []corev1.ContainerStatus{
			{RestartCount: 5},
		}, 5},
		{"multiple containers", []corev1.ContainerStatus{
			{RestartCount: 3},
			{RestartCount: 7},
			{RestartCount: 1},
		}, 11},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := getRestarts(tc.containers)
			if got != tc.want {
				t.Errorf("getRestarts() = %d, want %d", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// kindToGVR tests
// ---------------------------------------------------------------------------

func TestKindToGVR(t *testing.T) {
	tests := []struct {
		kind string
		want schema.GroupVersionResource
	}{
		{"pod", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}},
		{"pods", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}},
		{"deployment", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
		{"deploy", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}},
		{"service", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}},
		{"svc", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}},
		{"configmap", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}},
		{"cm", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}},
		{"secret", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}},
		{"secrets", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}},
		{"ingress", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}},
		{"ing", schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}},
		{"namespace", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}},
		{"ns", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}},
		{"node", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "nodes"}},
		{"pv", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumes"}},
		{"pvc", schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}},
		{"statefulset", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}},
		{"sts", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}},
		{"daemonset", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}},
		{"ds", schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}},
		{"job", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}},
		{"cronjob", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}},
		{"cj", schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}},
		// Unknown kind returns empty GVR.
		{"unknown", schema.GroupVersionResource{}},
		{"foobar", schema.GroupVersionResource{}},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			got := kindToGVR(tc.kind)
			if got != tc.want {
				t.Errorf("kindToGVR(%q) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isNamespaced tests
// ---------------------------------------------------------------------------

func TestIsNamespaced(t *testing.T) {
	tests := []struct {
		kind string
		want bool
	}{
		{"pod", true},
		{"deployment", true},
		{"service", true},
		{"configmap", true},
		{"secret", true},
		{"ingress", true},
		{"statefulset", true},
		// Cluster-scoped resources
		{"namespace", false},
		{"namespaces", false},
		{"ns", false},
		{"node", false},
		{"nodes", false},
		{"pv", false},
		{"persistentvolume", false},
		{"persistentvolumes", false},
	}
	for _, tc := range tests {
		t.Run(tc.kind, func(t *testing.T) {
			got := isNamespaced(tc.kind)
			if got != tc.want {
				t.Errorf("isNamespaced(%q) = %v, want %v", tc.kind, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// canonicalKindForEvents tests
// ---------------------------------------------------------------------------

func TestCanonicalKindForEvents(t *testing.T) {
	tests := []struct {
		kind string
		want string
	}{
		{"pod", "Pod"},
		{"pods", "Pod"},
		{"deployment", "Deployment"},
		{"deploy", "Deployment"},
		{"statefulset", "StatefulSet"},
		{"sts", "StatefulSet"},
		{"daemonset", "DaemonSet"},
		{"ds", "DaemonSet"},
		{"service", "Service"},
		{"svc", "Service"},
		{"configmap", "ConfigMap"},
		{"cm", "ConfigMap"},
		{"secret", "Secret"},
		{"secrets", "Secret"},
		{"namespace", "Namespace"},
		{"ns", "Namespace"},
		{"node", "Node"},
		{"nodes", "Node"},
		{"pvc", "PersistentVolumeClaim"},
		{"pv", "PersistentVolume"},
		{"job", "Job"},
		{"cronjob", "CronJob"},
		{"cj", "CronJob"},
		// Unknown kind capitalizes first letter
		{"widget", "Widget"},
		// Empty string
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.kind+"/"+tc.want, func(t *testing.T) {
			got := canonicalKindForEvents(tc.kind)
			if got != tc.want {
				t.Errorf("canonicalKindForEvents(%q) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// boundedEventListResult edge cases
// ---------------------------------------------------------------------------

func TestBoundedEventListResult_NilEvents(t *testing.T) {
	// nil slice behaves like empty.
	result, err := boundedEventListResult(nil, 4096)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	count := payload["count"].(float64)
	if count != 0 {
		t.Errorf("expected count=0 for nil events, got %v", count)
	}
}

func TestBoundedEventListResult_ExactFit(t *testing.T) {
	// A single small event with a generous limit should not be truncated.
	events := []map[string]any{
		{"type": "Normal", "reason": "Created"},
	}

	result, err := boundedEventListResult(events, 1024*1024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	if _, hasTruncated := payload["truncated"]; hasTruncated {
		t.Error("single small event should not be truncated")
	}

	arr := payload["events"].([]any)
	if len(arr) != 1 {
		t.Errorf("expected 1 event, got %d", len(arr))
	}
}

func TestBoundedEventListResult_VeryTightLimit(t *testing.T) {
	// With an extremely small maxBytes, truncation should still produce a valid result.
	events := []map[string]any{
		{"type": "Warning", "reason": "BackOff", "message": "Container crashed"},
	}

	// maxBytes so small that even 0 events in the truncated payload might barely fit.
	result, err := boundedEventListResult(events, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("failed to parse result JSON: %v", err)
	}

	// Result should be truncated with 0 events returned.
	truncated := payload["truncated"]
	if truncated != true {
		t.Error("expected truncated=true for very tight limit")
	}

	arr := payload["events"].([]any)
	if len(arr) != 0 {
		t.Errorf("expected 0 events with very tight limit, got %d", len(arr))
	}
}
