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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestContainsString(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		target   string
		expected bool
	}{
		{
			name:     "empty slice",
			slice:    []string{},
			target:   "test",
			expected: false,
		},
		{
			name:     "single element - contains",
			slice:    []string{"test"},
			target:   "test",
			expected: true,
		},
		{
			name:     "single element - does not contain",
			slice:    []string{"other"},
			target:   "test",
			expected: false,
		},
		{
			name:     "multiple elements - contains at start",
			slice:    []string{"test", "other", "values"},
			target:   "test",
			expected: true,
		},
		{
			name:     "multiple elements - contains in middle",
			slice:    []string{"first", "test", "last"},
			target:   "test",
			expected: true,
		},
		{
			name:     "multiple elements - contains at end",
			slice:    []string{"first", "second", "test"},
			target:   "test",
			expected: true,
		},
		{
			name:     "multiple elements - does not contain",
			slice:    []string{"first", "second", "third"},
			target:   "test",
			expected: false,
		},
		{
			name:     "empty string target - contains",
			slice:    []string{"", "test"},
			target:   "",
			expected: true,
		},
		{
			name:     "empty string target - does not contain",
			slice:    []string{"test", "other"},
			target:   "",
			expected: false,
		},
		{
			name:     "case sensitive",
			slice:    []string{"Test", "OTHER"},
			target:   "test",
			expected: false,
		},
		{
			name:     "finalizer string",
			slice:    []string{"kubernetes.io/pv-protection", aiv1alpha1.ModelDeploymentFinalizer, "other.finalizer/cleanup"},
			target:   aiv1alpha1.ModelDeploymentFinalizer,
			expected: true,
		},
		{
			name:     "duplicate elements",
			slice:    []string{"test", "test", "other"},
			target:   "test",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsString(tt.slice, tt.target)
			assert.Equal(t, tt.expected, result, "containsString result should match expected")
		})
	}
}

func TestRemoveString(t *testing.T) {
	tests := []struct {
		name     string
		slice    []string
		target   string
		expected []string
	}{
		{
			name:     "empty slice",
			slice:    []string{},
			target:   "test",
			expected: nil,
		},
		{
			name:     "single element - remove existing",
			slice:    []string{"test"},
			target:   "test",
			expected: nil,
		},
		{
			name:     "single element - remove non-existing",
			slice:    []string{"other"},
			target:   "test",
			expected: []string{"other"},
		},
		{
			name:     "multiple elements - remove from start",
			slice:    []string{"test", "other", "values"},
			target:   "test",
			expected: []string{"other", "values"},
		},
		{
			name:     "multiple elements - remove from middle",
			slice:    []string{"first", "test", "last"},
			target:   "test",
			expected: []string{"first", "last"},
		},
		{
			name:     "multiple elements - remove from end",
			slice:    []string{"first", "second", "test"},
			target:   "test",
			expected: []string{"first", "second"},
		},
		{
			name:     "multiple elements - remove non-existing",
			slice:    []string{"first", "second", "third"},
			target:   "test",
			expected: []string{"first", "second", "third"},
		},
		{
			name:     "remove empty string",
			slice:    []string{"", "test", "other"},
			target:   "",
			expected: []string{"test", "other"},
		},
		{
			name:     "case sensitive - no removal",
			slice:    []string{"Test", "OTHER"},
			target:   "test",
			expected: []string{"Test", "OTHER"},
		},
		{
			name:     "finalizer removal",
			slice:    []string{"kubernetes.io/pv-protection", aiv1alpha1.ModelDeploymentFinalizer, "other.finalizer/cleanup"},
			target:   aiv1alpha1.ModelDeploymentFinalizer,
			expected: []string{"kubernetes.io/pv-protection", "other.finalizer/cleanup"},
		},
		{
			name:     "remove multiple occurrences",
			slice:    []string{"test", "other", "test", "final"},
			target:   "test",
			expected: []string{"other", "final"},
		},
		{
			name:     "remove all elements",
			slice:    []string{"test", "test", "test"},
			target:   "test",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeString(tt.slice, tt.target)
			if tt.expected == nil {
				assert.Nil(t, result, "removeString result should be nil when expected")
			} else {
				assert.Equal(t, tt.expected, result, "removeString result should match expected")
			}

			// Note: Go slices may share underlying arrays in some cases,
			// but the removeString function creates a new slice when needed
		})
	}
}

