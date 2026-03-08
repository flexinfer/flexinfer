package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func testK8sBackend() *K8sBackend {
	return &K8sBackend{
		namespace:       "devbox",
		registry:        "registry.harbor.lan",
		workspacePVC:    "devbox-workspace-nfs",
		imagePullSecret: "harbor-creds",
		workspaceRoot:   "/workspace",
		builderImage:    "quay.io/buildah/stable:v1.38.0",
	}
}

func testK8sBackendGitClone() *K8sBackend {
	k := testK8sBackend()
	k.gitBaseURL = "https://gitlab.blevins.dev/homelab"
	k.gitSecret = "gitlab-creds"
	return k
}

func envMap(vars []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(vars))
	for _, v := range vars {
		out[v.Name] = v.Value
	}
	return out
}

func writeTestKubeconfig(t *testing.T) string {
	t.Helper()

	cfg := []byte(`apiVersion: v1
kind: Config
clusters:
  - name: test
    cluster:
      server: https://127.0.0.1:6443
contexts:
  - name: test
    context:
      cluster: test
      user: test
current-context: test
users:
  - name: test
    user:
      token: fake-token
`)

	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, cfg, 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func TestBuildRestConfig_UsesExplicitKubeconfig(t *testing.T) {
	kubeconfig := writeTestKubeconfig(t)

	cfg, err := buildRestConfig(kubeconfig)
	if err != nil {
		t.Fatalf("buildRestConfig returned error: %v", err)
	}
	if got := cfg.Host; got != "https://127.0.0.1:6443" {
		t.Fatalf("unexpected kube API host: %q", got)
	}
}

func TestNewK8sBackend_DefaultsAndOverrides(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	kubeconfig := writeTestKubeconfig(t)

	t.Run("defaults", func(t *testing.T) {
		k, err := NewK8sBackend(K8sBackendConfig{Kubeconfig: kubeconfig})
		if err != nil {
			t.Fatalf("NewK8sBackend returned error: %v", err)
		}

		if k.namespace != "devbox" {
			t.Fatalf("namespace=%q", k.namespace)
		}
		if k.registry != "registry.harbor.lan" {
			t.Fatalf("registry=%q", k.registry)
		}
		if k.workspacePVC != "devbox-workspace-nfs" {
			t.Fatalf("workspacePVC=%q", k.workspacePVC)
		}
		if k.imagePullSecret != "harbor-creds" {
			t.Fatalf("imagePullSecret=%q", k.imagePullSecret)
		}
		if k.builderImage != defaultBuilderImage {
			t.Fatalf("builderImage=%q", k.builderImage)
		}
		if k.workspaceRoot != filepath.Join(homeDir, "workspace") {
			t.Fatalf("workspaceRoot=%q", k.workspaceRoot)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		k, err := NewK8sBackend(K8sBackendConfig{
			Kubeconfig:      kubeconfig,
			Namespace:       "custom-ns",
			Registry:        "registry.example.test",
			WorkspacePVC:    "custom-pvc",
			ImagePullSecret: "custom-secret",
			WorkspaceRoot:   "/srv/workspace",
			BuilderImage:    "quay.io/custom/buildah:v1",
			SyncMode:        "tar-pipe",
			SyncExcludes:    []string{"**/*.tmp"},
			MaxSyncSize:     512,
			GitBaseURL:      "https://gitlab.example.test/team",
			GitSecret:       "git-token",
		})
		if err != nil {
			t.Fatalf("NewK8sBackend returned error: %v", err)
		}

		if k.namespace != "custom-ns" || k.registry != "registry.example.test" {
			t.Fatalf("unexpected core overrides: namespace=%q registry=%q", k.namespace, k.registry)
		}
		if k.workspacePVC != "custom-pvc" || k.imagePullSecret != "custom-secret" {
			t.Fatalf("unexpected storage overrides: pvc=%q secret=%q", k.workspacePVC, k.imagePullSecret)
		}
		if k.workspaceRoot != "/srv/workspace" || k.builderImage != "quay.io/custom/buildah:v1" {
			t.Fatalf("unexpected path/image overrides: root=%q image=%q", k.workspaceRoot, k.builderImage)
		}
		if k.syncMode != "tar-pipe" || len(k.syncExcludes) != 1 || k.syncExcludes[0] != "**/*.tmp" || k.maxSyncSize != 512 {
			t.Fatalf("unexpected sync overrides: mode=%q excludes=%v max=%d", k.syncMode, k.syncExcludes, k.maxSyncSize)
		}
		if k.gitBaseURL != "https://gitlab.example.test/team" || k.gitSecret != "git-token" {
			t.Fatalf("unexpected git overrides: base=%q secret=%q", k.gitBaseURL, k.gitSecret)
		}
	})
}

func TestBuildPodSpecDefaults(t *testing.T) {
	k := testK8sBackend()
	pod := k.buildPodSpec(StartOpts{
		Name:    "demo",
		Env:     map[string]string{"FOO": "bar"},
		AgentID: "agent-1",
	}, "registry.harbor.lan/devbox:latest")

	if pod.Name != "demo" || pod.Namespace != "devbox" {
		t.Fatalf("unexpected metadata: %#v", pod.ObjectMeta)
	}
	if pod.Labels["devbox/agent-id"] != "agent-1" {
		t.Fatalf("expected devbox/agent-id label, got: %#v", pod.Labels)
	}
	if got := pod.Spec.Containers[0].WorkingDir; got != "/workspace" {
		t.Fatalf("expected default work dir /workspace, got: %s", got)
	}
	if got := pod.Spec.Containers[0].Image; got != "registry.harbor.lan/devbox:latest" {
		t.Fatalf("unexpected image: %s", got)
	}
	if got := envMap(pod.Spec.Containers[0].Env)["FOO"]; got != "bar" {
		t.Fatalf("expected env FOO=bar, got: %q", got)
	}
	if pod.Spec.ImagePullSecrets[0].Name != "harbor-creds" {
		t.Fatalf("unexpected image pull secret: %#v", pod.Spec.ImagePullSecrets)
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].PersistentVolumeClaim == nil {
		t.Fatalf("expected default workspace PVC volume, got: %#v", pod.Spec.Volumes)
	}
}

func TestBuildPodSpecResourcesAndMounts(t *testing.T) {
	k := testK8sBackend()
	pod := k.buildPodSpec(StartOpts{
		Name:     "demo",
		MemoryMB: 256,
		CPUs:     0.5,
		WorkDir:  "/workspace/project",
		Mounts: []Mount{
			{Host: "/host/a", Container: "/container/a", ReadOnly: true},
			{Host: "/host/b", Container: "/container/b", ReadOnly: false},
		},
	}, "registry.harbor.lan/devbox:latest")

	container := pod.Spec.Containers[0]
	if got := container.WorkingDir; got != "/workspace/project" {
		t.Fatalf("unexpected working dir: %s", got)
	}
	if got := container.Resources.Limits.Memory().String(); got != "256Mi" {
		t.Fatalf("unexpected memory limit: %s", got)
	}
	if got := container.Resources.Limits.Cpu().MilliValue(); got != 500 {
		t.Fatalf("unexpected cpu milli value: %d", got)
	}

	if len(pod.Spec.Volumes) != 3 {
		t.Fatalf("expected 3 volumes (workspace + 2 host mounts), got %d", len(pod.Spec.Volumes))
	}
	if pod.Spec.Volumes[1].HostPath == nil || pod.Spec.Volumes[1].HostPath.Path != "/host/a" {
		t.Fatalf("unexpected host mount[0]: %#v", pod.Spec.Volumes[1])
	}
	if pod.Spec.Volumes[2].HostPath == nil || pod.Spec.Volumes[2].HostPath.Path != "/host/b" {
		t.Fatalf("unexpected host mount[1]: %#v", pod.Spec.Volumes[2])
	}

	if len(container.VolumeMounts) != 3 {
		t.Fatalf("expected 3 volume mounts, got %d", len(container.VolumeMounts))
	}
	if !container.VolumeMounts[1].ReadOnly || container.VolumeMounts[1].MountPath != "/container/a" {
		t.Fatalf("unexpected mount[1]: %#v", container.VolumeMounts[1])
	}
	if container.VolumeMounts[2].ReadOnly || container.VolumeMounts[2].MountPath != "/container/b" {
		t.Fatalf("unexpected mount[2]: %#v", container.VolumeMounts[2])
	}
}

func TestBuildBuildahPodSpec(t *testing.T) {
	k := testK8sBackend()
	pod := k.buildBuildahPodSpec("build-pod", "registry.harbor.lan/devbox:tag", "dockerfile-cm", "/workspace/services/loom-core")

	if pod.Name != "build-pod" || pod.Namespace != "devbox" {
		t.Fatalf("unexpected pod metadata: %#v", pod.ObjectMeta)
	}
	if pod.Annotations["container.apparmor.security.beta.kubernetes.io/buildah"] != "unconfined" {
		t.Fatalf("expected unconfined apparmor annotation, got: %#v", pod.Annotations)
	}
	container := pod.Spec.Containers[0]
	if container.Image != "quay.io/buildah/stable:v1.38.0" {
		t.Fatalf("unexpected builder image: %s", container.Image)
	}
	cmd := strings.Join(container.Command, " ")
	if !strings.Contains(cmd, "registries.conf") {
		t.Fatalf("expected registries.conf setup in build command: %s", cmd)
	}
	if !strings.Contains(cmd, "buildah build-using-dockerfile") ||
		!strings.Contains(cmd, "-t registry.harbor.lan/devbox:tag") ||
		!strings.Contains(cmd, "/workspace/services/loom-core") ||
		!strings.Contains(cmd, "buildah push") {
		t.Fatalf("unexpected build command: %s", cmd)
	}

	envs := envMap(container.Env)
	if envs["BUILDAH_ISOLATION"] != "chroot" || envs["STORAGE_DRIVER"] != "vfs" {
		t.Fatalf("unexpected buildah env: %#v", container.Env)
	}
	if envs["CONTAINERS_REGISTRIES_CONF"] != "/etc/containers/registries.conf" {
		t.Fatalf("expected CONTAINERS_REGISTRIES_CONF env var, got: %#v", container.Env)
	}
	if container.SecurityContext == nil || *container.SecurityContext.RunAsUser != 0 || *container.SecurityContext.RunAsGroup != 0 {
		t.Fatalf("unexpected security context: %#v", container.SecurityContext)
	}
	if container.SecurityContext.Privileged == nil || !*container.SecurityContext.Privileged {
		t.Fatalf("expected privileged build pod, got: %#v", container.SecurityContext)
	}

	if len(pod.Spec.Volumes) != 4 {
		t.Fatalf("expected 4 volumes, got %d", len(pod.Spec.Volumes))
	}
	if pod.Spec.Volumes[0].PersistentVolumeClaim == nil || pod.Spec.Volumes[0].PersistentVolumeClaim.ClaimName != "devbox-workspace-nfs" {
		t.Fatalf("unexpected workspace volume: %#v", pod.Spec.Volumes[0])
	}
	if pod.Spec.Volumes[1].ConfigMap == nil || pod.Spec.Volumes[1].ConfigMap.Name != "dockerfile-cm" {
		t.Fatalf("unexpected dockerfile configmap volume: %#v", pod.Spec.Volumes[1])
	}
	if pod.Spec.Volumes[3].Secret == nil || pod.Spec.Volumes[3].Secret.SecretName != "harbor-creds" {
		t.Fatalf("unexpected auth secret volume: %#v", pod.Spec.Volumes[3])
	}
}

func TestRegistryTag(t *testing.T) {
	k := testK8sBackend()

	if got := k.registryTag("service/devbox:1.0"); got != "registry.harbor.lan/service/devbox:1.0" {
		t.Fatalf("unexpected local tag rewrite: %s", got)
	}
	if got := k.registryTag("ghcr.io/acme/devbox:1.0"); got != "ghcr.io/acme/devbox:1.0" {
		t.Fatalf("expected fully qualified tag unchanged, got: %s", got)
	}
	if got := k.registryTag("localhost:5000/devbox:1.0"); got != "localhost:5000/devbox:1.0" {
		t.Fatalf("expected localhost registry tag unchanged, got: %s", got)
	}
}

func TestSanitizeBuildName(t *testing.T) {
	if got := sanitizeBuildName("registry.harbor.lan/team/my-image:tag"); got != "my-image-tag" {
		t.Fatalf("unexpected sanitized name: %s", got)
	}

	longTag := "registry.harbor.lan/team/" + strings.Repeat("a", 80) + ":v1"
	got := sanitizeBuildName(longTag)
	if len(got) != 63 {
		t.Fatalf("expected sanitized name to be truncated to 63 chars, got %d", len(got))
	}
	if strings.ContainsAny(got, ":/.") {
		t.Fatalf("expected sanitized name without reserved separators, got: %s", got)
	}
}

func TestParseExitCode(t *testing.T) {
	if got := parseExitCode(nil); got != 0 {
		t.Fatalf("expected exit code 0 for nil error, got %d", got)
	}
	if got := parseExitCode(errors.New("command terminated with exit code 137")); got != 137 {
		t.Fatalf("expected parsed exit code 137, got %d", got)
	}
	if got := parseExitCode(errors.New("some other error")); got != 1 {
		t.Fatalf("expected default exit code 1, got %d", got)
	}
}

func TestIsNotFound(t *testing.T) {
	if isNotFound(nil) {
		t.Fatal("expected nil error to be false")
	}
	if !isNotFound(errors.New("resource not found")) {
		t.Fatal("expected generic not-found message to match")
	}
	if !isNotFound(apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "pods"}, "x")) {
		t.Fatal("expected typed k8s not-found error to match")
	}
}

