// file_claims_conflict_bus.go defines a lightweight in-process pub/sub bus for
// file-claim conflict events (F9 - live file-claim conflict overlay).
//
// When agent_file_claim_acquire detects a collision with an existing claim,
// ClaimSvc.Acquire publishes a ClaimConflictEvent on the process-wide bus.
// HUD SSE subscribers consume those events and push them to the frontend.
//
// The bus is best-effort: publish is non-blocking; if a subscriber's buffered
// channel is full, the event is dropped for that subscriber. This keeps the
// write path fast and avoids back-pressuring claim acquisition.
package agentcontext

import (
	"sync"
	"sync/atomic"
	"time"
)

// ClaimConflictEvent is emitted when a requester tries to claim a file that is
// already held by another agent.
type ClaimConflictEvent struct {
	File      string    `json:"file"`
	Holder    string    `json:"holder"`
	Requester string    `json:"requester"`
	TS        time.Time `json:"ts"`
}

// conflictSubChanSize is the buffered size of each subscriber channel.
// Events are dropped when a subscriber is slower than this queue depth.
const conflictSubChanSize = 16

// ConflictBus is a thread-safe, non-blocking pub/sub bus for ClaimConflictEvent.
// The zero value is ready to use.
type ConflictBus struct {
	subs   sync.Map // uint64 -> chan ClaimConflictEvent
	nextID atomic.Uint64
}

// NewConflictBus returns a new, empty ConflictBus.
func NewConflictBus() *ConflictBus {
	return &ConflictBus{}
}

// Subscribe registers a new subscriber and returns its event channel plus an
// unsubscribe function. The channel is buffered (size 16) and closed by
// unsubscribe. Unsubscribe is safe to call multiple times.
func (b *ConflictBus) Subscribe() (<-chan ClaimConflictEvent, func()) {
	ch := make(chan ClaimConflictEvent, conflictSubChanSize)
	id := b.nextID.Add(1)
	b.subs.Store(id, ch)

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.subs.Delete(id)
			close(ch)
		})
	}
	return ch, unsub
}

// Publish fan-outs the event to every current subscriber. If a subscriber's
// buffer is full, the event is dropped for that subscriber only — never
// blocks. Safe to call concurrently from any goroutine.
func (b *ConflictBus) Publish(evt ClaimConflictEvent) {
	if b == nil {
		return
	}
	b.subs.Range(func(_, v any) bool {
		ch, ok := v.(chan ClaimConflictEvent)
		if !ok {
			return true
		}
		select {
		case ch <- evt:
		default:
			// Subscriber buffer full: drop and move on.
		}
		return true
	})
}

// defaultConflictBus is a process-wide singleton used to bridge the daemon
// (producer of ClaimConflictEvent) and the in-process HUD SSE handler
// (consumer). Both are wired into the same binary via bridge.LocalCaller.
var defaultConflictBus = NewConflictBus()

// DefaultConflictBus returns the process-wide bus. Tests should create their
// own via NewConflictBus instead of sharing this global.
func DefaultConflictBus() *ConflictBus {
	return defaultConflictBus
}
