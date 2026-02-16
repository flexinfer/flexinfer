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
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const federatedModelRequeueInterval = 30 * time.Second

type remoteClientFactory func(ctx context.Context, cluster *aiv1alpha2.Cluster) (client.Client, error)

// FederatedModelReconciler reconciles a FederatedModel object.
type FederatedModelReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	RemoteClientFactory remoteClientFactory
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=federatedmodels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=federatedmodels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=federatedmodels/finalizers,verbs=update
//+kubebuilder:rbac:groups=ai.flexinfer,resources=clusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=models,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

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

	clusterStatus, syncErr := r.syncFederatedModel(ctx, fm, targets)
	fm.Status.Clusters = clusterStatus
	fm.Status.TotalClusters = int32(len(clusterStatus))

	var readyCount int32
	for _, cs := range clusterStatus {
		if cs.TotalReplicas > 0 && cs.ReadyReplicas >= cs.TotalReplicas {
			readyCount++
		}
	}
	fm.Status.ReadyClusters = readyCount

	setFederatedModelCondition(fm, "SpecValid", metav1.ConditionTrue, "SpecValid", "spec validation passed")
	setFederatedModelCondition(fm, "ClusterPlacementResolved", metav1.ConditionTrue, "PlacementResolved", fmt.Sprintf("resolved %d target clusters", len(clusterStatus)))
	if syncErr != nil {
		setFederatedModelCondition(fm, "ModelSync", metav1.ConditionFalse, "ModelSyncFailed", syncErr.Error())
	} else {
		setFederatedModelCondition(fm, "ModelSync", metav1.ConditionTrue, "ModelSyncSucceeded", "remote models synchronized")
	}

	if syncErr == nil && fm.Status.TotalClusters > 0 && fm.Status.TotalClusters == fm.Status.ReadyClusters {
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

func (r *FederatedModelReconciler) syncFederatedModel(ctx context.Context, fm *aiv1alpha2.FederatedModel, targets []aiv1alpha2.Cluster) ([]aiv1alpha2.FederatedModelClusterStatus, error) {
	targetSet := make(map[string]struct{}, len(targets))
	statuses := make([]aiv1alpha2.FederatedModelClusterStatus, 0, len(targets))
	errorsList := make([]string, 0)

	for i := range targets {
		target := targets[i]
		targetSet[target.Name] = struct{}{}

		status, err := r.syncRemoteModelForCluster(ctx, fm, &target)
		if err != nil {
			errorsList = append(errorsList, fmt.Sprintf("%s: %v", target.Name, err))
		}
		statuses = append(statuses, status)
	}

	if err := r.cleanupRemovedRemoteModels(ctx, fm, targetSet); err != nil {
		errorsList = append(errorsList, err.Error())
	}

	sort.Slice(statuses, func(i, j int) bool {
		return statuses[i].Cluster < statuses[j].Cluster
	})

	if len(errorsList) > 0 {
		return statuses, errors.New(strings.Join(errorsList, "; "))
	}
	return statuses, nil
}

func (r *FederatedModelReconciler) syncRemoteModelForCluster(ctx context.Context, fm *aiv1alpha2.FederatedModel, cluster *aiv1alpha2.Cluster) (aiv1alpha2.FederatedModelClusterStatus, error) {
	replicas := int32(1)
	if fm.Spec.Placement.ReplicasPerCluster != nil {
		replicas = *fm.Spec.Placement.ReplicasPerCluster
	}

	status := aiv1alpha2.FederatedModelClusterStatus{
		Cluster:       cluster.Name,
		Phase:         "Pending",
		ReadyReplicas: 0,
		TotalReplicas: replicas,
	}

	remoteClient, err := r.remoteClientForCluster(ctx, cluster)
	if err != nil {
		status.Phase = "Error"
		return status, err
	}

	key := client.ObjectKey{Name: fm.Name, Namespace: fm.Namespace}
	desired := desiredRemoteModel(fm)
	existing := &aiv1alpha2.Model{}

	if err := remoteClient.Get(ctx, key, existing); err != nil {
		if !apierrors.IsNotFound(err) {
			status.Phase = "Error"
			return status, fmt.Errorf("get remote model: %w", err)
		}
		if err := remoteClient.Create(ctx, desired); err != nil {
			status.Phase = "Error"
			return status, fmt.Errorf("create remote model: %w", err)
		}
		existing = desired.DeepCopy()
	} else if needsRemoteModelUpdate(existing, desired) {
		existing.Spec = desired.Spec
		existing.Labels = desired.Labels
		existing.Annotations = desired.Annotations
		if err := remoteClient.Update(ctx, existing); err != nil {
			status.Phase = "Error"
			return status, fmt.Errorf("update remote model: %w", err)
		}
	}

	if err := remoteClient.Get(ctx, key, existing); err == nil {
		phase := string(existing.Status.Phase)
		if phase == "" {
			phase = string(aiv1alpha2.ModelPhasePending)
		}
		status.Phase = phase
		if existing.Status.Phase == aiv1alpha2.ModelPhaseReady {
			status.ReadyReplicas = replicas
		}
	}

	return status, nil
}

func (r *FederatedModelReconciler) cleanupRemovedRemoteModels(ctx context.Context, fm *aiv1alpha2.FederatedModel, targetSet map[string]struct{}) error {
	if len(fm.Status.Clusters) == 0 {
		return nil
	}

	errorsList := make([]string, 0)
	for _, prev := range fm.Status.Clusters {
		if _, keep := targetSet[prev.Cluster]; keep {
			continue
		}

		cluster := &aiv1alpha2.Cluster{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: fm.Namespace, Name: prev.Cluster}, cluster); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			errorsList = append(errorsList, fmt.Sprintf("resolve removed cluster %s: %v", prev.Cluster, err))
			continue
		}

		remoteClient, err := r.remoteClientForCluster(ctx, cluster)
		if err != nil {
			errorsList = append(errorsList, fmt.Sprintf("remote client for removed cluster %s: %v", prev.Cluster, err))
			continue
		}

		toDelete := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: fm.Name, Namespace: fm.Namespace}}
		if err := remoteClient.Delete(ctx, toDelete); err != nil && !apierrors.IsNotFound(err) {
			errorsList = append(errorsList, fmt.Sprintf("delete remote model on %s: %v", prev.Cluster, err))
		}
	}

	if len(errorsList) > 0 {
		return errors.New(strings.Join(errorsList, "; "))
	}
	return nil
}

