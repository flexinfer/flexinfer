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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GPUShareStrategy defines how models share the GPU
type GPUShareStrategy string

const (
	// GPUShareStrategyExclusive means only one model runs at a time (default)
	GPUShareStrategyExclusive GPUShareStrategy = "Exclusive"
	// GPUShareStrategyTimeSliced means models share GPU via NVIDIA MPS/MIG (future)
	GPUShareStrategyTimeSliced GPUShareStrategy = "TimeSliced"
	// GPUShareStrategyMemoryShared means multiple models if VRAM permits (future)
	GPUShareStrategyMemoryShared GPUShareStrategy = "MemoryShared"
)

// PreemptionPolicy defines when lower priority models are evicted
type PreemptionPolicy string

const (
	// PreemptionPolicyImmediate evicts immediately when higher priority needs GPU
	PreemptionPolicyImmediate PreemptionPolicy = "Immediate"
	// PreemptionPolicyGraceful waits for in-flight requests to complete
	PreemptionPolicyGraceful PreemptionPolicy = "Graceful"
)

// GPUGroupPhase describes the lifecycle of the GPU group
type GPUGroupPhase string

const (
	// GPUGroupPhasePending means the group is created but not yet reconciled
	GPUGroupPhasePending GPUGroupPhase = "Pending"
	// GPUGroupPhaseIdle means no models are currently active
	GPUGroupPhaseIdle GPUGroupPhase = "Idle"
	// GPUGroupPhaseActive means a model is currently running
	GPUGroupPhaseActive GPUGroupPhase = "Active"
	// GPUGroupPhaseDraining means current model is draining before swap
	GPUGroupPhaseDraining GPUGroupPhase = "Draining"
	// GPUGroupPhaseSwapping means models are being swapped
	GPUGroupPhaseSwapping GPUGroupPhase = "Swapping"
	// GPUGroupPhaseFailed means something went wrong
	GPUGroupPhaseFailed GPUGroupPhase = "Failed"
)

// ModelGroupState describes a model's state within the GPUGroup
type ModelGroupState string

const (
	// ModelGroupStateActive means this model is currently running on the GPU
	ModelGroupStateActive ModelGroupState = "Active"
	// ModelGroupStatePreempted means this model was scaled down for another model
	ModelGroupStatePreempted ModelGroupState = "Preempted"
	// ModelGroupStateQueued means requests are waiting for this model
	ModelGroupStateQueued ModelGroupState = "Queued"
	// ModelGroupStateIdle means this model has no pending requests
	ModelGroupStateIdle ModelGroupState = "Idle"
)

// GPUGroupSpec defines the desired state of GPUGroup
// +kubebuilder:object:generate=true
type GPUGroupSpec struct {
	// Models is the list of ModelDeployment references in this group.
	// Each model can run on the GPU based on priority and demand.
	// +kubebuilder:validation:MinItems=1
	Models []GPUGroupMember `json:"models"`

	// NodeSelector constrains which GPU node this group can use.
	// All models in the group will be scheduled to nodes matching this selector.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// ScalingPolicy defines how models scale within the group.
	// +optional
	ScalingPolicy GPUGroupScalingPolicy `json:"scalingPolicy,omitempty"`

	// AntiThrashing configures protection against rapid model swapping.
	// +optional
	AntiThrashing AntiThrashingConfig `json:"antiThrashing,omitempty"`

	// AutoCacheModels if true, creates Memory-backed ModelCache for all group models.
	// Enables fast GPU-to-GPU swapping by keeping models in RAM (/dev/shm).
	// +kubebuilder:default=false
	// +optional
	AutoCacheModels bool `json:"autoCacheModels,omitempty"`
}

// GPUGroupMember references a ModelDeployment with priority
// +kubebuilder:object:generate=true
type GPUGroupMember struct {
	// Name is the ModelDeployment name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the ModelDeployment. Defaults to the GPUGroup namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Priority determines preemption order. Higher values = higher priority.
	// When multiple models have pending requests, the highest priority model wins.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000
	// +kubebuilder:default=100
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// VRAMEstimateMB is the estimated VRAM usage for this model in megabytes.
	// Used for bin-packing decisions when multiple models can fit (future).
	// +optional
	VRAMEstimateMB int64 `json:"vramEstimateMB,omitempty"`

	// MinReplicas for this model within the group. Overrides ModelDeployment.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas for this model within the group.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`
}

