package daemon

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// captureBus is a minimal stand-in for an EventBus that records published
// events. We can't easily mock *EventBus because the handler calls a
// concrete method, so we point d.eventBus at a real one and subscribe.
func subscribeAll(t *testing.T, d *Daemon) (events <-chan Event, cleanup func()) {
	t.Helper()
	subID, ch := d.eventBus.Subscribe()
	return ch, func() { d.eventBus.Unsubscribe(subID) }
}

func newTestDaemonForEvents(t *testing.T) *Daemon {
	t.Helper()
	return &Daemon{
		eventBus: NewEventBus(slog.New(slog.DiscardHandler)),
	}
}

func postEvent(t *testing.T, d *Daemon, body string, headers map[string]string, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/events/publish", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	d.HandleEventsPublish(w, req)
	return w
}

func TestHandleEventsPublish_ValidEnvelope_Republishes(t *testing.T) {
	d := newTestDaemonForEvents(t)
	events, cleanup := subscribeAll(t, d)
	defer cleanup()

	w := postEvent(t, d,
		`{"type":"session.start","data":{"session_id":"s1","agent_id":"a1"}}`,
		nil, "127.0.0.1:54321")

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", w.Code, w.Body.String())
	}

	var got Event
	select {
	case got = <-events:
	default:
		t.Fatal("no event published to bus")
	}
	if string(got.Type) != "session.start" {
		t.Errorf("Type = %q, want session.start", got.Type)
	}
	dataMap, _ := got.Data.(map[string]any)
	if dataMap["session_id"] != "s1" {
		t.Errorf("Data.session_id = %v, want s1", dataMap["session_id"])
	}
}

func TestHandleEventsPublish_RejectsNonPost(t *testing.T) {
	d := newTestDaemonForEvents(t)
	req := httptest.NewRequest(http.MethodGet, "/events/publish", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	d.HandleEventsPublish(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleEventsPublish_RejectsEmptyType(t *testing.T) {
	d := newTestDaemonForEvents(t)
	w := postEvent(t, d, `{"type":"","data":{}}`, nil, "127.0.0.1:1234")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleEventsPublish_RejectsInvalidJSON(t *testing.T) {
	d := newTestDaemonForEvents(t)
	w := postEvent(t, d, `not json`, nil, "127.0.0.1:1234")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleEventsPublish_NoToken_RejectsRemoteCallers(t *testing.T) {
	d := newTestDaemonForEvents(t) // no admin token configured
	w := postEvent(t, d,
		`{"type":"session.start","data":{}}`,
		nil, "10.0.0.5:9999") // non-loopback
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for non-loopback without token", w.Code)
	}
}

func TestHandleEventsPublish_NoToken_AcceptsLoopback(t *testing.T) {
	d := newTestDaemonForEvents(t)
	w := postEvent(t, d,
		`{"type":"session.start","data":{}}`,
		nil, "127.0.0.1:9999")
	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204 for loopback; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleEventsPublish_WithToken_RequiresMatchingBearer(t *testing.T) {
	d := newTestDaemonForEvents(t)
	d.fileCfg.EmbeddedHUD.AdminToken = "right-token"

	cases := []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"missing", nil, http.StatusForbidden},
		{"wrong-bearer", map[string]string{"Authorization": "Bearer wrong"}, http.StatusForbidden},
		{"non-bearer", map[string]string{"Authorization": "Basic right-token"}, http.StatusForbidden},
		{"correct", map[string]string{"Authorization": "Bearer right-token"}, http.StatusNoContent},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Use a non-loopback remote so the token path is the only thing
			// gating access; otherwise loopback would let everything through.
			w := postEvent(t, d,
				`{"type":"session.start","data":{}}`,
				c.header, "10.0.0.5:1234")
			if w.Code != c.want {
				t.Errorf("status = %d, want %d", w.Code, c.want)
			}
		})
	}
}

// concurrentDaemon stresses the publish path under contention to catch races
// in the (small) handler logic. Run with -race.
func TestHandleEventsPublish_Concurrent(t *testing.T) {
	d := newTestDaemonForEvents(t)
	events, cleanup := subscribeAll(t, d)
	defer cleanup()

	const N = 50
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(publishEnvelope{Type: "session.start", Data: json.RawMessage(`{"i":` + itoa(i) + `}`)})
			w := postEvent(t, d, string(body), nil, "127.0.0.1:1234")
			if w.Code != http.StatusNoContent {
				t.Errorf("publish %d: status %d", i, w.Code)
			}
		}(i)
	}
	wg.Wait()

	// Drain — exact count delivery isn't guaranteed under buffer pressure,
	// but the bus's published count should match.
	got := 0
	for {
		select {
		case <-events:
			got++
		default:
			if got == 0 {
				t.Errorf("no events delivered to subscriber")
			}
			return
		}
	}
}

func itoa(i int) string {
	// Minimal int-to-string for the concurrent test body without importing
	// strconv (kept the test file's imports tight).
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return strings.Clone(string(buf[pos:]))
}
