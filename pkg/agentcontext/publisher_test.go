package agentcontext

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakePublisher captures everything published. Safe for concurrent use.
type fakePublisher struct {
	mu     sync.Mutex
	events []capturedEvent
}

type capturedEvent struct {
	Type    string
	Payload any
}

func (f *fakePublisher) Publish(eventType string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, capturedEvent{Type: eventType, Payload: payload})
}

func (f *fakePublisher) byType(t string) []capturedEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []capturedEvent
	for _, e := range f.events {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

func TestSessionSvc_DefaultPublisherIsNoop(t *testing.T) {
	svc, _ := newSessionServiceWithQdrant(t)
	if svc.sess.publisher == nil {
		t.Fatal("default publisher should be set (noop), not nil")
	}
	// Should not panic and should be safe to call.
	svc.sess.publisher.Publish("anything", nil)
}

func TestSessionSvc_SetPublisher_NilResetsToNoop(t *testing.T) {
	svc, _ := newSessionServiceWithQdrant(t)
	svc.sess.SetPublisher(&fakePublisher{})
	svc.sess.SetPublisher(nil)
	if _, isNoop := svc.sess.publisher.(noopPublisher); !isNoop {
		t.Errorf("nil should reset to noopPublisher, got %T", svc.sess.publisher)
	}
}

func TestSessionStart_PublishesSessionStartEvent(t *testing.T) {
	svc, _ := newSessionServiceWithQdrant(t)
	pub := &fakePublisher{}
	svc.sess.SetPublisher(pub)

	res, err := svc.sess.Start(context.Background(), map[string]any{
		"agent_id":    "agent-pub-test",
		"namespace":   "ns-test",
		"description": "publisher-test",
	})
	if err != nil || res == nil {
		t.Fatalf("Start failed: err=%v res=%v", err, res)
	}

	starts := pub.byType(EventTypeSessionStart)
	if len(starts) != 1 {
		t.Fatalf("expected 1 session.start event, got %d (events: %+v)", len(starts), pub.events)
	}
	payload, ok := starts[0].Payload.(SessionStartEvent)
	if !ok {
		t.Fatalf("payload is wrong type: %T", starts[0].Payload)
	}
	if payload.AgentID != "agent-pub-test" {
		t.Errorf("AgentID = %q, want agent-pub-test", payload.AgentID)
	}
	if payload.Namespace != "ns-test" {
		t.Errorf("Namespace = %q, want ns-test", payload.Namespace)
	}
	if payload.Description != "publisher-test" {
		t.Errorf("Description = %q, want publisher-test", payload.Description)
	}
	if payload.SessionID == "" {
		t.Error("SessionID empty")
	}
	if payload.StartedAt.IsZero() {
		t.Error("StartedAt unset")
	}
}

func TestSessionEnd_PublishesSessionEndEventWithDurationAndCount(t *testing.T) {
	svc, _ := newSessionServiceWithQdrant(t)
	pub := &fakePublisher{}
	svc.sess.SetPublisher(pub)

	// Seed an active session 250ms in the past with a known entry count.
	startedAt := time.Now().Add(-250 * time.Millisecond)
	session := &Session{
		ID:         "sess-end-test",
		AgentID:    "agent-end",
		Namespace:  "ns-end",
		Status:     string(SessionStatusActive),
		StartedAt:  startedAt,
		EntryCount: 17,
	}
	svc.sess.sessions[session.ID] = session

	res, err := svc.sess.End(context.Background(), map[string]any{
		"session_id": session.ID,
		"summarize":  false,
		"cleanup":    false,
	})
	if err != nil || res == nil {
		t.Fatalf("End failed: err=%v res=%v", err, res)
	}

	ends := pub.byType(EventTypeSessionEnd)
	if len(ends) != 1 {
		t.Fatalf("expected 1 session.end event, got %d", len(ends))
	}
	payload, ok := ends[0].Payload.(SessionEndEvent)
	if !ok {
		t.Fatalf("payload is wrong type: %T", ends[0].Payload)
	}
	if payload.SessionID != session.ID {
		t.Errorf("SessionID = %q, want %q", payload.SessionID, session.ID)
	}
	if payload.AgentID != "agent-end" {
		t.Errorf("AgentID = %q", payload.AgentID)
	}
	if payload.EntryCount != 17 {
		t.Errorf("EntryCount = %d, want 17", payload.EntryCount)
	}
	if payload.DurationMs < 200 {
		// Allow slop for clock granularity but should be >= ~250ms.
		t.Errorf("DurationMs = %d, expected ≥ 200", payload.DurationMs)
	}
	if payload.EndedAt.IsZero() {
		t.Error("EndedAt unset")
	}
}

func TestPresenceTransition_PublishesAgentStatusChange(t *testing.T) {
	svc := newTestService()
	pub := &fakePublisher{}
	svc.presence.SetPublisher(pub)

	ttl := 10
	p := newTestPresence("agent-trans", ttl)
	svc.presence.reg[p.AgentID] = p

	// Force active → idle by aging the heartbeat past 1×TTL.
	p.LastHeartbeat = time.Now().Add(-time.Duration(ttl+1) * time.Second)
	svc.presence.cleanupExpired(context.TODO())

	changes := pub.byType(EventTypeAgentStatusChange)
	if len(changes) != 1 {
		t.Fatalf("expected 1 agent.status.change, got %d", len(changes))
	}
	payload, ok := changes[0].Payload.(AgentStatusChangeEvent)
	if !ok {
		t.Fatalf("payload type: %T", changes[0].Payload)
	}
	if payload.AgentID != "agent-trans" {
		t.Errorf("AgentID = %q", payload.AgentID)
	}
	if payload.OldStatus != string(PresenceStatusActive) {
		t.Errorf("OldStatus = %q, want active", payload.OldStatus)
	}
	if payload.NewStatus != string(PresenceStatusIdle) {
		t.Errorf("NewStatus = %q, want idle", payload.NewStatus)
	}
	if payload.ChangedAt.IsZero() {
		t.Error("ChangedAt unset")
	}
}

func TestService_SetPublisher_PropagatesToBothSubServices(t *testing.T) {
	svc := newTestService()
	pub := &fakePublisher{}
	svc.SetPublisher(pub)

	if svc.sess.publisher != Publisher(pub) {
		t.Error("SessionSvc publisher not propagated")
	}
	if svc.presence.publisher != Publisher(pub) {
		t.Error("PresenceSvc publisher not propagated")
	}

	svc.SetPublisher(nil)
	if _, isNoop := svc.sess.publisher.(noopPublisher); !isNoop {
		t.Error("SessionSvc not reset to noop")
	}
	if _, isNoop := svc.presence.publisher.(noopPublisher); !isNoop {
		t.Error("PresenceSvc not reset to noop")
	}
}
