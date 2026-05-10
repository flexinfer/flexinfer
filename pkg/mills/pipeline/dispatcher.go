package pipeline

import (
	"context"
	"errors"
	"fmt"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// JobContext is the bundle every Worker receives. The dispatcher fills
// it from the run + item + stage so workers don't have to reach back
// into the runner.
type JobContext struct {
	Run    *store.PipelineRun
	Item   *store.BacklogItem
	Stage  Stage
	Prior  map[string]StageOutput
	Budget store.Budget
	// Env carries the LOOM_MILLS_* variables every worker propagates to
	// child processes (spawn, devbox exec, mcp tool calls). Workers may
	// add more before invoking the underlying client.
	Env map[string]string
}

// Worker executes one stage. The dispatcher resolves stage id → Worker
// at runtime; tests inject fakes that record calls and return canned
// StageOutputs.
type Worker interface {
	Run(ctx context.Context, jc JobContext) (StageOutput, error)
}

// Dispatcher routes stages to workers. The zero value is unusable; use
// NewDispatcher.
type Dispatcher struct {
	routes  map[string]Worker
	fallthr Worker
}

// NewDispatcher constructs a Dispatcher from a route table. A nil routes
// map is allowed; the dispatcher then falls back to fallback for every
// stage. A nil fallback errors on unmapped stages — the recommended mode
// for production so a typo can't silently no-op a write stage.
func NewDispatcher(routes map[string]Worker, fallback Worker) *Dispatcher {
	if routes == nil {
		routes = make(map[string]Worker)
	}
	return &Dispatcher{routes: routes, fallthr: fallback}
}

// Dispatch implements WorkerDispatcher. It looks up the stage's worker
// and invokes it with a populated JobContext.
func (d *Dispatcher) Dispatch(
	ctx context.Context,
	run *store.PipelineRun,
	item *store.BacklogItem,
	stage Stage,
	prior map[string]StageOutput,
) (StageOutput, error) {
	if d == nil {
		return StageOutput{}, errors.New("dispatcher: nil")
	}
	w := d.routes[stage.ID]
	if w == nil {
		w = d.fallthr
	}
	if w == nil {
		return StageOutput{}, fmt.Errorf("dispatcher: no worker for stage %q", stage.ID)
	}
	jc := JobContext{
		Run:    run,
		Item:   item,
		Stage:  stage,
		Prior:  prior,
		Budget: item.Budget,
		Env:    BuildMillsEnv(run, item, stage),
	}
	return w.Run(ctx, jc)
}

// Register adds or replaces the worker for a stage id. Useful when wiring
// the operator at startup (slice 4.7) and when tests want to swap one
// worker without reconstructing the whole route table.
func (d *Dispatcher) Register(stageID string, w Worker) {
	if d.routes == nil {
		d.routes = make(map[string]Worker)
	}
	d.routes[stageID] = w
}

// BuildMillsEnv returns the canonical LOOM_MILLS_* env-var bundle every
// worker forwards to its child. Persisted contract — child processes
// (spawn, devbox exec, mcp tools) parse these to record their parent
// run for cost attribution and audit.
func BuildMillsEnv(run *store.PipelineRun, item *store.BacklogItem, stage Stage) map[string]string {
	env := map[string]string{
		"LOOM_MILLS_RUN_ID":     run.ID,
		"LOOM_MILLS_BACKLOG_ID": item.ID,
		"LOOM_MILLS_STAGE":      stage.ID,
	}
	if run.ParentSessionID != "" {
		env["LOOM_PARENT_SESSION_ID"] = run.ParentSessionID
	}
	if run.WorktreePath != "" {
		env["LOOM_MILLS_WORKTREE"] = run.WorktreePath
	}
	if branch := BranchContractFor(run, item, stage, "").SourceBranch; branch != "" {
		env["LOOM_MILLS_BRANCH"] = branch
	}
	return env
}

// ----- Default worker implementations -----
//
// Each worker is a thin shell over a Client interface so the operator
// can wire a real backend at startup and tests can inject a fake. The
// concrete network glue lives in slice 4.7's main.go wiring; slice 4.2
// ships the contract surfaces and a fallback no-op.

// SpawnClient is the operator-facing facade over the spawn HTTP service.
// Implementations live outside this package; tests use a fake.
type SpawnClient interface {
	Run(ctx context.Context, req SpawnRequest) (SpawnResponse, error)
}

// SpawnRequest carries every field the spawn API needs to start a
// subordinate Claude/Codex/Gemini run for this stage.
type SpawnRequest struct {
	Prompt          string
	WorkingDir      string
	Model           string
	Env             map[string]string
	BudgetUSD       float64
	BudgetTurns     int
	BudgetMinutes   int
	ParentSessionID string
	StageID         string

	// BacklogID, Project, Branch, BaseBranch, and Namespace are
	// populated by SpawnWorker from JobContext for spawn services that
	// require git + agent-context routing (the loom HUD mobile API).
	// Stage workers that don't need them ignore them.
	BacklogID  string
	Project    string
	Branch     string
	BaseBranch string
	Namespace  string
}

// SpawnResponse summarises what the spawn returned. Workers translate
// it into a StageOutput for the runner.
type SpawnResponse struct {
	SpawnID        string
	CostUSD        float64
	LogTail        string
	FilesChanged   []string
	LinesAdded     int
	LinesRemoved   int
	DiffPatch      []byte
	CommitMessages []string
	Artifacts      map[string]any
}

// SpawnWorker dispatches a stage to the spawn service. Used for
// plan_slice, pr_self_review, and implement (with a worktree).
type SpawnWorker struct {
	Client SpawnClient
	// Model overrides the request's model field. Empty falls through
	// to the spawn service default.
	Model string
	// PromptFor returns the prompt body for this stage. The operator
	// supplies a function that pulls from the spec doc / sidecar; tests
	// inject a static string returner.
	PromptFor func(jc JobContext) string
	// NeedsWorktree marks stages that must allocate a per-run worktree
	// before invoking the spawn (implement). The allocator wires in
	// from outside; the worker just propagates run.WorktreePath.
	NeedsWorktree bool
	// Project is the repo name spawns target. Falls back to
	// "loom-core" when empty. The HUD spawn API needs this to resolve
	// the worktree base + git remote.
	Project string
	// Namespace is the agent_context namespace the spawn writes into.
	// Falls back to "loom-mills" — the same namespace the operator's
	// own session uses, so handoffs stay routable.
	Namespace string
	// BaseBranch is what spawned worktrees branch off. Empty falls
	// through to spawn-side default ("main").
	BaseBranch string
}

// Run satisfies Worker.
func (w *SpawnWorker) Run(ctx context.Context, jc JobContext) (StageOutput, error) {
	if w.Client == nil {
		return StageOutput{}, fmt.Errorf("spawn worker: client not configured for %s", jc.Stage.ID)
	}
	prompt := ""
	if w.PromptFor != nil {
		prompt = w.PromptFor(jc)
	}
	project := w.Project
	if project == "" {
		project = "loom-core"
	}
	namespace := w.Namespace
	if namespace == "" {
		namespace = "loom-mills"
	}
	branch := BranchContractFor(jc.Run, jc.Item, jc.Stage, "").SourceBranch
	if branch == "" {
		return StageOutput{}, fmt.Errorf("spawn worker: source branch unavailable for backlog %q", jc.Item.ID)
	}
	req := SpawnRequest{
		Prompt:          prompt,
		WorkingDir:      jc.Run.WorktreePath,
		Model:           w.Model,
		Env:             jc.Env,
		BudgetUSD:       jc.Budget.MaxCostUSD,
		BudgetTurns:     jc.Budget.MaxTurns,
		BudgetMinutes:   jc.Budget.MaxPipelineMinutes,
		ParentSessionID: jc.Run.ParentSessionID,
		StageID:         jc.Stage.ID,
		BacklogID:       jc.Item.ID,
		Project:         project,
		Branch:          branch,
		BaseBranch:      w.BaseBranch,
		Namespace:       namespace,
	}
	resp, err := w.Client.Run(ctx, req)
	if err != nil {
		return StageOutput{}, err
	}
	return StageOutput{
		CostUSD:        resp.CostUSD,
		SpawnID:        resp.SpawnID,
		LogTail:        resp.LogTail,
		Artifacts:      resp.Artifacts,
		FilesChanged:   resp.FilesChanged,
		LinesAdded:     resp.LinesAdded,
		LinesRemoved:   resp.LinesRemoved,
		DiffPatch:      resp.DiffPatch,
		CommitMessages: resp.CommitMessages,
	}, nil
}

// WeaverClient is the codebase-aware FlexInfer subagent facade. Used by
// the research stage to gather grounded context before implement.
type WeaverClient interface {
	Research(ctx context.Context, req WeaverRequest) (WeaverResponse, error)
}

// WeaverRequest is the bundle a research call ships off.
type WeaverRequest struct {
	// RunID is the pipeline_runs.id the call belongs to. Carried so the
	// shadow-mode diff recorder can update the research_diff column on
	// the right row (PipelineDAO.SetResearchDiff is keyed by run id).
	// Zero/empty when the caller doesn't have a run context — the
	// recorder must tolerate that and skip the persist.
	RunID     string
	BacklogID string
	Prompt    string
	Env       map[string]string
	BudgetUSD float64
}

// WeaverResponse carries the research notes back. Notes is appended to
// the stage_results.artifacts_json under "research_notes" for downstream
// stages.
type WeaverResponse struct {
	SpawnID  string
	CostUSD  float64
	LogTail  string
	Notes    string
	Citation map[string]any
}

// WeaverWorker dispatches the research stage.
type WeaverWorker struct {
	Client    WeaverClient
	PromptFor func(jc JobContext) string
}

// Run satisfies Worker.
func (w *WeaverWorker) Run(ctx context.Context, jc JobContext) (StageOutput, error) {
	if w.Client == nil {
		return StageOutput{}, fmt.Errorf("weaver worker: client not configured")
	}
	prompt := ""
	if w.PromptFor != nil {
		prompt = w.PromptFor(jc)
	}
	runID := ""
	if jc.Run != nil {
		runID = jc.Run.ID
	}
	resp, err := w.Client.Research(ctx, WeaverRequest{
		RunID:     runID,
		BacklogID: jc.Item.ID,
		Prompt:    prompt,
		Env:       jc.Env,
		BudgetUSD: jc.Budget.MaxCostUSD,
	})
	if err != nil {
		return StageOutput{}, err
	}
	art := map[string]any{"research_notes": resp.Notes}
	if resp.Citation != nil {
		art["citation"] = resp.Citation
	}
	return StageOutput{
		CostUSD:   resp.CostUSD,
		SpawnID:   resp.SpawnID,
		LogTail:   resp.LogTail,
		Artifacts: art,
	}, nil
}

// DevboxClient is the devbox quality-gate facade used by the tests stage.
type DevboxClient interface {
	QualityGate(ctx context.Context, req DevboxRequest) (DevboxResponse, error)
}

// DevboxRequest carries the project + agent id + env to a quality-gate run.
type DevboxRequest struct {
	Project string
	AgentID string
	Env     map[string]string
}

// DevboxResponse summarises the gate verdict + per-check results.
type DevboxResponse struct {
	Passed   bool
	CostUSD  float64
	LogTail  string
	Checks   []DevboxCheck
	Language string
}

// DevboxCheck captures one fmt/lint/test run inside the gate.
type DevboxCheck struct {
	Name     string
	Passed   bool
	ExitCode int
	Duration float64
	Output   string
}

// DevboxWorker dispatches the tests stage.
type DevboxWorker struct {
	Client  DevboxClient
	Project string
	AgentID string
}

// Run satisfies Worker.
func (w *DevboxWorker) Run(ctx context.Context, jc JobContext) (StageOutput, error) {
	if w.Client == nil {
		return StageOutput{}, fmt.Errorf("devbox worker: client not configured")
	}
	resp, err := w.Client.QualityGate(ctx, DevboxRequest{
		Project: w.Project,
		AgentID: w.AgentID,
		Env:     jc.Env,
	})
	if err != nil {
		return StageOutput{}, err
	}
	if !resp.Passed {
		// Treat a quality-gate fail as an error so the runner can retry
		// implement; the gate-fail/escalate path picks it up by attempt count.
		return StageOutput{
			CostUSD: resp.CostUSD,
			LogTail: resp.LogTail,
			Artifacts: map[string]any{
				"checks":   resp.Checks,
				"language": resp.Language,
			},
		}, fmt.Errorf("devbox quality gate failed: %d checks", len(resp.Checks))
	}
	return StageOutput{
		CostUSD: resp.CostUSD,
		LogTail: resp.LogTail,
		Artifacts: map[string]any{
			"checks":   resp.Checks,
			"language": resp.Language,
			"passed":   true,
		},
	}, nil
}

// GitLabClient is the merge-request lifecycle facade for the mr / ci_watch
// / merge / cleanup stages.
type GitLabClient interface {
	CreateMR(ctx context.Context, req CreateMRRequest) (CreateMRResponse, error)
	PollPipeline(ctx context.Context, req PollPipelineRequest) (PollPipelineResponse, error)
	Merge(ctx context.Context, req MergeRequestArgs) (MergeResponse, error)
	Cleanup(ctx context.Context, req CleanupRequest) (CleanupResponse, error)
}

// CreateMRRequest is the bundle a `mr` stage ships.
type CreateMRRequest struct {
	BacklogID    string
	SourceBranch string
	TargetBranch string
	Title        string
	Description  string
	Env          map[string]string
}

// CreateMRResponse carries the new MR iid back.
type CreateMRResponse struct {
	MRIID   int64
	URL     string
	CostUSD float64
}

// PollPipelineRequest asks the GitLab CI integration to wait on the MR's
// pipeline. Workers don't loop themselves; the client is expected to
// block until terminal state and return.
type PollPipelineRequest struct {
	MRIID int64
	Env   map[string]string
}

// PollPipelineResponse reports the terminal CI verdict.
type PollPipelineResponse struct {
	Status  string // "success" | "failed" | "canceled"
	CostUSD float64
	LogTail string
}

// MergeRequestArgs collects the inputs for the merge call.
type MergeRequestArgs struct {
	MRIID int64
	Env   map[string]string
}

// MergeResponse returns the merge sha.
type MergeResponse struct {
	MergedSHA string
	CostUSD   float64
}

// CleanupRequest tells the GitLab/git layer to release the worktree +
// branch tied to the run.
type CleanupRequest struct {
	WorktreePath string
	BranchName   string
	MRIID        int64
	Env          map[string]string
}

// CleanupResponse reports outcome.
type CleanupResponse struct {
	CostUSD float64
	LogTail string
}

// GitLabWorker dispatches mr / ci_watch / merge / cleanup. The same
// worker handles all four stages; it dispatches internally on jc.Stage.ID.
type GitLabWorker struct {
	Client GitLabClient
	// MRTitle / MRDescription return the strings the worker should send
	// to CreateMR. The operator wires these to draw from the spec doc;
	// tests can return constants.
	MRTitle       func(jc JobContext) string
	MRDescription func(jc JobContext) string
	// SourceBranch / TargetBranch return the refs to use. SourceBranch
	// falls back to BranchContractFor; TargetBranch falls back to "main".
	SourceBranch func(jc JobContext) string
	TargetBranch func(jc JobContext) string
}

// Run satisfies Worker.
func (w *GitLabWorker) Run(ctx context.Context, jc JobContext) (StageOutput, error) {
	if w.Client == nil {
		return StageOutput{}, fmt.Errorf("gitlab worker: client not configured")
	}
	switch jc.Stage.ID {
	case "mr":
		return w.runMR(ctx, jc)
	case "ci_watch":
		return w.runCI(ctx, jc)
	case "merge":
		return w.runMerge(ctx, jc)
	case "cleanup":
		return w.runCleanup(ctx, jc)
	default:
		return StageOutput{}, fmt.Errorf("gitlab worker: unsupported stage %q", jc.Stage.ID)
	}
}

func (w *GitLabWorker) runMR(ctx context.Context, jc JobContext) (StageOutput, error) {
	sourceBranch := w.sourceBranch(jc)
	if sourceBranch == "" {
		return StageOutput{}, fmt.Errorf("mr: source branch unavailable for backlog %q", jc.Item.ID)
	}
	req := CreateMRRequest{
		BacklogID:    jc.Item.ID,
		SourceBranch: sourceBranch,
		TargetBranch: callOr(w.TargetBranch, jc, "main"),
		Title:        callOr(w.MRTitle, jc, jc.Item.Title),
		Description:  callOr(w.MRDescription, jc, ""),
		Env:          jc.Env,
	}
	resp, err := w.Client.CreateMR(ctx, req)
	if err != nil {
		return StageOutput{}, err
	}
	return StageOutput{
		CostUSD: resp.CostUSD,
		MRIID:   resp.MRIID,
		Artifacts: map[string]any{
			"mr_url":  resp.URL,
			"mr_iid":  resp.MRIID,
			"branch":  req.SourceBranch,
			"created": true,
		},
	}, nil
}

func (w *GitLabWorker) runCI(ctx context.Context, jc JobContext) (StageOutput, error) {
	mrIID := mrIIDFrom(jc)
	if mrIID == 0 {
		return StageOutput{}, fmt.Errorf("ci_watch: no mr_iid in run")
	}
	resp, err := w.Client.PollPipeline(ctx, PollPipelineRequest{
		MRIID: mrIID,
		Env:   jc.Env,
	})
	if err != nil {
		return StageOutput{}, err
	}
	out := StageOutput{
		CostUSD: resp.CostUSD,
		LogTail: resp.LogTail,
		Artifacts: map[string]any{
			"ci_status": resp.Status,
		},
	}
	if resp.Status != "success" {
		return out, fmt.Errorf("ci pipeline %s for mr %d", resp.Status, mrIID)
	}
	return out, nil
}

func (w *GitLabWorker) runMerge(ctx context.Context, jc JobContext) (StageOutput, error) {
	mrIID := mrIIDFrom(jc)
	if mrIID == 0 {
		return StageOutput{}, fmt.Errorf("merge: no mr_iid in run")
	}
	resp, err := w.Client.Merge(ctx, MergeRequestArgs{
		MRIID: mrIID,
		Env:   jc.Env,
	})
	if err != nil {
		return StageOutput{}, err
	}
	return StageOutput{
		CostUSD:   resp.CostUSD,
		MergedSHA: resp.MergedSHA,
		Artifacts: map[string]any{
			"merged_sha": resp.MergedSHA,
		},
	}, nil
}

func (w *GitLabWorker) runCleanup(ctx context.Context, jc JobContext) (StageOutput, error) {
	sourceBranch := w.sourceBranch(jc)
	if sourceBranch == "" {
		return StageOutput{}, fmt.Errorf("cleanup: source branch unavailable for backlog %q", jc.Item.ID)
	}
	resp, err := w.Client.Cleanup(ctx, CleanupRequest{
		WorktreePath: jc.Run.WorktreePath,
		BranchName:   sourceBranch,
		MRIID:        mrIIDFrom(jc),
		Env:          jc.Env,
	})
	if err != nil {
		return StageOutput{}, err
	}
	return StageOutput{
		CostUSD: resp.CostUSD,
		LogTail: resp.LogTail,
	}, nil
}

func (w *GitLabWorker) sourceBranch(jc JobContext) string {
	if w.SourceBranch != nil {
		if branch := w.SourceBranch(jc); branch != "" {
			return branch
		}
	}
	return BranchContractFor(jc.Run, jc.Item, jc.Stage, "").SourceBranch
}

// mrIIDFrom pulls the MRIID off the run row, falling back to the
// `mr_iid` artifact recorded by the mr stage.
func mrIIDFrom(jc JobContext) int64 {
	if jc.Run.MRIID != nil && *jc.Run.MRIID != 0 {
		return *jc.Run.MRIID
	}
	if mr, ok := jc.Prior["mr"]; ok && mr.MRIID != 0 {
		return mr.MRIID
	}
	return 0
}

// callOr returns fn(jc) if fn is non-nil and returns a non-empty string;
// otherwise fallback.
func callOr(fn func(JobContext) string, jc JobContext, fallback string) string {
	if fn == nil {
		return fallback
	}
	v := fn(jc)
	if v == "" {
		return fallback
	}
	return v
}

// DefaultRoutes constructs a stage→worker map with the standard
// wiring: spawn workers for plan_slice/implement/pr_self_review, weaver
// for research, devbox for tests, gitlab for mr/ci_watch/merge/cleanup.
//
// Callers supply concrete clients; nil clients produce errors at run
// time, surfaced in stage_results.outcome=error so the failure is
// auditable rather than silent.
func DefaultRoutes(spawn SpawnClient, weaver WeaverClient, devbox DevboxClient, gitlab GitLabClient, project, agentID string, promptFor func(string) func(JobContext) string) map[string]Worker {
	if promptFor == nil {
		promptFor = func(string) func(JobContext) string { return nil }
	}
	gw := &GitLabWorker{Client: gitlab}
	return map[string]Worker{
		"plan_slice":     &SpawnWorker{Client: spawn, PromptFor: promptFor("plan_slice")},
		"research":       &WeaverWorker{Client: weaver, PromptFor: promptFor("research")},
		"implement":      &SpawnWorker{Client: spawn, PromptFor: promptFor("implement"), NeedsWorktree: true},
		"tests":          &DevboxWorker{Client: devbox, Project: project, AgentID: agentID},
		"pr_self_review": &SpawnWorker{Client: spawn, PromptFor: promptFor("pr_self_review")},
		"mr":             gw,
		"ci_watch":       gw,
		"merge":          gw,
		"cleanup":        gw,
	}
}
