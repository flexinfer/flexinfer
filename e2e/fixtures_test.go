// Package e2e provides test fixture helpers for end-to-end tests.
package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// TestTimeouts holds configurable timeout values for E2E tests.
// These can be overridden via environment variables for different cluster environments.
type TestTimeouts struct {
	// ModelReady is the maximum time to wait for a model to become ready
	ModelReady time.Duration
	// ModelDelete is the maximum time to wait for a model to be deleted
	ModelDelete time.Duration
	// ColdStart is the maximum time to wait for a cold start to complete
	ColdStart time.Duration
	// InferenceRequest is the maximum time for an inference request to complete
	InferenceRequest time.Duration
	// GPUSwap is the maximum time to wait for a GPUGroup swap to complete
	GPUSwap time.Duration
	// PollInterval is the interval between status checks
	PollInterval time.Duration
}

// DefaultTimeouts returns the default timeout configuration.
// These can be overridden by setting environment variables.
func DefaultTimeouts() TestTimeouts {
	return TestTimeouts{
		ModelReady:       getEnvDuration("E2E_TIMEOUT_MODEL_READY", 5*time.Minute),
		ModelDelete:      getEnvDuration("E2E_TIMEOUT_MODEL_DELETE", 60*time.Second),
		ColdStart:        getEnvDuration("E2E_TIMEOUT_COLD_START", 3*time.Minute),
		InferenceRequest: getEnvDuration("E2E_TIMEOUT_INFERENCE", 2*time.Minute),
		GPUSwap:          getEnvDuration("E2E_TIMEOUT_GPU_SWAP", 2*time.Minute),
		PollInterval:     getEnvDuration("E2E_POLL_INTERVAL", 2*time.Second),
	}
}

// getEnvDuration reads a duration from an environment variable.
func getEnvDuration(name string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(name); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

// getEnvInt reads an integer from an environment variable.
func getEnvInt(name string, defaultVal int) int {
	if val := os.Getenv(name); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

var timeouts = DefaultTimeouts()

// Fixture manages test resources and ensures cleanup.
type Fixture struct {
	t        *testing.T
	name     string
	mu       sync.Mutex
	models   []*aiv1alpha2.Model
	v1Models []*aiv1alpha1.ModelDeployment
	caches   []*aiv1alpha1.ModelCache
	groups   []*aiv1alpha1.GPUGroup
}

// NewFixture creates a new test fixture with cleanup registration.
func NewFixture(t *testing.T) *Fixture {
	t.Helper()
	f := &Fixture{
		t:    t,
		name: t.Name(),
	}
	t.Cleanup(f.Cleanup)
	return f
}

// Cleanup removes all resources created by this fixture.
// This is automatically called via t.Cleanup() but can be called manually for early cleanup.
func (f *Fixture) Cleanup() {
	f.mu.Lock()
	defer f.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Delete in reverse order of creation (dependencies first)
	for i := len(f.models) - 1; i >= 0; i-- {
		model := f.models[i]
		f.t.Logf("Cleaning up Model %s/%s", model.Namespace, model.Name)
		if err := k8sClient.Delete(ctx, model); err != nil && !errors.IsNotFound(err) {
			f.t.Logf("Warning: failed to delete Model %s: %v", model.Name, err)
		}
	}

	for i := len(f.v1Models) - 1; i >= 0; i-- {
		md := f.v1Models[i]
		f.t.Logf("Cleaning up ModelDeployment %s/%s", md.Namespace, md.Name)
		if err := k8sClient.Delete(ctx, md); err != nil && !errors.IsNotFound(err) {
			f.t.Logf("Warning: failed to delete ModelDeployment %s: %v", md.Name, err)
		}
	}

	for i := len(f.groups) - 1; i >= 0; i-- {
		gg := f.groups[i]
		f.t.Logf("Cleaning up GPUGroup %s/%s", gg.Namespace, gg.Name)
		if err := k8sClient.Delete(ctx, gg); err != nil && !errors.IsNotFound(err) {
			f.t.Logf("Warning: failed to delete GPUGroup %s: %v", gg.Name, err)
		}
	}

	for i := len(f.caches) - 1; i >= 0; i-- {
		mc := f.caches[i]
		f.t.Logf("Cleaning up ModelCache %s/%s", mc.Namespace, mc.Name)
		if err := k8sClient.Delete(ctx, mc); err != nil && !errors.IsNotFound(err) {
			f.t.Logf("Warning: failed to delete ModelCache %s: %v", mc.Name, err)
		}
	}

	// Wait for all Models to be deleted
	for _, model := range f.models {
		_ = f.waitForModelDeleted(ctx, model.Name, model.Namespace)
	}

	f.models = nil
	f.v1Models = nil
	f.caches = nil
	f.groups = nil
}

// CreateModel creates a v1alpha2 Model and registers it for cleanup.
func (f *Fixture) CreateModel(ctx context.Context, model *aiv1alpha2.Model) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if model.Namespace == "" {
		model.Namespace = *namespace
	}

	if err := k8sClient.Create(ctx, model); err != nil {
		return fmt.Errorf("failed to create Model %s: %w", model.Name, err)
	}

	f.models = append(f.models, model)
	f.t.Logf("Created Model %s/%s", model.Namespace, model.Name)
	return nil
}

// CreateModelDeployment creates a v1alpha1 ModelDeployment and registers it for cleanup.
func (f *Fixture) CreateModelDeployment(ctx context.Context, md *aiv1alpha1.ModelDeployment) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if md.Namespace == "" {
		md.Namespace = *namespace
	}

	if err := k8sClient.Create(ctx, md); err != nil {
		return fmt.Errorf("failed to create ModelDeployment %s: %w", md.Name, err)
	}

	f.v1Models = append(f.v1Models, md)
	f.t.Logf("Created ModelDeployment %s/%s", md.Namespace, md.Name)
	return nil
}

