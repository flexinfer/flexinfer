package bridge

import (
	"encoding/json"
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

// --- Tests for EntityInfo.UnmarshalJSON auto-sync ---

func TestEntityInfo_UnmarshalJSON_TypeSyncsToEntityType(t *testing.T) {
	data := []byte(`{"id":"e-1","name":"TestEntity","type":"service"}`)
	var e EntityInfo
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.EntityType != "service" {
		t.Errorf("expected EntityType 'service', got %q", e.EntityType)
	}
	if e.Type != "service" {
		t.Errorf("expected Type 'service', got %q", e.Type)
	}
}

func TestEntityInfo_UnmarshalJSON_EntityTypeSyncsToType(t *testing.T) {
	data := []byte(`{"id":"e-1","name":"TestEntity","entity_type":"database"}`)
	var e EntityInfo
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Type != "database" {
		t.Errorf("expected Type 'database', got %q", e.Type)
	}
	if e.EntityType != "database" {
		t.Errorf("expected EntityType 'database', got %q", e.EntityType)
	}
}

func TestEntityInfo_UnmarshalJSON_BothSet(t *testing.T) {
	data := []byte(`{"id":"e-1","name":"TestEntity","type":"service","entity_type":"database"}`)
	var e EntityInfo
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Type != "service" {
		t.Errorf("expected Type 'service', got %q", e.Type)
	}
	if e.EntityType != "database" {
		t.Errorf("expected EntityType 'database', got %q", e.EntityType)
	}
}

func TestEntityInfo_UnmarshalJSON_BothEmpty(t *testing.T) {
	data := []byte(`{"id":"e-1","name":"TestEntity"}`)
	var e EntityInfo
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Type != "" {
		t.Errorf("expected empty Type, got %q", e.Type)
	}
	if e.EntityType != "" {
		t.Errorf("expected empty EntityType, got %q", e.EntityType)
	}
}

func TestEntityInfo_UnmarshalJSON_InvalidJSON(t *testing.T) {
	data := []byte(`{invalid}`)
	var e EntityInfo
	if err := json.Unmarshal(data, &e); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

// --- Tests for EntityDetail.UnmarshalJSON auto-sync ---

func TestEntityDetail_UnmarshalJSON_TypeSyncsToEntityType(t *testing.T) {
	data := []byte(`{"id":"e-1","name":"TestEntity","type":"service"}`)
	var d EntityDetail
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.EntityType != "service" {
		t.Errorf("expected EntityType 'service', got %q", d.EntityType)
	}
}

func TestEntityDetail_UnmarshalJSON_EntityTypeSyncsToType(t *testing.T) {
	data := []byte(`{"id":"e-1","name":"TestEntity","entity_type":"database"}`)
	var d EntityDetail
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if d.Type != "database" {
		t.Errorf("expected Type 'database', got %q", d.Type)
	}
}

// --- Tests for RelationInfo.UnmarshalJSON auto-sync ---

func TestRelationInfo_UnmarshalJSON_TypeSyncsToRelationType(t *testing.T) {
	data := []byte(`{"id":"r-1","source_id":"s","target_id":"t","type":"depends_on"}`)
	var r RelationInfo
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.RelationType != "depends_on" {
		t.Errorf("expected RelationType 'depends_on', got %q", r.RelationType)
	}
	if r.Type != "depends_on" {
		t.Errorf("expected Type 'depends_on', got %q", r.Type)
	}
}

func TestRelationInfo_UnmarshalJSON_RelationTypeSyncsToType(t *testing.T) {
	data := []byte(`{"id":"r-1","source_id":"s","target_id":"t","relation_type":"owns"}`)
	var r RelationInfo
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Type != "owns" {
		t.Errorf("expected Type 'owns', got %q", r.Type)
	}
	if r.RelationType != "owns" {
		t.Errorf("expected RelationType 'owns', got %q", r.RelationType)
	}
}

func TestRelationInfo_UnmarshalJSON_BothSet(t *testing.T) {
	data := []byte(`{"id":"r-1","source_id":"s","target_id":"t","type":"uses","relation_type":"depends_on"}`)
	var r RelationInfo
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.Type != "uses" {
		t.Errorf("expected Type 'uses', got %q", r.Type)
	}
	if r.RelationType != "depends_on" {
		t.Errorf("expected RelationType 'depends_on', got %q", r.RelationType)
	}
}

func TestRelationInfo_UnmarshalJSON_InvalidJSON(t *testing.T) {
	data := []byte(`{invalid}`)
	var r RelationInfo
	if err := json.Unmarshal(data, &r); err == nil {
		t.Error("expected error for invalid JSON")
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
