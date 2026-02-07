package agentcontext

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// =========================================================================
// Codebase-Memory Deep Integration (Phase 3.2)
// Auto-link context to codebase-memory symbols
// =========================================================================

// CodebaseLink represents a link between context and codebase symbols
type CodebaseLink struct {
	// Context entry info
	ContextID string `json:"context_id"`
	SessionID string `json:"session_id,omitempty"`
	FilePath  string `json:"file_path"`

	// Codebase symbol info
	SymbolID   string `json:"symbol_id"`
	SymbolName string `json:"symbol_name"`
	SymbolType string `json:"symbol_type"` // "function", "class", "variable", etc.
	SymbolPath string `json:"symbol_path"` // Full qualified path

	// Link metadata
	LinkType    string    `json:"link_type"` // "file", "definition", "reference", "import"
	Confidence  float64   `json:"confidence"`
	CreatedAt   time.Time `json:"created_at"`
	ValidatedAt time.Time `json:"validated_at,omitempty"`
	IsValid     bool      `json:"is_valid"`
}

// CodebaseWatcher watches for codebase index updates
type CodebaseWatcher struct {
	// Callbacks
	OnFileChange   func(filePath string)
	OnSymbolChange func(symbolID string)
	OnIndexUpdate  func(repoPath string)

	// State
	Running      bool
	StopCh       chan struct{}
	PollInterval time.Duration

	// Last known states for change detection
	LastIndexTime map[string]time.Time // repo -> last index time
}

// CodebaseSyncConfig configures codebase synchronization
type CodebaseSyncConfig struct {
	// Enable automatic synchronization
	Enabled bool `json:"enabled"`

	// Poll interval for index updates
	PollInterval time.Duration `json:"poll_interval"`

	// Auto-link context entries to codebase symbols
	AutoLink bool `json:"auto_link"`

	// Auto-invalidate context when files change
	AutoInvalidate bool `json:"auto_invalidate"`

	// Repositories to watch
	WatchedRepos []string `json:"watched_repos"`
}

// DefaultCodebaseSyncConfig returns sensible defaults
func DefaultCodebaseSyncConfig() CodebaseSyncConfig {
	return CodebaseSyncConfig{
		Enabled:        true,
		PollInterval:   30 * time.Second,
		AutoLink:       true,
		AutoInvalidate: true,
		WatchedRepos:   []string{},
	}
}

// CodebaseSynchronizer manages synchronization between context and codebase
type CodebaseSynchronizer struct {
	mu sync.RWMutex

	config CodebaseSyncConfig
	logger *slog.Logger

	// Links storage
	links          map[string]*CodebaseLink // linkID -> link
	linksByContext map[string][]string      // contextID -> []linkID
	linksBySymbol  map[string][]string      // symbolID -> []linkID
	linksByFile    map[string][]string      // filePath -> []linkID

	// Codebase memory client interface
	codebaseClient CodebaseMemoryClient

	// Context service for invalidation
	contextService ContextInvalidator

	// State
	running bool
	stopCh  chan struct{}
}

// CodebaseMemoryClient interface for codebase-memory operations
type CodebaseMemoryClient interface {
	// Search for symbols
	Search(ctx context.Context, query string, limit int) ([]CodebaseSymbol, error)

	// Get symbol by ID
	GetSymbol(ctx context.Context, symbolID string) (*CodebaseSymbol, error)

	// Get definition for a symbol
	GetDefinition(ctx context.Context, symbolID string) (*CodebaseDefinition, error)

	// Get references to a symbol
	GetReferences(ctx context.Context, symbolID string) ([]CodebaseReference, error)

	// Get symbols in a file
	GetFileSymbols(ctx context.Context, filePath string) ([]CodebaseSymbol, error)

	// Get index status
	GetIndexStatus(ctx context.Context, repoPath string) (*CodebaseIndexStatus, error)

	// Watch for changes
	WatchChanges(ctx context.Context, repoPath string) (<-chan CodebaseChange, error)
}

// CodebaseSymbol represents a symbol from codebase-memory
type CodebaseSymbol struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Type       string         `json:"type"`
	FilePath   string         `json:"file_path"`
	Line       int            `json:"line"`
	Column     int            `json:"column"`
	Language   string         `json:"language"`
	Namespace  string         `json:"namespace,omitempty"`
	ParentID   string         `json:"parent_id,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

// CodebaseDefinition represents a symbol definition
type CodebaseDefinition struct {
	Symbol    CodebaseSymbol `json:"symbol"`
	Content   string         `json:"content"`
	StartLine int            `json:"start_line"`
	EndLine   int            `json:"end_line"`
}

// CodebaseReference represents a reference to a symbol
type CodebaseReference struct {
	SymbolID string `json:"symbol_id"`
	FilePath string `json:"file_path"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Context  string `json:"context,omitempty"`
}

