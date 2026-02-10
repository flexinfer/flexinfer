package daemon

import (
	"context"
	"sync"

	"gitlab.flexinfer.ai/libs/mcp-go"
)

type toolSourceKind uint8

const (
	toolSourceLocal toolSourceKind = iota
	toolSourceHub
)

type toolSource struct {
	name string
	kind toolSourceKind
}

type toolFetchResult struct {
	name  string
	tools []mcp.Tool
	err   error
}

func fetchToolsBounded(
	ctx context.Context,
	sources []toolSource,
	limit int,
	fetch func(context.Context, toolSource) ([]mcp.Tool, error),
) []toolFetchResult {
	if limit <= 0 {
		limit = 1
	}

	sem := make(chan struct{}, limit)
	results := make([]toolFetchResult, len(sources))

	var wg sync.WaitGroup
	wg.Add(len(sources))
	for i, src := range sources {
		go func(idx int, source toolSource) {
			defer wg.Done()

			// Acquire slot or abort if context is cancelled.
			select {
			case sem <- struct{}{}:
				// ok
			case <-ctx.Done():
				results[idx] = toolFetchResult{name: source.name, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			tools, err := fetch(ctx, source)
			results[idx] = toolFetchResult{name: source.name, tools: tools, err: err}
		}(i, src)
	}

	wg.Wait()
	return results
}
