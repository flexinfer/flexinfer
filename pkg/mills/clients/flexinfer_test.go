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

	"github.com/crb2nu/loom/pkg/mills/gates"
	"github.com/crb2nu/loom/pkg/mills/pipeline"
	"github.com/crb2nu/loom/pkg/mills/store"
)

// roundTripFn turns a function into an http.RoundTripper for stubbing
// the FlexInfer proxy without standing up a listener.
type roundTripFn func(*http.Request) (*http.Response, error)

func (f roundTripFn) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func newStubClient(t *testing.T, body string, status int) *FlexInferClient {
	t.Helper()
	cli, err := NewFlexInferClient(FlexInferConfig{ProxyURL: "http://stub"})
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	cli.SetTransport(roundTripFn(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v1/chat/completions" {
			return nil, errors.New("unexpected path: " + req.URL.Path)
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(bytes.NewBufferString(body)),
			Header:     make(http.Header),
		}, nil
	}))
	return cli
}

const successBody = `{
  "model": "qwen3-8b-instruct",
  "choices": [
    {"message": {"role": "assistant", "content": "Here is my verdict: {\"score\": 0.85, \"reasons\": [\"covers requirement A\", \"covers requirement B\"]}"}}
  ],
  "usage": {"prompt_tokens": 50, "completion_tokens": 30, "total_tokens": 80}
}`

func TestFlexInferClient_RequiresProxyURL(t *testing.T) {
	if _, err := NewFlexInferClient(FlexInferConfig{}); err == nil {
		t.Error("expected error when ProxyURL empty")
	}
}

func TestRubricJudge_ParsesScoreEnvelope(t *testing.T) {
	cli := newStubClient(t, successBody, 200)
	judge := NewRubricJudge(cli)
	v, err := judge.Judge(context.Background(), gates.SpecConformanceRubricName, gates.StageInput{
		Item:         &store.BacklogItem{ID: "BL-X", Title: "x"},
		FilesChanged: []string{"foo.go"},
	})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if v.Score != 0.85 {
		t.Errorf("score = %v, want 0.85", v.Score)
	}
	if len(v.Reasons) != 2 {
		t.Errorf("reasons len = %d, want 2", len(v.Reasons))
	}
	if !strings.Contains(v.Model, "qwen") {
		t.Errorf("model id = %q, want qwen substring", v.Model)
	}
}

func TestRubricJudge_FencedJSONBlock(t *testing.T) {
	// Build a properly-encoded body: model output is fenced JSON
	// embedded inside a chat completion's content field.
	content := "Reasoning...\n```json\n{\"score\": 0.4, \"reasons\": [\"missing tests\"]}\n```\nDone."
	resp := chatResponse{Model: "x"}
	resp.Choices = append(resp.Choices, struct {
		Message chatMessage `json:"message"`
	}{Message: chatMessage{Role: "assistant", Content: content}})
	bodyBytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	cli := newStubClient(t, string(bodyBytes), 200)
	v, err := NewRubricJudge(cli).Judge(context.Background(), "spec_conformance_v1", gates.StageInput{})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if v.Score != 0.4 {
		t.Errorf("score = %v", v.Score)
	}
}

func TestRubricJudge_ScoreOutOfRangeErrors(t *testing.T) {
	body := `{"model": "x", "choices": [{"message": {"content": "{\"score\": 1.5, \"reasons\": []}"}}]}`
	cli := newStubClient(t, body, 200)
	if _, err := NewRubricJudge(cli).Judge(context.Background(), "spec_conformance_v1", gates.StageInput{}); err == nil {
		t.Error("expected error for score > 1")
	}
}

func TestRubricJudge_HTTP500BubblesError(t *testing.T) {
	cli := newStubClient(t, `{"error": "model overloaded"}`, 500)
	if _, err := NewRubricJudge(cli).Judge(context.Background(), "spec_conformance_v1", gates.StageInput{}); err == nil {
		t.Error("expected error on 500")
	}
}

func TestRubricJudge_NoChoicesErrors(t *testing.T) {
	cli := newStubClient(t, `{"model": "x", "choices": []}`, 200)
	if _, err := NewRubricJudge(cli).Judge(context.Background(), "spec_conformance_v1", gates.StageInput{}); err == nil {
		t.Error("expected error for empty choices")
	}
}

