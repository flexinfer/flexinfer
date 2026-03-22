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

func newNodeLocalTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = aiv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = appsv1.AddToScheme(s)
	return s
}

func TestDaemonSetForNodeLocal_HFSource(t *testing.T) {
	s := newNodeLocalTestScheme()
	r := &ModelCacheReconciler{Scheme: s}

	m := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-node-local",
			Namespace: "default",
			UID:       "test-uid-nl",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "Qwen/Qwen3-14B",
			StorageStrategy: aiv1alpha1.StorageStrategyNodeLocal,
			NodeSelector:    map[string]string{"kubernetes.io/hostname": "gpu-node"},
		},
	}

	hostPath := "/var/lib/flexinfer/models"
	modelPath := hostPath + "/test-node-local"

	ds, err := r.daemonSetForNodeLocal(m, modelPath, hostPath)
	require.NoError(t, err)
	require.NotNil(t, ds)

	assert.Equal(t, "test-node-local-syncer", ds.Name)
	assert.Equal(t, "default", ds.Namespace)

	// Container should use python-slim for HF downloads
	containers := ds.Spec.Template.Spec.Containers
	require.Len(t, containers, 1)
	assert.Equal(t, ImagePythonSlim, containers[0].Image)
	assert.Equal(t, "syncer", containers[0].Name)

	// Node selector propagated
	assert.Equal(t, map[string]string{"kubernetes.io/hostname": "gpu-node"},
		ds.Spec.Template.Spec.NodeSelector)

	// hostPath volume
	foundHostPath := false
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "model-cache" && v.HostPath != nil {
			assert.Equal(t, hostPath, v.HostPath.Path)
			foundHostPath = true
		}
	}
	assert.True(t, foundHostPath, "expected hostPath volume")

	// Script should contain huggingface download
	assert.Contains(t, containers[0].Args[0], "huggingface")
}

func TestDaemonSetForNodeLocal_OCISource(t *testing.T) {
	s := newNodeLocalTestScheme()
	r := &ModelCacheReconciler{Scheme: s}

	registrySecret := "harbor-creds"
	m := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-oci-nl",
			Namespace: "default",
			UID:       "test-uid-oci-nl",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:               "oci://registry.harbor.lan/models/test:v1",
			StorageStrategy:      aiv1alpha1.StorageStrategyNodeLocal,
			NodeSelector:         map[string]string{"kubernetes.io/hostname": "gpu-node"},
			OCIRegistrySecretRef: &registrySecret,
		},
	}

	hostPath := "/var/lib/flexinfer/models"
	modelPath := hostPath + "/test-oci-nl"

	ds, err := r.daemonSetForNodeLocal(m, modelPath, hostPath)
	require.NoError(t, err)

	// Image should be ORAS for OCI pulls
	containers := ds.Spec.Template.Spec.Containers
	require.Len(t, containers, 1)
	assert.Equal(t, ImageORAS, containers[0].Image)

	// Script should contain oras pull
	assert.Contains(t, containers[0].Args[0], "oras pull")

	// Docker config secret should be mounted for registry auth
	foundDockerConfig := false
	for _, v := range ds.Spec.Template.Spec.Volumes {
		if v.Name == "docker-config" && v.Secret != nil {
			assert.Equal(t, registrySecret, v.Secret.SecretName)
			foundDockerConfig = true
		}
	}
	assert.True(t, foundDockerConfig, "expected docker-config secret volume for OCI auth")
}

func TestDaemonSetForNodeLocal_DefaultNodeSelector(t *testing.T) {
	s := newNodeLocalTestScheme()
	r := &ModelCacheReconciler{Scheme: s}

	m := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-default-ns-nl",
			Namespace: "default",
			UID:       "test-uid-default-nl",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "Qwen/Qwen3-14B",
			StorageStrategy: aiv1alpha1.StorageStrategyNodeLocal,
			// No NodeSelector — should default to GPU nodes
		},
	}

	ds, err := r.daemonSetForNodeLocal(m, "/var/lib/flexinfer/models/test", "/var/lib/flexinfer/models")
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"nvidia.com/gpu.present": "true"},
		ds.Spec.Template.Spec.NodeSelector)
}

func TestDaemonSetForNodeLocal_GPUTolerations(t *testing.T) {
	s := newNodeLocalTestScheme()
	r := &ModelCacheReconciler{Scheme: s}

	m := &aiv1alpha1.ModelCache{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tolerations",
			Namespace: "default",
			UID:       "test-uid-tol",
		},
		Spec: aiv1alpha1.ModelCacheSpec{
			Source:          "Qwen/Qwen3-14B",
			StorageStrategy: aiv1alpha1.StorageStrategyNodeLocal,
			NodeSelector:    map[string]string{"kubernetes.io/hostname": "gpu-node"},
		},
	}

	ds, err := r.daemonSetForNodeLocal(m, "/var/lib/flexinfer/models/test", "/var/lib/flexinfer/models")
	require.NoError(t, err)

	// Should have 3 GPU tolerations: dedicated=gpu, nvidia.com/gpu, amd.com/gpu
	tolerations := ds.Spec.Template.Spec.Tolerations
	assert.Len(t, tolerations, 3)

	tolerationKeys := make([]string, 0, len(tolerations))
	for _, tol := range tolerations {
		tolerationKeys = append(tolerationKeys, tol.Key)
	}
	assert.Contains(t, tolerationKeys, "dedicated")
	assert.Contains(t, tolerationKeys, "nvidia.com/gpu")
	assert.Contains(t, tolerationKeys, "amd.com/gpu")
}
