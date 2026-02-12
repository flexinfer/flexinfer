package k8surl

import (
	"fmt"
	"os"
	"strings"
)

const (
	// EnvClusterDomain overrides the Kubernetes service cluster domain.
	EnvClusterDomain = "K8S_CLUSTER_DOMAIN"
	// DefaultClusterDomain is the standard kube-dns domain suffix.
	DefaultClusterDomain = "cluster.local"
)

// ClusterDomain returns the configured cluster domain.
func ClusterDomain() string {
	if v := strings.TrimSpace(os.Getenv(EnvClusterDomain)); v != "" {
		return v
	}
	return DefaultClusterDomain
}

// ServiceHost returns the service DNS host.
// If fullyQualified is true, returns "<name>.<namespace>.svc.<cluster-domain>".
// Otherwise, returns "<name>.<namespace>.svc".
func ServiceHost(name, namespace string, fullyQualified bool) string {
	svcName := strings.TrimSpace(name)
	ns := strings.TrimSpace(namespace)
	if fullyQualified {
		return fmt.Sprintf("%s.%s.svc.%s", svcName, ns, ClusterDomain())
	}
	return fmt.Sprintf("%s.%s.svc", svcName, ns)
}

// ServiceAddress returns "<service-host>:<port>".
func ServiceAddress(name, namespace string, port int32, fullyQualified bool) string {
	return fmt.Sprintf("%s:%d", ServiceHost(name, namespace, fullyQualified), port)
}

// ServiceURL returns "http://<service-host>:<port>".
func ServiceURL(name, namespace string, port int32, fullyQualified bool) string {
	return "http://" + ServiceAddress(name, namespace, port, fullyQualified)
}
