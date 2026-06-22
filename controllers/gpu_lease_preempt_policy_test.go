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
	"testing"

	"github.com/stretchr/testify/assert"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// TestLeaseFreesCard covers the slice-5 preempt-policy gate: when a GPU lease
// frees the shared card by parking serving incumbents.
func TestLeaseFreesCard(t *testing.T) {
	// Three serving members at priorities 300 / 350 / 400.
	low := makeSharedModel("low", 300, aiv1alpha2.ModelPhaseReady, nil, nil)
	mid := makeSharedModel("mid", 350, aiv1alpha2.ModelPhaseReady, nil, nil)
	high := makeSharedModel("high", 400, aiv1alpha2.ModelPhaseReady, nil, nil)
	group := []*aiv1alpha2.Model{low, mid, high}

	cases := []struct {
		name  string
		lease *activeLease
		group []*aiv1alpha2.Model
		want  bool
	}{
		{
			name:  "no lease never frees the card",
			lease: nil,
			group: group,
			want:  false,
		},
		{
			name:  "ungated lease (nil priority) parks unconditionally",
			lease: &activeLease{Group: "test-group"},
			group: group,
			want:  true,
		},
		{
			name:  "gated lease outranks every member -> frees card",
			lease: &activeLease{Group: "test-group", Priority: int32Ptr(401)},
			group: group,
			want:  true,
		},
		{
			name:  "gated lease ties the top member -> blocked (not strictly outranked)",
			lease: &activeLease{Group: "test-group", Priority: int32Ptr(400)},
			group: group,
			want:  false,
		},
		{
			name:  "gated lease below the top member -> blocked",
			lease: &activeLease{Group: "test-group", Priority: int32Ptr(360)},
			group: group,
			want:  false,
		},
		{
			name:  "gated lease outranks the only member -> frees card",
			lease: &activeLease{Group: "test-group", Priority: int32Ptr(301)},
			group: []*aiv1alpha2.Model{low},
			want:  true,
		},
		{
			name:  "gated lease with empty group -> frees card (nothing to protect)",
			lease: &activeLease{Group: "test-group", Priority: int32Ptr(100)},
			group: nil,
			want:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, leaseFreesCard(tc.lease, tc.group))
		})
	}
}

// TestLeaseFromCRPreservesPriority confirms the preempt-policy threshold round
// trips from the GPULease CR into the resolved activeLease the election reads.
func TestLeaseFromCRPreservesPriority(t *testing.T) {
	cr := &aiv1alpha2.GPULease{
		Spec: aiv1alpha2.GPULeaseSpec{
			Group:    "5930k-textgen",
			Owner:    "ft-job",
			Priority: int32Ptr(390),
		},
	}
	l := leaseFromCR(cr)
	if assert.NotNil(t, l) {
		assert.NotNil(t, l.Priority)
		assert.EqualValues(t, 390, *l.Priority)
	}

	// A CR without a priority resolves to an ungated (nil) lease.
	ungated := leaseFromCR(&aiv1alpha2.GPULease{Spec: aiv1alpha2.GPULeaseSpec{Group: "g"}})
	if assert.NotNil(t, ungated) {
		assert.Nil(t, ungated.Priority)
	}
}
