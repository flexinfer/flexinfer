package router

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/crb2nu/loom/pkg/registry"
	"gitlab.flexinfer.ai/libs/mcp-go"
)

// createTestRegistry creates a registry with the specified servers.
func createTestRegistry(servers map[string][]string) *registry.Registry {
	reg := &registry.Registry{}
	for name, categories := range servers {
		reg.Servers = append(reg.Servers, &registry.Server{
			Name:       name,
			Categories: categories,
		})
	}
	return reg
}

// mockTransport implements mcp.Transport for testing.
type mockTransport struct {
	sendErr   error
	recvErr   error
	closed    atomic.Bool
	sendDelay time.Duration
}

func (m *mockTransport) Send(ctx context.Context, msg *mcp.Message) error {
	if m.sendDelay > 0 {
		time.Sleep(m.sendDelay)
	}
	return m.sendErr
}

func (m *mockTransport) Recv(ctx context.Context) (*mcp.Message, error) {
	if m.recvErr != nil {
		return nil, m.recvErr
	}
	return &mcp.Message{}, nil
}

func (m *mockTransport) Close() error {
	m.closed.Store(true)
	return nil
}

// =============================================================================
// Constructor Tests
// =============================================================================

func TestNew_DefaultConfig(t *testing.T) {
	r := New(Config{})

	if r.failureThreshold != 3 {
		t.Errorf("expected default failureThreshold=3, got %d", r.failureThreshold)
	}
	if r.recoveryTime != 30*time.Second {
		t.Errorf("expected default recoveryTime=30s, got %v", r.recoveryTime)
	}
}

func TestNew_CustomConfig(t *testing.T) {
	r := New(Config{
		FailureThreshold: 5,
		RecoveryTime:     time.Minute,
		HubEnabled:       true,
		HubURL:           "wss://example.com",
	})

	if r.failureThreshold != 5 {
		t.Errorf("expected failureThreshold=5, got %d", r.failureThreshold)
	}
	if r.recoveryTime != time.Minute {
		t.Errorf("expected recoveryTime=1m, got %v", r.recoveryTime)
	}
	if !r.hubEnabled {
		t.Error("expected hubEnabled=true")
	}
}

func TestNew_InitializesHealthForAllServers(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
		"server2": {"monitoring"},
		"server3": {"local-only"},
	})

	r := New(Config{Registry: reg})

	for _, srv := range []string{"server1", "server2", "server3"} {
		if r.localHealth[srv] == nil {
			t.Errorf("expected local health initialized for %s", srv)
		}
		if r.hubHealth[srv] == nil {
			t.Errorf("expected hub health initialized for %s", srv)
		}
		if !r.localHealth[srv].Healthy {
			t.Errorf("expected %s to start healthy", srv)
		}
	}
}

// =============================================================================
// Route Decision Tests
// =============================================================================

func TestRoute_UnknownServer(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"known-server": {},
	})
	r := New(Config{Registry: reg})

	decision, err := r.Route(context.Background(), "unknown-server")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Target != TargetUnavailable {
		t.Errorf("expected TargetUnavailable, got %v", decision.Target)
	}
	if decision.Reason != "server not in registry" {
		t.Errorf("unexpected reason: %s", decision.Reason)
	}
}

func TestRoute_LocalHealthyPreferred(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
	})
	r := New(Config{Registry: reg, HubEnabled: true})

	decision, err := r.Route(context.Background(), "server1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Target != TargetLocal {
		t.Errorf("expected TargetLocal, got %v", decision.Target)
	}
	if decision.Reason != "local healthy" {
		t.Errorf("unexpected reason: %s", decision.Reason)
	}
}

func TestRoute_HubFallbackWhenLocalUnhealthy(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
	})
	r := New(Config{Registry: reg, HubEnabled: true, FailureThreshold: 1})

	// Make local unhealthy
	r.RecordFailure("server1", TargetLocal, errors.New("connection failed"))

	decision, err := r.Route(context.Background(), "server1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Target != TargetHub {
		t.Errorf("expected TargetHub, got %v", decision.Target)
	}
	if decision.Reason == "" {
		t.Error("expected reason to be set")
	}
}

