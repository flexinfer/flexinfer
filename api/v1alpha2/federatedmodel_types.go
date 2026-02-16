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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RoutingStrategy defines cross-cluster traffic distribution strategy.
type RoutingStrategy string

const (
	// RoutingStrategyRoundRobin cycles requests across healthy clusters.
	RoutingStrategyRoundRobin RoutingStrategy = "RoundRobin"
	// RoutingStrategyLatency prefers the lowest-latency healthy cluster.
	RoutingStrategyLatency RoutingStrategy = "Latency"
	// RoutingStrategyWeighted distributes requests by explicit cluster weights.
	RoutingStrategyWeighted RoutingStrategy = "Weighted"
	// RoutingStrategyFailover uses ordered primary/backup clusters.
	RoutingStrategyFailover RoutingStrategy = "Failover"
)

// FederatedModelPlacement controls which clusters get the model.
// +kubebuilder:object:generate=true
type FederatedModelPlacement struct {
	// ClusterSelector selects target clusters by labels.
	// +optional
	ClusterSelector *metav1.LabelSelector `json:"clusterSelector,omitempty"`

	// Clusters explicitly lists target cluster names.
	// +optional
	Clusters []string `json:"clusters,omitempty"`

	// ReplicasPerCluster is the desired replicas per selected cluster.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	// +optional
	ReplicasPerCluster *int32 `json:"replicasPerCluster,omitempty"`
}

// FederatedModelRouting controls request routing across cluster-local endpoints.
// +kubebuilder:object:generate=true
type FederatedModelRouting struct {
	// Strategy is the routing mode.
	// +kubebuilder:validation:Enum=RoundRobin;Latency;Weighted;Failover
	// +kubebuilder:default=RoundRobin
	// +optional
	Strategy RoutingStrategy `json:"strategy,omitempty"`

	// Weights configures weighted routing (cluster -> weight).
	// +optional
	Weights map[string]int32 `json:"weights,omitempty"`

	// FailoverOrder defines primary -> backup order for failover routing.
	// +optional
	FailoverOrder []string `json:"failoverOrder,omitempty"`
}

// FederatedModelSpec defines desired state for a multi-cluster model.
// +kubebuilder:object:generate=true
type FederatedModelSpec struct {
	// Template is the Model spec propagated to selected clusters.
	// +kubebuilder:validation:Required
	Template ModelSpec `json:"template"`

	// Placement controls cluster selection and replica distribution.
	// +kubebuilder:validation:Required
	Placement FederatedModelPlacement `json:"placement"`

	// Routing controls request distribution across clusters.
	// +optional
	Routing *FederatedModelRouting `json:"routing,omitempty"`
}

// ValidateBasic performs simple spec checks independent of controller state.
func (s FederatedModelSpec) ValidateBasic() error {
	if len(s.Placement.Clusters) == 0 && s.Placement.ClusterSelector == nil {
		return fmt.Errorf("spec.placement requires either clusters or clusterSelector")
	}
	if s.Placement.ReplicasPerCluster != nil && *s.Placement.ReplicasPerCluster < 1 {
		return fmt.Errorf("spec.placement.replicasPerCluster must be >= 1")
	}
	if s.Routing != nil {
		if s.Routing.Strategy == RoutingStrategyWeighted && len(s.Routing.Weights) == 0 {
			return fmt.Errorf("spec.routing.weights required for Weighted strategy")
		}
		if s.Routing.Strategy == RoutingStrategyFailover && len(s.Routing.FailoverOrder) == 0 {
			return fmt.Errorf("spec.routing.failoverOrder required for Failover strategy")
		}
	}
	return nil
}

// FederatedModelClusterStatus is per-cluster deployment state.
// +kubebuilder:object:generate=true
type FederatedModelClusterStatus struct {
	// Cluster is the target cluster name.
	Cluster string `json:"cluster"`
	// Phase is the cluster-local phase summary.
	Phase string `json:"phase,omitempty"`
	// ReadyReplicas is the number of ready replicas in this cluster.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
	// TotalReplicas is the desired/observed replicas in this cluster.
	// +optional
	TotalReplicas int32 `json:"totalReplicas,omitempty"`
}

// FederatedModelStatus is aggregated state across target clusters.
// +kubebuilder:object:generate=true
type FederatedModelStatus struct {
	// Clusters is per-cluster deployment status.
	// +optional
	Clusters []FederatedModelClusterStatus `json:"clusters,omitempty"`
	// ReadyClusters is the count of clusters currently ready.
	// +optional
	ReadyClusters int32 `json:"readyClusters,omitempty"`
	// TotalClusters is the total selected clusters.
	// +optional
	TotalClusters int32 `json:"totalClusters,omitempty"`
	// Conditions are aggregated reconciliation conditions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=fmodel
//+kubebuilder:printcolumn:name="ReadyClusters",type="string",JSONPath=".status.readyClusters"
//+kubebuilder:printcolumn:name="TotalClusters",type="string",JSONPath=".status.totalClusters"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// FederatedModel declares model deployment intent across multiple clusters.
type FederatedModel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   FederatedModelSpec   `json:"spec,omitempty"`
	Status FederatedModelStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// FederatedModelList contains a list of FederatedModel resources.
type FederatedModelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FederatedModel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&FederatedModel{}, &FederatedModelList{})
}
