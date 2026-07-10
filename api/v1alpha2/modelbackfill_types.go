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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ModelBackfillFinalizer guarantees that an owned background Job is removed
// before the backfill declaration disappears.
const ModelBackfillFinalizer = "ai.flexinfer/modelbackfill"

// ModelBackfillPhase is the lifecycle phase of opportunistic model work.
type ModelBackfillPhase string

const (
	ModelBackfillWaiting    ModelBackfillPhase = "WaitingForIdle"
	ModelBackfillStarting   ModelBackfillPhase = "Starting"
	ModelBackfillRunning    ModelBackfillPhase = "Running"
	ModelBackfillCancelling ModelBackfillPhase = "Cancelling"
	ModelBackfillSucceeded  ModelBackfillPhase = "Succeeded"
	ModelBackfillFailed     ModelBackfillPhase = "Failed"
	ModelBackfillSuspended  ModelBackfillPhase = "Suspended"
	ModelBackfillBlocked    ModelBackfillPhase = "Blocked"
)

// ModelBackfillSpec runs CPU-side background work against an already-warm
// Model. It deliberately cannot claim a GPU; exclusive GPU work requires a
// separate GPULease-backed contract and live promotion gate.
// +kubebuilder:object:generate=true
type ModelBackfillSpec struct {
	// ModelRef names the v1alpha2 Model whose idle window gates this work.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ModelRef string `json:"modelRef"`

	// IdleFor is the continuous foreground-idle window required before a Job is
	// admitted. Zero defaults to ten minutes.
	// +optional
	IdleFor metav1.Duration `json:"idleFor,omitempty"`

	// MaxRunDuration bounds each Job attempt. Zero defaults to thirty minutes.
	// +optional
	MaxRunDuration metav1.Duration `json:"maxRunDuration,omitempty"`

	// Suspend prevents new work and cancels a running attempt.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// TemplateRef names a CronJob in the same namespace. Its Job template is
	// copied into an owner-referenced Job after the idle gate. GPU resource
	// requests are rejected: this first version consumes an existing warm model
	// endpoint rather than evicting it.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TemplateRef string `json:"templateRef"`
}

// ModelBackfillStatus reports the current admission/execution state.
// +kubebuilder:object:generate=true
type ModelBackfillStatus struct {
	// Phase is the coarse lifecycle phase.
	// +optional
	Phase ModelBackfillPhase `json:"phase,omitempty"`

	// ObservedGeneration is the spec generation represented by this status.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// NodeName is the node hosting the referenced warm Model.
	// +optional
	NodeName string `json:"nodeName,omitempty"`

	// JobName is the active or most recent owned Job.
	// +optional
	JobName string `json:"jobName,omitempty"`

	// IdleSince is the beginning of the currently observed foreground-idle span.
	// +optional
	IdleSince *metav1.Time `json:"idleSince,omitempty"`

	// StartedAt is when the current/most recent Job attempt started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletionTime is when the work reached a terminal result.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Attempts counts Jobs created for this declaration, including attempts
	// preempted by foreground demand.
	// +optional
	Attempts int32 `json:"attempts,omitempty"`

	// Reason is a stable machine-readable explanation for the phase.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable phase detail.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions carry detailed readiness and execution transitions.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=mbf;modelbackfill
//+kubebuilder:printcolumn:name="Model",type="string",JSONPath=".spec.modelRef"
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Node",type="string",JSONPath=".status.nodeName"
//+kubebuilder:printcolumn:name="Job",type="string",JSONPath=".status.jobName"
//+kubebuilder:printcolumn:name="Attempts",type="integer",JSONPath=".status.attempts"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ModelBackfill declares opportunistic work against an already-warm model.
type ModelBackfill struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelBackfillSpec   `json:"spec,omitempty"`
	Status ModelBackfillStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ModelBackfillList contains a list of ModelBackfill objects.
type ModelBackfillList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelBackfill `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelBackfill{}, &ModelBackfillList{})
}
