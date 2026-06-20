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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func finetuneLeaseReconciler(t *testing.T, objs ...runtime.Object) *ModelCacheReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	return &ModelCacheReconciler{
		Client:   c,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(20),
	}
}

func leasingModelCache(group string) *aiv1alpha1.ModelCache {
	return &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-1p7b-lora",
			Namespace: "flexinfer-system",
			UID:       "uid-1",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			NodeSelector: map[string]string{"kubernetes.io/hostname": "cblevins-5930k"},
			Finetune: &aiv1alpha1.FinetuneSpec{
				Dataset: aiv1alpha1.FinetuneDatasetSpec{},
				GPULease: &aiv1alpha1.FinetuneGPULeaseSpec{
					Group: group,
				},
			},
		},
	}
}

func TestEnsureFinetuneGPULeaseNoOpWhenUnset(t *testing.T) {
	ctx := context.Background()
	mc := leasingModelCache("5930k-textgen")
	mc.Spec.Finetune.GPULease = nil
	r := finetuneLeaseReconciler(t, mc)

	require.NoError(t, r.ensureFinetuneGPULease(ctx, mc))

	list := &aiv1alpha2.GPULeaseList{}
	require.NoError(t, r.List(ctx, list))
	assert.Empty(t, list.Items, "no lease created when finetune.gpuLease is unset")
}

func TestEnsureFinetuneGPULeaseCreatesCR(t *testing.T) {
	ctx := context.Background()
	mc := leasingModelCache("5930k-textgen")
	r := finetuneLeaseReconciler(t, mc)
	now := time.Now()

	require.NoError(t, r.ensureFinetuneGPULease(ctx, mc))

	cr := &aiv1alpha2.GPULease{}
	require.NoError(t, r.Get(ctx, types.NamespacedName{
		Namespace: "flexinfer-system", Name: "qwen3-1p7b-lora-gpu-lease",
	}, cr))
	assert.Equal(t, "5930k-textgen", cr.Spec.Group)
	assert.Equal(t, "cblevins-5930k", cr.Spec.Node)
	assert.Equal(t, "qwen3-1p7b-lora", cr.Spec.Owner)
	require.NotNil(t, cr.Spec.ExpiresAt)
	assert.True(t, cr.Spec.ExpiresAt.After(now), "lease has a future TTL")

	// Owner-referenced to the ModelCache (GC backstop).
	require.Len(t, cr.OwnerReferences, 1)
	assert.Equal(t, "qwen3-1p7b-lora", cr.OwnerReferences[0].Name)
	assert.Equal(t, "ModelCache", cr.OwnerReferences[0].Kind)

	// The election sees the group as held.
	got, err := findActiveLease(ctx, r.Client, "flexinfer-system", "5930k-textgen", now)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "qwen3-1p7b-lora", got.Owner)
}

func TestEnsureFinetuneGPULeaseRefreshesInPlace(t *testing.T) {
	ctx := context.Background()
	mc := leasingModelCache("5930k-textgen")
	r := finetuneLeaseReconciler(t, mc)

	require.NoError(t, r.ensureFinetuneGPULease(ctx, mc))
	first := &aiv1alpha2.GPULease{}
	require.NoError(t, r.Get(ctx, types.NamespacedName{Namespace: "flexinfer-system", Name: "qwen3-1p7b-lora-gpu-lease"}, first))

	// Re-acquire: still exactly one CR (refresh, not duplicate).
	require.NoError(t, r.ensureFinetuneGPULease(ctx, mc))
	list := &aiv1alpha2.GPULeaseList{}
	require.NoError(t, r.List(ctx, list))
	assert.Len(t, list.Items, 1)
}

func TestReleaseFinetuneGPULease(t *testing.T) {
	ctx := context.Background()
	mc := leasingModelCache("5930k-textgen")
	r := finetuneLeaseReconciler(t, mc)

	require.NoError(t, r.ensureFinetuneGPULease(ctx, mc))
	require.NoError(t, r.releaseFinetuneGPULease(ctx, mc))

	list := &aiv1alpha2.GPULeaseList{}
	require.NoError(t, r.List(ctx, list))
	assert.Empty(t, list.Items, "lease deleted on release")

	// Releasing again is a quiet no-op (idempotent / crash-safe).
	require.NoError(t, r.releaseFinetuneGPULease(ctx, mc))
}

func TestReleaseFinetuneGPULeaseNoOpWhenUnset(t *testing.T) {
	ctx := context.Background()
	mc := leasingModelCache("5930k-textgen")
	mc.Spec.Finetune.GPULease = nil
	r := finetuneLeaseReconciler(t, mc)
	require.NoError(t, r.releaseFinetuneGPULease(ctx, mc))
}

func TestFinetuneGPULeaseTTL(t *testing.T) {
	// Explicit TTL is honored.
	mc := leasingModelCache("g")
	ttlSec := int64(1800)
	mc.Spec.Finetune.GPULease.TTLSeconds = &ttlSec
	assert.Equal(t, 30*time.Minute, finetuneGPULeaseTTL(mc))

	// Default = finetune deadline + margin.
	mc2 := leasingModelCache("g")
	deadline := time.Duration(effectiveFinetuneDeadline(mc2.Spec.Finetune)) * time.Second
	assert.Equal(t, deadline+finetuneGPULeaseMargin, finetuneGPULeaseTTL(mc2))

	// Sub-minimum explicit TTL falls back to default (guard).
	mc3 := leasingModelCache("g")
	tiny := int64(5)
	mc3.Spec.Finetune.GPULease.TTLSeconds = &tiny
	assert.Equal(t, deadline+finetuneGPULeaseMargin, finetuneGPULeaseTTL(mc3))
}
