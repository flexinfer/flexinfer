package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// lintNotesSchema declares the input contract for icc_lint_notes.
func lintNotesSchema() mcp.InputSchema {
	return mcp.InputSchema{
		Type:     "object",
		Required: []string{"workspace_root"},
		Properties: map[string]any{
			"workspace_root": map[string]any{
				"type":        "string",
				"description": "Absolute path to the icc-project-workspaces repo root",
			},
			"project_slug": map[string]any{
				"type":        "string",
				"description": "Limit to one project; omit to lint all",
			},
			"fix": map[string]any{
				"type":        "boolean",
				"description": "Apply safe auto-fixes",
				"default":     false,
			},
		},
	}
}

// lintFinding is a single issue reported by the linter.
type lintFinding struct {
	Path     string `json:"path"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Fixable  bool   `json:"fixable"`
}

// lintResult is the JSON payload returned from the tool.
type lintResult struct {
	Findings     []lintFinding `json:"findings"`
	FilesScanned int           `json:"files_scanned"`
	FilesFixed   int           `json:"files_fixed"`
	FixesApplied []string      `json:"fixes_applied"`
}

// Source folders we walk inside each project directory. Folder names
// are also the canonical `source:` values in frontmatter.
var lintSources = []string{"slack", "email", "meetings", "research", "deliverables", "queries"}

// validClassifications enumerates the allowed values for the
// frontmatter `classification:` field.
var validClassifications = map[string]bool{
	"public":        true,
	"internal":      true,
	"confidential":  true,
	"possible_phi":  true,
	"confirmed_phi": true,
}

// datePrefixRE checks for `YYYY-MM-DD-...` filename prefix used by
// slack/email/meetings/research notes.
var datePrefixRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-.+\.md$`)

// statusPrefixRE checks for the status-prefixed filename used by
// deliverables (e.g. `draft-...`, `in-progress-...`, `final-...`).
var statusPrefixRE = regexp.MustCompile(`^(draft|in-progress|review|final|archived)-.+\.md$`)

func handleLintNotes(_ context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	workspaceRoot := v.Required("workspace_root")
	projectSlug := v.String("project_slug", "")
	doFix := v.Bool("fix", false)
	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	projectsDir := filepath.Join(workspaceRoot, "projects")
	info, err := os.Stat(projectsDir)
	if err != nil || !info.IsDir() {
		return mcp.ErrorResult(fmt.Errorf(
			"projects directory not found under workspace_root: %s",
			projectsDir,
		)), nil
	}

	result := &lintResult{
		Findings:     []lintFinding{},
		FixesApplied: []string{},
	}

	projects, err := listProjectDirs(projectsDir, projectSlug)
	if err != nil {
		return mcp.ErrorResult(err), nil
	}

	for _, projectPath := range projects {
		projectName := filepath.Base(projectPath)
		for _, source := range lintSources {
			sourceDir := filepath.Join(projectPath, source)
			if _, err := os.Stat(sourceDir); err != nil {
				continue
			}
			err := filepath.WalkDir(sourceDir, func(p string, d fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
					return nil
				}
				result.FilesScanned++
				ctx := lintFileContext{
					Path:          p,
					Source:        source,
					ProjectName:   projectName,
					WorkspaceRoot: workspaceRoot,
				}
				lintFile(ctx, result, doFix)
				return nil
			})
			if err != nil {
				return mcp.ErrorResult(err), nil
			}
		}
	}

	return jsonResult(result)
}

// lintFileContext bundles per-file inputs for the linter so the
// signature doesn't sprawl.
type lintFileContext struct {
	Path          string
	Source        string
	ProjectName   string
	WorkspaceRoot string
}

