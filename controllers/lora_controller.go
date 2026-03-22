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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/backend"
	"github.com/flexinfer/flexinfer/pkg/observability"
)

// LoRAAdapterReconciler reconciles a LoRAAdapter object.
type LoRAAdapterReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	HTTPClient *http.Client
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=loraadapters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=loraadapters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=loraadapters/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=endpoints,verbs=get;list;watch

func (r *LoRAAdapterReconciler) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	return &http.Client{Timeout: httpClientTimeout}
}

// Reconcile manages the lifecycle of a LoRA adapter.
func (r *LoRAAdapterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, _, endSpan := observability.StartReconcileSpan(ctx, "lora", req.Namespace, req.Name)
	defer endSpan()
	log := log.FromContext(ctx)

	adapter := &aiv1alpha2.LoRAAdapter{}
	if err := r.Get(ctx, req.NamespacedName, adapter); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle finalizer for cleanup
	if adapter.DeletionTimestamp.IsZero() {
		if !slices.Contains(adapter.GetFinalizers(), aiv1alpha2.LoRAAdapterFinalizer) {
			adapter.SetFinalizers(append(adapter.GetFinalizers(), aiv1alpha2.LoRAAdapterFinalizer))
			if err := r.Update(ctx, adapter); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		// Object is being deleted — unload from all replicas
		if slices.Contains(adapter.GetFinalizers(), aiv1alpha2.LoRAAdapterFinalizer) {
			r.unloadFromAllReplicas(ctx, adapter)
			adapter.SetFinalizers(slices.DeleteFunc(adapter.GetFinalizers(), func(v string) bool { return v == aiv1alpha2.LoRAAdapterFinalizer }))
			if err := r.Update(ctx, adapter); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Fetch parent model
	model := &aiv1alpha2.Model{}
	if err := r.Get(ctx, client.ObjectKey{Name: adapter.Spec.ModelRef, Namespace: adapter.Namespace}, model); err != nil {
		if errors.IsNotFound(err) {
			adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhaseFailed
			adapter.Status.Message = fmt.Sprintf("parent model %q not found", adapter.Spec.ModelRef)
			r.Recorder.Event(adapter, corev1.EventTypeWarning, "ModelNotFound", adapter.Status.Message)
			if err := r.Status().Update(ctx, adapter); err != nil {
				log.Error(err, "failed to update LoRAAdapter status")
			}
			return ctrl.Result{RequeueAfter: requeueLong}, nil
		}
		return ctrl.Result{}, err
	}

	// Check backend supports LoRA
	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhaseFailed
		adapter.Status.Message = fmt.Sprintf("%v: %s (on parent model)", backend.ErrUnknownBackend, model.Spec.Backend)
		if err := r.Status().Update(ctx, adapter); err != nil {
			log.Error(err, "failed to update LoRAAdapter status")
		}
		return ctrl.Result{}, nil
	}

	ls, isLoRASupporter := b.(backend.LoRASupporter)
	if !isLoRASupporter || !ls.SupportsLoRA() {
		adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhaseFailed
		adapter.Status.Message = fmt.Sprintf("backend %q does not support LoRA", model.Spec.Backend)
		r.Recorder.Event(adapter, corev1.EventTypeWarning, "BackendUnsupported", adapter.Status.Message)
		if err := r.Status().Update(ctx, adapter); err != nil {
			log.Error(err, "failed to update LoRAAdapter status")
		}
		return ctrl.Result{}, nil
	}

	// Model must be running to load adapters
	if model.Status.Phase != aiv1alpha2.ModelPhaseReady {
		adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhasePending
		adapter.Status.Message = "waiting for parent model to be ready"
		if err := r.Status().Update(ctx, adapter); err != nil {
			log.Error(err, "failed to update LoRAAdapter status")
		}
		return ctrl.Result{RequeueAfter: requeueMedium}, nil
	}

	// Get pod endpoints for the model
	podAddresses := r.getModelPodAddresses(ctx, model, b)
	adapter.Status.TotalReplicas = int32(len(podAddresses))

	if len(podAddresses) == 0 {
		adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhasePending
		adapter.Status.Message = "no ready pods for parent model"
		if err := r.Status().Update(ctx, adapter); err != nil {
			log.Error(err, "failed to update LoRAAdapter status")
		}
		return ctrl.Result{RequeueAfter: requeueMedium}, nil
	}

	// Load adapter on all replicas
	loaded := int32(0)
	var lastErr error
	for _, addr := range podAddresses {
		if err := r.loadAdapterOnPod(ctx, addr, adapter, ls); err != nil {
			log.Error(err, "failed to load LoRA adapter on pod", "pod", addr, "adapter", adapter.Spec.AdapterName)
			lastErr = err
		} else {
			loaded++
		}
	}

	adapter.Status.LoadedReplicas = loaded

	if loaded == adapter.Status.TotalReplicas {
		adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhaseLoaded
		adapter.Status.Message = fmt.Sprintf("loaded on %d/%d replicas", loaded, adapter.Status.TotalReplicas)
		r.Recorder.Event(adapter, corev1.EventTypeNormal, "Loaded", adapter.Status.Message)
	} else if loaded > 0 {
		adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhaseLoaded
		adapter.Status.Message = fmt.Sprintf("partially loaded on %d/%d replicas", loaded, adapter.Status.TotalReplicas)
	} else {
		adapter.Status.Phase = aiv1alpha2.LoRAAdapterPhaseFailed
		msg := "failed to load on any replica"
		if lastErr != nil {
			msg = fmt.Sprintf("failed to load on any replica: %v", lastErr)
		}
		adapter.Status.Message = msg
		r.Recorder.Event(adapter, corev1.EventTypeWarning, "LoadFailed", adapter.Status.Message)
	}

	if err := r.Status().Update(ctx, adapter); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
}

// getModelPodAddresses returns the IP:port addresses of all ready pods for a model.
func (r *LoRAAdapterReconciler) getModelPodAddresses(ctx context.Context, model *aiv1alpha2.Model, b backend.Backend) []string {
	var endpoints corev1.Endpoints
	if err := r.Get(ctx, client.ObjectKey{Name: model.Name, Namespace: model.Namespace}, &endpoints); err != nil {
		return nil
	}

	port := b.Port()
	var addresses []string
	for _, subset := range endpoints.Subsets {
		for _, p := range subset.Ports {
			port = p.Port
			break
		}
		for _, addr := range subset.Addresses {
			addresses = append(addresses, fmt.Sprintf("%s:%d", addr.IP, port))
		}
	}
	return addresses
}

// loadAdapterOnPod sends a POST to the vLLM LoRA load endpoint on a specific pod.
func (r *LoRAAdapterReconciler) loadAdapterOnPod(ctx context.Context, podAddr string, adapter *aiv1alpha2.LoRAAdapter, ls backend.LoRASupporter) error {
	payload := map[string]interface{}{
		"lora_name": adapter.Spec.AdapterName,
		"lora_path": adapter.Spec.Source.URI,
	}
	if adapter.Spec.MaxRank != nil {
		payload["max_lora_rank"] = *adapter.Spec.MaxRank
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	url := fmt.Sprintf("http://%s%s", podAddr, ls.LoadLoRAEndpoint())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("HTTP request to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("load adapter returned %d: %s", resp.StatusCode, string(respBody))
}

// unloadFromAllReplicas attempts to unload the adapter from all model replicas.
func (r *LoRAAdapterReconciler) unloadFromAllReplicas(ctx context.Context, adapter *aiv1alpha2.LoRAAdapter) {
	log := log.FromContext(ctx)

	model := &aiv1alpha2.Model{}
	if err := r.Get(ctx, client.ObjectKey{Name: adapter.Spec.ModelRef, Namespace: adapter.Namespace}, model); err != nil {
		log.V(1).Info("parent model not found during cleanup", "model", adapter.Spec.ModelRef)
		return
	}

	b, ok := backend.Get(model.Spec.Backend)
	if !ok {
		return
	}

	ls, isLoRASupporter := b.(backend.LoRASupporter)
	if !isLoRASupporter || !ls.SupportsLoRA() {
		return
	}

	podAddresses := r.getModelPodAddresses(ctx, model, b)
	for _, addr := range podAddresses {
		if err := r.unloadAdapterFromPod(ctx, addr, adapter, ls); err != nil {
			log.V(1).Info("failed to unload adapter during cleanup", "pod", addr, "error", err)
		}
	}
}

// unloadAdapterFromPod sends a POST to the vLLM LoRA unload endpoint.
func (r *LoRAAdapterReconciler) unloadAdapterFromPod(ctx context.Context, podAddr string, adapter *aiv1alpha2.LoRAAdapter, ls backend.LoRASupporter) error {
	payload := map[string]interface{}{
		"lora_name": adapter.Spec.AdapterName,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("http://%s%s", podAddr, ls.UnloadLoRAEndpoint())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("unload adapter returned %d: %s", resp.StatusCode, string(respBody))
}

// SetupWithManager sets up the controller with the Manager.
func (r *LoRAAdapterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.LoRAAdapter{}).
		Complete(r)
}
