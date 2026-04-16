package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

var _ = Describe("ModelCache Quantization Lifecycle", func() {
	const (
		CacheName      = "test-quant-cache"
		CacheNamespace = "default"
	)

	bindSharedPVC := func(ctx context.Context, cacheName string) {
		By("Binding the shared PVC")
		pvcKey := types.NamespacedName{Name: cacheName, Namespace: CacheNamespace}
		Eventually(func() error {
			pvc := &corev1.PersistentVolumeClaim{}
			if err := k8sClient.Get(ctx, pvcKey, pvc); err != nil {
				return err
			}
			if pvc.Status.Phase == corev1.ClaimBound {
				return nil
			}
			pvc.Status.Phase = corev1.ClaimBound
			return k8sClient.Status().Update(ctx, pvc)
		}, time.Minute, time.Second).Should(Succeed())
	}

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

			bindSharedPVC(ctx, CacheName)

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
			Expect(quantJob.Labels).To(HaveKey(LabelFormat))
			Expect(quantJob.Labels[LabelFormat]).To(Equal("GGUF"))
			Expect(quantJob.Labels[LabelCache]).To(Equal(CacheName))

			By("Simulating quantization job success")
			start := metav1.NewTime(time.Now().Add(-2 * time.Minute))
			completion := metav1.NewTime(time.Now())
			quantJob.Status.StartTime = &start
			quantJob.Status.CompletionTime = &completion
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
			Expect(cache.Status.Quantization.QuantizationTime).NotTo(BeEmpty())
		})
	})

	Context("When quantization job fails", func() {
		It("Should mark the ModelCache as Failed", func() {
			ctx := context.Background()
			cacheName := "test-quant-fail"
			noRetries := int32(0)

			By("Creating a ModelCache with quantization")
			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "some-model/failing",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					MaxRetries:      &noRetries,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:   aiv1alpha1.QuantizationFormatGGUF,
						GGUFType: "Q4_K_M",
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())

			bindSharedPVC(ctx, cacheName)

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

		It("Should treat an active retry as Quantizing even when failed history exists", func() {
			ctx := context.Background()
			cacheName := "test-quant-active-retry"

			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "some-model/retrying",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:   aiv1alpha1.QuantizationFormatGGUF,
						GGUFType: "Q4_K_M",
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())
			bindSharedPVC(ctx, cacheName)

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

			quantJobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-quantize", cacheName),
				Namespace: CacheNamespace,
			}
			quantJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, quantJobKey, quantJob)
			}, time.Minute, time.Second).Should(Succeed())

			cacheKey := types.NamespacedName{Name: cacheName, Namespace: CacheNamespace}
			Eventually(func() error {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return err
				}
				cache.Status.Phase = aiv1alpha1.ModelCachePhaseFailed
				cache.Status.Quantization = &aiv1alpha1.QuantizationStatus{
					Format:         "GGUF",
					Type:           "Q4_K_M",
					FailureMessage: "old failure",
				}
				return k8sClient.Status().Update(ctx, cache)
			}, time.Minute, time.Second).Should(Succeed())

			start := metav1.NewTime(time.Now().Add(-45 * time.Second))
			quantJob.Status.StartTime = &start
			quantJob.Status.Failed = 1
			quantJob.Status.Active = 1
			Expect(k8sClient.Status().Update(ctx, quantJob)).To(Succeed())

			Eventually(func(g Gomega) {
				cache := &aiv1alpha1.ModelCache{}
				g.Expect(k8sClient.Get(ctx, cacheKey, cache)).To(Succeed())
				g.Expect(cache.Status.Phase).To(Equal(aiv1alpha1.ModelCachePhaseQuantizing))
				g.Expect(cache.Status.Quantization).NotTo(BeNil())
				g.Expect(cache.Status.Quantization.FailureMessage).To(BeEmpty())
				g.Expect(cache.Status.Quantization.Progress).NotTo(BeNil())
			}, time.Minute, time.Second).Should(Succeed())
		})
	})

	Context("When a ModelCache uses AWQ quantization", func() {
		It("Should create an AWQ quantization job and record AWQ type on success", func() {
			ctx := context.Background()
			cacheName := "test-awq-cache"
			bits := int32(4)
			groupSize := int32(128)
			maxMem := int32(48)

			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "meta-llama/Meta-Llama-3-8B",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:      aiv1alpha1.QuantizationFormatAWQ,
						Bits:        &bits,
						GroupSize:   &groupSize,
						UseGPU:      true,
						MaxMemoryGB: &maxMem,
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())

			bindSharedPVC(ctx, cacheName)

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

			By("Checking AWQ quantization job creation")
			quantJobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-quantize", cacheName),
				Namespace: CacheNamespace,
			}
			quantJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, quantJobKey, quantJob)
			}, time.Minute, time.Second).Should(Succeed())

			Expect(quantJob.Labels[LabelFormat]).To(Equal("AWQ"))
			Expect(quantJob.Labels[LabelCache]).To(Equal(cacheName))

			By("Simulating AWQ quantization job success")
			start := metav1.NewTime(time.Now().Add(-90 * time.Second))
			completion := metav1.NewTime(time.Now())
			quantJob.Status.StartTime = &start
			quantJob.Status.CompletionTime = &completion
			quantJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, quantJob)).To(Succeed())

			By("Verifying AWQ quantization status")
			cacheKey := types.NamespacedName{Name: cacheName, Namespace: CacheNamespace}
			Eventually(func() aiv1alpha1.ModelCachePhase {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return ""
				}
				return cache.Status.Phase
			}, time.Minute, time.Second).Should(Equal(aiv1alpha1.ModelCachePhaseReady))

			cache := &aiv1alpha1.ModelCache{}
			Expect(k8sClient.Get(ctx, cacheKey, cache)).To(Succeed())
			Expect(cache.Status.Quantization).NotTo(BeNil())
			Expect(cache.Status.Quantization.Format).To(Equal("AWQ"))
			Expect(cache.Status.Quantization.Type).To(Equal("W4_G128"))
			Expect(cache.Status.Quantization.QuantizationTime).NotTo(BeEmpty())
		})
	})

	Context("Spec change detection", func() {
		It("Should seed the quant-spec-hash annotation when quantization succeeds", func() {
			ctx := context.Background()
			cacheName := "test-hash-seed"

			maxMem := int32(16)
			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "test-org/hash-seed-model",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:      aiv1alpha1.QuantizationFormatGGUF,
						GGUFType:    "Q4_K_M",
						MaxMemoryGB: &maxMem,
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())

			bindSharedPVC(ctx, cacheName)

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

			By("Waiting for quantization job creation")
			quantJobKey := types.NamespacedName{
				Name:      fmt.Sprintf("%s-quantize", cacheName),
				Namespace: CacheNamespace,
			}
			quantJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, quantJobKey, quantJob)
			}, time.Minute, time.Second).Should(Succeed())

			By("Verifying spec hash annotation is seeded after job creation")
			cacheKey := types.NamespacedName{Name: cacheName, Namespace: CacheNamespace}
			Eventually(func() string {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return ""
				}
				if cache.Annotations == nil {
					return ""
				}
				return cache.Annotations[annotationQuantSpecHash]
			}, time.Minute, time.Second).ShouldNot(BeEmpty())

			By("Verifying the hash matches the current spec")
			cache := &aiv1alpha1.ModelCache{}
			Expect(k8sClient.Get(ctx, cacheKey, cache)).To(Succeed())
			expectedHash := quantSpecHash(cache.Spec.Quantization)
			Expect(cache.Annotations[annotationQuantSpecHash]).To(Equal(expectedHash))
		})

		It("Should trigger re-quantization when spec changes on a Ready cache", func() {
			ctx := context.Background()
			cacheName := "test-spec-change"

			maxMem := int32(16)
			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "test-org/spec-change-model",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:      aiv1alpha1.QuantizationFormatGGUF,
						GGUFType:    "Q4_K_M",
						MaxMemoryGB: &maxMem,
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())

			bindSharedPVC(ctx, cacheName)

			By("Simulating download + quantization success")
			dlJobKey := types.NamespacedName{Name: cacheName + "-downloader", Namespace: CacheNamespace}
			dlJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, dlJobKey, dlJob)
			}, time.Minute, time.Second).Should(Succeed())
			dlJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, dlJob)).To(Succeed())

			quantJobKey := types.NamespacedName{Name: cacheName + "-quantize", Namespace: CacheNamespace}
			quantJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, quantJobKey, quantJob)
			}, time.Minute, time.Second).Should(Succeed())

			start := metav1.NewTime(time.Now().Add(-2 * time.Minute))
			completion := metav1.NewTime(time.Now())
			quantJob.Status.StartTime = &start
			quantJob.Status.CompletionTime = &completion
			quantJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, quantJob)).To(Succeed())

			cacheKey := types.NamespacedName{Name: cacheName, Namespace: CacheNamespace}
			Eventually(func() aiv1alpha1.ModelCachePhase {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return ""
				}
				return cache.Status.Phase
			}, time.Minute, time.Second).Should(Equal(aiv1alpha1.ModelCachePhaseReady))

			By("Changing the quantization spec (GGUFType)")
			cache := &aiv1alpha1.ModelCache{}
			Expect(k8sClient.Get(ctx, cacheKey, cache)).To(Succeed())
			cache.Spec.Quantization.GGUFType = "Q5_K_M"
			Expect(k8sClient.Update(ctx, cache)).To(Succeed())

			By("Verifying the controller clears prior quantization status and starts re-quantizing")
			Eventually(func() aiv1alpha1.ModelCachePhase {
				c := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, c); err != nil {
					return ""
				}
				return c.Status.Phase
			}, time.Minute, time.Second).Should(Equal(aiv1alpha1.ModelCachePhaseQuantizing))

			By("Verifying quantization status is cleared")
			Expect(k8sClient.Get(ctx, cacheKey, cache)).To(Succeed())
			Expect(cache.Status.Quantization).To(BeNil())
		})

		It("Should trigger re-quantization when requantize annotation is set", func() {
			ctx := context.Background()
			cacheName := "test-requant-annotation"

			maxMem := int32(16)
			modelCache := &aiv1alpha1.ModelCache{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cacheName,
					Namespace: CacheNamespace,
				},
				Spec: aiv1alpha1.ModelCacheSpec{
					Source:          "test-org/requant-model",
					StorageStrategy: aiv1alpha1.StorageStrategySharedPVC,
					Quantization: &aiv1alpha1.QuantizationSpec{
						Format:      aiv1alpha1.QuantizationFormatGGUF,
						GGUFType:    "Q4_K_M",
						MaxMemoryGB: &maxMem,
					},
				},
			}
			Expect(k8sClient.Create(ctx, modelCache)).To(Succeed())

			bindSharedPVC(ctx, cacheName)

			By("Simulating download + quantization success")
			dlJobKey := types.NamespacedName{Name: cacheName + "-downloader", Namespace: CacheNamespace}
			dlJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, dlJobKey, dlJob)
			}, time.Minute, time.Second).Should(Succeed())
			dlJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, dlJob)).To(Succeed())

			quantJobKey := types.NamespacedName{Name: cacheName + "-quantize", Namespace: CacheNamespace}
			quantJob := &batchv1.Job{}
			Eventually(func() error {
				return k8sClient.Get(ctx, quantJobKey, quantJob)
			}, time.Minute, time.Second).Should(Succeed())

			start := metav1.NewTime(time.Now().Add(-2 * time.Minute))
			completion := metav1.NewTime(time.Now())
			quantJob.Status.StartTime = &start
			quantJob.Status.CompletionTime = &completion
			quantJob.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, quantJob)).To(Succeed())

			cacheKey := types.NamespacedName{Name: cacheName, Namespace: CacheNamespace}
			Eventually(func() aiv1alpha1.ModelCachePhase {
				cache := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, cache); err != nil {
					return ""
				}
				return cache.Status.Phase
			}, time.Minute, time.Second).Should(Equal(aiv1alpha1.ModelCachePhaseReady))

			By("Setting the requantize annotation")
			cache := &aiv1alpha1.ModelCache{}
			Expect(k8sClient.Get(ctx, cacheKey, cache)).To(Succeed())
			if cache.Annotations == nil {
				cache.Annotations = make(map[string]string)
			}
			cache.Annotations[annotationRequantize] = "true"
			Expect(k8sClient.Update(ctx, cache)).To(Succeed())

			By("Verifying the controller clears prior status, starts re-quantizing, and clears the trigger annotation")
			Eventually(func() string {
				c := &aiv1alpha1.ModelCache{}
				if err := k8sClient.Get(ctx, cacheKey, c); err != nil {
					return "get-error"
				}
				trigger := ""
				if c.Annotations != nil {
					trigger = c.Annotations[annotationRequantize]
				}
				return fmt.Sprintf("%s|%s|%t", c.Status.Phase, trigger, c.Status.Quantization == nil)
			}, time.Minute, time.Second).Should(Equal("Quantizing||true"))
		})
	})
})

var _ = Describe("quantSpecHash", func() {
	It("Should return empty string for nil spec", func() {
		Expect(quantSpecHash(nil)).To(Equal(""))
	})

	It("Should return consistent hash for same spec", func() {
		bits := int32(4)
		spec := &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatGPTQ,
			Bits:   &bits,
		}
		h1 := quantSpecHash(spec)
		h2 := quantSpecHash(spec)
		Expect(h1).To(Equal(h2))
		Expect(h1).To(HaveLen(16)) // 8 bytes = 16 hex chars
	})

	It("Should return different hash for different specs", func() {
		bits4 := int32(4)
		bits8 := int32(8)
		spec4 := &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatGPTQ,
			Bits:   &bits4,
		}
		spec8 := &aiv1alpha1.QuantizationSpec{
			Format: aiv1alpha1.QuantizationFormatGPTQ,
			Bits:   &bits8,
		}
		Expect(quantSpecHash(spec4)).NotTo(Equal(quantSpecHash(spec8)))
	})
})
