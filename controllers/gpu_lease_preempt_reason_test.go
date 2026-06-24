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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	t.Run("static park -> ParkedBehindPrimary with not-promotable message", func(t *testing.T) {
		reason, msg := preemptedConditionReasonMessage(withPreemptedBy("primary/gemma4-26b-a4b-gptq"))
		assert.Equal(t, aiv1alpha2.ReasonParkedBehindPrimary, reason)
		assert.Contains(t, msg, "gemma4-26b-a4b-gptq")
		assert.Contains(t, strings.ToLower(msg), "not promotable")
	})

	t.Run("a model literally named like the primary prefix is not misread", func(t *testing.T) {
		reason, _ := preemptedConditionReasonMessage(withPreemptedBy("primaryish-model"))
		assert.Equal(t, aiv1alpha2.ReasonPreempted, reason)
	})
}

// TestStaticallyParkedBehindPrimary verifies the conservative gate that the
// proxy fast-fail depends on: only a lower-priority member behind a never-idling
// warm leader is flagged, so a promotable member is never starved of cold-start.
func TestStaticallyParkedBehindPrimary(t *testing.T) {
	mk := func(name string, prio int32, minReplicas int32, warmPrimary bool) *aiv1alpha2.Model {
		p, mr := prio, minReplicas
		spec := aiv1alpha2.ModelSpec{
			GPU:        &aiv1alpha2.GPUSpec{Priority: &p},
			Serverless: &aiv1alpha2.ServerlessSpec{MinReplicas: &mr},
		}
		if warmPrimary {
			spec.Config = &apiextensionsv1.JSON{Raw: []byte(`{"warmPolicy":"primary"}`)}
		}
		return &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: spec}
	}

	t.Run("lower priority behind warm-pinned leader -> parked", func(t *testing.T) {
		member := mk("whisper", 340, 0, false)
		leader := mk("gemma4", 350, 1, false) // minReplicas:1 = warm-pinned
		assert.True(t, staticallyParkedBehindPrimary(member, leader))
	})

	t.Run("lower priority behind warm-primary leader -> parked", func(t *testing.T) {
		member := mk("whisper", 340, 0, false)
		leader := mk("gemma4", 350, 0, true) // warmPolicy:primary
		assert.True(t, staticallyParkedBehindPrimary(member, leader))
	})

	t.Run("equal priority -> NOT parked (demand can swap)", func(t *testing.T) {
		member := mk("whisper", 350, 0, false)
		leader := mk("gemma4", 350, 1, true)
		assert.False(t, staticallyParkedBehindPrimary(member, leader))
	})

	t.Run("higher priority -> NOT parked", func(t *testing.T) {
		member := mk("whisper", 360, 0, false)
		leader := mk("gemma4", 350, 1, true)
		assert.False(t, staticallyParkedBehindPrimary(member, leader))
	})

	t.Run("leader not warm (scale-to-zero, no primary) -> NOT parked (leader idles out)", func(t *testing.T) {
		member := mk("whisper", 340, 0, false)
		leader := mk("gemma4", 350, 0, false)
		assert.False(t, staticallyParkedBehindPrimary(member, leader))
	})

	t.Run("same model / nils -> NOT parked", func(t *testing.T) {
		leader := mk("gemma4", 350, 1, true)
		assert.False(t, staticallyParkedBehindPrimary(leader, leader))
		assert.False(t, staticallyParkedBehindPrimary(nil, leader))
		assert.False(t, staticallyParkedBehindPrimary(leader, nil))
	})
}
