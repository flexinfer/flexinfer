package agentcontext

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type SourceVersionSvc struct{ *Service }

// SourceVersionProvider provides source version information
type SourceVersionProvider struct {
	workingDir string
}

// NewSourceVersionProvider creates a new source version provider
func NewSourceVersionProvider(workingDir string) *SourceVersionProvider {
	return &SourceVersionProvider{workingDir: workingDir}
}

// GetSourceVersion gets version information for a file
func (svp *SourceVersionProvider) GetSourceVersion(filePath string) (*SourceVersion, error) {
	sv := &SourceVersion{
		IndexedAt: time.Now().UTC(),
	}

	// Get file modification time
	info, err := os.Stat(filePath)
	if err == nil {
		sv.FileMtime = info.ModTime().UTC()
	}

	// Try to get git commit hash
	if svp.workingDir != "" {
		commitHash, err := svp.getGitCommitHash(filePath)
		if err == nil && commitHash != "" {
			sv.CommitHash = commitHash
		}
	}

	return sv, nil
}

// getGitCommitHash gets the last commit hash that modified a file
func (svp *SourceVersionProvider) getGitCommitHash(filePath string) (string, error) {
	cmd := exec.Command("git", "log", "-1", "--format=%H", "--", filePath) //nolint:noctx // quick git command
	cmd.Dir = svp.workingDir
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// CheckStale checks if a source entry is stale based on file changes
func (svp *SourceVersionProvider) CheckStale(entry *ContextEntry) (bool, *SourceVersion, error) {
	if entry.FilePath == "" {
		return false, nil, fmt.Errorf("entry has no file path")
	}
	if entry.SourceVersion == nil {
		return true, nil, nil // No version info means potentially stale
	}

	// Get current file info
	currentVersion, err := svp.GetSourceVersion(entry.FilePath)
	if err != nil {
		return false, nil, err
	}

	// Check if file has been modified since indexing
	if !currentVersion.FileMtime.IsZero() && !entry.SourceVersion.FileMtime.IsZero() {
		if currentVersion.FileMtime.After(entry.SourceVersion.FileMtime) {
			currentVersion.IsStale = true
			return true, currentVersion, nil
		}
	}

	// Check if git commit has changed
	if currentVersion.CommitHash != "" && entry.SourceVersion.CommitHash != "" {
		if currentVersion.CommitHash != entry.SourceVersion.CommitHash {
			currentVersion.IsStale = true
			return true, currentVersion, nil
		}
	}

	return false, currentVersion, nil
}

// StalenessReport contains information about stale entries
type StalenessReport struct {
	EntryID       string        `json:"entry_id"`
	FilePath      string        `json:"file_path"`
	IsStale       bool          `json:"is_stale"`
	IndexedAt     time.Time     `json:"indexed_at"`
	CurrentMtime  time.Time     `json:"current_mtime,omitempty"`
	IndexedMtime  time.Time     `json:"indexed_mtime,omitempty"`
	CurrentCommit string        `json:"current_commit,omitempty"`
	IndexedCommit string        `json:"indexed_commit,omitempty"`
	StaleDuration time.Duration `json:"stale_duration,omitempty"`
}

// GenerateStalenessReport generates a report for an entry
func (svp *SourceVersionProvider) GenerateStalenessReport(entry *ContextEntry) (*StalenessReport, error) {
	report := &StalenessReport{
		EntryID:  entry.ID,
		FilePath: entry.FilePath,
	}

	if entry.SourceVersion != nil {
		report.IndexedAt = entry.SourceVersion.IndexedAt
		report.IndexedMtime = entry.SourceVersion.FileMtime
		report.IndexedCommit = entry.SourceVersion.CommitHash
	}

	isStale, currentVersion, err := svp.CheckStale(entry)
	if err != nil {
		return nil, err
	}

	report.IsStale = isStale
	if currentVersion != nil {
		report.CurrentMtime = currentVersion.FileMtime
		report.CurrentCommit = currentVersion.CommitHash

		if isStale && !report.IndexedMtime.IsZero() && !report.CurrentMtime.IsZero() {
			report.StaleDuration = report.CurrentMtime.Sub(report.IndexedMtime)
		}
	}

	return report, nil
}

// Service methods for source versioning

// HandleCheckStale handles the agent_context_check_stale tool
func (s *SourceVersionSvc) HandleCheckStale(ctx context.Context, args map[string]any) (map[string]any, error) {
	entryIDs := toStringSlice(args["entry_ids"])
	filePath := toString(args["file_path"])
	sessionID := toString(args["session_id"])

	var entries []ContextEntry
	var err error

	if len(entryIDs) > 0 {
		// Check specific entries by ID
		for _, id := range entryIDs {
			p, err := s.qdrant.Get(CollContext).GetPoint(ctx, id, false)
			if err != nil || p.Payload == nil {
				continue
			}
			entry, err := PayloadToEntry(p.Payload)
			if err != nil || entry == nil {
				continue
			}
			entries = append(entries, *entry)
		}
	} else if filePath != "" {
		// Find entries by file path
		filter := FilterMust(Match("file_path", filePath))
		entries, err = s.qdrant.Get(CollContext).Scroll(ctx, filter, 100)
		if err != nil {
			return nil, fmt.Errorf("find entries: %w", err)
		}
	} else if sessionID != "" {
		// Find file_read entries in session
		filter := FilterMust(
			Match("session_id", sessionID),
			Match("entry_type", string(EntryTypeFileRead)),
		)
		entries, err = s.qdrant.Get(CollContext).Scroll(ctx, filter, 100)
		if err != nil {
			return nil, fmt.Errorf("find entries: %w", err)
		}
	} else {
		return nil, fmt.Errorf("entry_ids, file_path, or session_id is required")
	}

	// Get working directory from first entry or use default
	workingDir := ""
	if len(entries) > 0 {
		session, _ := s.getSession(ctx, entries[0].SessionID)
		if session != nil && session.WorkingDir != "" {
			workingDir = session.WorkingDir
		}
	}

	provider := NewSourceVersionProvider(workingDir)

	var reports []StalenessReport
	staleCount := 0

	for _, entry := range entries {
		if entry.FilePath == "" {
			continue // Skip entries without file paths
		}

		report, err := provider.GenerateStalenessReport(&entry)
		if err != nil {
			// Include error in report
			reports = append(reports, StalenessReport{
				EntryID:  entry.ID,
				FilePath: entry.FilePath,
				IsStale:  true, // Assume stale if we can't check
			})
			continue
		}

		reports = append(reports, *report)
		if report.IsStale {
			staleCount++
		}
	}

	return map[string]any{
		"ok":          true,
		"total":       len(reports),
		"stale_count": staleCount,
		"fresh_count": len(reports) - staleCount,
		"reports":     reports,
	}, nil
}

// HandleRefreshStale handles refreshing stale entries
func (s *SourceVersionSvc) HandleRefreshStale(ctx context.Context, args map[string]any) (map[string]any, error) {
	entryIDs := toStringSlice(args["entry_ids"])
	if len(entryIDs) == 0 {
		return nil, fmt.Errorf("entry_ids is required")
	}

	confirm, _ := args["confirm"].(bool)
	if !confirm {
		return nil, fmt.Errorf("confirm=true required to refresh entries")
	}

	refreshed := 0
	failed := 0
	var errors []string

	for _, id := range entryIDs {
		// Get the entry
		p, err := s.qdrant.Get(CollContext).GetPoint(ctx, id, true)
		if err != nil || p.Payload == nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: not found", id))
			continue
		}

		entry, err := PayloadToEntry(p.Payload)
		if err != nil || entry == nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: invalid payload", id))
			continue
		}

		if entry.FilePath == "" {
			failed++
			errors = append(errors, fmt.Sprintf("%s: no file path", id))
			continue
		}

		// Read current file content
		content, err := os.ReadFile(entry.FilePath)
		if err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}

		// Get current source version
		session, _ := s.getSession(ctx, entry.SessionID)
		workingDir := ""
		if session != nil {
			workingDir = session.WorkingDir
		}
		provider := NewSourceVersionProvider(workingDir)
		sourceVersion, _ := provider.GetSourceVersion(entry.FilePath)

		// Update entry
		entry.Content = string(content)
		entry.ContentHash = ContentHashFunc(entry.Content)
		entry.TokenCount = EstimateTokens(entry.Content)
		entry.SourceVersion = sourceVersion
		entry.Timestamp = time.Now()

		// Re-embed and update
		vectors, err := s.embed.EmbedDocuments(ctx, []string{entry.Title + "\n" + entry.Content})
		if err != nil || len(vectors) == 0 {
			failed++
			errors = append(errors, fmt.Sprintf("%s: embedding failed", id))
			continue
		}

		point := Point{
			ID:      entry.ID,
			Vector:  vectors[0],
			Payload: EntryToPayload(*entry, s.cfg.EmbedModel),
		}

		if err := s.qdrant.Get(CollContext).Upsert(ctx, []Point{point}, true); err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("%s: upsert failed", id))
			continue
		}

		refreshed++
	}

	return map[string]any{
		"ok":        true,
		"refreshed": refreshed,
		"failed":    failed,
		"errors":    errors,
	}, nil
}

