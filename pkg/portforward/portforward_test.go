package portforward

import "testing"

func TestNeedsPortForward(t *testing.T) {
	tests := []struct {
		host     string
		prefixes []string
		want     bool
	}{
		{"", nil, false},
		{"localhost", nil, false},
		{"127.0.0.1", nil, false},
		{"::1", nil, false},
		{"loki.logging.svc.cluster.local", nil, true},
		{"loki.logging.svc", nil, true},
		{"something.cluster.local", nil, true},
		{"loki", nil, true},                 // single-label = in-cluster
		{"grafana.example.com", nil, false}, // FQDN = external
		{"minio-service.ns.svc.cluster.local", nil, true},
		// Extra prefixes
		{"minio-service", []string{"minio-service"}, true},
		{"kube-prometheus-stack-grafana.monitoring.svc", []string{"kube-prometheus-stack-grafana"}, true},
		{"external.host.com", []string{"kube-prometheus-stack-grafana"}, false},
	}

	for _, tt := range tests {
		got := NeedsPortForward(tt.host, tt.prefixes)
		if got != tt.want {
			t.Errorf("NeedsPortForward(%q, %v) = %v, want %v", tt.host, tt.prefixes, got, tt.want)
		}
	}
}

func TestPortForwarder_DisabledIsNoop(t *testing.T) {
	pf := New(Config{
		Namespace:  "test",
		Service:    "svc/test",
		LocalPort:  8080,
		RemotePort: 80,
	}, false) // disabled

	url := "http://test.svc.cluster.local:80/path"
	got := pf.EnsureRunning(url)
	if got != url {
		t.Errorf("disabled PortForwarder should return original URL, got %q", got)
	}
}

func TestPortForwarder_ExternalHostNoForward(t *testing.T) {
	pf := New(Config{
		Namespace:  "test",
		Service:    "svc/test",
		LocalPort:  8080,
		RemotePort: 80,
	}, true)

	url := "http://grafana.example.com/path"
	got := pf.EnsureRunning(url)
	if got != url {
		t.Errorf("external host should return original URL, got %q", got)
	}
}

func TestPortForwarder_Cleanup_NilCmd(t *testing.T) {
	pf := New(Config{}, true)
	// Should not panic
	pf.Cleanup()
}
