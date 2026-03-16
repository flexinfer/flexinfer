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

package controllers

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/k8surl"
	pkgrt "github.com/flexinfer/flexinfer/pkg/runtime"
)

// loadViaRuntime sends a model load request to a runtime pod instead of
// creating a Deployment. This path is used when a persistent runtime
// container exists on the target GPU node.
func (r *ModelReconciler) loadViaRuntime(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend, gpuVendor backend.GPUVendor, endpoint *RuntimeEndpoint, gpuArch string) error {
	runtimeLog := log.FromContext(ctx)

	// Build the load request payload using shared builder.
	// Pass /models as modelBasePath so PVC sources resolve to /models/{pvc-subpath}.
	// Inject startupTimeoutSeconds from the model's coldStartTimeout so the runtime
	// uses a model-specific timeout instead of the backend default.
	config := model.Spec.GetConfigMap()
	if model.Spec.Serverless != nil && model.Spec.Serverless.ColdStartTimeout != nil {
		if config == nil {
			config = make(map[string]interface{})
		}
		config["startupTimeoutSeconds"] = model.Spec.Serverless.ColdStartTimeout.Duration.Seconds()
	}
	data, err := pkgrt.BuildLoadPayload(b.Name(), model.Spec.Source, "/models", config)
	if err != nil {
		return err
	}

	runtimeLog.Info("Sending load request to runtime",
		"model", model.Name,
		"backend", b.Name(),
		"runtimeURL", endpoint.URL(),
	)

	return r.Runtime.LoadModel(ctx, endpoint, model.Name, data)
}

