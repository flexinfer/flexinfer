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
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"
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
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	nodeB := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-b"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}
	notReadyNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-not-ready"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("2"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			},
		},
	}

	runningPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "tenant-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-a",
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
			NodeName: "gpu-node-a",
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

	unscheduledPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pending-model", Namespace: "tenant-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "waiting",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	notReadyPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "model-on-down-node", Namespace: "tenant-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-not-ready",
			Containers: []corev1.Container{
				{
					Name: "inference",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	clientset := k8sfake.NewSimpleClientset(nodeA, nodeB, notReadyNode, runningPod, succeededPod, unscheduledPod, notReadyPod)
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

func TestCollectGPUInventory_UsesInitContainerMaxForScheduledPods(t *testing.T) {
	ctx := context.Background()

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gpu-node-a"},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceName("amd.com/gpu"): resource.MustParse("4"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	podWithHeavyInit := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "tenant-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-a",
			InitContainers: []corev1.Container{
				{
					Name: "init",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("amd.com/gpu"): resource.MustParse("2"),
						},
					},
				},
			},
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

	podWithTwoContainers := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "tenant-a"},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
		Spec: corev1.PodSpec{
			NodeName: "gpu-node-a",
			Containers: []corev1.Container{
				{
					Name: "main",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
						},
					},
				},
				{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
						},
					},
				},
			},
		},
	}

	clientset := k8sfake.NewSimpleClientset(node, podWithHeavyInit, podWithTwoContainers)
	capacity, available, err := collectGPUInventory(ctx, clientset)
	if err != nil {
		t.Fatalf("collectGPUInventory() unexpected error: %v", err)
	}

	amdCap := capacity[corev1.ResourceName("amd.com/gpu")]
	if got := amdCap.String(); got != "4" {
		t.Fatalf("amd capacity = %s, want 4", got)
	}

	amdAvail := available[corev1.ResourceName("amd.com/gpu")]
	if got := amdAvail.String(); got != "0" {
		t.Fatalf("amd available = %s, want 0", got)
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
	if snapshot := w.snapshot(); snapshot.CacheReady {
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

	got := w.snapshot()
	if !got.CacheReady {
		t.Fatal("snapshot should be ready after initial list")
	}
	if len(got.Models) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(got.Models))
	}
	if got.Models[0].Namespace != "ns-a" || got.Models[0].Name != "alpha" || got.Models[0].Phase != "Loading" {
		t.Fatalf("first model = %+v, want ns-a/alpha Loading", got.Models[0])
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

	got = w.snapshot()
	if !got.CacheReady {
		t.Fatal("snapshot should remain ready after watch events")
	}
	if len(got.Models) != 1 {
		t.Fatalf("snapshot len = %d, want 1", len(got.Models))
	}
	if got.Models[0].Namespace != "ns-a" || got.Models[0].Name != "alpha" || got.Models[0].Phase != "Ready" {
		t.Fatalf("remaining model = %+v, want ns-a/alpha Ready", got.Models[0])
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

func TestResetClusterObservationStatus(t *testing.T) {
	status := &aiv1alpha2.ClusterStatus{
		Capacity: corev1.ResourceList{
			corev1.ResourceName("amd.com/gpu"): resource.MustParse("2"),
		},
		Available: corev1.ResourceList{
			corev1.ResourceName("amd.com/gpu"): resource.MustParse("1"),
		},
		Models: []aiv1alpha2.ClusterModelStatus{
			{Name: "model-a", Namespace: "tenant-a", Phase: "Ready"},
		},
	}

	resetClusterObservationStatus(status)

	if status.Capacity != nil {
		t.Fatalf("capacity = %v, want nil", status.Capacity)
	}
	if status.Available != nil {
		t.Fatalf("available = %v, want nil", status.Available)
	}
	if status.Models != nil {
		t.Fatalf("models = %v, want nil", status.Models)
	}
}

func TestInventorySourceGauges(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		wantWatch float64
		wantList  float64
	}{
		{name: "watch", source: "watch", wantWatch: 1, wantList: 0},
		{name: "list", source: "list", wantWatch: 0, wantList: 1},
		{name: "unknown", source: "other", wantWatch: 0, wantList: 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			gotWatch, gotList := inventorySourceGauges(tc.source)
			if gotWatch != tc.wantWatch || gotList != tc.wantList {
				t.Fatalf("inventorySourceGauges(%q) = (%v,%v), want (%v,%v)", tc.source, gotWatch, gotList, tc.wantWatch, tc.wantList)
			}
		})
	}
}

