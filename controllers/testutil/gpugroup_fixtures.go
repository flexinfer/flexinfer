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

//nolint:staticcheck // Test fixtures intentionally use deprecated v1alpha1 types while legacy controllers still exist.
package testutil

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

// Annotation keys for demand signaling
const (
	AnnotationQueueDepthPrefix = "flexinfer.ai/queue."
	AnnotationQueueSincePrefix = "flexinfer.ai/queue-since."
)

// GPUGroupOption is a functional option for configuring GPUGroup
type GPUGroupOption func(*aiv1alpha1.GPUGroup)

// NewTestGPUGroup creates a GPUGroup with sensible defaults for testing
func NewTestGPUGroup(name string, models ...string) *aiv1alpha1.GPUGroup {
	members := make([]aiv1alpha1.GPUGroupMember, len(models))
	for i, m := range models {
		members[i] = aiv1alpha1.GPUGroupMember{
			Name:     m,
			Priority: int32(100 - i*10), // Descending priority: 100, 90, 80, ...
		}
	}

	return &aiv1alpha1.GPUGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: aiv1alpha1.GPUGroupSpec{
			Models: members,
			ScalingPolicy: aiv1alpha1.GPUGroupScalingPolicy{
				Strategy:            aiv1alpha1.GPUShareStrategyExclusive,
				PreemptionPolicy:    aiv1alpha1.PreemptionPolicyGraceful,
				DrainTimeoutSeconds: 30,
			},
			AntiThrashing: aiv1alpha1.AntiThrashingConfig{
				Enabled:                        true,
				MinimumRunDurationSeconds:      30,
				CooldownAfterPreemptionSeconds: 60,
				RequestQueueThreshold:          3,
				HysteresisWindowSeconds:        10,
			},
		},
	}
}

// WithNamespace sets the namespace
func WithNamespace(ns string) GPUGroupOption {
	return func(g *aiv1alpha1.GPUGroup) {
		g.Namespace = ns
	}
}

// WithAntiThrashing configures anti-thrashing settings
func WithAntiThrashing(enabled bool, minRunDuration, cooldown, hysteresis, threshold int32) GPUGroupOption {
	return func(g *aiv1alpha1.GPUGroup) {
		g.Spec.AntiThrashing = aiv1alpha1.AntiThrashingConfig{
			Enabled:                        enabled,
			MinimumRunDurationSeconds:      minRunDuration,
			CooldownAfterPreemptionSeconds: cooldown,
			HysteresisWindowSeconds:        hysteresis,
			RequestQueueThreshold:          threshold,
		}
	}
}

// WithActiveModel sets the current active model in status
func WithActiveModel(model string) GPUGroupOption {
	return func(g *aiv1alpha1.GPUGroup) {
		g.Status.ActiveModel = model
	}
}

// WithLastSwapTime sets the last swap time
func WithLastSwapTime(t time.Time) GPUGroupOption {
	return func(g *aiv1alpha1.GPUGroup) {
		g.Status.LastSwapTime = &metav1.Time{Time: t}
	}
}

// WithPhase sets the GPUGroup phase
func WithPhase(phase aiv1alpha1.GPUGroupPhase) GPUGroupOption {
	return func(g *aiv1alpha1.GPUGroup) {
		g.Status.Phase = phase
	}
}

// WithModelStatus adds or updates a model status entry
func WithModelStatus(name string, state aiv1alpha1.ModelGroupState, opts ...ModelStatusOption) GPUGroupOption {
	return func(g *aiv1alpha1.GPUGroup) {
		status := aiv1alpha1.GPUGroupModelStatus{
			Name:  name,
			State: state,
		}
		for _, opt := range opts {
			opt(&status)
		}

		// Update existing or append
		found := false
		for i, ms := range g.Status.ModelStatuses {
			if ms.Name == name {
				g.Status.ModelStatuses[i] = status
				found = true
				break
			}
		}
		if !found {
			g.Status.ModelStatuses = append(g.Status.ModelStatuses, status)
		}
	}
}

// ModelStatusOption is a functional option for model status
type ModelStatusOption func(*aiv1alpha1.GPUGroupModelStatus)

// WithQueuedRequests sets queued request count and since time
func WithQueuedRequests(count int32, since time.Time) ModelStatusOption {
	return func(s *aiv1alpha1.GPUGroupModelStatus) {
		s.QueuedRequests = count
		s.QueuedSince = &metav1.Time{Time: since}
	}
}

// WithPreemptedAt sets the preemption time and preempted-by
func WithPreemptedAt(t time.Time, by string) ModelStatusOption {
	return func(s *aiv1alpha1.GPUGroupModelStatus) {
		s.PreemptedAt = &metav1.Time{Time: t}
		s.PreemptedBy = by
	}
}

