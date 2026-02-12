package benchmarkconfig

import "testing"

func TestDeploymentResultsConfigMapName(t *testing.T) {
	got := DeploymentResultsConfigMapName("example-model")
	want := "example-model-benchmark-results"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDefaultProxyURL(t *testing.T) {
	got := DefaultProxyURL("")
	want := "http://flexinfer-proxy.flexinfer-system.svc:80"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
