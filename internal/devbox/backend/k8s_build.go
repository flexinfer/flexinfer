package backend

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const buildMaxRetries = 2

func (k *K8sBackend) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	registryTag := k.registryTag(opts.Tag)

	// Compute NFS-relative paths so the Buildah pod can find files via the shared PVC.
	contextRel, err := filepath.Rel(k.workspaceRoot, opts.ContextDir)
	if err != nil || strings.HasPrefix(contextRel, "..") {
		return nil, fmt.Errorf("context dir %q is not under workspace root %q", opts.ContextDir, k.workspaceRoot)
	}

	// Detach from the request context: builds are long-running and must
	// survive MCP proxy timeouts / client disconnects. Use a generous
	// build-scoped timeout instead.
	buildCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	buildName := sanitizeBuildName(opts.Tag)

	// Inject Dockerfile via ConfigMap so it's accessible to the Buildah pod
	// without requiring the local filesystem to be the NFS volume.
	cmName := "buildah-dockerfile-" + buildName
	if err := k.createDockerfileConfigMap(buildCtx, cmName, opts.Dockerfile); err != nil {
		return nil, fmt.Errorf("create dockerfile configmap: %w", err)
	}
	defer func() {
		_ = k.deleteConfigMap(context.Background(), cmName)
	}()

	podName := "buildah-build-" + buildName

	// Serialize builds when using a shared cache PVC (RWO).
	if k.buildCachePVC != "" {
		k.buildMu.Lock()
		defer k.buildMu.Unlock()
	}

	var lastErr error
	for attempt := range buildMaxRetries {
		result, err := k.runBuildPod(buildCtx, podName, registryTag, cmName, contextRel)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Don't retry on context cancellation
		if buildCtx.Err() != nil {
			break
		}

		if attempt < buildMaxRetries-1 {
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
		}
	}
	return nil, lastErr
}

// runBuildPod creates a Buildah build pod, waits for completion, and returns the result.
func (k *K8sBackend) runBuildPod(ctx context.Context, podName, registryTag, cmName, contextRel string) (*BuildResult, error) {
	pod := k.buildBuildahPodSpec(podName, registryTag, cmName, contextRel)

	// Delete any leftover build pod with the same name
	_ = k.deletePod(ctx, podName)

	if _, err := k.clientset.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create buildah pod: %w", err)
	}
	defer func() {
		_ = k.deletePod(context.Background(), podName)
	}()

	// Wait for the build to complete
	if err := k.waitForPodDone(ctx, podName, 10*time.Minute); err != nil {
		logs, _ := k.getPodLogs(ctx, podName)
		return nil, fmt.Errorf("buildah build failed: %w\n%s", err, logs)
	}

	// Read build logs and check for cache hits
	logs, _ := k.getPodLogs(ctx, podName)
	cached := strings.Contains(logs, "Using cache") ||
		strings.Contains(logs, "--> Using cache")

	return &BuildResult{ImageTag: registryTag, Cached: cached}, nil
}

