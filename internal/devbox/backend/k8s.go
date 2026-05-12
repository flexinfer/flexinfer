package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	defaultBuilderImage  = "quay.io/buildah/stable:v1.38.0"
	defaultGitCloneImage = "alpine/git:2.47.2"
)

const (
	defaultBuildCPURequest              = "1"
	defaultBuildCPULimit                = "3"
	defaultBuildMemoryRequest           = "1Gi"
	defaultBuildMemoryLimit             = "3Gi"
	defaultBuildEphemeralStorageRequest = "2Gi"
	defaultBuildEphemeralStorageLimit   = "40Gi"
	defaultMaxBuilds                    = 1
)

// K8sBackend implements Backend using a Kubernetes cluster.
// Builds are performed in-cluster via Buildah pods — no local Docker daemon required.
// Each build pod gets its own EmptyDir storage and uses registry-based layer caching
// via --cache-from, allowing parallel builds across projects.
type K8sBackend struct {
	clientset       kubernetes.Interface
	restConfig      *rest.Config
	namespace       string
	registry        string
	workspacePVC    string // PVC name for NFS workspace volume
	imagePullSecret string // image pull secret name for private registry
	workspaceRoot   string // host path to workspace (NFS export source)
	builderImage    string // Buildah builder image
	gitCloneImage   string // git image used by git-clone init containers
	nfsFlush        bool   // prepend NFS cache flush to exec commands
	gitBaseURL      string // base git URL for workspace repos (enables git-clone mode)
	gitSecret       string // secret name containing git token (key: "token")

	buildCPURequest              resource.Quantity
	buildCPULimit                resource.Quantity
	buildMemoryRequest           resource.Quantity
	buildMemoryLimit             resource.Quantity
	buildEphemeralStorageRequest resource.Quantity
	buildEphemeralStorageLimit   resource.Quantity
	buildAvoidNodes              []string
	buildSlots                   chan struct{}

	// Tar-pipe sync configuration.
	syncMode     string   // "tar-pipe", "git-clone", or "nfs"
	syncExcludes []string // additional exclude patterns for tar-pipe sync
	maxSyncSize  int64    // max uncompressed tar size in bytes (0 = default 200MB)
}

