package benchmarkconfig

import (
	"net/http"
	"os"
	"strings"

	"github.com/flexinfer/flexinfer/pkg/k8surl"
)

const (
	// EnvBenchmarkResultsConfigMap overrides the global benchmark results ConfigMap name.
	EnvBenchmarkResultsConfigMap = "BENCHMARK_RESULTS_CONFIGMAP"
	// DefaultBenchmarkResultsConfigMap stores cross-device benchmark scores used by the scheduler.
	DefaultBenchmarkResultsConfigMap = "flexinfer-benchmark-results"
	// BenchmarkResultsSuffix is used for per-ModelDeployment benchmark result ConfigMaps.
	BenchmarkResultsSuffix = "-benchmark-results"

	// EnvProxyURL configures the benchmark client target URL.
	EnvProxyURL = "PROXY_URL"
	// EnvBenchmarkProxyURL overrides the default proxy service URL for benchmark jobs.
	EnvBenchmarkProxyURL = "BENCHMARK_PROXY_URL"
	// EnvBenchmarkProxyNamespace overrides the namespace used for default proxy URL construction.
	EnvBenchmarkProxyNamespace = "BENCHMARK_PROXY_NAMESPACE"

	// Default proxy service coordinates benchmark traffic through the activator path.
	DefaultProxyServiceName      = "flexinfer-proxy"
	DefaultProxyServiceNamespace = "flexinfer-system"
	DefaultProxyServicePort      = 80

	// EnvWorkloadClass selects the internal request class used by benchmark and
	// gauntlet clients. Only "background" has special behavior; unset and all
	// other values preserve normal foreground demand semantics.
	EnvWorkloadClass = "FLEXINFER_WORKLOAD_CLASS"
	// HeaderInternalWorkloadClass carries the request class to flexinfer-proxy.
	// The proxy always strips it before forwarding to a model backend.
	HeaderInternalWorkloadClass = "X-FlexInfer-Internal-Workload-Class"
	// WorkloadClassBackground marks work that may use an already-warm model but
	// must never create cold-start demand.
	WorkloadClassBackground = "background"
)

// GlobalResultsConfigMapName returns the global benchmark results ConfigMap name.
func GlobalResultsConfigMapName() string {
	return envOrDefault(EnvBenchmarkResultsConfigMap, DefaultBenchmarkResultsConfigMap)
}

// DeploymentResultsConfigMapName returns the per-ModelDeployment results ConfigMap name.
func DeploymentResultsConfigMapName(modelDeploymentName string) string {
	return strings.TrimSpace(modelDeploymentName) + BenchmarkResultsSuffix
}

// DefaultProxyURL returns the default in-cluster proxy URL for benchmark traffic.
func DefaultProxyURL(namespace string) string {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = DefaultProxyServiceNamespace
	}
	return k8surl.ServiceURL(DefaultProxyServiceName, ns, DefaultProxyServicePort, false)
}

// ProxyURL returns the benchmark proxy URL from env, then falls back to the default service URL.
func ProxyURL() string {
	if v := strings.TrimSpace(os.Getenv(EnvProxyURL)); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv(EnvBenchmarkProxyURL)); v != "" {
		return v
	}
	return DefaultProxyURL(os.Getenv(EnvBenchmarkProxyNamespace))
}

// ApplyWorkloadClass marks req as background work when requested by the
// benchmark process environment. Unsupported values are ignored so a typo can
// never accidentally opt traffic out of normal foreground demand handling.
func ApplyWorkloadClass(req *http.Request) {
	if req == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv(EnvWorkloadClass)), WorkloadClassBackground) {
		req.Header.Set(HeaderInternalWorkloadClass, WorkloadClassBackground)
		return
	}
	req.Header.Del(HeaderInternalWorkloadClass)
}

func envOrDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}
