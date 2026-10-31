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

import (
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InferenceJob is one asynchronous inference request: long work (a render, a
// transcription, a long chat) handed to the proxy, which runs it against the
// named Model under the lane's normal cold-start, budget and breaker path,
// stores the output and reports it by id. The proxy's /v1/jobs API and
// `background: true` on the OpenAI paths create these; the job object they
// return mirrors the OpenAI Responses background fields
// (docs/specs/proxy-api.md, "Jobs").

// InferenceJobPhase is a job's lifecycle state. The values are the OpenAI
// Responses background statuses, lowercase, so the API returns them as-is.
// +kubebuilder:validation:Enum=queued;in_progress;completed;failed;cancelled;incomplete
type InferenceJobPhase string

const (
	// InferenceJobQueued - accepted, waiting for a runner slot or for its lane.
	InferenceJobQueued InferenceJobPhase = "queued"
	// InferenceJobInProgress - a runner forwarded it to the lane.
	InferenceJobInProgress InferenceJobPhase = "in_progress"
	// InferenceJobCompleted - the lane answered 2xx and the output is stored.
	InferenceJobCompleted InferenceJobPhase = "completed"
	// InferenceJobFailed - the lane answered an error, or the job ran out of
	// attempts; status.error says which.
	InferenceJobFailed InferenceJobPhase = "failed"
	// InferenceJobCancelled - spec.cancel was set before the job finished.
	InferenceJobCancelled InferenceJobPhase = "cancelled"
	// InferenceJobIncomplete - the lane answered but the output could not be
	// stored whole (storage error or size cap).
	InferenceJobIncomplete InferenceJobPhase = "incomplete"
)

// IsTerminal reports whether phase ends the job.
func (p InferenceJobPhase) IsTerminal() bool {
	switch p {
	case InferenceJobCompleted, InferenceJobFailed, InferenceJobCancelled, InferenceJobIncomplete:
		return true
	}
	return false
}

// InferenceJobSpec is the request a job runs.
// +kubebuilder:object:generate=true
type InferenceJobSpec struct {
	// Model is the Model the request names: CR name, served name or alias,
	// resolved by the proxy the way a synchronous request would be.
	// +kubebuilder:validation:MinLength=1
	Model string `json:"model"`

	// Path is the OpenAI path the request is sent to, e.g.
	// /v1/images/generations or /v1/audio/transcriptions.
	// +kubebuilder:validation:Pattern=`^/v1/`
	Path string `json:"path"`

	// Request is the JSON request body, inline. Bodies over 256 KiB, and
	// non-JSON bodies (multipart uploads), go to RequestRef or to an
	// InputRefs entry named "body" instead.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Request *apiextensionsv1.JSON `json:"request,omitempty"`

	// RequestRef points at a ConfigMap or Secret key holding the request
	// body, for callers that create jobs with kubectl.
	// +optional
	RequestRef *InferenceJobRequestRef `json:"requestRef,omitempty"`

	// ContentType is the request body's media type. Defaults to
	// application/json; a multipart body carries its boundary here.
	// +optional
	ContentType string `json:"contentType,omitempty"`

	// InputRefs are request inputs held in job storage. The proxy stores a
	// multipart body (an audio file, an image to edit) as the input named
	// "body" and replays it to the lane verbatim.
	// +optional
	InputRefs []InferenceJobObject `json:"inputRefs,omitempty"`

	// Webhook is called when the job reaches the listed events.
	// +optional
	Webhook *InferenceJobWebhook `json:"webhook,omitempty"`

	// TTLAfterFinished is how long a finished job and its output are kept
	// before the manager deletes both. Defaults to 24h.
	// +optional
	TTLAfterFinished *metav1.Duration `json:"ttlAfterFinished,omitempty"`

	// QoSClass is the caller class the job runs under (D3); empty means the
	// class the proxy resolves for the caller.
	// +optional
	QoSClass string `json:"qosClass,omitempty"`

	// IdempotencyKey dedupes submissions: a second job with the same key for
	// the same caller returns the first instead of running again.
	// +kubebuilder:validation:MaxLength=255
	// +optional
	IdempotencyKey string `json:"idempotencyKey,omitempty"`

	// Cancel asks the runner to stop the job. A queued job is cancelled at
	// once; a running one when its forward is aborted.
	// +optional
	Cancel bool `json:"cancel,omitempty"`
}

// InferenceJobRequestRef selects the key of a ConfigMap or Secret in the
// job's namespace that holds the request body.
// +kubebuilder:object:generate=true
type InferenceJobRequestRef struct {
	// Kind is ConfigMap or Secret.
	// +kubebuilder:validation:Enum=ConfigMap;Secret
	Kind string `json:"kind"`
	// Name is the object's name.
	Name string `json:"name"`
	// Key is the data key holding the body.
	Key string `json:"key"`
}

// InferenceJobObject is one object in job storage: an input or the output.
// +kubebuilder:object:generate=true
type InferenceJobObject struct {
	// Name identifies an input ("body" for the request body); empty for the
	// output.
	// +optional
	Name string `json:"name,omitempty"`
	// URI locates the object: s3://bucket/key for MinIO, pvc://claim/path for
	// the PVC store.
	URI string `json:"uri"`
	// ContentType is the object's media type.
	// +optional
	ContentType string `json:"contentType,omitempty"`
	// Bytes is the object's size.
	// +optional
	Bytes int64 `json:"bytes,omitempty"`
	// SHA256 is the hex digest of the object.
	// +optional
	SHA256 string `json:"sha256,omitempty"`
}

// InferenceJobWebhook is a Standard Webhooks endpoint
// (https://www.standardwebhooks.com): deliveries carry webhook-id,
// webhook-timestamp and webhook-signature, signed with the secret.
// +kubebuilder:object:generate=true
type InferenceJobWebhook struct {
	// URL receives a POST per event.
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
	// SecretRef holds the signing secret ("whsec_" + base64, or raw bytes).
	// Without it deliveries are unsigned.
	// +optional
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`
	// Events to deliver: job.completed, job.failed, job.cancelled,
	// job.incomplete, job.in_progress. Defaults to the four terminal events.
	// +optional
	Events []string `json:"events,omitempty"`
}

// InferenceJobError is why a job did not complete. Code is an error-envelope
// code (model_failed, lane_unresponsive, ...) or the lane's own error code.
// +kubebuilder:object:generate=true
type InferenceJobError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	// Status is the HTTP status the lane or the proxy answered, 0 when the
	// job never reached the lane.
	// +optional
	Status int32 `json:"status,omitempty"`
}

// InferenceJobWebhookStatus records webhook delivery.
// +kubebuilder:object:generate=true
type InferenceJobWebhookStatus struct {
	// Delivered lists the events delivered with a 2xx.
	// +optional
	Delivered []string `json:"delivered,omitempty"`
	// Attempts counts delivery attempts across events.
	// +optional
	Attempts int32 `json:"attempts,omitempty"`
	// LastError is the last failed attempt's error.
	// +optional
	LastError string `json:"lastError,omitempty"`
	// LastAttemptAt is when the last attempt was made.
	// +optional
	LastAttemptAt *metav1.Time `json:"lastAttemptAt,omitempty"`
}

// InferenceJobStatus is a job's observed state.
// +kubebuilder:object:generate=true
type InferenceJobStatus struct {
	// Phase is the job's lifecycle state.
	// +optional
	Phase InferenceJobPhase `json:"phase,omitempty"`
	// Progress is a 0-100 estimate while in_progress, from the lane's
	// first-byte budget and the elapsed time; 100 once completed.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	Progress int32 `json:"progress,omitempty"`
	// QueuedAt is when the job was accepted.
	// +optional
	QueuedAt *metav1.Time `json:"queuedAt,omitempty"`
	// StartedAt is when a runner claimed it.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`
	// CompletedAt is when it reached a terminal phase.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// ExpiresAt is when the manager deletes the job and its output
	// (CompletedAt + spec.ttlAfterFinished).
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// Output is the stored response body.
	// +optional
	Output *InferenceJobObject `json:"output,omitempty"`
	// Error is set when the job failed or was incomplete.
	// +optional
	Error *InferenceJobError `json:"error,omitempty"`
	// Runner is the proxy pod that claimed the job.
	// +optional
	Runner string `json:"runner,omitempty"`
	// Lane is the Model that served the job (differs from spec.model after an
	// alias, label or reroute).
	// +optional
	Lane string `json:"lane,omitempty"`
	// Attempts counts runner claims; a job whose runner died is reclaimed.
	// +optional
	Attempts int32 `json:"attempts,omitempty"`
	// Webhook records delivery.
	// +optional
	Webhook *InferenceJobWebhookStatus `json:"webhook,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:shortName=ijob
//+kubebuilder:printcolumn:name="Model",type="string",JSONPath=".spec.model"
//+kubebuilder:printcolumn:name="Path",type="string",JSONPath=".spec.path"
//+kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
//+kubebuilder:printcolumn:name="Progress",type="integer",JSONPath=".status.progress",priority=1
//+kubebuilder:printcolumn:name="Lane",type="string",JSONPath=".status.lane",priority=1
//+kubebuilder:printcolumn:name="Runner",type="string",JSONPath=".status.runner",priority=1
//+kubebuilder:printcolumn:name="Completed",type="date",JSONPath=".status.completedAt"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// InferenceJob is the Schema for asynchronous inference requests.
type InferenceJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   InferenceJobSpec   `json:"spec,omitempty"`
	Status InferenceJobStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// InferenceJobList contains a list of InferenceJob.
type InferenceJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []InferenceJob `json:"items"`
}

func init() {
	SchemeBuilder.Register(&InferenceJob{}, &InferenceJobList{})
}
