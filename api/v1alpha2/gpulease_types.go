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

// GPULeaseSpec describes a transient, scheduler-honored hold on a shared-GPU
// group. While an unexpired lease exists for a group, the shared-GPU election
// (chooseSharedGroupLeaders) yields no leader so every serving member parks and
// stays parked, freeing amd.com/gpu for a training/quant Job, until the lease is
// deleted or expires.
//
// This is the first-class carrier that replaces the slice-1 labeled-ConfigMap
// sentinel; the election contract is unchanged (groupHasActiveLease). The
// controller still honors a legacy ConfigMap lease for backward compatibility.
// +kubebuilder:object:generate=true
type GPULeaseSpec struct {
	// Group is the shared-GPU group (Model.spec.gpu.shared) this lease holds.
	// +kubebuilder:validation:Required
	Group string `json:"group"`

	// Node is the node whose card the lease holds. Informational/targeting; the
	// election keys on Group, not Node.
	// +optional
	Node string `json:"node,omitempty"`

	// Owner is the workload that acquired the lease (e.g. a ModelCache name),
	// used for PreemptedBy attribution and observability.
	// +optional
	Owner string `json:"owner,omitempty"`

	// ExpiresAt is the TTL deadline. The election ignores the lease once
	// now >= ExpiresAt, so a dead acquirer cannot strand serving forever
	// (crash-safety backstop). A nil ExpiresAt means the lease is honored for
	// as long as the object exists -- pair it with an ownerReference so it is
	// still garbage-collected when the owning workload is deleted.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Priority is the preempt-policy threshold (slice-5 hardening): the serving
	// priority this lease outranks. The shared-GPU election frees the card only
	// when every serving member of the group has a gpu.priority strictly below
	// this value; a member at or above it keeps the card and the leasing
	// workload waits instead of preempting a higher- or equal-priority lane. A
	// nil Priority means the lease is honored unconditionally (every serving
	// member parks) -- the original pre-slice-5 park-and-hold behavior.
	// +optional
	Priority *int32 `json:"priority,omitempty"`
}

// GPULeaseStatus is the observed state of a GPU lease.
// +kubebuilder:object:generate=true
type GPULeaseStatus struct {
	// AcquiredAt is when the controller first observed the lease as active.
	// +optional
	AcquiredAt *metav1.Time `json:"acquiredAt,omitempty"`

	// Active reflects whether the lease is currently honored by the election
	// (group held). False once it has expired.
	// +optional
	Active bool `json:"active,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=gpulease
//+kubebuilder:printcolumn:name="Group",type="string",JSONPath=".spec.group"
//+kubebuilder:printcolumn:name="Owner",type="string",JSONPath=".spec.owner"
//+kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.node"
//+kubebuilder:printcolumn:name="Expires",type="date",JSONPath=".spec.expiresAt"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GPULease is the Schema for transient training-vs-serving GPU holds on a
// shared-GPU group. A training/quant workload creates one to park the serving
// incumbent, then deletes it to restore serving.
type GPULease struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GPULeaseSpec   `json:"spec,omitempty"`
	Status GPULeaseStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GPULeaseList contains a list of GPULease.
type GPULeaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPULease `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPULease{}, &GPULeaseList{})
}
