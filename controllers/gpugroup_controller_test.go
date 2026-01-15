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

// TestGPUGroupReconciler_determineActiveModel_Hysteresis tests hysteresis window behavior
func TestGPUGroupReconciler_determineActiveModel_Hysteresis(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name                string
		hysteresisWindow    int32
		queuedSinceAgo      time.Duration
		currentActive       string
		expectedActiveModel string
		expectedReason      string
	}{
		{
			name:                "block swap when hysteresis window not elapsed",
			hysteresisWindow:    10,
			queuedSinceAgo:      5 * time.Second, // Only 5s ago, window is 10s
			currentActive:       "model-b",
			expectedActiveModel: "model-b", // Keep current
			expectedReason:      "hysteresis window",
		},
		{
			name:                "allow swap when hysteresis window elapsed",
			hysteresisWindow:    10,
			queuedSinceAgo:      15 * time.Second, // 15s ago, window is 10s
			currentActive:       "model-b",
			expectedActiveModel: "model-a", // Allow swap to higher priority
			expectedReason:      "demand",
		},
		{
			name:                "skip hysteresis for already active model",
			hysteresisWindow:    10,
			queuedSinceAgo:      5 * time.Second, // Only 5s ago
			currentActive:       "model-a",       // Already active
			expectedActiveModel: "model-a",       // Stays active
			expectedReason:      "demand",
		},
		{
			name:                "allow immediate swap when no current active",
			hysteresisWindow:    10,
			queuedSinceAgo:      2 * time.Second, // Very recent
			currentActive:       "",              // No current active
			expectedActiveModel: "model-a",       // Can activate immediately
			expectedReason:      "demand",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queuedSince := time.Now().Add(-tt.queuedSinceAgo)

			group := &aiv1alpha1.GPUGroup{
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
						Enabled:                 true,
						RequestQueueThreshold:   1, // Low threshold
						HysteresisWindowSeconds: tt.hysteresisWindow,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel: tt.currentActive,
					ModelStatuses: []aiv1alpha1.GPUGroupModelStatus{
						{Name: "model-a", QueuedRequests: 5, QueuedSince: &metav1.Time{Time: queuedSince}},
						{Name: "model-b", QueuedRequests: 0},
					},
				},
			}

			modelDeployments := []*aiv1alpha1.ModelDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
			}

			objects := []runtime.Object{group}
			for _, md := range modelDeployments {
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

			members := make(map[string]*aiv1alpha1.ModelDeployment)
			for _, md := range modelDeployments {
				members[md.Name] = md
			}

			ctx := context.Background()
			activeModel, reason := reconciler.determineActiveModel(ctx, group, members)

			assert.Equal(t, tt.expectedActiveModel, activeModel, "Active model should match")
			assert.Contains(t, reason, tt.expectedReason, "Reason should contain expected text")
		})
	}
}

// TestGPUGroupReconciler_determineActiveModel_QueueThreshold tests queue threshold enforcement
func TestGPUGroupReconciler_determineActiveModel_QueueThreshold(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name                string
		queueThreshold      int32
		queueDepth          int32
		currentActive       string
		expectedActiveModel string
		expectedReason      string
	}{
		{
			name:                "block swap when queue below threshold",
			queueThreshold:      5,
			queueDepth:          3, // Below threshold of 5
			currentActive:       "model-b",
			expectedActiveModel: "model-b", // Keep current
			expectedReason:      "queue threshold not met",
		},
		{
			name:                "allow swap when queue meets threshold",
			queueThreshold:      5,
			queueDepth:          5, // Exactly threshold
			currentActive:       "model-b",
			expectedActiveModel: "model-a", // Allow swap
			expectedReason:      "demand",
		},
		{
			name:                "allow swap when queue exceeds threshold",
			queueThreshold:      5,
			queueDepth:          10, // Above threshold
			currentActive:       "model-b",
			expectedActiveModel: "model-a",
			expectedReason:      "demand",
		},
		{
			name:                "skip threshold check for current active model",
			queueThreshold:      5,
			queueDepth:          2, // Below threshold
			currentActive:       "model-a",
			expectedActiveModel: "model-a", // Stays active
			expectedReason:      "demand",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := &aiv1alpha1.GPUGroup{
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
						Enabled:                 true,
						RequestQueueThreshold:   tt.queueThreshold,
						HysteresisWindowSeconds: 0, // Disable hysteresis for this test
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel: tt.currentActive,
					ModelStatuses: []aiv1alpha1.GPUGroupModelStatus{
						{Name: "model-a", QueuedRequests: tt.queueDepth, QueuedSince: &metav1.Time{Time: time.Now().Add(-30 * time.Second)}},
						{Name: "model-b", QueuedRequests: 0},
					},
				},
			}

			modelDeployments := []*aiv1alpha1.ModelDeployment{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "default"},
					Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
				},
			}

			objects := []runtime.Object{group}
			for _, md := range modelDeployments {
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

			members := make(map[string]*aiv1alpha1.ModelDeployment)
			for _, md := range modelDeployments {
				members[md.Name] = md
			}

			ctx := context.Background()
			activeModel, reason := reconciler.determineActiveModel(ctx, group, members)

			assert.Equal(t, tt.expectedActiveModel, activeModel, "Active model should match")
			assert.Contains(t, reason, tt.expectedReason, "Reason should contain expected text")
		})
	}
}