// buildBuildahPodSpec creates a Pod spec for a Buildah in-cluster build.
// Buildah runs as root with --storage-driver=vfs --isolation=chroot.
// Root is required because chroot isolation needs to remount / (MS_REC|MS_SLAVE).
// The Dockerfile is injected via a ConfigMap (dockerfileCM) so it doesn't
// need to exist on the NFS workspace volume.
func (k *K8sBackend) buildBuildahPodSpec(podName, destination, dockerfileCM, contextRel string) *corev1.Pod {
	gracePeriod := int64(0)
	runAsUser := int64(0)
	runAsGroup := int64(0)

	// Cache pruning prefix: if cache PVC is used, prune when >15GB keeping last 5 images.
	var cachePrunePrefix string
	if k.buildCachePVC != "" {
		cachePrunePrefix = "buildah --storage-driver=vfs images -q 2>/dev/null | tail -n +6 | xargs -r buildah --storage-driver=vfs rmi 2>/dev/null; "
	}

	buildAndPush := strings.Join([]string{
		// Configure registries for short-name resolution (non-interactive builds)
		"mkdir -p /etc/containers",
		"&&",
		`printf 'unqualified-search-registries = ["docker.io"]\nshort-name-mode = "permissive"\n' > /etc/containers/registries.conf`,
		"&&",
		cachePrunePrefix + "buildah build-using-dockerfile",
		"--storage-driver=vfs",
		"--isolation=chroot",
		"--tls-verify=false",
		"--layers",
		"-f /buildah-dockerfile/Dockerfile",
		"-t " + destination,
		"/workspace/" + contextRel,
		"&&",
		"buildah push --storage-driver=vfs --tls-verify=false " + destination,
	}, " ")

	volumeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: "/workspace"},
		{Name: "dockerfile", MountPath: "/buildah-dockerfile", ReadOnly: true},
		{Name: "buildah-storage", MountPath: "/var/lib/containers/storage"},
		{Name: "auth", MountPath: "/run/containers/0/auth.json", SubPath: "config.json", ReadOnly: true},
	}

	// Buildah storage: use persistent PVC if configured, otherwise EmptyDir.
	var storageVolume corev1.Volume
	if k.buildCachePVC != "" {
		storageVolume = corev1.Volume{
			Name: "buildah-storage",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: k.buildCachePVC,
				},
			},
		}
	} else {
		storageVolume = corev1.Volume{
			Name: "buildah-storage",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: resourcePtr(resource.MustParse("10Gi")),
				},
			},
		}
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: k.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-devbox",
				"devbox/build":                 "buildah",
			},
			Annotations: map[string]string{
				// Belt-and-suspenders: deprecated annotation for pre-1.30 clusters
				"container.apparmor.security.beta.kubernetes.io/buildah": "unconfined",
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			ServiceAccountName:            "mcp-devbox",
			TerminationGracePeriodSeconds: &gracePeriod,
			NodeSelector: map[string]string{
				"kubernetes.io/arch": "amd64",
			},
			Containers: []corev1.Container{
				{
					Name:    "buildah",
					Image:   k.builderImage,
					Command: []string{"sh", "-c", buildAndPush},
					Env: []corev1.EnvVar{
						{Name: "BUILDAH_ISOLATION", Value: "chroot"},
						{Name: "STORAGE_DRIVER", Value: "vfs"},
						{Name: "CONTAINERS_REGISTRIES_CONF", Value: "/etc/containers/registries.conf"},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: boolPtr(true),
						RunAsUser:  &runAsUser,
						RunAsGroup: &runAsGroup,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("2"),
							corev1.ResourceMemory: resource.MustParse("2Gi"),
						},
					},
					VolumeMounts: volumeMounts,
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "workspace",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: k.workspacePVC,
						},
					},
				},
				{
					Name: "dockerfile",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: dockerfileCM,
							},
						},
					},
				},
				storageVolume,
				{
					Name: "auth",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: k.imagePullSecret,
							Items: []corev1.KeyToPath{
								{Key: ".dockerconfigjson", Path: "config.json"},
							},
						},
					},
				},
			},
		},
	}
}

// createDockerfileConfigMap creates a ConfigMap containing the Dockerfile.
func (k *K8sBackend) createDockerfileConfigMap(ctx context.Context, name string, dockerfile []byte) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: k.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-devbox",
				"devbox/build":                 "buildah",
			},
		},
		Data: map[string]string{
			"Dockerfile": string(dockerfile),
		},
	}
	_ = k.deleteConfigMap(ctx, name)
	_, err := k.clientset.CoreV1().ConfigMaps(k.namespace).Create(ctx, cm, metav1.CreateOptions{})
	return err
}

// deleteConfigMap deletes a ConfigMap by name.
func (k *K8sBackend) deleteConfigMap(ctx context.Context, name string) error {
	err := k.clientset.CoreV1().ConfigMaps(k.namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !isNotFound(err) {
		return err
	}
	return nil
}

// sanitizeBuildName extracts a filesystem-safe name from an image tag.
func sanitizeBuildName(tag string) string {
	// Use the last path component, strip the registry prefix
	parts := strings.Split(tag, "/")
	name := parts[len(parts)-1]
	// Replace colons and other unsafe chars
	name = buildNameRe.ReplaceAllString(name, "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return name
}
