package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func decodeJSONResult(t *testing.T, resText string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(resText), &out); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, resText)
	}
	return out
}

func TestClampInt(t *testing.T) {
	t.Parallel()
	if got := clampInt(-1, 1, 10); got != 1 {
		t.Fatalf("clampInt(-1) = %d, want 1", got)
	}
	if got := clampInt(5, 1, 10); got != 5 {
		t.Fatalf("clampInt(5) = %d, want 5", got)
	}
	if got := clampInt(99, 1, 10); got != 10 {
		t.Fatalf("clampInt(99) = %d, want 10", got)
	}
}

func TestBoundedEventSliceResult_TruncatesWhenOverCap(t *testing.T) {
	t.Parallel()
	events := make([]map[string]any, 0, 20)
	for i := 0; i < 20; i++ {
		events = append(events, map[string]any{
			"message": strings.Repeat("x", 80),
			"index":   i,
		})
	}

	got, truncated := boundedEventSliceResult(events, 900)
	if !truncated {
		t.Fatal("expected result to be truncated")
	}
	if tr, _ := got["truncated"].(bool); !tr {
		t.Fatalf("expected payload truncated=true, got %v", got["truncated"])
	}

	count, _ := got["count"].(int)
	total, _ := got["total_event_count"].(int)
	if total != len(events) {
		t.Fatalf("total_event_count=%d, want %d", total, len(events))
	}
	if count >= total {
		t.Fatalf("expected truncated count to be less than total (count=%d total=%d)", count, total)
	}
}

func TestHandleEvents_KubernetesFallback_Filtering(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	t.Setenv("FLUX_MAX_RESPONSE_BYTES", "1048576")
	t.Setenv("FLUX_EVENTS_MAX_ITEMS", "100")

	cs := k8sfake.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "flux-system"},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Kustomization",
				Name:      "apps",
				Namespace: "flux-system",
			},
			Reason:  "ReconciliationSucceeded",
			Message: "Kustomization applied",
			Type:    "Normal",
		},
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "e2", Namespace: "flux-system"},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "GitRepository",
				Name:      "platform",
				Namespace: "flux-system",
			},
			Reason:  "GitOperationFailed",
			Message: "Fetch failed",
			Type:    "Warning",
		},
	)

	f := &fluxServer{
		namespace:  "flux-system",
		timeout:    2 * time.Second,
		fluxBin:    "",
		kubeClient: cs,
	}

	res, err := f.handleEvents(context.Background(), map[string]any{
		"for":   "Kustomization/apps",
		"limit": 50,
	})
	if err != nil {
		t.Fatalf("handleEvents: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected JSON result content")
	}

	decoded := decodeJSONResult(t, res.Content[0].Text)
	if decoded["mode"] != "kubernetes-api" {
		t.Fatalf("mode=%v, want kubernetes-api", decoded["mode"])
	}
	if decoded["count"] != float64(1) {
		t.Fatalf("count=%v, want 1", decoded["count"])
	}

	events, ok := decoded["events"].([]any)
	if !ok || len(events) != 1 {
		t.Fatalf("events length=%d, want 1", len(events))
	}
	first, _ := events[0].(map[string]any)
	involved, _ := first["involved_object"].(map[string]any)
	if involved["kind"] != "Kustomization" || involved["name"] != "apps" {
		t.Fatalf("unexpected involved_object=%v", involved)
	}
}

func TestHandleEvents_KubernetesFallback_RespectsResponseCap(t *testing.T) {
	t.Setenv("LOOM_MCP_OUTPUT_FORMAT", "json")
	t.Setenv("FLUX_MAX_RESPONSE_BYTES", "900")
	t.Setenv("FLUX_EVENTS_MAX_ITEMS", "1000")

	objs := make([]*corev1.Event, 0, 30)
	for i := 0; i < 30; i++ {
		objs = append(objs, &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("event-%02d", i),
				Namespace: "flux-system",
			},
			InvolvedObject: corev1.ObjectReference{
				Kind:      "Kustomization",
				Name:      "apps",
				Namespace: "flux-system",
			},
			Reason:  "Progressing",
			Message: strings.Repeat("very-long-event-message ", 8),
			Type:    "Normal",
		})
	}

	runtimeObjs := make([]runtime.Object, 0, len(objs))
	for _, ev := range objs {
		runtimeObjs = append(runtimeObjs, ev)
	}
	cs := k8sfake.NewSimpleClientset(runtimeObjs...)

	f := &fluxServer{
		namespace:  "flux-system",
		timeout:    2 * time.Second,
		fluxBin:    "",
		kubeClient: cs,
	}

	res, err := f.handleEvents(context.Background(), map[string]any{"limit": 500})
	if err != nil {
		t.Fatalf("handleEvents: %v", err)
	}
	if res == nil || len(res.Content) == 0 {
		t.Fatal("expected JSON result content")
	}

	decoded := decodeJSONResult(t, res.Content[0].Text)
	if decoded["truncated"] != true {
		t.Fatalf("expected truncated response, got truncated=%v", decoded["truncated"])
	}
	count, _ := decoded["count"].(float64)
	total, _ := decoded["total_event_count"].(float64)
	if count >= total {
		t.Fatalf("expected count < total_event_count in truncated payload (count=%v total=%v)", count, total)
	}
}