func TestWorkDir(t *testing.T) {
	if got := workDir(""); got != "/workspace" {
		t.Fatalf("unexpected default work dir: %s", got)
	}
	if got := workDir("/tmp/custom"); got != "/tmp/custom" {
		t.Fatalf("unexpected explicit work dir: %s", got)
	}
}

func TestBuildRejectsContextOutsideWorkspaceRoot(t *testing.T) {
	k := testK8sBackend()
	k.workspaceRoot = "/workspace"

	_, err := k.Build(context.Background(), BuildOpts{
		Tag:        "service/devbox:1.0",
		Dockerfile: []byte("FROM scratch"),
		ContextDir: "/tmp/outside",
	})
	if err == nil {
		t.Fatal("expected Build to reject context outside workspace root")
	}
	if !strings.Contains(err.Error(), "is not under workspace root") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildBuildahPodSpec_EmptyDirAndRegistryCache(t *testing.T) {
	k := testK8sBackend()
	pod := k.buildBuildahPodSpec("build-pod", "registry.harbor.lan/devbox:tag", "dockerfile-cm", "/workspace/services/loom-core")

	// Verify the buildah-storage volume always uses EmptyDir
	var found bool
	for _, vol := range pod.Spec.Volumes {
		if vol.Name == "buildah-storage" {
			found = true
			if vol.EmptyDir == nil {
				t.Fatal("expected EmptyDir volume for buildah-storage")
			}
			if vol.PersistentVolumeClaim != nil {
				t.Fatal("should not have PVC — builds use EmptyDir + registry cache")
			}
		}
	}
	if !found {
		t.Fatal("buildah-storage volume not found")
	}

	// Verify build command includes --cache-from with bare repo (no tag/digest).
	buildCmd := pod.Spec.Containers[0].Command[2] // sh -c "<cmd>"
	if !strings.Contains(buildCmd, "--cache-from=registry.harbor.lan/devbox") {
		t.Fatalf("expected --cache-from with bare repo in build command, got: %s", buildCmd)
	}
	if strings.Contains(buildCmd, "--cache-from=registry.harbor.lan/devbox:") {
		t.Fatalf("--cache-from must not include a tag (buildah v1.29+ rejects it), got: %s", buildCmd)
	}

	// Verify build command pushes cache tag
	if !strings.Contains(buildCmd, "buildah tag") {
		t.Fatalf("expected cache tag push in build command, got: %s", buildCmd)
	}

	// Verify bumped resources: 1 CPU request, 3 CPU limit
	container := pod.Spec.Containers[0]
	cpuReq := container.Resources.Requests["cpu"]
	if cpuReq.String() != "1" {
		t.Errorf("expected 1 CPU request, got %s", cpuReq.String())
	}
	cpuLim := container.Resources.Limits["cpu"]
	if cpuLim.String() != "3" {
		t.Errorf("expected 3 CPU limit, got %s", cpuLim.String())
	}
}

func TestImagePullPolicy(t *testing.T) {
	tests := []struct {
		imageTag string
		want     corev1.PullPolicy
	}{
		{"registry.harbor.lan/mcp/devbox/app:a3b9c1d", corev1.PullIfNotPresent},
		{"registry.harbor.lan/mcp/devbox/app:v1.2.3", corev1.PullIfNotPresent},
		{"registry.harbor.lan/mcp/devbox/app:latest", corev1.PullAlways},
		{"registry.harbor.lan/mcp/devbox/app", corev1.PullAlways},
		{"nginx:1.25", corev1.PullIfNotPresent},
		{"nginx:latest", corev1.PullAlways},
		{"nginx", corev1.PullAlways},
	}
	for _, tt := range tests {
		got := imagePullPolicy(tt.imageTag)
		if got != tt.want {
			t.Errorf("imagePullPolicy(%q) = %v, want %v", tt.imageTag, got, tt.want)
		}
	}
}

func TestBuildPodSpec_ImagePullPolicy_HashTag(t *testing.T) {
	k := testK8sBackend()
	pod := k.buildPodSpec(StartOpts{
		Name: "demo",
	}, "registry.harbor.lan/devbox:a3b9c1d")

	got := pod.Spec.Containers[0].ImagePullPolicy
	if got != corev1.PullIfNotPresent {
		t.Fatalf("expected IfNotPresent for hash-tagged image, got %v", got)
	}
}

func TestBuildPodSpec_ImagePullPolicy_Latest(t *testing.T) {
	k := testK8sBackend()
	pod := k.buildPodSpec(StartOpts{
		Name: "demo",
	}, "registry.harbor.lan/devbox:latest")

	got := pod.Spec.Containers[0].ImagePullPolicy
	if got != corev1.PullAlways {
		t.Fatalf("expected Always for :latest image, got %v", got)
	}
}

func TestGitEnabled(t *testing.T) {
	k := testK8sBackend()
	if k.gitEnabled() {
		t.Fatal("expected gitEnabled=false for default backend")
	}

	k.gitBaseURL = "https://gitlab.example.com/team"
	if k.gitEnabled() {
		t.Fatal("expected gitEnabled=false when gitSecret is empty")
	}

	k.gitSecret = "git-creds"
	if !k.gitEnabled() {
		t.Fatal("expected gitEnabled=true when both gitBaseURL and gitSecret are set")
	}
}

func TestGitCloneInitContainer(t *testing.T) {
	k := testK8sBackendGitClone()

	ic := k.gitCloneInitContainer("/workspace/services/loom-core")
	if ic.Name != "git-clone" {
		t.Fatalf("expected initContainer name 'git-clone', got %q", ic.Name)
	}
	if ic.Image != "alpine/git:latest" {
		t.Fatalf("expected alpine/git image, got %q", ic.Image)
	}

	// Should have GIT_TOKEN env from secret ref
	if len(ic.Env) != 1 || ic.Env[0].Name != "GIT_TOKEN" {
		t.Fatalf("expected GIT_TOKEN env, got: %#v", ic.Env)
	}
	if ic.Env[0].ValueFrom == nil || ic.Env[0].ValueFrom.SecretKeyRef == nil {
		t.Fatal("expected GIT_TOKEN from secretKeyRef")
	}
	if ic.Env[0].ValueFrom.SecretKeyRef.Name != "gitlab-creds" {
		t.Fatalf("expected secret name 'gitlab-creds', got %q", ic.Env[0].ValueFrom.SecretKeyRef.Name)
	}

	// Clone script should reference the correct repo URL and destination
	script := ic.Command[2]
	if !strings.Contains(script, "gitlab.blevins.dev/homelab/loom-core.git") {
		t.Fatalf("expected repo URL in clone script, got: %s", script)
	}
	if !strings.Contains(script, "/workspace/services/loom-core") {
		t.Fatalf("expected clone dest /workspace/services/loom-core, got: %s", script)
	}

	// Should mount workspace volume
	if len(ic.VolumeMounts) != 1 || ic.VolumeMounts[0].MountPath != "/workspace" {
		t.Fatalf("expected /workspace volume mount, got: %#v", ic.VolumeMounts)
	}
}

func TestBuildPodSpec_GitCloneMode(t *testing.T) {
	k := testK8sBackendGitClone()
	pod := k.buildPodSpec(StartOpts{
		Name:    "devbox-loom-core",
		WorkDir: "/workspace/services/loom-core",
		AgentID: "agent-1",
	}, "registry.harbor.lan/devbox/loom-core:abc123")

	// Should use emptyDir, not NFS PVC
	wsVol := pod.Spec.Volumes[0]
	if wsVol.Name != "workspace" {
		t.Fatalf("expected workspace volume first, got: %q", wsVol.Name)
	}
	if wsVol.EmptyDir == nil {
		t.Fatal("expected emptyDir workspace volume in git-clone mode")
	}
	if wsVol.PersistentVolumeClaim != nil {
		t.Fatal("should not have PVC in git-clone mode")
	}

	// Should have a git-clone initContainer
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("expected 1 initContainer, got %d", len(pod.Spec.InitContainers))
	}
	ic := pod.Spec.InitContainers[0]
	if ic.Name != "git-clone" {
		t.Fatalf("expected git-clone initContainer, got %q", ic.Name)
	}
}