// CreateModelCache creates a ModelCache and registers it for cleanup.
func (f *Fixture) CreateModelCache(ctx context.Context, mc *aiv1alpha1.ModelCache) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if mc.Namespace == "" {
		mc.Namespace = *namespace
	}

	if err := k8sClient.Create(ctx, mc); err != nil {
		return fmt.Errorf("failed to create ModelCache %s: %w", mc.Name, err)
	}

	f.caches = append(f.caches, mc)
	f.t.Logf("Created ModelCache %s/%s", mc.Namespace, mc.Name)
	return nil
}

// CreateGPUGroup creates a GPUGroup and registers it for cleanup.
func (f *Fixture) CreateGPUGroup(ctx context.Context, gg *aiv1alpha1.GPUGroup) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if gg.Namespace == "" {
		gg.Namespace = *namespace
	}

	if err := k8sClient.Create(ctx, gg); err != nil {
		return fmt.Errorf("failed to create GPUGroup %s: %w", gg.Name, err)
	}

	f.groups = append(f.groups, gg)
	f.t.Logf("Created GPUGroup %s/%s", gg.Namespace, gg.Name)
	return nil
}

// WaitForModelReady waits for a Model to reach the Ready phase.
func (f *Fixture) WaitForModelReady(ctx context.Context, name, ns string) (*aiv1alpha2.Model, error) {
	var model aiv1alpha2.Model
	err := wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelReady, true,
		func(ctx context.Context) (bool, error) {
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &model); err != nil {
				return false, err
			}
			return model.Status.Phase == aiv1alpha2.ModelPhaseReady, nil
		})
	if err != nil {
		return nil, fmt.Errorf("model %s/%s did not become ready (phase: %s): %w", ns, name, model.Status.Phase, err)
	}
	return &model, nil
}

// WaitForModelPhase waits for a Model to reach a specific phase.
func (f *Fixture) WaitForModelPhase(ctx context.Context, name, ns string, phase aiv1alpha2.ModelPhase) (*aiv1alpha2.Model, error) {
	var model aiv1alpha2.Model
	err := wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelReady, true,
		func(ctx context.Context) (bool, error) {
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &model); err != nil {
				return false, err
			}
			return model.Status.Phase == phase, nil
		})
	if err != nil {
		return nil, fmt.Errorf("model %s/%s did not reach phase %s (current: %s): %w",
			ns, name, phase, model.Status.Phase, err)
	}
	return &model, nil
}

// waitForModelDeleted waits for a Model to be fully deleted.
func (f *Fixture) waitForModelDeleted(ctx context.Context, name, ns string) error {
	return wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelDelete, true,
		func(ctx context.Context) (bool, error) {
			var model aiv1alpha2.Model
			err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &model)
			return errors.IsNotFound(err), nil
		})
}

// WaitForModelCacheReady waits for a ModelCache to reach the Ready phase.
func (f *Fixture) WaitForModelCacheReady(ctx context.Context, name, ns string) (*aiv1alpha1.ModelCache, error) {
	var cache aiv1alpha1.ModelCache
	err := wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelReady, true,
		func(ctx context.Context) (bool, error) {
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &cache); err != nil {
				return false, err
			}
			return cache.Status.Phase == aiv1alpha1.ModelCachePhaseReady, nil
		})
	if err != nil {
		return nil, fmt.Errorf("modelcache %s/%s did not become ready (phase: %s): %w",
			ns, name, cache.Status.Phase, err)
	}
	return &cache, nil
}

