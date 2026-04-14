package agentcontext

import (
	"context"
	"testing"
	"time"
)

type fakeCodebaseClient struct {
	fileSymbols map[string][]CodebaseSymbol
	symbols     map[string]*CodebaseSymbol
	search      []CodebaseSymbol
}

func (f *fakeCodebaseClient) Search(ctx context.Context, query string, limit int) ([]CodebaseSymbol, error) {
	return f.search, nil
}
func (f *fakeCodebaseClient) GetSymbol(ctx context.Context, symbolID string) (*CodebaseSymbol, error) {
	return f.symbols[symbolID], nil
}
func (f *fakeCodebaseClient) GetDefinition(ctx context.Context, symbolID string) (*CodebaseDefinition, error) {
	return nil, nil
}
func (f *fakeCodebaseClient) GetReferences(ctx context.Context, symbolID string) ([]CodebaseReference, error) {
	return nil, nil
}
func (f *fakeCodebaseClient) GetFileSymbols(ctx context.Context, filePath string) ([]CodebaseSymbol, error) {
	return f.fileSymbols[filePath], nil
}
func (f *fakeCodebaseClient) GetIndexStatus(ctx context.Context, repoPath string) (*CodebaseIndexStatus, error) {
	return &CodebaseIndexStatus{RepoPath: repoPath, LastIndexTime: time.Now()}, nil
}
func (f *fakeCodebaseClient) WatchChanges(ctx context.Context, repoPath string) (<-chan CodebaseChange, error) {
	ch := make(chan CodebaseChange)
	close(ch)
	return ch, nil
}

type fakeContextInvalidator struct {
	staleIDs []string
}

func (f *fakeContextInvalidator) InvalidateByFile(ctx context.Context, filePath string) (int, error) {
	return 0, nil
}
func (f *fakeContextInvalidator) InvalidateBySymbol(ctx context.Context, symbolID string) (int, error) {
	return 0, nil
}
func (f *fakeContextInvalidator) MarkStale(ctx context.Context, entryIDs []string) error {
	f.staleIDs = append(f.staleIDs, entryIDs...)
	return nil
}

