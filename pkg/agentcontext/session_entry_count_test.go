package agentcontext

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestIncrementStats_PersistsToQdrant verifies the basic writer invariant
// behind the writer-side bug: after IncrementStats, Qdrant has the non-zero
// entry_count. Goes through SessionSvc.IncrementStats, the production hot
// path called from agent_context_add.
func TestIncrementStats_PersistsToQdrant(t *testing.T) {
	svc, stub := newSessionServiceWithQdrant(t)

	sess := &Session{
		ID:        "S-single",
		AgentID:   "agent-1",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now(),
	}
	svc.sess.mu.Lock()
	svc.sess.sessions[sess.ID] = sess
	svc.sess.mu.Unlock()
	if err := svc.sess.Persist(context.Background(), sess); err != nil {
		t.Fatalf("initial persist: %v", err)
	}

	svc.sess.IncrementStats(context.Background(), sess, 5, 100)

	persisted, ok := stub.sessionByID(sess.ID)
	if !ok {
		t.Fatal("session missing in Qdrant")
	}
	if persisted.EntryCount != 5 {
		t.Errorf("Qdrant EntryCount = %d, want 5", persisted.EntryCount)
	}
	if persisted.TotalTokens != 100 {
		t.Errorf("Qdrant TotalTokens = %d, want 100", persisted.TotalTokens)
	}
}

// TestIncrementStats_FreshSubprocessAccumulates simulates subprocess churn:
// subprocess A increments, subprocess B starts fresh (empty in-memory map),
// loads from Qdrant, increments again. After both, Qdrant must reflect the
// cumulative count — proving the writer is durable across process boundaries.
func TestIncrementStats_FreshSubprocessAccumulates(t *testing.T) {
	svcA, stub := newSessionServiceWithQdrant(t)

	sess := &Session{
		ID:        "S-fresh",
		AgentID:   "agent-fresh",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now(),
	}
	svcA.sess.mu.Lock()
	svcA.sess.sessions[sess.ID] = sess
	svcA.sess.mu.Unlock()
	if err := svcA.sess.Persist(context.Background(), sess); err != nil {
		t.Fatalf("A initial persist: %v", err)
	}
	svcA.sess.IncrementStats(context.Background(), sess, 3, 60)

	// Subprocess B: brand-new SessionSvc that points at the same Qdrant.
	svcB := &Service{cfg: svcA.cfg, logger: svcA.logger, metrics: svcA.metrics}
	svcB.sess = NewSessionSvc(svcA.sess.qdrant, svcA.cfg, svcA.logger, svcA.metrics)
	if err := svcB.sess.LoadFromQdrant(context.Background()); err != nil {
		t.Fatalf("B LoadFromQdrant: %v", err)
	}

	loaded, err := svcB.sess.Get(context.Background(), sess.ID)
	if err != nil || loaded == nil {
		t.Fatalf("B Get: err=%v sess=%v", err, loaded)
	}
	if loaded.EntryCount != 3 {
		t.Errorf("B loaded EntryCount = %d, want 3", loaded.EntryCount)
	}

	svcB.sess.IncrementStats(context.Background(), loaded, 2, 40)

	persisted, ok := stub.sessionByID(sess.ID)
	if !ok {
		t.Fatal("session missing in Qdrant")
	}
	if persisted.EntryCount != 5 {
		t.Errorf("Qdrant EntryCount = %d, want 5 (3+2)", persisted.EntryCount)
	}
	if persisted.TotalTokens != 100 {
		t.Errorf("Qdrant TotalTokens = %d, want 100 (60+40)", persisted.TotalTokens)
	}
}

// TestIncrementStats_ConcurrentAddsSharedPointer is the production hot path
// under load: many concurrent agent_context_add calls in the same subprocess
// share an in-memory session pointer. Final Qdrant state must equal the sum
// of all increments. Run with -race; the previous goroutine-based writer
// raced on session.EntryCount via SessionToPayload(*session).
func TestIncrementStats_ConcurrentAddsSharedPointer(t *testing.T) {
	svc, stub := newSessionServiceWithQdrant(t)

	sess := &Session{
		ID:        "S-concurrent",
		AgentID:   "agent-c",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now(),
	}
	svc.sess.mu.Lock()
	svc.sess.sessions[sess.ID] = sess
	svc.sess.mu.Unlock()
	if err := svc.sess.Persist(context.Background(), sess); err != nil {
		t.Fatalf("initial persist: %v", err)
	}

	const adders = 10
	const perAdder = 3
	var wg sync.WaitGroup
	wg.Add(adders)
	for i := 0; i < adders; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perAdder; j++ {
				svc.sess.IncrementStats(context.Background(), sess, 1, 10)
			}
		}()
	}
	wg.Wait()

	wantEntries := adders * perAdder
	wantTokens := adders * perAdder * 10

	persisted, ok := stub.sessionByID(sess.ID)
	if !ok {
		t.Fatal("session missing in Qdrant")
	}
	if persisted.EntryCount != wantEntries {
		t.Errorf("Qdrant EntryCount = %d, want %d", persisted.EntryCount, wantEntries)
	}
	if persisted.TotalTokens != wantTokens {
		t.Errorf("Qdrant TotalTokens = %d, want %d", persisted.TotalTokens, wantTokens)
	}
}

