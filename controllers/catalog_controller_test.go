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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestRegistryTypeFromCRD(t *testing.T) {
	tests := []struct {
		input    aiv1alpha2.RegistrySourceType
		expected string
	}{
		{aiv1alpha2.RegistrySourceOCI, "oci"},
		{aiv1alpha2.RegistrySourceHuggingFace, "huggingface"},
		{aiv1alpha2.RegistrySourceOllama, "ollama"},
		{aiv1alpha2.RegistrySourceType("Custom"), "custom"},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			result := registryTypeFromCRD(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSetCatalogCondition_NewCondition(t *testing.T) {
	catalog := &aiv1alpha2.ModelCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-catalog",
			Generation: 1,
		},
	}

	setCatalogCondition(catalog, "Synced", true, "SyncSucceeded", "synced 5 models")

	assert.Len(t, catalog.Status.Conditions, 1)
	cond := catalog.Status.Conditions[0]
	assert.Equal(t, "Synced", cond.Type)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "SyncSucceeded", cond.Reason)
	assert.Equal(t, "synced 5 models", cond.Message)
	assert.Equal(t, int64(1), cond.ObservedGeneration)
}

func TestSetCatalogCondition_UpdateExisting(t *testing.T) {
	originalTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	catalog := &aiv1alpha2.ModelCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-catalog",
			Generation: 2,
		},
		Status: aiv1alpha2.ModelCatalogStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Synced",
					Status:             metav1.ConditionTrue,
					Reason:             "SyncSucceeded",
					Message:            "old message",
					LastTransitionTime: originalTime,
					ObservedGeneration: 1,
				},
			},
		},
	}

	// Update with same status (True) — should preserve LastTransitionTime
	setCatalogCondition(catalog, "Synced", true, "SyncSucceeded", "synced 10 models")

	assert.Len(t, catalog.Status.Conditions, 1)
	cond := catalog.Status.Conditions[0]
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, "synced 10 models", cond.Message)
	assert.Equal(t, originalTime, cond.LastTransitionTime, "should preserve transition time when status unchanged")
	assert.Equal(t, int64(2), cond.ObservedGeneration)
}

func TestSetCatalogCondition_StatusTransition(t *testing.T) {
	originalTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	catalog := &aiv1alpha2.ModelCatalog{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-catalog",
			Generation: 2,
		},
		Status: aiv1alpha2.ModelCatalogStatus{
			Conditions: []metav1.Condition{
				{
					Type:               "Synced",
					Status:             metav1.ConditionTrue,
					Reason:             "SyncSucceeded",
					Message:            "was synced",
					LastTransitionTime: originalTime,
					ObservedGeneration: 1,
				},
			},
		},
	}

	// Transition from True -> False
	setCatalogCondition(catalog, "Synced", false, "SyncFailed", "all registries failed")

	assert.Len(t, catalog.Status.Conditions, 1)
	cond := catalog.Status.Conditions[0]
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, "SyncFailed", cond.Reason)
	assert.NotEqual(t, originalTime, cond.LastTransitionTime, "should update transition time on status change")
}

func TestSetCatalogCondition_MultipleTypes(t *testing.T) {
	catalog := &aiv1alpha2.ModelCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "test-catalog"},
	}

	setCatalogCondition(catalog, "Synced", true, "SyncSucceeded", "ok")
	setCatalogCondition(catalog, "Ready", true, "Ready", "ready")

	assert.Len(t, catalog.Status.Conditions, 2)
	assert.Equal(t, "Synced", catalog.Status.Conditions[0].Type)
	assert.Equal(t, "Ready", catalog.Status.Conditions[1].Type)
}

func TestCatalogReconcile_CatalogNotFound(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, aiv1alpha2.AddToScheme(s))

	cl := fake.NewClientBuilder().WithScheme(s).Build()
	rec := &FakeEventRecorder{}

	r := &ModelCatalogReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "nonexistent", Namespace: "default"},
	})

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

func TestCatalogReconcile_NoRegistries(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, aiv1alpha2.AddToScheme(s))

	catalog := &aiv1alpha2.ModelCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-catalog", Namespace: "default"},
		Spec: aiv1alpha2.ModelCatalogSpec{
			Registries: []aiv1alpha2.RegistrySource{},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(catalog).
		WithStatusSubresource(catalog).Build()
	rec := &FakeEventRecorder{}

	r := &ModelCatalogReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "empty-catalog", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, 1*time.Hour, result.RequeueAfter) // default sync interval

	updated := &aiv1alpha2.ModelCatalog{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "empty-catalog", Namespace: "default"}, updated))
	assert.Equal(t, 0, updated.Status.TotalModels)
	assert.NotNil(t, updated.Status.LastSyncTime)

	// Should have Synced condition = True (no errors, 0 models is still a success)
	require.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, "Synced", updated.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, updated.Status.Conditions[0].Status)
	assert.Equal(t, "SyncSucceeded", updated.Status.Conditions[0].Reason)
}

func TestCatalogReconcile_UnknownRegistryType(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, aiv1alpha2.AddToScheme(s))

	catalog := &aiv1alpha2.ModelCatalog{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-registry", Namespace: "default"},
		Spec: aiv1alpha2.ModelCatalogSpec{
			Registries: []aiv1alpha2.RegistrySource{
				{Type: aiv1alpha2.RegistrySourceType("nonexistent"), URL: "https://example.com"},
			},
		},
	}

	cl := fake.NewClientBuilder().WithScheme(s).
		WithObjects(catalog).
		WithStatusSubresource(catalog).Build()
	rec := &FakeEventRecorder{}

	r := &ModelCatalogReconciler{
		Client:   cl,
		Scheme:   s,
		Recorder: rec,
	}

	result, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "bad-registry", Namespace: "default"},
	})

	require.NoError(t, err)
	assert.Equal(t, 1*time.Hour, result.RequeueAfter)

	updated := &aiv1alpha2.ModelCatalog{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "bad-registry", Namespace: "default"}, updated))
	assert.Equal(t, 0, updated.Status.TotalModels)

	// With 0 entries from failed registries, condition should be False
	require.Len(t, updated.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionFalse, updated.Status.Conditions[0].Status)
	assert.Equal(t, "SyncPartialFailure", updated.Status.Conditions[0].Reason)
}
