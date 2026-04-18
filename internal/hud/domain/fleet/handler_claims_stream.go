package fleet

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/crb2nu/loom/pkg/agentcontext"
)

// handleClaimsStream streams live file-claim conflict events to the HUD as
// Server-Sent Events (F9 overlay). Each event is the JSON form of
// ClaimConflictEvent. A heartbeat is emitted every 15s to keep the connection
// alive through any intermediate proxy.
//
// The handler subscribes to agentcontext.DefaultConflictBus(), which the
// in-process daemon's ClaimSvc publishes to on every Acquire collision. The
// HUD process shares the same binary via bridge.LocalCaller, so subscriber
// and publisher see the same bus.
func (d *FleetDomain) handleClaimsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	bus := agentcontext.DefaultConflictBus()
	ch, unsub := bus.Subscribe()
	defer unsub()

	// Prime the stream with a ready comment so EventSource readyState flips
	// to OPEN even when no conflicts are flowing yet.
	fmt.Fprint(w, ": stream-open\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, open := <-ch:
			if !open {
				return
			}
			payload, err := json.Marshal(map[string]any{
				"file":      evt.File,
				"holder":    evt.Holder,
				"requester": evt.Requester,
				"ts":        evt.TS.Format(time.RFC3339Nano),
			})
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: claim.conflict\ndata: %s\n\n", payload)
			flusher.Flush()
		case t := <-ticker.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"time\":%q}\n\n", t.Format(time.RFC3339))
			flusher.Flush()
		}
	}
}