func TestCodebaseSynchronizer_LinkContextAndSearchRelated(t *testing.T) {
	client := &fakeCodebaseClient{
		fileSymbols: map[string][]CodebaseSymbol{
			"pkg/example.go": {
				{ID: "sym-1", Name: "DoThing", Type: "function", Namespace: "pkg"},
				{ID: "sym-2", Name: "Helper", Type: "function", Namespace: "pkg"},
			},
		},
		symbols: map[string]*CodebaseSymbol{
			"sym-1": {ID: "sym-1", Name: "DoThing"},
			"sym-2": {ID: "sym-2", Name: "Helper"},
		},
		search: []CodebaseSymbol{{ID: "sym-2", Name: "Helper"}},
	}
	invalidator := &fakeContextInvalidator{}
	syncer := NewCodebaseSynchronizer(DefaultCodebaseSyncConfig(), client, invalidator)

	links, err := syncer.LinkContext(context.Background(), "ctx-1", "pkg/example.go")
	if err != nil {
		t.Fatalf("LinkContext: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("links = %d, want 2", len(links))
	}

	stats := syncer.Stats()
	if stats.TotalLinks != 2 || stats.ValidLinks != 2 {
		t.Fatalf("unexpected stats: %#v", stats)
	}

	related, err := syncer.SearchRelatedContext(context.Background(), "helper", 5)
	if err != nil {
		t.Fatalf("SearchRelatedContext: %v", err)
	}
	if len(related) != 1 || related[0] != "ctx-1" {
		t.Fatalf("related = %#v, want ctx-1", related)
	}
}

func TestCodebaseSynchronizer_HandleChangeInvalidatesLinks(t *testing.T) {
	client := &fakeCodebaseClient{
		fileSymbols: map[string][]CodebaseSymbol{
			"pkg/example.go": {
				{ID: "sym-1", Name: "DoThing", Type: "function", Namespace: "pkg"},
			},
		},
		symbols: map[string]*CodebaseSymbol{
			"sym-1": {ID: "sym-1", Name: "DoThing"},
		},
	}
	invalidator := &fakeContextInvalidator{}
	syncer := NewCodebaseSynchronizer(DefaultCodebaseSyncConfig(), client, invalidator)

	if _, err := syncer.LinkContext(context.Background(), "ctx-1", "pkg/example.go"); err != nil {
		t.Fatalf("LinkContext: %v", err)
	}
	syncer.handleChange(context.Background(), CodebaseChange{Type: "file_modified", FilePath: "pkg/example.go"})

	stats := syncer.Stats()
	if stats.InvalidLinks != 1 {
		t.Fatalf("invalid links = %d, want 1", stats.InvalidLinks)
	}
	if len(invalidator.staleIDs) != 1 || invalidator.staleIDs[0] != "ctx-1" {
		t.Fatalf("stale IDs = %#v, want [ctx-1]", invalidator.staleIDs)
	}
}

func TestCodebaseSynchronizer_GettersAndRemovals(t *testing.T) {
	client := &fakeCodebaseClient{
		fileSymbols: map[string][]CodebaseSymbol{
			"pkg/example.go": {
				{ID: "sym-1", Name: "DoThing", Type: "function", Namespace: "pkg"},
				{ID: "sym-2", Name: "Helper", Type: "function", Namespace: "pkg"},
			},
		},
		symbols: map[string]*CodebaseSymbol{
			"sym-1": {ID: "sym-1", Name: "DoThing"},
			"sym-2": {ID: "sym-2", Name: "Helper"},
		},
	}
	syncer := NewCodebaseSynchronizer(DefaultCodebaseSyncConfig(), client, &fakeContextInvalidator{})

	if _, err := syncer.LinkContext(context.Background(), "ctx-1", "pkg/example.go"); err != nil {
		t.Fatalf("LinkContext: %v", err)
	}
	if got := len(syncer.GetLinksForContext("ctx-1")); got != 2 {
		t.Fatalf("context links = %d, want 2", got)
	}
	if got := len(syncer.GetLinksForSymbol("sym-1")); got != 1 {
		t.Fatalf("symbol links = %d, want 1", got)
	}
	if got := len(syncer.GetLinksForFile("pkg/example.go")); got != 2 {
		t.Fatalf("file links = %d, want 2", got)
	}

	if !syncer.RemoveLink("ctx-1_sym-1") {
		t.Fatal("expected RemoveLink to remove existing link")
	}
	if got := len(syncer.GetLinksForContext("ctx-1")); got != 1 {
		t.Fatalf("context links after remove = %d, want 1", got)
	}

	if removed := syncer.RemoveLinksForContext("ctx-1"); removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if got := syncer.Stats().TotalLinks; got != 0 {
		t.Fatalf("total links = %d, want 0", got)
	}
}

func TestCodebaseSynchronizer_SymbolChangeInvalidatesLinkedContext(t *testing.T) {
	client := &fakeCodebaseClient{
		fileSymbols: map[string][]CodebaseSymbol{
			"pkg/example.go": {
				{ID: "sym-1", Name: "DoThing", Type: "function", Namespace: "pkg"},
			},
		},
		symbols: map[string]*CodebaseSymbol{
			"sym-1": {ID: "sym-1", Name: "DoThing"},
		},
	}
	invalidator := &fakeContextInvalidator{}
	syncer := NewCodebaseSynchronizer(DefaultCodebaseSyncConfig(), client, invalidator)

	if _, err := syncer.LinkContext(context.Background(), "ctx-1", "pkg/example.go"); err != nil {
		t.Fatalf("LinkContext: %v", err)
	}
	syncer.handleChange(context.Background(), CodebaseChange{Type: "symbol_changed", SymbolID: "sym-1"})

	if got := syncer.Stats().InvalidLinks; got != 1 {
		t.Fatalf("invalid links = %d, want 1", got)
	}
	if len(invalidator.staleIDs) != 1 || invalidator.staleIDs[0] != "ctx-1" {
		t.Fatalf("stale IDs = %#v, want [ctx-1]", invalidator.staleIDs)
	}
}

func TestCodebaseSynchronizer_RevalidateLinks(t *testing.T) {
	client := &fakeCodebaseClient{
		fileSymbols: map[string][]CodebaseSymbol{
			"pkg/example.go": {
				{ID: "sym-1", Name: "DoThing", Type: "function", Namespace: "pkg"},
			},
		},
		symbols: map[string]*CodebaseSymbol{},
	}
	syncer := NewCodebaseSynchronizer(DefaultCodebaseSyncConfig(), client, &fakeContextInvalidator{})

	if _, err := syncer.LinkContext(context.Background(), "ctx-1", "pkg/example.go"); err != nil {
		t.Fatalf("LinkContext: %v", err)
	}

	syncer.revalidateLinks(context.Background(), "repo")
	if got := syncer.Stats().InvalidLinks; got != 1 {
		t.Fatalf("invalid links = %d, want 1 after missing symbol", got)
	}

	client.symbols["sym-1"] = &CodebaseSymbol{ID: "sym-1", Name: "DoThing"}
	syncer.revalidateLinks(context.Background(), "repo")
	if got := syncer.Stats().ValidLinks; got != 1 {
		t.Fatalf("valid links = %d, want 1 after symbol restore", got)
	}
}
