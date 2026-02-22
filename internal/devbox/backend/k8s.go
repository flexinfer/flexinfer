package backend

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const defaultBuilderImage = "quay.io/buildah/stable:v1.38.0"

// K8sBackend implements Backend using a Kubernetes cluster.
// Builds are performed in-cluster via Buildah pods — no local Docker daemon required.
type K8sBackend struct {
	clientset       kubernetes.Interface
	restConfig      *rest.Config
	namespace       string
	registry        string
	workspacePVC    string // PVC name for NFS workspace volume
	imagePullSecret string // image pull secret name for private registry
	workspaceRoot   string // host path to workspace (NFS export source)
	builderImage    string // Buildah builder image
}

// K8sBackendConfig holds configuration for the K8s backend.
type K8sBackendConfig struct {
	Kubeconfig      string // path to kubeconfig file
	Namespace       string // namespace for sandbox pods (default: "devbox")
	Registry        string // image registry (e.g., "registry.harbor.lan")
	StorageClass    string // storage class for PVCs (default: "longhorn")
	WorkspacePVC    string // PVC name for NFS workspace (default: "devbox-workspace-nfs")
	ImagePullSecret string // image pull secret name (default: "harbor-creds")
	WorkspaceRoot   string // host path to workspace (for NFS-relative path computation)
	BuilderImage    string // Buildah builder image (default: quay.io/buildah/stable:v1.38.0)
}

// NewK8sBackend creates a new Kubernetes backend.
func NewK8sBackend(cfg K8sBackendConfig) (*K8sBackend, error) {
	if cfg.Namespace == "" {
		cfg.Namespace = "devbox"
	}
	if cfg.Registry == "" {
		cfg.Registry = "registry.harbor.lan"
	}
	if cfg.WorkspacePVC == "" {
		cfg.WorkspacePVC = "devbox-workspace-nfs"
	}
	if cfg.ImagePullSecret == "" {
		cfg.ImagePullSecret = "harbor-creds"
	}
	if cfg.BuilderImage == "" {
		cfg.BuilderImage = defaultBuilderImage
	}
	if cfg.WorkspaceRoot == "" {
		home, _ := os.UserHomeDir()
		cfg.WorkspaceRoot = filepath.Join(home, "workspace")
	}

	restConfig, err := buildRestConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	return &K8sBackend{
		clientset:       clientset,
		restConfig:      restConfig,
		namespace:       cfg.Namespace,
		registry:        cfg.Registry,
		workspacePVC:    cfg.WorkspacePVC,
		imagePullSecret: cfg.ImagePullSecret,
		workspaceRoot:   cfg.WorkspaceRoot,
		builderImage:    cfg.BuilderImage,
	}, nil
}

func buildRestConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	// Try in-cluster first, then default kubeconfig
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	home, _ := os.UserHomeDir()
	return clientcmd.BuildConfigFromFlags("", home+"/.kube/config")
}

func (k *K8sBackend) Health(ctx context.Context) error {
	_, err := k.clientset.CoreV1().Namespaces().Get(ctx, k.namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("namespace %q not accessible: %w", k.namespace, err)
	}
	return nil
}

const buildMaxRetries = 2