func TestRoute_UnavailableWhenBothDown(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
	})
	r := New(Config{Registry: reg, HubEnabled: true, FailureThreshold: 1})

	// Make both unhealthy
	r.RecordFailure("server1", TargetLocal, errors.New("local failed"))
	r.RecordFailure("server1", TargetHub, errors.New("hub failed"))

	decision, err := r.Route(context.Background(), "server1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if decision.Target != TargetUnavailable {
		t.Errorf("expected TargetUnavailable, got %v", decision.Target)
	}
}

func TestRoute_LocalOnlyConstraint(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"filesystem-server": {"filesystem"},
		"local-only-server": {"local-only"},
	})
	r := New(Config{Registry: reg, HubEnabled: true})

	tests := []struct {
		name   string
		server string
	}{
		{"filesystem category", "filesystem-server"},
		{"local-only category", "local-only-server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, _ := r.Route(context.Background(), tt.server)
			if decision.Target != TargetLocal {
				t.Errorf("expected TargetLocal for local-only server, got %v", decision.Target)
			}
			if decision.Reason != "local-only server" {
				t.Errorf("unexpected reason: %s", decision.Reason)
			}
		})
	}
}

func TestRoute_LocalOnlyUnavailableWhenDown(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"local-server": {"local-only"},
	})
	r := New(Config{Registry: reg, HubEnabled: true, FailureThreshold: 1})

	// Make local unhealthy
	r.RecordFailure("local-server", TargetLocal, errors.New("local failed"))

	decision, _ := r.Route(context.Background(), "local-server")
	if decision.Target != TargetUnavailable {
		t.Errorf("expected TargetUnavailable for unhealthy local-only, got %v", decision.Target)
	}
}

func TestRoute_HubDisabled(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
	})
	r := New(Config{Registry: reg, HubEnabled: false, FailureThreshold: 1})

	// Make local unhealthy
	r.RecordFailure("server1", TargetLocal, errors.New("local failed"))

	decision, _ := r.Route(context.Background(), "server1")
	if decision.Target != TargetUnavailable {
		t.Errorf("expected TargetUnavailable when hub disabled, got %v", decision.Target)
	}
}

// =============================================================================
// Health Recording Tests
// =============================================================================

func TestRecordSuccess_ResetsFailures(t *testing.T) {
	r := New(Config{FailureThreshold: 3})

	// Record some failures first
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))

	// Record success
	r.RecordSuccess("server1", TargetLocal, 100)

	local, _ := r.GetHealth("server1")
	if local.ConsecFails != 0 {
		t.Errorf("expected ConsecFails=0 after success, got %d", local.ConsecFails)
	}
	if !local.Healthy {
		t.Error("expected healthy=true after success")
	}
	if local.ErrorMessage != "" {
		t.Errorf("expected empty error message after success, got %s", local.ErrorMessage)
	}
}

func TestRecordSuccess_UpdatesLatency(t *testing.T) {
	r := New(Config{})

	// First latency
	r.RecordSuccess("server1", TargetLocal, 100)
	local, _ := r.GetHealth("server1")
	if local.AvgLatencyMs != 100 {
		t.Errorf("expected initial latency=100, got %f", local.AvgLatencyMs)
	}

	// Second latency with EMA
	r.RecordSuccess("server1", TargetLocal, 200)
	local, _ = r.GetHealth("server1")
	// EMA: 100*0.8 + 200*0.2 = 80 + 40 = 120
	if local.AvgLatencyMs != 120 {
		t.Errorf("expected EMA latency=120, got %f", local.AvgLatencyMs)
	}
}

