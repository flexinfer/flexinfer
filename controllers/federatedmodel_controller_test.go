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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestMapClustersToSortedSlice(t *testing.T) {
	input := map[string]aiv1alpha2.Cluster{
		"cluster-b": {ObjectMeta: metav1.ObjectMeta{Name: "cluster-b"}},
		"cluster-a": {ObjectMeta: metav1.ObjectMeta{Name: "cluster-a"}},
	}

	out := mapClustersToSortedSlice(input)
	if len(out) != 2 {
		t.Fatalf("len(out) = %d, want 2", len(out))
	}
	if out[0].Name != "cluster-a" || out[1].Name != "cluster-b" {
		t.Fatalf("sorted order = [%s, %s], want [cluster-a, cluster-b]", out[0].Name, out[1].Name)
	}
}

func TestFederatedModelReconcile_StatusAggregation(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	replicas := int32(2)
	fm := &aiv1alpha2.FederatedModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen-global",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.FederatedModelSpec{
			Template: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
			},
			Placement: aiv1alpha2.FederatedModelPlacement{
				Clusters:           []string{"cluster-b", "cluster-a"},
				ReplicasPerCluster: &replicas,
			},
		},
	}

	clusterA := &aiv1alpha2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-a",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ClusterSpec{
			APIEndpoint: "https://cluster-a.example.com",
			SecretRef:   corev1.LocalObjectReference{Name: "cluster-a-kubeconfig"},
		},
	}
	clusterB := &aiv1alpha2.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-b",
			Namespace: "flexinfer-system",
		},
		Spec: aiv1alpha2.ClusterSpec{
			APIEndpoint: "https://cluster-b.example.com",
			SecretRef:   corev1.LocalObjectReference{Name: "cluster-b-kubeconfig"},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aiv1alpha2.FederatedModel{}).
		WithObjects(fm, clusterA, clusterB).
		Build()

	remoteModelA := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-global", Namespace: "flexinfer-system"},
		Spec:       fm.Spec.Template,
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhaseReady,
		},
	}
	remoteModelB := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "qwen-global", Namespace: "flexinfer-system"},
		Spec:       fm.Spec.Template,
		Status: aiv1alpha2.ModelStatus{
			Phase: aiv1alpha2.ModelPhasePending,
		},
	}

	remoteClientA := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		WithObjects(remoteModelA).
		Build()
	remoteClientB := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&aiv1alpha2.Model{}).
		WithObjects(remoteModelB).
		Build()

	r := &FederatedModelReconciler{
		Client: fakeClient,
		Scheme: scheme,
		RemoteClientFactory: func(ctx context.Context, cluster *aiv1alpha2.Cluster) (client.Client, error) {
			switch cluster.Name {
			case "cluster-a":
				return remoteClientA, nil
			case "cluster-b":
				return remoteClientB, nil
			default:
				return nil, fmt.Errorf("unexpected cluster %q", cluster.Name)
			}
		},
	}

	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "qwen-global",
			Namespace: "flexinfer-system",
		},
	})
	if err != nil {
		t.Fatalf("Reconcile() error: %v", err)
	}

	var got aiv1alpha2.FederatedModel
	if err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      "qwen-global",
		Namespace: "flexinfer-system",
	}, &got); err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if got.Status.TotalClusters != 2 {
		t.Fatalf("TotalClusters = %d, want 2", got.Status.TotalClusters)
	}
	if got.Status.ReadyClusters != 1 {
		t.Fatalf("ReadyClusters = %d, want 1", got.Status.ReadyClusters)
	}
	if len(got.Status.Clusters) != 2 {
		t.Fatalf("len(Clusters) = %d, want 2", len(got.Status.Clusters))
	}
	if got.Status.Clusters[0].Cluster != "cluster-a" || got.Status.Clusters[0].ReadyReplicas != 2 || got.Status.Clusters[0].Phase != string(aiv1alpha2.ModelPhaseReady) {
		t.Fatalf("cluster-a status = %+v, want ready replicas 2", got.Status.Clusters[0])
	}
	if got.Status.Clusters[1].Cluster != "cluster-b" || got.Status.Clusters[1].ReadyReplicas != 0 || got.Status.Clusters[1].Phase != string(aiv1alpha2.ModelPhasePending) {
		t.Fatalf("cluster-b status = %+v, want ready replicas 0", got.Status.Clusters[1])
	}
}
