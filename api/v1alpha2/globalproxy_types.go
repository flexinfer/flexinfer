/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha2

import (
	"fmt"
	"net/url"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GlobalRoutingStrategy defines how global proxy requests are routed across clusters.
type GlobalRoutingStrategy string

const (
	// GlobalRoutingStrategyRoundRobin distributes requests across healthy clusters.
	GlobalRoutingStrategyRoundRobin GlobalRoutingStrategy = "RoundRobin"
	// GlobalRoutingStrategyFailover sends traffic to the first healthy cluster in failoverOrder.
	GlobalRoutingStrategyFailover GlobalRoutingStrategy = "Failover"
	// GlobalRoutingStrategyLatency routes to the healthy cluster with the lowest observed latency.
	GlobalRoutingStrategyLatency GlobalRoutingStrategy = "Latency"
	// GlobalRoutingStrategyWeighted distributes requests by cluster weights.
	GlobalRoutingStrategyWeighted GlobalRoutingStrategy = "Weighted"
)

// GlobalProxyClusterEndpoint configures one downstream cluster proxy endpoint.
// +kubebuilder:object:generate=true
type GlobalProxyClusterEndpoint struct {
	// Name is the logical cluster name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Endpoint is the cluster proxy URL.
	// Must be an absolute http(s) URL.
	// +kubebuilder:validation:Pattern=`^https?://`
	// +kubebuilder:validation:Required
	Endpoint string `json:"endpoint"`

	// Weight is reserved for future weighted routing.
	// +kubebuilder:validation:Minimum=1
	// +optional
	Weight *int32 `json:"weight,omitempty"`

	// GPUVendor optionally declares the primary GPU vendor for this cluster.
	// Used by GPU-aware routing hints in the global proxy.
	// +kubebuilder:validation:Enum=nvidia;amd;intel;cpu
	// +optional
	GPUVendor string `json:"gpuVendor,omitempty"`
}

// GlobalProxyTLSSpec configures TLS for the public global proxy endpoint.
// +kubebuilder:object:generate=true
type GlobalProxyTLSSpec struct {
	// SecretRef references the TLS secret in the same namespace.
	// +optional
	SecretRef *string `json:"secretRef,omitempty"`

	// InsecureSkipVerify controls downstream TLS certificate verification.
	// +optional
	InsecureSkipVerify bool `json:"insecureSkipVerify,omitempty"`
}

// GlobalProxySpec defines desired state for global cross-cluster routing.
// +kubebuilder:object:generate=true
type GlobalProxySpec struct {
	// ExternalEndpoint is the externally reachable hostname for this proxy.
	// +kubebuilder:validation:Required
	ExternalEndpoint string `json:"externalEndpoint"`

	// Strategy configures routing behavior.
	// +kubebuilder:validation:Enum=RoundRobin;Failover;Latency;Weighted
	// +kubebuilder:default=RoundRobin
	// +optional
	Strategy GlobalRoutingStrategy `json:"strategy,omitempty"`

	// TLS configures public and downstream TLS behavior.
	// +optional
	TLS *GlobalProxyTLSSpec `json:"tls,omitempty"`

	// Clusters are eligible downstream cluster proxy endpoints.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Required
	Clusters []GlobalProxyClusterEndpoint `json:"clusters"`

	// FailoverOrder is consulted only when strategy=Failover.
	// +optional
	FailoverOrder []string `json:"failoverOrder,omitempty"`
}

// ValidateBasic performs lightweight validation independent of controller state.
func (s GlobalProxySpec) ValidateBasic() error {
	if strings.TrimSpace(s.ExternalEndpoint) == "" {
		return fmt.Errorf("spec.externalEndpoint is required")
	}
	if strings.Contains(s.ExternalEndpoint, "://") {
		return fmt.Errorf("spec.externalEndpoint must be a hostname, not a URL")
	}
	if len(s.Clusters) == 0 {
		return fmt.Errorf("spec.clusters must include at least one cluster endpoint")
	}

	names := make(map[string]struct{}, len(s.Clusters))
	for _, c := range s.Clusters {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return fmt.Errorf("spec.clusters[].name is required")
		}
		if _, exists := names[name]; exists {
			return fmt.Errorf("spec.clusters has duplicate name %q", name)
		}
		names[name] = struct{}{}

		u, err := url.Parse(strings.TrimSpace(c.Endpoint))
		if err != nil {
			return fmt.Errorf("spec.clusters[%q].endpoint invalid: %w", name, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("spec.clusters[%q].endpoint must use http or https", name)
		}
		if u.Host == "" {
			return fmt.Errorf("spec.clusters[%q].endpoint host is required", name)
		}
		vendor := strings.ToLower(strings.TrimSpace(c.GPUVendor))
		if vendor != "" && vendor != "nvidia" && vendor != "amd" && vendor != "intel" && vendor != "cpu" {
			return fmt.Errorf("spec.clusters[%q].gpuVendor must be one of nvidia, amd, intel, cpu", name)
		}
	}

	strategy := s.Strategy
	if strategy == "" {
		strategy = GlobalRoutingStrategyRoundRobin
	}
	if strategy == GlobalRoutingStrategyFailover {
		if len(s.FailoverOrder) == 0 {
			return fmt.Errorf("spec.failoverOrder required when spec.strategy=Failover")
		}
		seen := make(map[string]struct{}, len(s.FailoverOrder))
		for _, name := range s.FailoverOrder {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				return fmt.Errorf("spec.failoverOrder entries must be non-empty")
			}
			if _, exists := names[trimmed]; !exists {
				return fmt.Errorf("spec.failoverOrder references unknown cluster %q", trimmed)
			}
			if _, dup := seen[trimmed]; dup {
				return fmt.Errorf("spec.failoverOrder has duplicate cluster %q", trimmed)
			}
			seen[trimmed] = struct{}{}
		}
	}
	if strategy != GlobalRoutingStrategyFailover && len(s.FailoverOrder) > 0 {
		return fmt.Errorf("spec.failoverOrder is only used when spec.strategy=Failover")
	}

	return nil
}

// GlobalProxyStatus reports observed routing and health state.
// +kubebuilder:object:generate=true
type GlobalProxyStatus struct {
	// ActiveCluster is the current primary cluster for failover strategy.
	// +optional
	ActiveCluster string `json:"activeCluster,omitempty"`

	// HealthyClusters is the number of healthy downstream clusters.
	// +optional
	HealthyClusters int32 `json:"healthyClusters,omitempty"`

	// TotalClusters is the configured number of downstream clusters.
	// +optional
	TotalClusters int32 `json:"totalClusters,omitempty"`

	// Conditions represent the latest observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=gproxy
//+kubebuilder:printcolumn:name="Strategy",type="string",JSONPath=".spec.strategy"
//+kubebuilder:printcolumn:name="Healthy",type="integer",JSONPath=".status.healthyClusters"
//+kubebuilder:printcolumn:name="Total",type="integer",JSONPath=".status.totalClusters"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GlobalProxy is the Schema for globally routing requests across cluster-local proxies.
type GlobalProxy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalProxySpec   `json:"spec,omitempty"`
	Status GlobalProxyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GlobalProxyList contains a list of GlobalProxy resources.
type GlobalProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalProxy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GlobalProxy{}, &GlobalProxyList{})
}
