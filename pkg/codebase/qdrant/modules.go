package qdrant

import (
	"context"

	"github.com/crb2nu/loom/pkg/codebase/schema"
)

func (c *Client) ListModules(ctx context.Context, repoID string, max int) ([]schema.Chunk, error) {
	if max <= 0 {
		max = 2048
	}
	return c.scroll(ctx, filterMust(
		match("repo_id", repoID),
		match("chunk_type", "module"),
	), max)
}
