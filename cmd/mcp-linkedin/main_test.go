package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/httpclient"
)

func newTestLinkedInServer(baseURL string) *linkedInServer {
	return &linkedInServer{
		baseURL:    baseURL,
		mode:       linkedinModeAuto,
		httpClient: httpclient.NewDefault(),
	}
}

func TestParseLinkedInMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default auto", input: "", want: linkedinModeAuto},
		{name: "official", input: "official", want: linkedinModeOfficial},
		{name: "experimental", input: "experimental", want: linkedinModeExperimental},
		{name: "trim + case", input: "  AuTo  ", want: linkedinModeAuto},
		{name: "invalid", input: "bad-mode", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLinkedInMode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got mode=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseLinkedInMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeJSessionID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"quoted", "\"ajax:123\"", "\"ajax:123\""},
		{"unquoted", "ajax:123", "\"ajax:123\""},
		{"trim", "  ajax:123  ", "\"ajax:123\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeJSessionID(tt.input)
			if got != tt.want {
				t.Fatalf("normalizeJSessionID(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReadStringSliceArg(t *testing.T) {
	got := readStringSliceArg([]any{" urn:1 ", 12, "", "urn:2"})
	if len(got) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(got))
	}
	if got[0] != "urn:1" || got[1] != "urn:2" {
		t.Fatalf("unexpected values: %#v", got)
	}
}

func TestHandleGetConversationMessages_MissingConversationURN(t *testing.T) {
	s := newTestLinkedInServer("http://example.com")
	s.sessionToken = "cookie"

	result, err := s.handleGetConversationMessages(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected MCP error result")
	}
}

func TestHandleSendMessage_RequiresTarget(t *testing.T) {
	s := newTestLinkedInServer("http://example.com")
	s.mode = linkedinModeExperimental
	s.sessionToken = "cookie"
	s.jsessionID = "ajax:123"

	result, err := s.handleSendMessage(context.Background(), map[string]any{
		"text": "hello",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected MCP error result")
	}
}

func TestHandleAuthStatus(t *testing.T) {
	s := &linkedInServer{
		mode:         linkedinModeAuto,
		accessToken:  "token-123",
		sessionToken: "cookie-123",
		jsessionID:   "ajax:123",
		httpClient:   httpclient.NewDefault(),
	}

	result, err := s.handleAuthStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error")
	}
	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "mode: auto") {
		t.Fatalf("expected mode in response, got: %+v", result.Content)
	}
}

func TestHandleListConversations_OfficialModeBlocked(t *testing.T) {
	s := newTestLinkedInServer("http://example.com")
	s.mode = linkedinModeOfficial
	s.accessToken = "token-123"

	result, err := s.handleListConversations(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected MCP error result")
	}
}

func TestHandleSendMessage_RequiresJSessionID(t *testing.T) {
	s := newTestLinkedInServer("http://example.com")
	s.mode = linkedinModeExperimental
	s.sessionToken = "cookie-123"

	result, err := s.handleSendMessage(context.Background(), map[string]any{
		"text":       "hello",
		"recipients": []any{"urn:li:fs_miniProfile:abc"},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected MCP error result")
	}
}

func TestRequestJSON_SetsBearerToken(t *testing.T) {
	var gotAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := &linkedInServer{
		baseURL:     ts.URL,
		accessToken: "token-123",
		httpClient:  httpclient.NewDefault(),
	}

	_, err := s.requestJSON(context.Background(), http.MethodGet, "/me", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAuth != "Bearer token-123" {
		t.Fatalf("expected bearer token header, got %q", gotAuth)
	}
}

func TestRequestJSON_SetsCookieAndCSRFHeaders(t *testing.T) {
	var gotCookie string
	var gotCSRF string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotCSRF = r.Header.Get("csrf-token")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	s := &linkedInServer{
		baseURL:      ts.URL,
		sessionToken: "li-at-cookie",
		jsessionID:   "ajax:123",
		httpClient:   httpclient.NewDefault(),
	}

	_, err := s.requestJSON(context.Background(), http.MethodGet, "/me", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(gotCookie, "li_at=li-at-cookie") {
		t.Fatalf("expected li_at cookie, got %q", gotCookie)
	}
	if !strings.Contains(gotCookie, "JSESSIONID=\"ajax:123\"") {
		t.Fatalf("expected quoted JSESSIONID cookie, got %q", gotCookie)
	}
	if gotCSRF != "ajax:123" {
		t.Fatalf("expected csrf-token header, got %q", gotCSRF)
	}
}
