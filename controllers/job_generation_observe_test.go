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

func ownedJob(name string, ownerGen string, ownerUID types.UID, controlled bool) *batchv1.Job {
	j := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels:    map[string]string{LabelModel: "m1"},
		},
	}
	if ownerGen != "" {
		j.Annotations = map[string]string{AnnotationOwnerGeneration: ownerGen}
	}
	if controlled {
		ctrlRef := true
		j.OwnerReferences = []metav1.OwnerReference{{
			APIVersion: "ai.flexinfer/v1alpha2",
			Kind:       "Model",
			Name:       "m1",
			UID:        ownerUID,
			Controller: &ctrlRef,
		}}
	}
	return j
}

// TestObserveStaleGenerationJobs verifies the gauge counts only Jobs that are
// controller-owned by this parent AND stamped with a strictly-older generation;
// current/newer, unstamped, and foreign-owned Jobs are excluded. When none are
// stale the series is cleared.
func TestObserveStaleGenerationJobs(t *testing.T) {
	s := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(s))

	const ownerUID = types.UID("owner-123")
	metrics.OwnedJobsStaleGeneration.Reset()

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(
		ownedJob("m1-cache-stage", "3", ownerUID, true),           // older -> counts
		ownedJob("m1-cache-check", "4", ownerUID, true),           // older -> counts
		ownedJob("m1-cache-prefetch", "5", ownerUID, true),        // == current -> no
		ownedJob("m1-newer", "6", ownerUID, true),                 // newer -> no
		ownedJob("m1-unstamped", "", ownerUID, true),              // no stamp -> no
		ownedJob("m1-foreign", "1", types.UID("other-uid"), true), // different owner -> no
		ownedJob("m1-orphan", "1", "", false),                     // no controller ref -> no
	).Build()

	observeStaleGenerationJobs(context.Background(), cl, "Model", "ns", "m1", ownerUID, 5, LabelModel)

	got := testutil.ToFloat64(metrics.OwnedJobsStaleGeneration.WithLabelValues("Model", "ns", "m1"))
	assert.Equal(t, float64(2), got, "only the two strictly-older owned+stamped jobs count")

	// A second pass with no stale jobs clears the series (reads back as 0).
	cl2 := fake.NewClientBuilder().WithScheme(s).WithObjects(
		ownedJob("m1-cache-prefetch", "5", ownerUID, true),
	).Build()
	observeStaleGenerationJobs(context.Background(), cl2, "Model", "ns", "m1", ownerUID, 5, LabelModel)
	got2 := testutil.ToFloat64(metrics.OwnedJobsStaleGeneration.WithLabelValues("Model", "ns", "m1"))
	assert.Equal(t, float64(0), got2, "series cleared when no stale jobs remain")
}
