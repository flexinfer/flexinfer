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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func runtimePod(mutate func(p *corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "flexinfer-runtime-gfx906-abc",
			Namespace:       "flexinfer-system",
			ResourceVersion: "1000",
			Labels:          map[string]string{"app.kubernetes.io/component": runtimeComponentLabel},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.42.0.7",
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
	if mutate != nil {
		mutate(p)
	}
	return p
}

// TestRuntimePodMeaningfulChange_DropsStatusOnlyChurn is the regression test for
// the reconcile-starvation blocker: a healthy runtime pod's high-frequency
// status-only updates (which fan out to every model the pod serves) must NOT
// re-enqueue. Only a resourceVersion bump with no IP/phase/ready/deletion
// change should be filtered out.
func TestRuntimePodMeaningfulChange_DropsStatusOnlyChurn(t *testing.T) {
	old := runtimePod(nil)
	noisy := runtimePod(func(p *corev1.Pod) {
		// Kubelet status resync: resourceVersion bumps, plus cosmetic status
		// noise that the reconcile does not read.
		p.ResourceVersion = "1001"
		p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{
			Type:    corev1.ContainersReady,
			Status:  corev1.ConditionTrue,
			Message: "all containers ready",
		})
		p.Status.StartTime = &metav1.Time{}
	})

	admitted := runtimePodMeaningfulChange.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: noisy})
	assert.False(t, admitted, "status-only churn must be filtered to avoid reconcile amplification")
}

func TestRuntimePodMeaningfulChange_AdmitsMeaningfulUpdates(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(p *corev1.Pod)
	}{
		{
			name:   "pod IP changed (pod replaced)",
			mutate: func(p *corev1.Pod) { p.Status.PodIP = "10.42.0.9" },
		},
		{
			name:   "phase changed",
			mutate: func(p *corev1.Pod) { p.Status.Phase = corev1.PodFailed },
		},
		{
			name: "readiness flipped to NotReady",
			mutate: func(p *corev1.Pod) {
				p.Status.Conditions = []corev1.PodCondition{
					{Type: corev1.PodReady, Status: corev1.ConditionFalse},
				}
			},
		},
		{
			name:   "deletion started",
			mutate: func(p *corev1.Pod) { p.DeletionTimestamp = &metav1.Time{} },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := runtimePod(nil)
			updated := runtimePod(func(p *corev1.Pod) {
				p.ResourceVersion = "1001"
				tc.mutate(p)
			})
			admitted := runtimePodMeaningfulChange.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated})
			assert.True(t, admitted, "meaningful change must re-enqueue")
		})
	}
}

func TestRuntimePodMeaningfulChange_CreateDeleteGeneric(t *testing.T) {
	pod := runtimePod(nil)
	assert.True(t, runtimePodMeaningfulChange.Create(event.CreateEvent{Object: pod}),
		"create must be admitted")
	assert.True(t, runtimePodMeaningfulChange.Delete(event.DeleteEvent{Object: pod}),
		"delete must be admitted")
	assert.False(t, runtimePodMeaningfulChange.Generic(event.GenericEvent{Object: pod}),
		"generic events are dropped")
}
