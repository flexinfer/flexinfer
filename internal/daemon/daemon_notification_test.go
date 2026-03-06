package daemon

import (
	"log/slog"
	"testing"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

func TestToolNamesChanged_SameSets(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	if toolNamesChanged(tools, tools) {
		t.Error("expected no change for identical slices")
	}
}

func TestToolNamesChanged_Reordered(t *testing.T) {
	old := []mcp.Tool{{Name: "a"}, {Name: "b"}}
	new := []mcp.Tool{{Name: "b"}, {Name: "a"}}
	if toolNamesChanged(old, new) {
		t.Error("expected no change for reordered tools")
	}
}

func TestToolNamesChanged_Added(t *testing.T) {
	old := []mcp.Tool{{Name: "a"}}
	new := []mcp.Tool{{Name: "a"}, {Name: "b"}}
	if !toolNamesChanged(old, new) {
		t.Error("expected change when tool added")
	}
}

func TestToolNamesChanged_Removed(t *testing.T) {
	old := []mcp.Tool{{Name: "a"}, {Name: "b"}}
	new := []mcp.Tool{{Name: "a"}}
	if !toolNamesChanged(old, new) {
		t.Error("expected change when tool removed")
	}
}

func TestToolNamesChanged_Replaced(t *testing.T) {
	old := []mcp.Tool{{Name: "a"}, {Name: "b"}}
	new := []mcp.Tool{{Name: "a"}, {Name: "c"}}
	if !toolNamesChanged(old, new) {
		t.Error("expected change when tool replaced")
	}
}

func TestToolNamesChanged_BothEmpty(t *testing.T) {
	if toolNamesChanged(nil, nil) {
		t.Error("expected no change for two empty lists")
	}
}

func TestResourceNamesChanged_SameSets(t *testing.T) {
	res := []mcp.Resource{
		{URI: "loom://a"}, {URI: "loom://b"},
	}
	if resourceNamesChanged(res, res) {
		t.Error("expected no change for identical slices")
	}
}

func TestResourceNamesChanged_Added(t *testing.T) {
	old := []mcp.Resource{{URI: "loom://a"}}
	new := []mcp.Resource{{URI: "loom://a"}, {URI: "loom://b"}}
	if !resourceNamesChanged(old, new) {
		t.Error("expected change when resource added")
	}
}

func TestResourceNamesChanged_Removed(t *testing.T) {
	old := []mcp.Resource{{URI: "loom://a"}, {URI: "loom://b"}}
	new := []mcp.Resource{{URI: "loom://a"}}
	if !resourceNamesChanged(old, new) {
		t.Error("expected change when resource removed")
	}
}

func TestEventToMCPNotification_ToolsChanged(t *testing.T) {
	event := Event{Type: EventToolsChanged}
	notif := eventToMCPNotification(event)
	if notif == nil {
		t.Fatal("expected non-nil notification for EventToolsChanged")
	}
	if notif.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q, want %q", notif.JSONRPC, "2.0")
	}
	if notif.Method != "notifications/tools/list_changed" {
		t.Errorf("Method = %q, want %q", notif.Method, "notifications/tools/list_changed")
	}
	if notif.ID != nil {
		t.Errorf("ID = %v, want nil (notification must not have an ID)", notif.ID)
	}
}

func TestEventToMCPNotification_ResourcesChanged(t *testing.T) {
	event := Event{Type: EventResourcesChanged}
	notif := eventToMCPNotification(event)
	if notif == nil {
		t.Fatal("expected non-nil notification for EventResourcesChanged")
	}
	if notif.Method != "notifications/resources/list_changed" {
		t.Errorf("Method = %q, want %q", notif.Method, "notifications/resources/list_changed")
	}
}

func TestEventToMCPNotification_UnrelatedEvent(t *testing.T) {
	event := Event{Type: EventToolCall}
	notif := eventToMCPNotification(event)
	if notif != nil {
		t.Errorf("expected nil for unrelated event type, got Method=%q", notif.Method)
	}
}

func TestEventBus_ToolsChangedSubscribable(t *testing.T) {
	eb := NewEventBus(slog.Default())
	id, ch := eb.Subscribe()
	defer eb.Unsubscribe(id)

	eb.Publish(EventToolsChanged, map[string]any{"test": true})

	event := <-ch
	if event.Type != EventToolsChanged {
		t.Errorf("Type = %q, want %q", event.Type, EventToolsChanged)
	}
}

func TestEventBus_ResourcesChangedSubscribable(t *testing.T) {
	eb := NewEventBus(slog.Default())
	id, ch := eb.Subscribe()
	defer eb.Unsubscribe(id)

	eb.Publish(EventResourcesChanged, map[string]any{"test": true})

	event := <-ch
	if event.Type != EventResourcesChanged {
		t.Errorf("Type = %q, want %q", event.Type, EventResourcesChanged)
	}
}
