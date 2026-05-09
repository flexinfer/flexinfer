package bridge

import (
	"encoding/json"
	"testing"
)

func TestAgentBridge_EngramSummary_AggregatesCounts(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		var req struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(params, &req); err != nil {
			t.Fatalf("unmarshal params: %v", err)
		}
		if req.Name != "agent_context__agent_engram_list" {
			t.Fatalf("unexpected tool name: %s", req.Name)
		}
		// Returned shape mirrors HandleEngramList: {ok, count, items: [...]}.
		body := map[string]any{
			"ok":    true,
			"count": 4,
			"items": []map[string]any{
				{"uri": "engram://a/x", "tier": 1, "proof_status": "verified"},
				{"uri": "engram://b/x", "tier": 2, "proof_status": "verified"},
				{"uri": "engram://c/x", "tier": 1, "proof_status": "stale"},
				{"uri": "engram://d/x", "tier": 3, "proof_status": "failing"},
			},
		}
		raw, _ := json.Marshal(body)
		return map[string]any{
			"isError": false,
			"content": []map[string]any{
				{"type": "text", "text": string(raw)},
			},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	got, err := bridge.EngramSummary()
	if err != nil {
		t.Fatalf("summary failed: %v", err)
	}

	if got.Total != 4 {
		t.Errorf("total: got %d want 4", got.Total)
	}
	if got.ByStatus["verified"] != 2 {
		t.Errorf("verified: got %d want 2", got.ByStatus["verified"])
	}
	if got.ByStatus["stale"] != 1 {
		t.Errorf("stale: got %d want 1", got.ByStatus["stale"])
	}
	if got.ByStatus["failing"] != 1 {
		t.Errorf("failing: got %d want 1", got.ByStatus["failing"])
	}
	if got.ByStatus["unverified"] != 0 {
		t.Errorf("unverified: got %d want 0 (key must be present even when zero)", got.ByStatus["unverified"])
	}
	if got.ByTier["tier:1"] != 2 || got.ByTier["tier:2"] != 1 || got.ByTier["tier:3"] != 1 {
		t.Errorf("by_tier wrong: %+v", got.ByTier)
	}
}

func TestAgentBridge_EngramSummary_MissingProofStatusDefaultsUnverified(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		body := map[string]any{
			"ok":    true,
			"count": 1,
			"items": []map[string]any{
				// Legacy recipe data: no proof_status field at all.
				{"uri": "engram://legacy/x", "tier": 1},
			},
		}
		raw, _ := json.Marshal(body)
		return map[string]any{
			"isError": false,
			"content": []map[string]any{{"type": "text", "text": string(raw)}},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	got, err := bridge.EngramSummary()
	if err != nil {
		t.Fatalf("summary failed: %v", err)
	}
	if got.ByStatus["unverified"] != 1 {
		t.Errorf("legacy item should default to unverified; got by_status=%+v", got.ByStatus)
	}
}

func TestAgentBridge_EngramSummary_EmptyLibrary(t *testing.T) {
	sockPath, handlers := mockDaemon(t)

	handlers.handle("tools/call", func(params json.RawMessage) (any, error) {
		body := map[string]any{"ok": true, "count": 0, "items": []map[string]any{}}
		raw, _ := json.Marshal(body)
		return map[string]any{
			"isError": false,
			"content": []map[string]any{{"type": "text", "text": string(raw)}},
		}, nil
	})

	client := NewDaemonClient(sockPath, nil)
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Close()

	bridge := NewAgentBridge(client)
	got, err := bridge.EngramSummary()
	if err != nil {
		t.Fatalf("summary failed: %v", err)
	}
	if got.Total != 0 {
		t.Errorf("total: got %d want 0", got.Total)
	}
	for _, k := range []string{"unverified", "verified", "stale", "failing"} {
		if got.ByStatus[k] != 0 {
			t.Errorf("by_status[%s] should be 0; got %d", k, got.ByStatus[k])
		}
	}
}