// K8sBackendConfig holds configuration for the K8s backend.
type K8sBackendConfig struct {
	Kubeconfig                   string // path to kubeconfig file
	Namespace                    string // namespace for sandbox pods (default: "devbox")
	Registry                     string // image registry (e.g., "registry.harbor.lan")
	StorageClass                 string // storage class for PVCs (default: "longhorn")
	WorkspacePVC                 string // PVC name for NFS workspace (default: "devbox-workspace-nfs")
	ImagePullSecret              string // image pull secret name (default: "harbor-creds")
	WorkspaceRoot                string // host path to workspace (for NFS-relative path computation)
	BuilderImage                 string // Buildah builder image (default: quay.io/buildah/stable:v1.38.0)
	GitCloneImage                string // git image for git-clone init containers (default: alpine/git:2.47.2)
	NFSFlush                     bool   // prepend NFS attr cache flush to exec commands (default: true for K8s)
	GitBaseURL                   string // base git URL for repos (e.g., "https://gitlab.blevins.dev/homelab"); enables git-clone mode
	GitSecret                    string // secret name containing git token (key: "token"); required when GitBaseURL is set
	BuildCPURequest              string // Buildah pod CPU request as Kubernetes quantity (default: "1")
	BuildCPULimit                string // Buildah pod CPU limit as Kubernetes quantity (default: "3")
	BuildMemoryRequest           string // Buildah pod memory request as Kubernetes quantity (default: "1Gi")
	BuildMemoryLimit             string // Buildah pod memory limit as Kubernetes quantity (default: "3Gi")
	BuildEphemeralStorageRequest string // Buildah pod ephemeral-storage request (default: "2Gi")
	BuildEphemeralStorageLimit   string // Buildah pod ephemeral-storage limit (default: "40Gi")
	BuildAvoidNodes              string // comma-separated node names to avoid for Buildah pods
	MaxConcurrentBuilds          int    // max concurrent Buildah pods per backend process (default: 1)

	// Tar-pipe sync: stream local files into pods via SPDY exec.
	SyncMode     string   // "tar-pipe" (default local), "git-clone", "nfs"
	SyncExcludes []string // additional exclude patterns for tar-pipe sync
	MaxSyncSize  int64    // max uncompressed tar bytes (default: 200MB)
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
	if cfg.GitCloneImage == "" {
		cfg.GitCloneImage = defaultGitCloneImage
	}
	if cfg.WorkspaceRoot == "" {
		home, _ := os.UserHomeDir()
		cfg.WorkspaceRoot = filepath.Join(home, "workspace")
	}
	buildCPURequest, err := parseBuildQuantity("build CPU request", cfg.BuildCPURequest, defaultBuildCPURequest)
	if err != nil {
		return nil, err
	}
	buildCPULimit, err := parseBuildQuantity("build CPU limit", cfg.BuildCPULimit, defaultBuildCPULimit)
	if err != nil {
		return nil, err
	}
	buildMemoryRequest, err := parseBuildQuantity("build memory request", cfg.BuildMemoryRequest, defaultBuildMemoryRequest)
	if err != nil {
		return nil, err
	}
	buildMemoryLimit, err := parseBuildQuantity("build memory limit", cfg.BuildMemoryLimit, defaultBuildMemoryLimit)
	if err != nil {
		return nil, err
	}
	buildEphemeralStorageRequest, err := parseBuildQuantity("build ephemeral-storage request", cfg.BuildEphemeralStorageRequest, defaultBuildEphemeralStorageRequest)
	if err != nil {
		return nil, err
	}
	buildEphemeralStorageLimit, err := parseBuildQuantity("build ephemeral-storage limit", cfg.BuildEphemeralStorageLimit, defaultBuildEphemeralStorageLimit)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConcurrentBuilds <= 0 {
		cfg.MaxConcurrentBuilds = defaultMaxBuilds
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
		clientset:                    clientset,
		restConfig:                   restConfig,
		namespace:                    cfg.Namespace,
		registry:                     cfg.Registry,
		workspacePVC:                 cfg.WorkspacePVC,
		imagePullSecret:              cfg.ImagePullSecret,
		workspaceRoot:                cfg.WorkspaceRoot,
		builderImage:                 cfg.BuilderImage,
		gitCloneImage:                cfg.GitCloneImage,
		nfsFlush:                     cfg.NFSFlush,
		gitBaseURL:                   cfg.GitBaseURL,
		gitSecret:                    cfg.GitSecret,
		buildCPURequest:              buildCPURequest,
		buildCPULimit:                buildCPULimit,
		buildMemoryRequest:           buildMemoryRequest,
		buildMemoryLimit:             buildMemoryLimit,
		buildEphemeralStorageRequest: buildEphemeralStorageRequest,
		buildEphemeralStorageLimit:   buildEphemeralStorageLimit,
		buildAvoidNodes:              splitCSV(cfg.BuildAvoidNodes),
		buildSlots:                   make(chan struct{}, cfg.MaxConcurrentBuilds),
		syncMode:                     cfg.SyncMode,
		syncExcludes:                 cfg.SyncExcludes,
		maxSyncSize:                  cfg.MaxSyncSize,
	}, nil
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseBuildQuantity(name, value, fallback string) (resource.Quantity, error) {
	if value == "" {
		value = fallback
	}
	q, err := resource.ParseQuantity(value)
	if err != nil {
		return resource.Quantity{}, fmt.Errorf("invalid %s %q: %w", name, value, err)
	}
	return q, nil
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

// Clientset returns the underlying Kubernetes clientset. This allows callers
// (e.g., the spawn controller) to share the same authenticated client for
// direct pod queries without creating a separate kubeconfig connection.
func (k *K8sBackend) Clientset() kubernetes.Interface {
	return k.clientset
}

// Namespace returns the K8s namespace this backend targets.
func (k *K8sBackend) Namespace() string {
	return k.namespace
}

// RestConfig returns the Kubernetes REST config for StreamExec callers.
func (k *K8sBackend) RestConfig() *rest.Config {
	return k.restConfig
}

// NFSFlush returns whether NFS cache flush is enabled.
func (k *K8sBackend) NFSFlush() bool {
	return k.nfsFlush
}

func (k *K8sBackend) Health(ctx context.Context) error {
	_, err := k.clientset.CoreV1().Namespaces().Get(ctx, k.namespace, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("namespace %q not accessible: %w", k.namespace, err)
	}
	return nil
}

// buildNameRe matches characters unsafe for K8s resource names.
var buildNameRe = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// Ensure K8sBackend implements Backend at compile time.
var _ Backend = (*K8sBackend)(nil)
