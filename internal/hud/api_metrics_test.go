package hud

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDaemonMetrics_Empty(t *testing.T) {
	result := parseDaemonMetrics("")
	assert.Empty(t, result.Servers)
}

func TestParseDaemonMetrics_RequestCounts(t *testing.T) {
	body := `# HELP loom_daemon_requests_total Total daemon requests
# TYPE loom_daemon_requests_total counter
loom_daemon_requests_total{server="github",method="tools/call",status="ok"} 42
loom_daemon_requests_total{server="github",method="tools/call",status="error"} 3
loom_daemon_requests_total{server="slack",method="tools/call",status="ok"} 10
`
	result := parseDaemonMetrics(body)
	require.Len(t, result.Servers, 2)

	// Sorted by name
	assert.Equal(t, "github", result.Servers[0].Name)
	assert.Equal(t, float64(45), result.Servers[0].RequestCount)
	assert.Equal(t, float64(3), result.Servers[0].ErrorCount)
	assert.InDelta(t, 3.0/45.0, result.Servers[0].ErrorRate, 0.001)

	assert.Equal(t, "slack", result.Servers[1].Name)
	assert.Equal(t, float64(10), result.Servers[1].RequestCount)
	assert.Equal(t, float64(0), result.Servers[1].ErrorCount)
}

func TestParseDaemonMetrics_Histogram(t *testing.T) {
	body := `# TYPE loom_daemon_request_duration_seconds_bucket histogram
loom_daemon_request_duration_seconds_bucket{server="test",method="tools/call",target="local",le="0.001"} 5
loom_daemon_request_duration_seconds_bucket{server="test",method="tools/call",target="local",le="0.01"} 40
loom_daemon_request_duration_seconds_bucket{server="test",method="tools/call",target="local",le="0.1"} 90
loom_daemon_request_duration_seconds_bucket{server="test",method="tools/call",target="local",le="1"} 100
loom_daemon_request_duration_seconds_bucket{server="test",method="tools/call",target="local",le="+Inf"} 100
loom_daemon_request_duration_seconds_count{server="test",method="tools/call",target="local"} 100
`
	result := parseDaemonMetrics(body)
	require.Len(t, result.Servers, 1)

	s := result.Servers[0]
	assert.Equal(t, "test", s.Name)

	// p50 should be within the 0.001-0.01 bucket (50th of 100 = count 50, bucket at le=0.01 has 40)
	// Actually: bucket 0.001 has 5, bucket 0.01 has 40. target=50. p50 is in bucket 0.01-0.1.
	assert.True(t, s.P50Ms > 0 && s.P50Ms < 100, "p50 should be reasonable: got %f", s.P50Ms)
	assert.True(t, s.P95Ms > s.P50Ms, "p95 should be > p50")
	assert.True(t, s.P99Ms >= s.P95Ms, "p99 should be >= p95")
}

func TestParseDaemonMetrics_InFlight(t *testing.T) {
	body := `loom_daemon_requests_in_flight{server="test"} 3
`
	result := parseDaemonMetrics(body)
	require.Len(t, result.Servers, 1)
	assert.Equal(t, float64(3), result.Servers[0].InFlight)
}

func TestParsePrometheusLine(t *testing.T) {
	tests := []struct {
		line       string
		wantName   string
		wantLabels map[string]string
		wantVal    float64
	}{
		{
			line:       `loom_daemon_requests_total{server="gh",status="ok"} 42`,
			wantName:   "loom_daemon_requests_total",
			wantLabels: map[string]string{"server": "gh", "status": "ok"},
			wantVal:    42,
		},
		{
			line:     `go_goroutines 15`,
			wantName: "go_goroutines",
			wantVal:  15,
		},
		{
			line:     `# comment`,
			wantName: "",
		},
	}

	for _, tt := range tests {
		name, labels, val := parsePrometheusLine(tt.line)
		assert.Equal(t, tt.wantName, name, "line: %s", tt.line)
		if tt.wantLabels != nil {
			for k, v := range tt.wantLabels {
				assert.Equal(t, v, labels[k], "label %s in line: %s", k, tt.line)
			}
		}
		if tt.wantName != "" {
			assert.Equal(t, tt.wantVal, val, "value in line: %s", tt.line)
		}
	}
}

func TestHistogramPercentile_Empty(t *testing.T) {
	assert.Equal(t, 0.0, histogramPercentile(nil, 0, 0.5))
}

func TestHistogramPercentile_Simple(t *testing.T) {
	buckets := []histBucket{
		{le: 0.1, count: 50},
		{le: 1.0, count: 100},
	}
	p50 := histogramPercentile(buckets, 100, 0.50)
	assert.True(t, p50 >= 0 && p50 <= 0.1, "p50 should be in first bucket: got %f", p50)
	assert.False(t, math.IsNaN(p50))

	p99 := histogramPercentile(buckets, 100, 0.99)
	assert.True(t, p99 > 0.1, "p99 should be in second bucket: got %f", p99)
}
