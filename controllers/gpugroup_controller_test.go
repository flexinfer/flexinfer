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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestGPUGroupReconciler_shouldBlockSwap(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name           string
		group          *aiv1alpha1.GPUGroup
		targetModel    string
		currentActive  string
		lastSwapTime   *metav1.Time
		expectedBlock  bool
		expectedReason string
	}{
		{
			name: "allow swap when anti-thrashing disabled",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled: false,
					},
				},
			},
			targetModel:   "model-b",
			currentActive: "model-a",
			expectedBlock: false,
		},
		{
			name: "allow swap when no current active model",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled:                   true,
						MinimumRunDurationSeconds: 30,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel: "",
				},
			},
			targetModel:   "model-a",
			currentActive: "",
			expectedBlock: false,
		},
		{
			name: "block swap within minimum run duration",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled:                   true,
						MinimumRunDurationSeconds: 30,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel:  "model-a",
					LastSwapTime: &metav1.Time{Time: time.Now().Add(-10 * time.Second)}, // 10s ago
				},
			},
			targetModel:    "model-b",
			currentActive:  "model-a",
			expectedBlock:  true,
			expectedReason: "minimum run duration not met",
		},
		{
			name: "allow swap after minimum run duration",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled:                   true,
						MinimumRunDurationSeconds: 30,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel:  "model-a",
					LastSwapTime: &metav1.Time{Time: time.Now().Add(-60 * time.Second)}, // 60s ago
				},
			},
			targetModel:   "model-b",
			currentActive: "model-a",
			expectedBlock: false,
		},
		{
			name: "block swap during cooldown period",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled:                        true,
						MinimumRunDurationSeconds:      30,
						CooldownAfterPreemptionSeconds: 60,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel:  "model-b",
					LastSwapTime: &metav1.Time{Time: time.Now().Add(-35 * time.Second)}, // 35s ago
					ModelStatuses: []aiv1alpha1.GPUGroupModelStatus{
						{
							Name:        "model-a",
							State:       aiv1alpha1.ModelGroupStatePreempted,
							PreemptedAt: &metav1.Time{Time: time.Now().Add(-35 * time.Second)}, // Preempted 35s ago
							PreemptedBy: "model-b",
						},
					},
				},
			},
			targetModel:    "model-a",
			currentActive:  "model-b",
			expectedBlock:  true,
			expectedReason: "cooldown period active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithStatusSubresource(&aiv1alpha1.GPUGroup{}).
				WithRuntimeObjects(tt.group).
				Build()

			reconciler := &GPUGroupReconciler{
				Client:    fakeClient,
				APIReader: fakeClient,
				Scheme:    s,
			}

			blocked := reconciler.shouldBlockSwap(tt.group, tt.targetModel)

			assert.Equal(t, tt.expectedBlock, blocked, "Block status should match")
		})
	}
}

func TestGPUGroupReconciler_determineActiveModel(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	now := metav1.Now()

	tests := []struct {
		name                string
		group               *aiv1alpha1.GPUGroup
		modelDeployments    []*aiv1alpha1.ModelDeployment
		expectedActiveModel string
		expectedReason      string
	}{
		{
			name: "select highest priority model with demand",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					Models: []aiv1alpha1.GPUGroupMember{
						{Name: "model-a", Priority: 100},
						{Name: "model-b", Priority: 50},
					},
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled:               true,
						RequestQueueThreshold: 3,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					// Model statuses with queue depths
					ModelStatuses: []aiv1alpha1.GPUGroupModelStatus{
						{Name: "model-a", QueuedRequests: 5, QueuedSince: &metav1.Time{Time: time.Now().Add(-30 * time.Second)}},
						{Name: "model-b", QueuedRequests: 3, QueuedSince: &metav1.Time{Time: time.Now().Add(-20 * time.Second)}},
					},
				},
			},
			modelDeployments: []*aiv1alpha1.ModelDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
			},
			expectedActiveModel: "model-a",
			expectedReason:      "demand",
		},
		{
			name: "keep current model when recently active",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					Models: []aiv1alpha1.GPUGroupMember{
						{Name: "model-a", Priority: 100},
						{Name: "model-b", Priority: 50},
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel: "model-b",
				},
			},
			modelDeployments: []*aiv1alpha1.ModelDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
					Status: aiv1alpha1.ModelDeploymentStatus{
						LastAccessTime: &now, // Recently accessed
					},
				},
			},
			expectedActiveModel: "model-b",
			expectedReason:      "active",
		},
		{
			name: "no active model when no demand",
			group: &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					Models: []aiv1alpha1.GPUGroupMember{
						{Name: "model-a", Priority: 100},
						{Name: "model-b", Priority: 50},
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					// No active model, no queue
				},
			},
			modelDeployments: []*aiv1alpha1.ModelDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
			},
			expectedActiveModel: "",
			expectedReason:      "no demand",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := []runtime.Object{tt.group}
			for _, md := range tt.modelDeployments {
				objects = append(objects, md)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithStatusSubresource(&aiv1alpha1.GPUGroup{}, &aiv1alpha1.ModelDeployment{}).
				WithRuntimeObjects(objects...).
				Build()

			reconciler := &GPUGroupReconciler{
				Client:    fakeClient,
				APIReader: fakeClient,
				Scheme:    s,
			}

			// Build members map
			members := make(map[string]*aiv1alpha1.ModelDeployment)
			for _, md := range tt.modelDeployments {
				members[md.Name] = md
			}

			ctx := context.Background()
			activeModel, reason := reconciler.determineActiveModel(ctx, tt.group, members)

			assert.Equal(t, tt.expectedActiveModel, activeModel, "Active model should match")
			if tt.expectedReason != "" {
				assert.Contains(t, reason, tt.expectedReason, "Reason should contain expected text")
			}
		})
	}
}

