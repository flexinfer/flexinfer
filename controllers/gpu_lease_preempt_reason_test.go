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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// TestPreemptedConditionReasonMessage covers the slice-5 observability split:
// a training/quant GPU-lease park surfaces a distinct condition reason so it is
// not read as a serving-vs-serving preemption or an outage.
func TestPreemptedConditionReasonMessage(t *testing.T) {
	withPreemptedBy := func(by string) *aiv1alpha2.Model {
		return &aiv1alpha2.Model{
			Status: aiv1alpha2.ModelStatus{
				SharedGroup: &aiv1alpha2.SharedGroupStatus{PreemptedBy: by},
			},
		}
	}

	t.Run("gpu lease park -> GPULeaseHeld with training message", func(t *testing.T) {
		reason, msg := preemptedConditionReasonMessage(withPreemptedBy("gpu-lease/ft-crd-flexland"))
		assert.Equal(t, aiv1alpha2.ReasonGPULeaseHeld, reason)
		assert.Contains(t, msg, "Card held by training")
		assert.Contains(t, msg, "ft-crd-flexland")
		assert.NotContains(t, strings.ToLower(msg), "higher priority")
	})

	t.Run("serving preemption -> Preempted", func(t *testing.T) {
		reason, msg := preemptedConditionReasonMessage(withPreemptedBy("gemma4-26b"))
		assert.Equal(t, aiv1alpha2.ReasonPreempted, reason)
		assert.Equal(t, "preempted by gemma4-26b", msg)
	})

	t.Run("empty PreemptedBy -> Preempted (generic)", func(t *testing.T) {
		reason, _ := preemptedConditionReasonMessage(withPreemptedBy(""))
		assert.Equal(t, aiv1alpha2.ReasonPreempted, reason)
	})

	t.Run("nil shared group -> Preempted", func(t *testing.T) {
		reason, _ := preemptedConditionReasonMessage(&aiv1alpha2.Model{})
		assert.Equal(t, aiv1alpha2.ReasonPreempted, reason)
	})

	t.Run("a model literally named like the prefix is not misread", func(t *testing.T) {
		// "gpu-lease/" requires the trailing slash; a serving model whose name
		// merely starts with "gpu-lease" must still read as a preemption.
		reason, _ := preemptedConditionReasonMessage(withPreemptedBy("gpu-leaseish-model"))
		assert.Equal(t, aiv1alpha2.ReasonPreempted, reason)
	})
}
