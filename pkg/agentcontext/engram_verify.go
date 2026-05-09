package agentcontext

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// VerifyResult captures the outcome of a single engram proof verification.
type VerifyResult struct {
	URI         string `json:"uri"`
	ProofKind   string `json:"proof_kind"` // file_ref | url | command | unknown
	Status      string `json:"status"`     // verified | stale | failing | skipped
	Reason      string `json:"reason,omitempty"`
	LastChecked string `json:"last_checked"` // RFC3339
}

// VerifyOptions controls how an engram's proof is checked.
type VerifyOptions struct {
	// RepoRoot is the directory file-ref proofs are resolved against. When
	// empty, the cwd is used. Tests pass an explicit value so they don't
	// depend on the test runner's cwd.
	RepoRoot string

	// HTTPClient is used for URL proofs. When nil, http.DefaultClient is used
	// with a 5-second timeout.
	HTTPClient *http.Client

	// SkipCommand controls whether `command:` proofs are executed. The MVP
	// verifier never runs commands (devbox sandboxing is out of scope for S3);
	// command proofs always return Status="skipped" with Reason="command
	// verification requires devbox sandbox (S4)".
	SkipCommand bool
}

// HandleEngramVerify verifies an engram (or every engram, when `all=true`)
// and updates `proof_status`, `last_verified`, and `unlocked_in` based on the
// outcome.
//
// Inputs:
//
//	uri:        engram URI to verify (mutually exclusive with all)
//	all:        bool; verify every engram in the workspace
//	repo:       optional repo identifier; appended to `unlocked_in` on success
//	            (defaults to the basename of the cwd)
//	skip_command: bool; default true, since devbox-sandboxed command verification
//	            is deferred to a follow-up.
func (s *Service) HandleEngramVerify(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	uri := v.String("uri", "")
	all := v.Bool("all", false)
	if uri == "" && !all {
		return mcp.ErrorResult(errors.New("either uri or all=true is required")), nil
	}

	repo := v.String("repo", inferRepoName())
	opts := VerifyOptions{
		RepoRoot:    v.String("repo_root", ""),
		SkipCommand: v.Bool("skip_command", true),
	}

	var targets []MemoryItem
	if all {
		res, err := s.memoryHierarchy.Recall(MemoryRecallRequest{
			Categories: []string{EngramCategory},
			Tiers:      []MemoryTier{MemoryTierLongTerm},
			Limit:      1000,
		})
		if err != nil {
			return mcp.ErrorResult(fmt.Errorf("recall: %w", err)), nil
		}
		targets = res.Items
	} else {
		item, err := s.lookupEngramByURI(uri)
		if err != nil {
			return mcp.ErrorResult(err), nil
		}
		if item == nil {
			return mcp.ErrorResult(fmt.Errorf("engram %q not found", uri)), nil
		}
		targets = []MemoryItem{*item}
	}

	results := make([]VerifyResult, 0, len(targets))
	counts := map[string]int{}
	for i := range targets {
		item := &targets[i]
		res := verifyOne(ctx, item, repo, opts)
		results = append(results, res)
		counts[res.Status]++

		if err := s.applyVerifyResult(ctx, item, res, repo); err != nil {
			// Don't crash a bulk run on one persistence failure — surface it
			// in the result and keep going.
			res.Reason = fmt.Sprintf("%s; persistence failed: %v", res.Reason, err)
			results[len(results)-1] = res
		}
	}

	return mcp.JSONResult(map[string]any{
		"ok":      true,
		"count":   len(results),
		"results": results,
		"summary": counts,
	})
}

