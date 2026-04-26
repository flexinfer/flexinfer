package council

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/crb2nu/loom/pkg/hive/store"
)

// ArtifactWriter materialises the editor's output into committed files
// under .loom/. The writer is the only piece of the council that touches
// the filesystem at write time; everything upstream (brief, reviewers,
// editor) is pure-function-shaped so the dryrun path can short-circuit
// here.
//
// The writer does NOT commit + push; that's the operator's job in
// slice 3.7 (it knows the council branch + git author identity).
// Returning a populated CouncilRun + ArtifactRefs is the writer's
// contract — the caller persists into council_runs and runs git
// separately.
type ArtifactWriter struct {
	// RepoRoot is the absolute path to the loom-core checkout the
	// writer should publish into. The .loom/ directory under it must
	// exist (the writer doesn't create it; if .loom/ is missing the
	// council shouldn't be writing there in the first place).
	RepoRoot string

	// Now is injectable for deterministic tests + reproducible filename
	// indices. Defaults to time.Now.
	Now func() time.Time
}

// WriteResult is the audit footprint of one Write call.
type WriteResult struct {
	Run           *store.CouncilRun
	ArtifactRefs  []store.ArtifactRef
	SidecarPath   string
	WrittenBytes  int
	NextFreeIndex int // the .loom/<NN>- index the writer used as its base
}

// Write commits the editor's documents + sidecar to .loom/. The runID
// argument seeds CouncilRun.ID; passing the same id twice produces
// idempotent output (overwrites the same files). Filenames are
// .loom/<NN>-<kind>-<runID>.md for the markdown documents and
// .loom/<NN>-<runID>-sidecar.json for the JSON sidecar — the runID
// keeps two same-day runs from colliding.
func (w *ArtifactWriter) Write(ctx context.Context, runID string, out *EditorOutput) (*WriteResult, error) {
	if w == nil || w.RepoRoot == "" {
		return nil, errors.New("council: ArtifactWriter not configured")
	}
	if out == nil || len(out.Documents) == 0 {
		return nil, errors.New("council: editor output requires ≥ 1 document")
	}
	if runID == "" {
		return nil, errors.New("council: runID required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loomDir := filepath.Join(w.RepoRoot, ".loom")
	info, err := os.Stat(loomDir)
	if err != nil {
		return nil, fmt.Errorf("council: .loom dir: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("council: %s is not a directory", loomDir)
	}

	now := time.Now().UTC()
	if w.Now != nil {
		now = w.Now().UTC()
	}

	nextIdx, err := nextFreeIndex(loomDir)
	if err != nil {
		return nil, err
	}

	res := &WriteResult{
		NextFreeIndex: nextIdx,
		ArtifactRefs:  make([]store.ArtifactRef, 0, len(out.Documents)+1),
	}
	idx := nextIdx
	for _, doc := range out.Documents {
		filename := fmt.Sprintf("%02d-%s-%s.md", idx, doc.Kind.FilenameFragment(), runID)
		full := filepath.Join(loomDir, filename)
		body := renderDocumentMarkdown(doc, runID, now)
		if err := writeFileAtomicCouncil(full, []byte(body), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", filename, err)
		}
		res.WrittenBytes += len(body)
		res.ArtifactRefs = append(res.ArtifactRefs, store.ArtifactRef{
			Kind: string(doc.Kind),
			Path: filepath.Join(".loom", filename),
		})
		idx++
	}

	// Stamp + persist sidecar last so a partial write doesn't leave a
	// sidecar that points at missing files.
	out.Sidecar.CouncilRunID = runID
	if out.Sidecar.StartedAt.IsZero() {
		out.Sidecar.StartedAt = now
	}
	end := now
	out.Sidecar.EndedAt = &end
	out.Sidecar.Artifacts = append([]store.ArtifactRef(nil), res.ArtifactRefs...)

	sidecarBytes, err := out.Sidecar.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal sidecar: %w", err)
	}
	sidecarFilename := fmt.Sprintf("%02d-%s-sidecar.json", nextIdx, runID)
	sidecarPath := filepath.Join(loomDir, sidecarFilename)
	if err := writeFileAtomicCouncil(sidecarPath, sidecarBytes, 0o644); err != nil {
		return nil, fmt.Errorf("write sidecar: %w", err)
	}
	res.WrittenBytes += len(sidecarBytes)
	res.SidecarPath = filepath.Join(".loom", sidecarFilename)

	res.Run = &store.CouncilRun{
		ID:              runID,
		Trigger:         store.CouncilTriggerManual, // caller overrides if cron/incident
		StartedAt:       out.Sidecar.StartedAt,
		EndedAt:         out.Sidecar.EndedAt,
		Outcome:         store.CouncilOutcomeSuccess,
		CostFrontierUSD: out.Sidecar.CostUSD.Frontier,
		CostLocalUSD:    out.Sidecar.CostUSD.Local,
		Artifacts:       res.ArtifactRefs,
		BacklogDeltas:   store.BacklogDeltas{}, // mutator (slice 3.6) populates ids
		Sidecar:         map[string]any{"path": res.SidecarPath, "bytes": len(sidecarBytes)},
		Notes:           out.Sidecar.Notes,
	}
	return res, nil
}

// renderDocumentMarkdown stamps an H1 + a deterministic header onto each
// editor body so commits look like the existing hand-curated .loom/ docs.
func renderDocumentMarkdown(doc ArtifactDoc, runID string, now time.Time) string {
	var b strings.Builder
	if doc.Title != "" {
		fmt.Fprintf(&b, "# %s\n\n", doc.Title)
	} else {
		fmt.Fprintf(&b, "# %s — %s\n\n", string(doc.Kind), runID)
	}
	fmt.Fprintf(&b, "> Generated by Loom Hive council run `%s` at `%s`.\n\n",
		runID, now.Format(time.RFC3339))
	b.WriteString(strings.TrimSpace(doc.Body))
	b.WriteString("\n")
	return b.String()
}

// indexedFilenameRE picks the leading two-digit index off existing
// .loom/ filenames so nextFreeIndex picks one above the highest seen.
var indexedFilenameRE = regexp.MustCompile(`^(\d{2,3})-`)

// nextFreeIndex scans .loom/ for files matching NN-*.md / NN-*.json
// and returns the highest seen + 1, defaulting to 90 if the directory
// is empty (matches the existing manual numbering convention which is
// already in the high 80s).
func nextFreeIndex(loomDir string) (int, error) {
	entries, err := os.ReadDir(loomDir)
	if err != nil {
		return 0, fmt.Errorf("read .loom: %w", err)
	}
	highest := 89 // start at 90 so a fresh repo doesn't collide with manual docs
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := indexedFilenameRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	return highest + 1, nil
}

// SortedArtifacts returns the writer's ArtifactRefs in a deterministic
// order (by Path). Useful for tests that snapshot the output.
func SortedArtifacts(refs []store.ArtifactRef) []store.ArtifactRef {
	out := append([]store.ArtifactRef(nil), refs...)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// writeFileAtomicCouncil is the same tempfile+rename pattern pkg/skills
// uses (see pkg/skills/fileops.go). Local copy avoids a cross-package
// dependency for a 20-line primitive; behaviour is identical.
func writeFileAtomicCouncil(path string, data []byte, mode os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".council-*.tmp")
	if err != nil {
		return fmt.Errorf("tempfile: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename tmp: %w", err)
	}
	return nil
}
