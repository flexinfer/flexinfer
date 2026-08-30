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

// GamingSessionFinalizer ensures a node is reverted to inference mode before a
// GamingSession is removed, so deleting the CR always restores the node to the
// servable fleet.
const GamingSessionFinalizer = "ai.flexinfer.ai/gamingsession"

// GamingSessionPhase is the lifecycle phase of a gaming session.
type GamingSessionPhase string

const (
	// GamingSessionPending means the target node's runtime is not yet reachable
	// (pod starting, or no runtime on that node).
	GamingSessionPending GamingSessionPhase = "Pending"
	// GamingSessionDraining means the scheduling overlay is parking the node's
	// inference workloads and the controller is waiting for their pods to leave
	// the node before starting the gaming backend.
	GamingSessionDraining GamingSessionPhase = "Draining"
	// GamingSessionActivating means the controller has requested the mode switch
	// and is waiting for the runtime to report the target mode.
	GamingSessionActivating GamingSessionPhase = "Activating"
	// GamingSessionActive means the node is confirmed in the desired mode.
	GamingSessionActive GamingSessionPhase = "Active"
	// GamingSessionDegraded means the node reports the desired mode but its
	// backing subprocess (e.g. Sunshine) is down; the runtime is supervising
	// restarts and the session recovers to Active once one succeeds.
	GamingSessionDegraded GamingSessionPhase = "Degraded"
	// GamingSessionReverting means the session is being deleted and the node is
	// being returned to inference mode.
	GamingSessionReverting GamingSessionPhase = "Reverting"
	// GamingSessionExpired means the session lease deadline passed and the
	// controller has stopped or is stopping gaming mode. An expired session is
	// not reactivated; delete or extend it to start gaming again.
	GamingSessionExpired GamingSessionPhase = "Expired"
	// GamingSessionFailed means the mode switch could not be completed.
	GamingSessionFailed GamingSessionPhase = "Failed"
)

// GamingSession status condition types. Each maps to one actionable question:
// can this node game at all, is inference off the card, is the streaming host
// up, and is anyone actually playing.
const (
	// GamingConditionCapabilityReady is True when the target node can host an
	// approved gaming session: the runtime pod is reachable and its image is on
	// the operator's approved list, and the session satisfies owner policy.
	GamingConditionCapabilityReady = "CapabilityReady"
	// GamingConditionDrainComplete is True when no inference model pods remain
	// on the node (runtime-managed models are unloaded by the runtime during
	// the mode switch itself).
	GamingConditionDrainComplete = "DrainComplete"
	// GamingConditionSunshineReady is True when the runtime reports gaming mode
	// with a healthy (non-degraded) gaming backend.
	GamingConditionSunshineReady = "SunshineReady"
	// GamingConditionStreamActive is True while the runtime observes an
	// established Moonlight client connection. Unknown when the runtime image
	// predates client telemetry.
	GamingConditionStreamActive = "StreamActive"
)

// GamingSession condition reasons (shared with the CLI so `flexinfer game`
// can explain blockers with the same vocabulary).
const (
	GamingReasonRuntimeNotFound      = "RuntimeNotFound"
	GamingReasonImageNotApproved     = "ImageNotApproved"
	GamingReasonOwnerRequired        = "OwnerRequired"
	GamingReasonPolicySatisfied      = "PolicySatisfied"
	GamingReasonAdoptedActiveMode    = "AdoptedActiveMode"
	GamingReasonDraining             = "Draining"
	GamingReasonDrainBlocked         = "DrainBlocked"
	GamingReasonDrainComplete        = "DrainComplete"
	GamingReasonModeSwitchPending    = "ModeSwitchPending"
	GamingReasonBackendDegraded      = "BackendDegraded"
	GamingReasonSunshineUp           = "SunshineUp"
	GamingReasonClientConnected      = "ClientConnected"
	GamingReasonNoClient             = "NoClient"
	GamingReasonTelemetryUnavailable = "TelemetryUnavailable"
	GamingReasonSessionEnded         = "SessionEnded"
	GamingReasonRuntimeReverted      = "RuntimeReverted"
	GamingReasonIdleTimeout          = "IdleTimeout"
	GamingReasonExpired              = "Expired"
)

