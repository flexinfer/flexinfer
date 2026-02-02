package daemon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewMetrics(t *testing.T) {
	m := NewMetrics()

	if m == nil {
		t.Fatal("NewMetrics returned nil")
	}
	if m.registry == nil {
		t.Error("registry should not be nil")
	}
	if m.RequestsTotal == nil {
		t.Error("RequestsTotal should not be nil")
	}
	if m.RequestDuration == nil {
		t.Error("RequestDuration should not be nil")
	}
	if m.RequestsInFlight == nil {
		t.Error("RequestsInFlight should not be nil")
	}
	if m.ServerHealth == nil {
		t.Error("ServerHealth should not be nil")
	}
	if m.ToolCacheSize == nil {
		t.Error("ToolCacheSize should not be nil")
	}
}

func TestMetrics_Handler(t *testing.T) {
	m := NewMetrics()

	handler := m.Handler()
	if handler == nil {
		t.Fatal("Handler returned nil")
	}

	// Test that handler serves metrics
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Handler returned status %d, want 200", rec.Code)
	}

	body, _ := io.ReadAll(rec.Body)
	bodyStr := string(body)

	// Should contain some metric names
	if !strings.Contains(bodyStr, "loom_daemon") {
		t.Error("response should contain loom_daemon metrics")
	}
}

func TestMetrics_RecordRequest(t *testing.T) {
	m := NewMetrics()

	// Should not panic
	m.RecordRequest("test-server", "tools/call", "success", "local", 100*time.Millisecond)
	m.RecordRequest("test-server", "tools/list", "error", "hub", 500*time.Millisecond)
}

func TestMetrics_RecordRequestStartEnd(t *testing.T) {
	m := NewMetrics()

	// Start a request
	m.RecordRequestStart("test-server")
	m.RecordRequestStart("test-server")

	// End requests
	m.RecordRequestEnd("test-server")
	m.RecordRequestEnd("test-server")
}

func TestMetrics_UpdateServerHealth(t *testing.T) {
	m := NewMetrics()

	// Update health status
	m.UpdateServerHealth("server1", "local", true, 25.5)
	m.UpdateServerHealth("server2", "hub", false, 0)
}

func TestMetrics_RecordServerFailureSuccess(t *testing.T) {
	m := NewMetrics()

	m.RecordServerFailure("server1", "local", "timeout")
	m.RecordServerFailure("server1", "local", "connection_error")
	m.RecordServerSuccess("server1", "local")
	m.RecordServerSuccess("server2", "hub")
}

func TestMetrics_UpdatePoolStats(t *testing.T) {
	m := NewMetrics()

	m.UpdatePoolStats("main-pool", 10, 5)
	m.UpdatePoolStats("main-pool", 8, 7)
}

func TestMetrics_UpdateProcessCount(t *testing.T) {
	m := NewMetrics()

	m.UpdateProcessCount(5)
	m.UpdateProcessCount(10)
	m.UpdateProcessCount(3)
}

func TestMetrics_RecordProcessRestart(t *testing.T) {
	m := NewMetrics()

	m.RecordProcessRestart("server1")
	m.RecordProcessRestart("server1")
	m.RecordProcessRestart("server2")
}

func TestMetrics_UpdateToolCache(t *testing.T) {
	m := NewMetrics()

	m.UpdateToolCache(100, 30*time.Second)
	m.UpdateToolCache(150, 2*time.Minute)
}

func TestMetrics_RecordToolCacheOperations(t *testing.T) {
	m := NewMetrics()

	m.RecordToolCacheHit()
	m.RecordToolCacheHit()
	m.RecordToolCacheMiss()
	m.RecordToolCacheRefresh()
}

func TestMetrics_RecordResponseCacheOperations(t *testing.T) {
	m := NewMetrics()

	m.RecordResponseCacheHit("prometheus", "query")
	m.RecordResponseCacheHit("github", "list_repos")
	m.RecordResponseCacheMiss("docker", "ps")
	m.UpdateResponseCacheStats(100, 1024*1024)
	m.RecordResponseCacheEviction()
}

func TestMetrics_UpdateHubConnection(t *testing.T) {
	m := NewMetrics()

	m.UpdateHubConnection(true, 50.0)
	m.UpdateHubConnection(false, 0)
}

func TestMetrics_RecordHubOperations(t *testing.T) {
	m := NewMetrics()

	m.RecordHubRequest("tools/list", "success")
	m.RecordHubRequest("tools/call", "error")
	m.RecordHubFailure()
	m.RecordHubFailure()
}

func TestMetrics_AllMetricsRegistered(t *testing.T) {
	m := NewMetrics()

	// The metrics should be non-nil after creation
	// This verifies the registration doesn't panic
	if m.RequestsTotal == nil {
		t.Error("RequestsTotal not registered")
	}
	if m.RequestDuration == nil {
		t.Error("RequestDuration not registered")
	}
	if m.RequestsInFlight == nil {
		t.Error("RequestsInFlight not registered")
	}
	if m.ServerHealth == nil {
		t.Error("ServerHealth not registered")
	}
	if m.ServerLatency == nil {
		t.Error("ServerLatency not registered")
	}
	if m.PoolConnections == nil {
		t.Error("PoolConnections not registered")
	}
	if m.ProcessCount == nil {
		t.Error("ProcessCount not registered")
	}
	if m.ToolCacheSize == nil {
		t.Error("ToolCacheSize not registered")
	}
	if m.HubConnected == nil {
		t.Error("HubConnected not registered")
	}
	if m.ResponseCacheHits == nil {
		t.Error("ResponseCacheHits not registered")
	}
	if m.ResponseCacheMisses == nil {
		t.Error("ResponseCacheMisses not registered")
	}
	if m.ResponseCacheSize == nil {
		t.Error("ResponseCacheSize not registered")
	}
	if m.ResponseCacheEntries == nil {
		t.Error("ResponseCacheEntries not registered")
	}
	if m.ResponseCacheEvicts == nil {
		t.Error("ResponseCacheEvicts not registered")
	}
	if m.registry == nil {
		t.Error("registry not created")
	}
}

func TestMetrics_ConcurrentAccess(t *testing.T) {
	m := NewMetrics()

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				m.RecordRequest("server", "method", "success", "local", time.Millisecond)
				m.RecordRequestStart("server")
				m.RecordRequestEnd("server")
				m.UpdateServerHealth("server", "local", true, 10.0)
				m.RecordToolCacheHit()
			}
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
