package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildDepFiles are dependency manifest files included in the build ConfigMap
// for tar-pipe mode. These are the files that Dockerfile templates COPY.
var buildDepFiles = []string{
	"go.mod", "go.sum",
	"package.json", "pnpm-lock.yaml", "yarn.lock", "package-lock.json",
	"pyproject.toml", "uv.lock", "poetry.lock", "requirements.txt",
	"Cargo.toml", "Cargo.lock",
	"Gemfile", "Gemfile.lock",
}

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

	// Inject Dockerfile (and dep files in tar-pipe mode) via ConfigMap.
	cmName := "buildah-dockerfile-" + buildName

	// In tar-pipe mode, bundle dep files into the ConfigMap and use it as build context.
	// This eliminates the need for workspace volume during builds.
	buildContextDir := "/workspace/" + contextRel
	if k.syncMode == "tar-pipe" {
		depFiles := readDepFiles(opts.ContextDir)
		if err := k.createBuildConfigMap(buildCtx, cmName, opts.Dockerfile, depFiles); err != nil {
			return nil, fmt.Errorf("create build configmap: %w", err)
		}
		buildContextDir = "/buildah-dockerfile"
	} else {
		if err := k.createDockerfileConfigMap(buildCtx, cmName, opts.Dockerfile); err != nil {
			return nil, fmt.Errorf("create dockerfile configmap: %w", err)
		}
	}
	defer func() {
		_ = k.deleteConfigMap(context.Background(), cmName)
	}()

	podName := "buildah-build-" + buildName

	var lastErr error
	for attempt := range buildMaxRetries {
		result, err := k.runBuildPod(buildCtx, podName, registryTag, cmName, buildContextDir)
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
// buildContext is the absolute path inside the pod (e.g., "/workspace/services/loom-core"
// or "/buildah-dockerfile" for tar-pipe mode).
func (k *K8sBackend) runBuildPod(ctx context.Context, podName, registryTag, cmName, buildContext string) (*BuildResult, error) {
	pod := k.buildBuildahPodSpec(podName, registryTag, cmName, buildContext)

	// Delete any leftover build pod with the same name
	_ = k.deletePod(ctx, podName)

	if _, err := k.clientset.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create buildah pod: %w", err)
	}
	defer func() {
		_ = k.deletePod(context.Background(), podName)
	}()

	// Wait for the build to complete (must match the 15-minute build context timeout)
	if err := k.waitForPodDone(ctx, podName, 15*time.Minute); err != nil {
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
// buildContext is the absolute path inside the pod to use as the Docker build context
// (e.g., "/workspace/services/loom-core" for NFS/git, "/buildah-dockerfile" for tar-pipe).
func (k *K8sBackend) buildBuildahPodSpec(podName, destination, dockerfileCM, buildContext string) *corev1.Pod {
	gracePeriod := int64(0)
	runAsUser := int64(0)
	runAsGroup := int64(0)

	// Cache repo: repository path without tag for --cache-from (buildah v1.29+
	// requires a bare repository reference, no tag or digest).
	// Cache tag: full image:cache reference for tagging and pushing cache layers.
	cacheRepo := destination
	if idx := strings.LastIndex(cacheRepo, ":"); idx > 0 {
		cacheRepo = cacheRepo[:idx]
	}
	cacheTag := cacheRepo + ":cache"

	buildAndPush := strings.Join([]string{
		// Configure registries for short-name resolution (non-interactive builds)
		"mkdir -p /etc/containers",
		"&&",
		`printf 'unqualified-search-registries = ["docker.io"]\nshort-name-mode = "permissive"\n' > /etc/containers/registries.conf`,
		"&&",
		"buildah build-using-dockerfile",
		"--storage-driver=vfs",
		"--isolation=chroot",
		"--tls-verify=false",
		"--layers",
		"--cache-from=" + cacheRepo,
		"-f /buildah-dockerfile/Dockerfile",
		"-t " + destination,
		buildContext,
		"&&",
		"buildah push --storage-driver=vfs --tls-verify=false " + destination,
		"&&",
		// Push a cache tag so future builds can use --cache-from
		"buildah tag --storage-driver=vfs " + destination + " " + cacheTag,
		"&&",
		"buildah push --storage-driver=vfs --tls-verify=false " + cacheTag,
	}, " ")

	volumeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: "/workspace"},
		{Name: "dockerfile", MountPath: "/buildah-dockerfile", ReadOnly: true},
		{Name: "buildah-storage", MountPath: "/var/lib/containers/storage"},
		{Name: "auth", MountPath: "/run/containers/0/auth.json", SubPath: "config.json", ReadOnly: true},
	}

	// Workspace volume: tar-pipe and git-clone use emptyDir (no NFS),
	// NFS mode uses the shared PVC.
	var workspaceVolume corev1.Volume
	var initContainers []corev1.Container

	switch {
	case k.syncMode == "tar-pipe":
		// Tar-pipe mode: emptyDir workspace. For builds, dep files are injected
		// via ConfigMap so no workspace sync is needed.
		workspaceVolume = corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: resourcePtr(resource.MustParse("5Gi")),
				},
			},
		}
	case k.gitEnabled():
		workspaceVolume = corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					SizeLimit: resourcePtr(resource.MustParse("5Gi")),
				},
			},
		}
		// Build context path determines the clone target.
		initContainers = []corev1.Container{
			k.gitCloneInitContainer(buildContext),
		}
	default:
		workspaceVolume = corev1.Volume{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: k.workspacePVC,
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
			InitContainers: initContainers,
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
							corev1.ResourceCPU:    resource.MustParse("1"),
							corev1.ResourceMemory: resource.MustParse("1Gi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("3"),
							corev1.ResourceMemory: resource.MustParse("3Gi"),
						},
					},
					VolumeMounts: volumeMounts,
				},
			},
			Volumes: []corev1.Volume{
				workspaceVolume,
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
				{
					Name: "buildah-storage",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							SizeLimit: resourcePtr(resource.MustParse("40Gi")),
						},
					},
				},
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

// createBuildConfigMap creates a ConfigMap containing the Dockerfile and dep files.
// Used in tar-pipe mode where the ConfigMap serves as the entire build context.
func (k *K8sBackend) createBuildConfigMap(ctx context.Context, name string, dockerfile []byte, depFiles map[string]string) error {
	data := map[string]string{
		"Dockerfile": string(dockerfile),
	}
	for fname, content := range depFiles {
		data[fname] = content
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: k.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-devbox",
				"devbox/build":                 "buildah",
			},
		},
		Data: data,
	}
	_ = k.deleteConfigMap(ctx, name)
	_, err := k.clientset.CoreV1().ConfigMaps(k.namespace).Create(ctx, cm, metav1.CreateOptions{})
	return err
}

// readDepFiles reads dependency manifest files from the project directory.
// Returns a map of filename→content for files that exist.
func readDepFiles(projectDir string) map[string]string {
	files := make(map[string]string)
	for _, name := range buildDepFiles {
		data, err := os.ReadFile(filepath.Join(projectDir, name))
		if err != nil {
			continue
		}
		files[name] = string(data)
	}
	return files
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
