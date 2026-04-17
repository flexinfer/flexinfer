// svc_context_delta.go -- Delta-style recall for resumable polling.
//
// HandleRecallSince returns entries strictly newer than an opaque cursor,
// ordered oldest-first. It is intended for agents that want to tail a session
// feed without repeatedly re-fetching the full history.
package agentcontext

import (
	"context"
	"fmt"
	"sort"

	"gitlab.flexinfer.ai/libs/mcp-go"

	"github.com/crb2nu/loom/pkg/validate"
)

// defaultRecallSinceLimit is the page size used when the caller omits `limit`.
const defaultRecallSinceLimit = 50

// recallSinceScrollCap bounds how many points we fetch from Qdrant per call
// before filtering/sorting in Go. It is intentionally generous — Qdrant payload
// fields cannot be range-filtered on RFC3339 strings, so we post-filter.
const recallSinceScrollCap = 2000

// HandleRecallSince returns the slice of context entries for a session whose
// timestamp is strictly greater than the position encoded in `cursor`, ordered
// ascending by timestamp. Response shape:
//
//	{
//	  "ok":          true,
//	  "results":     []ContextEntry,   // oldest-first, length <= limit
//	  "count":       int,
//	  "next_cursor": "<opaque string>" // pass back on next call
//	}
//
// When no entries are newer than the cursor, the input cursor is echoed back
// so a poll loop stays cheap.
func (s *Service) HandleRecallSince(ctx context.Context, args map[string]any) (*mcp.CallToolResult, error) {
	v := validate.NewArgs(args)
	sessionID := v.Required("session_id")
	cursor := v.String("cursor", "")
	limit := v.Int("limit", defaultRecallSinceLimit)

	if err := v.Validate(); err != nil {
		return mcp.ErrorResult(err), nil
	}

	if limit <= 0 {
		limit = defaultRecallSinceLimit
	}

	cursorSession, cursorNs, err := DecodeCursor(cursor)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("invalid cursor: %w", err)), nil
	}
	// Defensive: if caller passes a cursor from a different session, reject.
	if cursorSession != "" && cursorSession != sessionID {
		return mcp.ErrorResult(fmt.Errorf("cursor session mismatch: cursor=%q session_id=%q", cursorSession, sessionID)), nil
	}

	filter := FilterMust(Match("session_id", sessionID))

	client := s.qdrant.Get(CollContext)
	if client == nil {
		return mcp.ErrorResult(fmt.Errorf("context collection not available")), nil
	}

	// Scroll entries for the session, then filter + sort in Go. Payload
	// timestamps are stored as RFC3339Nano strings so we cannot express a
	// range condition in Qdrant's filter DSL reliably; post-filtering is
	// the defensible correctness choice here.
	points, err := client.ScrollPoints(ctx, filter, recallSinceScrollCap, false)
	if err != nil {
		return mcp.ErrorResult(fmt.Errorf("scroll: %w", err)), nil
	}

	raw := make([]ContextEntry, 0, len(points))
	for _, p := range points {
		entry, err := PayloadToEntry(p.Payload)
		if err != nil || entry == nil {
			continue
		}
		raw = append(raw, *entry)
	}

	entries := filterEntriesAfter(raw, cursorNs, limit)

	nextCursor := cursor
	if len(entries) > 0 {
		last := entries[len(entries)-1].Timestamp
		nextCursor = EncodeCursor(sessionID, last)
	}

	s.metrics.RecallRequests.Add(1)

	return mcp.JSONResult(map[string]any{
		"ok":          true,
		"results":     entries,
		"count":       len(entries),
		"next_cursor": nextCursor,
	})
}

// filterEntriesAfter returns entries whose Timestamp is strictly greater than
// cursorNs (nanoseconds since epoch), sorted ascending by Timestamp, truncated
// to at most `limit` items. Extracted as a pure helper so the partition
// invariant can be tested without a Qdrant stub.
//
// Invariant: for two consecutive calls using the same underlying entry set and
// chaining cursors (empty → max-ts of first result → …), the union of returned
// slices covers every entry once, with no overlap.
func filterEntriesAfter(entries []ContextEntry, cursorNs int64, limit int) []ContextEntry {
	out := make([]ContextEntry, 0, len(entries))
	for _, e := range entries {
		if e.Timestamp.UnixNano() <= cursorNs {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
