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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	"github.com/flexinfer/flexinfer/controllers/testutil"
)

var _ = Describe("GPUGroup Controller Integration", func() {
	const (
		timeout  = time.Second * 30
		interval = time.Millisecond * 250
	)

	Context("When creating a GPUGroup with models", func() {
		It("Should initialize with no active model", func() {
			gpuGroupName := "test-gpugroup-init"
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, "model-a", "model-b")

			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}
			createdGPUGroup := &aiv1alpha1.GPUGroup{}

			// Wait for the GPUGroup to be created
			Eventually(func() error {
				return k8sClient.Get(ctx, gpuGroupLookupKey, createdGPUGroup)
			}, timeout, interval).Should(Succeed())

			// Initially, no model should be active (no demand)
			Expect(createdGPUGroup.Status.ActiveModel).Should(BeEmpty())

			// Clean up
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When signaling demand via annotations", func() {
		It("Should activate the model with demand", func() {
			gpuGroupName := "test-gpugroup-demand"
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, "model-a", "model-b")

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments for the models
			mdA := testutil.NewTestModelDeployment("model-a",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdB := testutil.NewTestModelDeployment("model-b",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(80),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			// Signal demand for model-b
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-b", 5)).Should(Succeed())

			// Wait for model-b to become active
			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal("model-b"))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdB)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When swapping models", func() {
		It("Should swap from one model to another on demand", func() {
			gpuGroupName := "test-gpugroup-swap"
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, "model-a", "model-b")

			// Disable anti-thrashing for faster testing
			gpuGroup.Spec.AntiThrashing.Enabled = false

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdA := testutil.NewTestModelDeployment("model-a",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdB := testutil.NewTestModelDeployment("model-b",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(80),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand for model-a
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-a", 5)).Should(Succeed())

			// Wait for model-a to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal("model-a"))

			// Clear demand for model-a, signal demand for model-b
			Expect(testutil.ClearDemand(ctx, k8sClient, gpuGroupName, "model-a")).Should(Succeed())
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-b", 5)).Should(Succeed())

			// Wait for model-b to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal("model-b"))

			// Verify model-a was scaled down
			mdALookupKey := types.NamespacedName{Name: "model-a", Namespace: "default"}
			Eventually(func() int32 {
				updatedMD := &aiv1alpha1.ModelDeployment{}
				if err := k8sClient.Get(ctx, mdALookupKey, updatedMD); err != nil {
					return -1
				}
				if updatedMD.Spec.Replicas == nil {
					return 0
				}
				return *updatedMD.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(0)))

			// Verify model-b was scaled up
			mdBLookupKey := types.NamespacedName{Name: "model-b", Namespace: "default"}
			Eventually(func() int32 {
				updatedMD := &aiv1alpha1.ModelDeployment{}
				if err := k8sClient.Get(ctx, mdBLookupKey, updatedMD); err != nil {
					return -1
				}
				if updatedMD.Spec.Replicas == nil {
					return 0
				}
				return *updatedMD.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(1)))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdB)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When anti-thrashing is enabled", func() {
		It("Should block rapid swaps during minimum run duration", func() {
			gpuGroupName := "test-gpugroup-antithrash"
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, "model-a", "model-b")

			// Set very short minimum run duration for testing
			gpuGroup.Spec.AntiThrashing.Enabled = true
			gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds = 5
			gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds = 0 // Disable hysteresis for this test

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdA := testutil.NewTestModelDeployment("model-a",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdB := testutil.NewTestModelDeployment("model-b",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(80),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand for model-a to activate it
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-a", 5)).Should(Succeed())

			// Wait for model-a to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal("model-a"))

			// Immediately signal demand for model-b (should be blocked by min run duration)
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-b", 5)).Should(Succeed())

			// Short wait - model-a should still be active
			time.Sleep(1 * time.Second)

			currentGPUGroup := &aiv1alpha1.GPUGroup{}
			Expect(k8sClient.Get(ctx, gpuGroupLookupKey, currentGPUGroup)).Should(Succeed())
			Expect(currentGPUGroup.Status.ActiveModel).Should(Equal("model-a"))

			// Wait for minimum run duration to elapse, then swap should happen
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, time.Second*10, interval).Should(Equal("model-b"))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdB)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When model priority determines selection", func() {
		It("Should select higher priority model when both have demand", func() {
			gpuGroupName := "test-gpugroup-priority"
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, "model-high", "model-low")

			// Disable anti-thrashing for cleaner test
			gpuGroup.Spec.AntiThrashing.Enabled = false

			// Set explicit priorities
			gpuGroup.Spec.Models[0].Priority = 100 // model-high
			gpuGroup.Spec.Models[1].Priority = 50  // model-low

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdHigh := testutil.NewTestModelDeployment("model-high",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdLow := testutil.NewTestModelDeployment("model-low",
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(50),
			)

			Expect(k8sClient.Create(ctx, mdHigh)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdLow)).Should(Succeed())

			// Signal demand for BOTH models
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-high", 3)).Should(Succeed())
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-low", 10)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Higher priority model should be selected despite lower queue depth
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal("model-high"))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdHigh)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdLow)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When queue threshold is enforced", func() {
		It("Should not activate model below queue threshold", func() {
			gpuGroupName := "test-gpugroup-threshold"
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, "model-a")

			// Set high queue threshold
			gpuGroup.Spec.AntiThrashing.Enabled = true
			gpuGroup.Spec.AntiThrashing.RequestQueueThreshold = 10
			gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds = 0

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployment
			mdA := testutil.NewTestModelDeployment("model-a",
				testutil.MDWithGPUGroup(gpuGroupName),
			)
			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand BELOW threshold
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-a", 5)).Should(Succeed())

			// Wait and verify model is NOT activated
			time.Sleep(2 * time.Second)

			currentGPUGroup := &aiv1alpha1.GPUGroup{}
			Expect(k8sClient.Get(ctx, gpuGroupLookupKey, currentGPUGroup)).Should(Succeed())
			Expect(currentGPUGroup.Status.ActiveModel).Should(BeEmpty())

			// Signal demand AT threshold
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-a", 10)).Should(Succeed())

			// Now model should be activated
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal("model-a"))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("GPUGroup Status Updates", func() {
		It("Should update ModelStatuses correctly", func() {
			gpuGroupName := "test-gpugroup-status"
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, "model-a", "model-b")
			gpuGroup.Spec.AntiThrashing.Enabled = false

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdA := testutil.NewTestModelDeployment("model-a",
				testutil.MDWithGPUGroup(gpuGroupName),
			)
			mdB := testutil.NewTestModelDeployment("model-b",
				testutil.MDWithGPUGroup(gpuGroupName),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand for model-a
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, "model-a", 5)).Should(Succeed())

			// Wait for model-a to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal("model-a"))

			// Check model statuses
			Eventually(func() int {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return 0
				}
				return len(updatedGPUGroup.Status.ModelStatuses)
			}, timeout, interval).Should(BeNumerically(">", 0))

			// Find model-a status
			currentGPUGroup := &aiv1alpha1.GPUGroup{}
			Expect(k8sClient.Get(ctx, gpuGroupLookupKey, currentGPUGroup)).Should(Succeed())

			var modelAStatus *aiv1alpha1.GPUGroupModelStatus
			for i, ms := range currentGPUGroup.Status.ModelStatuses {
				if ms.Name == "model-a" {
					modelAStatus = &currentGPUGroup.Status.ModelStatuses[i]
					break
				}
			}

			Expect(modelAStatus).ShouldNot(BeNil())
			Expect(modelAStatus.State).Should(Equal(aiv1alpha1.ModelGroupStateActive))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdB)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})
})
