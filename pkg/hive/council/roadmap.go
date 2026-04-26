// Package council is the planning tier of Loom Hive — the editor-pattern
// ensemble that turns ROADMAP.md, recent merges, alerts, and current KPIs
// into committed planning artifacts and a backlog delta.
//
// The package is organised one-file-per-stage of a council run:
//   - roadmap.go        — extract structured intents from ROADMAP.md
//   - brief.go          — assemble the deterministic prompt header
//   - reviewer.go       — dispatch to lensed reviewer agents
//   - editor.go         — drive the editor agent (multi-turn spawn)
//   - artifacts.go      — write markdown + sidecar to disk + open council branch
//   - backlog_mutator.go — translate sidecar deltas into canonical store + GitLab
//
// Each piece is pure-function-shaped where possible so the operator can
// dryrun the council against a scratch DB without spawning real agents.
package council

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// Roadmap is the in-memory view of ROADMAP.md the council consumes. The
// extractor builds one of these per council run; the reconciler then
// upserts every intent into the canonical roadmap_intents table.
type Roadmap struct {
	// SHA is the git revision the file was read from. Persisted into
	// roadmap_intents.last_seen_in_roadmap_sha so a future
	// DeleteStale(sha) call can retire intents whose human edits removed
	// the source bullet.
	SHA string

	// Intents are the open (unchecked) bullets the council will plan
	// against. Closed (checked) bullets are intentionally dropped — the
	// council looks forward, not back.
	Intents []*store.RoadmapIntent
}

// ExtractFromFile reads path and parses it as ROADMAP.md. The sha argument
// is the value the upsert path will record into last_seen_in_roadmap_sha;
// the caller is responsible for passing the right value (typically
// `git rev-parse HEAD`-of-the-file). Empty sha is allowed for tests.
func ExtractFromFile(path, sha string) (*Roadmap, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("council: open roadmap: %w", err)
	}
	defer f.Close()
	return Extract(f, sha)
}

// Extract parses ROADMAP.md from r. Exposed so tests + the brief assembler
// can drive it from in-memory fixtures.
func Extract(r io.Reader, sha string) (*Roadmap, error) {
	if r == nil {
		return nil, errors.New("council: roadmap reader required")
	}
	rd := bufio.NewScanner(r)
	rd.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	now := time.Now().UTC()
	out := &Roadmap{SHA: sha}

	var (
		currentSection string
		currentPrio    = unknownPriority
	)
	for rd.Scan() {
		line := rd.Text()

		if name, prio, ok := parseSectionHeader(line); ok {
			currentSection = name
			currentPrio = prio
			continue
		}

		title, done, ok := parseTaskBullet(line)
		if !ok {
			continue
		}
		if done {
			continue
		}
		if currentSection == "" || currentPrio == unknownPriority {
			// Tasks before the first tiered section (e.g. under "Current
			// Status") are informational; don't promote them to intents.
			continue
		}
		intent := &store.RoadmapIntent{
			Theme:                currentSection,
			Priority:             currentPrio,
			Summary:              title,
			Constraints:          map[string]any{},
			LastSeenInRoadmapSHA: sha,
			CreatedAt:            now,
			UpdatedAt:            now,
		}
		out.Intents = append(out.Intents, intent)
	}
	if err := rd.Err(); err != nil {
		return nil, fmt.Errorf("council: scan roadmap: %w", err)
	}
	return out, nil
}

// SyncToStore applies the extracted intents to the canonical store and
// retires any prior intents whose last_seen_in_roadmap_sha differs from
// the current run. Returns counts for the audit log.
type SyncResult struct {
	Upserted int
	Retired  int64
}

// SyncToStore is the deterministic upsert + retire pass. Idempotent: two
// consecutive runs against the same Roadmap produce 0 changes the second
// time (RoadmapDAO.Upsert is keyed on theme+summary).
func (r *Roadmap) SyncToStore(ctx context.Context, dao *store.RoadmapDAO) (SyncResult, error) {
	if r == nil {
		return SyncResult{}, errors.New("council: roadmap is nil")
	}
	if dao == nil {
		return SyncResult{}, errors.New("council: roadmap dao is nil")
	}

	res := SyncResult{}
	for _, intent := range r.Intents {
		if err := dao.Upsert(ctx, intent); err != nil {
			return res, fmt.Errorf("upsert %q: %w", intent.Summary, err)
		}
		res.Upserted++
	}
	if r.SHA != "" {
		retired, err := dao.DeleteStale(ctx, r.SHA)
		if err != nil {
			return res, fmt.Errorf("retire stale: %w", err)
		}
		res.Retired = retired
	}
	return res, nil
}

// ----- internal: parsing helpers -----

const unknownPriority = -1

// sectionHeaderRE matches `## <title>` (and only H2s). H3s are sub-themes
// that we attach to their parent rather than promote to standalone
// intents.
var sectionHeaderRE = regexp.MustCompile(`^##\s+(.+?)\s*$`)

// taskBulletRE matches `- [ ] **Title**...` or `- [x] **Title**...`. The
// title can be wrapped in single asterisks (italic) or double (bold);
// we tolerate either. Issue links + emoji status markers in the trailer
// are intentionally ignored.
var taskBulletRE = regexp.MustCompile(`^\s*-\s+\[([ xX/-])\]\s+\*\*?(.+?)\*\*?(?:\s+\(.*)?$`)

// tierPriorityRE captures the digit out of a "Tier N: …" header so the
// priority field gets an integer rank rather than a string.
var tierPriorityRE = regexp.MustCompile(`^Tier\s+(\d+)`)

// parseSectionHeader returns (sectionTitle, priorityRank, true) for any H2.
// Non-tier H2s get a deterministic-but-low priority bucket so they don't
// shadow tiered work.
func parseSectionHeader(line string) (string, int, bool) {
	m := sectionHeaderRE.FindStringSubmatch(line)
	if m == nil {
		return "", 0, false
	}
	title := strings.TrimSpace(m[1])
	if pm := tierPriorityRE.FindStringSubmatch(title); pm != nil {
		n, _ := strconv.Atoi(pm[1])
		return title, n, true
	}
	// Non-tier sections that the council still cares about. "Recently
	// Shipped" and "References" are explicitly skipped (high priority
	// number = deprioritised; the reconciler can also filter on theme
	// substring "Tier" if it wants to be strict).
	switch {
	case strings.HasPrefix(title, "Recently Shipped"),
		strings.HasPrefix(title, "References"),
		strings.HasPrefix(title, "Market Context"),
		strings.HasPrefix(title, "Current Status"):
		return title, unknownPriority, true
	case strings.HasPrefix(title, "Immediate Architecture"):
		return title, 1, true
	case strings.HasPrefix(title, "Ongoing Engineering"):
		return title, 9, true
	}
	// Unknown section: keep but at very low priority so its intents
	// don't displace tiered work.
	return title, 8, true
}

// parseTaskBullet returns (title, done, true) when line is a task bullet.
// done is true for `[x]` and `[X]`; false for `[ ]`. `[/]` and `[-]`
// (some markdown renderers use these for "in progress" / "skipped")
// count as not-done so the council still plans against them.
func parseTaskBullet(line string) (string, bool, bool) {
	m := taskBulletRE.FindStringSubmatch(line)
	if m == nil {
		return "", false, false
	}
	state := m[1]
	title := strings.TrimSpace(m[2])
	done := state == "x" || state == "X"
	return title, done, true
}
