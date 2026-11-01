package proxy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/modelmeta"
	"github.com/flexinfer/flexinfer/pkg/validation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// OpenAIModel represents a model in OpenAI API format. FlexInfer is the cold
// capability descriptor of a v1alpha2 Model (nil for legacy ModelDeployments).
type OpenAIModel struct {
	ID        string           `json:"id"`
	Object    string           `json:"object"`
	Created   int64            `json:"created"`
	OwnedBy   string           `json:"owned_by"`
	Metadata  map[string]any   `json:"metadata,omitempty"`
	FlexInfer *ModelDescriptor `json:"flexinfer,omitempty"`
}

// ModelDescriptor is what a caller needs to decide whether and how to call a
// lane, answered from the Model CR without waking it: its state and ETA (the
// same fields as the error envelope) and what it serves and accepts
// (modelmeta.Capabilities).
type ModelDescriptor struct {
	Phase           *string `json:"phase"`
	LoadingSubstage *string `json:"loading_substage"`
	Ready           bool    `json:"ready"`
	ETASeconds      *int    `json:"eta_seconds"`
	RerouteTo       *string `json:"reroute_to"`
	modelmeta.Capabilities
}

// OpenAIModelsResponse is the response format for /v1/models.
type OpenAIModelsResponse struct {
	Object string        `json:"object"`
	Data   []OpenAIModel `json:"data"`
}

// handleModels returns OpenAI-compatible list of available models.
func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		validation.WriteMethodNotAllowed(w, r.Method)
		return
	}

	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("flexinfer/proxy").Start(ctx, "proxy.list_models")
	defer span.End()
	var models []OpenAIModel

	// List ModelDeployments (v1alpha1)
	var mds aiv1alpha1.ModelDeploymentList
	if err := p.client.List(ctx, &mds, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("error listing ModelDeployments", "error", err)
	} else {
		for _, md := range mds.Items {
			models = append(models, p.modelDeploymentToOpenAI(&md))
		}
	}

	// List Models (v1alpha2)
	var ms aiv1alpha2.ModelList
	if err := p.client.List(ctx, &ms, client.InNamespace(p.namespace)); err != nil {
		slog.Warn("error listing Models", "error", err)
	} else {
		for i := range ms.Items {
			m := &ms.Items[i]
			// Hide parked / de-advertised models (spec.litellm.enabled=false)
			// from the catalog so the router table and any prober iterating
			// /v1/models no longer discover models we do not intend to serve.
			if litellmExplicitlyDisabled(m) {
				continue
			}
			models = append(models, p.modelToOpenAI(m))
		}
	}

	response := OpenAIModelsResponse{
		Object: "list",
		Data:   models,
	}

	data, err := json.Marshal(response)
	if err != nil {
		slog.Warn("error encoding models response", "error", err)
		validation.WriteInternalError(w, "Failed to encode models response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(data); err != nil {
		slog.Warn("error writing models response", "error", err)
	}
}