func TestCleanupModelDeployment(t *testing.T) {
	// Set up scheme
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha1.AddToScheme(s))

	tests := []struct {
		name            string
		setupResources  func(t *testing.T) []runtime.Object
		modelDeployment *aiv1alpha1.ModelDeployment
		expectError     bool
		errorMessage    string
		validateCleanup func(t *testing.T, reconciler *ModelDeploymentReconciler, ctx context.Context, md *aiv1alpha1.ModelDeployment)
	}{
		{
			name: "cleanup all resources successfully",
			setupResources: func(t *testing.T) []runtime.Object {
				md := &aiv1alpha1.ModelDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-model",
						Namespace: "default",
					},
				}

				deployment := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-model",
						Namespace: "default",
					},
				}

				service := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-model",
						Namespace: "default",
					},
				}

				pvc := &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-model",
						Namespace: "default",
					},
				}

				job := &batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-model-benchmark",
						Namespace: "default",
					},
				}

				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-model-benchmark-results",
						Namespace: "default",
					},
				}

				return []runtime.Object{md, deployment, service, pvc, job, configMap}
			},
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-model",
					Namespace: "default",
				},
			},
			expectError: false,
			validateCleanup: func(t *testing.T, reconciler *ModelDeploymentReconciler, ctx context.Context, md *aiv1alpha1.ModelDeployment) {
				// Verify all resources are deleted
				deployment := &appsv1.Deployment{}
				err := reconciler.Get(ctx, types.NamespacedName{Name: md.Name, Namespace: md.Namespace}, deployment)
				assert.True(t, errors.IsNotFound(err), "Deployment should be deleted")

				service := &corev1.Service{}
				err = reconciler.Get(ctx, types.NamespacedName{Name: md.Name, Namespace: md.Namespace}, service)
				assert.True(t, errors.IsNotFound(err), "Service should be deleted")

				pvc := &corev1.PersistentVolumeClaim{}
				err = reconciler.Get(ctx, types.NamespacedName{Name: md.Name, Namespace: md.Namespace}, pvc)
				assert.True(t, errors.IsNotFound(err), "PVC should be deleted")

				job := &batchv1.Job{}
				err = reconciler.Get(ctx, types.NamespacedName{Name: md.Name + "-benchmark", Namespace: md.Namespace}, job)
				assert.True(t, errors.IsNotFound(err), "Benchmark job should be deleted")

				configMap := &corev1.ConfigMap{}
				err = reconciler.Get(ctx, types.NamespacedName{Name: md.Name + "-benchmark-results", Namespace: md.Namespace}, configMap)
				assert.True(t, errors.IsNotFound(err), "Benchmark ConfigMap should be deleted")
			},
		},
		{
			name: "cleanup with partially existing resources",
			setupResources: func(t *testing.T) []runtime.Object {
				md := &aiv1alpha1.ModelDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "partial-model",
						Namespace: "default",
					},
				}

				// Only create some resources
				deployment := &appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "partial-model",
						Namespace: "default",
					},
				}

				configMap := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "partial-model-benchmark-results",
						Namespace: "default",
					},
				}

				return []runtime.Object{md, deployment, configMap}
			},
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "partial-model",
					Namespace: "default",
				},
			},
			expectError: false,
			validateCleanup: func(t *testing.T, reconciler *ModelDeploymentReconciler, ctx context.Context, md *aiv1alpha1.ModelDeployment) {
				// Verify existing resources are deleted
				deployment := &appsv1.Deployment{}
				err := reconciler.Get(ctx, types.NamespacedName{Name: md.Name, Namespace: md.Namespace}, deployment)
				assert.True(t, errors.IsNotFound(err), "Deployment should be deleted")

				configMap := &corev1.ConfigMap{}
				err = reconciler.Get(ctx, types.NamespacedName{Name: md.Name + "-benchmark-results", Namespace: md.Namespace}, configMap)
				assert.True(t, errors.IsNotFound(err), "ConfigMap should be deleted")

				// Verify non-existing resources don't cause errors
				service := &corev1.Service{}
				err = reconciler.Get(ctx, types.NamespacedName{Name: md.Name, Namespace: md.Namespace}, service)
				assert.True(t, errors.IsNotFound(err), "Service should remain not found")
			},
		},
		{
			name: "cleanup with no existing resources",
			setupResources: func(t *testing.T) []runtime.Object {
				md := &aiv1alpha1.ModelDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "empty-model",
						Namespace: "default",
					},
				}
				return []runtime.Object{md}
			},
			modelDeployment: &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-model",
					Namespace: "default",
				},
			},
			expectError: false,
			validateCleanup: func(t *testing.T, reconciler *ModelDeploymentReconciler, ctx context.Context, md *aiv1alpha1.ModelDeployment) {
				// This should complete without errors even when no resources exist
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up fake client with resources
			resources := tt.setupResources(t)
			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithRuntimeObjects(resources...).
				Build()

			reconciler := &ModelDeploymentReconciler{
				Client: fakeClient,
				Scheme: s,
			}

			ctx := context.Background()

			// Execute cleanup
			err := reconciler.cleanupModelDeployment(ctx, tt.modelDeployment)

			// Verify error expectation
			if tt.expectError {
				require.Error(t, err, "Expected cleanup to fail")
				if tt.errorMessage != "" {
					assert.Contains(t, err.Error(), tt.errorMessage, "Error message should contain expected text")
				}
			} else {
				require.NoError(t, err, "Expected cleanup to succeed")
			}

			// Run validation if provided
			if tt.validateCleanup != nil {
				tt.validateCleanup(t, reconciler, ctx, tt.modelDeployment)
			}
		})
	}
}