func lintFile(ctx lintFileContext, result *lintResult, doFix bool) {
	raw, err := os.ReadFile(ctx.Path)
	if err != nil {
		result.Findings = append(result.Findings, lintFinding{
			Path:     ctx.Path,
			Rule:     "read_error",
			Severity: "error",
			Message:  err.Error(),
			Fixable:  false,
		})
		return
	}

	fm, body, ok := splitFrontmatter(raw)
	if !ok {
		result.Findings = append(result.Findings, lintFinding{
			Path:     ctx.Path,
			Rule:     "frontmatter_present",
			Severity: "error",
			Message:  "file is missing YAML frontmatter (--- delimited block at top)",
			Fixable:  false,
		})
		return
	}

	parsed := parseFrontmatter(fm)

	// rule: frontmatter_required_fields
	required := []string{"project", "source", "classification", "captured_at"}
	missing := []string{}
	for _, f := range required {
		if strings.TrimSpace(parsed[f]) == "" {
			missing = append(missing, f)
		}
	}
	// Fixable subset: classification / captured_at / source.
	fixedAny := false
	if doFix {
		mtime := fileMTime(ctx.Path)
		if parsed["classification"] == "" {
			parsed["classification"] = "possible_phi"
			fixedAny = true
		}
		if parsed["captured_at"] == "" && !mtime.IsZero() {
			parsed["captured_at"] = mtime.Format(time.RFC3339)
			fixedAny = true
		}
		if parsed["source"] == "" {
			parsed["source"] = ctx.Source
			fixedAny = true
		}
		if fixedAny {
			if err := writeFrontmatter(ctx.Path, parsed, body); err == nil {
				result.FilesFixed++
				result.FixesApplied = append(result.FixesApplied, ctx.Path)
				// Recompute missing after fixes.
				missing = missing[:0]
				for _, f := range required {
					if strings.TrimSpace(parsed[f]) == "" {
						missing = append(missing, f)
					}
				}
			}
		}
	}
	for _, f := range missing {
		// `project` is required but never auto-fixed (human decision).
		fixable := f != "project"
		result.Findings = append(result.Findings, lintFinding{
			Path:     ctx.Path,
			Rule:     "frontmatter_required_fields",
			Severity: "error",
			Message:  fmt.Sprintf("missing required field: %s", f),
			Fixable:  fixable,
		})
	}

	// rule: source_matches_folder
	if src := strings.TrimSpace(parsed["source"]); src != "" && src != ctx.Source {
		result.Findings = append(result.Findings, lintFinding{
			Path:     ctx.Path,
			Rule:     "source_matches_folder",
			Severity: "error",
			Message:  fmt.Sprintf("frontmatter source=%q but file lives in %q folder", src, ctx.Source),
			Fixable:  true,
		})
	}

	// rule: classification_valid
	if c := strings.TrimSpace(parsed["classification"]); c != "" && !validClassifications[c] {
		result.Findings = append(result.Findings, lintFinding{
			Path:     ctx.Path,
			Rule:     "classification_valid",
			Severity: "error",
			Message:  fmt.Sprintf("classification %q is not one of public|internal|confidential|possible_phi|confirmed_phi", c),
			Fixable:  false,
		})
	}

	// rule: naming_pattern
	if !checkNaming(ctx.Source, filepath.Base(ctx.Path)) {
		result.Findings = append(result.Findings, lintFinding{
			Path:     ctx.Path,
			Rule:     "naming_pattern",
			Severity: "warning",
			Message:  namingPatternMessage(ctx.Source),
			Fixable:  false,
		})
	}

	// rule: project_exists_in_map
	if proj := strings.TrimSpace(parsed["project"]); proj != "" && proj != "_inbox" {
		projectDir := filepath.Join(ctx.WorkspaceRoot, "projects", proj)
		if info, err := os.Stat(projectDir); err != nil || !info.IsDir() {
			result.Findings = append(result.Findings, lintFinding{
				Path:     ctx.Path,
				Rule:     "project_exists_in_map",
				Severity: "warning",
				Message:  fmt.Sprintf("project %q has no matching folder under projects/", proj),
				Fixable:  false,
			})
		}
	}
}

// listProjectDirs returns project directory paths to lint, filtered to
// `slug` when non-empty.
func listProjectDirs(projectsDir, slug string) ([]string, error) {
	if slug != "" {
		p := filepath.Join(projectsDir, slug)
		info, err := os.Stat(p)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("project not found: %s", slug)
		}
		return []string{p}, nil
	}
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(projectsDir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

var frontmatterDelimRE = regexp.MustCompile(`(?m)^---\s*$`)

// splitFrontmatter peels off a leading `---\n...\n---\n` block.
// Returns (frontmatter, body, ok). When no frontmatter is present
// it returns ("", entire-file, false).
func splitFrontmatter(raw []byte) (string, string, bool) {
	s := string(raw)
	if !strings.HasPrefix(strings.TrimLeft(s, " \t\r\n"), "---") {
		return "", s, false
	}
	// Walk to find the first ---, then the next ---.
	loc := frontmatterDelimRE.FindAllStringIndex(s, 2)
	if len(loc) < 2 {
		return "", s, false
	}
	fmStart := loc[0][1] // after the first ---
	fmEnd := loc[1][0]   // before the second ---
	body := s[loc[1][1]:]
	body = strings.TrimLeft(body, "\n")
	return strings.Trim(s[fmStart:fmEnd], "\n"), body, true
}

// parseFrontmatter is a minimal `key: value` parser. It is NOT a full
// YAML parser — it handles flat scalar fields, which is all the spec
// requires. Arrays (e.g. participants) are stored as their raw value
// string so lint can ignore them but writeFrontmatter can round-trip.
func parseFrontmatter(fm string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(fm, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		idx := strings.Index(trim, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(trim[:idx])
		val := strings.TrimSpace(trim[idx+1:])
		val = strings.Trim(val, `"'`)
		out[key] = val
	}
	return out
}

// writeFrontmatter rewrites a file with an updated frontmatter map.
// The original key order is not preserved (we use a canonical order
// instead) which is acceptable for auto-fixes.
func writeFrontmatter(path string, fm map[string]string, body string) error {
	preferred := []string{"project", "source", "classification", "captured_at"}
	written := map[string]bool{}
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range preferred {
		if v := fm[k]; v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
			written[k] = true
		}
	}
	// Keep any extra keys in a deterministic order.
	extras := make([]string, 0, len(fm))
	for k := range fm {
		if !written[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		fmt.Fprintf(&b, "%s: %s\n", k, fm[k])
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func fileMTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// checkNaming returns true when filename matches the convention for
// the given source folder. Queries are exempt (always returns true).
func checkNaming(source, filename string) bool {
	switch source {
	case "queries":
		return true
	case "deliverables":
		return statusPrefixRE.MatchString(filename)
	default:
		return datePrefixRE.MatchString(filename)
	}
}

func namingPatternMessage(source string) string {
	switch source {
	case "deliverables":
		return "deliverables filename must start with one of: draft-, in-progress-, review-, final-, archived-"
	default:
		return "filename should start with YYYY-MM-DD-..."
	}
}
