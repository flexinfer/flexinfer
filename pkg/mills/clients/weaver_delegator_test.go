package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/mills/pipeline"
)

// helper: build a delegator wired to a roundtrip stub.
func newStubDelegator(t *testing.T, rt roundTripFn) *WeaverHTTPDelegator {
	t.Helper()
	d, err := NewWeaverHTTPDelegator(WeaverHTTPConfig{
		BaseURL: "http://stub",
		AgentID: "loom-mills-operator",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	d.SetTransport(rt)
	return d
}

func TestNewWeaverHTTPDelegator_RequiresBaseURL(t *testing.T) {
	if _, err := NewWeaverHTTPDelegator(WeaverHTTPConfig{}); err == nil {
		t.Error("expected error when BaseURL empty")
	}
	if _, err := NewWeaverHTTPDelegator(WeaverHTTPConfig{BaseURL: "  "}); err == nil {
		t.Error("expected error when BaseURL is whitespace only")
	}
}

func TestWeaverHTTPDelegator_Delegate_Success(t *testing.T) {
	var captured queryRequestBody
	d := newStubDelegator(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", req.Method)
		}
		if req.URL.Path != "/api/weaver/query" {
			t.Errorf("path = %s, want /api/weaver/query", req.URL.Path)
		}
		body, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(body, &captured); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		respBody := `{
			"answer": "k3s nodes are healthy",
			"domain_results": [
				{"domain":"cluster-ops","answer":"k3s nodes are healthy","tokens":120,"latency_ms":850}
			],
			"total_tokens": 120,
			"latency_ms": 850,
			"domains_used": ["cluster-ops"]
		}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(respBody)),
			Header:     make(http.Header),
		}, nil
	})

	resp, err := d.Delegate(context.Background(), pipeline.WeaverRequest{
		RunID:     "RUN-77",
		BacklogID: "BL-42",
		Prompt:    "check k3s health",
	})
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	// Forwarded body sanity checks.
	if captured.Query != "check k3s health" {
		t.Errorf("forwarded query = %q, want %q", captured.Query, "check k3s health")
	}
	if captured.AgentID != "loom-mills-operator" {
		t.Errorf("forwarded agent_id = %q, want loom-mills-operator", captured.AgentID)
	}
	if captured.SessionID != "RUN-77" {
		t.Errorf("forwarded session_id = %q, want RUN-77 (run id flow)", captured.SessionID)
	}
	if captured.ParentSessionID != "RUN-77" {
		t.Errorf("forwarded parent_session_id = %q, want RUN-77", captured.ParentSessionID)
	}

	// Response mapping.
	if resp.Notes != "k3s nodes are healthy" {
		t.Errorf("Notes = %q, want %q", resp.Notes, "k3s nodes are healthy")
	}
	if resp.SpawnID != "weaver-router" {
		t.Errorf("SpawnID = %q, want weaver-router", resp.SpawnID)
	}
	if resp.Citation["total_tokens"].(int) != 120 {
		t.Errorf("Citation[total_tokens] = %v, want 120", resp.Citation["total_tokens"])
	}
	if !strings.Contains(resp.LogTail, "cluster-ops=120t") {
		t.Errorf("LogTail = %q, want cluster-ops=120t", resp.LogTail)
	}
}

func TestWeaverHTTPDelegator_Delegate_4xxBecomesError(t *testing.T) {
	d := newStubDelegator(t, func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 400,
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"query is required"}`)),
			Header:     make(http.Header),
		}, nil
	})
	_, err := d.Delegate(context.Background(), pipeline.WeaverRequest{Prompt: "p"})
	if err == nil {
		t.Fatal("expected error on 400 status")
	}
	if !strings.Contains(err.Error(), "status 400") {
		t.Errorf("error should mention status 400; got %q", err.Error())
	}
}

func TestWeaverHTTPDelegator_Delegate_5xxBecomesError(t *testing.T) {
	d := newStubDelegator(t, func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 502,
			Body:       io.NopCloser(bytes.NewBufferString(`{"error":"weaver query failed"}`)),
			Header:     make(http.Header),
		}, nil
	})
	_, err := d.Delegate(context.Background(), pipeline.WeaverRequest{Prompt: "p"})
	if err == nil {
		t.Fatal("expected error on 502 status")
	}
}

func TestWeaverHTTPDelegator_Delegate_RequiresPrompt(t *testing.T) {
	d := newStubDelegator(t, func(_ *http.Request) (*http.Response, error) {
		t.Fatal("HTTP should not be called for empty prompt")
		return nil, nil
	})
	if _, err := d.Delegate(context.Background(), pipeline.WeaverRequest{Prompt: "  "}); err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestWeaverHTTPDelegator_Delegate_TransportErrorPropagates(t *testing.T) {
	d := newStubDelegator(t, func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})
	_, err := d.Delegate(context.Background(), pipeline.WeaverRequest{Prompt: "p"})
	if err == nil {
		t.Fatal("expected transport error to surface")
	}
	if !strings.Contains(err.Error(), "POST:") {
		t.Errorf("error should be wrapped with POST: prefix; got %q", err.Error())
	}
}

func TestWeaverHTTPDelegator_Delegate_InvalidJSONBecomesError(t *testing.T) {
	d := newStubDelegator(t, func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(`not json`)),
			Header:     make(http.Header),
		}, nil
	})
	_, err := d.Delegate(context.Background(), pipeline.WeaverRequest{Prompt: "p"})
	if err == nil {
		t.Fatal("expected JSON decode error")
	}
}

func TestWeaverHTTPDelegator_Delegate_TokenHeaderForwarded(t *testing.T) {
	d, err := NewWeaverHTTPDelegator(WeaverHTTPConfig{
		BaseURL: "http://stub",
		Token:   "secret-123",
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	var seen string
	d.SetTransport(roundTripFn(func(req *http.Request) (*http.Response, error) {
		seen = req.Header.Get("Authorization")
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(`{"answer":"x","domain_results":[],"total_tokens":0,"latency_ms":0,"domains_used":[]}`)),
			Header:     make(http.Header),
		}, nil
	}))
	if _, err := d.Delegate(context.Background(), pipeline.WeaverRequest{Prompt: "p"}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if seen != "Bearer secret-123" {
		t.Errorf("Authorization = %q, want Bearer secret-123", seen)
	}
}

func TestSummarizeDomainResults(t *testing.T) {
	cases := []struct {
		name string
		in   []queryDomainResultBody
		want string
	}{
		{name: "empty", in: nil, want: "weaver: 0 domains"},
		{
			name: "single",
			in:   []queryDomainResultBody{{Domain: "codebase", Tokens: 210}},
			want: "weaver: 1 domains (codebase=210t)",
		},
		{
			name: "multi with error",
			in: []queryDomainResultBody{
				{Domain: "codebase", Tokens: 210},
				{Domain: "ci-pipeline", Tokens: 180},
				{Domain: "cluster-ops", Error: "router off"},
			},
			want: `weaver: 3 domains (codebase=210t, ci-pipeline=180t, cluster-ops=0t err="router off")`,
		},
	}
	for _, tc := range cases {
		if got := summarizeDomainResults(tc.in); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