// verifyOne dispatches to the right proof checker based on the proof string.
func verifyOne(ctx context.Context, item *MemoryItem, repo string, opts VerifyOptions) VerifyResult {
	now := time.Now().UTC().Format(time.RFC3339)
	uri := metadataString(item.Metadata, mdEngramURI)
	proof := metadataString(item.Metadata, mdRecipeProof)

	res := VerifyResult{URI: uri, LastChecked: now}

	switch detectProofKind(proof) {
	case "command":
		res.ProofKind = "command"
		if opts.SkipCommand {
			res.Status = "skipped"
			res.Reason = "command verification requires devbox sandbox (deferred to S4)"
			return res
		}
		// MVP: even when not skipped, we don't actually exec untrusted commands.
		// Mark skipped with a clear reason so callers know why.
		res.Status = "skipped"
		res.Reason = "command verification not yet implemented"
		return res

	case "url":
		res.ProofKind = "url"
		client := opts.HTTPClient
		if client == nil {
			client = &http.Client{Timeout: 5 * time.Second}
		}
		urlStr := extractURL(proof)
		ok, reason := checkURL(ctx, client, urlStr)
		if ok {
			res.Status = "verified"
		} else {
			res.Status = "failing"
			res.Reason = reason
		}
		return res

	case "file_ref":
		res.ProofKind = "file_ref"
		ok, reason := checkFileRef(proof, opts.RepoRoot)
		if ok {
			res.Status = "verified"
		} else {
			res.Status = "stale"
			res.Reason = reason
		}
		return res

	default:
		res.ProofKind = "unknown"
		res.Status = "skipped"
		res.Reason = "could not classify proof; expected file_ref (path:line), url, or 'command:' marker"
		return res
	}
}

// detectProofKind classifies a proof string. Order matters — `command:`
// markers in URL/file proofs are detected first.
func detectProofKind(proof string) string {
	p := strings.TrimSpace(proof)
	if p == "" {
		return ""
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, "command:") {
		return "command"
	}
	if extractURL(p) != "" {
		return "url"
	}
	if fileRefPattern.MatchString(p) {
		return "file_ref"
	}
	// Bare relative path with extension counts as file_ref too.
	if strings.Contains(p, "/") || strings.Contains(p, ".") {
		return "file_ref"
	}
	return ""
}

// fileRefPattern matches `path/to/file.ext` optionally followed by `:N` or
// `:N-M`. The path may be relative or absolute.
var fileRefPattern = regexp.MustCompile(`^[^\s:]+(?::\d+(?:-\d+)?)?$`)

