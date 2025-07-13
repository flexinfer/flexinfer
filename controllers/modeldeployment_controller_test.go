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
	"k8s.io/utils/pointer"

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
					Replicas: pointer.Int32(1),
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
			Expect(createdDeployment.Spec.Template.Spec.Containers[0].Image).To(Equal("ghcr.io/flexinfer/ollama:latest"))

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
	})
})