func (r *FederatedModelReconciler) remoteClientForCluster(ctx context.Context, cluster *aiv1alpha2.Cluster) (client.Client, error) {
	if r.RemoteClientFactory != nil {
		return r.RemoteClientFactory(ctx, cluster)
	}
	return r.buildRemoteClient(ctx, cluster)
}

func (r *FederatedModelReconciler) buildRemoteClient(ctx context.Context, cluster *aiv1alpha2.Cluster) (client.Client, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Spec.SecretRef.Name}, secret); err != nil {
		return nil, fmt.Errorf("read kubeconfig secret: %w", err)
	}

	kubeconfig, err := extractKubeconfig(secret)
	if err != nil {
		return nil, err
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}
	restConfig.Host = cluster.Spec.NormalizedAPIEndpoint()
	restConfig.Timeout = defaultClusterProbeTimeout

	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add ai v1alpha2 scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("add core v1 scheme: %w", err)
	}

	remoteClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("create remote client: %w", err)
	}
	return remoteClient, nil
}

func desiredRemoteModel(fm *aiv1alpha2.FederatedModel) *aiv1alpha2.Model {
	labels := map[string]string{
		"flexinfer.ai/federated-model": fm.Name,
	}
	for k, v := range fm.Labels {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}

	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fm.Name,
			Namespace:   fm.Namespace,
			Labels:      labels,
			Annotations: fm.Annotations,
		},
		Spec: fm.Spec.Template,
	}
}

func needsRemoteModelUpdate(current, desired *aiv1alpha2.Model) bool {
	if !reflect.DeepEqual(current.Spec, desired.Spec) {
		return true
	}
	if !reflect.DeepEqual(current.Labels, desired.Labels) {
		return true
	}
	if !reflect.DeepEqual(current.Annotations, desired.Annotations) {
		return true
	}
	return false
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
