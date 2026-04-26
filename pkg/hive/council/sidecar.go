package council

import (
	"encoding/json"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
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
