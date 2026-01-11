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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestUpdateModelDeploymentStatus(t *testing.T) {
	// Set up scheme
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name            string
		initialStatus   aiv1alpha1.ModelDeploymentStatus
		newPhase        aiv1alpha1.ModelDeploymentPhase
		message         string
		expectedPhase   aiv1alpha1.ModelDeploymentPhase
		expectCondition bool
		conditionType   string
		conditionStatus metav1.ConditionStatus
	}{
		{
			name:            "update from empty status to pending",
			initialStatus:   aiv1alpha1.ModelDeploymentStatus{},
			newPhase:        aiv1alpha1.ModelDeploymentPhasePending,
			message:         "Initializing ModelDeployment",
			expectedPhase:   aiv1alpha1.ModelDeploymentPhasePending,
			expectCondition: true,
			conditionType:   aiv1alpha1.ConditionTypeProgressing,
			conditionStatus: metav1.ConditionTrue,
		},
		{
			name: "update from pending to running",
			initialStatus: aiv1alpha1.ModelDeploymentStatus{
				Phase: aiv1alpha1.ModelDeploymentPhasePending,
				Conditions: []metav1.Condition{
					{
						Type:               aiv1alpha1.ConditionTypeProgressing,
						Status:             metav1.ConditionTrue,
						Reason:             aiv1alpha1.ReasonReconciling,
						Message:            "Initializing ModelDeployment",
						LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
					},
				},
			},
			newPhase:        aiv1alpha1.ModelDeploymentPhaseRunning,
			message:         "ModelDeployment is running successfully",
			expectedPhase:   aiv1alpha1.ModelDeploymentPhaseRunning,
			expectCondition: true,
			conditionType:   aiv1alpha1.ConditionTypeProgressing,
			conditionStatus: metav1.ConditionTrue,
		},
		{
			name: "update to failed phase",
			initialStatus: aiv1alpha1.ModelDeploymentStatus{
				Phase: aiv1alpha1.ModelDeploymentPhasePending,
			},
			newPhase:        aiv1alpha1.ModelDeploymentPhaseFailed,
			message:         "ModelDeployment failed to deploy",
			expectedPhase:   aiv1alpha1.ModelDeploymentPhaseFailed,
			expectCondition: true,
			conditionType:   aiv1alpha1.ConditionTypeProgressing,
			conditionStatus: metav1.ConditionTrue,
		},
		{
			name: "update to terminating phase",
			initialStatus: aiv1alpha1.ModelDeploymentStatus{
				Phase: aiv1alpha1.ModelDeploymentPhaseRunning,
			},
			newPhase:        aiv1alpha1.ModelDeploymentPhaseTerminating,
			message:         "ModelDeployment is being terminated",
			expectedPhase:   aiv1alpha1.ModelDeploymentPhaseTerminating,
			expectCondition: true,
			conditionType:   aiv1alpha1.ConditionTypeProgressing,
			conditionStatus: metav1.ConditionTrue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create ModelDeployment with initial status
			md := &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "ollama",
					Model:   "llama3:8b",
				},
				Status: tt.initialStatus,
			}

			// Create fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithStatusSubresource(&aiv1alpha1.ModelDeployment{}).
				WithRuntimeObjects(md).
				Build()

			reconciler := &ModelDeploymentReconciler{
				Client:    fakeClient,
				APIReader: fakeClient, // Use same client for APIReader in tests
				Scheme:    s,
			}

			ctx := context.Background()

			// Execute update
			err := reconciler.updateModelDeploymentStatus(ctx, md, tt.newPhase, tt.message)
			require.NoError(t, err, "Status update should succeed")

			// Re-fetch the object to verify the update (function updates a fresh copy)
			updatedMd := &aiv1alpha1.ModelDeployment{}
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(md), updatedMd)
			require.NoError(t, err, "Should be able to re-fetch updated object")

			// Verify phase was updated
			assert.Equal(t, tt.expectedPhase, updatedMd.Status.Phase, "Phase should be updated")

			// Verify condition was added/updated
			if tt.expectCondition {
				found := false
				for _, condition := range updatedMd.Status.Conditions {
					if condition.Type == tt.conditionType {
						assert.Equal(t, tt.conditionStatus, condition.Status, "Condition status should match")
						assert.Equal(t, aiv1alpha1.ReasonReconciling, condition.Reason, "Condition reason should be Reconciling")
						assert.Equal(t, tt.message, condition.Message, "Condition message should match")
						found = true
						break
					}
				}
				assert.True(t, found, "Expected condition should be present")
			}
		})
	}
}

