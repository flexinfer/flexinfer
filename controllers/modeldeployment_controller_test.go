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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var _ = Describe("ModelDeployment controller", func() {
	const (
		ModelDeploymentName      = "test-modeldeployment"
		ModelDeploymentNamespace = "default"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a ModelDeployment", func() {
		It("Should create a Deployment, Service, PVC, and benchmark Job", func() {
			By("By creating a new ModelDeployment")
			ctx := context.Background()
			md := &aiv1alpha1.ModelDeployment{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "ai.flexinfer/v1alpha1",
					Kind:       "ModelDeployment",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      ModelDeploymentName,
					Namespace: ModelDeploymentNamespace,
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:  "ollama",
					Model:    "test-model",
					Replicas: ptr.To(int32(1)),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, md)).Should(Succeed())

			// We check for the benchmark job first, as it's the first thing the reconciler creates.
			jobLookupKey := types.NamespacedName{Name: ModelDeploymentName + "-benchmark", Namespace: ModelDeploymentNamespace}
			createdJob := &batchv1.Job{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, jobLookupKey, createdJob)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			// Manually update the job status to have one completion.
			By("By updating the benchmark job status")
			createdJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, createdJob)).Should(Succeed())

			// Manually create the benchmark result ConfigMap to simulate the job finishing
			By("By creating the benchmark result ConfigMap")
			benchmarkCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ModelDeploymentName + "-benchmark-results",
					Namespace: ModelDeploymentNamespace,
				},
				Data: map[string]string{"tokensPerSecond": "150.75"},
			}
			Expect(k8sClient.Create(ctx, benchmarkCM)).Should(Succeed())

			deploymentLookupKey := types.NamespacedName{Name: ModelDeploymentName, Namespace: ModelDeploymentNamespace}
			createdDeployment := &appsv1.Deployment{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, deploymentLookupKey, createdDeployment)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(createdDeployment.Spec.Template.Spec.Containers[0].Image).To(Equal("ollama/ollama:latest"))

			serviceLookupKey := types.NamespacedName{Name: ModelDeploymentName, Namespace: ModelDeploymentNamespace}
			createdService := &corev1.Service{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, serviceLookupKey, createdService)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(createdService.Spec.Ports[0].Port).To(Equal(int32(11434)))

			pvcLookupKey := types.NamespacedName{Name: ModelDeploymentName, Namespace: ModelDeploymentNamespace}
			createdPVC := &corev1.PersistentVolumeClaim{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, pvcLookupKey, createdPVC)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			Expect(createdPVC.Spec.Resources.Requests[corev1.ResourceStorage]).To(Equal(resource.MustParse("1Gi")))
		})

		It("Should apply LiteLLM and service label annotations to the Service when configured", func() {
			By("By creating a new ModelDeployment with LiteLLM and service labels")
			ctx := context.Background()

			litellmEnabled := true
			md := &aiv1alpha1.ModelDeployment{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "ai.flexinfer/v1alpha1",
					Kind:       "ModelDeployment",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      ModelDeploymentName + "-litellm",
					Namespace: ModelDeploymentNamespace,
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:  "ollama",
					Model:    "test-model",
					Replicas: ptr.To(int32(1)),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("1Gi"),
						},
					},
					LiteLLM: &aiv1alpha1.LiteLLMSpec{
						Enabled:         &litellmEnabled,
						ServedModelName: "my-served-model",
						Aliases:         []string{"alias-1", "alias-2"},
					},
					ServiceLabels: []string{"textgen", "chat"},
				},
			}
			Expect(k8sClient.Create(ctx, md)).Should(Succeed())

			// Wait for benchmark job creation and simulate completion like the base test.
			jobLookupKey := types.NamespacedName{Name: md.Name + "-benchmark", Namespace: md.Namespace}
			createdJob := &batchv1.Job{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, jobLookupKey, createdJob)
				return err == nil
			}, timeout, interval).Should(BeTrue())

			createdJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, createdJob)).Should(Succeed())

			benchmarkCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      md.Name + "-benchmark-results",
					Namespace: md.Namespace,
				},
				Data: map[string]string{"tokensPerSecond": "150.75"},
			}
			Expect(k8sClient.Create(ctx, benchmarkCM)).Should(Succeed())

			serviceLookupKey := types.NamespacedName{Name: md.Name, Namespace: md.Namespace}
			createdService := &corev1.Service{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, serviceLookupKey, createdService)
				if createdService.Annotations == nil {
					return ""
				}
				return createdService.Annotations["litellm.flexinfer.ai/served-model"]
			}, timeout, interval).Should(Equal("my-served-model"))

			Expect(createdService.Annotations["litellm.flexinfer.ai/aliases"]).To(Equal("alias-1,alias-2"))
			Expect(createdService.Annotations["flexinfer.ai/service-labels"]).To(Equal("textgen,chat"))

			// Disabling LiteLLM should remove the LiteLLM annotations from the Service.
			By("By disabling LiteLLM and verifying annotations are removed")
			litellmDisabled := false
			updated := &aiv1alpha1.ModelDeployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: md.Name, Namespace: md.Namespace}, updated)).Should(Succeed())
			updated.Spec.LiteLLM.Enabled = &litellmDisabled
			Expect(k8sClient.Update(ctx, updated)).Should(Succeed())

			Eventually(func() bool {
				_ = k8sClient.Get(ctx, serviceLookupKey, createdService)
				if createdService.Annotations == nil {
					return true
				}
				_, hasServed := createdService.Annotations["litellm.flexinfer.ai/served-model"]
				_, hasAliases := createdService.Annotations["litellm.flexinfer.ai/aliases"]
				return !hasServed && !hasAliases
			}, timeout, interval).Should(BeTrue())

			// ServiceLabels annotation should remain.
			Expect(createdService.Annotations["flexinfer.ai/service-labels"]).To(Equal("textgen,chat"))
		})
	})
})
