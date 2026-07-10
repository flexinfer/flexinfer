/*
Copyright 2026.

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

const ModelExperimentFinalizer = "ai.flexinfer/modelexperiment"

type ModelExperimentPhase string

const (
	ModelExperimentDeploying  ModelExperimentPhase = "Deploying"
	ModelExperimentServing    ModelExperimentPhase = "Serving"
	ModelExperimentEvaluating ModelExperimentPhase = "Evaluating"
	ModelExperimentSucceeded  ModelExperimentPhase = "Succeeded"
	ModelExperimentFailed     ModelExperimentPhase = "Failed"
	ModelExperimentBlocked    ModelExperimentPhase = "Blocked"
	ModelExperimentSuspended  ModelExperimentPhase = "Suspended"
)

// ModelExperimentGauntletSpec configures the Job that evaluates a candidate.
// +kubebuilder:object:generate=true
type ModelExperimentGauntletSpec struct {
	// TemplateRef names a CronJob in the same namespace. Its Job template is
	// copied after the candidate Model becomes Ready.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TemplateRef string `json:"templateRef"`

	// Env contains literal environment overrides applied to every regular
	// container in the copied Job. MODELS is controller-managed.
	// +optional
	// +kubebuilder:validation:MaxProperties=32
	Env map[string]string `json:"env,omitempty"`
}

// ModelExperimentSpec declares an isolated serving candidate and its gauntlet.
// +kubebuilder:object:generate=true
type ModelExperimentSpec struct {
	// Candidate is the complete Model spec deployed only for this experiment.
	Candidate ModelSpec `json:"candidate"`

	// Gauntlet defines the evaluation Job template and threshold overrides.
	Gauntlet ModelExperimentGauntletSpec `json:"gauntlet"`

	// Timeout bounds the experiment from candidate creation through verdict.
	// Zero defaults to thirty minutes.
	// +optional
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// Suspend prevents new work and removes active experiment resources.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// ModelExperimentVerdict is the durable result of the gauntlet Job.
// +kubebuilder:object:generate=true
type ModelExperimentVerdict struct {
	Pass bool `json:"pass"`

	// Reason is a stable machine-readable result classification.
	Reason string `json:"reason"`

	// Summary is a human-readable result description. Detailed checks remain in
	// the retained Job logs and benchmark result store.
	Summary string `json:"summary"`

	CompletedAt metav1.Time `json:"completedAt"`
}

// ModelExperimentStatus reports the owned resources and current verdict.
// +kubebuilder:object:generate=true
type ModelExperimentStatus struct {
	// +optional
	Phase ModelExperimentPhase `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	CandidateName string `json:"candidateName,omitempty"`
	// +optional
	JobName string `json:"jobName,omitempty"`
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// +optional
	Verdict *ModelExperimentVerdict `json:"verdict,omitempty"`
	// +optional
	Reason string `json:"reason,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=mexp;modelexperiment
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Candidate",type="string",JSONPath=".status.candidateName"
//+kubebuilder:printcolumn:name="Job",type="string",JSONPath=".status.jobName"
//+kubebuilder:printcolumn:name="Pass",type="boolean",JSONPath=".status.verdict.pass"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ModelExperiment runs a bounded canary Model through an evaluation gauntlet.
type ModelExperiment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModelExperimentSpec   `json:"spec,omitempty"`
	Status ModelExperimentStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

type ModelExperimentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModelExperiment `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ModelExperiment{}, &ModelExperimentList{})
}
