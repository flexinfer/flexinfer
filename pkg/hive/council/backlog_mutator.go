package council

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// BacklogProposal is the editor's structured ask for one new backlog
// item. The mutator translates one proposal → one canonical
// store.BacklogItem + one .loom/backlog/<id>.yaml export. GitLab sync
// is a separate post-step (BacklogMutator.SyncToGitLab, slot reserved
// for the mcp-gitlab integration in a follow-up slice).
type BacklogProposal struct {
	// IDHint, if set, is used as the canonical id directly. Empty hint
	// → auto-generated HIVE-YYYY-MM-DD-NNN. Tests use IDHint for
	// determinism; the real editor leaves it empty.
	IDHint string

	Title        string
	Labels       []string
	Priority     store.Priority
	SpecDoc      string // .loom/ path back-reference
	SpecAnchor   string // section anchor inside SpecDoc
	Slices       []store.Slice
	Success      store.SuccessCriteria
	Budget       store.Budget
	Policy       store.ItemPolicy
	Dependencies []string

	// Notes captures free-form rationale the editor wants to record on
	// the item. Surfaces in the YAML export only; not persisted to the
	// canonical store.
	Notes string
}

// MutationOptions tunes one Apply call. All fields optional.
type MutationOptions struct {
	// MaxNewItems caps the number of created items per run; the spec
	// requires this default to ≤ 10. A proposal slice longer than the
	// cap is truncated (deterministic prefix kept, surplus dropped).
	MaxNewItems int

	// SkipBecausePartial short-circuits the whole call. Set when the
	// eval Loop A judge marked the run partial; the mutator still
	// returns a result with TotalProposed populated so the audit log
	// records what was *intended* even though nothing landed.
	SkipBecausePartial bool

	// RepoRoot is the absolute path to the loom-core checkout the
	// mutator should write the .loom/backlog/ exports into. Empty
	// disables YAML export (canonical store still gets the inserts).
	RepoRoot string
}

// MutationResult is the audit footprint of one Apply call.
type MutationResult struct {
	TotalProposed   int
	Skipped         bool
	SkipReason      string
	CreatedItems    []*store.BacklogItem
	CreatedYAMLPath []string
	Truncated       int // proposals dropped to honour MaxNewItems
}

// defaultMaxNewItems matches the spec's "≤ 10 new items per council
// run" rule. Tunable via MutationOptions.MaxNewItems.
const defaultMaxNewItems = 10

// BacklogMutator translates EditorOutput.BacklogProposals into canonical
// store inserts + derived YAML exports. Canonical store wins as the
// source of truth; YAML is regenerated on every run from the canonical
// view so manual YAML edits are imported via reconciler diff (slice 4.x).
type BacklogMutator struct {
	Store *store.Store
	// Now is injectable for deterministic IDs.
	Now func() time.Time
}

// Apply persists every accepted proposal into the canonical store, then
// writes a derived YAML for each. Returns the populated result so the
// caller can update store.CouncilRun.BacklogDeltas in one pass.
func (m *BacklogMutator) Apply(ctx context.Context, runID string, out *EditorOutput, opts MutationOptions) (*MutationResult, error) {
	if m == nil || m.Store == nil {
		return nil, errors.New("council: BacklogMutator not configured")
	}
	if out == nil {
		return nil, errors.New("council: EditorOutput required")
	}
	if runID == "" {
		return nil, errors.New("council: runID required")
	}

	res := &MutationResult{TotalProposed: len(out.BacklogProposals)}
	if opts.SkipBecausePartial {
		res.Skipped = true
		res.SkipReason = "council run scored below eval threshold; mutations dropped"
		return res, nil
	}

	cap := opts.MaxNewItems
	if cap <= 0 {
		cap = defaultMaxNewItems
	}
	proposals := out.BacklogProposals
	if len(proposals) > cap {
		res.Truncated = len(proposals) - cap
		proposals = proposals[:cap]
	}

	now := time.Now().UTC()
	if m.Now != nil {
		now = m.Now().UTC()
	}

	for i, p := range proposals {
		item, err := m.persistOne(ctx, runID, now, i, p)
		if err != nil {
			return res, fmt.Errorf("persist proposal %d (%q): %w", i, p.Title, err)
		}
		res.CreatedItems = append(res.CreatedItems, item)
	}

	if opts.RepoRoot != "" {
		if err := m.exportYAML(opts.RepoRoot, res); err != nil {
			return res, fmt.Errorf("export yaml: %w", err)
		}
	}
	return res, nil
}

