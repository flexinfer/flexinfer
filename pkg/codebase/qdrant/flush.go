package qdrant

import (
	"context"
	"fmt"
	"net/http"
)

// Flush forces Qdrant to durably commit any in-flight wait=false writes for
// this collection by issuing an empty-points upsert with wait=true. The
// round-trip blocks until Qdrant has flushed the WAL and finished pending
// segment work on the affected collection.
//
// Implementation note: we send an empty `points` array with `wait=true`
// directly via doJSON rather than calling Client.Upsert, because Upsert
// short-circuits on len(points)==0 to keep the bulk hot path cheap. Sending
// the request explicitly keeps Flush's "force durability" contract intact
// without changing Upsert's existing semantics or test surface.
//
// Use once at the end of a bulk indexing job. Idempotent and safe to call
// multiple times. Callers MUST treat Flush errors as soft warnings rather
// than job failures: prior wait=false writes are still durable via Qdrant's
// WAL fsync (flush_interval_sec=5 default server-side), so a transport
// failure here does not mean data loss — only that we cannot prove
// durability over the wire.
func (c *Client) Flush(ctx context.Context) error {
	path := fmt.Sprintf("/collections/%s/points?wait=true", c.collection)
	body := map[string]any{"points": []map[string]any{}}
	return c.doJSON(ctx, http.MethodPut, path, body, nil)
}
