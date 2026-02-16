package bridge

import (
	"errors"
	"io"
	"net"
	"testing"
)

// --- Tests for isServerUnavailable ---

func TestIsServerUnavailable_NilError(t *testing.T) {
	if isServerUnavailable(nil) {
		t.Error("expected false for nil error")
	}
}

func TestIsServerUnavailable_ServerUnavailableMessage(t *testing.T) {
	err := errors.New("daemon error: server unavailable")
	if !isServerUnavailable(err) {
		t.Error("expected true for 'server unavailable' message")
	}
}

func TestIsServerUnavailable_BrokenPipeMessage(t *testing.T) {
	err := errors.New("write body: broken pipe")
	if !isServerUnavailable(err) {
		t.Error("expected true for 'broken pipe' message")
	}
}

func TestIsServerUnavailable_BadHandshakeMessage(t *testing.T) {
	err := errors.New("transport: bad handshake")
	if !isServerUnavailable(err) {
		t.Error("expected true for 'bad handshake' message")
	}
}

func TestIsServerUnavailable_UnrelatedError(t *testing.T) {
	err := errors.New("connection timeout")
	if isServerUnavailable(err) {
		t.Error("expected false for unrelated error")
	}
}

// --- Tests for isTransportError ---

func TestIsTransportError_NilError(t *testing.T) {
	if isTransportError(nil) {
		t.Error("expected false for nil error")
	}
}

func TestIsTransportError_EOF(t *testing.T) {
	if !isTransportError(io.EOF) {
		t.Error("expected true for io.EOF")
	}
}

func TestIsTransportError_NetErrClosed(t *testing.T) {
	if !isTransportError(net.ErrClosed) {
		t.Error("expected true for net.ErrClosed")
	}
}

func TestIsTransportError_NotConnected(t *testing.T) {
	err := errors.New("not connected")
	if !isTransportError(err) {
		t.Error("expected true for 'not connected'")
	}
}

func TestIsTransportError_SendError(t *testing.T) {
	err := errors.New("send: write failed")
	if !isTransportError(err) {
		t.Error("expected true for 'send:' prefix")
	}
}

func TestIsTransportError_RecvError(t *testing.T) {
	err := errors.New("recv: read failed")
	if !isTransportError(err) {
		t.Error("expected true for 'recv:' prefix")
	}
}

func TestIsTransportError_ConnectionReset(t *testing.T) {
	err := errors.New("connection reset by peer")
	if !isTransportError(err) {
		t.Error("expected true for 'connection reset'")
	}
}

func TestIsTransportError_ClosedNetwork(t *testing.T) {
	err := errors.New("use of closed network connection")
	if !isTransportError(err) {
		t.Error("expected true for closed network connection")
	}
}

func TestIsTransportError_DaemonErrorNotTransport(t *testing.T) {
	// "daemon error" messages indicate successful communication with daemon,
	// even if the downstream server is broken.
	err := errors.New("daemon error (500): broken pipe downstream")
	if isTransportError(err) {
		t.Error("expected false for 'daemon error' messages (downstream issue, not transport)")
	}
}

func TestIsTransportError_UnrelatedError(t *testing.T) {
	err := errors.New("json: invalid character")
	if isTransportError(err) {
		t.Error("expected false for unrelated JSON error")
	}
}

// --- Tests for normalizeEntityInfo ---

func TestNormalizeEntityInfo_NilSafe(t *testing.T) {
	// Should not panic on nil.
	normalizeEntityInfo(nil)
}

func TestNormalizeEntityInfo_CopiesTypeToEntityType(t *testing.T) {
	e := &EntityInfo{
		ID:   "e-1",
		Name: "TestEntity",
		Type: "service",
	}
	normalizeEntityInfo(e)

	if e.EntityType != "service" {
		t.Errorf("expected EntityType 'service', got %q", e.EntityType)
	}
}

func TestNormalizeEntityInfo_CopiesEntityTypeToType(t *testing.T) {
	e := &EntityInfo{
		ID:         "e-1",
		Name:       "TestEntity",
		EntityType: "database",
	}
	normalizeEntityInfo(e)

	if e.Type != "database" {
		t.Errorf("expected Type 'database', got %q", e.Type)
	}
}

func TestNormalizeEntityInfo_BothSet(t *testing.T) {
	e := &EntityInfo{
		ID:         "e-1",
		Name:       "TestEntity",
		Type:       "service",
		EntityType: "database",
	}
	normalizeEntityInfo(e)

	// Both are already set, should remain unchanged.
	if e.Type != "service" {
		t.Errorf("expected Type 'service', got %q", e.Type)
	}
	if e.EntityType != "database" {
		t.Errorf("expected EntityType 'database', got %q", e.EntityType)
	}
}