func TestRubricJudge_PromptIncludesItemFilesAndDiff(t *testing.T) {
	var captured string
	cli, err := NewFlexInferClient(FlexInferConfig{ProxyURL: "http://stub"})
	if err != nil {
		t.Fatal(err)
	}
	cli.SetTransport(roundTripFn(func(req *http.Request) (*http.Response, error) {
		buf, _ := io.ReadAll(req.Body)
		var parsed chatRequest
		if err := json.Unmarshal(buf, &parsed); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		if len(parsed.Messages) > 0 {
			captured = parsed.Messages[0].Content
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(successBody)),
			Header:     make(http.Header),
		}, nil
	}))
	in := gates.StageInput{
		Item:           &store.BacklogItem{ID: "BL-Y", Title: "feature Y", SpecDoc: ".loom/spec.md", SpecAnchor: "phase-1"},
		FilesChanged:   []string{"a.go", "b.go"},
		LinesAdded:     12,
		LinesRemoved:   3,
		DiffPatch:      []byte("diff --git a/a.go b/a.go\n+x\n"),
		CommitMessages: []string{"feat: add y", "test: add y test"},
	}
	if _, err := NewRubricJudge(cli).Judge(context.Background(), "spec_conformance_v1", in); err != nil {
		t.Fatalf("judge: %v", err)
	}
	for _, want := range []string{"BL-Y", "feature Y", ".loom/spec.md", "phase-1", "a.go", "b.go", "+12 / -3", "feat: add y"} {
		if !strings.Contains(captured, want) {
			t.Errorf("prompt missing %q; got=\n%s", want, captured)
		}
	}
}

func TestRubricJudge_DiffTruncatedAtCap(t *testing.T) {
	var captured string
	cli, err := NewFlexInferClient(FlexInferConfig{ProxyURL: "http://stub"})
	if err != nil {
		t.Fatal(err)
	}
	cli.SetTransport(roundTripFn(func(req *http.Request) (*http.Response, error) {
		buf, _ := io.ReadAll(req.Body)
		var parsed chatRequest
		_ = json.Unmarshal(buf, &parsed)
		if len(parsed.Messages) > 0 {
			captured = parsed.Messages[0].Content
		}
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(bytes.NewBufferString(successBody)),
			Header:     make(http.Header),
		}, nil
	}))
	bigDiff := bytes.Repeat([]byte("x"), 12*1024)
	if _, err := NewRubricJudge(cli).Judge(context.Background(), "spec_conformance_v1", gates.StageInput{DiffPatch: bigDiff}); err != nil {
		t.Fatalf("judge: %v", err)
	}
	if !strings.Contains(captured, "(truncated)") {
		t.Error("expected truncation marker for oversize diff")
	}
}

// ----- WeaverClient -----

func TestWeaverClient_ReturnsNotesAndCitation(t *testing.T) {
	cli := newStubClient(t, successBody, 200)
	w := NewWeaverClient(cli)
	resp, err := w.Research(context.Background(), pipeline.WeaverRequest{
		BacklogID: "BL-W",
		Prompt:    "research how X works",
	})
	if err != nil {
		t.Fatalf("research: %v", err)
	}
	if !strings.Contains(resp.Notes, "verdict") {
		t.Errorf("notes did not contain model output: %q", resp.Notes)
	}
	if resp.Citation["model"] == "" {
		t.Errorf("citation.model missing: %+v", resp.Citation)
	}
	if resp.CostUSD <= 0 {
		t.Errorf("cost should be > 0 for non-empty usage: %v", resp.CostUSD)
	}
}

func TestWeaverClient_NilClientErrors(t *testing.T) {
	w := &WeaverClient{}
	if _, err := w.Research(context.Background(), pipeline.WeaverRequest{}); err == nil {
		t.Error("expected error for nil client")
	}
}

// ----- Composition: gate flow end-to-end against the stub proxy -----

func TestSpecConformanceGate_AgainstFlexInferStub(t *testing.T) {
	cli := newStubClient(t, successBody, 200)
	g := gates.NewSpecConformanceGate(NewRubricJudge(cli))
	out, err := g.Evaluate(context.Background(), gates.StageInput{Item: &store.BacklogItem{ID: "BL-A"}})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !out.Pass {
		t.Errorf("expected pass at score 0.85 (threshold 0.8); reasons=%v", out.Reasons)
	}
	if !strings.HasPrefix(out.JudgedBy, "flexinfer:") {
		t.Errorf("JudgedBy = %q, want flexinfer:* prefix", out.JudgedBy)
	}
}