// WaitForGPUGroupActiveModel waits for a GPUGroup to have a specific active model.
func (f *Fixture) WaitForGPUGroupActiveModel(ctx context.Context, name, ns, expectedModel string) (*aiv1alpha1.GPUGroup, error) {
	var gg aiv1alpha1.GPUGroup
	err := wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.GPUSwap, true,
		func(ctx context.Context) (bool, error) {
			if err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &gg); err != nil {
				return false, err
			}
			return gg.Status.ActiveModel == expectedModel, nil
		})
	if err != nil {
		return nil, fmt.Errorf("gpugroup %s/%s did not activate model %s (current: %s): %w",
			ns, name, expectedModel, gg.Status.ActiveModel, err)
	}
	return &gg, nil
}

// GetModel retrieves a Model by name.
func (f *Fixture) GetModel(ctx context.Context, name, ns string) (*aiv1alpha2.Model, error) {
	var model aiv1alpha2.Model
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &model); err != nil {
		return nil, err
	}
	return &model, nil
}

// GetModelDeployment retrieves a ModelDeployment by name.
func (f *Fixture) GetModelDeployment(ctx context.Context, name, ns string) (*aiv1alpha1.ModelDeployment, error) {
	var md aiv1alpha1.ModelDeployment
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, &md); err != nil {
		return nil, err
	}
	return &md, nil
}

// GetPodsByLabel retrieves pods matching a label selector.
func (f *Fixture) GetPodsByLabel(ctx context.Context, ns, labelSelector string) ([]corev1.Pod, error) {
	pods, err := clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}
	return pods.Items, nil
}

// WaitForPodsReady waits for all pods matching a selector to be ready.
func (f *Fixture) WaitForPodsReady(ctx context.Context, ns, labelSelector string, minCount int) error {
	return wait.PollUntilContextTimeout(ctx, timeouts.PollInterval, timeouts.ModelReady, true,
		func(ctx context.Context) (bool, error) {
			pods, err := f.GetPodsByLabel(ctx, ns, labelSelector)
			if err != nil {
				return false, err
			}

			readyCount := 0
			for _, pod := range pods {
				if pod.Status.Phase == corev1.PodRunning {
					ready := true
					for _, cond := range pod.Status.Conditions {
						if cond.Type == corev1.PodReady && cond.Status != corev1.ConditionTrue {
							ready = false
							break
						}
					}
					if ready {
						readyCount++
					}
				}
			}

			return readyCount >= minCount, nil
		})
}

// UniqueModelName generates a unique model name for a test.
func (f *Fixture) UniqueModelName(prefix string) string {
	return fmt.Sprintf("%s-%s-%d", prefix, f.name, time.Now().UnixNano()%1000000)
}

// OllamaModel creates a basic Ollama model spec for testing.
func OllamaModel(name, modelID string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  fmt.Sprintf("ollama://%s", modelID),
			GPU: &aiv1alpha2.GPUSpec{
				Count: int32Ptr(1),
			},
		},
	}
}

// OllamaModelCPU creates a CPU-only Ollama model spec for testing.
func OllamaModelCPU(name, modelID string) *aiv1alpha2.Model {
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  fmt.Sprintf("ollama://%s", modelID),
			// No GPU spec means CPU-only
		},
	}
}

// ServerlessModel creates a model with serverless configuration.
func ServerlessModel(name, backend, source string, idleTimeout time.Duration) *aiv1alpha2.Model {
	timeout := metav1.Duration{Duration: idleTimeout}
	return &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: backend,
			Source:  source,
			GPU: &aiv1alpha2.GPUSpec{
				Count: int32Ptr(1),
			},
			Serverless: &aiv1alpha2.ServerlessSpec{
				IdleTimeout: &timeout,
			},
		},
	}
}

// RequireGPU skips the test if no GPU is available in the cluster.
func RequireGPU(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Failed to list nodes: %v", err)
	}

	for _, node := range nodes.Items {
		if _, hasNvidia := node.Status.Capacity["nvidia.com/gpu"]; hasNvidia {
			return
		}
		if _, hasAMD := node.Status.Capacity["amd.com/gpu"]; hasAMD {
			return
		}
	}

	t.Skip("No GPU available in cluster, skipping test")
}

// RequireFlexInfer skips the test if FlexInfer is not installed.
func RequireFlexInfer(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Check for FlexInfer controller
	pods, err := clientset.CoreV1().Pods("flexinfer-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=flexinfer",
	})
	if err != nil {
		t.Fatalf("Failed to list FlexInfer pods: %v", err)
	}

	if len(pods.Items) == 0 {
		t.Skip("FlexInfer not installed, skipping test")
	}

	// Check at least one pod is running
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			return
		}
	}

	t.Skip("FlexInfer pods not running, skipping test")
}
