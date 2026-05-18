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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// containsField returns true if any entry in fields contains substr.
func containsField(fields []string, substr string) bool {
	for _, f := range fields {
		if strings.Contains(f, substr) {
			return true
		}
	}
	return false
}

// baseDeployment returns a minimal deployment for use in comparison tests.
func baseDeployment() *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
			Labels:    map[string]string{"app": "test"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Strategy: appsv1.DeploymentStrategy{},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "model",
						Image: "test:latest",
						Args:  []string{"--model", "/models/test"},
					}},
				},
			},
		},
	}
}

// copyMap returns a shallow copy of a string map. Returns nil for nil input.
func copyMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// =============================================================================
// 1. TestDeploymentManagedFieldChanges
// =============================================================================

func TestDeploymentManagedFieldChanges(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		labels := copyMap(dep.Labels)
		annotations := copyMap(dep.Annotations)
		templateLabels := copyMap(dep.Spec.Template.Labels)
		podAnnotations := copyMap(dep.Spec.Template.Annotations)

		fields := deploymentManagedFieldChanges(dep, desired, labels, annotations, templateLabels, podAnnotations)
		if len(fields) != 0 {
			t.Errorf("expected no changes, got %v", fields)
		}
	})

	t.Run("replica change", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		desired.Replicas = int32Ptr(3)

		fields := deploymentManagedFieldChanges(dep, desired, dep.Labels, dep.Annotations, dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
		if !containsField(fields, "replicas(1\u21923)") {
			t.Errorf("expected replicas(1\u21923) in %v", fields)
		}
	})

	t.Run("image change", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		desired.Template.Spec.Containers[0].Image = "test:v2"

		fields := deploymentManagedFieldChanges(dep, desired, dep.Labels, dep.Annotations, dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
		if !containsField(fields, "containers") {
			t.Errorf("expected 'containers' in %v", fields)
		}
	})

	t.Run("strategy change", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		desired.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RecreateDeploymentStrategyType,
		}

		fields := deploymentManagedFieldChanges(dep, desired, dep.Labels, dep.Annotations, dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
		if !containsField(fields, "strategy") {
			t.Errorf("expected 'strategy' in %v", fields)
		}
	})

	t.Run("labels change", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		newLabels := map[string]string{"app": "test", "version": "v2"}

		fields := deploymentManagedFieldChanges(dep, desired, newLabels, dep.Annotations, dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
		if !containsField(fields, "labels") {
			t.Errorf("expected 'labels' in %v", fields)
		}
	})

	t.Run("annotations change", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		newAnnotations := map[string]string{"note": "updated"}

		fields := deploymentManagedFieldChanges(dep, desired, dep.Labels, newAnnotations, dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
		if !containsField(fields, "annotations") {
			t.Errorf("expected 'annotations' in %v", fields)
		}
	})

	t.Run("multiple changes", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		desired.Replicas = int32Ptr(5)
		desired.Strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		desired.Template.Spec.Containers[0].Image = "test:v3"
		newLabels := map[string]string{"app": "changed"}

		fields := deploymentManagedFieldChanges(dep, desired, newLabels, dep.Annotations, dep.Spec.Template.Labels, dep.Spec.Template.Annotations)

		expected := []string{"replicas", "strategy", "containers", "labels"}
		for _, exp := range expected {
			if !containsField(fields, exp) {
				t.Errorf("expected %q in %v", exp, fields)
			}
		}
		if len(fields) < 4 {
			t.Errorf("expected at least 4 changed fields, got %d: %v", len(fields), fields)
		}
	})

	t.Run("image pull secrets change", func(t *testing.T) {
		dep := baseDeployment()
		desired := dep.Spec.DeepCopy()
		desired.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "harbor-creds"}}

		fields := deploymentManagedFieldChanges(dep, desired, dep.Labels, dep.Annotations, dep.Spec.Template.Labels, dep.Spec.Template.Annotations)
		if !containsField(fields, "imagePullSecrets") {
			t.Errorf("expected 'imagePullSecrets' in %v", fields)
		}
	})
}

