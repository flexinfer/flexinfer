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

// BacklogItem is the canonical record for a unit of work in the mills.
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
//
// ParentRunID + Depth implement bounded recursion (Mills v2). Top-level runs
// have ParentRunID == nil and Depth == 0; sub-runs created via
// mills_pipeline_subrun_create increment Depth from their parent. The
// dispatcher rejects creation when Depth would exceed
// policy.pipeline.max_recursion_depth (default 2).
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
	ParentRunID     *string
	Depth           int
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

// ----- Mills v2 — Hierarchical Swarm types ---------------------------------

// Squad is the persistence-side mirror of a squad manifest YAML
// (`platform/gitops/k3s/mills/squads/<name>.yaml`). The squad loader
// writes into this table on boot + on fsnotify change.
type Squad struct {
	ID               string // PK = Name; kept as ID for symmetry with other DAOs
	Name             string
	Paths            []string
	Tests            []string
	Gates            map[string]any // {required:[…], advisory:[…]}
	Ensemble         map[string]any // editor / reviewers / judge
	BudgetShare      float64
	RecursionEnabled bool
	Enabled          bool
	LastLoadedSHA    string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// SquadMemoryKind classifies a working-memory entry.
type SquadMemoryKind string

const (
	SquadMemoryMerge      SquadMemoryKind = "merge"
	SquadMemoryTechDebt   SquadMemoryKind = "tech_debt"
	SquadMemoryConvention SquadMemoryKind = "convention"
	SquadMemoryFollowup   SquadMemoryKind = "followup"
)

// SquadMemory is one append-on-merge working-memory entry. The weekly
// pruner drops rows with importance < 0.3 older than 30 days.
type SquadMemory struct {
	ID         int64
	SquadName  string
	Kind       SquadMemoryKind
	Title      string
	Body       string
	Refs       []string
	Importance float64
	CreatedAt  time.Time
	LastSeenAt time.Time
}

// SquadOutcomeKind reports how a squad-routed pipeline run resolved.
type SquadOutcomeKind string

const (
	SquadOutcomeMergedClean     SquadOutcomeKind = "merged_clean"
	SquadOutcomeMergedRegressed SquadOutcomeKind = "merged_regressed"
	SquadOutcomeFailed          SquadOutcomeKind = "failed"
	SquadOutcomeSelfVetoed      SquadOutcomeKind = "self_vetoed"
)

// SquadOutcome is the per-run record the router consults to compute
// rolling success rate per (squad, path_class).
type SquadOutcome struct {
	ID              int64
	SquadName       string
	PathClass       string
	PipelineRunID   string
	Outcome         SquadOutcomeKind
	CostUSD         float64
	DurationSeconds int64
	CreatedAt       time.Time
}

// AuditSubjectKind names what an AuditFinding row is judging.
type AuditSubjectKind string

const (
	AuditSubjectCouncilArtifact AuditSubjectKind = "council_artifact"
	AuditSubjectPipelineMerge   AuditSubjectKind = "pipeline_merge"
)

// AuditSeverity is the categorical severity emitted by the audit rubric.
type AuditSeverity string

const (
	AuditSeverityInfo     AuditSeverity = "info"
	AuditSeverityWarn     AuditSeverity = "warn"
	AuditSeverityCritical AuditSeverity = "critical"
)

// AuditFinding is one independent adversarial verdict on a council artifact
// or a pipeline merge. The auditor pool is captured per row so policy
// rotations are auditable in retrospect.
type AuditFinding struct {
	ID            int64
	SubjectKind   AuditSubjectKind
	SubjectID     string
	Severity      AuditSeverity
	RubricID      string
	SurvivalScore float64
	Findings      []map[string]any
	AuditorPool   []map[string]any
	CostUSD       float64
	CreatedAt     time.Time
}

// CrossRepoState is the lifecycle state of an atomic cross-repo run.
type CrossRepoState string

const (
	CrossRepoPlanning   CrossRepoState = "planning"
	CrossRepoOpen       CrossRepoState = "open"
	CrossRepoGatesGreen CrossRepoState = "gates_green"
	CrossRepoMerging    CrossRepoState = "merging"
	CrossRepoMerged     CrossRepoState = "merged"
	CrossRepoReverted   CrossRepoState = "reverted"
	CrossRepoFailed     CrossRepoState = "failed"
)

// CrossRepoRepoEntry is one repo's slice of an atomic cross-repo run.
type CrossRepoRepoEntry struct {
	ProjectID  int64  `json:"project_id"`
	RepoName   string `json:"repo_name,omitempty"`
	Branch     string `json:"branch"`
	MRIID      *int64 `json:"mr_iid,omitempty"`
	CIStatus   string `json:"ci_status,omitempty"`
	GateStatus string `json:"gate_status,omitempty"`
}

// CrossRepoRun coordinates a backlog item that spans multiple repos.
type CrossRepoRun struct {
	ID                string
	BacklogItemID     string
	Repos             []CrossRepoRepoEntry
	State             CrossRepoState
	AtomicityStrategy string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// DebateRole names which step in a Council Debate round emitted this row.
type DebateRole string

const (
	DebateRoleEditorProposes    DebateRole = "editor_proposes"
	DebateRoleReviewerCritiques DebateRole = "reviewer_critiques"
	DebateRoleModeratorDecision DebateRole = "moderator_decision"
	DebateRoleEditorRevises     DebateRole = "editor_revises"
)

// CouncilDebateRound is one entry in a Council Debate transcript.
// ArtifactDeltas references path + line range pairs so the sidecar
// stays small even on long debates.
type CouncilDebateRound struct {
	ID             int64
	CouncilRunID   string
	RoundIndex     int
	Role           DebateRole
	CostUSD        float64
	Summary        string
	ArtifactDeltas []map[string]any
	CreatedAt      time.Time
}

// PolicyProposalKind classifies an adaptive-policy suggestion.
type PolicyProposalKind string

const (
	PolicyProposalRelax          PolicyProposalKind = "relax"
	PolicyProposalTighten        PolicyProposalKind = "tighten"
	PolicyProposalRotateEnsemble PolicyProposalKind = "rotate_ensemble"
)

// PolicyProposalState tracks the lifecycle of one adaptive proposal.
type PolicyProposalState string

const (
	PolicyProposalPending      PolicyProposalState = "pending"
	PolicyProposalAppliedHuman PolicyProposalState = "applied_human"
	PolicyProposalAppliedAuto  PolicyProposalState = "applied_auto"
	PolicyProposalRejected     PolicyProposalState = "rejected"
	PolicyProposalReverted     PolicyProposalState = "reverted"
)

// PolicyProposal is one machine-emitted suggestion to relax, tighten, or
// rotate a policy element. Rationale cites kpi_snapshots / eval_scores /
// audit_findings / gate_outcomes; the .loom/mills/policy_proposals/<date>.md
// markdown is the human-facing copy.
type PolicyProposal struct {
	ID             int64
	ProposalDate   string // YYYY-MM-DD
	Kind           PolicyProposalKind
	Target         string
	Diff           string
	Rationale      string
	State          PolicyProposalState
	AppliedAt      *time.Time
	RevertDeadline *time.Time
	CreatedAt      time.Time
}