// handleModel answers GET /v1/models/{name} from the Model CR: the OpenAI model
// object plus the cold capability descriptor. It never touches demand, queues
// or activates, so a caller can read a cold lane's limits and ETA for free.
// name may be a served name or alias.
func (p *Proxy) handleModel(w http.ResponseWriter, r *http.Request) {
	ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
	ctx, span := otel.Tracer("flexinfer/proxy").Start(ctx, "proxy.get_model")
	defer span.End()

	requested := r.PathValue("name")
	name := requested
	if p.resolver != nil {
		name = p.resolver.ResolveModelAlias(ctx, requested)
	}
	if !isValidModelResourceName(name) {
		validation.WriteModelNotFound(w, requested)
		return
	}
	var model OpenAIModel
	m, err := p.getModel(ctx, name)
	switch {
	case err == nil && litellmExplicitlyDisabled(m):
		p.writeLaneError(w, m, laneError{
			status:     http.StatusNotFound,
			errType:    validation.ErrorTypeNotFound,
			code:       validation.CodeModelParked,
			message:    fmt.Sprintf("Model '%s' not found: parked (spec.litellm.enabled=false) and not served by name", requested),
			param:      "model",
			retryAfter: -1,
		})
		return
	case err == nil:
		model = p.modelToOpenAI(m)
	case apierrors.IsNotFound(err):
		md, mdErr := p.getModelDeployment(ctx, name)
		if mdErr != nil {
			validation.WriteModelNotFound(w, requested)
			return
		}
		model = p.modelDeploymentToOpenAI(md)
	default:
		slog.Warn("error fetching model for descriptor", "model", name, "error", err)
		validation.WriteInternalError(w, "Internal error fetching model")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(model); err != nil {
		slog.Warn("error writing model response", "error", err)
	}
}

// modelDescriptor builds m's cold capability descriptor.
func (p *Proxy) modelDescriptor(m *aiv1alpha2.Model, now time.Time) *ModelDescriptor {
	state := p.laneDetails(m, laneError{retryAfter: -1}, now)
	return &ModelDescriptor{
		Phase:           state.Phase,
		LoadingSubstage: state.LoadingSubstage,
		Ready:           m.Status.Phase == aiv1alpha2.ModelPhaseReady,
		ETASeconds:      state.ETASeconds,
		RerouteTo:       state.RerouteTo,
		Capabilities:    modelmeta.ResolveCapabilities(m),
	}
}

// modelDeploymentToOpenAI converts a ModelDeployment to OpenAI model format.
func (p *Proxy) modelDeploymentToOpenAI(md *aiv1alpha1.ModelDeployment) OpenAIModel {
	// Determine readiness
	ready := isReady(md)
	replicas := int32(0)
	if md.Spec.Replicas != nil {
		replicas = *md.Spec.Replicas
	}

	// Build metadata
	metadata := map[string]any{
		"backend":    md.Spec.Backend,
		"ready":      ready,
		"scaled":     replicas > 0,
		"version":    "v1alpha1",
		"deprecated": true,
	}

	if md.Status.Phase != "" {
		metadata["phase"] = string(md.Status.Phase)
	}

	if md.Spec.GPUGroupRef != nil && *md.Spec.GPUGroupRef != "" {
		metadata["gpu_group"] = *md.Spec.GPUGroupRef
	}

	// Add aliases from LiteLLM spec
	if md.Spec.LiteLLM != nil {
		if md.Spec.LiteLLM.ServedModelName != "" {
			metadata["served_model_name"] = md.Spec.LiteLLM.ServedModelName
		}
		if len(md.Spec.LiteLLM.Aliases) > 0 {
			metadata["aliases"] = md.Spec.LiteLLM.Aliases
		}
	}

	// Add service labels
	if len(md.Spec.ServiceLabels) > 0 {
		metadata["service_labels"] = md.Spec.ServiceLabels
	}

	return OpenAIModel{
		ID:       md.Name,
		Object:   "model",
		Created:  md.CreationTimestamp.Unix(),
		OwnedBy:  "flexinfer",
		Metadata: metadata,
	}
}

// modelToOpenAI converts a v1alpha2 Model to OpenAI model format.
func (p *Proxy) modelToOpenAI(m *aiv1alpha2.Model) OpenAIModel {
	// Determine readiness from status
	ready := m.Status.Phase == aiv1alpha2.ModelPhaseReady

	// Build metadata
	metadata := map[string]any{
		"backend": m.Spec.Backend,
		"source":  m.Spec.Source,
		"ready":   ready,
		"version": "v1alpha2",
	}

	if m.Status.Phase != "" {
		metadata["phase"] = string(m.Status.Phase)
	}

	// GPU sharing info
	if m.Spec.GPU != nil {
		if m.Spec.GPU.Shared != "" {
			metadata["gpu_shared"] = m.Spec.GPU.Shared
		}
		if m.Spec.GPU.Priority != nil {
			metadata["gpu_priority"] = *m.Spec.GPU.Priority
		}
	}

	// Add aliases from LiteLLM spec
	if m.Spec.LiteLLM != nil {
		if m.Spec.LiteLLM.ServedModelName != "" {
			metadata["served_model_name"] = m.Spec.LiteLLM.ServedModelName
		}
		if len(m.Spec.LiteLLM.Aliases) > 0 {
			metadata["aliases"] = m.Spec.LiteLLM.Aliases
		}
	}

	// Add service labels
	if len(m.Spec.ServiceLabels) > 0 {
		metadata["service_labels"] = m.Spec.ServiceLabels
	}

	modelmeta.ApplyTokenLimitMetadata(metadata, modelmeta.ResolveTokenLimits(&m.Spec))

	return OpenAIModel{
		ID:        m.Name,
		Object:    "model",
		Created:   m.CreationTimestamp.Unix(),
		OwnedBy:   "flexinfer",
		Metadata:  metadata,
		FlexInfer: p.modelDescriptor(m, time.Now()),
	}
}