func TestBuildPodSpec_NFSMode(t *testing.T) {
	k := testK8sBackend() // no git config → NFS mode
	pod := k.buildPodSpec(StartOpts{
		Name:    "devbox-loom-core",
		WorkDir: "/workspace/services/loom-core",
	}, "registry.harbor.lan/devbox/loom-core:abc123")

	// Should use NFS PVC
	wsVol := pod.Spec.Volumes[0]
	if wsVol.PersistentVolumeClaim == nil {
		t.Fatal("expected NFS PVC workspace volume in NFS mode")
	}
	if wsVol.PersistentVolumeClaim.ClaimName != "devbox-workspace-nfs" {
		t.Fatalf("unexpected PVC name: %q", wsVol.PersistentVolumeClaim.ClaimName)
	}

	// Should NOT have initContainers
	if len(pod.Spec.InitContainers) != 0 {
		t.Fatalf("expected 0 initContainers in NFS mode, got %d", len(pod.Spec.InitContainers))
	}
}

func TestBuildPodSpec_TarPipeMode(t *testing.T) {
	k := testK8sBackend()
	k.syncMode = "tar-pipe"
	pod := k.buildPodSpec(StartOpts{
		Name:    "devbox-loom-core",
		WorkDir: "/workspace/services/loom-core",
		AgentID: "claude-code",
	}, "registry.harbor.lan/devbox/loom-core:abc123")

	// Should use emptyDir (files come via SyncWorkspace after pod starts).
	wsVol := pod.Spec.Volumes[0]
	if wsVol.EmptyDir == nil {
		t.Fatal("expected emptyDir workspace volume in tar-pipe mode")
	}
	if wsVol.PersistentVolumeClaim != nil {
		t.Fatal("should not have PVC in tar-pipe mode")
	}

	// Should NOT have initContainers (tar-pipe syncs post-start).
	if len(pod.Spec.InitContainers) != 0 {
		t.Fatalf("expected 0 initContainers in tar-pipe mode, got %d", len(pod.Spec.InitContainers))
	}

	// Agent ID should be in labels.
	if pod.Labels["devbox/agent-id"] != "claude-code" {
		t.Fatalf("expected agent-id label, got: %v", pod.Labels)
	}
}