func TestUpdateCondition(t *testing.T) {
	// Set up scheme
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name               string
		initialConditions  []metav1.Condition
		conditionType      string
		status             metav1.ConditionStatus
		reason             string
		message            string
		expectNewCondition bool
		expectUpdate       bool
	}{
		{
			name:               "add new condition to empty list",
			initialConditions:  []metav1.Condition{},
			conditionType:      aiv1alpha1.ConditionTypeReady,
			status:             metav1.ConditionTrue,
			reason:             aiv1alpha1.ReasonDeploymentReady,
			message:            "Deployment is ready",
			expectNewCondition: true,
			expectUpdate:       true,
		},
		{
			name: "add new condition to existing list",
			initialConditions: []metav1.Condition{
				{
					Type:               aiv1alpha1.ConditionTypeProgressing,
					Status:             metav1.ConditionTrue,
					Reason:             aiv1alpha1.ReasonReconciling,
					Message:            "Reconciling resources",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			conditionType:      aiv1alpha1.ConditionTypeReady,
			status:             metav1.ConditionTrue,
			reason:             aiv1alpha1.ReasonDeploymentReady,
			message:            "Deployment is ready",
			expectNewCondition: true,
			expectUpdate:       true,
		},
		{
			name: "update existing condition with different status",
			initialConditions: []metav1.Condition{
				{
					Type:               aiv1alpha1.ConditionTypeReady,
					Status:             metav1.ConditionFalse,
					Reason:             "NotReady",
					Message:            "Deployment not ready",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			conditionType:      aiv1alpha1.ConditionTypeReady,
			status:             metav1.ConditionTrue,
			reason:             aiv1alpha1.ReasonDeploymentReady,
			message:            "Deployment is ready",
			expectNewCondition: false,
			expectUpdate:       true,
		},
		{
			name: "update existing condition with different reason",
			initialConditions: []metav1.Condition{
				{
					Type:               aiv1alpha1.ConditionTypeGPUAllocated,
					Status:             metav1.ConditionTrue,
					Reason:             "OldReason",
					Message:            "GPU allocated",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			conditionType:      aiv1alpha1.ConditionTypeGPUAllocated,
			status:             metav1.ConditionTrue,
			reason:             aiv1alpha1.ReasonGPUAllocated,
			message:            "GPU allocated successfully",
			expectNewCondition: false,
			expectUpdate:       true,
		},
		{
			name: "update existing condition with different message",
			initialConditions: []metav1.Condition{
				{
					Type:               aiv1alpha1.ConditionTypeProgressing,
					Status:             metav1.ConditionTrue,
					Reason:             aiv1alpha1.ReasonReconciling,
					Message:            "Old message",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			conditionType:      aiv1alpha1.ConditionTypeProgressing,
			status:             metav1.ConditionTrue,
			reason:             aiv1alpha1.ReasonReconciling,
			message:            "New message",
			expectNewCondition: false,
			expectUpdate:       true,
		},
		{
			name: "no update when condition is identical",
			initialConditions: []metav1.Condition{
				{
					Type:               aiv1alpha1.ConditionTypeReady,
					Status:             metav1.ConditionTrue,
					Reason:             aiv1alpha1.ReasonDeploymentReady,
					Message:            "Deployment is ready",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
				},
			},
			conditionType:      aiv1alpha1.ConditionTypeReady,
			status:             metav1.ConditionTrue,
			reason:             aiv1alpha1.ReasonDeploymentReady,
			message:            "Deployment is ready",
			expectNewCondition: false,
			expectUpdate:       true, // Still updates LastTransitionTime
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create ModelDeployment with initial conditions
			md := &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "ollama",
					Model:   "llama3:8b",
				},
				Status: aiv1alpha1.ModelDeploymentStatus{
					Conditions: tt.initialConditions,
				},
			}

			// Create fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithStatusSubresource(&aiv1alpha1.ModelDeployment{}).
				WithRuntimeObjects(md).
				Build()

			reconciler := &ModelDeploymentReconciler{
				Client:    fakeClient,
				APIReader: fakeClient, // Use same client for APIReader in tests
				Scheme:    s,
			}

			ctx := context.Background()
			initialConditionCount := len(md.Status.Conditions)

			// Execute update
			err := reconciler.updateCondition(ctx, md, tt.conditionType, tt.status, tt.reason, tt.message)
			require.NoError(t, err, "Condition update should succeed")

			// Re-fetch the object to verify the update (function updates a fresh copy)
			updatedMd := &aiv1alpha1.ModelDeployment{}
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(md), updatedMd)
			require.NoError(t, err, "Should be able to re-fetch updated object")

			// Verify condition count
			if tt.expectNewCondition {
				assert.Equal(t, initialConditionCount+1, len(updatedMd.Status.Conditions), "Should add new condition")
			} else {
				assert.Equal(t, initialConditionCount, len(updatedMd.Status.Conditions), "Should not add new condition")
			}

			// Find and verify the condition
			found := false
			for _, condition := range updatedMd.Status.Conditions {
				if condition.Type == tt.conditionType {
					assert.Equal(t, tt.status, condition.Status, "Condition status should match")
					assert.Equal(t, tt.reason, condition.Reason, "Condition reason should match")
					assert.Equal(t, tt.message, condition.Message, "Condition message should match")
					assert.False(t, condition.LastTransitionTime.IsZero(), "LastTransitionTime should be set")
					found = true
					break
				}
			}
			assert.True(t, found, "Condition should be found")
		})
	}
}

func TestUpdateEndpointStatus(t *testing.T) {
	// Set up scheme
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name             string
		modelDeployment  *aiv1alpha1.ModelDeployment
		service          *corev1.Service
		expectedInternal string
		expectedExternal string
	}{
		{
			name: "basic service endpoint update",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "ollama",
					Model:   "llama3:8b",
				},
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "default",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       11434,
							TargetPort: intstr.FromString("http"),
							Name:       "http",
						},
					},
				},
			},
			expectedInternal: "test-model.default.svc.cluster.local:11434",
		},
		{
			name: "service with custom port",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "custom-port-model",
					Namespace: "production",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "vllm",
					Model:   "mistral:7b",
				},
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "custom-port-model",
					Namespace: "production",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       8080,
							TargetPort: intstr.FromString("http"),
							Name:       "http",
						},
					},
				},
			},
			expectedInternal: "custom-port-model.production.svc.cluster.local:8080",
		},
		{
			name: "service with multiple ports (should use first)",
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-port-model",
					Namespace: "default",
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend: "tgi",
					Model:   "codellama:34b",
				},
			},
			service: &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-port-model",
					Namespace: "default",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{
						{
							Port:       11434,
							TargetPort: intstr.FromString("http"),
							Name:       "http",
						},
						{
							Port:       9090,
							TargetPort: intstr.FromString("metrics"),
							Name:       "metrics",
						},
					},
				},
			},
			expectedInternal: "multi-port-model.default.svc.cluster.local:11434",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithStatusSubresource(&aiv1alpha1.ModelDeployment{}).
				WithRuntimeObjects(tt.modelDeployment).
				Build()

			reconciler := &ModelDeploymentReconciler{
				Client:    fakeClient,
				APIReader: fakeClient, // Use same client for APIReader in tests
				Scheme:    s,
			}

			ctx := context.Background()

			// Execute update
			err := reconciler.updateEndpointStatus(ctx, tt.modelDeployment, tt.service)
			require.NoError(t, err, "Endpoint status update should succeed")

			// Re-fetch the object to verify the update (function updates a fresh copy)
			updatedMd := &aiv1alpha1.ModelDeployment{}
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tt.modelDeployment), updatedMd)
			require.NoError(t, err, "Should be able to re-fetch updated object")

			// Verify endpoint status
			require.NotNil(t, updatedMd.Status.Endpoints, "Endpoints should be set")
			assert.Equal(t, tt.expectedInternal, updatedMd.Status.Endpoints.Internal, "Internal endpoint should match")

			if tt.expectedExternal != "" {
				assert.Equal(t, tt.expectedExternal, updatedMd.Status.Endpoints.External, "External endpoint should match")
			}

			// Verify EndpointReady condition was set
			found := false
			for _, condition := range updatedMd.Status.Conditions {
				if condition.Type == aiv1alpha1.ConditionTypeEndpointReady {
					assert.Equal(t, metav1.ConditionTrue, condition.Status, "EndpointReady should be True")
					assert.Equal(t, aiv1alpha1.ReasonServiceReady, condition.Reason, "Reason should be ServiceReady")
					assert.Equal(t, "Service endpoint is ready", condition.Message, "Message should match")
					found = true
					break
				}
			}
			assert.True(t, found, "EndpointReady condition should be present")
		})
	}
}

