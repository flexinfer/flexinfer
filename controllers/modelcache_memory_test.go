package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
)

func newMemoryTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	return s
}

func TestDaemonSetForMemory_HFSource(t *testing.T) {
	s := newMemoryTestScheme()
	r := &ModelCacheReconciler{Scheme: s}

	m := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-model",
			Namespace: "default",
			UID:       "test-uid",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "Qwen/Qwen3-14B",
			StorageStrategy: aiv1alpha1.StorageStrategyMemory,
			NodeSelector:    map[string]string{"kubernetes.io/hostname": "gpu-node"},
		},
	}

	ds, err := r.daemonSetForMemory(m, "/dev/shm/flexinfer/test-model", "/dev/shm/flexinfer")
	require.NoError(t, err)
	require.NotNil(t, ds)

	assert.Equal(t, "test-model-ram-syncer", ds.Name)
	assert.Equal(t, "default", ds.Namespace)

	// Container image should be python-slim for HF downloads
	containers := ds.Spec.Template.Spec.Containers
	require.Len(t, containers, 1)
	assert.Equal(t, ImagePythonSlim, containers[0].Image)
	assert.Equal(t, "syncer", containers[0].Name)

	// Node selector propagated
	assert.Equal(t, map[string]string{"kubernetes.io/hostname": "gpu-node"},
		ds.Spec.Template.Spec.NodeSelector)

	// tmpfs volume mount for /dev/shm
	foundSHM := false
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "model-ram-cache" && v.HostPath != nil {
			assert.Equal(t, "/dev/shm/flexinfer", v.HostPath.Path)
			foundSHM = true
		}
	}
	assert.True(t, foundSHM, "expected /dev/shm hostPath volume")

	// Script should contain huggingface download
	assert.Contains(t, containers[0].Args[0], "huggingface")
}

func TestDaemonSetForMemory_PVCSource(t *testing.T) {
	s := newMemoryTestScheme()
	r := &ModelCacheReconciler{Scheme: s}

	existingClaim := "source-model-pvc"
	m := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ram-cache",
			Namespace: "default",
			UID:       "test-uid-2",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:            "Qwen/Qwen3-14B",
			StorageStrategy:   aiv1alpha1.StorageStrategyMemory,
			ExistingClaimName: &existingClaim,
			NodeSelector:      map[string]string{"kubernetes.io/hostname": "gpu-node"},
		},
	}

	ds, err := r.daemonSetForMemory(m, "/dev/shm/flexinfer/test-ram-cache", "/dev/shm/flexinfer")
	require.NoError(t, err)

	// Image should be alpine for PVC copy (no python needed)
	containers := ds.Spec.Template.Spec.Containers
	require.Len(t, containers, 1)
	assert.Equal(t, ImageAlpine, containers[0].Image)

	// Source PVC should be mounted
	foundSourcePVC := false
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "source-pvc" && v.PersistentVolumeClaim != nil {
			assert.Equal(t, existingClaim, v.PersistentVolumeClaim.ClaimName)
			assert.True(t, v.PersistentVolumeClaim.ReadOnly)
			foundSourcePVC = true
		}
	}
	assert.True(t, foundSourcePVC, "expected source PVC volume")

	// Script should contain rsync copy
	assert.Contains(t, containers[0].Args[0], "rsync")
}

func TestDaemonSetForMemory_DefaultNodeSelector(t *testing.T) {
	s := newMemoryTestScheme()
	r := &ModelCacheReconciler{Scheme: s}

	m := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-default-ns",
			Namespace: "default",
			UID:       "test-uid-3",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "Qwen/Qwen3-14B",
			StorageStrategy: aiv1alpha1.StorageStrategyMemory,
			// No NodeSelector — should default to GPU nodes
		},
	}

	ds, err := r.daemonSetForMemory(m, "/dev/shm/flexinfer/test-default-ns", "/dev/shm/flexinfer")
	require.NoError(t, err)

	// Default should be GPU node selector
	assert.Equal(t, map[string]string{"nvidia.com/gpu.present": "true"},
		ds.Spec.Template.Spec.NodeSelector)
}
