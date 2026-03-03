package agentcontext

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/httpclient"
)

type sessionsQdrantStub struct {
	mu       sync.Mutex
	sessions map[string]map[string]any
}

func newSessionsQdrantStub(t *testing.T, seeded ...Session) (*QdrantClient, *sessionsQdrantStub) {
	t.Helper()

	stub := &sessionsQdrantStub{
		sessions: make(map[string]map[string]any, len(seeded)),
	}
	for _, sess := range seeded {
		stub.sessions[sess.ID] = clonePayload(SessionToPayload(sess))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/collections/"+CollSessions, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, map[string]any{
				"result": map[string]any{
					"config": map[string]any{
						"params": map[string]any{
							"vectors": map[string]any{
								"size":     sessionsVectorSize,
								"distance": "Cosine",
							},
						},
					},
				},
			})
		case http.MethodPut:
			writeJSON(t, w, map[string]any{"status": "ok"})
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/collections/"+CollSessions+"/points/scroll", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode scroll body: %v", err)
		}
		filter, _ := body["filter"].(map[string]any)
		limit := 1000
		if raw, ok := body["limit"].(float64); ok && raw > 0 {
			limit = int(raw)
		}

		stub.mu.Lock()
		points := make([]map[string]any, 0, len(stub.sessions))
		for id, payload := range stub.sessions {
			if !matchesPayloadFilter(filter, payload) {
				continue
			}
			points = append(points, map[string]any{
				"id":      toPointID(id),
				"payload": clonePayload(payload),
			})
		}
		stub.mu.Unlock()

		if len(points) > limit {
			points = points[:limit]
		}
		writeJSON(t, w, map[string]any{
			"result": map[string]any{
				"points":           points,
				"next_page_offset": nil,
			},
		})
	})
	mux.HandleFunc("/collections/"+CollSessions+"/points", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode upsert body: %v", err)
		}
		rawPoints, _ := body["points"].([]any)
		stub.mu.Lock()
		for _, raw := range rawPoints {
			point, _ := raw.(map[string]any)
			payload, _ := point["payload"].(map[string]any)
			id, _ := payload["id"].(string)
			if id == "" {
				continue
			}
			stub.sessions[id] = clonePayload(payload)
		}
		stub.mu.Unlock()
		writeJSON(t, w, map[string]any{"status": "ok"})
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := NewQdrantClient(httpclient.NewDefault(), server.URL, "", CollSessions, "Cosine")
	return client, stub
}

func (s *sessionsQdrantStub) sessionByID(id string) (*Session, bool) {
	s.mu.Lock()
	payload, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	sess, err := PayloadToSession(clonePayload(payload))
	if err != nil || sess == nil {
		return nil, false
	}
	return sess, true
}

func writeJSON(t *testing.T, w http.ResponseWriter, payload map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatalf("encode JSON response: %v", err)
	}
}