func TestBuildBuildahPodSpec_GitCloneMode(t *testing.T) {
	k := testK8sBackendGitClone()
	pod := k.buildBuildahPodSpec("build-pod", "registry.harbor.lan/devbox:tag", "dockerfile-cm", "/workspace/services/loom-core")

	// Should use emptyDir for workspace, not NFS PVC
	wsVol := pod.Spec.Volumes[0]
	if wsVol.EmptyDir == nil {
		t.Fatal("expected emptyDir workspace volume in git-clone mode for build pod")
	}
	if wsVol.PersistentVolumeClaim != nil {
		t.Fatal("should not have PVC in git-clone mode for build pod")
	}

	// Should have a git-clone initContainer
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("expected 1 initContainer, got %d", len(pod.Spec.InitContainers))
	}
	ic := pod.Spec.InitContainers[0]
	if ic.Name != "git-clone" {
		t.Fatalf("expected git-clone initContainer, got %q", ic.Name)
	}

	// The clone script should target the correct path derived from contextRel
	script := ic.Command[2]
	if !strings.Contains(script, "loom-core.git") {
		t.Fatalf("expected loom-core.git in clone script, got: %s", script)
	}
}

func TestBuild_MonorepoContextCompletesWithFakeK8s(t *testing.T) {
	workspaceRoot := t.TempDir()
	contextDir := filepath.Join(workspaceRoot, "services", "loom-core")
	if err := os.MkdirAll(contextDir, 0o755); err != nil {
		t.Fatalf("mkdir context dir: %v", err)
	}

	clientset := k8sfake.NewSimpleClientset()
	buildWatch := watch.NewFake()
	watchStarted := make(chan struct{})
	var createdBuildPod *corev1.Pod

	clientset.PrependWatchReactor("pods", func(action k8stesting.Action) (bool, watch.Interface, error) {
		close(watchStarted)
		return true, buildWatch, nil
	})
	clientset.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create := action.(k8stesting.CreateAction)
		pod := create.GetObject().(*corev1.Pod).DeepCopy()
		createdBuildPod = pod

		go func(name string) {
			<-watchStarted
			buildWatch.Modify(&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "devbox"},
				Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
			})
		}(pod.Name)
		return false, nil, nil
	})

	k := testK8sBackend()
	k.workspaceRoot = workspaceRoot
	k.clientset = clientset

	res, err := k.Build(context.Background(), BuildOpts{
		Tag:        "mcp/devbox/loom-core:abc1234",
		Dockerfile: []byte("FROM scratch\n"),
		ContextDir: contextDir,
	})
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if res.ImageTag != "registry.harbor.lan/mcp/devbox/loom-core:abc1234" {
		t.Fatalf("ImageTag=%q", res.ImageTag)
	}

	if createdBuildPod == nil {
		t.Fatal("expected build pod to be created")
	}
	cmd := strings.Join(createdBuildPod.Spec.Containers[0].Command, " ")
	if !strings.Contains(cmd, "/workspace/services/loom-core") {
		t.Fatalf("expected monorepo-relative context path in build command, got: %s", cmd)
	}
}
