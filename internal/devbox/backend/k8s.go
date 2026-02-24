package backend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
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
	buildCachePVC   string // PVC name for persistent Buildah layer cache (empty = EmptyDir)
	buildMu         sync.Mutex
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
	BuildCachePVC   string // PVC name for persistent Buildah cache (empty = EmptyDir fallback)
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
		buildCachePVC:   cfg.BuildCachePVC,
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

// buildNameRe matches characters unsafe for K8s resource names.
var buildNameRe = regexp.MustCompile(`[^a-zA-Z0-9-]`)

// Ensure K8sBackend implements Backend at compile time.
var _ Backend = (*K8sBackend)(nil)
