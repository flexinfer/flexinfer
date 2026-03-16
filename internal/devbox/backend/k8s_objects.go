package backend

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// buildPodSpec creates a Pod spec for a devbox sandbox.
func (k *K8sBackend) buildPodSpec(opts StartOpts, imageTag string) *corev1.Pod {
	env := make([]corev1.EnvVar, 0, len(opts.Env)+len(opts.SecretEnv))
	for key, val := range opts.Env {
		env = append(env, corev1.EnvVar{Name: key, Value: val})
	}
	for _, s := range opts.SecretEnv {
		env = append(env, corev1.EnvVar{
			Name: s.Name,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: s.SecretName},
					Key:                  s.SecretKey,
					Optional:             boolPtr(true),
				},
			},
		})
	}

	// Set only limits (not requests) so sandbox pods schedule as Burstable/BestEffort.
	// Dev sandbox pods are short-lived; low requests prevent scheduling failures
	// on clusters with high CPU reservation.
	resources := corev1.ResourceRequirements{}
	if opts.MemoryMB > 0 {
		resources.Limits = corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse(fmt.Sprintf("%dMi", opts.MemoryMB)),
		}
	}
	if opts.CPUs > 0 {
		if resources.Limits == nil {
			resources.Limits = corev1.ResourceList{}
		}
		resources.Limits[corev1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", int(opts.CPUs*1000)))
	}

	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount
	var initContainers []corev1.Container

	switch {
	case k.syncMode == "tar-pipe":
		// Tar-pipe mode: emptyDir workspace populated post-start via SyncWorkspace().
		// No initContainer needed — files are streamed in after pod is running.
		volumes = []corev1.Volume{
			{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}
		volumeMounts = []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
		}

	case k.gitEnabled():
		// Git-clone mode: emptyDir workspace populated by initContainer.
		// Eliminates NFS dependency — each pod gets fresh source from git.
		volumes = []corev1.Volume{
			{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		}
		volumeMounts = []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
		}
		initContainers = []corev1.Container{
			k.gitCloneInitContainer(opts.WorkDir),
		}

	default:
		// NFS PVC mode (legacy): shared workspace via NFS.
		volumes = []corev1.Volume{
			{
				Name: "workspace",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: k.workspacePVC,
					},
				},
			},
		}
		volumeMounts = []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
		}
	}

	// Add secret volume mounts (e.g., auth token files for agent CLIs).
	for i, sm := range opts.SecretMounts {
		volName := fmt.Sprintf("secret-%d", i)
		items := make([]corev1.KeyToPath, 0, len(sm.Items))
		for _, item := range sm.Items {
			items = append(items, corev1.KeyToPath{Key: item.Key, Path: item.Path})
		}
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  sm.SecretName,
					Items:       items,
					Optional:    boolPtr(true),
					DefaultMode: int32Ptr(0o600),
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: sm.MountPath,
			ReadOnly:  true,
		})
	}

	// Add host-path mounts if requested (for additional bind mounts)
	for i, m := range opts.Mounts {
		volName := fmt.Sprintf("mount-%d", i)
		hostPathType := corev1.HostPathDirectoryOrCreate
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: m.Host,
					Type: &hostPathType,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: m.Container,
			ReadOnly:  m.ReadOnly,
		})
	}

	labels := map[string]string{
		"app.kubernetes.io/managed-by": "mcp-devbox",
		"devbox/project":               opts.Name,
	}
	if opts.AgentID != "" {
		labels["devbox/agent-id"] = opts.AgentID
	}

	gracePeriod := int64(3)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: k.namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:                 corev1.RestartPolicyNever,
			TerminationGracePeriodSeconds: &gracePeriod,
			ServiceAccountName:            "mcp-devbox",
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: k.imagePullSecret},
			},
			NodeSelector: map[string]string{
				"kubernetes.io/arch": "amd64",
			},
			InitContainers: initContainers,
			Containers: []corev1.Container{
				{
					Name:            "devbox",
					Image:           imageTag,
					Command:         []string{"sleep", "infinity"},
					Env:             env,
					Resources:       resources,
					WorkingDir:      workDir(opts.WorkDir),
					VolumeMounts:    volumeMounts,
					ImagePullPolicy: imagePullPolicy(imageTag),
				},
			},
			Volumes: volumes,
		},
	}
}

// imagePullPolicy returns IfNotPresent for hash-tagged images (immutable)
// and Always for untagged or :latest images.
func imagePullPolicy(imageTag string) corev1.PullPolicy {
	// Extract the tag portion after the last colon
	idx := strings.LastIndex(imageTag, ":")
	if idx < 0 {
		return corev1.PullAlways // no tag → always pull
	}
	tag := imageTag[idx+1:]
	if tag == "" || tag == "latest" {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}

// registryTag prepends the registry to a local image tag.
func (k *K8sBackend) registryTag(tag string) string {
	if strings.Contains(tag, "/") {
		prefix := strings.Split(tag, "/")[0]
		if strings.Contains(prefix, ".") || strings.Contains(prefix, ":") || prefix == "localhost" {
			return tag // already has a registry prefix
		}
	}
	return k.registry + "/" + tag
}

func boolPtr(b bool) *bool                               { return &b }
func int32Ptr(i int32) *int32                            { return &i }
func resourcePtr(q resource.Quantity) *resource.Quantity { return &q }

// workDir returns the working directory, defaulting to "/workspace".
func workDir(dir string) string {
	if dir != "" {
		return dir
	}
	return "/workspace"
}

// gitEnabled returns true when the backend is configured for git-clone mode.
func (k *K8sBackend) gitEnabled() bool {
	return k.gitBaseURL != "" && k.gitSecret != ""
}

// gitCloneInitContainer builds an initContainer that clones a git repo into
// the workspace emptyDir. The workDir determines which subdirectory receives
// the clone (e.g., /workspace/services/loom-core → repo "loom-core" cloned
// into /workspace/services/loom-core/).
func (k *K8sBackend) gitCloneInitContainer(workDirPath string) corev1.Container {
	// Derive project name and clone destination from workDir.
	// workDir is typically "/workspace/services/<project>" or "/workspace/<project>".
	cloneDest := workDirPath
	if cloneDest == "" || cloneDest == "/workspace" {
		cloneDest = "/workspace/project"
	}

	// Extract project name from the last path component for the git URL.
	parts := strings.Split(strings.TrimSuffix(cloneDest, "/"), "/")
	projectName := parts[len(parts)-1]
	repoURL := strings.TrimSuffix(k.gitBaseURL, "/") + "/" + projectName + ".git"

	// Clone script: shallow clone for speed. Preserve the original URL
	// scheme (http vs https) so internal HTTP-only registries work.
	scheme := "https"
	if strings.HasPrefix(repoURL, "http://") {
		scheme = "http"
	}
	hostAndPath := strings.TrimPrefix(strings.TrimPrefix(repoURL, "https://"), "http://")
	cloneScript := fmt.Sprintf(
		`set -e
mkdir -p "$(dirname %q)"
git clone --depth 1 "%s://token:${GIT_TOKEN}@%s" %q
echo "git-clone: cloned %s into %s"`,
		cloneDest,
		scheme,
		hostAndPath,
		cloneDest,
		projectName,
		cloneDest,
	)

	return corev1.Container{
		Name:    "git-clone",
		Image:   "alpine/git:latest",
		Command: []string{"sh", "-c", cloneScript},
		Env: []corev1.EnvVar{
			{
				Name: "GIT_TOKEN",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: k.gitSecret},
						Key:                  "token",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "workspace", MountPath: "/workspace"},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("100m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
	}
}
