package council

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/mills/store"
)

// BriefSources collects the input handles the brief assembler needs. The
// reviewer dispatcher (slice 3.3) and editor (slice 3.4) get a *Brief
// downstream; nothing past Compile is allowed to re-fetch from these
// sources, so a single tick of the council sees a coherent snapshot.
//
// The full spec calls for recent-merges + alerts + Loki errors fetched
// over MCP. Those land in slice 3.7 (triggers) once the council is
// orchestrated; for slice 3.2 the brief is canonical-store + filesystem
// only, which is enough for dryrun + the eval Loop A judge.
type BriefSources struct {
	// Store is the canonical SQLite-backed view. Required.
	Store *store.Store

	// RepoRoot is the absolute path to the loom-core checkout the
	// council should reference (typically the operator's mounted clone
	// or a freshly-allocated worktree). Used to read .loom/00-index.md
	// and ROADMAP.md.
	RepoRoot string

	// MaxBytes caps the rendered markdown size. Zero falls back to
	// briefDefaultMaxBytes (~16k chars ≈ 4k tokens). Sections are
	// rendered in priority order; later sections truncate first.
	MaxBytes int

	// Now is injectable for deterministic test snapshots. Defaults to
	// time.Now.
	Now func() time.Time
}

// briefDefaultMaxBytes is roughly 4k tokens at 4 chars/token. Generous
// for a planning brief; we'll tighten if reviewers consistently hit it.
const briefDefaultMaxBytes = 16 * 1024

// Brief is the assembled prompt header. Markdown is what the editor +
// reviewers consume verbatim; Sections is the structured projection
// the eval Loop A judge can reference without re-parsing the markdown.
type Brief struct {
	Markdown string
	Sections []BriefSection

	// SourceCounts records what made it into the brief and what was
	// truncated. Surfaced into the council_runs sidecar for forensics.
	SourceCounts BriefSourceCounts
}

// BriefSection is one labelled chunk in the rendered brief. Order in the
// slice matches order in Markdown.
type BriefSection struct {
	Heading string
	Body    string
}

// BriefSourceCounts is the audit footprint of one Compile call.
type BriefSourceCounts struct {
	Intents          int
	BacklogQueued    int
	BacklogActive    int
	KPISnapshot      bool
	IndexBytes       int
	CrossRunFindings int // number of Loop C eval rows surfaced
	TruncatedTo      int // final byte length after truncation, equal to MaxBytes if we hit the cap
}

// crossRunBriefWindow is how far back Compile reaches for Loop C
// findings to surface in the next council brief. Matches the cross-run
// scheduler's weekly cadence with a small grace overlap so a brief
// compiled Sunday afternoon still sees that morning's findings.
const crossRunBriefWindow = 8 * 24 * time.Hour

// Compile assembles the brief from the configured sources. Sections are
// rendered in priority order:
//  1. Roadmap intents (machine-shaped goals)
//  2. Recent KPIs (most recent 1d snapshot if any)
//  3. Backlog snapshot (queued + active counts; titles for queued)
//  4. .loom/00-index.md excerpt (operator-curated planning thread)
//
// Trailing sections truncate first if MaxBytes is hit, so a tight cap
// preserves the structured signals over the prose tail.
func Compile(ctx context.Context, src BriefSources) (*Brief, error) {
	if src.Store == nil {
		return nil, fmt.Errorf("council: brief requires a Store")
	}
	now := time.Now().UTC()
	if src.Now != nil {
		now = src.Now().UTC()
	}
	maxBytes := src.MaxBytes
	if maxBytes <= 0 {
		maxBytes = briefDefaultMaxBytes
	}

	b := &Brief{}

	intents, err := src.Store.Roadmap.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("brief: list roadmap: %w", err)
	}
	b.Sections = append(b.Sections, BriefSection{
		Heading: "Roadmap intents",
		Body:    renderIntents(intents),
	})
	b.SourceCounts.Intents = len(intents)

	if snap, err := src.Store.KPI.Latest(ctx, 86400); err == nil {
		b.Sections = append(b.Sections, BriefSection{
			Heading: "Mills KPIs (last 24h snapshot)",
			Body:    renderKPI(snap),
		})
		b.SourceCounts.KPISnapshot = true
	}

	queued, _ := src.Store.Backlog.ListByState(ctx, store.BacklogQueued)
	active, _ := src.Store.Pipeline.CountActive(ctx)
	b.Sections = append(b.Sections, BriefSection{
		Heading: "Backlog snapshot",
		Body:    renderBacklog(queued, active),
	})
	b.SourceCounts.BacklogQueued = len(queued)
	b.SourceCounts.BacklogActive = active

	if src.RepoRoot != "" {
		if body, n := readBriefFile(filepath.Join(src.RepoRoot, ".loom/00-index.md"), 8*1024); body != "" {
			b.Sections = append(b.Sections, BriefSection{
				Heading: ".loom/00-index.md (active planning thread)",
				Body:    body,
			})
			b.SourceCounts.IndexBytes = n
		}
	}

	// Loop C — surface the most recent cross-run findings so the council
	// can react to flaky gates / stale plans / divergent outcomes
	// without the operator humans having to query the eval table by hand.
	// We fetch the whole crossRunBriefWindow worth and let the renderer
	// drop the section if there are no findings.
	scores, _ := src.Store.Eval.ListSince(ctx, now.Add(-crossRunBriefWindow), 50)
	crossRunFindings := filterCrossRun(scores)
	if len(crossRunFindings) > 0 {
		b.Sections = append(b.Sections, BriefSection{
			Heading: "Cross-run findings (Loop C, last 7 days)",
			Body:    renderCrossRun(crossRunFindings),
		})
		b.SourceCounts.CrossRunFindings = len(crossRunFindings)
	}

	b.Markdown = renderMarkdown(now, b.Sections, maxBytes)
	b.SourceCounts.TruncatedTo = len(b.Markdown)
	return b, nil
}

