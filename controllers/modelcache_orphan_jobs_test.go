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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func orphanCache(gen int64) *aiv1alpha1.ModelCache {
	return &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "orphan-mc",
			Namespace:  "flexinfer-system",
			UID:        "mc-uid-1",
			Generation: gen,
		},
	}
}

// orphanJob builds a pipeline Job owned by mc. stamped controls whether the
// owner-generation annotation is present; ownerGen is its value; active sets
// Status.Active; owned controls the controller-ref UID match.
func orphanJob(mc *aiv1alpha1.ModelCache, suffix string, stamped bool, ownerGen int64, active int32, owned bool) *batchv1.Job {
	isCtrl := true
	uid := mc.UID
	if !owned {
		uid = "someone-else"
	}
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mc.Name + suffix,
			Namespace: mc.Namespace,
			Labels:    map[string]string{LabelCache: mc.Name},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "ai.flexinfer/v1alpha1",
				Kind:       "ModelCache",
				Name:       mc.Name,
				UID:        uid,
				Controller: &isCtrl,
			}},
		},
		Status: batchv1.JobStatus{Active: active},
	}
	if stamped {
		j.Annotations = map[string]string{AnnotationOwnerGeneration: strconv.FormatInt(ownerGen, 10)}
	}
	return j
}

func jobExists(t *testing.T, r *ModelCacheReconciler, ns, name string) bool {
	t.Helper()
	err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &batchv1.Job{})
	if err == nil {
		return true
	}
	require.True(t, apierrors.IsNotFound(err), "unexpected get error: %v", err)
	return false
}

func TestReapOrphanedStageJobs(t *testing.T) {
	t.Run("reaps a non-running, stale, owned orphan whose stage left the spec", func(t *testing.T) {
		mc := orphanCache(3) // Quantization nil -> -quantize is orphaned
		job := orphanJob(mc, "-quantize", true, 2, 0, true)
		r := finetuneSpecChangeReconciler(t, mc, job)

		r.reapOrphanedStageJobs(context.Background(), mc)
		assert.False(t, jobExists(t, r, mc.Namespace, mc.Name+"-quantize"), "orphan should be reaped")
	})

	t.Run("keeps a job whose stage is still in the spec", func(t *testing.T) {
		mc := orphanCache(3)
		mc.Spec.Finetune = &aiv1alpha1.FinetuneSpec{Dataset: aiv1alpha1.FinetuneDatasetSpec{}}
		job := orphanJob(mc, "-finetune", true, 2, 0, true)
		r := finetuneSpecChangeReconciler(t, mc, job)

		r.reapOrphanedStageJobs(context.Background(), mc)
		assert.True(t, jobExists(t, r, mc.Namespace, mc.Name+"-finetune"), "in-spec stage job must be kept")
	})

	t.Run("never reaps a running orphan", func(t *testing.T) {
		mc := orphanCache(3)
		job := orphanJob(mc, "-quantize", true, 2, 1 /* active */, true)
		r := finetuneSpecChangeReconciler(t, mc, job)

		r.reapOrphanedStageJobs(context.Background(), mc)
		assert.True(t, jobExists(t, r, mc.Namespace, mc.Name+"-quantize"), "running orphan must be left alone")
	})

	t.Run("keeps a current-generation orphan (not yet proven superseded)", func(t *testing.T) {
		mc := orphanCache(3)
		job := orphanJob(mc, "-quantize", true, 3 /* == current */, 0, true)
		r := finetuneSpecChangeReconciler(t, mc, job)

		r.reapOrphanedStageJobs(context.Background(), mc)
		assert.True(t, jobExists(t, r, mc.Namespace, mc.Name+"-quantize"), "non-stale job must be kept")
	})

	t.Run("keeps an unstamped orphan (unknown provenance)", func(t *testing.T) {
		mc := orphanCache(3)
		job := orphanJob(mc, "-quantize", false, 0, 0, true)
		r := finetuneSpecChangeReconciler(t, mc, job)

		r.reapOrphanedStageJobs(context.Background(), mc)
		assert.True(t, jobExists(t, r, mc.Namespace, mc.Name+"-quantize"), "unstamped job must be kept")
	})

	t.Run("keeps a job owned by a different controller ref", func(t *testing.T) {
		mc := orphanCache(3)
		job := orphanJob(mc, "-quantize", true, 2, 0, false /* not owned */)
		r := finetuneSpecChangeReconciler(t, mc, job)

		r.reapOrphanedStageJobs(context.Background(), mc)
		assert.True(t, jobExists(t, r, mc.Namespace, mc.Name+"-quantize"), "non-owned job must be kept")
	})

	t.Run("reaps the warmup sibling of an orphaned stage too", func(t *testing.T) {
		mc := orphanCache(3) // Quantization nil
		job := orphanJob(mc, "-quantize-image-warmup", true, 2, 0, true)
		r := finetuneSpecChangeReconciler(t, mc, job)

		r.reapOrphanedStageJobs(context.Background(), mc)
		assert.False(t, jobExists(t, r, mc.Namespace, mc.Name+"-quantize-image-warmup"), "warmup orphan should be reaped")
	})
}

func TestOrphanedStageSuffixes(t *testing.T) {
	// All stages absent -> all removable suffixes orphaned; -downloader never is.
	all := orphanedStageSuffixes(orphanCache(1))
	assert.Contains(t, all, "-quantize")
	assert.Contains(t, all, "-abliterate")
	assert.Contains(t, all, "-finetune")
	assert.Contains(t, all, "-publish")
	assert.NotContains(t, all, "-downloader")

	// A present stage drops out of the orphan set.
	mc := orphanCache(1)
	mc.Spec.Quantization = &aiv1alpha1.QuantizationSpec{}
	got := orphanedStageSuffixes(mc)
	assert.NotContains(t, got, "-quantize")
	assert.NotContains(t, got, "-quantize-image-warmup")
	assert.Contains(t, got, "-finetune")
}