// extractURL pulls the first http(s) URL from `proof`, or returns "" if none.
func extractURL(proof string) string {
	const httpPrefix = "http://"
	const httpsPrefix = "https://"
	for _, prefix := range []string{httpsPrefix, httpPrefix} {
		idx := strings.Index(proof, prefix)
		if idx < 0 {
			continue
		}
		rest := proof[idx:]
		end := strings.IndexAny(rest, " \t\n\r")
		if end < 0 {
			end = len(rest)
		}
		candidate := strings.TrimRight(rest[:end], ".,);]>")
		if _, err := url.Parse(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// checkURL issues a HEAD request and returns (true, "") on 2xx/3xx.
func checkURL(ctx context.Context, client *http.Client, urlStr string) (bool, string) {
	if urlStr == "" {
		return false, "could not extract URL from proof"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, urlStr, nil)
	if err != nil {
		return false, fmt.Sprintf("build request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, ""
	}
	return false, fmt.Sprintf("HEAD %s: %d %s", urlStr, resp.StatusCode, resp.Status)
}

// checkFileRef verifies that the file referenced by `proof` exists under
// `repoRoot` and that any line range it cites is within bounds. Returns
// (true, "") on success.
func checkFileRef(proof, repoRoot string) (bool, string) {
	path, startLine, endLine := parseFileRef(proof)
	if path == "" {
		return false, "empty file path"
	}

	// Resolve relative to repoRoot.
	if !filepath.IsAbs(path) {
		root := repoRoot
		if root == "" {
			cwd, err := os.Getwd()
			if err != nil {
				return false, fmt.Sprintf("getwd: %v", err)
			}
			root = cwd
		}
		path = filepath.Join(root, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Sprintf("stat: %v", err)
	}
	if info.IsDir() {
		return false, "file_ref points at a directory"
	}

	if startLine == 0 {
		return true, ""
	}

	// Validate the line range exists.
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Sprintf("read: %v", err)
	}
	lineCount := strings.Count(string(data), "\n")
	if !strings.HasSuffix(string(data), "\n") && len(data) > 0 {
		lineCount++
	}
	maxLine := endLine
	if maxLine == 0 {
		maxLine = startLine
	}
	if maxLine > lineCount {
		return false, fmt.Sprintf("line range %d-%d exceeds file length %d", startLine, maxLine, lineCount)
	}
	return true, ""
}

// parseFileRef parses `path[:start[-end]]`. Returns ("",0,0) on malformed
// input. The path itself may not contain spaces or colons in the range
// segment; the parser anchors on the *last* colon before the optional range.
func parseFileRef(proof string) (path string, start, end int) {
	proof = strings.TrimSpace(proof)
	if proof == "" {
		return "", 0, 0
	}
	// Strip URL fragments accidentally forwarded.
	if i := strings.IndexByte(proof, ' '); i >= 0 {
		proof = proof[:i]
	}

	// If the trailing segment after the last colon is a line spec, split it
	// off; otherwise treat the whole string as the path.
	idx := strings.LastIndexByte(proof, ':')
	if idx < 0 {
		return proof, 0, 0
	}
	tail := proof[idx+1:]
	if !lineRangePattern.MatchString(tail) {
		return proof, 0, 0
	}

	pathPart := proof[:idx]
	parts := strings.SplitN(tail, "-", 2)
	s, _ := strconv.Atoi(parts[0])
	e := 0
	if len(parts) == 2 {
		e, _ = strconv.Atoi(parts[1])
	}
	return pathPart, s, e
}

var lineRangePattern = regexp.MustCompile(`^\d+(?:-\d+)?$`)

// applyVerifyResult persists the result back to the memory item: updates
// proof_status, last_verified, unlocked_in.
func (s *Service) applyVerifyResult(ctx context.Context, item *MemoryItem, res VerifyResult, repo string) error {
	if item.Metadata == nil {
		item.Metadata = map[string]any{}
	}

	switch res.Status {
	case "verified":
		item.Metadata[mdEngramProofStatus] = ProofStatusVerified
	case "failing":
		item.Metadata[mdEngramProofStatus] = ProofStatusFailing
	case "stale":
		item.Metadata[mdEngramProofStatus] = ProofStatusStale
	case "skipped":
		// Don't downgrade status on skip; just record the timestamp.
	}
	item.Metadata[mdEngramLastVerified] = res.LastChecked

	// Refresh the engram-status:* tag.
	newStatus := metadataString(item.Metadata, mdEngramProofStatus)
	if newStatus == "" {
		newStatus = ProofStatusUnverified
	}
	updated := make([]string, 0, len(item.Tags))
	for _, t := range item.Tags {
		if !strings.HasPrefix(t, "engram-status:") {
			updated = append(updated, t)
		}
	}
	updated = append(updated, "engram-status:"+newStatus)
	item.Tags = updated

	// Append repo to unlocked_in only on successful verification.
	if res.Status == "verified" && repo != "" {
		unlocked := metadataStringSlice(item.Metadata, mdEngramUnlockedIn)
		if !contains(unlocked, repo) {
			unlocked = append(unlocked, repo)
			item.Metadata[mdEngramUnlockedIn] = stringSliceToAny(unlocked)
		}
	}

	if s.persistedMemoryHierarchy == nil {
		// Pure in-memory mutation only.
		return s.memoryHierarchy.UpdateItem(item)
	}
	return s.persistedMemoryHierarchy.UpdateItemWithPersistence(ctx, item, nil)
}

// inferRepoName returns the basename of the cwd, or "" if it cannot be
// determined. Used as the default `repo` value for `unlocked_in`.
func inferRepoName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	// In a worktree under <repo>/.claude/worktrees/<name>/ or <repo>/.worktrees/<branch>/,
	// the canonical repo name is the parent of `.worktrees`. Resolve up to
	// the first `.worktrees` segment.
	for _, marker := range []string{"/.claude/worktrees/", "/.worktrees/"} {
		if idx := strings.Index(cwd, marker); idx > 0 {
			return filepath.Base(cwd[:idx])
		}
	}
	return filepath.Base(cwd)
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
