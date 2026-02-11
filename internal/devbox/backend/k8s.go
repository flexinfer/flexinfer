package backend

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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

// K8sBackend implements Backend using a Kubernetes cluster.
type K8sBackend struct {
	clientset  *kubernetes.Clientset
	restConfig *rest.Config
	namespace  string
	registry   string
	dockerPath string // local docker CLI for building+pushing images
}

// K8sBackendConfig holds configuration for the K8s backend.
type K8sBackendConfig struct {
	Kubeconfig   string // path to kubeconfig file
	Namespace    string // namespace for sandbox pods (default: "devbox")
	Registry     string // image registry (e.g., "registry.harbor.lan")
	StorageClass string // storage class for PVCs (default: "longhorn")
}

// NewK8sBackend creates a new Kubernetes backend.
func NewK8sBackend(cfg K8sBackendConfig) (*K8sBackend, error) {
	if cfg.Namespace == "" {
		cfg.Namespace = "devbox"
	}
	if cfg.Registry == "" {
		cfg.Registry = "registry.harbor.lan"
	}

	restConfig, err := buildRestConfig(cfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("create clientset: %w", err)
	}

	// Docker CLI needed for building and pushing images
	dockerPath, _ := exec.LookPath("docker")

	return &K8sBackend{
		clientset:  clientset,
		restConfig: restConfig,
		namespace:  cfg.Namespace,
		registry:   cfg.Registry,
		dockerPath: dockerPath,
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
	if k.dockerPath == "" {
		return nil, fmt.Errorf("docker CLI not found — required for building images to push to registry")
	}

	// Prefix the tag with the registry for push
	registryTag := k.registryTag(opts.Tag)

	// Write Dockerfile to temp dir
	tmpDir, err := os.MkdirTemp("", "devbox-k8s-build-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dockerfilePath := tmpDir + "/Dockerfile"
	if err := os.WriteFile(dockerfilePath, opts.Dockerfile, 0600); err != nil {
		return nil, fmt.Errorf("write Dockerfile: %w", err)
	}

	// Build locally
	buildCmd := exec.CommandContext(ctx, k.dockerPath,
		"build", "-t", registryTag, "-f", dockerfilePath, opts.ContextDir)
	out, err := buildCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker build failed: %w\n%s", err, string(out))
	}
	cached := strings.Contains(string(out), "CACHED")

	// Push to registry
	pushCmd := exec.CommandContext(ctx, k.dockerPath, "push", registryTag)
	pushOut, err := pushCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker push failed: %w\n%s", err, string(pushOut))
	}

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

	resources := corev1.ResourceRequirements{}
	if opts.MemoryMB > 0 {
		if resources.Limits == nil {
			resources.Limits = corev1.ResourceList{}
		}
		if resources.Requests == nil {
			resources.Requests = corev1.ResourceList{}
		}
		mem := resource.MustParse(fmt.Sprintf("%dMi", opts.MemoryMB))
		resources.Limits[corev1.ResourceMemory] = mem
		resources.Requests[corev1.ResourceMemory] = mem
	}
	if opts.CPUs > 0 {
		if resources.Limits == nil {
			resources.Limits = corev1.ResourceList{}
		}
		if resources.Requests == nil {
			resources.Requests = corev1.ResourceList{}
		}
		cpu := resource.MustParse(fmt.Sprintf("%dm", int(opts.CPUs*1000)))
		resources.Limits[corev1.ResourceCPU] = cpu
		resources.Requests[corev1.ResourceCPU] = cpu
	}

	// Volumes: emptyDir for workspace (agent copies files via exec)
	volumes := []corev1.Volume{
		{
			Name: "workspace",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}
	volumeMounts := []corev1.VolumeMount{
		{Name: "workspace", MountPath: "/workspace"},
	}

	// Add host-path mounts if requested (for NFS-backed workspace access)
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

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      opts.Name,
			Namespace: k.namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "mcp-devbox",
				"devbox/project":               opts.Name,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
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