func TestWatchConditionForObservation(t *testing.T) {
	tests := []struct {
		name         string
		observation  *clusterObservation
		wantStatus   metav1.ConditionStatus
		wantReason   string
		wantContains string
	}{
		{
			name: "watch ready",
			observation: &clusterObservation{
				WatchReady: true,
			},
			wantStatus:   metav1.ConditionTrue,
			wantReason:   "WatchSynced",
			wantContains: "watch cache is ready",
		},
		{
			name: "list fallback",
			observation: &clusterObservation{
				WatchReady:  false,
				WatchReason: "watch cache not ready; using list fallback for model inventory",
			},
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "ListFallback",
			wantContains: "list fallback",
		},
		{
			name: "watch degraded",
			observation: &clusterObservation{
				WatchReady:    false,
				WatchDegraded: true,
				WatchReason:   "remote model watch start failed: timeout",
			},
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "WatchDegraded",
			wantContains: "watch start failed",
		},
		{
			name:         "nil observation",
			observation:  nil,
			wantStatus:   metav1.ConditionFalse,
			wantReason:   "ListFallback",
			wantContains: "list fallback",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			status, reason, message := watchConditionForObservation(tc.observation)
			if status != tc.wantStatus {
				t.Fatalf("status = %v, want %v", status, tc.wantStatus)
			}
			if reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tc.wantReason)
			}
			if !strings.Contains(message, tc.wantContains) {
				t.Fatalf("message = %q, want to contain %q", message, tc.wantContains)
			}
		})
	}
}

func TestClusterProbeMessage(t *testing.T) {
	tests := []struct {
		name        string
		observation *clusterObservation
		want        string
	}{
		{
			name:        "nil observation",
			observation: nil,
			want:        "probe succeeded",
		},
		{
			name: "watch ready",
			observation: &clusterObservation{
				ServerVersion: "v1.32.0",
				WatchReady:    true,
			},
			want: "probe succeeded (kubernetes v1.32.0)",
		},
		{
			name: "watch ready with restarts",
			observation: &clusterObservation{
				ServerVersion: "v1.32.0",
				WatchReady:    true,
				WatchRestarts: 2,
			},
			want: "probe succeeded (kubernetes v1.32.0); watch synced after 2 restarts",
		},
		{
			name: "watch fallback reason",
			observation: &clusterObservation{
				ServerVersion: "v1.32.0",
				WatchReady:    false,
				WatchReason:   "remote model watch start failed: timeout",
			},
			want: "probe succeeded (kubernetes v1.32.0); model inventory fallback: remote model watch start failed: timeout",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := clusterProbeMessage(tc.observation); got != tc.want {
				t.Fatalf("clusterProbeMessage() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMaybeRecordWatchConditionEvent(t *testing.T) {
	cluster := &aiv1alpha2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "flexinfer-system"},
	}
	rec := record.NewFakeRecorder(5)

	setClusterCondition(cluster, clusterConditionWatch, metav1.ConditionFalse, "WatchDegraded", "remote model watch start failed")
	maybeRecordWatchConditionEvent(rec, cluster, nil)

	select {
	case event := <-rec.Events:
		if !strings.Contains(event, "ModelWatchDegraded") {
			t.Fatalf("event = %q, want ModelWatchDegraded", event)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("expected watch condition event")
	}
}

func TestMaybeRecordWatchConditionEvent_NoOpWhenUnchanged(t *testing.T) {
	cluster := &aiv1alpha2.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster-a", Namespace: "flexinfer-system"},
	}
	rec := record.NewFakeRecorder(1)

	setClusterCondition(cluster, clusterConditionWatch, metav1.ConditionTrue, "WatchSynced", "remote model watch cache is ready")
	previous := getClusterCondition(cluster, clusterConditionWatch)
	maybeRecordWatchConditionEvent(rec, cluster, previous)

	select {
	case event := <-rec.Events:
		t.Fatalf("unexpected event emitted: %q", event)
	case <-time.After(200 * time.Millisecond):
		// expected no event
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