// ApplyGPUGroupOptions applies functional options to a GPUGroup
func ApplyGPUGroupOptions(g *aiv1alpha1.GPUGroup, opts ...GPUGroupOption) *aiv1alpha1.GPUGroup {
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// ModelDeploymentOption is a functional option for ModelDeployment
//
//nolint:staticcheck // v1alpha1 ModelDeployment is legacy but still used by GPUGroup controller tests.
type ModelDeploymentOption func(*aiv1alpha1.ModelDeployment)

// NewTestModelDeployment creates a ModelDeployment with sensible defaults
//
//nolint:staticcheck // v1alpha1 ModelDeployment is legacy but still used by GPUGroup controller tests.
func NewTestModelDeployment(name string, opts ...ModelDeploymentOption) *aiv1alpha1.ModelDeployment {
	replicas := int32(0)
	//nolint:staticcheck // v1alpha1 ModelDeployment is legacy but still used by GPUGroup controller tests.
	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend:  "ollama",
			Model:    "test-model",
			Replicas: &replicas,
		},
	}

	for _, opt := range opts {
		opt(md)
	}
	return md
}

// MDWithNamespace sets the namespace
func MDWithNamespace(ns string) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Namespace = ns
	}
}

// MDWithReplicas sets the replica count
func MDWithReplicas(n int32) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Spec.Replicas = &n
	}
}

// MDWithGPUGroup sets the gpuGroupRef
func MDWithGPUGroup(name string) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Spec.GPUGroupRef = &name
	}
}

// MDWithPriority sets the priority
func MDWithPriority(p int32) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Spec.Priority = &p
	}
}

// MDWithBackend sets the backend type
func MDWithBackend(backend string) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Spec.Backend = backend
	}
}

// MDWithPhase sets the deployment phase in status
func MDWithPhase(phase aiv1alpha1.ModelDeploymentPhase) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Status.Phase = phase
	}
}

// MDWithLastAccessTime sets the last access time
func MDWithLastAccessTime(t time.Time) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Status.LastAccessTime = &metav1.Time{Time: t}
	}
}

// MDWithServiceLabels sets service labels
func MDWithServiceLabels(labels ...string) ModelDeploymentOption {
	return func(md *aiv1alpha1.ModelDeployment) {
		md.Spec.ServiceLabels = labels
	}
}

// SimulateDemand adds queue annotations to GPUGroup to simulate proxy demand signaling
func SimulateDemand(ctx context.Context, c client.Client, gpuGroupName, modelName string, queueDepth int) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the latest GPUGroup to avoid update conflicts with status updates from the controller.
		group := &aiv1alpha1.GPUGroup{}
		if err := c.Get(ctx, client.ObjectKey{Name: gpuGroupName, Namespace: "default"}, group); err != nil {
			return fmt.Errorf("failed to get GPUGroup: %w", err)
		}

		// Set annotations
		if group.Annotations == nil {
			group.Annotations = make(map[string]string)
		}

		if queueDepth > 0 {
			group.Annotations[AnnotationQueueDepthPrefix+modelName] = fmt.Sprintf("%d", queueDepth)
			group.Annotations[AnnotationQueueSincePrefix+modelName] = time.Now().Format(time.RFC3339)
		} else {
			delete(group.Annotations, AnnotationQueueDepthPrefix+modelName)
			delete(group.Annotations, AnnotationQueueSincePrefix+modelName)
		}

		if err := c.Update(ctx, group); err != nil {
			if apierrors.IsConflict(err) {
				return err
			}
			return fmt.Errorf("failed to update GPUGroup annotations: %w", err)
		}

		return nil
	})
}

// ClearDemand removes queue annotations from GPUGroup
func ClearDemand(ctx context.Context, c client.Client, gpuGroupName, modelName string) error {
	return SimulateDemand(ctx, c, gpuGroupName, modelName, 0)
}

// WaitForPhase polls until GPUGroup reaches expected phase or timeout
func WaitForPhase(ctx context.Context, c client.Client, name string, phase aiv1alpha1.GPUGroupPhase, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for GPUGroup %s to reach phase %s", name, phase)
			}

			group := &aiv1alpha1.GPUGroup{}
			if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: "default"}, group); err != nil {
				continue // Retry on error
			}

			if group.Status.Phase == phase {
				return nil
			}
		}
	}
}

// WaitForActiveModel polls until GPUGroup has expected active model
func WaitForActiveModel(ctx context.Context, c client.Client, gpuGroupName, modelName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for GPUGroup %s to have active model %s", gpuGroupName, modelName)
			}

			group := &aiv1alpha1.GPUGroup{}
			if err := c.Get(ctx, client.ObjectKey{Name: gpuGroupName, Namespace: "default"}, group); err != nil {
				continue
			}

			if group.Status.ActiveModel == modelName {
				return nil
			}
		}
	}
}

// WaitForReplicas polls until ModelDeployment has expected replica count
func WaitForReplicas(ctx context.Context, c client.Client, mdName string, replicas int32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timeout waiting for ModelDeployment %s to have %d replicas", mdName, replicas)
			}

			md := &aiv1alpha1.ModelDeployment{}
			if err := c.Get(ctx, client.ObjectKey{Name: mdName, Namespace: "default"}, md); err != nil {
				continue
			}

			if md.Spec.Replicas != nil && *md.Spec.Replicas == replicas {
				return nil
			}
		}
	}
}

// Int32Ptr returns a pointer to an int32 value
func Int32Ptr(i int32) *int32 {
	return &i
}

// TimePtr returns a pointer to a metav1.Time
func TimePtr(t time.Time) *metav1.Time {
	return &metav1.Time{Time: t}
}

// TimeAgo returns a time that is the given duration in the past
func TimeAgo(d time.Duration) time.Time {
	return time.Now().Add(-d)
}