func TestRecordFailure_IncrementsCount(t *testing.T) {
	r := New(Config{FailureThreshold: 5})

	r.RecordFailure("server1", TargetLocal, errors.New("fail 1"))
	r.RecordFailure("server1", TargetLocal, errors.New("fail 2"))
	r.RecordFailure("server1", TargetLocal, errors.New("fail 3"))

	local, _ := r.GetHealth("server1")
	if local.ConsecFails != 3 {
		t.Errorf("expected ConsecFails=3, got %d", local.ConsecFails)
	}
	if local.ErrorMessage != "fail 3" {
		t.Errorf("expected last error message, got %s", local.ErrorMessage)
	}
}

func TestRecordFailure_MarksUnhealthyAtThreshold(t *testing.T) {
	// Use a registry so health is pre-initialized as healthy
	reg := createTestRegistry(map[string][]string{
		"server1": {},
	})
	r := New(Config{Registry: reg, FailureThreshold: 2})

	// First failure - still healthy
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))
	local, _ := r.GetHealth("server1")
	if !local.Healthy {
		t.Error("expected still healthy after 1 failure")
	}

	// Second failure - now unhealthy
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))
	local, _ = r.GetHealth("server1")
	if local.Healthy {
		t.Error("expected unhealthy at threshold")
	}
}

func TestRecordSuccess_HubTarget(t *testing.T) {
	r := New(Config{})

	r.RecordSuccess("server1", TargetHub, 50)

	_, hub := r.GetHealth("server1")
	if hub == nil {
		t.Fatal("expected hub health to be recorded")
	}
	if !hub.Healthy {
		t.Error("expected hub healthy")
	}
}

func TestRecordFailure_HubTarget(t *testing.T) {
	r := New(Config{FailureThreshold: 1})

	r.RecordFailure("server1", TargetHub, errors.New("hub fail"))

	_, hub := r.GetHealth("server1")
	if hub == nil {
		t.Fatal("expected hub health to be recorded")
	}
	if hub.Healthy {
		t.Error("expected hub unhealthy")
	}
}

// =============================================================================
// Circuit Breaker Tests
// =============================================================================

func TestIsHealthy_CircuitBreakerOpen(t *testing.T) {
	r := New(Config{FailureThreshold: 2, RecoveryTime: time.Hour})

	// Trip the circuit
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))

	local, _ := r.GetHealth("server1")
	if r.isHealthy(local) {
		t.Error("expected circuit to be open (unhealthy)")
	}
}

func TestIsHealthy_CircuitBreakerHalfOpen(t *testing.T) {
	r := New(Config{FailureThreshold: 2, RecoveryTime: 10 * time.Millisecond})

	// Trip the circuit
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))
	r.RecordFailure("server1", TargetLocal, errors.New("fail"))

	// Wait for recovery time
	time.Sleep(20 * time.Millisecond)

	// Manually set healthy back to test half-open state
	r.mu.Lock()
	r.localHealth["server1"].Healthy = true
	r.mu.Unlock()

	local, _ := r.GetHealth("server1")
	if !r.isHealthy(local) {
		t.Error("expected circuit to be half-open (allow retry)")
	}
}

func TestIsHealthy_NilHealth(t *testing.T) {
	r := New(Config{})

	if r.isHealthy(nil) {
		t.Error("expected nil health to be unhealthy")
	}
}

// =============================================================================
// GetHealth Tests
// =============================================================================

func TestGetHealth_ReturnsCorrectStatus(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
	})
	// Use FailureThreshold: 1 so a single failure marks unhealthy
	r := New(Config{Registry: reg, FailureThreshold: 1})

	r.RecordSuccess("server1", TargetLocal, 100)
	r.RecordFailure("server1", TargetHub, errors.New("hub fail"))

	local, hub := r.GetHealth("server1")

	if local == nil || !local.Healthy {
		t.Error("expected local healthy")
	}
	if hub == nil || hub.Healthy {
		t.Error("expected hub unhealthy")
	}
}

