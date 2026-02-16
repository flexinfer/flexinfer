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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metautil "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/metrics"
)

const (
	defaultClusterProbeInterval = 30 * time.Second
	defaultClusterProbeTimeout  = 10 * time.Second

	clusterConditionReady     = "Ready"
	clusterConditionSpecValid = "SpecValid"
)

var gpuResourceKeys = []corev1.ResourceName{
	corev1.ResourceName("nvidia.com/gpu"),
	corev1.ResourceName("amd.com/gpu"),
	corev1.ResourceName("gpu.intel.com/i915"),
}

// ClusterReconciler reconciles a Cluster object.
type ClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=ai.flexinfer,resources=clusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ai.flexinfer,resources=clusters/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=ai.flexinfer,resources=clusters/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

func (r *ClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cluster := &aiv1alpha2.Cluster{}
	if err := r.Get(ctx, req.NamespacedName, cluster); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	requeueAfter := defaultClusterProbeInterval
	if cluster.Spec.ProbeInterval != nil && cluster.Spec.ProbeInterval.Duration > 0 {
		requeueAfter = cluster.Spec.ProbeInterval.Duration
	}

	if err := cluster.Spec.ValidateBasic(); err != nil {
		msg := fmt.Sprintf("invalid cluster spec: %v", err)
		cluster.Status.Phase = aiv1alpha2.ClusterPhaseNotReady
		cluster.Status.Message = msg
		now := metav1.Now()
		cluster.Status.LastProbeTime = &now
		setClusterCondition(cluster, clusterConditionSpecValid, metav1.ConditionFalse, "InvalidSpec", msg)
		setClusterCondition(cluster, clusterConditionReady, metav1.ConditionFalse, "InvalidSpec", msg)
		metrics.ClusterHealth.WithLabelValues(cluster.Name, clusterRegion(cluster)).Set(0)
		if updateErr := r.Status().Update(ctx, cluster); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	start := time.Now()
	probeCtx, cancel := context.WithTimeout(ctx, defaultClusterProbeTimeout)
	defer cancel()

	observation, probeErr := r.probeCluster(probeCtx, cluster)
	metrics.ClusterProbeLatencySeconds.WithLabelValues(cluster.Name).Observe(time.Since(start).Seconds())

	now := metav1.Now()
	cluster.Status.LastProbeTime = &now
	setClusterCondition(cluster, clusterConditionSpecValid, metav1.ConditionTrue, "SpecValid", "spec validation passed")

	if probeErr != nil {
		msg := fmt.Sprintf("cluster probe failed: %v", probeErr)
		cluster.Status.Phase = aiv1alpha2.ClusterPhaseNotReady
		cluster.Status.Message = msg
		setClusterCondition(cluster, clusterConditionReady, metav1.ConditionFalse, "ProbeFailed", msg)
		metrics.ClusterHealth.WithLabelValues(cluster.Name, clusterRegion(cluster)).Set(0)
		if updateErr := r.Status().Update(ctx, cluster); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		logger.Error(probeErr, "cluster probe failed", "cluster", cluster.Name)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	cluster.Status.Phase = aiv1alpha2.ClusterPhaseReady
	cluster.Status.Message = fmt.Sprintf("probe succeeded (kubernetes %s)", observation.ServerVersion)
	cluster.Status.Capacity = observation.Capacity
	cluster.Status.Available = observation.Available
	cluster.Status.Models = observation.Models
	setClusterCondition(cluster, clusterConditionReady, metav1.ConditionTrue, "ProbeSucceeded", "cluster reachable")
	metrics.ClusterHealth.WithLabelValues(cluster.Name, clusterRegion(cluster)).Set(1)

	if err := r.Status().Update(ctx, cluster); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *ClusterReconciler) probeCluster(ctx context.Context, cluster *aiv1alpha2.Cluster) (*clusterObservation, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: cluster.Spec.SecretRef.Name, Namespace: cluster.Namespace}, secret); err != nil {
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

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	version, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("query server version: %w", err)
	}

	capacity, available, err := collectGPUInventory(ctx, clientset)
	if err != nil {
		return nil, err
	}
	models, err := collectRemoteModels(ctx, restConfig)
	if err != nil {
		return nil, err
	}

	return &clusterObservation{
		ServerVersion: version.GitVersion,
		Capacity:      capacity,
		Available:     available,
		Models:        models,
	}, nil
}