// filterCrossRun selects only Loop C eval scores from a mixed result.
func filterCrossRun(scores []*store.EvalScore) []*store.EvalScore {
	out := make([]*store.EvalScore, 0, len(scores))
	for _, s := range scores {
		if s.SubjectKind == store.EvalSubjectCrossRun {
			out = append(out, s)
		}
	}
	return out
}

// renderCrossRun formats Loop C findings. Score < 1.0 means the rubric
// flagged something — we surface the rubric, score, and notes line so
// the council brief reads at a glance.
func renderCrossRun(scores []*store.EvalScore) string {
	var b strings.Builder
	for _, s := range scores {
		notes := s.Notes
		if notes == "" {
			notes = "_(no details)_"
		}
		fmt.Fprintf(&b, "- **%s** (score `%.2f`, %s): %s\n",
			s.Rubric, s.Score, s.SubjectID, notes)
	}
	return b.String()
}

// ----- renderers -----

func renderIntents(intents []*store.RoadmapIntent) string {
	if len(intents) == 0 {
		return "_(no roadmap intents in canonical store; run the extractor first)_"
	}
	var b strings.Builder
	for _, i := range intents {
		fmt.Fprintf(&b, "- **P%d %s** — %s\n", i.Priority, i.Theme, i.Summary)
	}
	return b.String()
}

func renderKPI(snap *store.KPISnapshot) string {
	if snap == nil {
		return "_(no KPI snapshot yet)_"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "_Snapshot at %s_\n\n", snap.SnapshotAt.Format(time.RFC3339))
	for k, v := range snap.Metrics {
		fmt.Fprintf(&b, "- `%s`: %v\n", k, v)
	}
	return b.String()
}

func renderBacklog(queued []*store.BacklogItem, active int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Queued: **%d** | Active pipeline runs: **%d**\n", len(queued), active)
	if len(queued) == 0 {
		return b.String()
	}
	b.WriteString("\nQueued items:\n")
	for _, item := range queued {
		fmt.Fprintf(&b, "- `%s` %s — %s\n", item.ID, item.Priority, item.Title)
	}
	return b.String()
}

// renderMarkdown stitches sections together with H2 headings, prefixed
// with a deterministic "compiled at" timestamp so reviewers see when the
// snapshot was taken. Truncation happens at section boundaries — we'd
// rather drop a section entirely than render half of one.
func renderMarkdown(now time.Time, sections []BriefSection, maxBytes int) string {
	header := fmt.Sprintf("# Council Brief — compiled %s\n\n", now.Format(time.RFC3339))
	out := strings.Builder{}
	out.WriteString(header)
	for _, s := range sections {
		chunk := fmt.Sprintf("\n## %s\n\n%s\n", s.Heading, strings.TrimRight(s.Body, "\n"))
		if out.Len()+len(chunk) > maxBytes {
			fmt.Fprintf(&out, "\n_(brief truncated at %d bytes — section %q dropped to fit)_\n", maxBytes, s.Heading)
			break
		}
		out.WriteString(chunk)
	}
	return out.String()
}

// readBriefFile reads up to maxBytes from path. Returns ("", 0) on any
// error so the brief stays well-formed if the file is missing.
func readBriefFile(path string, maxBytes int) (string, int) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	if err != nil {
		return "", 0
	}
	return string(body), len(body)
}
