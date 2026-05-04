package council

import (
	"encoding/json"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// Sidecar is the machine-readable deliverable of every council run.
// Markdown is what humans read; the sidecar is what the pipeline
// scheduler + the eval Loop A judge consume. Field names follow the
// JSON conventions in .loom/90- §"Council artifact sidecar".
type Sidecar struct {
	CouncilRunID    string              `json:"council_run_id"`
	Models          []string            `json:"models"`
	StartedAt       time.Time           `json:"started_at"`
	EndedAt         *time.Time          `json:"ended_at,omitempty"`
	CostUSD         SidecarCost         `json:"cost_usd"`
	Artifacts       []store.ArtifactRef `json:"artifacts"`
	BacklogDeltas   SidecarBacklog      `json:"backlog_deltas"`
	SignalsConsumed []string            `json:"signals_consumed,omitempty"`
	Notes           string              `json:"notes,omitempty"`

	// Debate is populated only when the run executed under Council
	// Debate Mode (Mills v2 Phase 5). Single-pass v1 runs leave this
	// nil and emit no `debate` key in JSON.
	Debate *SidecarDebate `json:"debate,omitempty"`
}

// SidecarDebate records the per-round transcript of a Debate Mode
// council run. Shape mirrors .loom/93- §"Council Debate Mode" so the
// HUD's "Debate Rounds" expander and the audit pool can read the same
// fields off disk and out of the in-memory sidecar.
type SidecarDebate struct {
	// Enabled is true when debate mode actually ran (vs. fell through
	// to single-pass for a non-debate trigger). Always true when
	// Debate is non-nil; redundant by construction but explicit so a
	// JSON-only consumer can `if (sidecar.debate?.enabled) { ... }`.
	Enabled bool `json:"enabled"`
	// Rounds is the chronological transcript. Round 0 is the editor's
	// initial propose; subsequent rounds alternate
	// reviewer_critiques / moderator_decision / editor_revises.
	Rounds []SidecarDebateRound `json:"rounds"`
	// EarlyExitReason is set when the run exited before reaching the
	// configured MaxRounds — typically "budget" (≥80% of debate USD
	// cap consumed) or "converged" (moderator declared agreement).
	// Empty string when the run ran to completion / max rounds.
	EarlyExitReason string `json:"early_exit_reason,omitempty"`
	// TotalCostUSD is the sum of every round's CostUSD. Useful for
	// the HUD card without re-walking the rounds slice.
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// SidecarDebateRound is one entry in the debate transcript. Roles
// match the dao_debate.role column enum so the persistence and the
// JSON wire format use identical strings.
type SidecarDebateRound struct {
	// Round is the 0-indexed round number. Round 0 is editor.propose;
	// odd rounds (1, 3, ...) are reviewer/moderator pairs; even rounds
	// (2, ...) are editor.revise.
	Round int `json:"round"`
	// Role is one of: editor_proposes | reviewer_critiques |
	// moderator_decision | editor_revises.
	Role string `json:"role"`
	// CostUSD is the USD spend attributed to this entry only.
	CostUSD float64 `json:"cost_usd"`
	// Summary is a short markdown blurb (≤ a few hundred chars). The
	// full transcript stays in council_debate_rounds.summary in SQLite.
	Summary string `json:"summary,omitempty"`
	// Converged is set on moderator_decision rows. nil for non-moderator
	// rows so the JSON omits the key.
	Converged *bool `json:"converged,omitempty"`
	// FocusAreas is set on moderator_decision rows when the moderator
	// declined to converge and instead issued focus tags for the next
	// editor.revise round.
	FocusAreas []string `json:"focus_areas,omitempty"`
	// ArtifactDeltas optionally describes which sections of which
	// artifact docs the editor.revise round touched. Lets reviewers
	// diff round outputs without re-running them.
	ArtifactDeltas []SidecarDebateDelta `json:"artifact_deltas,omitempty"`
}

// SidecarDebateDelta names a slice of an artifact doc the editor
// modified between rounds. The line range is best-effort (LLMs often
// regenerate whole sections); the action enumerates the high-level
// kind of edit.
type SidecarDebateDelta struct {
	Path      string `json:"path"`
	LineRange string `json:"line_range,omitempty"`
	Action    string `json:"action,omitempty"` // add | edit | remove
}

// SidecarCost splits frontier vs local spend so the eval framework's
// Council ROI rollup can attribute correctly.
type SidecarCost struct {
	Frontier float64 `json:"frontier"`
	Local    float64 `json:"local"`
}

// SidecarBacklog summarises the backlog mutations a council run intends.
// IDs are persisted in store.CouncilRun.BacklogDeltas; the sidecar holds
// only the count summary so reviewers / humans can grok the magnitude
// at a glance.
type SidecarBacklog struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Closed  int `json:"closed"`
}

// Marshal returns indented JSON suitable for committing to .loom/. The
// indentation is stable across runs so a no-change council run produces
// a byte-for-byte identical file.
func (s *Sidecar) Marshal() ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
