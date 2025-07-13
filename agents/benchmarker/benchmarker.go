// Package benchmarker implements the logic for running benchmarks and reporting results.
package benchmarker

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Benchmarker runs benchmarks for a model on a specific device.
type Benchmarker struct {
	kubeClient kubernetes.Interface
	namespace  string
}

// NewBenchmarker creates a new Benchmarker.
func NewBenchmarker() (*Benchmarker, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes clientset: %w", err)
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		return nil, fmt.Errorf("POD_NAMESPACE environment variable not set")
	}

	return &Benchmarker{
		kubeClient: clientset,
		namespace:  namespace,
	}, nil
}

// Run executes the benchmark and stores the result in a ConfigMap.
func (b *Benchmarker) Run(ctx context.Context, model, configMapName string) error {
	log := log.FromContext(ctx)
	log.Info("Running benchmark", "model", model)

	tokensPerSecond, err := b.runBenchmarkSimulation(ctx)
	if err != nil {
		return fmt.Errorf("benchmark simulation failed: %w", err)
	}

	log.Info("Benchmark result", "tokensPerSecond", tokensPerSecond)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: b.namespace,
		},
		Data: map[string]string{
			"tokensPerSecond": strconv.FormatFloat(tokensPerSecond, 'f', -1, 64),
			"model":           model,
			"timestamp":       time.Now().Format(time.RFC3339),
		},
	}

	log.Info("Creating ConfigMap with benchmark results", "configMap", configMapName)
	_, err = b.kubeClient.CoreV1().ConfigMaps(b.namespace).Create(ctx, cm, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create benchmark result configmap: %w", err)
	}

	return nil
}

// runBenchmarkSimulation simulates a benchmark run.
func (b *Benchmarker) runBenchmarkSimulation(ctx context.Context) (float64, error) {
	log := log.FromContext(ctx)
	log.Info("Simulating benchmark...")
	// Placeholder: In a real implementation, this would involve loading the model
	// and running actual inference to measure tokens per second.
	time.Sleep(2 * time.Second) // Simulate work
	tokensPerSecond := 150.75   // Placeholder value
	return tokensPerSecond, nil
}