// TestIncrementStats_SurvivesEndStaleClobber is the regression guard for the
// production bug. A concurrent reaper-style writer (EndStale) decodes a fresh
// session from Qdrant and full-upserts status=ended. Before the fix, the
// addSessionEntryStats path full-upserted via a goroutine that would lose the
// race with the reaper's full-upsert and the count would land at 0 (the
// reaper's snapshot). After the fix, IncrementStats uses SetPayload partial
// merge, so even if EndStale's full-upsert wins the timing race, the count
// gets layered back on top by the next IncrementStats merge — and a single
// IncrementStats call that lands AFTER EndStale must still leave count > 0.
func TestIncrementStats_SurvivesEndStaleClobber(t *testing.T) {
	svc, stub := newSessionServiceWithQdrant(t)
	svc.sess.liveAgentIDs = func() []string { return nil }

	sess := &Session{
		ID:        "S-race",
		AgentID:   "agent-race",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now().Add(-10 * time.Hour),
	}
	svc.sess.mu.Lock()
	svc.sess.sessions[sess.ID] = sess
	svc.sess.mu.Unlock()
	if err := svc.sess.Persist(context.Background(), sess); err != nil {
		t.Fatalf("initial persist: %v", err)
	}

	// Order 1: EndStale runs first, then IncrementStats.
	// EndStale full-upserts status=ended with whatever count was in
	// Qdrant (0 right now). IncrementStats then SetPayloads count=7.
	// Result: count=7 (merged), status=ended.
	_ = svc.sess.EndStale(context.Background(), 1)
	svc.sess.IncrementStats(context.Background(), sess, 7, 140)

	persisted, ok := stub.sessionByID(sess.ID)
	if !ok {
		t.Fatal("session missing in Qdrant")
	}
	if persisted.EntryCount != 7 {
		t.Errorf("Qdrant EntryCount = %d, want 7 (count must survive EndStale clobber)",
			persisted.EntryCount)
	}
	if persisted.TotalTokens != 140 {
		t.Errorf("Qdrant TotalTokens = %d, want 140", persisted.TotalTokens)
	}
	if persisted.Status != string(SessionStatusEnded) {
		t.Errorf("Qdrant Status = %q, want ended (EndStale's status survives)",
			persisted.Status)
	}
}

// TestIncrementStats_NotClobberedBySessionEnd locks in that a follow-up
// session_end (via SessionSvc.End → ss.Persist full upsert) carries the
// in-memory entry_count over rather than zeroing it. Reproduces the
// session lifecycle: start → 5 adds → end → reload from Qdrant → expect 5.
func TestIncrementStats_NotClobberedBySessionEnd(t *testing.T) {
	svc, stub := newSessionServiceWithQdrant(t)

	sess := &Session{
		ID:        "S-end",
		AgentID:   "agent-end",
		Status:    string(SessionStatusActive),
		StartedAt: time.Now(),
	}
	svc.sess.mu.Lock()
	svc.sess.sessions[sess.ID] = sess
	svc.sess.mu.Unlock()
	if err := svc.sess.Persist(context.Background(), sess); err != nil {
		t.Fatalf("initial persist: %v", err)
	}

	svc.sess.IncrementStats(context.Background(), sess, 5, 100)

	// Now end the session. End() reads the in-memory pointer (which has
	// the incremented values) and full-upserts. The count must come along.
	if _, err := svc.sess.End(context.Background(), map[string]any{
		"session_id": sess.ID,
		"summarize":  false,
		"cleanup":    false,
	}); err != nil {
		t.Fatalf("End: %v", err)
	}

	persisted, ok := stub.sessionByID(sess.ID)
	if !ok {
		t.Fatal("session missing in Qdrant")
	}
	if persisted.EntryCount != 5 {
		t.Errorf("Qdrant EntryCount = %d, want 5", persisted.EntryCount)
	}
	if persisted.Status != string(SessionStatusEnded) {
		t.Errorf("Qdrant Status = %q, want ended", persisted.Status)
	}
}
