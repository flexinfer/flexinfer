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
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WorkflowProfileFinalizer is the finalizer used for WorkflowProfile cleanup.
const WorkflowProfileFinalizer = "flexinfer.ai/workflowprofile-cleanup"

// WorkflowRouteObjective describes how a profile should choose between eligible models.
type WorkflowRouteObjective string

const (
	// WorkflowRouteObjectivePreferReady chooses already-ready models before cold-start candidates.
	WorkflowRouteObjectivePreferReady WorkflowRouteObjective = "PreferReady"
	// WorkflowRouteObjectiveLowestLatency chooses the lowest observed latency candidate.
	WorkflowRouteObjectiveLowestLatency WorkflowRouteObjective = "LowestLatency"
	// WorkflowRouteObjectiveLongestContext chooses the candidate with the largest sufficient context.
	WorkflowRouteObjectiveLongestContext WorkflowRouteObjective = "LongestContext"
	// WorkflowRouteObjectiveCheapestSufficient chooses the smallest validated lane that satisfies the request.
	WorkflowRouteObjectiveCheapestSufficient WorkflowRouteObjective = "CheapestSufficient"
	// WorkflowRouteObjectivePinned chooses a configured model until policy changes.
	WorkflowRouteObjectivePinned WorkflowRouteObjective = "Pinned"
)

// WorkflowGateState is the aggregate gate state for a candidate or profile.
type WorkflowGateState string

const (
	// WorkflowGateStateUnknown indicates the controller has not evaluated gates yet.
	WorkflowGateStateUnknown WorkflowGateState = "Unknown"
	// WorkflowGateStateSatisfied indicates all configured gates passed.
	WorkflowGateStateSatisfied WorkflowGateState = "Satisfied"
	// WorkflowGateStateBlocked indicates at least one configured gate blocked routing.
	WorkflowGateStateBlocked WorkflowGateState = "Blocked"
	// WorkflowGateStateCanaryOnly indicates the candidate may receive canary traffic but not primary traffic.
	WorkflowGateStateCanaryOnly WorkflowGateState = "CanaryOnly"
)

// WorkflowSupportLevel is an operator-facing maturity tier for routing gates.
type WorkflowSupportLevel string

const (
	// WorkflowSupportLevelExperimental is suitable only for explicit experiments.
	WorkflowSupportLevelExperimental WorkflowSupportLevel = "Experimental"
	// WorkflowSupportLevelCanary is suitable for canary or limited traffic.
	WorkflowSupportLevelCanary WorkflowSupportLevel = "Canary"
	// WorkflowSupportLevelValidated has validation evidence for normal profile traffic.
	WorkflowSupportLevelValidated WorkflowSupportLevel = "Validated"
	// WorkflowSupportLevelProduction has the strongest production support signal.
	WorkflowSupportLevelProduction WorkflowSupportLevel = "Production"
)

// WorkflowProfile condition types.
const (
	// ConditionWorkflowProfileResolved indicates candidate resolution completed.
	ConditionWorkflowProfileResolved = "Resolved"
	// ConditionWorkflowProfileCandidatesAvailable indicates at least one eligible candidate exists.
	ConditionWorkflowProfileCandidatesAvailable = "CandidatesAvailable"
	// ConditionWorkflowProfileGatesSatisfied indicates configured gates allow routing.
	ConditionWorkflowProfileGatesSatisfied = "GatesSatisfied"
	// ConditionWorkflowProfileRoutable indicates the profile has an active routable model.
	ConditionWorkflowProfileRoutable = "Routable"
)

// WorkflowProfileSpec defines the desired state of WorkflowProfile.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="!has(self.servedName) || !self.servedName.startsWith('model:')",message="spec.servedName must be a client-facing profile name, not a reserved concrete-model prefix"
type WorkflowProfileSpec struct {
	// Intent is a short human-readable serving intent, such as fast-chat,
	// long-context-review, code-agent, or image-edit.
	// +optional
	// +kubebuilder:validation:MaxLength=80
	Intent string `json:"intent,omitempty"`

	// ServedName is the primary model name clients send in OpenAI-compatible
	// requests. Defaults to the WorkflowProfile metadata.name.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	ServedName string `json:"servedName,omitempty"`

	// Aliases are additional profile names accepted by the proxy.
	// +optional
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Aliases []string `json:"aliases,omitempty"`

	// Selector chooses existing Model resources that may satisfy this profile.
	// The MVP never creates or mutates Models from a profile.
	// +kubebuilder:validation:Required
	Selector WorkflowProfileSelector `json:"selector"`

	// Routes define ordered routing rules. The controller and proxy evaluate
	// routes top-to-bottom and use objective as the tie-breaker within a route.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Routes []WorkflowRoute `json:"routes"`

	// Gates define policy and validation evidence required before candidates
	// may receive traffic.
	// +optional
	Gates WorkflowGates `json:"gates,omitempty"`
}

