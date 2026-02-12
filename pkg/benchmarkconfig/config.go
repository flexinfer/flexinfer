package benchmarkconfig

import (
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

func envOrDefault(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}