func TestFinalizerIntegration(t *testing.T) {
	// Integration test for finalizer workflow
	finalizers := []string{
		"kubernetes.io/pv-protection",
		"some.other/finalizer",
	}

	// Test adding finalizer
	assert.False(t, containsString(finalizers, aiv1alpha1.ModelDeploymentFinalizer), "Finalizer should not be present initially")

	// Add finalizer
	finalizers = append(finalizers, aiv1alpha1.ModelDeploymentFinalizer)
	assert.True(t, containsString(finalizers, aiv1alpha1.ModelDeploymentFinalizer), "Finalizer should be present after adding")

	// Verify other finalizers are preserved
	assert.True(t, containsString(finalizers, "kubernetes.io/pv-protection"), "Other finalizers should be preserved")
	assert.True(t, containsString(finalizers, "some.other/finalizer"), "Other finalizers should be preserved")

	// Remove finalizer
	finalizers = removeString(finalizers, aiv1alpha1.ModelDeploymentFinalizer)
	assert.False(t, containsString(finalizers, aiv1alpha1.ModelDeploymentFinalizer), "Finalizer should be removed")

	// Verify other finalizers are still preserved
	assert.True(t, containsString(finalizers, "kubernetes.io/pv-protection"), "Other finalizers should still be preserved")
	assert.True(t, containsString(finalizers, "some.other/finalizer"), "Other finalizers should still be preserved")

	expectedFinalizers := []string{"kubernetes.io/pv-protection", "some.other/finalizer"}
	assert.Equal(t, expectedFinalizers, finalizers, "Final finalizer list should match expected")
}

func TestFinalizerEdgeCases(t *testing.T) {
	t.Run("remove from nil slice", func(t *testing.T) {
		var nilSlice []string
		result := removeString(nilSlice, "test")
		assert.Empty(t, result, "Removing from nil slice should return empty slice")
	})

	t.Run("contains in nil slice", func(t *testing.T) {
		var nilSlice []string
		result := containsString(nilSlice, "test")
		assert.False(t, result, "Contains in nil slice should return false")
	})

	t.Run("remove empty string from slice with empty strings", func(t *testing.T) {
		slice := []string{"", "", "test", ""}
		result := removeString(slice, "")
		expected := []string{"test"}
		assert.Equal(t, expected, result, "Should remove all empty strings")
	})

	t.Run("contains and remove with special characters", func(t *testing.T) {
		specialString := "flexinfer.ai/cleanup-with-special-chars!@#$%^&*()"
		slice := []string{"normal", specialString, "other"}

		assert.True(t, containsString(slice, specialString), "Should find string with special characters")

		result := removeString(slice, specialString)
		expected := []string{"normal", "other"}
		assert.Equal(t, expected, result, "Should remove string with special characters")
	})
}
