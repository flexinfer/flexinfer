package panels

import (
	"testing"
)

func TestFormatLatency(t *testing.T) {
	tests := []struct {
		ms   float64
		want string
	}{
		{0, "---"},
		{-1, "---"},
		{0.5, "<1ms"},
		{0.99, "<1ms"},
		{1, "1ms"},
		{42.6, "43ms"},
		{100, "100ms"},
		{999.5, "1000ms"},
	}
	for _, tt := range tests {
		got := formatLatency(tt.ms)
		if got != tt.want {
			t.Errorf("formatLatency(%v) = %q, want %q", tt.ms, got, tt.want)
		}
	}
}

func TestColorLatency(t *testing.T) {
	tests := []struct {
		name string
		text string
		ms   float64
	}{
		{"zero", "---", 0},
		{"fast", "5ms", 5},
		{"medium", "200ms", 200},
		{"slow", "800ms", 800},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := colorLatency(tt.text, tt.ms)
			if got == "" {
				t.Error("colorLatency returned empty string")
			}
		})
	}
}

func TestServerStatus(t *testing.T) {
	tests := []struct {
		name string
		data ServerData
		want string
	}{
		{"healthy", ServerData{Running: true, Healthy: true}, "healthy"},
		{"degraded", ServerData{Running: true, Healthy: false}, "degraded"},
		{"down not running", ServerData{Running: false, Healthy: false}, "down"},
		{"down not running but healthy flag set", ServerData{Running: false, Healthy: true}, "down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverStatus(tt.data)
			if got != tt.want {
				t.Errorf("serverStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSparkColor(t *testing.T) {
	tests := []struct {
		name string
		data ServerData
	}{
		{"healthy", ServerData{Running: true, Healthy: true}},
		{"degraded", ServerData{Running: true, Healthy: false}},
		{"down", ServerData{Running: false, Healthy: false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sparkColor(tt.data)
			if got == "" {
				t.Error("sparkColor returned empty color")
			}
		})
	}
}
