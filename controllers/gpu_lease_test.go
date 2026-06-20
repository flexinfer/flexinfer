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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/pkg/constants"
)

func leaseScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	return scheme
}

func TestGPULeaseActive(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name  string
		lease *activeLease
		want  bool
	}{
		{"nil", nil, false},
		{"empty group", &activeLease{}, false},
		{"no ttl honored", &activeLease{Group: "g"}, true},
		{"future expiry honored", &activeLease{Group: "g", ExpiresAt: now.Add(time.Minute)}, true},
		{"past expiry ignored", &activeLease{Group: "g", ExpiresAt: now.Add(-time.Minute)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.lease.active(now))
		})
	}
}

func TestLeaseFromConfigMapRoundTrip(t *testing.T) {
	acquired := time.Now().Truncate(time.Second).UTC()
	expires := acquired.Add(30 * time.Minute)
	in := activeLease{
		Group:      "5930k-textgen",
		Node:       "k3s-w-09",
		Owner:      "qwen3-1p7b-lora",
		AcquiredAt: acquired,
		ExpiresAt:  expires,
	}
	cm := gpuLeaseConfigMap("flexinfer", in)

	assert.Equal(t, "gpu-lease-5930k-textgen", cm.Name)
	assert.Equal(t, "5930k-textgen", cm.Labels[constants.LabelGPULeaseGroup])

	out := leaseFromConfigMap(cm)
	require.NotNil(t, out)
	assert.Equal(t, in.Group, out.Group)
	assert.Equal(t, in.Node, out.Node)
	assert.Equal(t, in.Owner, out.Owner)
	assert.True(t, in.AcquiredAt.Equal(out.AcquiredAt))
	assert.True(t, in.ExpiresAt.Equal(out.ExpiresAt))
}

func TestLeaseFromConfigMapNotALease(t *testing.T) {
	// A ConfigMap without the group data key is not a lease.
	cm := &corev1.ConfigMap{Data: map[string]string{"unrelated": "x"}}
	assert.Nil(t, leaseFromConfigMap(cm))
	assert.Nil(t, leaseFromConfigMap(nil))
}

func TestAcquireFindReleaseGPULease(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()

	// No lease initially.
	got, err := findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	assert.Nil(t, got)

	// Acquire with a TTL.
	_, err = acquireGPULease(ctx, c, "flexinfer", activeLease{
		Group:      "g1",
		Owner:      "trainer",
		AcquiredAt: now,
		ExpiresAt:  now.Add(10 * time.Minute),
	})
	require.NoError(t, err)

	got, err = findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "trainer", got.Owner)

	// A different group is unaffected.
	other, err := findActiveLease(ctx, c, "flexinfer", "g2", now)
	require.NoError(t, err)
	assert.Nil(t, other)

	// Release removes it.
	require.NoError(t, releaseGPULease(ctx, c, "flexinfer", "g1"))
	got, err = findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	assert.Nil(t, got)

	// Releasing a non-existent lease is a no-op success (crash-safe idempotency).
	require.NoError(t, releaseGPULease(ctx, c, "flexinfer", "g1"))
}

