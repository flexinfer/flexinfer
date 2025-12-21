package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var _ = Describe("ModelDeployment Controller", func() {

	const (
		ModelDeploymentName      = "test-model-deployment"
		ModelDeploymentNamespace = "default"
		JobName                  = "test-job"
	)

	Context("When updating ModelDeployment Status", func() {
		It("Should update ModelMetrics correctly", func() {
			By("Creating a new ModelDeployment")
			ctx := context.Background()
			modelDeployment := &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ModelDeploymentName,
					Namespace: ModelDeploymentNamespace,
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Backend:  "ollama",
					Model:    "llama2",
					Replicas: func() *int32 { i := int32(1); return &i }(),
				},
			}
			Expect(k8sClient.Create(ctx, modelDeployment)).Should(Succeed())

			modelDeploymentLookupKey := types.NamespacedName{Name: ModelDeploymentName, Namespace: ModelDeploymentNamespace}
			createdModelDeployment := &aiv1alpha1.ModelDeployment{}

			Eventually(func() bool {
				err := k8sClient.Get(ctx, modelDeploymentLookupKey, createdModelDeployment)
				return err == nil
			}, time.Minute, time.Second).Should(BeTrue())

			By("Updating the ModelMetrics")
			createdModelDeployment.Status.Metrics = &aiv1alpha1.ModelMetrics{
				TokensPerSecond:  "50.5",
				AvgModelLoadTime: "2.3",
				AvgLatencyMs:     "100.0",
				ErrorRate:        "0.01",
			}
			Expect(k8sClient.Status().Update(ctx, createdModelDeployment)).Should(Succeed())

			By("Verifying the ModelMetrics are updated")
			Eventually(func() string {
				err := k8sClient.Get(ctx, modelDeploymentLookupKey, createdModelDeployment)
				if err != nil {
					return ""
				}
				if createdModelDeployment.Status.Metrics == nil {
					return ""
				}
				return createdModelDeployment.Status.Metrics.TokensPerSecond
			}, time.Minute, time.Second).Should(Equal("50.5"))

			Expect(createdModelDeployment.Status.Metrics.AvgModelLoadTime).Should(Equal("2.3"))
			Expect(createdModelDeployment.Status.Metrics.AvgLatencyMs).Should(Equal("100.0"))
			Expect(createdModelDeployment.Status.Metrics.ErrorRate).Should(Equal("0.01"))
		})
	})
})