// =========================================================================
// Ask the Source Tool (Phase 2.2 - btca-inspired)
// =========================================================================

// AskSourceResult represents the result of an "ask the source" query
type AskSourceResult struct {
	Query        string           `json:"query"`
	Results      []AskSourceEntry `json:"results"`
	TotalTokens  int              `json:"total_tokens"`
	StaleCount   int              `json:"stale_count"`
	RefreshedIDs []string         `json:"refreshed_ids,omitempty"`
	SourcesUsed  []string         `json:"sources_used"`
}

// AskSourceEntry represents a single result entry
type AskSourceEntry struct {
	EntryID    string     `json:"entry_id"`
	EntryType  string     `json:"entry_type"`
	Title      string     `json:"title"`
	Content    string     `json:"content"`
	FilePath   string     `json:"file_path,omitempty"`
	Score      float64    `json:"score"`
	Freshness  string     `json:"freshness"` // "fresh", "stale", "unknown"
	IndexedAt  *time.Time `json:"indexed_at,omitempty"`
	TokenCount int        `json:"token_count"`
}

// HandleAskSource handles the agent_context_ask_source tool
// This combines context recall with live codebase search and freshness checking
func (s *SourceVersionSvc) HandleAskSource(ctx context.Context, args map[string]any) (map[string]any, error) {
	query := toString(args["query"])
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	sessionID := toString(args["session_id"])
	agentID := toString(args["agent_id"])
	namespace := toString(args["namespace"])
	fileContext := toString(args["file_context"])
	tokenBudget := toInt(args["token_budget"])
	if tokenBudget <= 0 {
		tokenBudget = s.cfg.DefaultTokenBudget
	}
	autoRefresh := true
	if v, ok := args["auto_refresh"].(bool); ok {
		autoRefresh = v
	}
	limit := toInt(args["limit"])
	if limit <= 0 {
		limit = 10
	}

	// Phase 1: Recall from context
	recallOpts := RecallOptions{
		Query:            query,
		AgentID:          agentID,
		SessionID:        sessionID,
		Namespace:        namespace,
		TokenBudget:      tokenBudget,
		IncludeSummaries: true,
		IncludeDecisions: true,
		FileContext:      fileContext,
	}

	contextEntries, err := s.ctxSvc.recallContext(ctx, recallOpts)
	if err != nil {
		return nil, fmt.Errorf("recall context: %w", err)
	}

	// Phase 2: Check freshness and optionally refresh stale entries
	session, _ := s.getSession(ctx, sessionID)
	workingDir := ""
	if session != nil {
		workingDir = session.WorkingDir
	}
	provider := NewSourceVersionProvider(workingDir)

	var results []AskSourceEntry
	var sourcesUsed []string
	staleCount := 0
	var refreshedIDs []string
	totalTokens := 0

	sourceSet := make(map[string]bool)

	for _, entry := range contextEntries {
		if totalTokens >= tokenBudget {
			break
		}

		// Determine freshness
		freshness := "unknown"
		var indexedAt *time.Time

		if entry.SourceVersion != nil {
			indexedAt = &entry.SourceVersion.IndexedAt

			if entry.FilePath != "" {
				isStale, _, err := provider.CheckStale(&entry)
				if err == nil {
					if isStale {
						freshness = "stale"
						staleCount++

						// Auto-refresh if enabled
						if autoRefresh {
							refreshResult, err := s.HandleRefreshStale(ctx, map[string]any{
								"entry_ids": []string{entry.ID},
								"confirm":   true,
							})
							if err == nil && toInt(refreshResult["refreshed"]) > 0 {
								refreshedIDs = append(refreshedIDs, entry.ID)
								freshness = "refreshed"

								// Re-fetch the updated entry
								p, _ := s.qdrant.Get(CollContext).GetPoint(ctx, entry.ID, false)
								if p.Payload != nil {
									if updated, _ := PayloadToEntry(p.Payload); updated != nil {
										entry = *updated
									}
								}
							}
						}
					} else {
						freshness = "fresh"
					}
				}
			}
		} else if entry.FilePath == "" {
			freshness = "n/a" // Non-file entries don't have freshness
		}

		result := AskSourceEntry{
			EntryID:    entry.ID,
			EntryType:  string(entry.EntryType),
			Title:      entry.Title,
			Content:    entry.Content,
			FilePath:   entry.FilePath,
			Freshness:  freshness,
			IndexedAt:  indexedAt,
			TokenCount: entry.TokenCount,
		}

		results = append(results, result)
		totalTokens += entry.TokenCount

		// Track sources
		if entry.FilePath != "" && !sourceSet[entry.FilePath] {
			sourceSet[entry.FilePath] = true
			sourcesUsed = append(sourcesUsed, entry.FilePath)
		}

		if len(results) >= limit {
			break
		}
	}

	return map[string]any{
		"ok": true,
		"result": AskSourceResult{
			Query:        query,
			Results:      results,
			TotalTokens:  totalTokens,
			StaleCount:   staleCount,
			RefreshedIDs: refreshedIDs,
			SourcesUsed:  sourcesUsed,
		},
	}, nil
}
