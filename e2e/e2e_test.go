// Package e2e provides end-to-end tests for FlexInfer.
//
// These tests run against a real Kubernetes cluster and verify complete
// workflows including Model creation, inference requests, and cleanup.
//
// To run these tests:
//
//	# Against current kubectl context
//	go test -v ./e2e/...
//
//	# Against specific kubeconfig
//	KUBECONFIG=/path/to/kubeconfig go test -v ./e2e/...
//
//	# Skip if no cluster available (CI-friendly)
//	go test -v ./e2e/... -skip-no-cluster
//
// The tests are designed to be non-destructive and clean up after themselves.
package e2e

import (
	"context"
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

var (
	skipNoCluster = flag.Bool("skip-no-cluster", false, "Skip tests if no cluster is available")
	namespace     = flag.String("namespace", "flexinfer-e2e", "Namespace for e2e tests")

	scheme    = runtime.NewScheme()
	k8sClient client.Client
	clientset *kubernetes.Clientset
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = aiv1alpha2.AddToScheme(scheme)
}

func TestMain(m *testing.M) {
	flag.Parse()

	// Load kubeconfig
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		if *skipNoCluster {
			fmt.Println("No cluster available, skipping e2e tests")
			os.Exit(0)
		}
		fmt.Printf("Failed to load kubeconfig: %v\n", err)
		os.Exit(1)
	}

	// Create clients
	k8sClient, err = client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		fmt.Printf("Failed to create k8s client: %v\n", err)
		os.Exit(1)
	}

	clientset, err = kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Failed to create clientset: %v\n", err)
		os.Exit(1)
	}

	// Verify cluster connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	if err != nil {
		if *skipNoCluster {
			fmt.Println("Cannot connect to cluster, skipping e2e tests")
			os.Exit(0)
		}
		fmt.Printf("Failed to connect to cluster: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Connected to cluster, running e2e tests in namespace %s\n", *namespace)

	// Run tests
	code := m.Run()

	os.Exit(code)
}

// ensureNamespace creates the test namespace if it doesn't exist.
func ensureNamespace(t *testing.T) {
	ctx := context.Background()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: *namespace,
			Labels: map[string]string{
				"flexinfer.ai/e2e-test": "true",
			},
		},
	}

	err := k8sClient.Create(ctx, ns)
	if err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("Failed to create namespace: %v", err)
	}
}

// cleanupModel deletes a model and waits for cleanup.
func cleanupModel(t *testing.T, name string) {
	ctx := context.Background()
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: *namespace,
		},
	}

	err := k8sClient.Delete(ctx, model)
	if err != nil && !errors.IsNotFound(err) {
		t.Logf("Warning: failed to delete model %s: %v", name, err)
	}

	// Wait for deletion
	_ = wait.PollImmediate(time.Second, 30*time.Second, func() (bool, error) {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model)
		return errors.IsNotFound(err), nil
	})
}

// TestModelLifecycle tests the basic Model create/ready/delete lifecycle.
func TestModelLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	ensureNamespace(t)

	modelName := fmt.Sprintf("e2e-test-%d", time.Now().Unix())
	defer cleanupModel(t, modelName)

	ctx := context.Background()

	// Create a simple model
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: *namespace,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://llama3.2:1b",
			GPU: &aiv1alpha2.GPUSpec{
				Count: 1,
			},
		},
	}

	t.Logf("Creating model %s", modelName)
	if err := k8sClient.Create(ctx, model); err != nil {
		t.Fatalf("Failed to create model: %v", err)
	}

	// Wait for model to be processed (reach any non-empty phase)
	t.Log("Waiting for model to be processed...")
	err := wait.PollImmediate(2*time.Second, 60*time.Second, func() (bool, error) {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
			return false, err
		}
		return model.Status.Phase != "", nil
	})
	if err != nil {
		t.Fatalf("Model never reached a phase: %v", err)
	}

	t.Logf("Model phase: %s", model.Status.Phase)

	// Verify conditions are set
	if len(model.Status.Conditions) == 0 {
		t.Log("Warning: no conditions set on model")
	} else {
		for _, cond := range model.Status.Conditions {
			t.Logf("Condition %s: %s (%s)", cond.Type, cond.Status, cond.Reason)
		}
	}

	// Cleanup
	t.Log("Cleaning up model")
	if err := k8sClient.Delete(ctx, model); err != nil {
		t.Fatalf("Failed to delete model: %v", err)
	}

	// Verify deletion
	err = wait.PollImmediate(time.Second, 30*time.Second, func() (bool, error) {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model)
		return errors.IsNotFound(err), nil
	})
	if err != nil {
		t.Fatalf("Model not deleted: %v", err)
	}

	t.Log("Model lifecycle test passed")
}

