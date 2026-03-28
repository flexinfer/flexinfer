package autofix

import (
	"context"
	"fmt"
	"testing"
)

// --- parseDiagnosis table-driven tests ---

func TestParseDiagnosis(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		project     string
		pipelineID  int
		failedJobs  []string
		logSnippets []string
		wantCat     string
		wantConf    float64
		wantRoot    string
	}{
		{
			name:       "valid JSON diagnosis",
			response:   `{"root_cause":"missing dependency","category":"dependency","suggested_fix":"add go module","confidence":0.9}`,
			project:    "loom-core",
			pipelineID: 42,
			failedJobs: []string{"build"},
			wantCat:    "dependency",
			wantConf:   0.9,
			wantRoot:   "missing dependency",
		},
		{
			name:       "JSON with markdown fences",
			response:   "```json\n{\"root_cause\":\"test assertion\",\"category\":\"test_failure\",\"suggested_fix\":\"fix test\",\"confidence\":0.85}\n```",
			project:    "loom-core",
			pipelineID: 100,
			failedJobs: []string{"unit-test"},
			wantCat:    "test_failure",
			wantConf:   0.85,
			wantRoot:   "test assertion",
		},
		{
			name:        "invalid JSON falls back to raw text",
			response:    "This is not JSON at all",
			project:     "loom-core",
			pipelineID:  99,
			failedJobs:  []string{"lint"},
			logSnippets: []string{"error output"},
			wantCat:     "build_error",
			wantConf:    0.3,
			wantRoot:    "This is not JSON at all",
		},
		{
			name:       "confidence clamped above 1.0",
			response:   `{"root_cause":"over-confident","category":"lint","suggested_fix":"run linter","confidence":1.5}`,
			project:    "proj",
			pipelineID: 1,
			wantCat:    "lint",
			wantConf:   1.0,
			wantRoot:   "over-confident",
		},
		{
			name:       "confidence clamped below 0",
			response:   `{"root_cause":"under-confident","category":"infra","suggested_fix":"check runner","confidence":-0.5}`,
			project:    "proj",
			pipelineID: 2,
			wantCat:    "infra",
			wantConf:   0.0,
			wantRoot:   "under-confident",
		},
		{
			name:       "JSON with leading text before brace",
			response:   "Here is the analysis:\n{\"root_cause\":\"network timeout\",\"category\":\"infra\",\"suggested_fix\":\"retry\",\"confidence\":0.7}",
			project:    "proj",
			pipelineID: 3,
			wantCat:    "infra",
			wantConf:   0.7,
			wantRoot:   "network timeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diag, err := parseDiagnosis(tt.response, tt.project, tt.pipelineID, tt.failedJobs, tt.logSnippets)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diag.Category != tt.wantCat {
				t.Errorf("expected category %q, got %q", tt.wantCat, diag.Category)
			}
			if diag.Confidence != tt.wantConf {
				t.Errorf("expected confidence %v, got %v", tt.wantConf, diag.Confidence)
			}
			if diag.RootCause != tt.wantRoot {
				t.Errorf("expected root_cause %q, got %q", tt.wantRoot, diag.RootCause)
			}
			if diag.Project != tt.project {
				t.Errorf("expected project %q, got %q", tt.project, diag.Project)
			}
			if diag.PipelineID != tt.pipelineID {
				t.Errorf("expected pipeline_id %d, got %d", tt.pipelineID, diag.PipelineID)
			}
		})
	}
}

// --- buildDiagnosticPrompt table-driven tests ---