// persistOne writes one BacklogProposal to the canonical store. The id
// is either the hint (tests) or HIVE-YYYY-MM-DD-NNN (auto). NNN is the
// 1-based index within the current run, zero-padded to 3 digits so
// alphabetical sort stays in proposal order.
func (m *BacklogMutator) persistOne(ctx context.Context, runID string, now time.Time, idx int, p BacklogProposal) (*store.BacklogItem, error) {
	id := p.IDHint
	if id == "" {
		id = fmt.Sprintf("HIVE-%s-%03d", now.Format("2006-01-02"), idx+1)
	}
	if p.Title == "" {
		return nil, fmt.Errorf("proposal missing Title")
	}
	priority := p.Priority
	if priority == "" {
		priority = store.P2
	}
	council := runID
	item := &store.BacklogItem{
		ID:           id,
		Title:        p.Title,
		Labels:       append([]string(nil), p.Labels...),
		State:        store.BacklogQueued,
		Priority:     priority,
		SpecDoc:      p.SpecDoc,
		SpecAnchor:   p.SpecAnchor,
		Success:      p.Success,
		Budget:       p.Budget,
		Policy:       p.Policy,
		Slices:       append([]store.Slice(nil), p.Slices...),
		Dependencies: append([]string(nil), p.Dependencies...),
		CouncilRunID: &council,
		CreatedBy:    "council",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := m.Store.Backlog.Put(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

// backlogYAML is the on-disk shape of one .loom/backlog/<id>.yaml
// export. Mirrors .loom/90- §"Backlog item YAML" — fields named
// snake_case for git diff readability.
type backlogYAML struct {
	ID             string                `yaml:"id"`
	GitLabIssueIID *int64                `yaml:"gitlab_issue_iid,omitempty"`
	Title          string                `yaml:"title"`
	Labels         []string              `yaml:"labels,omitempty"`
	State          string                `yaml:"state"`
	Priority       string                `yaml:"priority"`
	SpecDoc        string                `yaml:"spec_doc,omitempty"`
	SpecAnchor     string                `yaml:"spec_anchor,omitempty"`
	Slices         []store.Slice         `yaml:"slices,omitempty"`
	Success        store.SuccessCriteria `yaml:"success,omitempty"`
	Budget         store.Budget          `yaml:"budget,omitempty"`
	Policy         store.ItemPolicy      `yaml:"policy,omitempty"`
	Dependencies   []string              `yaml:"dependencies,omitempty"`
	CreatedBy      string                `yaml:"created_by"`
	CreatedAt      time.Time             `yaml:"created_at"`
	CouncilRunID   string                `yaml:"council_run_id,omitempty"`
}

// exportYAML writes one .loom/backlog/<id>.yaml per created item. Files
// are written atomically (tempfile+rename) so the watcher hooks don't
// observe a partial document.
func (m *BacklogMutator) exportYAML(repoRoot string, res *MutationResult) error {
	dir := filepath.Join(repoRoot, ".loom", "backlog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir backlog: %w", err)
	}
	for _, item := range res.CreatedItems {
		out := backlogYAML{
			ID:             item.ID,
			GitLabIssueIID: item.GitLabIssueIID,
			Title:          item.Title,
			Labels:         item.Labels,
			State:          string(item.State),
			Priority:       string(item.Priority),
			SpecDoc:        item.SpecDoc,
			SpecAnchor:     item.SpecAnchor,
			Slices:         item.Slices,
			Success:        item.Success,
			Budget:         item.Budget,
			Policy:         item.Policy,
			Dependencies:   item.Dependencies,
			CreatedBy:      item.CreatedBy,
			CreatedAt:      item.CreatedAt,
		}
		if item.CouncilRunID != nil {
			out.CouncilRunID = *item.CouncilRunID
		}
		body, err := yaml.Marshal(&out)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", item.ID, err)
		}
		path := filepath.Join(dir, item.ID+".yaml")
		if err := writeFileAtomicCouncil(path, body, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", item.ID, err)
		}
		res.CreatedYAMLPath = append(res.CreatedYAMLPath,
			filepath.Join(".loom", "backlog", item.ID+".yaml"))
	}
	return nil
}

// CreatedIDs is a tiny helper for callers updating
// store.CouncilRun.BacklogDeltas without iterating themselves.
func (r *MutationResult) CreatedIDs() []string {
	out := make([]string, 0, len(r.CreatedItems))
	for _, it := range r.CreatedItems {
		out = append(out, it.ID)
	}
	return out
}

// summarizeReasons is exported so the operator's structured logging
// can render a short single-string summary of a mutation result.
func (r *MutationResult) Summary() string {
	if r.Skipped {
		return r.SkipReason
	}
	parts := []string{
		fmt.Sprintf("created=%d", len(r.CreatedItems)),
		fmt.Sprintf("proposed=%d", r.TotalProposed),
	}
	if r.Truncated > 0 {
		parts = append(parts, fmt.Sprintf("truncated=%d", r.Truncated))
	}
	return strings.Join(parts, " ")
}