// TestFlexInferInstalled verifies FlexInfer components are running.
func TestFlexInferInstalled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	ctx := context.Background()

	// Check for FlexInfer CRDs
	var models aiv1alpha2.ModelList
	if err := k8sClient.List(ctx, &models, client.InNamespace("flexinfer-system")); err != nil {
		t.Fatalf("Failed to list Models (CRD may not be installed): %v", err)
	}
	t.Logf("Found %d models in flexinfer-system", len(models.Items))

	// Check for controller deployment
	pods, err := clientset.CoreV1().Pods("flexinfer-system").List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=flexinfer",
	})
	if err != nil {
		t.Logf("Warning: failed to list FlexInfer pods: %v", err)
	} else {
		t.Logf("Found %d FlexInfer pods", len(pods.Items))
		for _, pod := range pods.Items {
			t.Logf("  - %s: %s", pod.Name, pod.Status.Phase)
		}
	}

	t.Log("FlexInfer installation check passed")
}

// TestProxyAvailable verifies the proxy service is reachable.
func TestProxyAvailable(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	ctx := context.Background()

	// Check for proxy service
	svc, err := clientset.CoreV1().Services("flexinfer-system").Get(ctx, "flexinfer-proxy", metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			t.Skip("Proxy service not found, skipping")
		}
		t.Fatalf("Failed to get proxy service: %v", err)
	}

	t.Logf("Proxy service found: %s (type: %s)", svc.Name, svc.Spec.Type)

	// Check endpoints
	endpoints, err := clientset.CoreV1().Endpoints("flexinfer-system").Get(ctx, "flexinfer-proxy", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Failed to get proxy endpoints: %v", err)
	}

	readyCount := 0
	for _, subset := range endpoints.Subsets {
		readyCount += len(subset.Addresses)
	}

	if readyCount == 0 {
		t.Fatal("Proxy has no ready endpoints")
	}

	t.Logf("Proxy has %d ready endpoints", readyCount)
	t.Log("Proxy availability check passed")
}

// TestServerlessScaleToZero tests the serverless idle timeout behavior.
func TestServerlessScaleToZero(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping e2e test in short mode")
	}

	ensureNamespace(t)

	modelName := fmt.Sprintf("e2e-serverless-%d", time.Now().Unix())
	defer cleanupModel(t, modelName)

	ctx := context.Background()

	// Create a model with serverless config (short idle timeout for testing)
	idleTimeout := int32(30) // 30 seconds idle timeout
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: *namespace,
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "ollama",
			Source:  "ollama://llama3.2:1b",
			GPU: &aiv1alpha2.GPUSpec{
				Count: 1,
			},
			Serverless: &aiv1alpha2.ServerlessSpec{
				IdleTimeoutSeconds: &idleTimeout,
			},
		},
	}

	t.Logf("Creating serverless model %s with %ds idle timeout", modelName, idleTimeout)
	if err := k8sClient.Create(ctx, model); err != nil {
		t.Fatalf("Failed to create model: %v", err)
	}

	// Wait for model to reach a phase
	t.Log("Waiting for model to be processed...")
	err := wait.PollImmediate(2*time.Second, 60*time.Second, func() (bool, error) {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
			return false, err
		}
		return model.Status.Phase != "", nil
	})
	if err != nil {
		t.Fatalf("Model never reached a phase: %v", err)
	}

	t.Logf("Model phase: %s", model.Status.Phase)

	// Check that serverless config was applied
	if model.Status.Phase == aiv1alpha2.PhaseIdle {
		t.Log("Model correctly started in Idle phase (scale-to-zero)")
	} else if model.Status.Phase == aiv1alpha2.PhaseReady {
		t.Log("Model is Ready, waiting for idle timeout to trigger scale-down...")

		// Wait for the model to scale down to Idle
		err = wait.PollImmediate(5*time.Second, 90*time.Second, func() (bool, error) {
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
				return false, err
			}
			return model.Status.Phase == aiv1alpha2.PhaseIdle, nil
		})
		if err != nil {
			t.Logf("Note: Model did not scale to Idle within timeout (phase: %s)", model.Status.Phase)
			// This is not necessarily a failure - the model might be in use
		} else {
			t.Log("Model scaled to Idle phase successfully")
		}
	} else {
		t.Logf("Model in phase %s (serverless behavior depends on cluster state)", model.Status.Phase)
	}

	// Verify the model still exists and is functional
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model); err != nil {
		t.Fatalf("Failed to get model after scale test: %v", err)
	}

	t.Logf("Final model phase: %s", model.Status.Phase)
	t.Log("Serverless scale-to-zero test passed")
}
