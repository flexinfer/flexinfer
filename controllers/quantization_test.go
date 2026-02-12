package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var _ = Describe("ModelCache Quantization Lifecycle", func() {
	const (
		CacheName      = "test-quant-cache"
		CacheNamespace = "default"
	)

	Context("When a ModelCache has quantization spec", func() {
		It("Should create a quantization job after download succeeds", func() {
			ctx := context.Background()

			By("Creating a ModelCache with quantization")
			maxMem := int32(16)
			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      CacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "meta-llama/Meta-Llama-3-8B",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:      aiv1alpha1.QuantizationFormatGGUF,
						GGUFType:    "Q4_K_M",
						MaxMemoryGB: &maxMem,
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())

			By("Checking that a downloader job is created")
			dlJobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-downloader", CacheName),
				Namespace: CacheNamespace,
			}
			dlJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, dlJobKey, dlJob)
			}, time.Minute, time.Second).Should(Succeed())

			By("Simulating download job success")
			dlJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, dlJob)).To(Succeed())

			By("Checking for quantization job creation")
			quantJobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-quantize", CacheName),
				Namespace: CacheNamespace,
			}
			quantJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, quantJobKey, quantJob)
			}, time.Minute, time.Second).Should(Succeed())

			By("Verifying the ModelCache transitions to Quantizing phase")
			cacheKey := types.NamespacedName{Name: CacheName, Namespace: CacheNamespace}
			Eventually(func() aiv1alpha1.ModelCachePhase {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return ""
				}
				return cache.Status.Phase
			}, time.Minute, time.Second).Should(Equal(aiv1alpha1.ModelCachePhaseQuantizing))

			By("Verifying quantization job labels")
			Expect(quantJob.Labels).To(HaveKey("flexinfer.ai/format"))
			Expect(quantJob.Labels["flexinfer.ai/format"]).To(Equal("GGUF"))
			Expect(quantJob.Labels["flexinfer.ai/cache"]).To(Equal(CacheName))

			By("Simulating quantization job success")
			quantJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, quantJob)).To(Succeed())

			By("Verifying the ModelCache transitions to Ready")
			Eventually(func() aiv1alpha1.ModelCachePhase {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return ""
				}
				return cache.Status.Phase
			}, time.Minute, time.Second).Should(Equal(aiv1alpha1.ModelCachePhaseReady))

			By("Verifying quantization status is populated")
			cache := &aiv1alpha1.ModelCache{}
			Expect(k8sClient.Get(ctx, cacheKey, cache)).To(Succeed())
			Expect(cache.Status.Quantization).NotTo(BeNil())
			Expect(cache.Status.Quantization.Format).To(Equal("GGUF"))
			Expect(cache.Status.Quantization.Type).To(Equal("Q4_K_M"))
		})
	})

	Context("When quantization job fails", func() {
		It("Should mark the ModelCache as Failed", func() {
			ctx := context.Background()
			cacheName := "test-quant-fail"

			By("Creating a ModelCache with quantization")
			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "some-model/failing",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:   aiv1alpha1.QuantizationFormatGGUF,
						GGUFType: "Q4_K_M",
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())

			By("Simulating download job success")
			dlJobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-downloader", cacheName),
				Namespace: CacheNamespace,
			}
			dlJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, dlJobKey, dlJob)
			}, time.Minute, time.Second).Should(Succeed())
			dlJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, dlJob)).To(Succeed())

			By("Simulating quantization job failure")
			quantJobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-quantize", cacheName),
				Namespace: CacheNamespace,
			}
			quantJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, quantJobKey, quantJob)
			}, time.Minute, time.Second).Should(Succeed())
			quantJob.Status.Failed = 1
			Expect(k8sClient.Status().Update(ctx, quantJob)).To(Succeed())

			By("Verifying the ModelCache transitions to Failed")
			cacheKey := types.NamespacedName{Name: cacheName, Namespace: CacheNamespace}
			Eventually(func() aiv1alpha1.ModelCachePhase {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return ""
				}
				return cache.Status.Phase
			}, time.Minute, time.Second).Should(Equal(aiv1alpha1.ModelCachePhaseFailed))
		})
	})
})