func TestParseQueueAnnotations(t *testing.T) {
	tests := []struct {
		name           string
		annotations    map[string]string
		modelName      string
		expectedDepth  int
		expectedSince  bool
		expectedExists bool
	}{
		{
			name: "parse valid queue annotation",
			annotations: map[string]string{
				"flexinfer.ai/queue.model-a":       "5",
				"flexinfer.ai/queue-since.model-a": "2025-01-11T12:00:00Z",
			},
			modelName:      "model-a",
			expectedDepth:  5,
			expectedSince:  true,
			expectedExists: true,
		},
		{
			name: "no annotation for model",
			annotations: map[string]string{
				"flexinfer.ai/queue.model-b": "3",
			},
			modelName:      "model-a",
			expectedDepth:  0,
			expectedExists: false,
		},
		{
			name:           "empty annotations",
			annotations:    map[string]string{},
			modelName:      "model-a",
			expectedDepth:  0,
			expectedExists: false,
		},
		{
			name: "invalid queue depth value",
			annotations: map[string]string{
				"flexinfer.ai/queue.model-a": "invalid",
			},
			modelName:      "model-a",
			expectedDepth:  0,
			expectedExists: false, // Invalid value treated as not existing
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			depth, since, exists := parseQueueAnnotation(group, tt.modelName)

			assert.Equal(t, tt.expectedDepth, depth, "Queue depth should match")
			if tt.expectedSince {
				assert.False(t, since.IsZero(), "Since time should be set")
			}
			assert.Equal(t, tt.expectedExists, exists, "Exists flag should match")
		})
	}
}

// parseQueueAnnotation is a helper function to parse queue annotations from GPUGroup
func parseQueueAnnotation(group *aiv1alpha1.GPUGroup, modelName string) (depth int, since time.Time, exists bool) {
	if group.Annotations == nil {
		return 0, time.Time{}, false
	}

	queueKey := AnnotationQueueDepthPrefix + modelName
	sinceKey := AnnotationQueueSincePrefix + modelName

	depthStr, hasDepth := group.Annotations[queueKey]
	if !hasDepth {
		return 0, time.Time{}, false
	}

	var err error
	depth = 0
	if depthStr != "" {
		_, err = time.Parse(time.RFC3339, depthStr) // Check if it's accidentally a timestamp
		if err == nil {
			return 0, time.Time{}, false // Invalid format (timestamp in depth field)
		}
		// Try to parse as integer
		var n int
		_, err = time.ParseDuration(depthStr)
		if err != nil {
			// Not a duration, try as integer
			for _, c := range depthStr {
				if c < '0' || c > '9' {
					return 0, time.Time{}, false // Invalid number
				}
				n = n*10 + int(c-'0')
			}
			depth = n
		}
	}

	sinceStr, hasSince := group.Annotations[sinceKey]
	if hasSince {
		since, _ = time.Parse(time.RFC3339, sinceStr)
	}

	return depth, since, true
}
