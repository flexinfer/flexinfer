package daemon

import "testing"

func TestComputeHealthDivergence(t *testing.T) {
	cases := []struct {
		name            string
		monitor         *ServerHealthStatus
		routerAvailable bool
		expectNil       bool
		expectReason    string
	}{
		{
			name:            "nil monitor",
			monitor:         nil,
			routerAvailable: true,
			expectNil:       true,
		},
		{
			name:            "agree healthy",
			monitor:         &ServerHealthStatus{Healthy: true},
			routerAvailable: true,
			expectNil:       true,
		},
		{
			name:            "agree unhealthy",
			monitor:         &ServerHealthStatus{Healthy: false},
			routerAvailable: false,
			expectNil:       true,
		},
		{
			name:            "monitor healthy router unavailable",
			monitor:         &ServerHealthStatus{Healthy: true},
			routerAvailable: false,
			expectReason:    "monitor_healthy_router_unavailable",
		},
		{
			name:            "monitor unhealthy router available",
			monitor:         &ServerHealthStatus{Healthy: false},
			routerAvailable: true,
			expectReason:    "monitor_unhealthy_router_available",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result := computeHealthDivergence(tc.monitor, tc.routerAvailable)
			if tc.expectNil {
				if result != nil {
					t.Fatalf("expected nil divergence, got %+v", result)
				}
				return
			}
			if result == nil {
				t.Fatal("expected divergence, got nil")
			}
			if result.Reason != tc.expectReason {
				t.Fatalf("expected reason %q, got %q", tc.expectReason, result.Reason)
			}
		})
	}
}