func TestStatusManagementIntegration(t *testing.T) {
	// Integration test for complete status management workflow
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "integration-test",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			Model:   "llama3:8b",
		},
		Status: aiv1alpha1.ModelDeploymentStatus{},
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "integration-test",
			Namespace: "default",
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Port:       11434,
					TargetPort: intstr.FromString("http"),
					Name:       "http",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha1.ModelDeployment{}).
		WithRuntimeObjects(md).
		Build()

	reconciler := &ModelDeploymentReconciler{
		Client:    fakeClient,
		APIReader: fakeClient, // Use same client for APIReader in tests
		Scheme:    s,
	}

	ctx := context.Background()

	// Step 1: Initialize to Pending
	err := reconciler.updateModelDeploymentStatus(ctx, md, aiv1alpha1.ModelDeploymentPhasePending, "Initializing ModelDeployment")
	require.NoError(t, err, "Initial status update should succeed")

	// Re-fetch to verify (function updates a fresh copy)
	updatedMd := &aiv1alpha1.ModelDeployment{}
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(md), updatedMd)
	require.NoError(t, err, "Should be able to re-fetch")
	assert.Equal(t, aiv1alpha1.ModelDeploymentPhasePending, updatedMd.Status.Phase)

	// Step 2: Add GPU allocation condition
	err = reconciler.updateCondition(ctx, md, aiv1alpha1.ConditionTypeGPUAllocated, metav1.ConditionTrue, aiv1alpha1.ReasonGPUAllocated, "GPU resources allocated")
	require.NoError(t, err, "GPU condition update should succeed")

	// Step 3: Add model loaded condition
	err = reconciler.updateCondition(ctx, md, aiv1alpha1.ConditionTypeModelLoaded, metav1.ConditionTrue, "ModelLoaded", "Model loaded successfully")
	require.NoError(t, err, "Model loaded condition update should succeed")

	// Step 4: Update endpoint status
	err = reconciler.updateEndpointStatus(ctx, md, service)
	require.NoError(t, err, "Endpoint status update should succeed")

	// Step 5: Move to Running
	err = reconciler.updateModelDeploymentStatus(ctx, md, aiv1alpha1.ModelDeploymentPhaseRunning, "ModelDeployment is running successfully")
	require.NoError(t, err, "Running status update should succeed")

	// Step 6: Set final Ready condition
	err = reconciler.updateCondition(ctx, md, aiv1alpha1.ConditionTypeReady, metav1.ConditionTrue, aiv1alpha1.ReasonDeploymentReady, "All resources are ready and healthy")
	require.NoError(t, err, "Ready condition update should succeed")

	// Re-fetch to verify final state (all functions update a fresh copy)
	finalMd := &aiv1alpha1.ModelDeployment{}
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(md), finalMd)
	require.NoError(t, err, "Should be able to re-fetch final state")

	// Verify final state
	assert.Equal(t, aiv1alpha1.ModelDeploymentPhaseRunning, finalMd.Status.Phase, "Final phase should be Running")

	// Verify all expected conditions are present
	expectedConditions := []string{
		aiv1alpha1.ConditionTypeProgressing,
		aiv1alpha1.ConditionTypeGPUAllocated,
		aiv1alpha1.ConditionTypeModelLoaded,
		aiv1alpha1.ConditionTypeEndpointReady,
		aiv1alpha1.ConditionTypeReady,
	}

	for _, conditionType := range expectedConditions {
		found := false
		for _, condition := range finalMd.Status.Conditions {
			if condition.Type == conditionType {
				assert.Equal(t, metav1.ConditionTrue, condition.Status, "Condition %s should be True", conditionType)
				found = true
				break
			}
		}
		assert.True(t, found, "Condition %s should be present", conditionType)
	}

	// Verify endpoint status
	require.NotNil(t, finalMd.Status.Endpoints, "Endpoints should be set")
	assert.Equal(t, "integration-test.default.svc.cluster.local:11434", finalMd.Status.Endpoints.Internal, "Internal endpoint should be set")
}

func TestStatusUpdateErrorHandling(t *testing.T) {
	// Test error handling in status updates
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	md := &aiv1alpha1.ModelDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
		},
		Spec: aiv1alpha1.ModelDeploymentSpec{
			Backend: "ollama",
			Model:   "llama3:8b",
		},
	}

	// Create client without the ModelDeployment to simulate not found error
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithStatusSubresource(&aiv1alpha1.ModelDeployment{}).
		Build()

	reconciler := &ModelDeploymentReconciler{
		Client:    fakeClient,
		APIReader: fakeClient, // Use same client for APIReader in tests
		Scheme:    s,
	}

	ctx := context.Background()

	// This should fail because the ModelDeployment doesn't exist in the client
	err := reconciler.updateModelDeploymentStatus(ctx, md, aiv1alpha1.ModelDeploymentPhasePending, "Test message")
	assert.Error(t, err, "Status update should fail when resource doesn't exist")

	err = reconciler.updateCondition(ctx, md, aiv1alpha1.ConditionTypeReady, metav1.ConditionTrue, "TestReason", "Test message")
	assert.Error(t, err, "Condition update should fail when resource doesn't exist")
}
