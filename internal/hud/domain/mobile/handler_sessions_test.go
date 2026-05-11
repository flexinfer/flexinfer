package mobile

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crb2nu/loom/internal/hud/bridge"
)

type sessionTreeCaller struct {
	sessions []bridge.SessionInfo
}

func (c *sessionTreeCaller) Call(string, any) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected Call")
}

func (c *sessionTreeCaller) CallWithTimeout(string, any, time.Duration) (json.RawMessage, error) {
	return nil, fmt.Errorf("unexpected CallWithTimeout")
}

func (c *sessionTreeCaller) CallTool(name string, args map[string]any) (json.RawMessage, error) {
	return c.CallToolWithTimeout(name, args, 0)
}

func (c *sessionTreeCaller) CallToolWithTimeout(name string, _ map[string]any, _ time.Duration) (json.RawMessage, error) {
	if name != "agent_context__agent_session_list" {
		return nil, fmt.Errorf("unexpected tool %s", name)
	}
	return json.Marshal(map[string]any{"sessions": c.sessions})
}

func (c *sessionTreeCaller) CircuitOpen() bool { return false }
func (c *sessionTreeCaller) Close() error      { return nil }

func TestBuildMobileSessionTree_NormalizesRootsAndOrphans(t *testing.T) {
	updatedAt := time.Date(2026, 5, 11, 14, 0, 0, 0, time.UTC)
	resp := buildMobileSessionTree([]bridge.SessionInfo{
		{ID: "root", AgentID: "codex", Status: "active"},
		{ID: "child", AgentID: "codex-sub", ParentSessionID: "root", RootSessionID: "root", Status: "active"},
		{ID: "ended-child", AgentID: "codex-sub", ParentSessionID: "root", RootSessionID: "root", Status: "ended"},
		{ID: "orphan", AgentID: "lost", ParentSessionID: "missing-parent", Status: "active"},
		{ID: "session-only", AgentID: "codex-session-only", Status: "active"},
	}, updatedAt)

	if resp.Summary.RootCount != 2 {
		t.Fatalf("expected 2 roots, got %+v", resp.Summary)
	}
	if resp.Summary.ActiveSessions != 4 {
		t.Fatalf("expected 4 active sessions, got %+v", resp.Summary)
	}
	if resp.Summary.OrphanSessions != 1 {
		t.Fatalf("expected 1 orphan, got %+v", resp.Summary)
	}
	if resp.Roots[0].Session.RootSessionID != "root" {
		t.Fatalf("expected root session root id to normalize to itself, got %q", resp.Roots[0].Session.RootSessionID)
	}
	if resp.Roots[0].ChildCount != 2 || resp.Roots[0].ActiveChildCount != 1 {
		t.Fatalf("unexpected root child counts: %+v", resp.Roots[0])
	}
	if resp.Orphans[0].Session.ID != "orphan" {
		t.Fatalf("expected orphan bucket to include missing-parent child, got %+v", resp.Orphans)
	}
}

func TestHandleMobileSessionsTree(t *testing.T) {
	deps := newTestMockDeps()
	deps.agent = bridge.NewAgentBridge(&sessionTreeCaller{sessions: []bridge.SessionInfo{
		{ID: "root", AgentID: "codex", Status: "active"},
		{ID: "child", AgentID: "codex-sub", ParentSessionID: "root", RootSessionID: "root", Status: "active"},
	}})
	d := New(deps)

	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	req := newAuthRequest("GET", "/api/mobile/v1/sessions/tree?status=all")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var env struct {
		OK   bool                `json:"ok"`
		Data SessionTreeResponse `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("expected ok envelope: %s", rec.Body.String())
	}
	if len(env.Data.Roots) != 1 || env.Data.Roots[0].ChildCount != 1 {
		t.Fatalf("unexpected tree response: %+v", env.Data)
	}
}

func TestRouteRegistration_SessionsTree(t *testing.T) {
	deps := newTestMockDeps()
	d := New(deps)

	mux := http.NewServeMux()
	d.RegisterRoutes(mux, func(h http.HandlerFunc) http.HandlerFunc { return h })

	req := httptest.NewRequest("GET", "/api/mobile/v1/sessions/tree", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatal("expected sessions tree route to be registered, got 404")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 (route exists but no auth), got %d", rec.Code)
	}
}