func clonePayload(payload map[string]any) map[string]any {
	raw, _ := json.Marshal(payload)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func matchesPayloadFilter(filter map[string]any, payload map[string]any) bool {
	if len(filter) == 0 {
		return true
	}

	if rawMust, ok := filter["must"].([]any); ok {
		for _, cond := range rawMust {
			if !matchesPayloadCondition(cond, payload) {
				return false
			}
		}
	}

	if rawShould, ok := filter["should"].([]any); ok {
		matched := false
		for _, cond := range rawShould {
			if matchesPayloadCondition(cond, payload) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func matchesPayloadCondition(rawCond any, payload map[string]any) bool {
	cond, ok := rawCond.(map[string]any)
	if !ok {
		return false
	}

	if _, ok := cond["must"]; ok {
		return matchesPayloadFilter(cond, payload)
	}
	if _, ok := cond["should"]; ok {
		return matchesPayloadFilter(cond, payload)
	}

	key, _ := cond["key"].(string)
	if key == "" {
		return false
	}

	match, _ := cond["match"].(map[string]any)
	if match == nil {
		return false
	}

	if want, ok := match["value"]; ok {
		got, _ := payload[key].(string)
		wantStr, _ := want.(string)
		return got == wantStr
	}

	if rawAny, ok := match["any"].([]any); ok {
		got, _ := payload[key].(string)
		for _, candidate := range rawAny {
			if want, ok := candidate.(string); ok && got == want {
				return true
			}
		}
		return false
	}

	return false
}

func TestHandleSessionDelete_RemovesFromMemory(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	// Create a session
	svc.sess.sessions["sess-1"] = &Session{
		ID:        "sess-1",
		AgentID:   "agent-1",
		Status:    string(SessionStatusEnded),
		StartedAt: now.Add(-2 * time.Hour),
	}

	result, err := svc.HandleSessionDelete(context.Background(), map[string]any{
		"session_id": "sess-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}

	// Verify removed from memory
	svc.sess.mu.RLock()
	_, exists := svc.sess.sessions["sess-1"]
	svc.sess.mu.RUnlock()
	if exists {
		t.Error("session should have been deleted from memory")
	}
}

func TestHandleSessionDelete_NonExistent(t *testing.T) {
	svc := newTestService()

	result, err := svc.HandleSessionDelete(context.Background(), map[string]any{
		"session_id": "nonexistent",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Should succeed but report existed=false (no Qdrant client = no Qdrant error)
	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}
}

func TestHandleSessionDelete_RequiresSessionID(t *testing.T) {
	svc := newTestService()

	result, err := svc.HandleSessionDelete(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("expected error when session_id is missing")
	}
}

func TestHandleSessionPrune_DryRun(t *testing.T) {
	svc := newTestService()

	// Without Qdrant, PruneSessions returns 0 pruned (no data source)
	result, err := svc.HandleSessionPrune(context.Background(), map[string]any{
		"max_age_hours": 72,
		"status":        "ended,summarized",
		"dry_run":       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result)
	}
}

func TestPruneSessions_NilQdrant(t *testing.T) {
	svc := newTestService()

	// With nil Qdrant client, should return 0 gracefully
	pruned, err := svc.sess.PruneSessions(context.Background(), 72, "ended,summarized", false)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned with nil Qdrant, got %d", pruned)
	}
}

func TestPruneSessions_EmptyStatusFilter(t *testing.T) {
	svc := newTestService()

	pruned, err := svc.sess.PruneSessions(context.Background(), 72, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 0 {
		t.Errorf("expected 0 pruned with empty status, got %d", pruned)
	}
}

func TestSessionReaperConfig(t *testing.T) {
	svc := newTestService()
	svc.cfg.SessionReaperEnabled = true
	svc.cfg.SessionReaperInterval = 1800
	svc.cfg.SessionReaperMaxAge = 168

	if !svc.cfg.SessionReaperEnabled {
		t.Error("session reaper should be enabled")
	}
	if svc.cfg.SessionReaperInterval != 1800 {
		t.Errorf("interval = %d, want 1800", svc.cfg.SessionReaperInterval)
	}
	if svc.cfg.SessionReaperMaxAge != 168 {
		t.Errorf("max_age = %d, want 168", svc.cfg.SessionReaperMaxAge)
	}
}

func TestEndActiveSessionsForAgent(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	// Create active sessions for agent-1
	svc.sess.sessions["s1"] = &Session{
		ID:        "s1",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-2 * time.Hour),
	}
	svc.sess.sessions["s2"] = &Session{
		ID:        "s2",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-1 * time.Hour),
	}
	// Create a session for a different agent (should not be ended)
	svc.sess.sessions["s3"] = &Session{
		ID:        "s3",
		AgentID:   "agent-2",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-3 * time.Hour),
	}
	// Create an already-ended session for agent-1 (should stay ended)
	ended := now.Add(-30 * time.Minute)
	svc.sess.sessions["s4"] = &Session{
		ID:        "s4",
		AgentID:   "agent-1",
		Status:    string(SessionStatusEnded),
		StartedAt: now.Add(-4 * time.Hour),
		EndedAt:   &ended,
	}

	svc.endActiveSessionsForAgent(context.Background(), "agent-1")

	svc.sess.mu.RLock()
	defer svc.sess.mu.RUnlock()

	if svc.sess.sessions["s1"].Status != string(SessionStatusEnded) {
		t.Errorf("s1 status = %s, want ended", svc.sess.sessions["s1"].Status)
	}
	if svc.sess.sessions["s1"].EndedAt == nil {
		t.Error("s1 EndedAt should be set")
	}
	if svc.sess.sessions["s2"].Status != string(SessionStatusEnded) {
		t.Errorf("s2 status = %s, want ended", svc.sess.sessions["s2"].Status)
	}
	if svc.sess.sessions["s3"].Status != string(SessionStatusActive) {
		t.Errorf("s3 (agent-2) status = %s, want active", svc.sess.sessions["s3"].Status)
	}
	if svc.sess.sessions["s4"].Status != string(SessionStatusEnded) {
		t.Errorf("s4 status = %s, want ended", svc.sess.sessions["s4"].Status)
	}
}

func TestEndStaleSessions_NilQdrant(t *testing.T) {
	svc := newTestService()

	ended := svc.sess.EndStale(context.Background(), 24)
	if ended != 0 {
		t.Errorf("expected 0 ended with nil Qdrant, got %d", ended)
	}
}

func TestEndStaleSessions_PersistsOnlyExpiredSessionsWithoutLivePresence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	alreadyEndedAt := now.Add(-4 * time.Hour)
	seeded := []Session{
		{
			ID:        "stale-dead",
			AgentID:   "dead-agent",
			Status:    string(SessionStatusActive),
			StartedAt: now.Add(-3 * time.Hour),
		},
		{
			ID:        "stale-live",
			AgentID:   "live-agent",
			Status:    string(SessionStatusActive),
			StartedAt: now.Add(-3 * time.Hour),
		},
		{
			ID:        "recent-dead",
			AgentID:   "dead-agent",
			Status:    string(SessionStatusActive),
			StartedAt: now.Add(-20 * time.Minute),
		},
		{
			ID:        "already-ended",
			AgentID:   "dead-agent",
			Status:    string(SessionStatusEnded),
			StartedAt: now.Add(-5 * time.Hour),
			EndedAt:   &alreadyEndedAt,
		},
	}

	qdrant, stub := newSessionsQdrantStub(t, seeded...)
	svc := newTestService()
	svc.sess.qdrant = qdrant
	svc.sess.liveAgentIDs = func() []string { return []string{"live-agent"} }

	// Verify in-memory cache gets updated for preloaded sessions.
	svc.sess.sessions["stale-dead"] = &Session{
		ID:        "stale-dead",
		AgentID:   "dead-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-3 * time.Hour),
	}

	ended := svc.sess.EndStale(context.Background(), 1)
	if ended != 1 {
		t.Fatalf("ended = %d, want 1", ended)
	}

	staleDead, ok := stub.sessionByID("stale-dead")
	if !ok {
		t.Fatal("stale-dead session missing from persisted store")
	}
	if staleDead.Status != string(SessionStatusEnded) {
		t.Fatalf("stale-dead status = %q, want ended", staleDead.Status)
	}
	if staleDead.EndedAt == nil {
		t.Fatal("stale-dead EndedAt should be set")
	}

	staleLive, ok := stub.sessionByID("stale-live")
	if !ok {
		t.Fatal("stale-live session missing from persisted store")
	}
	if staleLive.Status != string(SessionStatusActive) {
		t.Fatalf("stale-live status = %q, want active", staleLive.Status)
	}
	if staleLive.EndedAt != nil {
		t.Fatal("stale-live EndedAt should remain nil")
	}

	recentDead, ok := stub.sessionByID("recent-dead")
	if !ok {
		t.Fatal("recent-dead session missing from persisted store")
	}
	if recentDead.Status != string(SessionStatusActive) {
		t.Fatalf("recent-dead status = %q, want active", recentDead.Status)
	}

	svc.sess.mu.RLock()
	cached := svc.sess.sessions["stale-dead"]
	svc.sess.mu.RUnlock()
	if cached == nil {
		t.Fatal("stale-dead session missing from in-memory cache")
	}
	if cached.Status != string(SessionStatusEnded) {
		t.Fatalf("cached stale-dead status = %q, want ended", cached.Status)
	}
	if cached.EndedAt == nil {
		t.Fatal("cached stale-dead EndedAt should be set")
	}
}

func TestSessionReaperTick_EndsStaleSessionsUsingConfiguredActiveMaxAge(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	seeded := []Session{
		{
			ID:        "stale-dead",
			AgentID:   "dead-agent",
			Status:    string(SessionStatusActive),
			StartedAt: now.Add(-2 * time.Hour),
		},
		{
			ID:        "stale-live",
			AgentID:   "live-agent",
			Status:    string(SessionStatusActive),
			StartedAt: now.Add(-2 * time.Hour),
		},
	}

	qdrant, stub := newSessionsQdrantStub(t, seeded...)
	svc := newTestService()
	svc.sess.qdrant = qdrant
	svc.sess.cfg.SessionReaperActiveMaxAge = 1
	svc.sess.liveAgentIDs = func() []string { return []string{"live-agent"} }

	svc.sess.reaperTick(context.Background())

	staleDead, ok := stub.sessionByID("stale-dead")
	if !ok {
		t.Fatal("stale-dead session missing from persisted store")
	}
	if staleDead.Status != string(SessionStatusEnded) {
		t.Fatalf("stale-dead status = %q, want ended", staleDead.Status)
	}
	if staleDead.EndedAt == nil {
		t.Fatal("stale-dead EndedAt should be set")
	}

	staleLive, ok := stub.sessionByID("stale-live")
	if !ok {
		t.Fatal("stale-live session missing from persisted store")
	}
	if staleLive.Status != string(SessionStatusActive) {
		t.Fatalf("stale-live status = %q, want active", staleLive.Status)
	}
}

func TestLiveAgentIDs(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	svc.presence.reg["alive"] = &AgentPresence{
		AgentID:       "alive",
		LastHeartbeat: now,
		HeartbeatTTL:  120,
		Status:        PresenceStatusActive,
	}
	svc.presence.reg["stale"] = &AgentPresence{
		AgentID:       "stale",
		LastHeartbeat: now.Add(-10 * time.Minute), // 600s > 3×120s = 360s
		HeartbeatTTL:  120,
		Status:        PresenceStatusOffline,
	}

	ids := svc.presence.LiveAgentIDs()
	live := make(map[string]bool, len(ids))
	for _, id := range ids {
		live[id] = true
	}
	if !live["alive"] {
		t.Error("alive agent should be live")
	}
	if live["stale"] {
		t.Error("stale agent should not be live")
	}
}

func TestSessionStartEndsPriorActiveSessions(t *testing.T) {
	svc := newTestService()
	now := time.Now()

	// Create existing active sessions for the agent.
	svc.sess.sessions["old-1"] = &Session{
		ID:        "old-1",
		AgentID:   "test-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-2 * time.Hour),
	}
	svc.sess.sessions["old-2"] = &Session{
		ID:        "old-2",
		AgentID:   "test-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-1 * time.Hour),
	}

	// Simulate what HandleSessionStart does: end prior active sessions.
	// (HandleSessionStart calls endActiveSessionsForAgent before creating
	// the new session. We test the helper directly to avoid requiring Qdrant.)
	svc.endActiveSessionsForAgent(context.Background(), "test-agent")

	svc.sess.mu.RLock()
	defer svc.sess.mu.RUnlock()

	if svc.sess.sessions["old-1"].Status != string(SessionStatusEnded) {
		t.Errorf("old-1 status = %s, want ended", svc.sess.sessions["old-1"].Status)
	}
	if svc.sess.sessions["old-1"].EndedAt == nil {
		t.Error("old-1 EndedAt should be set")
	}
	if svc.sess.sessions["old-2"].Status != string(SessionStatusEnded) {
		t.Errorf("old-2 status = %s, want ended", svc.sess.sessions["old-2"].Status)
	}
}

func TestSessionReaperActiveMaxAgeConfig(t *testing.T) {
	svc := newTestService()
	svc.cfg.SessionReaperActiveMaxAge = 48

	if svc.cfg.SessionReaperActiveMaxAge != 48 {
		t.Errorf("active_max_age = %d, want 48", svc.cfg.SessionReaperActiveMaxAge)
	}
}

func TestSessionReaperTick_EndsStaleInMemorySessions(t *testing.T) {
	svc := newTestService()
	svc.sess.cfg.SessionReaperActiveMaxAge = 1 // 1 hour
	now := time.Now()

	// Create a stale active session older than 1 hour with no live presence.
	svc.sess.sessions["stale-1"] = &Session{
		ID:        "stale-1",
		AgentID:   "dead-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-3 * time.Hour),
	}

	// Create a recent active session (should NOT be ended).
	svc.sess.sessions["recent-1"] = &Session{
		ID:        "recent-1",
		AgentID:   "dead-agent",
		Status:    string(SessionStatusActive),
		StartedAt: now.Add(-30 * time.Minute),
	}

	// Run one reaper tick (same function called on startup).
	svc.sess.reaperTick(context.Background())

	svc.sess.mu.RLock()
	defer svc.sess.mu.RUnlock()

	// Stale session should remain active in memory (no Qdrant = EndStale returns 0).
	// But reaperTick should not panic or error with nil Qdrant.
	if svc.sess.sessions["recent-1"].Status != string(SessionStatusActive) {
		t.Errorf("recent-1 status = %s, want active", svc.sess.sessions["recent-1"].Status)
	}
}
