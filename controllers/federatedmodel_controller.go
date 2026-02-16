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
	"sort"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const federatedModelRequeueInterval = 30 * time.Second

// FederatedModelReconciler reconciles a FederatedModel object.
type FederatedModelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=federatedmodels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=federatedmodels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=federatedmodels/finalizers,verbs=update
//+kubebuilder:rbac:groups=ai.flexinfer,resources=clusters,verbs=get;list;watch

func (r *FederatedModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	fm := &aiv1alpha2.FederatedModel{}
	if err := r.Get(ctx, req.NamespacedName, fm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if err := fm.Spec.ValidateBasic(); err != nil {
		setFederatedModelCondition(fm, "SpecValid", metav1.ConditionFalse, "InvalidSpec", err.Error())
		setFederatedModelCondition(fm, "Ready", metav1.ConditionFalse, "InvalidSpec", "federated model spec invalid")
		fm.Status.TotalClusters = 0
		fm.Status.ReadyClusters = 0
		fm.Status.Clusters = nil
		if updateErr := r.Status().Update(ctx, fm); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{RequeueAfter: federatedModelRequeueInterval}, nil
	}

	targets, err := r.resolvePlacementClusters(ctx, fm)
	if err != nil {
		setFederatedModelCondition(fm, "ClusterPlacementResolved", metav1.ConditionFalse, "PlacementResolveFailed", err.Error())
		setFederatedModelCondition(fm, "Ready", metav1.ConditionFalse, "PlacementResolveFailed", "failed to resolve placement")
		if updateErr := r.Status().Update(ctx, fm); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{RequeueAfter: federatedModelRequeueInterval}, nil
	}

	replicas := int32(1)
	if fm.Spec.Placement.ReplicasPerCluster != nil {
		replicas = *fm.Spec.Placement.ReplicasPerCluster
	}

	clusterStatus := make([]aiv1alpha2.FederatedModelClusterStatus, 0, len(targets))
	var readyCount int32
	for _, c := range targets {
		readyReplicas := int32(0)
		if c.Status.Phase == aiv1alpha2.ClusterPhaseReady {
			readyReplicas = replicas
			readyCount++
		}
		clusterStatus = append(clusterStatus, aiv1alpha2.FederatedModelClusterStatus{
			Cluster:       c.Name,
			Phase:         string(c.Status.Phase),
			ReadyReplicas: readyReplicas,
			TotalReplicas: replicas,
		})
	}

	fm.Status.Clusters = clusterStatus
	fm.Status.TotalClusters = int32(len(clusterStatus))
	fm.Status.ReadyClusters = readyCount
	setFederatedModelCondition(fm, "SpecValid", metav1.ConditionTrue, "SpecValid", "spec validation passed")
	setFederatedModelCondition(fm, "ClusterPlacementResolved", metav1.ConditionTrue, "PlacementResolved", fmt.Sprintf("resolved %d target clusters", len(clusterStatus)))
	if fm.Status.TotalClusters > 0 && fm.Status.TotalClusters == fm.Status.ReadyClusters {
		setFederatedModelCondition(fm, "Ready", metav1.ConditionTrue, "AllClustersReady", "all selected clusters are ready")
	} else {
		setFederatedModelCondition(fm, "Ready", metav1.ConditionFalse, "ClustersNotReady", "waiting for selected clusters to become ready")
	}

	if err := r.Status().Update(ctx, fm); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: federatedModelRequeueInterval}, nil
}

func (r *FederatedModelReconciler) resolvePlacementClusters(ctx context.Context, fm *aiv1alpha2.FederatedModel) ([]aiv1alpha2.Cluster, error) {
	out := make(map[string]aiv1alpha2.Cluster)

	for _, name := range fm.Spec.Placement.Clusters {
		c := &aiv1alpha2.Cluster{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: fm.Namespace, Name: name}, c); err != nil {
			return nil, fmt.Errorf("get cluster %q: %w", name, err)
		}
		out[c.Name] = *c
	}

	if fm.Spec.Placement.ClusterSelector != nil {
		sel, err := metav1.LabelSelectorAsSelector(fm.Spec.Placement.ClusterSelector)
		if err != nil {
			return nil, fmt.Errorf("invalid clusterSelector: %w", err)
		}

		var clusters aiv1alpha2.ClusterList
		if err := r.List(ctx, &clusters, client.InNamespace(fm.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
			return nil, fmt.Errorf("list clusters by selector: %w", err)
		}
		for i := range clusters.Items {
			out[clusters.Items[i].Name] = clusters.Items[i]
		}
	}

	return mapClustersToSortedSlice(out), nil
}

func mapClustersToSortedSlice(items map[string]aiv1alpha2.Cluster) []aiv1alpha2.Cluster {
	if len(items) == 0 {
		return nil
	}
	names := make([]string, 0, len(items))
	for name := range items {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]aiv1alpha2.Cluster, 0, len(names))
	for _, name := range names {
		out = append(out, items[name])
	}
	return out
}

func setFederatedModelCondition(fm *aiv1alpha2.FederatedModel, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	newCond := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: fm.Generation,
	}

	for i := range fm.Status.Conditions {
		if fm.Status.Conditions[i].Type == conditionType {
			if fm.Status.Conditions[i].Status == status {
				newCond.LastTransitionTime = fm.Status.Conditions[i].LastTransitionTime
			}
			fm.Status.Conditions[i] = newCond
			return
		}
	}
	fm.Status.Conditions = append(fm.Status.Conditions, newCond)
}

// SetupWithManager sets up the controller with the Manager.
func (r *FederatedModelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.FederatedModel{}).
		Complete(r)
}