func (k *K8sBackend) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	registryTag := k.registryTag(opts.Tag)

	// Compute NFS-relative paths so the Buildah pod can find files via the shared PVC.
	contextRel, err := filepath.Rel(k.workspaceRoot, opts.ContextDir)
	if err != nil || strings.HasPrefix(contextRel, "..") {
		return nil, fmt.Errorf("context dir %q is not under workspace root %q", opts.ContextDir, k.workspaceRoot)
	}

	buildName := sanitizeBuildName(opts.Tag)

	// Inject Dockerfile via ConfigMap so it's accessible to the Buildah pod
	// without requiring the local filesystem to be the NFS volume.
	cmName := "buildah-dockerfile-" + buildName
	if err := k.createDockerfileConfigMap(ctx, cmName, opts.Dockerfile); err != nil {
		return nil, fmt.Errorf("create dockerfile configmap: %w", err)
	}
	defer func() {
		_ = k.deleteConfigMap(context.Background(), cmName)
	}()

	podName := "buildah-build-" + buildName

	var lastErr error
	for attempt := range buildMaxRetries {
		result, err := k.runBuildPod(ctx, podName, registryTag, cmName, contextRel)
		if err == nil {
			return result, nil
		}
		lastErr = err

		// Don't retry on context cancellation
		if ctx.Err() != nil {
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

func (k *K8sBackend) Start(ctx context.Context, opts StartOpts) (*StartResult, error) {
	// Delete existing pod if present (idempotent)
	_ = k.Stop(ctx, opts.Name)

	registryTag := k.registryTag(opts.ImageTag)
	pod := k.buildPodSpec(opts, registryTag)

	created, err := k.clientset.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create pod: %w", err)
	}

	// Wait for pod to be Running; cleanup dangling pod on failure
	if err := k.waitForPodRunning(ctx, opts.Name, 120*time.Second); err != nil {
		_ = k.Stop(ctx, opts.Name) // cleanup dangling pod
		return nil, fmt.Errorf("pod not ready: %w", err)
	}

	return &StartResult{ContainerID: created.Name}, nil
}

func (k *K8sBackend) Exec(ctx context.Context, opts ExecOpts) (*ExecResult, error) {
	if opts.TimeoutSec > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutSec)*time.Second)
		defer cancel()
	}

	start := time.Now()

	// Build the command with workdir and env vars prepended
	shellCmd := opts.Command
	if len(opts.Env) > 0 {
		var envPrefix strings.Builder
		for k, v := range opts.Env {
			envPrefix.WriteString(fmt.Sprintf("export %s=%q; ", k, v))
		}
		shellCmd = envPrefix.String() + shellCmd
	}
	if opts.WorkDir != "" {
		shellCmd = fmt.Sprintf("cd %q && %s", opts.WorkDir, shellCmd)
	}

	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(opts.ContainerID).
		Namespace(k.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"sh", "-c", shellCmd},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})

	durationMs := time.Since(start).Milliseconds()

	exitCode := 0
	if streamErr != nil {
		if ctx.Err() != nil {
			return &ExecResult{
				ExitCode:   124,
				StdoutTail: "command timed out",
				DurationMs: durationMs,
			}, nil
		}
		// Extract exit code from error message if possible
		exitCode = parseExitCode(streamErr)
	}

	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = 20
	}

	stdoutTail, stdoutTotal, stdoutTrunc := TruncateOutput(stdoutBuf.String(), maxLines)
	stderrTail, stderrTotal, stderrTrunc := TruncateOutput(stderrBuf.String(), maxLines)

	return &ExecResult{
		ExitCode:    exitCode,
		StdoutLines: stdoutTotal,
		StderrLines: stderrTotal,
		StdoutTail:  stdoutTail,
		StderrTail:  stderrTail,
		DurationMs:  durationMs,
		Truncated:   stdoutTrunc || stderrTrunc,
		OOMKilled:   exitCode == 137,
	}, nil
}

func (k *K8sBackend) Stop(ctx context.Context, id string) error {
	gracePeriod := int64(5)
	err := k.clientset.CoreV1().Pods(k.namespace).Delete(ctx, id, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete pod: %w", err)
	}
	return nil
}

func (k *K8sBackend) Status(ctx context.Context, id string) (*StatusResult, error) {
	pod, err := k.clientset.CoreV1().Pods(k.namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if isNotFound(err) {
			return &StatusResult{Running: false, Status: "not_found"}, nil
		}
		return nil, fmt.Errorf("get pod: %w", err)
	}

	status := strings.ToLower(string(pod.Status.Phase))
	return &StatusResult{
		Running: pod.Status.Phase == corev1.PodRunning,
		Status:  status,
	}, nil
}

