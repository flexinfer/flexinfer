package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// ----- Fakes -----

type fakeSpawn struct {
	calls    []SpawnRequest
	resp     SpawnResponse
	err      error
	gotEnvs  []map[string]string
	gotWdirs []string
}

func (f *fakeSpawn) Run(_ context.Context, req SpawnRequest) (SpawnResponse, error) {
	f.calls = append(f.calls, req)
	f.gotEnvs = append(f.gotEnvs, req.Env)
	f.gotWdirs = append(f.gotWdirs, req.WorkingDir)
	if f.err != nil {
		return SpawnResponse{}, f.err
	}
	return f.resp, nil
}

type fakeWeaver struct {
	calls []WeaverRequest
	resp  WeaverResponse
	err   error
}

func (f *fakeWeaver) Research(_ context.Context, req WeaverRequest) (WeaverResponse, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return WeaverResponse{}, f.err
	}
	return f.resp, nil
}

type fakeDevbox struct {
	calls []DevboxRequest
	resp  DevboxResponse
	err   error
}

func (f *fakeDevbox) QualityGate(_ context.Context, req DevboxRequest) (DevboxResponse, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return DevboxResponse{}, f.err
	}
	return f.resp, nil
}

type fakeGitLab struct {
	createCalls  []CreateMRRequest
	pollCalls    []PollPipelineRequest
	mergeCalls   []MergeRequestArgs
	cleanupCalls []CleanupRequest
	createResp   CreateMRResponse
	pollResp     PollPipelineResponse
	mergeResp    MergeResponse
	cleanupResp  CleanupResponse
}

func (f *fakeGitLab) CreateMR(_ context.Context, req CreateMRRequest) (CreateMRResponse, error) {
	f.createCalls = append(f.createCalls, req)
	return f.createResp, nil
}
func (f *fakeGitLab) PollPipeline(_ context.Context, req PollPipelineRequest) (PollPipelineResponse, error) {
	f.pollCalls = append(f.pollCalls, req)
	return f.pollResp, nil
}
func (f *fakeGitLab) Merge(_ context.Context, req MergeRequestArgs) (MergeResponse, error) {
	f.mergeCalls = append(f.mergeCalls, req)
	return f.mergeResp, nil
}
func (f *fakeGitLab) Cleanup(_ context.Context, req CleanupRequest) (CleanupResponse, error) {
	f.cleanupCalls = append(f.cleanupCalls, req)
	return f.cleanupResp, nil
}

func sampleJobContext(stageID string, opts ...func(*JobContext)) JobContext {
	run := &store.PipelineRun{
		ID:              "PIPE-X-1",
		BacklogID:       "BL-X",
		ParentSessionID: "claude-code-session-9",
		WorktreePath:    "/tmp/wt",
	}
	item := &store.BacklogItem{
		ID:    "BL-X",
		Title: "x",
		Budget: store.Budget{
			MaxCostUSD:         5,
			MaxTurns:           50,
			MaxPipelineMinutes: 30,
		},
	}
	stage := Stage{ID: stageID, Type: "agent_spawn"}
	jc := JobContext{
		Run:    run,
		Item:   item,
		Stage:  stage,
		Prior:  map[string]StageOutput{},
		Budget: item.Budget,
		Env:    BuildMillsEnv(run, item, stage),
	}
	for _, o := range opts {
		o(&jc)
	}
	return jc
}

func TestBuildMillsEnv_AlwaysIncludesIDs(t *testing.T) {
	jc := sampleJobContext("implement")
	env := jc.Env
	want := map[string]string{
		"LOOM_MILLS_RUN_ID":      "PIPE-X-1",
		"LOOM_MILLS_BACKLOG_ID":  "BL-X",
		"LOOM_MILLS_STAGE":       "implement",
		"LOOM_PARENT_SESSION_ID": "claude-code-session-9",
		"LOOM_MILLS_WORKTREE":    "/tmp/wt",
		"LOOM_MILLS_BRANCH":      "feat/BL-X",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, env[k], v)
		}
	}
}