func TestParseModelImagePullSecrets(t *testing.T) {
	got := ParseModelImagePullSecrets(" harbor-creds, ,harbor-oci,harbor-creds ")
	want := []corev1.LocalObjectReference{
		{Name: "harbor-creds"},
		{Name: "harbor-oci"},
	}
	if !apiequality.Semantic.DeepEqual(got, want) {
		t.Fatalf("ParseModelImagePullSecrets() = %#v, want %#v", got, want)
	}
}

// =============================================================================
// 2. TestContainerManagedFieldsChanged
// =============================================================================

func TestContainerManagedFieldsChanged(t *testing.T) {
	baseContainers := func() []corev1.Container {
		return []corev1.Container{{
			Name:  "model",
			Image: "test:latest",
			Args:  []string{"--model", "/models/test"},
			Env:   []corev1.EnvVar{{Name: "FOO", Value: "bar"}},
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					HTTPGet: &corev1.HTTPGetAction{
						Path: "/health",
						Port: intstr.FromInt32(8080),
					},
				},
			},
		}}
	}

	t.Run("same containers", func(t *testing.T) {
		a, b := baseContainers(), baseContainers()
		if containerManagedFieldsChanged(a, b) {
			t.Error("expected no change for identical containers")
		}
	})

	t.Run("different image", func(t *testing.T) {
		a, b := baseContainers(), baseContainers()
		b[0].Image = "test:v2"
		if !containerManagedFieldsChanged(a, b) {
			t.Error("expected change for different image")
		}
	})

	t.Run("different args", func(t *testing.T) {
		a, b := baseContainers(), baseContainers()
		b[0].Args = []string{"--model", "/models/other"}
		if !containerManagedFieldsChanged(a, b) {
			t.Error("expected change for different args")
		}
	})

	t.Run("different env", func(t *testing.T) {
		a, b := baseContainers(), baseContainers()
		b[0].Env = []corev1.EnvVar{{Name: "FOO", Value: "baz"}}
		if !containerManagedFieldsChanged(a, b) {
			t.Error("expected change for different env")
		}
	})

	t.Run("different resources", func(t *testing.T) {
		a, b := baseContainers(), baseContainers()
		b[0].Resources.Limits[corev1.ResourceMemory] = resource.MustParse("8Gi")
		if !containerManagedFieldsChanged(a, b) {
			t.Error("expected change for different resources")
		}
	})

	t.Run("different readiness probe", func(t *testing.T) {
		a, b := baseContainers(), baseContainers()
		b[0].ReadinessProbe.ProbeHandler.HTTPGet.Path = "/ready"
		if !containerManagedFieldsChanged(a, b) {
			t.Error("expected change for different readiness probe")
		}
	})

	t.Run("different count", func(t *testing.T) {
		a := baseContainers()
		b := append(baseContainers(), corev1.Container{Name: "sidecar", Image: "sidecar:v1"})
		if !containerManagedFieldsChanged(a, b) {
			t.Error("expected change for different container count")
		}
	})

	t.Run("different terminationMessagePath ignored", func(t *testing.T) {
		a, b := baseContainers(), baseContainers()
		a[0].TerminationMessagePath = "/dev/termination-log"
		b[0].TerminationMessagePath = "/custom/path"
		if containerManagedFieldsChanged(a, b) {
			t.Error("terminationMessagePath is not a managed field, should not trigger change")
		}
	})
}

// =============================================================================
// 3. TestMergeContainers
// =============================================================================

