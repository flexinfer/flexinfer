package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var _ = Describe("ModelDeployment Controller Integration", func() {
	const (
		ModelName      = "test-model-sim"
		ModelNamespace = "default"
	)

	Context("When creating a ModelDeployment", func() {
		It("Should create a Benchmark Job with Sidecar", func() {
			ctx := context.Background()

			By("Creating a new ModelDeployment")
			modelDeployment := &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ModelName,
					Namespace: ModelNamespace,
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Model:    "llama3:8b",
					Replicas: int32Ptr(1),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
						Limits: corev1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelDeployment)).To(Succeed())

			By("Checking for the Benchmark Job")
			jobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-benchmark", ModelName),
				Namespace: ModelNamespace,
			}
			createdJob := &batchv1.Job{}

			Eventually(func() error {
				return k8sClient.Get(ctx, jobKey, createdJob)
			}, time.Minute, time.Second).Should(Succeed())

			By("Verifying the Sidecar Architecture")
			Expect(createdJob.Spec.Template.Spec.Containers).To(HaveLen(2), "Job should have 2 containers (benchmarker + sidecar)")

			var benchContainer, backendContainer corev1.Container
			for _, c := range createdJob.Spec.Template.Spec.Containers {
				if c.Name == "flexinfer-bench" {
					benchContainer = c
				} else if c.Name == "llm-backend" {
					backendContainer = c
				}
			}

			Expect(benchContainer.Name).To(Equal("flexinfer-bench"))
			Expect(backendContainer.Name).To(Equal("llm-backend"))
			Expect(backendContainer.Image).To(ContainSubstring("ollama"), "Backend should be ollama")

			By("Verifying Benchmarker Configuration")
			var backendURL string
			for _, env := range benchContainer.Env {
				if env.Name == "BACKEND_URL" {
					backendURL = env.Value
				}
			}
			Expect(backendURL).To(Equal("http://localhost:11434"), "Benchmarker should point to localhost sidecar")

		})
	})

	Context("When creating a ModelDeployment with vLLM", func() {
		It("Should create a Job with vLLM backend image and port", func() {
			ctx := context.Background()
			name := "test-model-vllm"

			modelDeployment := &aiv1alpha1.ModelDeployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ModelNamespace,
				},
				Spec: aiv1alpha1.ModelDeploymentSpec{
					Model:    "llama3:8b",
					Backend:  "vllm",
					Replicas: int32Ptr(1),
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
						Limits: corev1.ResourceList{
							"nvidia.com/gpu": resource.MustParse("1"),
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelDeployment)).To(Succeed())

			jobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-benchmark", name),
				Namespace: ModelNamespace,
			}
			createdJob := &batchv1.Job{}

			Eventually(func() error {
				return k8sClient.Get(ctx, jobKey, createdJob)
			}, time.Minute, time.Second).Should(Succeed())

			var backendContainer corev1.Container
			for _, c := range createdJob.Spec.Template.Spec.Containers {
				if c.Name == "llm-backend" {
					backendContainer = c
				}
			}

			Expect(backendContainer.Image).To(ContainSubstring("vllm-openai"), "Backend should use vllm image")
			Expect(backendContainer.Ports[0].ContainerPort).To(Equal(int32(8000)))
			Expect(backendContainer.Args).To(ContainElement("--model"))
			Expect(backendContainer.Args).To(ContainElement("llama3:8b"))
		})
	})
})

func int32Ptr(i int32) *int32 {
	return &i
}