func TestBuildMillsEnv_OmitsOptionalWhenAbsent(t *testing.T) {
	jc := sampleJobContext("plan_slice", func(jc *JobContext) {
		jc.Run.ParentSessionID = ""
		jc.Run.WorktreePath = ""
		jc.Env = BuildMillsEnv(jc.Run, jc.Item, jc.Stage)
	})
	if _, ok := jc.Env["LOOM_PARENT_SESSION_ID"]; ok {
		t.Errorf("LOOM_PARENT_SESSION_ID should be omitted when empty")
	}
	if _, ok := jc.Env["LOOM_MILLS_WORKTREE"]; ok {
		t.Errorf("LOOM_MILLS_WORKTREE should be omitted when empty")
	}
}

func TestSpawnWorker_PropagatesBudgetEnvAndPrompt(t *testing.T) {
	sp := &fakeSpawn{resp: SpawnResponse{
		SpawnID:      "spawn-1",
		CostUSD:      0.42,
		FilesChanged: []string{"a.go"},
		LinesAdded:   3,
	}}
	w := &SpawnWorker{Client: sp, PromptFor: func(jc JobContext) string {
		return "plan slices for " + jc.Item.ID
	}}
	out, err := w.Run(context.Background(), sampleJobContext("plan_slice"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.SpawnID != "spawn-1" || out.CostUSD != 0.42 {
		t.Errorf("output passthrough wrong: %+v", out)
	}
	if len(sp.calls) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(sp.calls))
	}
	got := sp.calls[0]
	if got.Prompt != "plan slices for BL-X" {
		t.Errorf("prompt = %q", got.Prompt)
	}
	if got.BudgetUSD != 5 || got.BudgetTurns != 50 || got.BudgetMinutes != 30 {
		t.Errorf("budget propagation wrong: %+v", got)
	}
	if got.WorkingDir != "/tmp/wt" {
		t.Errorf("workdir = %q", got.WorkingDir)
	}
	if got.ParentSessionID != "claude-code-session-9" {
		t.Errorf("parent session = %q", got.ParentSessionID)
	}
	if got.Env["LOOM_MILLS_RUN_ID"] != "PIPE-X-1" {
		t.Errorf("env not propagated to spawn")
	}
	if got.Branch != "feat/BL-X" {
		t.Errorf("branch = %q", got.Branch)
	}
}

func TestSpawnWorker_NilClientErrors(t *testing.T) {
	w := &SpawnWorker{}
	if _, err := w.Run(context.Background(), sampleJobContext("plan_slice")); err == nil {
		t.Error("expected error for nil client")
	}
}