// WorkflowProfileSelector chooses candidate Model resources.
// +kubebuilder:object:generate=true
// +kubebuilder:validation:XValidation:rule="has(self.modelRefs) || has(self.matchLabels) || has(self.matchServiceLabels) || has(self.backends) || has(self.capabilities)",message="selector must set at least one of modelRefs, matchLabels, matchServiceLabels, backends, or capabilities"
type WorkflowProfileSelector struct {
	// ModelRefs pins selection to named Models in the same namespace.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	ModelRefs []corev1.LocalObjectReference `json:"modelRefs,omitempty"`

	// MatchLabels requires all listed Kubernetes labels on the Model resource.
	// +optional
	// +kubebuilder:validation:MaxProperties=16
	MatchLabels map[string]string `json:"matchLabels,omitempty"`

	// MatchServiceLabels requires all listed semantic service labels from
	// Model.spec.serviceLabels.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	MatchServiceLabels []string `json:"matchServiceLabels,omitempty"`

	// Backends limits candidates to these backend names.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Backends []string `json:"backends,omitempty"`

	// Capabilities requires explicit Model.spec.capabilities matches.
	// +optional
	Capabilities *ModelCapabilities `json:"capabilities,omitempty"`
}

// WorkflowRoute defines one ordered profile route rule.
// +kubebuilder:object:generate=true
type WorkflowRoute struct {
	// Name is an operator-friendly route name used in status and trace headers.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// Objective describes how candidates are ranked for this route.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=PreferReady;LowestLatency;LongestContext;CheapestSufficient;Pinned
	Objective WorkflowRouteObjective `json:"objective"`

	// MaxPromptTokens is the largest approximate prompt size accepted by this route.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxPromptTokens *int32 `json:"maxPromptTokens,omitempty"`

	// RequiredCapabilities are additional per-route capability requirements.
	// +optional
	RequiredCapabilities *ModelCapabilities `json:"requiredCapabilities,omitempty"`

	// Strategy is an optional downstream concrete-model routing strategy name.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Strategy string `json:"strategy,omitempty"`

	// Weight is reserved for future weighted profile routing experiments.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Weight *int32 `json:"weight,omitempty"`
}

// WorkflowGates defines policy checks that must pass before routing.
// +kubebuilder:object:generate=true
type WorkflowGates struct {
	// RequireReadyOrColdStart permits Ready models and models in phases listed
	// by allowedPhases. When false, phase gating is left to the selected route
	// implementation.
	// +optional
	RequireReadyOrColdStart bool `json:"requireReadyOrColdStart,omitempty"`

	// RequirePromotionEvidence requires validation or promotion evidence before
	// a candidate can receive profile traffic.
	// +optional
	RequirePromotionEvidence bool `json:"requirePromotionEvidence,omitempty"`

	// AllowedPhases are non-Ready phases allowed by this profile, commonly Idle
	// for scale-to-zero cold starts.
	// +optional
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:Enum=Idle;Pending;Loading;Ready;Preempted;Failed
	AllowedPhases []ModelPhase `json:"allowedPhases,omitempty"`

	// MinSupportLevel is the minimum maturity tier required for routing.
	// +optional
	// +kubebuilder:validation:Enum=Experimental;Canary;Validated;Production
	MinSupportLevel WorkflowSupportLevel `json:"minSupportLevel,omitempty"`

	// BackendAllowlist is an optional gate-specific backend allowlist. It is
	// applied after selector.backends so operators can keep broad discovery but
	// narrow routable candidates.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	BackendAllowlist []string `json:"backendAllowlist,omitempty"`
}

// WorkflowProfileStatus defines the observed state of WorkflowProfile.
// +kubebuilder:object:generate=true
type WorkflowProfileStatus struct {
	// ServedName is the resolved primary client-facing name.
	// +optional
	ServedName string `json:"servedName,omitempty"`

	// Aliases are the resolved profile aliases.
	// +optional
	Aliases []string `json:"aliases,omitempty"`

	// ActiveRoute is the route that currently selects ActiveModel.
	// +optional
	ActiveRoute string `json:"activeRoute,omitempty"`

	// ActiveModel is the currently selected concrete Model.
	// +optional
	ActiveModel *corev1.LocalObjectReference `json:"activeModel,omitempty"`

	// GateState is the aggregate gate state for the active route.
	// +optional
	GateState WorkflowGateState `json:"gateState,omitempty"`

	// EligibleCandidates are candidates that may receive profile traffic.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	EligibleCandidates []WorkflowCandidateStatus `json:"eligibleCandidates,omitempty"`

	// RejectedCandidates are candidates that were considered but blocked.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	RejectedCandidates []WorkflowCandidateStatus `json:"rejectedCandidates,omitempty"`

	// LastDecisionTime is when candidate resolution last changed.
	// +optional
	LastDecisionTime *metav1.Time `json:"lastDecisionTime,omitempty"`

	// Conditions represent the latest profile observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// WorkflowCandidateStatus summarizes one considered Model candidate.
// +kubebuilder:object:generate=true
type WorkflowCandidateStatus struct {
	// Name is the Model resource name.
	Name string `json:"name,omitempty"`

	// Route is the route that evaluated this candidate.
	// +optional
	Route string `json:"route,omitempty"`

	// Backend is the candidate Model.spec.backend.
	// +optional
	Backend string `json:"backend,omitempty"`

	// Phase is the candidate Model.status.phase.
	// +optional
	Phase ModelPhase `json:"phase,omitempty"`

	// GateState is the aggregate candidate gate state.
	// +optional
	GateState WorkflowGateState `json:"gateState,omitempty"`

	// Reasons explain why a candidate was selected or rejected.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Reasons []string `json:"reasons,omitempty"`

	// EvidenceRefs identify validation evidence, usually annotation keys or
	// validation matrix references.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=wfp;wfprofile,scope=Namespaced
//+kubebuilder:printcolumn:name="Served",type="string",JSONPath=".status.servedName"
//+kubebuilder:printcolumn:name="Route",type="string",JSONPath=".status.activeRoute"
//+kubebuilder:printcolumn:name="Model",type="string",JSONPath=".status.activeModel.name"
//+kubebuilder:printcolumn:name="Gate",type="string",JSONPath=".status.gateState"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// WorkflowProfile exposes a stable serving-intent name over existing Models.
type WorkflowProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkflowProfileSpec   `json:"spec,omitempty"`
	Status WorkflowProfileStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// WorkflowProfileList contains a list of WorkflowProfile.
type WorkflowProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkflowProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&WorkflowProfile{}, &WorkflowProfileList{})
}