// GamingSessionSpec declares that a GPU node should be placed in a given mode.
// Creating a GamingSession drives the node to spec.mode (default "gaming"),
// which drains inference; deleting it always reverts the node to "inference".
// +kubebuilder:object:generate=true
type GamingSessionSpec struct {
	// NodeName is the GPU node to switch (matched via kubernetes.io/hostname).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	NodeName string `json:"nodeName"`

	// Mode is the desired node mode while this session exists. Normal usage is
	// "gaming"; deleting the session reverts the node to "inference" regardless.
	// +kubebuilder:validation:Enum=inference;gaming
	// +kubebuilder:default=gaming
	// +optional
	Mode string `json:"mode,omitempty"`

	// Owner identifies the person, tool, or workflow that requested the gaming
	// lease. It is informational and used in events/status so stuck sessions can
	// be attributed.
	// +optional
	Owner string `json:"owner,omitempty"`

	// ExpiresAt is an absolute lease deadline for this gaming session. Once
	// now >= the effective deadline, the controller drives the node back to
	// inference mode and keeps the session Expired until the object is deleted
	// or the deadline is extended.
	//
	// Every session has a deadline: the effective deadline is the earliest of
	// ExpiresAt, creation+TTL, and creation+maxTTL (from the gaming policy).
	// When neither ExpiresAt nor TTL is set, the policy's defaultTTL applies.
	// The controller reports the effective deadline in status.expiresAt.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// TTL bounds the session relative to its creation time (e.g. "2h"). See
	// ExpiresAt for how the effective deadline is derived.
	// +optional
	TTL *metav1.Duration `json:"ttl,omitempty"`

	// IdleTimeout ends the session after no Moonlight client has been
	// connected for this long (measured from activation or the last observed
	// client, whichever is later). Enforcement requires a runtime image that
	// reports client telemetry; without it the session only expires via its
	// deadline or the runtime's own GAMING_IDLE_TIMEOUT guard.
	// +optional
	IdleTimeout *metav1.Duration `json:"idleTimeout,omitempty"`

	// DrainTimeout bounds how long activation may wait for inference pods to
	// leave the node before reporting DrainBlocked. Defaults to the gaming
	// policy's drainTimeout (10m when unset).
	// +optional
	DrainTimeout *metav1.Duration `json:"drainTimeout,omitempty"`
}

// GamingSessionStatus is the observed state of a gaming session.
// +kubebuilder:object:generate=true
type GamingSessionStatus struct {
	// Phase is the coarse lifecycle phase.
	// +optional
	Phase GamingSessionPhase `json:"phase,omitempty"`

	// ObservedMode is the mode the node's runtime last reported.
	// +optional
	ObservedMode string `json:"observedMode,omitempty"`

	// RuntimePod is the runtime pod the controller is driving on the node.
	// +optional
	RuntimePod string `json:"runtimePod,omitempty"`

	// Message is a human-readable detail for the current phase.
	// +optional
	Message string `json:"message,omitempty"`

	// ActivatedAt is when the node first reached the desired gaming mode.
	// +optional
	ActivatedAt *metav1.Time `json:"activatedAt,omitempty"`

	// ExpiredAt is when the controller first observed the session past its
	// lease deadline and began reverting the node to inference.
	// +optional
	ExpiredAt *metav1.Time `json:"expiredAt,omitempty"`

	// ExpiresAt is the effective lease deadline the controller enforces: the
	// earliest of spec.expiresAt, creation+spec.ttl, and creation+policy
	// maxTTL, defaulted from the gaming policy when the spec is silent.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// LastStreamActiveAt is the last time the runtime reported an established
	// Moonlight client. Drives spec.idleTimeout enforcement.
	// +optional
	LastStreamActiveAt *metav1.Time `json:"lastStreamActiveAt,omitempty"`

	// EndReason records why a session concluded: Expired (deadline),
	// IdleTimeout, or RuntimeReverted (the node left gaming mode outside the
	// controller, e.g. the runtime's own idle guard). A deadline expiry can be
	// undone by extending spec.expiresAt/ttl; IdleTimeout and RuntimeReverted
	// are terminal — delete the session and start a new one to game again.
	// +optional
	EndReason string `json:"endReason,omitempty"`

	// Conditions carry the actionable activation state: CapabilityReady,
	// DrainComplete, SunshineReady, StreamActive.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=gs;gamesession
//+kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.nodeName"
//+kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.mode"
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Observed",type="string",JSONPath=".status.observedMode"
//+kubebuilder:printcolumn:name="Owner",type="string",JSONPath=".spec.owner"
//+kubebuilder:printcolumn:name="Expires",type="date",JSONPath=".status.expiresAt"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// GamingSession is the declarative carrier for putting a GPU node into gaming
// mode. Creating one drains inference on the node and starts the Sunshine
// gaming host; deleting it returns the node to the inference fleet.
type GamingSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GamingSessionSpec   `json:"spec,omitempty"`
	Status GamingSessionStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// GamingSessionList contains a list of GamingSession.
type GamingSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GamingSession `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GamingSession{}, &GamingSessionList{})
}
