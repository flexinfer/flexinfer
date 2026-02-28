package monitor

import "testing"

func TestClassifyHealthEntry(t *testing.T) {
	tests := []struct {
		name  string
		entry ServerHealthEntry
		want  healthClass
	}{
		{
			name: "hub healthy server with local process stopped is healthy",
			entry: ServerHealthEntry{
				Running:     false,
				Target:      "hub",
				Healthy:     true,
				ConsecFails: 0,
			},
			want: healthClassHealthy,
		},
		{
			name: "hub target unhealthy with minor failures is degraded",
			entry: ServerHealthEntry{
				Running:     false,
				Target:      "hub",
				Healthy:     false,
				ConsecFails: 1,
			},
			want: healthClassDegraded,
		},
		{
			name: "local stopped and unavailable is idle",
			entry: ServerHealthEntry{
				Running:     false,
				Target:      "unavailable",
				Healthy:     false,
				ConsecFails: 0,
			},
			want: healthClassIdle,
		},
		{
			name: "running server with unavailable target is down",
			entry: ServerHealthEntry{
				Running:     true,
				Target:      "unavailable",
				Healthy:     false,
				ConsecFails: 0,
			},
			want: healthClassDown,
		},
		{
			name: "sustained failures are down",
			entry: ServerHealthEntry{
				Running:     true,
				Target:      "local",
				Healthy:     false,
				ConsecFails: 4,
			},
			want: healthClassDown,
		},
		{
			name: "single failure is degraded",
			entry: ServerHealthEntry{
				Running:     true,
				Target:      "local",
				Healthy:     false,
				ConsecFails: 1,
			},
			want: healthClassDegraded,
		},
		{
			name: "healthy local server is healthy",
			entry: ServerHealthEntry{
				Running:     true,
				Target:      "local",
				Healthy:     true,
				ConsecFails: 0,
			},
			want: healthClassHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyHealthEntry(tt.entry)
			if got != tt.want {
				t.Fatalf("classifyHealthEntry() = %q, want %q", got, tt.want)
			}
		})
	}
}