// reconcileViaRuntime manages a model's lifecycle via the runtime API.
// When a runtime pod exists on the target node, this completely replaces
// the Deployment-based flow: the runtime pod already holds the GPU device.
func (r *ModelReconciler) reconcileViaRuntime(
	ctx context.Context,
	model *aiv1alpha2.Model,
	b backend.Backend,
	gpuVendor backend.GPUVendor,
	gpuArch string,
	endpoint *RuntimeEndpoint,
	desiredReplicas int32,
	cacheReady bool,
	requeueAfter time.Duration,
) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info("Reconciling model via runtime",
		"node", endpoint.NodeName,
		"runtimePod", endpoint.PodName,
		"desiredReplicas", desiredReplicas,
		"cacheReady", cacheReady,
	)

	// Ensure a Service exists for proxy routing (annotations for LiteLLM, etc.).
	if err := r.ensureService(ctx, model, b); err != nil {
		log.Error(err, "Failed to ensure Service for runtime model")
		return ctrl.Result{}, err
	}

	// If we don't want the model running, unload it from the runtime.
	if desiredReplicas == 0 || !cacheReady {
		r.unloadFromRuntime(ctx, model, endpoint)
		if model.Status.Phase != aiv1alpha2.ModelPhasePreempted {
			phase := aiv1alpha2.ModelPhaseIdle
			if !cacheReady {
				phase = aiv1alpha2.ModelPhasePending
			}
			if err := r.updatePhase(ctx, model, phase); err != nil {
				log.Error(err, "Failed to update phase")
			}
		}
		// Clean up any Endpoints that pointed to a previous runtime session.
		r.removeRuntimeEndpoints(ctx, model)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	// desiredReplicas > 0 and cache is ready — check if model is loaded.
	status, err := r.Runtime.CheckModelHealth(ctx, endpoint, model.Name)
	if err != nil {
		log.Error(err, "Failed to check runtime model health")
		status = nil // Can't determine state — try to load anyway.
	}

	if status == nil {
		// Model not loaded — send load request.
		if err := r.loadViaRuntime(ctx, model, b, gpuVendor, endpoint, gpuArch); err != nil {
			log.Error(err, "Failed to load model via runtime")
			r.Recorder.Event(model, corev1.EventTypeWarning, "RuntimeLoadFailed", err.Error())
			model.Status.Phase = aiv1alpha2.ModelPhaseFailed
			if err := r.Status().Update(ctx, model); err != nil {
				log.Error(err, "failed to update Model status", "model", model.Name)
			}
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		if err := r.updatePhase(ctx, model, aiv1alpha2.ModelPhaseLoading); err != nil {
			log.Error(err, "Failed to update phase after runtime load")
		}
		// Requeue quickly to poll for readiness.
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Model is loaded — update status based on runtime state.
	switch status.State {
	case "Ready":
		// Update Endpoints to point to the runtime pod's backend port.
		port := b.Port()
		if err := r.ensureRuntimeEndpoints(ctx, model, endpoint.PodIP, port); err != nil {
			log.Error(err, "Failed to ensure runtime endpoints")
			return ctrl.Result{}, err
		}
		model.Status.Endpoint = k8surl.ServiceURL(model.Name, model.Namespace, port, false)
		setModelCondition(model, aiv1alpha2.ConditionModelReady, true, "RuntimeReady", "Model ready via runtime")
		if model.Status.Phase != aiv1alpha2.ModelPhaseReady {
			r.Recorder.Event(model, corev1.EventTypeNormal, "RuntimeReady", "Model is ready via runtime")
		}
		model.Status.Phase = aiv1alpha2.ModelPhaseReady
		if err := r.Status().Update(ctx, model); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	case "Loading":
		if err := r.updatePhase(ctx, model, aiv1alpha2.ModelPhaseLoading); err != nil {
			log.Error(err, "Failed to update phase")
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	case "Failed":
		model.Status.Phase = aiv1alpha2.ModelPhaseFailed
		setModelCondition(model, aiv1alpha2.ConditionModelReady, false, "RuntimeFailed", status.Error)
		r.Recorder.Event(model, corev1.EventTypeWarning, "RuntimeFailed", status.Error)
		if err := r.Status().Update(ctx, model); err != nil {
			log.Error(err, "Failed to update status")
			return ctrl.Result{}, err
		}
	default:
		log.Info("Unknown runtime model state", "state", status.State)
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// unloadFromRuntime sends an unload request to the runtime if the model is loaded.
func (r *ModelReconciler) unloadFromRuntime(ctx context.Context, model *aiv1alpha2.Model, endpoint *RuntimeEndpoint) {
	log := log.FromContext(ctx)

	// Check if the model is actually loaded.
	status, err := r.Runtime.CheckModelHealth(ctx, endpoint, model.Name)
	if err != nil {
		log.V(1).Info("Could not check runtime model health for unload", "error", err)
	}
	if status == nil {
		return // Not loaded — nothing to do.
	}

	log.Info("Unloading model from runtime", "model", model.Name)
	if err := r.Runtime.UnloadModel(ctx, endpoint, model.Name); err != nil {
		log.Error(err, "Failed to unload model from runtime")
	}
}

// ensureRuntimeEndpoints creates or updates an Endpoints resource for a
// runtime-managed model, pointing to the runtime pod's backend port.
// The model's Service must not have a selector for this to take effect.
func (r *ModelReconciler) ensureRuntimeEndpoints(ctx context.Context, model *aiv1alpha2.Model, podIP string, port int32) error {
	log := log.FromContext(ctx)

	// First, ensure the Service has no selector (runtime manages endpoints manually).
	svc := &corev1.Service{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, svc); err != nil {
		return fmt.Errorf("getting service for runtime endpoints: %w", err)
	}
	if svc.Spec.Selector != nil {
		svc.Spec.Selector = nil
		if err := r.Update(ctx, svc); err != nil {
			return fmt.Errorf("removing selector from service: %w", err)
		}
		log.Info("Removed selector from Service for runtime management", "service", svc.Name)
	}

	// Build desired Endpoints.
	desired := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      model.Name,
			Namespace: model.Namespace,
			Labels:    r.labelsForModel(model),
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: podIP},
				},
				Ports: []corev1.EndpointPort{
					{
						Name:     "http",
						Port:     port,
						Protocol: corev1.ProtocolTCP,
					},
				},
			},
		},
	}

	existing := &corev1.Endpoints{}
	err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, existing)
	if errors.IsNotFound(err) {
		log.Info("Creating runtime Endpoints", "name", model.Name, "podIP", podIP, "port", port)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}

	// Update if subsets differ.
	if !apiequality.Semantic.DeepEqual(existing.Subsets, desired.Subsets) {
		existing.Subsets = desired.Subsets
		log.Info("Updating runtime Endpoints", "name", model.Name, "podIP", podIP)
		return r.Update(ctx, existing)
	}

	return nil
}

// removeRuntimeEndpoints clears the Endpoints subsets for a runtime-managed
// model so the proxy stops routing to it.
func (r *ModelReconciler) removeRuntimeEndpoints(ctx context.Context, model *aiv1alpha2.Model) {
	log := log.FromContext(ctx)

	ep := &corev1.Endpoints{}
	if err := r.Get(ctx, types.NamespacedName{Name: model.Name, Namespace: model.Namespace}, ep); err != nil {
		return // Not found or error — nothing to clean up.
	}

	if len(ep.Subsets) > 0 {
		ep.Subsets = nil
		if err := r.Update(ctx, ep); err != nil {
			log.Error(err, "Failed to clear runtime endpoints", "model", model.Name)
		}
	}
}
