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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func TestFinalizerIntegration(t *testing.T) {
	// Integration test for finalizer workflow using stdlib slices
	finalizers := []string{
		"kubernetes.io/pv-protection",
		"some.other/finalizer",
	}

	// Test adding finalizer
	assert.False(t, slices.Contains(finalizers, aiv1alpha1.ModelDeploymentFinalizer), "Finalizer should not be present initially")

	// Add finalizer
	finalizers = append(finalizers, aiv1alpha1.ModelDeploymentFinalizer)
	assert.True(t, slices.Contains(finalizers, aiv1alpha1.ModelDeploymentFinalizer), "Finalizer should be present after adding")

	// Verify other finalizers are preserved
	assert.True(t, slices.Contains(finalizers, "kubernetes.io/pv-protection"), "Other finalizers should be preserved")
	assert.True(t, slices.Contains(finalizers, "some.other/finalizer"), "Other finalizers should be preserved")

	// Remove finalizer
	finalizers = slices.DeleteFunc(finalizers, func(v string) bool { return v == aiv1alpha1.ModelDeploymentFinalizer })
	assert.False(t, slices.Contains(finalizers, aiv1alpha1.ModelDeploymentFinalizer), "Finalizer should be removed")

	// Verify other finalizers are still preserved
	assert.True(t, slices.Contains(finalizers, "kubernetes.io/pv-protection"), "Other finalizers should still be preserved")
	assert.True(t, slices.Contains(finalizers, "some.other/finalizer"), "Other finalizers should still be preserved")

	expectedFinalizers := []string{"kubernetes.io/pv-protection", "some.other/finalizer"}
	assert.Equal(t, expectedFinalizers, finalizers, "Final finalizer list should match expected")
}

func TestFinalizerEdgeCases(t *testing.T) {
	t.Run("remove from nil slice", func(t *testing.T) {
		var nilSlice []string
		result := slices.DeleteFunc(nilSlice, func(v string) bool { return v == "test" })
		assert.Empty(t, result, "Removing from nil slice should return empty slice")
	})

	t.Run("contains in nil slice", func(t *testing.T) {
		var nilSlice []string
		result := slices.Contains(nilSlice, "test")
		assert.False(t, result, "Contains in nil slice should return false")
	})

	t.Run("contains and remove with special characters", func(t *testing.T) {
		specialString := "flexinfer.ai/cleanup-with-special-chars!@#$%^&*()"
		slice := []string{"normal", specialString, "other"}

		assert.True(t, slices.Contains(slice, specialString), "Should find string with special characters")

		result := slices.DeleteFunc(slice, func(v string) bool { return v == specialString })
		expected := []string{"normal", "other"}
		assert.Equal(t, expected, result, "Should remove string with special characters")
	})
}
