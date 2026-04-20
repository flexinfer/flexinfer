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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func TestApplyManagedAnnotations(t *testing.T) {
	managed := []string{"key1", "key2"}

	t.Run("empty existing and empty desired", func(t *testing.T) {
		got := applyManagedAnnotations(nil, nil, managed)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("non-managed preserved and managed set", func(t *testing.T) {
		existing := map[string]string{"other": "keep"}
		desired := map[string]string{"key1": "val1", "key2": "val2"}
		got := applyManagedAnnotations(existing, desired, managed)

		if got == nil {
			t.Fatalf("expected non-nil map, got nil")
		}
		if got["other"] != "keep" {
			t.Errorf("non-managed key 'other' = %q, want %q", got["other"], "keep")
		}
		if got["key1"] != "val1" {
			t.Errorf("managed key 'key1' = %q, want %q", got["key1"], "val1")
		}
		if got["key2"] != "val2" {
			t.Errorf("managed key 'key2' = %q, want %q", got["key2"], "val2")
		}
	})

	t.Run("managed key not in desired is removed", func(t *testing.T) {
		existing := map[string]string{"key1": "old", "other": "keep"}
		desired := map[string]string{} // key1 not present in desired
		got := applyManagedAnnotations(existing, desired, managed)

		if got == nil {
			t.Fatalf("expected non-nil map, got nil")
		}
		if _, ok := got["key1"]; ok {
			t.Errorf("managed key 'key1' should be removed, but found %q", got["key1"])
		}
		if got["other"] != "keep" {
			t.Errorf("non-managed key 'other' = %q, want %q", got["other"], "keep")
		}
	})

	t.Run("desired managed key with empty string is removed", func(t *testing.T) {
		existing := map[string]string{"key1": "old", "other": "keep"}
		desired := map[string]string{"key1": ""}
		got := applyManagedAnnotations(existing, desired, managed)

		if got == nil {
			t.Fatalf("expected non-nil map, got nil")
		}
		if _, ok := got["key1"]; ok {
			t.Errorf("managed key 'key1' with empty desired should be removed, but found %q", got["key1"])
		}
		if got["other"] != "keep" {
			t.Errorf("non-managed key 'other' = %q, want %q", got["other"], "keep")
		}
	})

	t.Run("nil desired removes managed keys from existing", func(t *testing.T) {
		existing := map[string]string{"key1": "old", "key2": "old2", "other": "keep"}
		got := applyManagedAnnotations(existing, nil, managed)

		if got == nil {
			t.Fatalf("expected non-nil map, got nil")
		}
		if _, ok := got["key1"]; ok {
			t.Errorf("managed key 'key1' should be removed when desired is nil")
		}
		if _, ok := got["key2"]; ok {
			t.Errorf("managed key 'key2' should be removed when desired is nil")
		}
		if got["other"] != "keep" {
			t.Errorf("non-managed key 'other' = %q, want %q", got["other"], "keep")
		}
	})
}

func TestMergeStringMap(t *testing.T) {
	t.Run("both nil", func(t *testing.T) {
		got := mergeStringMap(nil, nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("existing non-nil and additional nil", func(t *testing.T) {
		existing := map[string]string{"a": "1", "b": "2"}
		got := mergeStringMap(existing, nil)

		if got == nil {
			t.Fatalf("expected non-nil map, got nil")
		}
		if len(got) != 2 {
			t.Errorf("expected 2 entries, got %d", len(got))
		}
		if got["a"] != "1" || got["b"] != "2" {
			t.Errorf("got %v, want map[a:1 b:2]", got)
		}
		// Verify it is a copy, not the same map.
		got["a"] = "changed"
		if existing["a"] == "changed" {
			t.Errorf("mergeStringMap should return a copy, not mutate existing")
		}
	})

	t.Run("overlapping keys additional wins", func(t *testing.T) {
		existing := map[string]string{"a": "1", "b": "2"}
		additional := map[string]string{"b": "override", "c": "3"}
		got := mergeStringMap(existing, additional)

		if got == nil {
			t.Fatalf("expected non-nil map, got nil")
		}
		if got["a"] != "1" {
			t.Errorf("key 'a' = %q, want %q", got["a"], "1")
		}
		if got["b"] != "override" {
			t.Errorf("key 'b' = %q, want %q (additional should win)", got["b"], "override")
		}
		if got["c"] != "3" {
			t.Errorf("key 'c' = %q, want %q", got["c"], "3")
		}
	})

	t.Run("both empty maps", func(t *testing.T) {
		got := mergeStringMap(map[string]string{}, map[string]string{})
		if got != nil {
			t.Errorf("expected nil for both empty, got %v", got)
		}
	})
}

func TestEnvStringOrDefault(t *testing.T) {
	const envKey = "TEST_ENV_STRING_OR_DEFAULT"

	t.Run("env var set to custom", func(t *testing.T) {
		t.Setenv(envKey, "custom")
		got := envStringOrDefault(envKey, "fallback")
		if got != "custom" {
			t.Errorf("envStringOrDefault() = %q, want %q", got, "custom")
		}
	})

	t.Run("env var set to spaces only returns fallback", func(t *testing.T) {
		t.Setenv(envKey, "   ")
		got := envStringOrDefault(envKey, "fallback")
		if got != "fallback" {
			t.Errorf("envStringOrDefault() = %q, want %q", got, "fallback")
		}
	})

	t.Run("env var not set returns fallback", func(t *testing.T) {
		// t.Setenv not called — env var is not set.
		got := envStringOrDefault(envKey, "fallback")
		if got != "fallback" {
			t.Errorf("envStringOrDefault() = %q, want %q", got, "fallback")
		}
	})
}

func TestEnvIntOrDefault(t *testing.T) {
	const envKey = "TEST_ENV_INT_OR_DEFAULT"

	t.Run("valid int", func(t *testing.T) {
		t.Setenv(envKey, "42")
		got := envIntOrDefault(envKey, 10)
		if got != 42 {
			t.Errorf("envIntOrDefault() = %d, want 42", got)
		}
	})

	t.Run("zero returns fallback", func(t *testing.T) {
		t.Setenv(envKey, "0")
		got := envIntOrDefault(envKey, 10)
		if got != 10 {
			t.Errorf("envIntOrDefault() = %d, want 10 (0 is <= 0)", got)
		}
	})

	t.Run("negative returns fallback", func(t *testing.T) {
		t.Setenv(envKey, "-1")
		got := envIntOrDefault(envKey, 10)
		if got != 10 {
			t.Errorf("envIntOrDefault() = %d, want 10 (-1 is <= 0)", got)
		}
	})

	t.Run("non-numeric returns fallback", func(t *testing.T) {
		t.Setenv(envKey, "abc")
		got := envIntOrDefault(envKey, 10)
		if got != 10 {
			t.Errorf("envIntOrDefault() = %d, want 10 (parse error)", got)
		}
	})

	t.Run("not set returns fallback", func(t *testing.T) {
		got := envIntOrDefault(envKey, 10)
		if got != 10 {
			t.Errorf("envIntOrDefault() = %d, want 10 (not set)", got)
		}
	})
}

func TestEnvBoolOrDefault(t *testing.T) {
	const envKey = "TEST_ENV_BOOL_OR_DEFAULT"

	truthy := []string{"1", "true", "yes", "on"}
	for _, v := range truthy {
		t.Run("truthy_"+v, func(t *testing.T) {
			t.Setenv(envKey, v)
			got := envBoolOrDefault(envKey, false)
			if !got {
				t.Errorf("envBoolOrDefault(%q) = false, want true", v)
			}
		})
	}

	falsy := []string{"0", "false", "no", "off"}
	for _, v := range falsy {
		t.Run("falsy_"+v, func(t *testing.T) {
			t.Setenv(envKey, v)
			got := envBoolOrDefault(envKey, true)
			if got {
				t.Errorf("envBoolOrDefault(%q) = true, want false", v)
			}
		})
	}

	t.Run("garbage returns fallback", func(t *testing.T) {
		t.Setenv(envKey, "garbage")
		got := envBoolOrDefault(envKey, true)
		if !got {
			t.Errorf("envBoolOrDefault('garbage') = false, want true (fallback)")
		}
		got = envBoolOrDefault(envKey, false)
		if got {
			t.Errorf("envBoolOrDefault('garbage') = true, want false (fallback)")
		}
	})

	t.Run("empty string returns fallback", func(t *testing.T) {
		t.Setenv(envKey, "")
		got := envBoolOrDefault(envKey, true)
		if !got {
			t.Errorf("envBoolOrDefault('') = false, want true (fallback)")
		}
	})

	t.Run("not set returns fallback", func(t *testing.T) {
		got := envBoolOrDefault(envKey, true)
		if !got {
			t.Errorf("envBoolOrDefault(not set) = false, want true (fallback)")
		}
	})
}

func TestParseOptionalQuantity(t *testing.T) {
	t.Run("valid quantity", func(t *testing.T) {
		q, ok := parseOptionalQuantity("8Gi")
		if !ok {
			t.Fatalf("parseOptionalQuantity('8Gi') ok = false, want true")
		}
		if q == nil {
			t.Fatalf("parseOptionalQuantity('8Gi') returned nil quantity")
		}
		expected := resource.MustParse("8Gi")
		if !q.Equal(expected) {
			t.Errorf("parseOptionalQuantity('8Gi') = %s, want %s", q.String(), expected.String())
		}
	})

	t.Run("empty string", func(t *testing.T) {
		q, ok := parseOptionalQuantity("")
		if ok {
			t.Errorf("parseOptionalQuantity('') ok = true, want false")
		}
		if q != nil {
			t.Errorf("parseOptionalQuantity('') = %v, want nil", q)
		}
	})

	t.Run("whitespace only", func(t *testing.T) {
		q, ok := parseOptionalQuantity("  ")
		if ok {
			t.Errorf("parseOptionalQuantity('  ') ok = true, want false")
		}
		if q != nil {
			t.Errorf("parseOptionalQuantity('  ') = %v, want nil", q)
		}
	})

	t.Run("invalid quantity", func(t *testing.T) {
		q, ok := parseOptionalQuantity("notAQuantity")
		if ok {
			t.Errorf("parseOptionalQuantity('notAQuantity') ok = true, want false")
		}
		if q != nil {
			t.Errorf("parseOptionalQuantity('notAQuantity') = %v, want nil", q)
		}
	})
}

func TestModelUsesPersistentVolume(t *testing.T) {
	t.Run("pvc source", func(t *testing.T) {
		model := &aiv1alpha2.Model{
			Spec: aiv1alpha2.ModelSpec{
				Source: "pvc://my-pvc/subpath",
			},
		}
		if !modelUsesPersistentVolume(model) {
			t.Errorf("modelUsesPersistentVolume(pvc://) = false, want true")
		}
	})

	t.Run("HF source with SharedPVC cache", func(t *testing.T) {
		model := &aiv1alpha2.Model{
			Spec: aiv1alpha2.ModelSpec{
				Source: "HF://org/model",
				Cache:  &aiv1alpha2.CacheSpec{Strategy: "SharedPVC"},
			},
		}
		if !modelUsesPersistentVolume(model) {
			t.Errorf("modelUsesPersistentVolume(SharedPVC) = false, want true")
		}
	})

	t.Run("HF source with Local cache", func(t *testing.T) {
		model := &aiv1alpha2.Model{
			Spec: aiv1alpha2.ModelSpec{
				Source: "HF://org/model",
				Cache:  &aiv1alpha2.CacheSpec{Strategy: "Local"},
			},
		}
		if !modelUsesPersistentVolume(model) {
			t.Errorf("modelUsesPersistentVolume(Local) = false, want true")
		}
	})

	t.Run("HF source default strategy is SharedPVC", func(t *testing.T) {
		// No cache spec, not shared => cacheStrategy returns "SharedPVC"
		model := &aiv1alpha2.Model{
			Spec: aiv1alpha2.ModelSpec{
				Source: "HF://org/model",
			},
		}
		if !modelUsesPersistentVolume(model) {
			t.Errorf("modelUsesPersistentVolume(default SharedPVC) = false, want true")
		}
	})

	t.Run("HF source with Memory cache", func(t *testing.T) {
		model := &aiv1alpha2.Model{
			Spec: aiv1alpha2.ModelSpec{
				Source: "HF://org/model",
				Cache:  &aiv1alpha2.CacheSpec{Strategy: "Memory"},
			},
		}
		if modelUsesPersistentVolume(model) {
			t.Errorf("modelUsesPersistentVolume(Memory) = true, want false")
		}
	})
}

func TestCheckAliasConflicts(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = aiv1alpha2.AddToScheme(scheme)

	findCondition := func(model *aiv1alpha2.Model, condType string) *metav1.Condition {
		for i := range model.Status.Conditions {
			if model.Status.Conditions[i].Type == condType {
				return &model.Status.Conditions[i]
			}
		}
		return nil
	}

	t.Run("no litellm config sets ConfigValid true", func(t *testing.T) {
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
			},
		}

		cb := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model).WithStatusSubresource(&aiv1alpha2.Model{})
		r := &ModelReconciler{
			Client:   cb.Build(),
			Recorder: &FakeEventRecorder{},
		}

		r.checkAliasConflicts(context.Background(), model)

		cond := findCondition(model, aiv1alpha2.ConditionConfigValid)
		if cond == nil {
			t.Fatalf("expected ConfigValid condition to be set")
		}
		if cond.Status != metav1.ConditionTrue {
			t.Errorf("ConfigValid status = %s, want True", cond.Status)
		}
		if cond.Reason != aiv1alpha2.ReasonConfigValid {
			t.Errorf("ConfigValid reason = %q, want %q", cond.Reason, aiv1alpha2.ReasonConfigValid)
		}
	})

	t.Run("no conflicts sets ConfigValid true", func(t *testing.T) {
		other := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "other-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/other",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					ServedModelName: "other-served",
					Aliases:         []string{"other-alias"},
				},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					ServedModelName: "my-served",
					Aliases:         []string{"my-alias"},
				},
			},
		}

		cb := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, other).WithStatusSubresource(&aiv1alpha2.Model{})
		r := &ModelReconciler{
			Client:   cb.Build(),
			Recorder: &FakeEventRecorder{},
		}

		r.checkAliasConflicts(context.Background(), model)

		cond := findCondition(model, aiv1alpha2.ConditionConfigValid)
		if cond == nil {
			t.Fatalf("expected ConfigValid condition to be set")
		}
		if cond.Status != metav1.ConditionTrue {
			t.Errorf("ConfigValid status = %s, want True", cond.Status)
		}
	})

	t.Run("ServedModelName conflict sets ConfigValid false", func(t *testing.T) {
		other := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "other-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/other",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					ServedModelName: "shared-name",
				},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					ServedModelName: "shared-name",
				},
			},
		}

		cb := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, other).WithStatusSubresource(&aiv1alpha2.Model{})
		recorder := &FakeEventRecorder{}
		r := &ModelReconciler{
			Client:   cb.Build(),
			Recorder: recorder,
		}

		r.checkAliasConflicts(context.Background(), model)

		cond := findCondition(model, aiv1alpha2.ConditionConfigValid)
		if cond == nil {
			t.Fatalf("expected ConfigValid condition to be set")
		}
		if cond.Status != metav1.ConditionFalse {
			t.Errorf("ConfigValid status = %s, want False", cond.Status)
		}
		if cond.Reason != aiv1alpha2.ReasonAliasConflict {
			t.Errorf("ConfigValid reason = %q, want %q", cond.Reason, aiv1alpha2.ReasonAliasConflict)
		}
		if len(recorder.Events) == 0 {
			t.Errorf("expected a warning event to be recorded")
		}
	})

	t.Run("alias conflict sets ConfigValid false", func(t *testing.T) {
		other := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "other-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/other",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					Aliases: []string{"common-alias"},
				},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					Aliases: []string{"common-alias"},
				},
			},
		}

		cb := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, other).WithStatusSubresource(&aiv1alpha2.Model{})
		recorder := &FakeEventRecorder{}
		r := &ModelReconciler{
			Client:   cb.Build(),
			Recorder: recorder,
		}

		r.checkAliasConflicts(context.Background(), model)

		cond := findCondition(model, aiv1alpha2.ConditionConfigValid)
		if cond == nil {
			t.Fatalf("expected ConfigValid condition to be set")
		}
		if cond.Status != metav1.ConditionFalse {
			t.Errorf("ConfigValid status = %s, want False", cond.Status)
		}
		if cond.Reason != aiv1alpha2.ReasonAliasConflict {
			t.Errorf("ConfigValid reason = %q, want %q", cond.Reason, aiv1alpha2.ReasonAliasConflict)
		}
	})

	t.Run("copilot alias conflict sets ConfigValid false", func(t *testing.T) {
		other := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "other-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/other",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					CopilotAlias: "shared-copilot",
				},
			},
		}
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "my-model", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://org/model",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{
					CopilotAlias: "shared-copilot",
				},
			},
		}

		cb := fake.NewClientBuilder().WithScheme(scheme).WithObjects(model, other).WithStatusSubresource(&aiv1alpha2.Model{})
		recorder := &FakeEventRecorder{}
		r := &ModelReconciler{
			Client:   cb.Build(),
			Recorder: recorder,
		}

		r.checkAliasConflicts(context.Background(), model)

		cond := findCondition(model, aiv1alpha2.ConditionConfigValid)
		if cond == nil {
			t.Fatalf("expected ConfigValid condition to be set")
		}
		if cond.Status != metav1.ConditionFalse {
			t.Errorf("ConfigValid status = %s, want False", cond.Status)
		}
		if cond.Reason != aiv1alpha2.ReasonAliasConflict {
			t.Errorf("ConfigValid reason = %q, want %q", cond.Reason, aiv1alpha2.ReasonAliasConflict)
		}
	})
}

