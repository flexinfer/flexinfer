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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const defaultKanikoImage = "gcr.io/kaniko-project/executor:v1.23.2"

// K8sBackend implements Backend using a Kubernetes cluster.
// Builds are performed in-cluster via Kaniko pods — no local Docker daemon required.
type K8sBackend struct {
	clientset       *kubernetes.Clientset
	restConfig      *rest.Config
	namespace       string
	registry        string
	workspacePVC    string // PVC name for NFS workspace volume
	imagePullSecret string // image pull secret name for private registry
	workspaceRoot   string // host path to workspace (NFS export source)
	kanikoImage     string // Kaniko executor image
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
	KanikoImage     string // Kaniko executor image (default: gcr.io/kaniko-project/executor:v1.23.2)
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
	if cfg.KanikoImage == "" {
		cfg.KanikoImage = defaultKanikoImage
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
		kanikoImage:     cfg.KanikoImage,
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

func (k *K8sBackend) Build(ctx context.Context, opts BuildOpts) (*BuildResult, error) {
	registryTag := k.registryTag(opts.Tag)

	// Compute NFS-relative paths so the Kaniko pod can find files via the shared PVC.
	contextRel, err := filepath.Rel(k.workspaceRoot, opts.ContextDir)
	if err != nil || strings.HasPrefix(contextRel, "..") {
		return nil, fmt.Errorf("context dir %q is not under workspace root %q", opts.ContextDir, k.workspaceRoot)
	}

	buildName := sanitizeBuildName(opts.Tag)
	cacheRepo := k.registry + "/cache/devbox"

	// Inject Dockerfile via ConfigMap so it's accessible to the Kaniko pod
	// without requiring the local filesystem to be the NFS volume.
	cmName := "kaniko-dockerfile-" + buildName
	if err := k.createDockerfileConfigMap(ctx, cmName, opts.Dockerfile); err != nil {
		return nil, fmt.Errorf("create dockerfile configmap: %w", err)
	}
	defer func() {
		_ = k.deleteConfigMap(context.Background(), cmName)
	}()

	// Create the Kaniko build pod
	podName := "kaniko-build-" + buildName
	pod := k.buildKanikoPodSpec(podName, registryTag, cmName, contextRel, cacheRepo)

	// Delete any leftover build pod with the same name
	_ = k.deletePod(ctx, podName)

	if _, err := k.clientset.CoreV1().Pods(k.namespace).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("create kaniko pod: %w", err)
	}
	defer func() {
		_ = k.deletePod(context.Background(), podName)
	}()

	// Wait for the build to complete
	if err := k.waitForPodDone(ctx, podName, 10*time.Minute); err != nil {
		logs, _ := k.getPodLogs(ctx, podName)
		return nil, fmt.Errorf("kaniko build failed: %w\n%s", err, logs)
	}

	// Read build logs and check for cache hits
	logs, _ := k.getPodLogs(ctx, podName)
	cached := strings.Contains(logs, "Found layer in cache") ||
		strings.Contains(logs, "Using caching version of cmd")

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

// waitForPodRunning polls until the pod reaches Running phase or timeout.
func (k *K8sBackend) waitForPodRunning(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pod, err := k.clientset.CoreV1().Pods(k.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		switch pod.Status.Phase {
		case corev1.PodRunning:
			return nil
		case corev1.PodFailed, corev1.PodSucceeded:
			return fmt.Errorf("pod entered terminal phase: %s", pod.Status.Phase)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return fmt.Errorf("pod %s did not reach Running within %s", name, timeout)
}

// buildKanikoPodSpec creates a Pod spec for a Kaniko in-cluster build.
// The Dockerfile is injected via a ConfigMap (dockerfileCM) so it doesn't
// need to exist on the NFS workspace volume.
func (k *K8sBackend) buildKanikoPodSpec(podName, destination, dockerfileCM, contextRel, cacheRepo string) *corev1.Pod {
	gracePeriod := int64(0)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: k.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-devbox",
				"devbox/build":                 "kaniko",
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
					Name:  "kaniko",
					Image: k.kanikoImage,
					Args: []string{
						"--context=dir:///workspace/" + contextRel,
						"--dockerfile=/kaniko-dockerfile/Dockerfile",
						"--destination=" + destination,
						"--cache=true",
						"--cache-repo=" + cacheRepo,
						"--cache-copy-layers",
						"--snapshot-mode=redo",
						"--use-new-run",
						"--skip-tls-verify",
						"--skip-tls-verify-pull",
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
						{Name: "docker-config", MountPath: "/kaniko/.docker", ReadOnly: true},
						{Name: "dockerfile", MountPath: "/kaniko-dockerfile", ReadOnly: true},
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
					Name: "docker-config",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: k.imagePullSecret,
							Items: []corev1.KeyToPath{
								{Key: ".dockerconfigjson", Path: "config.json"},
							},
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
			},
		},
	}
}

// waitForPodDone polls until the pod reaches Succeeded or Failed, or timeout.
// Returns early on image pull errors to avoid waiting the full timeout.
func (k *K8sBackend) waitForPodDone(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pod, err := k.clientset.CoreV1().Pods(k.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get pod: %w", err)
		}
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			return nil
		case corev1.PodFailed:
			return fmt.Errorf("build pod failed (phase: %s)", pod.Status.Phase)
		}
		// Check for image pull errors (early exit)
		for _, cs := range pod.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil {
				if w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" {
					return fmt.Errorf("image pull error: %s — %s", w.Reason, w.Message)
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return fmt.Errorf("build pod %s did not complete within %s", name, timeout)
}

// getPodLogs reads the last 100 lines from the kaniko container.
func (k *K8sBackend) getPodLogs(ctx context.Context, podName string) (string, error) {
	tailLines := int64(100)
	req := k.clientset.CoreV1().Pods(k.namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: "kaniko",
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
				"devbox/build":                 "kaniko",
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
	if strings.Contains(tag, "/") && strings.Contains(strings.Split(tag, "/")[0], ".") {
		return tag // already has a registry prefix
	}
	return k.registry + "/" + tag
}

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
	return err != nil && strings.Contains(err.Error(), "not found")
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
