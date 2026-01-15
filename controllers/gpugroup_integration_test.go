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
	"fmt"
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
			modelAName := fmt.Sprintf("%s-model-a", gpuGroupName)
			modelBName := fmt.Sprintf("%s-model-b", gpuGroupName)
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, modelAName, modelBName)

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
			modelAName := fmt.Sprintf("%s-model-a", gpuGroupName)
			modelBName := fmt.Sprintf("%s-model-b", gpuGroupName)
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, modelAName, modelBName)

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments for the models
			mdA := testutil.NewTestModelDeployment(modelAName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdB := testutil.NewTestModelDeployment(modelBName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(80),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			// Signal demand for model-b
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelBName, 5)).Should(Succeed())

			// Wait for model-b to become active
			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal(modelBName))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdB)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When swapping models", func() {
		It("Should swap from one model to another on demand", func() {
			gpuGroupName := "test-gpugroup-swap"
			modelAName := fmt.Sprintf("%s-model-a", gpuGroupName)
			modelBName := fmt.Sprintf("%s-model-b", gpuGroupName)
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, modelAName, modelBName)

			// Disable anti-thrashing for faster testing
			gpuGroup.Spec.AntiThrashing.Enabled = false

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdA := testutil.NewTestModelDeployment(modelAName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdB := testutil.NewTestModelDeployment(modelBName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(80),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand for model-a
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelAName, 5)).Should(Succeed())

			// Wait for model-a to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal(modelAName))

			// Clear demand for model-a, signal demand for model-b
			Expect(testutil.ClearDemand(ctx, k8sClient, gpuGroupName, modelAName)).Should(Succeed())
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelBName, 5)).Should(Succeed())

			// Wait for model-b to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal(modelBName))

			// Verify model-a was scaled down
			mdALookupKey := types.NamespacedName{Name: modelAName, Namespace: "default"}
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
			mdBLookupKey := types.NamespacedName{Name: modelBName, Namespace: "default"}
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
			modelAName := fmt.Sprintf("%s-model-a", gpuGroupName)
			modelBName := fmt.Sprintf("%s-model-b", gpuGroupName)
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, modelAName, modelBName)

			// Set very short minimum run duration for testing
			gpuGroup.Spec.AntiThrashing.Enabled = true
			gpuGroup.Spec.AntiThrashing.MinimumRunDurationSeconds = 5
			gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds = 0 // Disable hysteresis for this test

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdA := testutil.NewTestModelDeployment(modelAName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdB := testutil.NewTestModelDeployment(modelBName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(80),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand for model-a to activate it
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelAName, 5)).Should(Succeed())

			// Wait for model-a to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal(modelAName))

			// Immediately signal demand for model-b (should be blocked by min run duration)
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelBName, 5)).Should(Succeed())

			// Short wait - model-a should still be active
			time.Sleep(1 * time.Second)

			currentGPUGroup := &aiv1alpha1.GPUGroup{}
			Expect(k8sClient.Get(ctx, gpuGroupLookupKey, currentGPUGroup)).Should(Succeed())
			Expect(currentGPUGroup.Status.ActiveModel).Should(Equal(modelAName))

			// Wait for minimum run duration to elapse, then swap should happen
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, time.Second*10, interval).Should(Equal(modelBName))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdB)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When model priority determines selection", func() {
		It("Should select higher priority model when both have demand", func() {
			gpuGroupName := "test-gpugroup-priority"
			modelHighName := fmt.Sprintf("%s-model-high", gpuGroupName)
			modelLowName := fmt.Sprintf("%s-model-low", gpuGroupName)
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, modelHighName, modelLowName)

			// Disable anti-thrashing for cleaner test
			gpuGroup.Spec.AntiThrashing.Enabled = false

			// Set explicit priorities
			gpuGroup.Spec.Models[0].Priority = 100 // modelHighName
			gpuGroup.Spec.Models[1].Priority = 50  // modelLowName

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdHigh := testutil.NewTestModelDeployment(modelHighName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(100),
			)
			mdLow := testutil.NewTestModelDeployment(modelLowName,
				testutil.MDWithGPUGroup(gpuGroupName),
				testutil.MDWithPriority(50),
			)

			Expect(k8sClient.Create(ctx, mdHigh)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdLow)).Should(Succeed())

			// Signal demand for BOTH models
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelHighName, 3)).Should(Succeed())
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelLowName, 10)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Higher priority model should be selected despite lower queue depth
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal(modelHighName))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdHigh)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, mdLow)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("When queue threshold is enforced", func() {
		It("Should not activate model below queue threshold", func() {
			gpuGroupName := "test-gpugroup-threshold"
			modelAName := fmt.Sprintf("%s-model-a", gpuGroupName)
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, modelAName)

			// Set high queue threshold
			gpuGroup.Spec.AntiThrashing.Enabled = true
			gpuGroup.Spec.AntiThrashing.RequestQueueThreshold = 10
			gpuGroup.Spec.AntiThrashing.HysteresisWindowSeconds = 0

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployment
			mdA := testutil.NewTestModelDeployment(modelAName,
				testutil.MDWithGPUGroup(gpuGroupName),
			)
			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand BELOW threshold
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelAName, 5)).Should(Succeed())

			// Wait and verify model is NOT activated
			time.Sleep(2 * time.Second)

			currentGPUGroup := &aiv1alpha1.GPUGroup{}
			Expect(k8sClient.Get(ctx, gpuGroupLookupKey, currentGPUGroup)).Should(Succeed())
			Expect(currentGPUGroup.Status.ActiveModel).Should(BeEmpty())

			// Signal demand AT threshold
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelAName, 10)).Should(Succeed())

			// Now model should be activated
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal(modelAName))

			// Clean up
			Expect(k8sClient.Delete(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, gpuGroup)).Should(Succeed())
		})
	})

	Context("GPUGroup Status Updates", func() {
		It("Should update ModelStatuses correctly", func() {
			gpuGroupName := "test-gpugroup-status"
			modelAName := fmt.Sprintf("%s-model-a", gpuGroupName)
			modelBName := fmt.Sprintf("%s-model-b", gpuGroupName)
			gpuGroup := testutil.NewTestGPUGroup(gpuGroupName, modelAName, modelBName)
			gpuGroup.Spec.AntiThrashing.Enabled = false

			// Create GPUGroup
			Expect(k8sClient.Create(ctx, gpuGroup)).Should(Succeed())

			// Create ModelDeployments
			mdA := testutil.NewTestModelDeployment(modelAName,
				testutil.MDWithGPUGroup(gpuGroupName),
			)
			mdB := testutil.NewTestModelDeployment(modelBName,
				testutil.MDWithGPUGroup(gpuGroupName),
			)

			Expect(k8sClient.Create(ctx, mdA)).Should(Succeed())
			Expect(k8sClient.Create(ctx, mdB)).Should(Succeed())

			gpuGroupLookupKey := types.NamespacedName{Name: gpuGroupName, Namespace: "default"}

			// Signal demand for model-a
			Expect(testutil.SimulateDemand(ctx, k8sClient, gpuGroupName, modelAName, 5)).Should(Succeed())

			// Wait for model-a to become active
			Eventually(func() string {
				updatedGPUGroup := &aiv1alpha1.GPUGroup{}
				if err := k8sClient.Get(ctx, gpuGroupLookupKey, updatedGPUGroup); err != nil {
					return ""
				}
				return updatedGPUGroup.Status.ActiveModel
			}, timeout, interval).Should(Equal(modelAName))

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
				if ms.Name == modelAName {
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