func TestNodeHasActivePipelineWork(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	const (
		ns       = "flexinfer-system"
		nodeName = "cblevins-5930k"
	)

	t.Run("pending pod targeting node via nodeSelector is detected", func(t *testing.T) {
		// Regression for 2026-04-19/20 incident: gemma4-26b-a4b-gptq-dense quant
		// job sat Pending on cblevins-5930k (no spec.NodeName assigned yet) while
		// gonzalomo-fluxpony-imagegen held the GPU as warm primary. The prior
		// NodeName-only check missed it, so the warm model never yielded.
		pendingJobPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "gemma4-26b-a4b-gptq-dense-quantize-xyz",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "gemma4-26b-a4b-gptq-dense-quantize"},
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"kubernetes.io/hostname": nodeName},
				// No NodeName — pod is Pending, unscheduled.
			},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pendingJobPod).Build()
		r := &ModelReconciler{Client: fakeClient}

		if !r.nodeHasActivePipelineWork(context.Background(), ns, nodeName) {
			t.Fatalf("nodeHasActivePipelineWork(%q) = false, want true for unscheduled Pending quantize pod", nodeName)
		}
	})

	t.Run("pending pod targeting different node is ignored", func(t *testing.T) {
		pendingJobPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-quantize-abc",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "other-quantize"},
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{"kubernetes.io/hostname": "some-other-node"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pendingJobPod).Build()
		r := &ModelReconciler{Client: fakeClient}

		if r.nodeHasActivePipelineWork(context.Background(), ns, nodeName) {
			t.Fatalf("nodeHasActivePipelineWork(%q) = true, want false when pending pod targets a different node", nodeName)
		}
	})

	t.Run("pending pod with no node affinity is ignored", func(t *testing.T) {
		pendingJobPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "floating-quantize-abc",
				Namespace: ns,
				Labels:    map[string]string{"job-name": "floating-quantize"},
			},
			Spec:   corev1.PodSpec{},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		}

		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pendingJobPod).Build()
		r := &ModelReconciler{Client: fakeClient}

		if r.nodeHasActivePipelineWork(context.Background(), ns, nodeName) {
			t.Fatalf("nodeHasActivePipelineWork(%q) = true, want false when pending pod has no hostname selector", nodeName)
		}
	})
}
