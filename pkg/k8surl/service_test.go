package k8surl

import "testing"

func TestServiceHostAndURL(t *testing.T) {
	t.Setenv(EnvClusterDomain, "example.local")

	host := ServiceHost("model-a", "flexinfer-system", true)
	if host != "model-a.flexinfer-system.svc.example.local" {
		t.Fatalf("unexpected host: %s", host)
	}

	shortHost := ServiceHost("model-a", "flexinfer-system", false)
	if shortHost != "model-a.flexinfer-system.svc" {
		t.Fatalf("unexpected short host: %s", shortHost)
	}

	url := ServiceURL("model-a", "flexinfer-system", 8000, true)
	if url != "http://model-a.flexinfer-system.svc.example.local:8000" {
		t.Fatalf("unexpected url: %s", url)
	}
}