func TestWeaverWorker_RecordsResearchNotes(t *testing.T) {
	wv := &fakeWeaver{resp: WeaverResponse{
		SpawnID: "weaver-1", CostUSD: 0.1, Notes: "found prior art",
	}}
	w := &WeaverWorker{Client: wv, PromptFor: func(jc JobContext) string { return "research " + jc.Item.ID }}
	out, err := w.Run(context.Background(), sampleJobContext("research"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Artifacts["research_notes"] != "found prior art" {
		t.Errorf("research_notes = %v", out.Artifacts["research_notes"])
	}
	if len(wv.calls) != 1 || wv.calls[0].Prompt != "research BL-X" {
		t.Errorf("weaver call wrong: %+v", wv.calls)
	}
}

func TestDevboxWorker_FailedGateReturnsError(t *testing.T) {
	db := &fakeDevbox{resp: DevboxResponse{
		Passed:  false,
		CostUSD: 0.05,
		Checks:  []DevboxCheck{{Name: "lint", Passed: false, Output: "boom"}},
	}}
	w := &DevboxWorker{Client: db, Project: "loom-core", AgentID: "claude-code"}
	out, err := w.Run(context.Background(), sampleJobContext("tests"))
	if err == nil {
		t.Error("expected error on failed quality gate")
	}
	if out.CostUSD != 0.05 {
		t.Errorf("cost not propagated on fail: %v", out.CostUSD)
	}
	if len(db.calls) != 1 {
		t.Errorf("devbox calls = %d", len(db.calls))
	}
}

func TestDevboxWorker_PassPropagatesArtifacts(t *testing.T) {
	db := &fakeDevbox{resp: DevboxResponse{
		Passed:  true,
		CostUSD: 0.02,
		Checks:  []DevboxCheck{{Name: "test", Passed: true}},
	}}
	w := &DevboxWorker{Client: db, Project: "loom-core", AgentID: "claude-code"}
	out, err := w.Run(context.Background(), sampleJobContext("tests"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.Artifacts["passed"] != true {
		t.Errorf("passed flag missing")
	}
}

func TestGitLabWorker_CreateMR_RecordsIID(t *testing.T) {
	gl := &fakeGitLab{createResp: CreateMRResponse{MRIID: 99, URL: "https://gl/mr/99", CostUSD: 0.01}}
	w := &GitLabWorker{Client: gl, MRTitle: func(jc JobContext) string { return "feat: x" }}
	out, err := w.Run(context.Background(), sampleJobContext("mr"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.MRIID != 99 {
		t.Errorf("mr_iid = %d", out.MRIID)
	}
	if out.Artifacts["mr_url"] != "https://gl/mr/99" {
		t.Errorf("mr_url not recorded")
	}
	if len(gl.createCalls) != 1 || gl.createCalls[0].Title != "feat: x" {
		t.Errorf("createMR call wrong: %+v", gl.createCalls)
	}
	if gl.createCalls[0].SourceBranch != "feat/BL-X" {
		t.Errorf("source branch = %q", gl.createCalls[0].SourceBranch)
	}
}

func TestGitLabWorker_CreateMR_FanOutParentUsesIntegrationBranch(t *testing.T) {
	gl := &fakeGitLab{createResp: CreateMRResponse{MRIID: 99}}
	w := &GitLabWorker{Client: gl}
	jc := sampleJobContext("mr", func(jc *JobContext) {
		jc.Item.Slices = []store.Slice{
			{Name: "alpha", ParallelWith: []string{"beta"}},
			{Name: "beta", ParallelWith: []string{"alpha"}},
		}
		jc.Env = BuildMillsEnv(jc.Run, jc.Item, jc.Stage)
	})
	if _, err := w.Run(context.Background(), jc); err != nil {
		t.Fatalf("mr: %v", err)
	}
	if gl.createCalls[0].SourceBranch != "integrate/BL-X" {
		t.Errorf("source branch = %q", gl.createCalls[0].SourceBranch)
	}
}

func TestGitLabWorker_CIWatch_FailingPipelineErrors(t *testing.T) {
	gl := &fakeGitLab{pollResp: PollPipelineResponse{Status: "failed"}}
	w := &GitLabWorker{Client: gl}
	jc := sampleJobContext("ci_watch", func(jc *JobContext) {
		mr := int64(99)
		jc.Run.MRIID = &mr
	})
	out, err := w.Run(context.Background(), jc)
	if err == nil {
		t.Error("expected error on failed pipeline")
	}
	if out.Artifacts["ci_status"] != "failed" {
		t.Errorf("ci_status not recorded")
	}
}

func TestGitLabWorker_CIWatch_NoMRIDErrors(t *testing.T) {
	gl := &fakeGitLab{pollResp: PollPipelineResponse{Status: "success"}}
	w := &GitLabWorker{Client: gl}
	if _, err := w.Run(context.Background(), sampleJobContext("ci_watch")); err == nil {
		t.Error("expected error when no mr_iid present")
	}
}

func TestGitLabWorker_Merge_PropagatesSHA(t *testing.T) {
	gl := &fakeGitLab{mergeResp: MergeResponse{MergedSHA: "abc123", CostUSD: 0.01}}
	w := &GitLabWorker{Client: gl}
	jc := sampleJobContext("merge", func(jc *JobContext) {
		mr := int64(99)
		jc.Run.MRIID = &mr
	})
	out, err := w.Run(context.Background(), jc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out.MergedSHA != "abc123" {
		t.Errorf("merged_sha = %q", out.MergedSHA)
	}
}

func TestGitLabWorker_Cleanup_UsesSourceBranchOverride(t *testing.T) {
	gl := &fakeGitLab{}
	w := &GitLabWorker{Client: gl, SourceBranch: func(jc JobContext) string { return "feat/custom" }}
	jc := sampleJobContext("cleanup", func(jc *JobContext) {
		mr := int64(99)
		jc.Run.MRIID = &mr
	})
	if _, err := w.Run(context.Background(), jc); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(gl.cleanupCalls) != 1 || gl.cleanupCalls[0].BranchName != "feat/custom" {
		t.Errorf("cleanup branch override not honored: %+v", gl.cleanupCalls)
	}
}

func TestGitLabWorker_MRRequiresSourceBranch(t *testing.T) {
	gl := &fakeGitLab{}
	w := &GitLabWorker{Client: gl}
	jc := sampleJobContext("mr", func(jc *JobContext) {
		jc.Item.ID = ""
		jc.Env = BuildMillsEnv(jc.Run, jc.Item, jc.Stage)
	})
	if _, err := w.Run(context.Background(), jc); err == nil {
		t.Fatal("expected error when source branch is unavailable")
	}
	if len(gl.createCalls) != 0 {
		t.Errorf("CreateMR should not be called: %+v", gl.createCalls)
	}
}

func TestGitLabWorker_UnknownStageErrors(t *testing.T) {
	gl := &fakeGitLab{}
	w := &GitLabWorker{Client: gl}
	if _, err := w.Run(context.Background(), sampleJobContext("nope")); err == nil {
		t.Error("expected error for unknown stage")
	}
}

// ----- Dispatcher routing -----

func TestDispatcher_RoutesToRegistered(t *testing.T) {
	called := ""
	wA := workerFn(func(_ context.Context, jc JobContext) (StageOutput, error) {
		called = jc.Stage.ID
		return StageOutput{CostUSD: 0.1}, nil
	})
	d := NewDispatcher(map[string]Worker{"plan_slice": wA}, nil)
	jc := sampleJobContext("plan_slice")
	out, err := d.Dispatch(context.Background(), jc.Run, jc.Item, jc.Stage, jc.Prior)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if called != "plan_slice" || out.CostUSD != 0.1 {
		t.Errorf("dispatcher did not route correctly: called=%s out=%+v", called, out)
	}
}

func TestDispatcher_NoFallbackUnmappedErrors(t *testing.T) {
	d := NewDispatcher(nil, nil)
	jc := sampleJobContext("plan_slice")
	if _, err := d.Dispatch(context.Background(), jc.Run, jc.Item, jc.Stage, jc.Prior); err == nil {
		t.Error("expected error for unmapped stage with no fallback")
	}
}

func TestDispatcher_FallbackHandlesUnmapped(t *testing.T) {
	called := false
	fb := workerFn(func(_ context.Context, _ JobContext) (StageOutput, error) {
		called = true
		return StageOutput{}, nil
	})
	d := NewDispatcher(nil, fb)
	jc := sampleJobContext("plan_slice")
	if _, err := d.Dispatch(context.Background(), jc.Run, jc.Item, jc.Stage, jc.Prior); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !called {
		t.Error("fallback should have run")
	}
}

func TestDispatcher_RegisterReplacesRoute(t *testing.T) {
	d := NewDispatcher(nil, nil)
	d.Register("implement", workerFn(func(_ context.Context, _ JobContext) (StageOutput, error) {
		return StageOutput{}, errors.New("first")
	}))
	d.Register("implement", workerFn(func(_ context.Context, _ JobContext) (StageOutput, error) {
		return StageOutput{CostUSD: 99}, nil
	}))
	jc := sampleJobContext("implement")
	out, err := d.Dispatch(context.Background(), jc.Run, jc.Item, jc.Stage, jc.Prior)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if out.CostUSD != 99 {
		t.Errorf("Register did not replace route")
	}
}

func TestDefaultRoutes_WiresAllStages(t *testing.T) {
	routes := DefaultRoutes(&fakeSpawn{}, &fakeWeaver{}, &fakeDevbox{}, &fakeGitLab{}, "loom-core", "claude-code", nil)
	for _, want := range []string{"plan_slice", "research", "implement", "tests", "pr_self_review", "mr", "ci_watch", "merge", "cleanup"} {
		if _, ok := routes[want]; !ok {
			t.Errorf("DefaultRoutes missing %s", want)
		}
	}
}

// workerFn adapts a function into the Worker interface.
type workerFn func(ctx context.Context, jc JobContext) (StageOutput, error)

func (f workerFn) Run(ctx context.Context, jc JobContext) (StageOutput, error) { return f(ctx, jc) }
