package weaver

import "testing"

func TestMetrics_Summary(t *testing.T) {
	m := NewMetrics(nil) // nil registerer for testing
	m.RecordQuery("ok", 100, 50)
	m.RecordQuery("ok", 200, 75)
	m.RecordQuery("error", 300, 0)

	s := m.Summary()
	if s["total_queries"].(int64) != 3 {
		t.Errorf("expected 3 total queries, got %v", s["total_queries"])
	}
	if s["error_count"].(int64) != 1 {
		t.Errorf("expected 1 error, got %v", s["error_count"])
	}
	if s["total_tokens"].(int64) != 125 {
		t.Errorf("expected 125 total tokens, got %v", s["total_tokens"])
	}
	avgLatency := s["avg_latency_ms"].(float64)
	if avgLatency != 200.0 {
		t.Errorf("expected avg latency 200.0, got %v", avgLatency)
	}
	errorRate := s["error_rate"].(float64)
	expectedRate := 1.0 / 3.0
	if errorRate < expectedRate-0.001 || errorRate > expectedRate+0.001 {
		t.Errorf("expected error rate ~%v, got %v", expectedRate, errorRate)
	}
}

func TestMetrics_Summary_Empty(t *testing.T) {
	m := NewMetrics(nil)
	s := m.Summary()

	if s["total_queries"].(int64) != 0 {
		t.Errorf("expected 0 total queries, got %v", s["total_queries"])
	}
	if s["avg_latency_ms"].(float64) != 0 {
		t.Errorf("expected 0 avg latency, got %v", s["avg_latency_ms"])
	}
	if s["error_rate"].(float64) != 0 {
		t.Errorf("expected 0 error rate, got %v", s["error_rate"])
	}
}
