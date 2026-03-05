package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/crb2nu/loom/pkg/openairesponses"
)

type fakeRunResponsesClient struct {
	reqs      []openairesponses.TurnRequest
	responses []openairesponses.TurnResponse
	err       error
}

func (f *fakeRunResponsesClient) Create(_ context.Context, req openairesponses.TurnRequest) (openairesponses.TurnResponse, error) {
	f.reqs = append(f.reqs, req)
	if f.err != nil {
		return openairesponses.TurnResponse{}, f.err
	}
	if len(f.responses) == 0 {
		return openairesponses.TurnResponse{Terminal: true}, nil
	}
	resp := f.responses[0]
	f.responses = f.responses[1:]
	return resp, nil
}

func TestResponsesStatus_DefaultDisabled(t *testing.T) {
	t.Setenv("LOOM_EXPERIMENTAL_OPENAI_RESPONSES", "")
	t.Setenv("LOOM_RESPONSES_REQUEST_TIMEOUT", "")
	t.Setenv("LOOM_RESPONSES_MAX_LOOP_ITERATIONS", "")

	cmd := newResponsesStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("status command failed: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "enabled: false") {
		t.Fatalf("expected disabled output, got: %s", text)
	}
	if !strings.Contains(text, "feature_gate_env: LOOM_EXPERIMENTAL_OPENAI_RESPONSES") {
		t.Fatalf("expected feature gate env output, got: %s", text)
	}
}

func TestResponsesStatus_EnabledWithOverrides(t *testing.T) {
	t.Setenv("LOOM_EXPERIMENTAL_OPENAI_RESPONSES", "1")
	t.Setenv("LOOM_RESPONSES_REQUEST_TIMEOUT", "45s")
	t.Setenv("LOOM_RESPONSES_MAX_LOOP_ITERATIONS", "20")

	cmd := newResponsesStatusCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("status command failed: %v", err)
	}

	text := out.String()
	if !strings.Contains(text, "enabled: true") {
		t.Fatalf("expected enabled output, got: %s", text)
	}
	if !strings.Contains(text, "request_timeout: 45s") {
		t.Fatalf("expected timeout override output, got: %s", text)
	}
	if !strings.Contains(text, "max_loop_iterations: 20") {
		t.Fatalf("expected max-loop override output, got: %s", text)
	}
}

func TestResponsesRun_RequiresFeatureGateBeforeFactory(t *testing.T) {
	t.Setenv("LOOM_EXPERIMENTAL_OPENAI_RESPONSES", "")

	origFactory := responsesRuntimeFactory
	t.Cleanup(func() { responsesRuntimeFactory = origFactory })

	factoryCalled := false
	responsesRuntimeFactory = func(_ context.Context) (responsesRuntimeDependencies, error) {
		factoryCalled = true
		return responsesRuntimeDependencies{}, nil
	}

	cmd := newResponsesRunCmd()
	cmd.SetArgs([]string{"--model", "gpt-5", "--input", "hello"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected feature gate error")
	}
	if !strings.Contains(err.Error(), openairesponses.FeatureGateEnvVar) {
		t.Fatalf("expected error containing %s, got %v", openairesponses.FeatureGateEnvVar, err)
	}
	if factoryCalled {
		t.Fatal("runtime factory should not be called when feature gate is disabled")
	}
}

func TestResponsesRun_SuccessOutputsJSONAndPassesContext(t *testing.T) {
	t.Setenv("LOOM_EXPERIMENTAL_OPENAI_RESPONSES", "1")

	origFactory := responsesRuntimeFactory
	t.Cleanup(func() { responsesRuntimeFactory = origFactory })

	client := &fakeRunResponsesClient{
		responses: []openairesponses.TurnResponse{
			{
				ResponseID: "resp_1",
				Terminal:   true,
				OutputText: "done",
				ToolCalls:  nil,
				RawPayload: nil,
			},
		},
	}

	responsesRuntimeFactory = func(_ context.Context) (responsesRuntimeDependencies, error) {
		return responsesRuntimeDependencies{Client: client}, nil
	}

	cmd := newResponsesRunCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"--model", "gpt-5",
		"--input", "hello",
		"--context-mode", "chain",
		"--previous-response-id", "resp_prev",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("run command failed: %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("client request count = %d, want 1", len(client.reqs))
	}
	if client.reqs[0].Context.PreviousResponseID != "resp_prev" {
		t.Fatalf("previous_response_id = %q, want resp_prev", client.reqs[0].Context.PreviousResponseID)
	}

	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode output json: %v\noutput=%s", err, out.String())
	}
	if got := payload["response_id"]; got != "resp_1" {
		t.Fatalf("response_id = %#v, want resp_1", got)
	}
	if got := payload["iterations"]; got != float64(1) {
		t.Fatalf("iterations = %#v, want 1", got)
	}
}

func TestResponsesRun_PropagatesFactoryError(t *testing.T) {
	t.Setenv("LOOM_EXPERIMENTAL_OPENAI_RESPONSES", "1")

	origFactory := responsesRuntimeFactory
	t.Cleanup(func() { responsesRuntimeFactory = origFactory })

	responsesRuntimeFactory = func(_ context.Context) (responsesRuntimeDependencies, error) {
		return responsesRuntimeDependencies{}, errors.New("runtime unavailable")
	}

	cmd := newResponsesRunCmd()
	cmd.SetArgs([]string{"--model", "gpt-5"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected runtime factory error")
	}
	if !strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}