func (k *K8sBackend) Pause(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (k *K8sBackend) Resume(_ context.Context, _ string) error {
	return ErrNotSupported
}

func (k *K8sBackend) ReadFile(ctx context.Context, id, path string) ([]byte, error) {
	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(id).
		Namespace(k.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"cat", path},
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	}); err != nil {
		return nil, fmt.Errorf("read file %q: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (k *K8sBackend) WriteFile(ctx context.Context, id, path string, content []byte, mode string) error {
	if mode == "" {
		mode = "0644"
	}
	shellCmd := fmt.Sprintf("cat > %q && chmod %s %q", path, mode, path)
	req := k.clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(id).
		Namespace(k.namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "devbox",
			Command:   []string{"sh", "-c", shellCmd},
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(k.restConfig, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("create executor: %w", err)
	}

	var stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  bytes.NewReader(content),
		Stderr: &stderr,
	}); err != nil {
		return fmt.Errorf("write file %q: %w (%s)", path, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// buildPodSpec creates a Pod spec for a devbox sandbox.
func (k *K8sBackend) buildPodSpec(opts StartOpts, imageTag string) *corev1.Pod {
	env := make([]corev1.EnvVar, 0, len(opts.Env))
	for key, val := range opts.Env {
		env = append(env, corev1.EnvVar{Name: key, Value: val})
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

	// Volumes: NFS workspace via PVC (shared across all sandbox pods)
	volumes := []corev1.Volume{
		{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: k.workspacePVC,
				},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: "/workspace"},
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

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: k.namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: "mcp-devbox",
			ImagePullSecrets: []corev1.LocalObjectReference{
				{Name: k.imagePullSecret},
			},
			NodeSelector: map[string]string{
				"kubernetes.io/arch": "amd64",
			},
			Containers: []corev1.Container{
				{
					Name:            "devbox",
					Image:           imageTag,
					Command:         []string{"sleep", "infinity"},
					Env:             env,
					Resources:       resources,
					WorkingDir:      workDir(opts.WorkDir),
					VolumeMounts:    volumeMounts,
					ImagePullPolicy: corev1.PullAlways,
				},
			},
			Volumes: volumes,
		},
	}
}

// waitForPodRunning watches until the pod reaches Running phase or timeout.
// Uses the Watch API for sub-second latency instead of polling.
func (k *K8sBackend) waitForPodRunning(ctx context.Context, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := k.clientset.CoreV1().Pods(k.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name,
	})
	if err != nil {
		return fmt.Errorf("watch pod: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return fmt.Errorf("pod entered terminal phase: %s", podFailureReason(pod))
		}
		// Early exit on image pull errors
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil {
				if w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" {
					return fmt.Errorf("image pull error: %s — %s", w.Reason, w.Message)
				}
			}
		}
	}
	return fmt.Errorf("watch closed for pod %s", name)
}

// podFailureReason extracts a diagnostic string from a failed pod's container statuses.
func podFailureReason(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if t := cs.State.Terminated; t != nil {
			parts := []string{fmt.Sprintf("exit_code=%d", t.ExitCode)}
			if t.Reason != "" {
				parts = append(parts, "reason="+t.Reason)
			}
			if t.Message != "" {
				parts = append(parts, "message="+t.Message)
			}
			return strings.Join(parts, " ")
		}
	}
	if pod.Status.Message != "" {
		return pod.Status.Message
	}
	return string(pod.Status.Phase)
}