// CodebaseIndexStatus represents the index status
type CodebaseIndexStatus struct {
	RepoPath      string    `json:"repo_path"`
	LastIndexTime time.Time `json:"last_index_time"`
	FilesIndexed  int       `json:"files_indexed"`
	SymbolsCount  int       `json:"symbols_count"`
	IsComplete    bool      `json:"is_complete"`
}

// CodebaseChange represents a change event
type CodebaseChange struct {
	Type      string    `json:"type"` // "file_modified", "file_deleted", "symbol_changed", "index_complete"
	FilePath  string    `json:"file_path,omitempty"`
	SymbolID  string    `json:"symbol_id,omitempty"`
	RepoPath  string    `json:"repo_path,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// ContextInvalidator interface for invalidating context entries
type ContextInvalidator interface {
	InvalidateByFile(ctx context.Context, filePath string) (int, error)
	InvalidateBySymbol(ctx context.Context, symbolID string) (int, error)
	MarkStale(ctx context.Context, entryIDs []string) error
}

// NewCodebaseSynchronizer creates a new codebase synchronizer
func NewCodebaseSynchronizer(
	config CodebaseSyncConfig,
	codebaseClient CodebaseMemoryClient,
	contextService ContextInvalidator,
) *CodebaseSynchronizer {
	return &CodebaseSynchronizer{
		config:         config,
		logger:         slog.Default(),
		codebaseClient: codebaseClient,
		contextService: contextService,
		links:          make(map[string]*CodebaseLink),
		linksByContext: make(map[string][]string),
		linksBySymbol:  make(map[string][]string),
		linksByFile:    make(map[string][]string),
		stopCh:         make(chan struct{}),
	}
}

// Start begins the synchronization process
func (cs *CodebaseSynchronizer) Start(ctx context.Context) error {
	cs.mu.Lock()
	if cs.running {
		cs.mu.Unlock()
		return nil
	}
	cs.running = true
	cs.stopCh = make(chan struct{})
	cs.mu.Unlock()

	// Start watching each repository
	for _, repo := range cs.config.WatchedRepos {
		go cs.watchRepository(ctx, repo)
	}

	return nil
}

// Stop stops the synchronization process
func (cs *CodebaseSynchronizer) Stop() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.running {
		return
	}

	close(cs.stopCh)
	cs.running = false
}

// watchRepository watches a single repository for changes
func (cs *CodebaseSynchronizer) watchRepository(ctx context.Context, repoPath string) {
	if cs.codebaseClient == nil {
		return
	}

	changeCh, err := cs.codebaseClient.WatchChanges(ctx, repoPath)
	if err != nil {
		// Fall back to polling
		cs.pollRepository(ctx, repoPath)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-cs.stopCh:
			return
		case change, ok := <-changeCh:
			if !ok {
				return
			}
			cs.handleChange(ctx, change)
		}
	}
}

// pollRepository polls a repository for changes
func (cs *CodebaseSynchronizer) pollRepository(ctx context.Context, repoPath string) {
	ticker := time.NewTicker(cs.config.PollInterval)
	defer ticker.Stop()

	var lastIndexTime time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-cs.stopCh:
			return
		case <-ticker.C:
			if cs.codebaseClient == nil {
				continue
			}

			status, err := cs.codebaseClient.GetIndexStatus(ctx, repoPath)
			if err != nil {
				continue
			}

			if status.LastIndexTime.After(lastIndexTime) {
				lastIndexTime = status.LastIndexTime
				cs.handleChange(ctx, CodebaseChange{
					Type:      "index_complete",
					RepoPath:  repoPath,
					Timestamp: status.LastIndexTime,
				})
			}
		}
	}
}

// handleChange processes a codebase change event
func (cs *CodebaseSynchronizer) handleChange(ctx context.Context, change CodebaseChange) {
	if !cs.config.AutoInvalidate {
		return
	}

	switch change.Type {
	case "file_modified", "file_deleted":
		// Invalidate context entries linked to this file
		cs.invalidateByFile(ctx, change.FilePath)

	case "symbol_changed":
		// Invalidate context entries linked to this symbol
		cs.invalidateBySymbol(ctx, change.SymbolID)

	case "index_complete":
		// Re-validate all links for this repository
		cs.revalidateLinks(ctx, change.RepoPath)
	}
}

// invalidateByFile invalidates context linked to a file
func (cs *CodebaseSynchronizer) invalidateByFile(ctx context.Context, filePath string) {
	cs.mu.RLock()
	linkIDs := cs.linksByFile[filePath]
	cs.mu.RUnlock()

	if len(linkIDs) == 0 {
		return
	}

	// Mark links as invalid
	cs.mu.Lock()
	contextIDs := make([]string, 0)
	for _, linkID := range linkIDs {
		if link, ok := cs.links[linkID]; ok {
			link.IsValid = false
			contextIDs = append(contextIDs, link.ContextID)
		}
	}
	cs.mu.Unlock()

	// Mark context entries as stale
	if cs.contextService != nil && len(contextIDs) > 0 {
		if err := cs.contextService.MarkStale(ctx, contextIDs); err != nil {
			cs.logger.Warn("failed to mark context entries as stale by file", "file_path", filePath, "entries", len(contextIDs), "error", err)
		}
	}
}

// invalidateBySymbol invalidates context linked to a symbol
func (cs *CodebaseSynchronizer) invalidateBySymbol(ctx context.Context, symbolID string) {
	cs.mu.RLock()
	linkIDs := cs.linksBySymbol[symbolID]
	cs.mu.RUnlock()

	if len(linkIDs) == 0 {
		return
	}

	// Mark links as invalid
	cs.mu.Lock()
	contextIDs := make([]string, 0)
	for _, linkID := range linkIDs {
		if link, ok := cs.links[linkID]; ok {
			link.IsValid = false
			contextIDs = append(contextIDs, link.ContextID)
		}
	}
	cs.mu.Unlock()

	// Mark context entries as stale
	if cs.contextService != nil && len(contextIDs) > 0 {
		if err := cs.contextService.MarkStale(ctx, contextIDs); err != nil {
			cs.logger.Warn("failed to mark context entries as stale by symbol", "symbol_id", symbolID, "entries", len(contextIDs), "error", err)
		}
	}
}

// revalidateLinks validates links against current codebase state
func (cs *CodebaseSynchronizer) revalidateLinks(ctx context.Context, repoPath string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	for _, link := range cs.links {
		// Check if this link belongs to the updated repo
		// For now, validate all links (could filter by repo path)

		if cs.codebaseClient != nil {
			symbol, err := cs.codebaseClient.GetSymbol(ctx, link.SymbolID)
			if err != nil || symbol == nil {
				link.IsValid = false
			} else {
				link.IsValid = true
				link.ValidatedAt = time.Now()
			}
		}
	}
}

// LinkContext creates a link between a context entry and codebase symbols
func (cs *CodebaseSynchronizer) LinkContext(ctx context.Context, contextID string, filePath string) ([]CodebaseLink, error) {
	if cs.codebaseClient == nil {
		return nil, nil
	}

	// Get symbols in the file
	symbols, err := cs.codebaseClient.GetFileSymbols(ctx, filePath)
	if err != nil {
		return nil, err
	}

	var links []CodebaseLink

	for _, symbol := range symbols {
		link := CodebaseLink{
			ContextID:  contextID,
			FilePath:   filePath,
			SymbolID:   symbol.ID,
			SymbolName: symbol.Name,
			SymbolType: symbol.Type,
			SymbolPath: symbol.Namespace + "." + symbol.Name,
			LinkType:   "file",
			Confidence: 1.0,
			CreatedAt:  time.Now(),
			IsValid:    true,
		}

		linkID := contextID + "_" + symbol.ID

		cs.mu.Lock()
		cs.links[linkID] = &link
		cs.linksByContext[contextID] = append(cs.linksByContext[contextID], linkID)
		cs.linksBySymbol[symbol.ID] = append(cs.linksBySymbol[symbol.ID], linkID)
		cs.linksByFile[filePath] = append(cs.linksByFile[filePath], linkID)
		cs.mu.Unlock()

		links = append(links, link)
	}

	return links, nil
}

// GetLinksForContext returns all links for a context entry
func (cs *CodebaseSynchronizer) GetLinksForContext(contextID string) []CodebaseLink {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	linkIDs := cs.linksByContext[contextID]
	links := make([]CodebaseLink, 0, len(linkIDs))

	for _, linkID := range linkIDs {
		if link, ok := cs.links[linkID]; ok {
			links = append(links, *link)
		}
	}

	return links
}

// GetLinksForSymbol returns all links for a symbol
func (cs *CodebaseSynchronizer) GetLinksForSymbol(symbolID string) []CodebaseLink {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	linkIDs := cs.linksBySymbol[symbolID]
	links := make([]CodebaseLink, 0, len(linkIDs))

	for _, linkID := range linkIDs {
		if link, ok := cs.links[linkID]; ok {
			links = append(links, *link)
		}
	}

	return links
}

// GetLinksForFile returns all links for a file
func (cs *CodebaseSynchronizer) GetLinksForFile(filePath string) []CodebaseLink {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	linkIDs := cs.linksByFile[filePath]
	links := make([]CodebaseLink, 0, len(linkIDs))

	for _, linkID := range linkIDs {
		if link, ok := cs.links[linkID]; ok {
			links = append(links, *link)
		}
	}

	return links
}

// RemoveLink removes a specific link
func (cs *CodebaseSynchronizer) RemoveLink(linkID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	link, ok := cs.links[linkID]
	if !ok {
		return false
	}

	// Remove from all indexes
	delete(cs.links, linkID)
	cs.removeFromSlice(cs.linksByContext, link.ContextID, linkID)
	cs.removeFromSlice(cs.linksBySymbol, link.SymbolID, linkID)
	cs.removeFromSlice(cs.linksByFile, link.FilePath, linkID)

	return true
}

// RemoveLinksForContext removes all links for a context entry
func (cs *CodebaseSynchronizer) RemoveLinksForContext(contextID string) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	linkIDs := cs.linksByContext[contextID]
	removed := 0

	for _, linkID := range linkIDs {
		if link, ok := cs.links[linkID]; ok {
			delete(cs.links, linkID)
			cs.removeFromSlice(cs.linksBySymbol, link.SymbolID, linkID)
			cs.removeFromSlice(cs.linksByFile, link.FilePath, linkID)
			removed++
		}
	}

	delete(cs.linksByContext, contextID)
	return removed
}

// removeFromSlice removes a value from a slice in a map
func (cs *CodebaseSynchronizer) removeFromSlice(m map[string][]string, key string, value string) {
	if slice, ok := m[key]; ok {
		for i, v := range slice {
			if v == value {
				m[key] = append(slice[:i], slice[i+1:]...)
				return
			}
		}
	}
}

// Stats returns synchronization statistics
type CodebaseSyncStats struct {
	TotalLinks     int `json:"total_links"`
	ValidLinks     int `json:"valid_links"`
	InvalidLinks   int `json:"invalid_links"`
	LinkedContexts int `json:"linked_contexts"`
	LinkedSymbols  int `json:"linked_symbols"`
	LinkedFiles    int `json:"linked_files"`
	WatchedRepos   int `json:"watched_repos"`
}

// Stats returns current synchronization statistics
func (cs *CodebaseSynchronizer) Stats() CodebaseSyncStats {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	stats := CodebaseSyncStats{
		TotalLinks:     len(cs.links),
		LinkedContexts: len(cs.linksByContext),
		LinkedSymbols:  len(cs.linksBySymbol),
		LinkedFiles:    len(cs.linksByFile),
		WatchedRepos:   len(cs.config.WatchedRepos),
	}

	for _, link := range cs.links {
		if link.IsValid {
			stats.ValidLinks++
		} else {
			stats.InvalidLinks++
		}
	}

	return stats
}

// SearchRelatedContext searches for context related to a codebase query
func (cs *CodebaseSynchronizer) SearchRelatedContext(ctx context.Context, query string, limit int) ([]string, error) {
	if cs.codebaseClient == nil {
		return nil, nil
	}

	// Search codebase for matching symbols
	symbols, err := cs.codebaseClient.Search(ctx, query, limit*2)
	if err != nil {
		return nil, err
	}

	// Find context entries linked to these symbols
	contextIDs := make(map[string]bool)

	cs.mu.RLock()
	for _, symbol := range symbols {
		linkIDs := cs.linksBySymbol[symbol.ID]
		for _, linkID := range linkIDs {
			if link, ok := cs.links[linkID]; ok && link.IsValid {
				contextIDs[link.ContextID] = true
			}
		}
	}
	cs.mu.RUnlock()

	// Convert to slice
	result := make([]string, 0, len(contextIDs))
	for id := range contextIDs {
		result = append(result, id)
		if len(result) >= limit {
			break
		}
	}

	return result, nil
}