func TestNormalizeEntityInfo_BothEmpty(t *testing.T) {
	e := &EntityInfo{
		ID:   "e-1",
		Name: "TestEntity",
	}
	normalizeEntityInfo(e)

	if e.Type != "" {
		t.Errorf("expected empty Type, got %q", e.Type)
	}
	if e.EntityType != "" {
		t.Errorf("expected empty EntityType, got %q", e.EntityType)
	}
}

// --- Tests for normalizeRelationInfo ---

func TestNormalizeRelationInfo_NilSafe(t *testing.T) {
	normalizeRelationInfo(nil)
}

func TestNormalizeRelationInfo_CopiesTypeToRelationType(t *testing.T) {
	r := &RelationInfo{
		ID:   "r-1",
		Type: "depends_on",
	}
	normalizeRelationInfo(r)

	if r.RelationType != "depends_on" {
		t.Errorf("expected RelationType 'depends_on', got %q", r.RelationType)
	}
}

func TestNormalizeRelationInfo_CopiesRelationTypeToType(t *testing.T) {
	r := &RelationInfo{
		ID:           "r-1",
		RelationType: "owns",
	}
	normalizeRelationInfo(r)

	if r.Type != "owns" {
		t.Errorf("expected Type 'owns', got %q", r.Type)
	}
}

func TestNormalizeRelationInfo_BothSet(t *testing.T) {
	r := &RelationInfo{
		ID:           "r-1",
		Type:         "uses",
		RelationType: "depends_on",
	}
	normalizeRelationInfo(r)

	if r.Type != "uses" {
		t.Errorf("expected Type 'uses', got %q", r.Type)
	}
	if r.RelationType != "depends_on" {
		t.Errorf("expected RelationType 'depends_on', got %q", r.RelationType)
	}
}

// --- Tests for NewEventConsumer URL normalization ---

func TestNewEventConsumer_StripsTrailingSlash(t *testing.T) {
	ec := NewEventConsumer("http://localhost:9090/", nil)
	if ec.daemonHTTPURL != "http://localhost:9090" {
		t.Errorf("expected trailing slash stripped, got %q", ec.daemonHTTPURL)
	}
}

func TestNewEventConsumer_NoTrailingSlash(t *testing.T) {
	ec := NewEventConsumer("http://localhost:9090", nil)
	if ec.daemonHTTPURL != "http://localhost:9090" {
		t.Errorf("expected unchanged URL, got %q", ec.daemonHTTPURL)
	}
}

func TestNewEventConsumer_MultipleTrailingSlashes(t *testing.T) {
	ec := NewEventConsumer("http://localhost:9090///", nil)
	if ec.daemonHTTPURL != "http://localhost:9090" {
		t.Errorf("expected all trailing slashes stripped, got %q", ec.daemonHTTPURL)
	}
}

func TestNewEventConsumer_HandlersInitialized(t *testing.T) {
	ec := NewEventConsumer("http://localhost:9090", nil)
	if ec.handlers == nil {
		t.Error("expected handlers map to be initialized")
	}
}

// --- Tests for EventConsumer.On and OnAny handler registration ---

func TestEventConsumer_OnRegistersHandler(t *testing.T) {
	ec := NewEventConsumer("http://localhost:9090", nil)

	ec.On("server.health", func(_ SSEEvent) {})

	ec.mu.RLock()
	handlers := ec.handlers["server.health"]
	ec.mu.RUnlock()

	if len(handlers) != 1 {
		t.Fatalf("expected 1 handler for server.health, got %d", len(handlers))
	}
}

func TestEventConsumer_OnAnyRegistersHandler(t *testing.T) {
	ec := NewEventConsumer("http://localhost:9090", nil)

	ec.OnAny(func(event SSEEvent) {})
	ec.OnAny(func(event SSEEvent) {})

	ec.mu.RLock()
	count := len(ec.anyHandlers)
	ec.mu.RUnlock()

	if count != 2 {
		t.Fatalf("expected 2 any handlers, got %d", count)
	}
}

// --- Tests for Cache.Len ---

func TestCache_Len(t *testing.T) {
	c := NewCache()

	if c.Len() != 0 {
		t.Fatalf("expected 0, got %d", c.Len())
	}

	c.Set("a", 1, 5e9)
	c.Set("b", 2, 5e9)

	if c.Len() != 2 {
		t.Fatalf("expected 2, got %d", c.Len())
	}
}