// TestGPUGroupReconciler_determineActiveModel_FIFO tests FIFO ordering for same priority
func TestGPUGroupReconciler_determineActiveModel_FIFO(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	// Both models have same priority, model-a queued first
	modelAQueuedSince := time.Now().Add(-60 * time.Second) // 60s ago
	modelBQueuedSince := time.Now().Add(-30 * time.Second) // 30s ago

	group := &aiv1alpha1.GPUGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-group",
			Namespace: "default",
		},
		Spec: aiv1alpha1.GPUGroupSpec{
			Models: []aiv1alpha1.GPUGroupMember{
				{Name: "model-a", Priority: 100}, // Same priority
				{Name: "model-b", Priority: 100}, // Same priority
			},
			AntiThrashing: aiv1alpha1.AntiThrashingConfig{
				Enabled:                 true,
				RequestQueueThreshold:   1,
				HysteresisWindowSeconds: 0,
			},
		},
		Status: aiv1alpha1.GPUGroupStatus{
			ModelStatuses: []aiv1alpha1.GPUGroupModelStatus{
				{Name: "model-a", QueuedRequests: 5, QueuedSince: &metav1.Time{Time: modelAQueuedSince}},
				{Name: "model-b", QueuedRequests: 5, QueuedSince: &metav1.Time{Time: modelBQueuedSince}},
			},
		},
	}

	modelDeployments := []*aiv1alpha1.ModelDeployment{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default"},
			Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "default"},
			Spec:       aiv1alpha1.ModelDeploymentSpec{Backend: "ollama"},
		},
	}

	objects := []runtime.Object{group}
	for _, md := range modelDeployments {
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

	members := make(map[string]*aiv1alpha1.ModelDeployment)
	for _, md := range modelDeployments {
		members[md.Name] = md
	}

	ctx := context.Background()
	activeModel, reason := reconciler.determineActiveModel(ctx, group, members)

	// Model A should be selected because it was queued first (FIFO)
	assert.Equal(t, "model-a", activeModel, "Model queued first should be selected (FIFO)")
	assert.Contains(t, reason, "demand", "Should be selected due to demand")
}