func TestMergeContainers(t *testing.T) {
	t.Run("updates managed fields from desired", func(t *testing.T) {
		existing := []corev1.Container{{
			Name:                     "model",
			Image:                    "test:v1",
			Args:                     []string{"--old"},
			Env:                      []corev1.EnvVar{{Name: "OLD", Value: "val"}},
			TerminationMessagePath:   "/dev/termination-log",
			TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		}}
		desired := []corev1.Container{{
			Name:  "model",
			Image: "test:v2",
			Args:  []string{"--new"},
			Env:   []corev1.EnvVar{{Name: "NEW", Value: "val"}},
			// Desired does NOT set TerminationMessagePath.
		}}

		mergeContainers(existing, desired)

		if existing[0].Image != "test:v2" {
			t.Errorf("image not updated: got %q", existing[0].Image)
		}
		if len(existing[0].Args) != 1 || existing[0].Args[0] != "--new" {
			t.Errorf("args not updated: got %v", existing[0].Args)
		}
		if len(existing[0].Env) != 1 || existing[0].Env[0].Name != "NEW" {
			t.Errorf("env not updated: got %v", existing[0].Env)
		}
	})

	t.Run("preserves terminationMessagePath", func(t *testing.T) {
		existing := []corev1.Container{{
			Name:                   "model",
			Image:                  "test:v1",
			TerminationMessagePath: "/dev/termination-log",
		}}
		desired := []corev1.Container{{
			Name:  "model",
			Image: "test:v2",
			// TerminationMessagePath intentionally empty in desired.
		}}

		mergeContainers(existing, desired)

		// mergeContainers does NOT copy TerminationMessagePath — it is not in the
		// managed fields list. The existing value is preserved.
		if existing[0].TerminationMessagePath != "/dev/termination-log" {
			t.Errorf("terminationMessagePath should be preserved, got %q", existing[0].TerminationMessagePath)
		}
	})

	t.Run("mismatched lengths returns early", func(t *testing.T) {
		existing := []corev1.Container{
			{Name: "model", Image: "test:v1"},
		}
		desired := []corev1.Container{
			{Name: "model", Image: "test:v2"},
			{Name: "sidecar", Image: "sidecar:v1"},
		}

		mergeContainers(existing, desired)

		// Function returns early on mismatched lengths — existing is unchanged.
		if existing[0].Image != "test:v1" {
			t.Errorf("expected no-op for mismatched lengths, got image %q", existing[0].Image)
		}
	})
}

// =============================================================================
// 4. TestPodSecurityContextEqual
// =============================================================================

func TestPodSecurityContextEqual(t *testing.T) {
	t.Run("nil vs nil", func(t *testing.T) {
		if !podSecurityContextEqual(nil, nil) {
			t.Error("nil vs nil should be equal")
		}
	})

	t.Run("nil vs empty", func(t *testing.T) {
		if !podSecurityContextEqual(nil, &corev1.PodSecurityContext{}) {
			t.Error("nil vs &PodSecurityContext{} should be equal")
		}
	})

	t.Run("empty vs nil", func(t *testing.T) {
		if !podSecurityContextEqual(&corev1.PodSecurityContext{}, nil) {
			t.Error("&PodSecurityContext{} vs nil should be equal")
		}
	})

	t.Run("non-empty vs nil", func(t *testing.T) {
		a := &corev1.PodSecurityContext{SupplementalGroups: []int64{109}}
		if podSecurityContextEqual(a, nil) {
			t.Error("non-empty PodSecurityContext vs nil should not be equal")
		}
	})

	t.Run("same non-nil values", func(t *testing.T) {
		a := &corev1.PodSecurityContext{SupplementalGroups: []int64{109, 992}}
		b := &corev1.PodSecurityContext{SupplementalGroups: []int64{109, 992}}
		if !podSecurityContextEqual(a, b) {
			t.Error("identical non-nil PodSecurityContexts should be equal")
		}
	})
}

// =============================================================================
// 5. TestPodObjectEqual
// =============================================================================

func TestPodObjectEqual(t *testing.T) {
	t.Run("nil vs nil", func(t *testing.T) {
		if !podObjectEqual[corev1.Affinity](nil, nil) {
			t.Error("nil vs nil should be equal")
		}
	})

	t.Run("nil vs empty", func(t *testing.T) {
		if !podObjectEqual(nil, &corev1.Affinity{}) {
			t.Error("nil vs &Affinity{} should be equal")
		}
	})

	t.Run("empty vs nil", func(t *testing.T) {
		if !podObjectEqual(&corev1.Affinity{}, nil) {
			t.Error("&Affinity{} vs nil should be equal")
		}
	})

	t.Run("different values", func(t *testing.T) {
		a := &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{TopologyKey: "kubernetes.io/hostname"},
				},
			},
		}
		b := &corev1.Affinity{}
		if podObjectEqual(a, b) {
			t.Error("different Affinity values should not be equal")
		}
	})

	t.Run("same non-nil values", func(t *testing.T) {
		a := &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{TopologyKey: "kubernetes.io/hostname"},
				},
			},
		}
		b := &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{TopologyKey: "kubernetes.io/hostname"},
				},
			},
		}
		if !podObjectEqual(a, b) {
			t.Error("identical Affinity values should be equal")
		}
	})
}
