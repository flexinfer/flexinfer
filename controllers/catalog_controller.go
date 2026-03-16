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
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/observability"
	"github.com/flexinfer/flexinfer/pkg/registry"
)

// ModelCatalogReconciler reconciles a ModelCatalog object.
type ModelCatalogReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcatalogs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=modelcatalogs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile syncs the catalog by querying all configured registries.
func (r *ModelCatalogReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, _, endSpan := observability.StartReconcileSpan(ctx, "catalog", req.Namespace, req.Name)
	defer endSpan()
	log := log.FromContext(ctx)

	catalog := &aiv1alpha2.ModelCatalog{}
	if err := r.Get(ctx, req.NamespacedName, catalog); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("Syncing model catalog", "name", catalog.Name, "registries", len(catalog.Spec.Registries))

	var allEntries []aiv1alpha2.CatalogEntryStatus
	var syncErrors []string

	for _, src := range catalog.Spec.Registries {
		regType := registryTypeFromCRD(src.Type)
		reg, err := registry.Get(regType)
		if err != nil {
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %v", regType, err))
			continue
		}

		filter := registry.ListFilter{Limit: 100}
		if catalog.Spec.Filter != nil {
			filter.Tags = catalog.Spec.Filter.Tags
			filter.Query = catalog.Spec.Filter.NamePattern
		}

		entries, err := reg.List(ctx, filter)
		if err != nil {
			log.Error(err, "failed to list models from registry", "type", regType)
			syncErrors = append(syncErrors, fmt.Sprintf("%s: %v", regType, err))
			continue
		}

		for _, e := range entries {
			allEntries = append(allEntries, aiv1alpha2.CatalogEntryStatus{
				Name:      e.Name,
				Registry:  e.Registry,
				Reference: e.Reference,
				Size:      e.Size,
			})
		}
	}

	// Update status
	now := metav1.Now()
	catalog.Status.Entries = allEntries
	catalog.Status.TotalModels = len(allEntries)
	catalog.Status.LastSyncTime = &now

	reason := "SyncSucceeded"
	message := fmt.Sprintf("synced %d models from %d registries", len(allEntries), len(catalog.Spec.Registries))
	condStatus := true
	if len(syncErrors) > 0 {
		reason = "SyncPartialFailure"
		message = fmt.Sprintf("synced %d models; errors: %s", len(allEntries), strings.Join(syncErrors, "; "))
		condStatus = len(allEntries) > 0
	}

	setCatalogCondition(catalog, "Synced", condStatus, reason, message)

	if err := r.Status().Update(ctx, catalog); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Event(catalog, "Normal", reason, message)

	syncInterval := 1 * time.Hour
	if catalog.Spec.SyncInterval != nil {
		syncInterval = catalog.Spec.SyncInterval.Duration
	}

	return ctrl.Result{RequeueAfter: syncInterval}, nil
}

func registryTypeFromCRD(t aiv1alpha2.RegistrySourceType) string {
	switch t {
	case aiv1alpha2.RegistrySourceOCI:
		return "oci"
	case aiv1alpha2.RegistrySourceHuggingFace:
		return "huggingface"
	case aiv1alpha2.RegistrySourceOllama:
		return "ollama"
	default:
		return strings.ToLower(string(t))
	}
}

func setCatalogCondition(catalog *aiv1alpha2.ModelCatalog, conditionType string, status bool, reason, message string) {
	condStatus := metav1.ConditionFalse
	if status {
		condStatus = metav1.ConditionTrue
	}

	now := metav1.Now()
	newCond := metav1.Condition{
		Type:               conditionType,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: catalog.Generation,
	}

	for i := range catalog.Status.Conditions {
		if catalog.Status.Conditions[i].Type == conditionType {
			if catalog.Status.Conditions[i].Status == condStatus {
				newCond.LastTransitionTime = catalog.Status.Conditions[i].LastTransitionTime
			}
			catalog.Status.Conditions[i] = newCond
			return
		}
	}
	catalog.Status.Conditions = append(catalog.Status.Conditions, newCond)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ModelCatalogReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.ModelCatalog{}).
		Complete(r)
}
