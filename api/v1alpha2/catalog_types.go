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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RegistrySourceType identifies the registry adapter to use.
type RegistrySourceType string

const (
	RegistrySourceOCI         RegistrySourceType = "OCI"
	RegistrySourceHuggingFace RegistrySourceType = "HuggingFace"
	RegistrySourceOllama      RegistrySourceType = "Ollama"
)

// RegistrySource configures a single model registry endpoint.
// +kubebuilder:object:generate=true
type RegistrySource struct {
	// Type is the registry adapter to use.
	// +kubebuilder:validation:Enum=OCI;HuggingFace;Ollama
	// +kubebuilder:validation:Required
	Type RegistrySourceType `json:"type"`

	// URL is the registry endpoint (e.g., "registry.harbor.lan").
	// Required for OCI registries. Ignored for HuggingFace/Ollama.
	// +optional
	URL string `json:"url,omitempty"`

	// SecretRef references a Kubernetes secret with registry credentials.
	// For OCI: kubernetes.io/dockerconfigjson format.
	// For HuggingFace: key "HF_TOKEN".
	// +optional
	SecretRef *string `json:"secretRef,omitempty"`
}

// CatalogFilter constrains which models are synced from registries.
// +kubebuilder:object:generate=true
type CatalogFilter struct {
	// Tags filters models that have all specified tags.
	// +optional
	Tags []string `json:"tags,omitempty"`

	// NamePattern is a glob pattern for filtering model names.
	// +optional
	NamePattern string `json:"namePattern,omitempty"`
}

// ModelCatalogSpec defines the desired state of a ModelCatalog.
// +kubebuilder:object:generate=true
type ModelCatalogSpec struct {
	// Registries is the list of model registries to sync from.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:Required
	Registries []RegistrySource `json:"registries"`

	// SyncInterval is how often the catalog re-syncs with registries.
	// +kubebuilder:default="1h"
	// +optional
	SyncInterval *metav1.Duration `json:"syncInterval,omitempty"`

	// Filter constrains which models are included in the catalog.
	// +optional
	Filter *CatalogFilter `json:"filter,omitempty"`
}

// CatalogEntryStatus represents a discovered model in the catalog.
// +kubebuilder:object:generate=true
type CatalogEntryStatus struct {
	// Name is the model name.
	Name string `json:"name"`

	// Registry is the source registry type.
	Registry string `json:"registry"`

	// Reference is the full qualified reference for pulling.
	Reference string `json:"reference"`

	// Size is the approximate model size in bytes.
	// +optional
	Size int64 `json:"size,omitempty"`
}

// ModelCatalogStatus defines the observed state of a ModelCatalog.
// +kubebuilder:object:generate=true
type ModelCatalogStatus struct {
	// Entries is the list of discovered models.
	// +optional
	Entries []CatalogEntryStatus `json:"entries,omitempty"`

	// TotalModels is the count of discovered models.
	// +optional
	TotalModels int `json:"totalModels,omitempty"`

	// LastSyncTime is when the catalog was last synced.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// Conditions represent the latest observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=mcat
//+kubebuilder:printcolumn:name="Models",type="integer",JSONPath=".status.totalModels"
//+kubebuilder:printcolumn:name="LastSync",type="date",JSONPath=".status.lastSyncTime"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ModelCatalog is the Schema for the modelcatalogs API.
// It syncs model metadata from one or more registries into a discoverable catalog.
type ModelCatalog struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelCatalogSpec   `json:"spec,omitempty"`
	Status ModelCatalogStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ModelCatalogList contains a list of ModelCatalog.
type ModelCatalogList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelCatalog `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelCatalog{}, &ModelCatalogList{})
}
