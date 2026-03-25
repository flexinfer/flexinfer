package hud

import (
	"strings"
	"testing"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

func TestClassifyPipelineEvent_SuppressesSuccessWhenActiveWorkRemains(t *testing.T) {
	t.Parallel()

	event := bridge.SSEEvent{
		Type: "hud.pipeline",
		Data: []byte(`{"pipelines":[
			{"id":1,"project":"services/loom-core","ref":"main","status":"running"},
			{"id":2,"project":"services/loom-core","ref":"feature/merge","status":"success"}
		]}`),
	}

	got := classifyPipelineEvent(event)
	if !got.Worthy {
		t.Fatal("expected active pipeline classification")
	}
	if got.EventType != "hud.pipeline.active" {
		t.Fatalf("event type = %q, want hud.pipeline.active", got.EventType)
	}
	if got.Title != "Pipeline Still Running" {
		t.Fatalf("title = %q, want Pipeline Still Running", got.Title)
	}
	if !strings.Contains(got.Body, "running pipeline") {
		t.Fatalf("expected body to mention running work, got %q", got.Body)
	}
	if !strings.Contains(got.Body, "passed pipeline") {
		t.Fatalf("expected body to mention passed work, got %q", got.Body)
	}
}

func TestClassifyPipelineEvent_RepresentsManualJobsAsWaiting(t *testing.T) {
	t.Parallel()

	event := bridge.SSEEvent{
		Type: "hud.pipeline",
		Data: []byte(`{"pipelines":[
			{"id":3,"project":"services/loom-core","ref":"feature/manual","status":"manual"}
		]}`),
	}

	got := classifyPipelineEvent(event)
	if !got.Worthy {
		t.Fatal("expected waiting pipeline classification")
	}
	if got.EventType != "hud.pipeline.active" {
		t.Fatalf("event type = %q, want hud.pipeline.active", got.EventType)
	}
	if got.Title != "Pipeline Waiting" {
		t.Fatalf("title = %q, want Pipeline Waiting", got.Title)
	}
	if !strings.Contains(got.Body, "waiting on manual jobs") {
		t.Fatalf("expected body to mention manual jobs, got %q", got.Body)
	}
}

func TestClassifyPipelineEvent_SucceedsWhenEverythingIsDone(t *testing.T) {
	t.Parallel()

	event := bridge.SSEEvent{
		Type: "hud.pipeline",
		Data: []byte(`{"pipelines":[
			{"id":4,"project":"services/loom-core","ref":"main","status":"success"}
		]}`),
	}

	got := classifyPipelineEvent(event)
	if !got.Worthy {
		t.Fatal("expected success classification")
	}
	if got.EventType != "hud.pipeline.success" {
		t.Fatalf("event type = %q, want hud.pipeline.success", got.EventType)
	}
	if got.Title != "Pipeline Succeeded" {
		t.Fatalf("title = %q, want Pipeline Succeeded", got.Title)
	}
	if !strings.Contains(got.Body, "passed") {
		t.Fatalf("expected body to mention pass result, got %q", got.Body)
	}
}