// buildBuildahPodSpec creates a Pod spec for a Buildah in-cluster build.
// Buildah runs rootless with --storage-driver=vfs --isolation=chroot —
// no privileged containers or SYS_ADMIN capability required.
// The Dockerfile is injected via a ConfigMap (dockerfileCM) so it doesn't
// need to exist on the NFS workspace volume.
func (k *K8sBackend) buildBuildahPodSpec(podName, destination, dockerfileCM, contextRel string) *corev1.Pod {
	gracePeriod := int64(0)
	runAsUser := int64(1000)
	runAsGroup := int64(1000)

	buildAndPush := strings.Join([]string{
		// Configure registries for short-name resolution (non-interactive builds)
		"mkdir -p /home/build/.config/containers",
		"&&",
		`printf 'unqualified-search-registries = ["docker.io"]\nshort-name-mode = "permissive"\n' > /home/build/.config/containers/registries.conf`,
		"&&",
		"buildah build-using-dockerfile",
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

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: k.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-devbox",
				"devbox/build":                 "buildah",
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
						{Name: "CONTAINERS_REGISTRIES_CONF", Value: "/home/build/.config/containers/registries.conf"},
					},
					SecurityContext: &corev1.SecurityContext{
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
					VolumeMounts: []corev1.VolumeMount{
						{Name: "workspace", MountPath: "/workspace"},
						{Name: "dockerfile", MountPath: "/buildah-dockerfile", ReadOnly: true},
						{Name: "buildah-storage", MountPath: "/home/build/.local/share/containers/storage"},
						{Name: "auth", MountPath: "/run/containers/0/auth.json", SubPath: "config.json", ReadOnly: true},
					},
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
				{
					Name: "buildah-storage",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							SizeLimit: resourcePtr(resource.MustParse("10Gi")),
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

// waitForPodDone watches until the pod reaches Succeeded or Failed, or timeout.
// Uses the Watch API for sub-second latency instead of polling.
// Returns early on image pull errors to avoid waiting the full timeout.
func (k *K8sBackend) waitForPodDone(ctx context.Context, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watcher, err := k.clientset.CoreV1().Pods(k.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: "metadata.name=" + name,
	})
	if err != nil {
		return fmt.Errorf("watch pod: %w", err)
	}
	defer watcher.Stop()

	for event := range watcher.ResultChan() {
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			continue
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			return fmt.Errorf("build pod failed: %s", podFailureReason(pod))
		}
		// Early exit on image pull errors
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil {
				if w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" {
					return fmt.Errorf("image pull error: %s — %s", w.Reason, w.Message)
				}
			}
		}
	}
	return fmt.Errorf("watch closed for pod %s", name)
}

// getPodLogs reads the last 100 lines from the buildah container.
func (k *K8sBackend) getPodLogs(ctx context.Context, podName string) (string, error) {
	tailLines := int64(100)
	req := k.clientset.CoreV1().Pods(k.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: "buildah",
		TailLines: &tailLines,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("get logs: %w", err)
	}
	defer stream.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, stream); err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	return buf.String(), nil
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

// deletePod deletes a pod with zero grace period (immediate).
func (k *K8sBackend) deletePod(ctx context.Context, name string) error {
	gracePeriod := int64(0)
	err := k.clientset.CoreV1().Pods(k.namespace).Delete(ctx, name, metav1.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete pod: %w", err)
	}
	return nil
}

// sanitizeBuildName extracts a filesystem-safe name from an image tag.
var buildNameRe = regexp.MustCompile(`[^a-zA-Z0-9-]`)

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

func resourcePtr(q resource.Quantity) *resource.Quantity { return &q }

// parseExitCode extracts the exit code from a K8s exec error.
// Returns 1 as default for non-zero exits when code can't be parsed.
func parseExitCode(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	// K8s exec errors look like: "command terminated with exit code 2"
	if strings.Contains(msg, "exit code") {
		var code int
		if _, scanErr := fmt.Sscanf(msg[strings.LastIndex(msg, "exit code")+len("exit code "):], "%d", &code); scanErr == nil {
			return code
		}
	}
	return 1
}

// isNotFound returns true if the error is a K8s "not found" error.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if apierrors.IsNotFound(err) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "not found")
}

// workDir returns the working directory, defaulting to "/workspace".
func workDir(dir string) string {
	if dir != "" {
		return dir
	}
	return "/workspace"
}

// Ensure K8sBackend implements Backend at compile time.
var _ Backend = (*K8sBackend)(nil)
