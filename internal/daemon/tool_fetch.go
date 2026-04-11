package daemon

import (
	"context"

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
	received := make([]bool, len(sources))
	type fetchOutcome struct {
		idx    int
		result toolFetchResult
	}
	outcomes := make(chan fetchOutcome, len(sources))

	for i, src := range sources {
		go func(idx int, source toolSource) {
			// Acquire slot or abort if context is cancelled.
			select {
			case sem <- struct{}{}:
				// ok
			case <-ctx.Done():
				outcomes <- fetchOutcome{
					idx: idx,
					result: toolFetchResult{
						name: source.name,
						err:  ctx.Err(),
					},
				}
				return
			}
			defer func() { <-sem }()

			tools, err := fetch(ctx, source)
			outcomes <- fetchOutcome{
				idx: idx,
				result: toolFetchResult{
					name:  source.name,
					tools: tools,
					err:   err,
				},
			}
		}(i, src)
	}

	for remaining := len(sources); remaining > 0; remaining-- {
		select {
		case outcome := <-outcomes:
			results[outcome.idx] = outcome.result
			received[outcome.idx] = true
		case <-ctx.Done():
			for i, source := range sources {
				if received[i] {
					continue
				}
				results[i] = toolFetchResult{name: source.name, err: ctx.Err()}
			}
			return results
		}
	}

	return results
}
