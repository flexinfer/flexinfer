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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestExtractKubeconfig(t *testing.T) {
	tests := []struct {
		name    string
		secret  *corev1.Secret
		wantErr bool
	}{
		{
			name: "kubeconfig key",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "flexinfer-system"},
				Data:       map[string][]byte{"kubeconfig": []byte("apiVersion: v1")},
			},
			wantErr: false,
		},
		{
			name: "single data key fallback",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-b", Namespace: "flexinfer-system"},
				Data:       map[string][]byte{"remote-config": []byte("apiVersion: v1")},
			},
			wantErr: false,
		},
		{
			name: "empty data",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-c", Namespace: "flexinfer-system"},
				Data:       map[string][]byte{},
			},
			wantErr: true,
		},
		{
			name: "missing preferred keys with multiple entries",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-d", Namespace: "flexinfer-system"},
				Data: map[string][]byte{
					"a": []byte("x"),
					"b": []byte("y"),
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := extractKubeconfig(tc.secret)
			if tc.wantErr && err == nil {
				t.Fatal("extractKubeconfig() error = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("extractKubeconfig() unexpected error: %v", err)
			}
		})
	}
}

func TestCollectGPUInventory(t *testing.T) {
	ctx := context.Background()

	nodeA := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-a"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resource.MustParse("2"),
			},
		},
	}
	nodeB := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-b"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			},
		},
	}

	runningPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "tenant-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "inference",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	succeededPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "done-job", Namespace: "tenant-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodSucceeded,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "ignored",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	clientset := k8sfake.NewSimpleClientset(nodeA, nodeB, runningPod, succeededPod)
	capacity, available, err := collectGPUInventory(ctx, clientset)
	if err != nil {
		t.Fatalf("collectGPUInventory() unexpected error: %v", err)
	}

	amdCap := capacity[corev1.ResourceName("amd.com/gpu")]
	if got := amdCap.String(); got != "2" {
		t.Fatalf("amd capacity = %s, want 2", got)
	}
	nvidiaCap := capacity[corev1.ResourceName("nvidia.com/gpu")]
	if got := nvidiaCap.String(); got != "1" {
		t.Fatalf("nvidia capacity = %s, want 1", got)
	}

	amdAvail := available[corev1.ResourceName("amd.com/gpu")]
	if got := amdAvail.String(); got != "1" {
		t.Fatalf("amd available = %s, want 1", got)
	}
	nvidiaAvail := available[corev1.ResourceName("nvidia.com/gpu")]
	if got := nvidiaAvail.String(); got != "1" {
		t.Fatalf("nvidia available = %s, want 1", got)
	}
}

func TestBuildClusterModelStatusSorted(t *testing.T) {
	items := []unstructured.Unstructured{
		{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "zeta",
					"namespace": "ns-b",
				},
				"status": map[string]interface{}{
					"phase": "Ready",
				},
			},
		},
		{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "alpha",
					"namespace": "ns-a",
				},
				"status": map[string]interface{}{
					"phase": "Pending",
				},
			},
		},
	}

	got := buildClusterModelStatus(items)
	if len(got) != 2 {
		t.Fatalf("buildClusterModelStatus() len = %d, want 2", len(got))
	}
	if got[0].Namespace != "ns-a" || got[0].Name != "alpha" || got[0].Phase != "Pending" {
		t.Fatalf("first model = %+v, want ns-a/alpha Pending", got[0])
	}
	if got[1].Namespace != "ns-b" || got[1].Name != "zeta" || got[1].Phase != "Ready" {
		t.Fatalf("second model = %+v, want ns-b/zeta Ready", got[1])
	}
}