func TestBuildDiagnosticPrompt(t *testing.T) {
	tests := []struct {
		name        string
		project     string
		pipelineID  int
		ref         string
		failedJobs  []string
		logSnippets []string
		wantContain []string
	}{
		{
			name:        "basic prompt with all fields",
			project:     "loom-core",
			pipelineID:  42,
			ref:         "main",
			failedJobs:  []string{"build", "test"},
			logSnippets: []string{"=== build ===\nerror: compile failed"},
			wantContain: []string{"loom-core", "42", "main", "build, test", "Job Logs:", "compile failed"},
		},
		{
			name:        "prompt without log snippets",
			project:     "proj",
			pipelineID:  1,
			ref:         "feat/x",
			failedJobs:  []string{"lint"},
			logSnippets: nil,
			wantContain: []string{"proj", "1", "feat/x", "lint"},
		},
		{
			name:        "empty failed jobs list",
			project:     "proj",
			pipelineID:  10,
			ref:         "develop",
			failedJobs:  nil,
			logSnippets: nil,
			wantContain: []string{"proj", "10", "develop"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildDiagnosticPrompt(tt.project, tt.pipelineID, tt.ref, tt.failedJobs, tt.logSnippets)
			for _, want := range tt.wantContain {
				if !containsSubstring(result, want) {
					t.Errorf("expected prompt to contain %q, got:\n%s", want, result)
				}
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// --- ProposeAutoFix tests ---

func TestProposeAutoFix(t *testing.T) {
	tests := []struct {
		name         string
		diag         Diagnosis
		wantStrategy string
		wantApproval bool
	}{
		{
			name: "high confidence test_failure gets agent_fix",
			diag: Diagnosis{
				PipelineID: 1,
				Project:    "proj",
				Category:   "test_failure",
				Confidence: 0.85,
			},
			wantStrategy: "agent_fix",
			wantApproval: true,
		},
		{
			name: "high confidence lint gets agent_fix",
			diag: Diagnosis{
				PipelineID: 2,
				Project:    "proj",
				Category:   "lint",
				Confidence: 0.9,
			},
			wantStrategy: "agent_fix",
			wantApproval: true,
		},
		{
			name: "infra category gets retry without approval",
			diag: Diagnosis{
				PipelineID: 3,
				Project:    "proj",
				Category:   "infra",
				Confidence: 0.5,
			},
			wantStrategy: "retry",
			wantApproval: false,
		},
		{
			name: "medium confidence gets agent_fix",
			diag: Diagnosis{
				PipelineID: 4,
				Project:    "proj",
				Category:   "build_error",
				Confidence: 0.65,
			},
			wantStrategy: "agent_fix",
			wantApproval: true,
		},
		{
			name: "low confidence gets manual",
			diag: Diagnosis{
				PipelineID: 5,
				Project:    "proj",
				Category:   "build_error",
				Confidence: 0.4,
			},
			wantStrategy: "manual",
			wantApproval: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
			proposal, err := engine.ProposeAutoFix(tt.diag)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if proposal.Strategy != tt.wantStrategy {
				t.Errorf("expected strategy %q, got %q", tt.wantStrategy, proposal.Strategy)
			}
			if proposal.RequiresApproval != tt.wantApproval {
				t.Errorf("expected requires_approval %v, got %v", tt.wantApproval, proposal.RequiresApproval)
			}
			if proposal.Confidence != tt.diag.Confidence {
				t.Errorf("expected confidence %v, got %v", tt.diag.Confidence, proposal.Confidence)
			}
			if proposal.ID == "" {
				t.Error("expected non-empty proposal ID")
			}
		})
	}
}

// --- NewAutoFixEngine tests ---

func TestNewAutoFixEngine_Defaults(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	if engine.model != "qwen3-8b" {
		t.Errorf("expected default model 'qwen3-8b', got %q", engine.model)
	}
	if engine.logger == nil {
		t.Error("expected non-nil logger")
	}
	if engine.proposals == nil {
		t.Error("expected non-nil proposals slice")
	}
	if engine.executions == nil {
		t.Error("expected non-nil executions slice")
	}
	if engine.diagnoses == nil {
		t.Error("expected non-nil diagnoses map")
	}
}

func TestNewAutoFixEngine_CustomModel(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "custom-model", nil)
	if engine.model != "custom-model" {
		t.Errorf("expected model 'custom-model', got %q", engine.model)
	}
}

// --- ListProposals / ListExecutions tests ---

func TestListProposals_Empty(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	proposals := engine.ListProposals()
	if len(proposals) != 0 {
		t.Errorf("expected 0 proposals, got %d", len(proposals))
	}
}

func TestListProposals_ReversesOrder(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	// Add two proposals via ProposeAutoFix.
	_, _ = engine.ProposeAutoFix(Diagnosis{Project: "a", PipelineID: 1, Category: "infra", Confidence: 0.5})
	_, _ = engine.ProposeAutoFix(Diagnosis{Project: "b", PipelineID: 2, Category: "infra", Confidence: 0.6})

	proposals := engine.ListProposals()
	if len(proposals) != 2 {
		t.Fatalf("expected 2 proposals, got %d", len(proposals))
	}
	// Newest (second added) should be first in list.
	if proposals[0].DiagnosisID != "b:2" {
		t.Errorf("expected first proposal diagnosis_id 'b:2', got %q", proposals[0].DiagnosisID)
	}
}

func TestListExecutions_Empty(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	executions := engine.ListExecutions()
	if len(executions) != 0 {
		t.Errorf("expected 0 executions, got %d", len(executions))
	}
}

// --- GetProposal / GetExecution tests ---

func TestGetProposal_NotFound(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	_, err := engine.GetProposal("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent proposal")
	}
}

func TestGetProposal_Found(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	proposal, _ := engine.ProposeAutoFix(Diagnosis{Project: "p", PipelineID: 1, Category: "infra", Confidence: 0.5})

	found, err := engine.GetProposal(proposal.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.ID != proposal.ID {
		t.Errorf("expected proposal ID %q, got %q", proposal.ID, found.ID)
	}
}

func TestGetExecution_NotFound(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	_, err := engine.GetExecution("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent execution")
	}
}

// --- RejectProposal tests ---

func TestRejectProposal(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	proposal, _ := engine.ProposeAutoFix(Diagnosis{Project: "p", PipelineID: 1, Category: "infra", Confidence: 0.5})

	err := engine.RejectProposal(proposal.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have created a rejected execution.
	executions := engine.ListExecutions()
	if len(executions) != 1 {
		t.Fatalf("expected 1 execution, got %d", len(executions))
	}
	if executions[0].Status != "rejected" {
		t.Errorf("expected status 'rejected', got %q", executions[0].Status)
	}
	if executions[0].ProposalID != proposal.ID {
		t.Errorf("expected proposal_id %q, got %q", proposal.ID, executions[0].ProposalID)
	}
}

// --- ExecuteAutoFix tests ---

func TestExecuteAutoFix_RetryStrategy(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	proposal := AutoFixProposal{
		ID:       "prop-1",
		Strategy: "retry",
	}

	exec, err := engine.ExecuteAutoFix(context.Background(), proposal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.Status != "succeeded" {
		t.Errorf("expected status 'succeeded', got %q", exec.Status)
	}
	if exec.CompletedAt == nil {
		t.Error("expected CompletedAt to be set")
	}
}

func TestExecuteAutoFix_ManualStrategy(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	proposal := AutoFixProposal{
		ID:       "prop-2",
		Strategy: "manual",
	}

	exec, err := engine.ExecuteAutoFix(context.Background(), proposal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", exec.Status)
	}
	if exec.Result != "manual intervention required" {
		t.Errorf("unexpected result: %q", exec.Result)
	}
}

func TestExecuteAutoFix_AgentFixNoSpawner(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	proposal := AutoFixProposal{
		ID:       "prop-3",
		Strategy: "agent_fix",
	}

	exec, err := engine.ExecuteAutoFix(context.Background(), proposal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", exec.Status)
	}
	if exec.Result != "no spawn orchestrator available" {
		t.Errorf("unexpected result: %q", exec.Result)
	}
}

// --- mockSpawner for agent_fix with spawner ---

type mockSpawner struct {
	spawnID  string
	spawnErr error
}

func (m *mockSpawner) Spawn(_ context.Context, _ SpawnRequest) (string, error) {
	return m.spawnID, m.spawnErr
}

func TestExecuteAutoFix_AgentFixWithSpawner(t *testing.T) {
	spawner := &mockSpawner{spawnID: "spawn-123"}
	engine := NewAutoFixEngine(nil, nil, nil, spawner, "", nil)

	// Store a diagnosis so the engine can look it up.
	diag := &Diagnosis{
		PipelineID:   42,
		Project:      "loom-core",
		RootCause:    "test failed",
		Category:     "test_failure",
		SuggestedFix: "fix test",
		Confidence:   0.9,
	}
	engine.diagnoses["loom-core:42"] = diag

	proposal := AutoFixProposal{
		ID:          "prop-4",
		DiagnosisID: "loom-core:42",
		Strategy:    "agent_fix",
	}

	exec, err := engine.ExecuteAutoFix(context.Background(), proposal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.Status != "running" {
		t.Errorf("expected status 'running', got %q", exec.Status)
	}
	if exec.SpawnID != "spawn-123" {
		t.Errorf("expected spawn_id 'spawn-123', got %q", exec.SpawnID)
	}
}

func TestExecuteAutoFix_AgentFixSpawnError(t *testing.T) {
	spawner := &mockSpawner{spawnErr: fmt.Errorf("spawn failed")}
	engine := NewAutoFixEngine(nil, nil, nil, spawner, "", nil)

	diag := &Diagnosis{
		PipelineID: 42,
		Project:    "loom-core",
	}
	engine.diagnoses["loom-core:42"] = diag

	proposal := AutoFixProposal{
		ID:          "prop-5",
		DiagnosisID: "loom-core:42",
		Strategy:    "agent_fix",
	}

	exec, err := engine.ExecuteAutoFix(context.Background(), proposal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec.Status != "failed" {
		t.Errorf("expected status 'failed', got %q", exec.Status)
	}
}

// --- DiagnoseFailure error cases ---

func TestDiagnoseFailure_NilLLM(t *testing.T) {
	engine := NewAutoFixEngine(nil, nil, nil, nil, "", nil)
	_, err := engine.DiagnoseFailure(context.Background(), "proj", 1)
	if err == nil {
		t.Error("expected error when LLM is nil")
	}
}

func TestDiagnoseFailure_NilPipeline(t *testing.T) {
	// Need a non-nil FlexInferClient, but we can't easily create one without
	// a real server. The check for nil LLM happens first, so we test the
	// pipeline nil check by using a non-nil value. We skip this test since
	// creating a FlexInferClient requires a URL.
	t.Skip("requires non-nil FlexInferClient which needs a real URL")
}
