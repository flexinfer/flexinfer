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
	"errors"
	"testing"

	"github.com/flexinfer/flexinfer/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func jobIdempotencyScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(s))
	return s
}

func newTestJob(name string) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
	}
}

func TestResolveControllerInstanceID(t *testing.T) {
	tests := []struct {
		name        string
		envHostname string
		osHostname  func() (string, error)
		want        string
	}{
		{
			name:        "env hostname wins",
			envHostname: "controller-pod-abc",
			osHostname:  func() (string, error) { return "fallback-host", nil },
			want:        "controller-pod-abc",
		},
		{
			name:        "falls back to os hostname when env empty",
			envHostname: "",
			osHostname:  func() (string, error) { return "node-host", nil },
			want:        "node-host",
		},
		{
			name:        "empty when env empty and os hostname errors",
			envHostname: "",
			osHostname:  func() (string, error) { return "", errors.New("boom") },
			want:        "",
		},
		{
			name:        "empty when os hostname getter is nil",
			envHostname: "",
			osHostname:  nil,
			want:        "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveControllerInstanceID(tc.envHostname, tc.osHostname))
		})
	}
}

func TestControllerInstanceID_StableAndCached(t *testing.T) {
	// The process-wide ID is resolved once and must be stable across calls.
	first := ControllerInstanceID()
	second := ControllerInstanceID()
	assert.Equal(t, first, second)
}

func TestCreateJobIdempotent_CreatesWhenAbsent(t *testing.T) {
	s := jobIdempotencyScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()

	job := newTestJob("quant-job")
	created, err := createJobIdempotent(context.Background(), cl, job, "quantize", 0)
	require.NoError(t, err)
	assert.True(t, created, "expected created=true on first create")

	// The job must actually be persisted.
	got := &batchv1.Job{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: "quant-job", Namespace: "default"}, got))

	// Provenance annotation is stamped with this controller's instance ID
	// (omitted only when the instance ID cannot be resolved at all).
	if id := ControllerInstanceID(); id != "" {
		assert.Equal(t, id, got.Annotations[AnnotationControllerInstance])
	}
}

func TestCreateJobIdempotent_SwallowsAlreadyExists(t *testing.T) {
	s := jobIdempotencyScheme(t)
	// Pre-seed a job with the same name to simulate the rolling-update race
	// where another controller generation already created it.
	existing := newTestJob("dup-job")
	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(existing).Build()

	// A distinct job_type keeps this assertion isolated from other tests that
	// touch the shared global counter.
	const jobType = "conflict-test-stage"
	before := testutil.ToFloat64(metrics.ModelCacheJobCreateConflictsTotal.WithLabelValues(jobType))

	created, err := createJobIdempotent(context.Background(), cl, newTestJob("dup-job"), jobType, 0)
	require.NoError(t, err, "AlreadyExists must be treated as success")
	assert.False(t, created, "expected created=false when the job already exists")

	after := testutil.ToFloat64(metrics.ModelCacheJobCreateConflictsTotal.WithLabelValues(jobType))
	assert.Equal(t, before+1, after, "tolerated AlreadyExists conflict must increment the counter")
}

func TestCreateJobIdempotent_StampsAnnotationOnNilMap(t *testing.T) {
	// Regression guard: a job with no annotations map must not panic and must
	// receive the provenance annotation when an instance ID is available.
	if ControllerInstanceID() == "" {
		t.Skip("no resolvable controller instance ID in this environment")
	}
	s := jobIdempotencyScheme(t)
	cl := fake.NewClientBuilder().WithScheme(s).Build()

	job := newTestJob("ann-job")
	job.Annotations = nil
	created, err := createJobIdempotent(context.Background(), cl, job, "image_warmup", 0)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, ControllerInstanceID(), job.Annotations[AnnotationControllerInstance])
}

func TestCreateJobIdempotent_StampsOwnerGeneration(t *testing.T) {
	s := jobIdempotencyScheme(t)

	t.Run("stamps generation when positive", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(s).Build()
		job := newTestJob("gen-job")
		created, err := createJobIdempotent(context.Background(), cl, job, "quantize", 7)
		require.NoError(t, err)
		require.True(t, created)
		assert.Equal(t, "7", job.Annotations[AnnotationOwnerGeneration],
			"owner generation must be stamped when > 0")
	})

	t.Run("omits generation when zero", func(t *testing.T) {
		cl := fake.NewClientBuilder().WithScheme(s).Build()
		job := newTestJob("nogen-job")
		created, err := createJobIdempotent(context.Background(), cl, job, "quantize", 0)
		require.NoError(t, err)
		require.True(t, created)
		_, ok := job.Annotations[AnnotationOwnerGeneration]
		assert.False(t, ok, "owner generation annotation must be omitted when ownerGeneration is 0")
	})
}