func TestGetAllHealth_ReturnsAllServers(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
		"server2": {},
		"server3": {},
	})
	r := New(Config{Registry: reg})

	all := r.GetAllHealth()

	if len(all) != 3 {
		t.Errorf("expected 3 servers, got %d", len(all))
	}

	for _, name := range []string{"server1", "server2", "server3"} {
		if _, ok := all[name]; !ok {
			t.Errorf("expected %s in health map", name)
		}
	}
}

// =============================================================================
// Proxy Tests
// =============================================================================

func TestProxy_SendRecordsLatency(t *testing.T) {
	r := New(Config{})
	transport := &mockTransport{sendDelay: 10 * time.Millisecond}
	proxy := NewProxy(r, transport, "server1", TargetLocal)

	err := proxy.Send(context.Background(), &mcp.Message{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	local, _ := r.GetHealth("server1")
	if local.AvgLatencyMs < 10 {
		t.Errorf("expected latency >= 10ms, got %f", local.AvgLatencyMs)
	}
}

func TestProxy_SendRecordsFailure(t *testing.T) {
	r := New(Config{FailureThreshold: 1})
	transport := &mockTransport{sendErr: errors.New("send failed")}
	proxy := NewProxy(r, transport, "server1", TargetLocal)

	err := proxy.Send(context.Background(), &mcp.Message{})
	if err == nil {
		t.Fatal("expected error")
	}

	local, _ := r.GetHealth("server1")
	if local.Healthy {
		t.Error("expected unhealthy after send failure")
	}
}

func TestProxy_RecvRecordsFailure(t *testing.T) {
	r := New(Config{FailureThreshold: 1})
	transport := &mockTransport{recvErr: errors.New("recv failed")}
	proxy := NewProxy(r, transport, "server1", TargetLocal)

	_, err := proxy.Recv(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}

	local, _ := r.GetHealth("server1")
	if local.Healthy {
		t.Error("expected unhealthy after recv failure")
	}
}

func TestProxy_ClosesDelegates(t *testing.T) {
	r := New(Config{})
	transport := &mockTransport{}
	proxy := NewProxy(r, transport, "server1", TargetLocal)

	proxy.Close()

	if !transport.closed.Load() {
		t.Error("expected underlying transport to be closed")
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestRoute_ConcurrentCalls(t *testing.T) {
	reg := createTestRegistry(map[string][]string{
		"server1": {},
		"server2": {},
	})
	r := New(Config{Registry: reg, HubEnabled: true})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			server := "server1"
			if i%2 == 0 {
				server = "server2"
			}
			r.Route(context.Background(), server)
		}(i)
	}
	wg.Wait()
}

func TestRecordSuccess_ConcurrentUpdates(t *testing.T) {
	r := New(Config{})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.RecordSuccess("server1", TargetLocal, float64(i))
		}(i)
	}
	wg.Wait()

	local, _ := r.GetHealth("server1")
	if local == nil {
		t.Fatal("expected health to be recorded")
	}
	if !local.Healthy {
		t.Error("expected healthy after successes")
	}
}

func TestRecordMixed_ConcurrentUpdates(t *testing.T) {
	r := New(Config{FailureThreshold: 50})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				r.RecordSuccess("server1", TargetLocal, 100)
			} else {
				r.RecordFailure("server1", TargetLocal, errors.New("fail"))
			}
		}(i)
	}
	wg.Wait()

	// Just verify no panic/race - state depends on execution order
	local, _ := r.GetHealth("server1")
	if local == nil {
		t.Fatal("expected health to be recorded")
	}
}

// =============================================================================
// Target String Tests
// =============================================================================

func TestTarget_String(t *testing.T) {
	tests := []struct {
		target Target
		want   string
	}{
		{TargetLocal, "local"},
		{TargetHub, "hub"},
		{TargetUnavailable, "unavailable"},
		{Target(99), "unavailable"}, // Unknown value
	}

	for _, tt := range tests {
		if got := tt.target.String(); got != tt.want {
			t.Errorf("Target(%d).String() = %q, want %q", tt.target, got, tt.want)
		}
	}
}