func TestFindActiveLeaseIgnoresExpired(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()

	// Acquire a lease that has already expired (TTL backstop: a dead acquirer's
	// stale lease must not strand serving).
	_, err := acquireGPULease(ctx, c, "flexinfer", activeLease{
		Group:      "g1",
		Owner:      "dead-trainer",
		AcquiredAt: now.Add(-time.Hour),
		ExpiresAt:  now.Add(-time.Minute),
	})
	require.NoError(t, err)

	// The carrier ConfigMap still exists...
	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{
		Namespace: "flexinfer", Name: constants.GPULeaseConfigMapName("g1"),
	}, cm))

	// ...but the election sees no active lease.
	got, err := findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestAcquireGPULeaseRefreshesTTL(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()

	first := now.Add(time.Minute)
	_, err := acquireGPULease(ctx, c, "flexinfer", activeLease{Group: "g1", Owner: "t", AcquiredAt: now, ExpiresAt: first})
	require.NoError(t, err)

	// Re-acquire extends the TTL in place (idempotent renew).
	second := now.Add(20 * time.Minute)
	_, err = acquireGPULease(ctx, c, "flexinfer", activeLease{Group: "g1", Owner: "t", AcquiredAt: now, ExpiresAt: second})
	require.NoError(t, err)

	cm := &corev1.ConfigMap{}
	require.NoError(t, c.Get(ctx, types.NamespacedName{
		Namespace: "flexinfer", Name: constants.GPULeaseConfigMapName("g1"),
	}, cm))
	assert.Equal(t, second.UTC().Format(time.RFC3339), cm.Data[constants.GPULeaseDataExpiresAt])

	// Exactly one lease ConfigMap exists for the group (no duplicates).
	list := &corev1.ConfigMapList{}
	require.NoError(t, c.List(ctx, list))
	count := 0
	for _, item := range list.Items {
		if item.Labels[constants.LabelGPULeaseGroup] == "g1" {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

// sanity: the not-found path of findActiveLease surfaces nil, not an error.
func TestFindActiveLeaseNotFound(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	got, err := findActiveLease(ctx, c, "flexinfer", "missing", time.Now())
	require.NoError(t, err)
	assert.Nil(t, got)
	// Ensure we didn't accidentally treat a real error as not-found.
	assert.False(t, apierrors.IsNotFound(err))
}

func TestLeaseFromCR(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	exp := now.Add(15 * time.Minute)
	cr := &aiv1alpha2.GPULease{
		ObjectMeta: metav1.ObjectMeta{Name: "l1", Namespace: "flexinfer", CreationTimestamp: metav1.Time{Time: now}},
		Spec: aiv1alpha2.GPULeaseSpec{
			Group:     "5930k-textgen",
			Node:      "cblevins-5930k",
			Owner:     "qwen3-1p7b-lora",
			ExpiresAt: &metav1.Time{Time: exp},
		},
	}
	l := leaseFromCR(cr)
	require.NotNil(t, l)
	assert.Equal(t, "5930k-textgen", l.Group)
	assert.Equal(t, "cblevins-5930k", l.Node)
	assert.Equal(t, "qwen3-1p7b-lora", l.Owner)
	assert.True(t, exp.Equal(l.ExpiresAt))
	assert.True(t, now.Equal(l.AcquiredAt), "AcquiredAt falls back to creationTimestamp")

	// A CR without spec.group is not a lease.
	assert.Nil(t, leaseFromCR(&aiv1alpha2.GPULease{}))
	assert.Nil(t, leaseFromCR(nil))
}

func TestAcquireFindReleaseGPULeaseCR(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()

	got, err := findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	assert.Nil(t, got)

	// Acquire a GPULease CR (first-class carrier).
	_, err = acquireGPULeaseCR(ctx, c, "flexinfer", "g1-lease", activeLease{
		Group:     "g1",
		Owner:     "trainer",
		ExpiresAt: now.Add(10 * time.Minute),
	})
	require.NoError(t, err)

	got, err = findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "trainer", got.Owner)

	// Re-acquire updates spec in place (idempotent renew); still one CR.
	_, err = acquireGPULeaseCR(ctx, c, "flexinfer", "g1-lease", activeLease{
		Group:     "g1",
		Owner:     "trainer",
		ExpiresAt: now.Add(30 * time.Minute),
	})
	require.NoError(t, err)
	list := &aiv1alpha2.GPULeaseList{}
	require.NoError(t, c.List(ctx, list))
	assert.Len(t, list.Items, 1)

	// Release deletes it.
	require.NoError(t, releaseGPULeaseCR(ctx, c, "flexinfer", "g1-lease"))
	got, err = findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	assert.Nil(t, got)
	// Releasing a missing CR is a no-op success (crash-safe idempotency).
	require.NoError(t, releaseGPULeaseCR(ctx, c, "flexinfer", "g1-lease"))
}

func TestFindActiveLeaseCRIgnoresExpired(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()

	// An expired GPULease CR must not hold the group (TTL backstop).
	_, err := acquireGPULeaseCR(ctx, c, "flexinfer", "dead-lease", activeLease{
		Group:     "g1",
		Owner:     "dead-trainer",
		ExpiresAt: now.Add(-time.Minute),
	})
	require.NoError(t, err)

	got, err := findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	assert.Nil(t, got, "expired CR is ignored")
}

func TestFindActiveLeaseCRTakesPrecedenceOverConfigMap(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()

	// Legacy ConfigMap carrier present...
	_, err := acquireGPULease(ctx, c, "flexinfer", activeLease{
		Group: "g1", Owner: "legacy-cm", ExpiresAt: now.Add(10 * time.Minute),
	})
	require.NoError(t, err)
	// ...and a first-class CR for the same group.
	_, err = acquireGPULeaseCR(ctx, c, "flexinfer", "g1-lease", activeLease{
		Group: "g1", Owner: "crd-owner", ExpiresAt: now.Add(10 * time.Minute),
	})
	require.NoError(t, err)

	got, err := findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "crd-owner", got.Owner, "CR is the primary carrier")
}

func TestFindActiveLeaseFallsBackToConfigMap(t *testing.T) {
	ctx := context.Background()
	scheme := leaseScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	now := time.Now()

	// No CR, only a legacy ConfigMap (manual kill-test runbook path).
	_, err := acquireGPULease(ctx, c, "flexinfer", activeLease{
		Group: "g1", Owner: "manual", ExpiresAt: now.Add(10 * time.Minute),
	})
	require.NoError(t, err)

	got, err := findActiveLease(ctx, c, "flexinfer", "g1", now)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "manual", got.Owner, "legacy ConfigMap still honored")
}
