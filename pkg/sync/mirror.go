// mirror.go — keep platform/gitops/mcp/context/{registry.yaml,skills-registry.yaml}
// in sync with the canonical services/loom-core/mcp/context/* source.
//
// Per AGENTS.md, services/loom-core is the source of truth for MCP server and
// skill registries; platform/gitops mirrors them for GitOps hub/manifest
// rendering and cross-repo visibility. Mirror drift goes unnoticed by default
// because each repo has its own git history, so we provide a CLI gate
// (`loom sync mirror`) and surface it in `loom sync status`.
package sync

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MirrorFiles are the registry files that must stay in lockstep between
// the canonical source and the GitOps mirror.
var MirrorFiles = []string{
	"mcp/context/registry.yaml",
	"mcp/context/skills-registry.yaml",
}

// MirrorTargetRel is the workspace-relative directory that mirrors the
// canonical registries. Discovered relative to Manager.WorkspaceRoot.
const MirrorTargetRel = "platform/gitops"

// MirrorSourceRel is the workspace-relative directory that holds the
// canonical mcp/context source. When `loom sync mirror` runs from a
// worktree or any other directory, we always resolve the source to this
// path so the canonical files (not a worktree snapshot) drive the mirror.
const MirrorSourceRel = "services/loom-core"

// MirrorFileStatus is one file's mirror drift state.
type MirrorFileStatus struct {
	RelPath       string
	SourcePath    string
	MirrorPath    string
	SourceExists  bool
	MirrorExists  bool
	SourceHash    string
	MirrorHash    string
	InSync        bool
	DiffLineCount int // best-effort diff size for reporting
}

// MirrorStatus is the aggregate mirror state.
type MirrorStatus struct {
	SourceRoot string
	MirrorRoot string
	Files      []MirrorFileStatus
	InSync     bool
}

// resolveMirrorRoots returns the canonical source root and mirror root,
// both absolute. Source is always <WorkspaceRoot>/services/loom-core
// (the canonical) — never a worktree snapshot — so running the command
// from a worktree still mirrors against canonical files.
func (m *Manager) resolveMirrorRoots() (sourceRoot, mirrorRoot string, err error) {
	if m.WorkspaceRoot == "" {
		return "", "", fmt.Errorf("workspace root unknown; cannot locate canonical or mirror")
	}
	sourceRoot = filepath.Join(m.WorkspaceRoot, MirrorSourceRel)
	if _, statErr := os.Stat(sourceRoot); os.IsNotExist(statErr) {
		// Fall back to RepoRoot when canonical isn't laid out the standard
		// way (e.g. an isolated test fixture). This preserves the
		// "source = current loom-core" behavior for non-workspace contexts.
		if m.RepoRoot == "" {
			return "", "", fmt.Errorf("canonical source not found at %s and no RepoRoot fallback", sourceRoot)
		}
		sourceRoot = m.RepoRoot
	}
	mirrorRoot = filepath.Join(m.WorkspaceRoot, MirrorTargetRel)
	if _, statErr := os.Stat(mirrorRoot); os.IsNotExist(statErr) {
		return "", "", fmt.Errorf("mirror root not found at %s; clone platform/gitops or set --mirror-root", mirrorRoot)
	}
	return sourceRoot, mirrorRoot, nil
}

// GetMirrorStatus computes per-file mirror drift between canonical
// services/loom-core/mcp/context/* and platform/gitops/mcp/context/*.
func (m *Manager) GetMirrorStatus() (*MirrorStatus, error) {
	sourceRoot, mirrorRoot, err := m.resolveMirrorRoots()
	if err != nil {
		return nil, err
	}

	status := &MirrorStatus{
		SourceRoot: sourceRoot,
		MirrorRoot: mirrorRoot,
		InSync:     true,
	}

	for _, rel := range MirrorFiles {
		src := filepath.Join(sourceRoot, rel)
		dst := filepath.Join(mirrorRoot, rel)

		fs := MirrorFileStatus{
			RelPath:    rel,
			SourcePath: src,
			MirrorPath: dst,
		}
		fs.SourceExists = Exists(src)
		fs.MirrorExists = Exists(dst)

		if fs.SourceExists {
			fs.SourceHash, _ = hashFileSHA(src)
		}
		if fs.MirrorExists {
			fs.MirrorHash, _ = hashFileSHA(dst)
		}

		fs.InSync = fs.SourceExists && fs.MirrorExists && fs.SourceHash == fs.MirrorHash
		if !fs.InSync {
			status.InSync = false
			if fs.SourceExists && fs.MirrorExists {
				fs.DiffLineCount = approxDiffLineCount(src, dst)
			}
		}
		status.Files = append(status.Files, fs)
	}

	return status, nil
}

// SyncMirror copies the canonical registry files into the GitOps mirror.
// dryRun=true reports the changes without writing. Returns the number of
// files that were (or would be) updated.
func (m *Manager) SyncMirror(dryRun bool) (int, *MirrorStatus, error) {
	status, err := m.GetMirrorStatus()
	if err != nil {
		return 0, nil, err
	}

	updated := 0
	for i, fs := range status.Files {
		if fs.InSync {
			continue
		}
		if !fs.SourceExists {
			// Don't propagate missing source onto mirror; that's a destructive op
			// users should opt into explicitly.
			continue
		}
		if dryRun {
			updated++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fs.MirrorPath), 0o755); err != nil {
			return updated, status, fmt.Errorf("mkdir %s: %w", filepath.Dir(fs.MirrorPath), err)
		}
		if err := CopyFile(fs.SourcePath, fs.MirrorPath); err != nil {
			return updated, status, fmt.Errorf("copy %s -> %s: %w", fs.SourcePath, fs.MirrorPath, err)
		}
		updated++
		// Refresh the status entry so the post-sync state is accurate.
		status.Files[i].MirrorExists = true
		status.Files[i].MirrorHash = fs.SourceHash
		status.Files[i].InSync = true
		status.Files[i].DiffLineCount = 0
	}

	status.InSync = true
	for _, fs := range status.Files {
		if !fs.InSync {
			status.InSync = false
			break
		}
	}

	return updated, status, nil
}

// hashFileSHA returns a full SHA256 hex digest of a file (status.hashFile
// truncates to 8 bytes for compact output; mirror comparison wants the
// full digest to avoid spurious collisions).
func hashFileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// approxDiffLineCount returns a coarse "lines changed" estimate by
// counting line-count delta between source and mirror. It's only used for
// human-readable status output; exact diffing belongs in `git diff`.
func approxDiffLineCount(a, b string) int {
	la, _ := countLines(a)
	lb, _ := countLines(b)
	if la > lb {
		return la - lb
	}
	return lb - la
}

func countLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range data {
		if c == '\n' {
			n++
		}
	}
	return n, nil
}