// GPUGroupScalingPolicy defines scaling behavior within the group
// +kubebuilder:object:generate=true
type GPUGroupScalingPolicy struct {
	// Strategy determines how models compete for GPU time.
	// - Exclusive: Only one model runs at a time (default, recommended)
	// - TimeSliced: Models share GPU via NVIDIA MPS/MIG (future)
	// - MemoryShared: Multiple models if VRAM permits (future)
	// +kubebuilder:validation:Enum=Exclusive;TimeSliced;MemoryShared
	// +kubebuilder:default="Exclusive"
	// +optional
	Strategy GPUShareStrategy `json:"strategy,omitempty"`

	// PreemptionPolicy controls when lower priority models are evicted.
	// - Immediate: Evict immediately when higher priority needs GPU
	// - Graceful: Wait for in-flight requests to complete (respects terminationGracePeriod)
	// +kubebuilder:validation:Enum=Immediate;Graceful
	// +kubebuilder:default="Graceful"
	// +optional
	PreemptionPolicy PreemptionPolicy `json:"preemptionPolicy,omitempty"`

	// DrainTimeoutSeconds is the max time to wait for model to drain before force-killing.
	// Only applies when PreemptionPolicy is Graceful.
	// +kubebuilder:default=60
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=300
	// +optional
	DrainTimeoutSeconds int32 `json:"drainTimeoutSeconds,omitempty"`
}

// AntiThrashingConfig prevents rapid model swapping (A→B→A→B oscillation)
// +kubebuilder:object:generate=true
type AntiThrashingConfig struct {
	// Enabled turns on anti-thrashing protection.
	// +kubebuilder:default=true
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// MinimumRunDurationSeconds is how long a model must run before it can be preempted.
	// Prevents thrashing when requests alternate rapidly between models.
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=300
	// +optional
	MinimumRunDurationSeconds int32 `json:"minimumRunDurationSeconds,omitempty"`

	// CooldownAfterPreemptionSeconds is how long to wait before the preempted model
	// can preempt back. Prevents A→B→A→B oscillation.
	// +kubebuilder:default=60
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=600
	// +optional
	CooldownAfterPreemptionSeconds int32 `json:"cooldownAfterPreemptionSeconds,omitempty"`

	// RequestQueueThreshold is the number of queued requests needed before preemption.
	// Helps batch requests and reduce unnecessary swaps.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	RequestQueueThreshold int32 `json:"requestQueueThreshold,omitempty"`

	// HysteresisWindowSeconds is the observation window for decision making.
	// Model swap only triggers if demand persists for this duration.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=60
	// +optional
	HysteresisWindowSeconds int32 `json:"hysteresisWindowSeconds,omitempty"`
}

// GPUGroupStatus defines the observed state of GPUGroup
// +kubebuilder:object:generate=true
type GPUGroupStatus struct {
	// Phase represents the current lifecycle state of the GPUGroup.
	// +optional
	Phase GPUGroupPhase `json:"phase,omitempty"`

	// ActiveModel is the name of the currently running model (Exclusive strategy).
	// Empty string means no model is active.
	// +optional
	ActiveModel string `json:"activeModel,omitempty"`

	// AllocatedGPU contains information about the assigned GPU.
	// +optional
	AllocatedGPU *GPUAllocation `json:"allocatedGPU,omitempty"`

	// LastSwapTime records when the active model last changed.
	// +optional
	LastSwapTime *metav1.Time `json:"lastSwapTime,omitempty"`

	// LastSwapReason records why the last swap occurred.
	// +optional
	LastSwapReason string `json:"lastSwapReason,omitempty"`

	// SwapCount is the total number of model swaps since creation.
	// +optional
	SwapCount int64 `json:"swapCount,omitempty"`

	// ModelStatuses tracks the state of each member model.
	// +optional
	ModelStatuses []GPUGroupModelStatus `json:"modelStatuses,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// GPUGroupModelStatus tracks individual model state within the group
// +kubebuilder:object:generate=true
type GPUGroupModelStatus struct {
	// Name is the ModelDeployment name.
	Name string `json:"name"`

	// State is the current state: Active, Preempted, Queued, Idle
	State ModelGroupState `json:"state,omitempty"`

	// QueuedRequests is the number of requests waiting for this model.
	// Updated by the proxy via annotations.
	// +optional
	QueuedRequests int32 `json:"queuedRequests,omitempty"`

	// QueuedSince is when requests started queueing (nil if no queue).
	// +optional
	QueuedSince *metav1.Time `json:"queuedSince,omitempty"`

	// LastActiveTime is when this model was last serving requests.
	// +optional
	LastActiveTime *metav1.Time `json:"lastActiveTime,omitempty"`

	// PreemptedAt is when this model was last preempted.
	// Used for cooldown tracking.
	// +optional
	PreemptedAt *metav1.Time `json:"preemptedAt,omitempty"`

	// PreemptedBy is the model that caused preemption.
	// +optional
	PreemptedBy string `json:"preemptedBy,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Active",type="string",JSONPath=".status.activeModel"
// +kubebuilder:printcolumn:name="Swaps",type="integer",JSONPath=".status.swapCount"
// +kubebuilder:printcolumn:name="Strategy",type="string",JSONPath=".spec.scalingPolicy.strategy"

// GPUGroup is the Schema for the gpugroups API.
// A GPUGroup represents a set of models that can share a single GPU,
// with priority-based preemption and anti-thrashing protection.
type GPUGroup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GPUGroupSpec   `json:"spec,omitempty"`
	Status GPUGroupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GPUGroupList contains a list of GPUGroup
type GPUGroupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GPUGroup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GPUGroup{}, &GPUGroupList{})
}