// ResolvedServedName returns spec.servedName or metadata.name when omitted.
func (p *WorkflowProfile) ResolvedServedName() string {
	if p == nil {
		return ""
	}
	if p.Spec.ServedName != "" {
		return p.Spec.ServedName
	}
	return p.Name
}

// ServedNames returns the primary served name followed by unique aliases.
func (p *WorkflowProfile) ServedNames() []string {
	if p == nil {
		return nil
	}
	names := []string{p.ResolvedServedName()}
	for _, alias := range p.Spec.Aliases {
		if alias != "" && !slices.Contains(names, alias) {
			names = append(names, alias)
		}
	}
	return names
}

// MatchesName reports whether name addresses the profile.
func (p *WorkflowProfile) MatchesName(name string) bool {
	return slices.Contains(p.ServedNames(), name)
}

// MatchesModel reports whether model satisfies the static selector.
func (s *WorkflowProfileSelector) MatchesModel(model *Model) bool {
	if s == nil || model == nil {
		return false
	}
	if len(s.ModelRefs) > 0 && !slices.ContainsFunc(s.ModelRefs, func(ref corev1.LocalObjectReference) bool {
		return ref.Name == model.Name
	}) {
		return false
	}
	for key, want := range s.MatchLabels {
		if model.Labels[key] != want {
			return false
		}
	}
	for _, want := range s.MatchServiceLabels {
		if !slices.Contains(model.Spec.ServiceLabels, want) {
			return false
		}
	}
	if len(s.Backends) > 0 && !slices.Contains(s.Backends, model.Spec.Backend) {
		return false
	}
	return CapabilitiesMatch(s.Capabilities, model.Spec.Capabilities)
}

// AllowsPhase reports whether the gates allow a candidate in phase.
func (g *WorkflowGates) AllowsPhase(phase ModelPhase) bool {
	if g == nil || !g.RequireReadyOrColdStart {
		return true
	}
	if phase == ModelPhaseReady {
		return true
	}
	return slices.Contains(g.AllowedPhases, phase)
}

// SelectRoute returns the first route compatible with the request constraints.
func (s *WorkflowProfileSpec) SelectRoute(promptTokens int32, required *ModelCapabilities) *WorkflowRoute {
	if s == nil {
		return nil
	}
	for i := range s.Routes {
		route := &s.Routes[i]
		if route.MaxPromptTokens != nil && promptTokens > *route.MaxPromptTokens {
			continue
		}
		if !CapabilitiesMatch(required, route.RequiredCapabilities) {
			continue
		}
		return route
	}
	return nil
}

// CapabilitiesMatch reports whether offered satisfies all explicitly required capabilities.
func CapabilitiesMatch(required, offered *ModelCapabilities) bool {
	if required == nil {
		return true
	}
	if required.ToolCalling != nil && !capabilityBoolMatches(required.ToolCalling, offeredBool(offered, "toolCalling")) {
		return false
	}
	if required.Vision != nil && !capabilityBoolMatches(required.Vision, offeredBool(offered, "vision")) {
		return false
	}
	if required.ImageGeneration != nil && !capabilityBoolMatches(required.ImageGeneration, offeredBool(offered, "imageGeneration")) {
		return false
	}
	return true
}

func capabilityBoolMatches(required, offered *bool) bool {
	return offered != nil && *required == *offered
}

func offeredBool(caps *ModelCapabilities, name string) *bool {
	if caps == nil {
		return nil
	}
	switch name {
	case "toolCalling":
		return caps.ToolCalling
	case "vision":
		return caps.Vision
	case "imageGeneration":
		return caps.ImageGeneration
	default:
		return nil
	}
}