// TestGPUGroupReconciler_shouldBlockSwap_AllConditions tests all anti-thrashing conditions together
func TestGPUGroupReconciler_shouldBlockSwap_AllConditions(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name          string
		minRunDur     int32
		cooldown      int32
		swapTimeAgo   time.Duration
		preemptedAgo  time.Duration
		targetModel   string
		currentActive string
		expectedBlock bool
	}{
		{
			name:          "block: minimum run duration not met",
			minRunDur:     60,
			cooldown:      30,
			swapTimeAgo:   30 * time.Second, // Only 30s, need 60s
			preemptedAgo:  0,                // Not preempted
			targetModel:   "model-b",
			currentActive: "model-a",
			expectedBlock: true,
		},
		{
			name:          "block: target model in cooldown",
			minRunDur:     30,
			cooldown:      120,
			swapTimeAgo:   60 * time.Second, // 60s, meets 30s min run
			preemptedAgo:  60 * time.Second, // Preempted 60s ago, cooldown is 120s
			targetModel:   "model-a",        // Trying to activate preempted model
			currentActive: "model-b",
			expectedBlock: true,
		},
		{
			name:          "allow: all conditions met",
			minRunDur:     30,
			cooldown:      60,
			swapTimeAgo:   120 * time.Second, // 120s, well past 30s min run
			preemptedAgo:  120 * time.Second, // Preempted 120s ago, past 60s cooldown
			targetModel:   "model-a",
			currentActive: "model-b",
			expectedBlock: false,
		},
		{
			name:          "allow: activating non-preempted model",
			minRunDur:     30,
			cooldown:      60,
			swapTimeAgo:   60 * time.Second,
			preemptedAgo:  0, // Not preempted
			targetModel:   "model-c",
			currentActive: "model-a",
			expectedBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled:                        true,
						MinimumRunDurationSeconds:      tt.minRunDur,
						CooldownAfterPreemptionSeconds: tt.cooldown,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel:  tt.currentActive,
					LastSwapTime: &metav1.Time{Time: time.Now().Add(-tt.swapTimeAgo)},
				},
			}

			// Add preempted model status if applicable
			if tt.preemptedAgo > 0 {
				group.Status.ModelStatuses = []aiv1alpha1.GPUGroupModelStatus{
					{
						Name:        tt.targetModel,
						State:       aiv1alpha1.ModelGroupStatePreempted,
						PreemptedAt: &metav1.Time{Time: time.Now().Add(-tt.preemptedAgo)},
					},
				}
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithStatusSubresource(&aiv1alpha1.GPUGroup{}).
				WithRuntimeObjects(group).
				Build()

			reconciler := &GPUGroupReconciler{
				Client:    fakeClient,
				APIReader: fakeClient,
				Scheme:    s,
			}

			blocked := reconciler.shouldBlockSwap(group, tt.targetModel)
			assert.Equal(t, tt.expectedBlock, blocked, "Block status should match")
		})
	}
}

// TestGPUGroupReconciler_IdleTimeout tests idle timeout behavior
func TestGPUGroupReconciler_IdleTimeout(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	idleTimeout := int32(300) // 5 minutes

	tests := []struct {
		name                string
		lastAccessAgo       time.Duration
		expectedActiveModel string
		expectedReason      string
	}{
		{
			name:                "keep active when recently accessed",
			lastAccessAgo:       60 * time.Second, // 1 minute ago
			expectedActiveModel: "model-a",
			expectedReason:      "still active",
		},
		{
			name:                "scale down when idle timeout exceeded",
			lastAccessAgo:       10 * time.Minute, // 10 minutes ago
			expectedActiveModel: "",
			expectedReason:      "no demand",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lastAccessTime := time.Now().Add(-tt.lastAccessAgo)

			group := &aiv1alpha1.GPUGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-group",
					Namespace: "default",
				},
				Spec: aiv1alpha1.GPUGroupSpec{
					Models: []aiv1alpha1.GPUGroupMember{
						{Name: "model-a", Priority: 100},
					},
					AntiThrashing: aiv1alpha1.AntiThrashingConfig{
						Enabled: true,
					},
				},
				Status: aiv1alpha1.GPUGroupStatus{
					ActiveModel: "model-a",
					ModelStatuses: []aiv1alpha1.GPUGroupModelStatus{
						{Name: "model-a", QueuedRequests: 0}, // No queue
					},
				},
			}

			md := &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default"},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:            "ollama",
					IdleTimeoutSeconds: &idleTimeout,
				},
				Status: aiv1alpha1.ModelDeploymentStatus{
					LastAccessTime: &metav1.Time{Time: lastAccessTime},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithStatusSubresource(&aiv1alpha1.GPUGroup{}, &aiv1alpha1.ModelDeployment{}).
				WithRuntimeObjects(group, md).
				Build()

			reconciler := &GPUGroupReconciler{
				Client:    fakeClient,
				APIReader: fakeClient,
				Scheme:    s,
			}

			members := map[string]*aiv1alpha1.ModelDeployment{"model-a": md}

			ctx := context.Background()
			activeModel, reason := reconciler.determineActiveModel(ctx, group, members)

			assert.Equal(t, tt.expectedActiveModel, activeModel, "Active model should match")
			assert.Contains(t, reason, tt.expectedReason, "Reason should contain expected text")
		})
	}
}