func TestRemoteModelWatchSnapshotAndEvents(t *testing.T) {
	w := newRemoteModelWatch("cfg", func() {})
	if _, ok := w.snapshot(); ok {
		t.Fatal("snapshot should not be ready before initial list")
	}

	initial := []unstructured.Unstructured{
		{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "zeta",
					"namespace": "ns-b",
				},
				"status": map[string]interface{}{
					"phase": "Pending",
				},
			},
		},
		{
			Object: map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":      "alpha",
					"namespace": "ns-a",
				},
				"status": map[string]interface{}{
					"phase": "Loading",
				},
			},
		},
	}
	w.replaceFromList(initial)

	got, ok := w.snapshot()
	if !ok {
		t.Fatal("snapshot should be ready after initial list")
	}
	if len(got) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(got))
	}
	if got[0].Namespace != "ns-a" || got[0].Name != "alpha" || got[0].Phase != "Loading" {
		t.Fatalf("first model = %+v, want ns-a/alpha Loading", got[0])
	}

	modified := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "alpha",
				"namespace": "ns-a",
			},
			"status": map[string]interface{}{
				"phase": "Ready",
			},
		},
	}
	w.applyWatchEvent(watch.Event{Type: watch.Modified, Object: modified})
	deleted := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"metadata": map[string]interface{}{
				"name":      "zeta",
				"namespace": "ns-b",
			},
		},
	}
	w.applyWatchEvent(watch.Event{Type: watch.Deleted, Object: deleted})

	got, ok = w.snapshot()
	if !ok {
		t.Fatal("snapshot should remain ready after watch events")
	}
	if len(got) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(got))
	}
	if got[0].Namespace != "ns-a" || got[0].Name != "alpha" || got[0].Phase != "Ready" {
		t.Fatalf("remaining model = %+v, want ns-a/alpha Ready", got[0])
	}
}

func TestStopRemoteModelWatch(t *testing.T) {
	canceled := false
	r := &ClusterReconciler{
		modelWatches: map[string]*remoteModelWatch{
			"flexinfer-system/cluster-a": newRemoteModelWatch("cfg", func() { canceled = true }),
		},
	}

	r.stopRemoteModelWatch("flexinfer-system/cluster-a")
	if !canceled {
		t.Fatal("expected watch cancel to be invoked")
	}
	if _, ok := r.modelWatches["flexinfer-system/cluster-a"]; ok {
		t.Fatal("expected watch entry to be removed")
	}
}

func TestIndexClusterSecretRefName(t *testing.T) {
	cluster := &aiv1alpha2.Cluster{
		Spec: aiv1alpha2.ClusterSpec{
			SecretRef: corev1.LocalObjectReference{Name: "remote-kubeconfig"},
		},
	}

	got := indexClusterSecretRefName(cluster)
	if len(got) != 1 || got[0] != "remote-kubeconfig" {
		t.Fatalf("indexClusterSecretRefName() = %v, want [remote-kubeconfig]", got)
	}
}

func TestRequestsForSecret_FallbackWithoutFieldIndexer(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("Add corev1 scheme: %v", err)
	}
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("Add aiv1alpha2 scheme: %v", err)
	}

	clusterA := &aiv1alpha2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ClusterSpec{
			APIEndpoint: "https://cluster-a.example.com",
			SecretRef:   corev1.LocalObjectReference{Name: "shared-kubeconfig"},
		},
	}
	clusterB := &aiv1alpha2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-b", Namespace: "flexinfer-system"},
		Spec: aiv1alpha2.ClusterSpec{
			APIEndpoint: "https://cluster-b.example.com",
			SecretRef:   corev1.LocalObjectReference{Name: "other-kubeconfig"},
		},
	}
	clusterOtherNS := &aiv1alpha2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-c", Namespace: "tenant-a"},
		Spec: aiv1alpha2.ClusterSpec{
			APIEndpoint: "https://cluster-c.example.com",
			SecretRef:   corev1.LocalObjectReference{Name: "shared-kubeconfig"},
		},
	}

	fakeClient := ctrlclientfake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(clusterA, clusterB, clusterOtherNS).
		Build()

	r := &ClusterReconciler{Client: fakeClient}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "shared-kubeconfig", Namespace: "flexinfer-system"}}

	requests := r.requestsForSecret(context.Background(), secret)
	if len(requests) != 1 {
		t.Fatalf("requests len = %d, want 1", len(requests))
	}
	want := client.ObjectKey{Namespace: "flexinfer-system", Name: "cluster-a"}
	if requests[0].NamespacedName != want {
		t.Fatalf("request key = %s, want %s", requests[0].NamespacedName, want)
	}
}
