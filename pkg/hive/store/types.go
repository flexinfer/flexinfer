package store

import "time"

// BacklogState is the lifecycle state of a backlog item.
type BacklogState string

const (
	BacklogQueued    BacklogState = "queued"
	BacklogRunning   BacklogState = "running"
	BacklogMerged    BacklogState = "merged"
	BacklogEscalated BacklogState = "escalated"
	BacklogPaused    BacklogState = "paused"
)

// Priority is the human-readable priority bucket for a backlog item.
type Priority string

const (
	P0 Priority = "P0"
	P1 Priority = "P1"
	P2 Priority = "P2"
	P3 Priority = "P3"
)

// Slice is one independent unit of work within a backlog item.
type Slice struct {
	Name         string   `json:"name"`
	Files        []string `json:"files"`
	Tests        []string `json:"tests"`
	ParallelWith []string `json:"parallel_with,omitempty"`
}

// SuccessCriteria captures the machine-checkable acceptance for a backlog item.
type SuccessCriteria struct {
	Tests       []string `json:"tests,omitempty"`
	Metrics     []string `json:"metrics,omitempty"`
	ManualCheck string   `json:"manual_check,omitempty"`
}

// Budget bounds the cost/turn/wall-clock for a backlog item's pipeline run.
type Budget struct {
	MaxCostUSD         float64 `json:"max_cost_usd,omitempty"`
	MaxTurns           int     `json:"max_turns,omitempty"`
	MaxPipelineMinutes int     `json:"max_pipeline_minutes,omitempty"`
}

// ItemPolicy carries per-item override of the global policy.
type ItemPolicy struct {
	RequireHumanReview    bool     `json:"require_human_review,omitempty"`
	AutoMerge             bool     `json:"auto_merge,omitempty"`
	ProtectedPathsTouched []string `json:"protected_paths_touched,omitempty"`
}

// BacklogItem is the canonical record for a unit of work in the hive.
type BacklogItem struct {
	ID             string
	GitLabIssueIID *int64
	Title          string
	Labels         []string
	State          BacklogState
	Priority       Priority
	SpecDoc        string
	SpecAnchor     string
	Success        SuccessCriteria
	Budget         Budget
	Policy         ItemPolicy
	Slices         []Slice
	Dependencies   []string
	CouncilRunID   *string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CouncilTrigger identifies what caused a council run.
type CouncilTrigger string

const (
	CouncilTriggerCron     CouncilTrigger = "cron"
	CouncilTriggerRoadmap  CouncilTrigger = "roadmap"
	CouncilTriggerIncident CouncilTrigger = "incident"
	CouncilTriggerManual   CouncilTrigger = "manual"
)

// CouncilOutcome reports the final status of a council run.
type CouncilOutcome string

const (
	CouncilOutcomeSuccess  CouncilOutcome = "success"
	CouncilOutcomePartial  CouncilOutcome = "partial"
	CouncilOutcomeError    CouncilOutcome = "error"
	CouncilOutcomeConflict CouncilOutcome = "conflict"
)

// ArtifactRef identifies one artifact emitted by a council run.
type ArtifactRef struct {
	Kind string `json:"kind"`
	Path string `json:"path,omitempty"`
	ID   string `json:"id,omitempty"`
}

// BacklogDeltas summarises the backlog mutations a council run intends.
type BacklogDeltas struct {
	Created []string `json:"created,omitempty"`
	Updated []string `json:"updated,omitempty"`
	Closed  []string `json:"closed,omitempty"`
}

// CouncilRun is one execution of the council ensemble.
type CouncilRun struct {
	ID              string
	Trigger         CouncilTrigger
	StartedAt       time.Time
	EndedAt         *time.Time
	Outcome         CouncilOutcome
	CostFrontierUSD float64
	CostLocalUSD    float64
	Artifacts       []ArtifactRef
	BacklogDeltas   BacklogDeltas
	Sidecar         map[string]any
	BranchName      string
	CommitSHA       string
	Notes           string
}

// PipelineState is the lifecycle state of a pipeline run.
type PipelineState string

const (
	PipelineQueued       PipelineState = "queued"
	PipelinePlanning     PipelineState = "planning"
	PipelineSlicing      PipelineState = "slicing"
	PipelineImplementing PipelineState = "implementing"
	PipelineTesting      PipelineState = "testing"
	PipelineReviewing    PipelineState = "reviewing"
	PipelineMR           PipelineState = "mr"
	PipelineCI           PipelineState = "ci"
	PipelineMerging      PipelineState = "merging"
	PipelineDone         PipelineState = "done"
	PipelineEscalated    PipelineState = "escalated"
	PipelinePaused       PipelineState = "paused"
)

// PipelineRun is one execution of the pipeline DAG for a backlog item.
type PipelineRun struct {
	ID              string
	BacklogID       string
	Template        string
	State           PipelineState
	CurrentStage    string
	Attempts        int
	WorktreePath    string
	MRIID           *int64
	StartedAt       time.Time
	EndedAt         *time.Time
	CostUSD         float64
	ParentSessionID string
}

// StageOutcome captures whether one stage attempt succeeded.
type StageOutcome string

const (
	StageOutcomeSuccess  StageOutcome = "success"
	StageOutcomeGateFail StageOutcome = "gate_fail"
	StageOutcomeError    StageOutcome = "error"
)

// StageResult is one attempt to execute one stage of a pipeline run.
type StageResult struct {
	ID            int64
	PipelineRunID string
	Stage         string
	Attempt       int
	StartedAt     time.Time
	EndedAt       *time.Time
	Outcome       *StageOutcome
	SpawnID       string
	CostUSD       float64
	Artifacts     map[string]any
	LogTail       string
}

// GateOutcomeKind reports whether a gate passed.
type GateOutcomeKind string

const (
	GateOutcomePass GateOutcomeKind = "pass"
	GateOutcomeFail GateOutcomeKind = "fail"
	GateOutcomeSkip GateOutcomeKind = "skip"
)

// GateOutcome is the persisted record of one gate evaluation.
type GateOutcome struct {
	ID            int64
	PipelineRunID string
	AfterStage    string
	GateName      string
	Outcome       GateOutcomeKind
	Reasons       []string
	JudgedBy      string
	EvaluatedAt   time.Time
}

// KPISnapshot is a rolled-up metric set persisted per reconcile tick.
type KPISnapshot struct {
	ID            int64
	SnapshotAt    time.Time
	WindowSeconds int
	Metrics       map[string]any
}

// EvalSubjectKind names what an EvalScore is judging.
type EvalSubjectKind string

const (
	EvalSubjectCouncilRun  EvalSubjectKind = "council_run"
	EvalSubjectPipelineRun EvalSubjectKind = "pipeline_run"
	EvalSubjectCrossRun    EvalSubjectKind = "cross_run"
)

// EvalScore is one judgment of a council run, pipeline run, or cross-run window.
type EvalScore struct {
	ID          int64
	SubjectKind EvalSubjectKind
	SubjectID   string
	Rubric      string
	Score       float64
	Breakdown   map[string]any
	JudgedBy    string
	EvaluatedAt time.Time
	Notes       string
}

// Event is an append-only audit / debug record.
type Event struct {
	ID          int64
	OccurredAt  time.Time
	Actor       string
	Kind        string
	SubjectKind string
	SubjectID   string
	Payload     map[string]any
}

// RoadmapIntent is one extracted theme/priority/constraint from ROADMAP.md.
type RoadmapIntent struct {
	ID                   int64
	Theme                string
	Priority             int
	Summary              string
	Constraints          map[string]any
	LastSeenInRoadmapSHA string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}