func extractKubeconfig(secret *corev1.Secret) ([]byte, error) {
	if secret == nil {
		return nil, fmt.Errorf("secret is nil")
	}
	if len(secret.Data) == 0 {
		return nil, fmt.Errorf("secret %s/%s has no data", secret.Namespace, secret.Name)
	}
	for _, key := range []string{"kubeconfig", "config", "value"} {
		if data, ok := secret.Data[key]; ok && len(data) > 0 {
			return data, nil
		}
	}
	if len(secret.Data) == 1 {
		for _, data := range secret.Data {
			if len(data) > 0 {
				return data, nil
			}
		}
	}
	return nil, fmt.Errorf("secret %s/%s does not contain kubeconfig data", secret.Namespace, secret.Name)
}

func collectGPUInventory(ctx context.Context, clientset kubernetes.Interface) (corev1.ResourceList, corev1.ResourceList, error) {
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list nodes: %w", err)
	}

	capacity := make(corev1.ResourceList, len(gpuResourceKeys))
	used := make(corev1.ResourceList, len(gpuResourceKeys))
	for _, key := range gpuResourceKeys {
		capacity[key] = *resource.NewQuantity(0, resource.DecimalSI)
		used[key] = *resource.NewQuantity(0, resource.DecimalSI)
	}

	for _, node := range nodes.Items {
		for _, key := range gpuResourceKeys {
			if qty, ok := node.Status.Allocatable[key]; ok {
				total := capacity[key]
				total.Add(qty)
				capacity[key] = total
			}
		}
	}

	pods, err := clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("list pods: %w", err)
	}

	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		for _, c := range pod.Spec.Containers {
			for _, key := range gpuResourceKeys {
				if qty, ok := c.Resources.Requests[key]; ok {
					acc := used[key]
					acc.Add(qty)
					used[key] = acc
				}
			}
		}
	}

	available := make(corev1.ResourceList, len(gpuResourceKeys))
	for _, key := range gpuResourceKeys {
		remaining := capacity[key]
		remaining.Sub(used[key])
		if remaining.Sign() < 0 {
			remaining = *resource.NewQuantity(0, resource.DecimalSI)
		}
		available[key] = remaining
	}

	return capacity, available, nil
}

func collectRemoteModels(ctx context.Context, restConfig *rest.Config) ([]aiv1alpha2.ClusterModelStatus, error) {
	dynClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create dynamic client: %w", err)
	}

	modelGVR := schema.GroupVersionResource{
		Group:    "ai.flexinfer",
		Version:  "v1alpha2",
		Resource: "models",
	}

	list, err := dynClient.Resource(modelGVR).Namespace(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Some clusters may not have v1alpha2 installed yet; treat as empty inventory.
		if errors.IsNotFound(err) || metautil.IsNoMatchError(err) {
			return []aiv1alpha2.ClusterModelStatus{}, nil
		}
		return nil, fmt.Errorf("list remote models: %w", err)
	}
	return buildClusterModelStatus(list.Items), nil
}

func buildClusterModelStatus(items []unstructured.Unstructured) []aiv1alpha2.ClusterModelStatus {
	out := make([]aiv1alpha2.ClusterModelStatus, 0, len(items))
	for _, item := range items {
		phase, _, _ := unstructured.NestedString(item.Object, "status", "phase")
		out = append(out, aiv1alpha2.ClusterModelStatus{
			Name:      item.GetName(),
			Namespace: item.GetNamespace(),
			Phase:     phase,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace == out[j].Namespace {
			return out[i].Name < out[j].Name
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}

func clusterRegion(cluster *aiv1alpha2.Cluster) string {
	if cluster == nil {
		return "unknown"
	}
	if v, ok := cluster.Spec.Labels["region"]; ok && v != "" {
		return v
	}
	return "unknown"
}

func setClusterCondition(cluster *aiv1alpha2.Cluster, conditionType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	newCond := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: cluster.Generation,
	}

	for i := range cluster.Status.Conditions {
		if cluster.Status.Conditions[i].Type == conditionType {
			if cluster.Status.Conditions[i].Status == status {
				newCond.LastTransitionTime = cluster.Status.Conditions[i].LastTransitionTime
			}
			cluster.Status.Conditions[i] = newCond
			return
		}
	}
	cluster.Status.Conditions = append(cluster.Status.Conditions, newCond)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aiv1alpha2.Cluster{}).
		Complete(r)
}

type clusterObservation struct {
	ServerVersion string
	Capacity      corev1.ResourceList
	Available     corev1.ResourceList
	Models        []aiv1alpha2.ClusterModelStatus
}
