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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterPhase captures the current health state of a registered cluster.
type ClusterPhase string

const (
	// ClusterPhasePending means the cluster is newly created and not yet probed.
	ClusterPhasePending ClusterPhase = "Pending"
	// ClusterPhaseReady means health probes succeeded and the cluster is reachable.
	ClusterPhaseReady ClusterPhase = "Ready"
	// ClusterPhaseNotReady means health probes failed.
	ClusterPhaseNotReady ClusterPhase = "NotReady"
	// ClusterPhaseUnknown means health could not be determined.
	ClusterPhaseUnknown ClusterPhase = "Unknown"
)

// ClusterModelStatus is an aggregated model status from the remote cluster.
// +kubebuilder:object:generate=true
type ClusterModelStatus struct {
	// Name is the model resource name.
	Name string `json:"name"`
	// Namespace is the model resource namespace in the remote cluster.
	Namespace string `json:"namespace"`
	// Phase is the remote model lifecycle phase.
	Phase string `json:"phase,omitempty"`
}

// ClusterSpec defines how to connect to and classify a remote cluster.
// +kubebuilder:object:generate=true
type ClusterSpec struct {
	// APIEndpoint is the Kubernetes API server URL for the remote cluster.
	// Must be an https URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://`
	APIEndpoint string `json:"apiEndpoint"`

	// SecretRef points to a kubeconfig secret in the same namespace.
	// +kubebuilder:validation:Required
	SecretRef corev1.LocalObjectReference `json:"secretRef"`

	// Labels are additional cluster metadata used for placement/routing policies.
	// Example: region, gpu-vendor, environment.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// ProbeInterval controls how often cluster health is checked.
	// +kubebuilder:default="30s"
	// +optional
	ProbeInterval *metav1.Duration `json:"probeInterval,omitempty"`
}

// NormalizedAPIEndpoint returns a canonical endpoint string for comparisons.
func (s ClusterSpec) NormalizedAPIEndpoint() string {
	return strings.TrimRight(strings.TrimSpace(s.APIEndpoint), "/")
}

// ValidateBasic performs lightweight, controller-agnostic spec checks.
func (s ClusterSpec) ValidateBasic() error {
	endpoint := s.NormalizedAPIEndpoint()
	if endpoint == "" {
		return fmt.Errorf("spec.apiEndpoint is required")
	}
	if s.SecretRef.Name == "" {
		return fmt.Errorf("spec.secretRef.name is required")
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid spec.apiEndpoint: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("spec.apiEndpoint must use https")
	}
	if u.Host == "" {
		return fmt.Errorf("spec.apiEndpoint host is required")
	}
	return nil
}

// ClusterStatus defines observed health/capacity for a registered cluster.
// +kubebuilder:object:generate=true
type ClusterStatus struct {
	// Phase is the high-level cluster state.
	// +optional
	Phase ClusterPhase `json:"phase,omitempty"`

	// Capacity is the total discovered capacity (for example GPU resources).
	// +optional
	Capacity corev1.ResourceList `json:"capacity,omitempty"`

	// Available is the currently available capacity.
	// +optional
	Available corev1.ResourceList `json:"available,omitempty"`

	// Models is the aggregated list of remote models.
	// +optional
	Models []ClusterModelStatus `json:"models,omitempty"`

	// LastProbeTime is the timestamp of the last successful probe attempt.
	// +optional
	LastProbeTime *metav1.Time `json:"lastProbeTime,omitempty"`

	// Message carries a human-readable status summary.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions represent detailed status conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=ficl
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.apiEndpoint"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// Cluster represents a remote Kubernetes cluster registered for federation.
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ClusterList contains a list of Cluster resources.
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cluster{}, &ClusterList{})
}
