package benchmarkconfig

import (
	"net/http"
	"testing"
)

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

func TestApplyWorkloadClass(t *testing.T) {
	t.Run("background", func(t *testing.T) {
		t.Setenv(EnvWorkloadClass, " BACKGROUND ")
		req, err := http.NewRequest(http.MethodGet, "http://example.test", nil)
		if err != nil {
			t.Fatal(err)
		}
		ApplyWorkloadClass(req)

		if got := req.Header.Get(HeaderInternalWorkloadClass); got != WorkloadClassBackground {
			t.Fatalf("workload header = %q, want %q", got, WorkloadClassBackground)
		}
	})

	t.Run("unsupported value", func(t *testing.T) {
		t.Setenv(EnvWorkloadClass, "foreground")
		req, err := http.NewRequest(http.MethodGet, "http://example.test", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(HeaderInternalWorkloadClass, WorkloadClassBackground)

		ApplyWorkloadClass(req)

		if got := req.Header.Get(HeaderInternalWorkloadClass); got != "" {
			t.Fatalf("workload header = %q, want empty", got)
		}
	})
}
