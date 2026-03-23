package autotune

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(aiv1alpha2.AddToScheme(s))
	utilruntime.Must(appsv1.AddToScheme(s))
	return s
}

func makeModel(name, ns, backend string, cfg map[string]any) *aiv1alpha2.Model {
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: aiv1alpha2.ModelSpec{
			Backend: backend,
			Source:  "HF://test/model",
		},
	}
	if cfg != nil {
		raw, _ := json.Marshal(cfg)
		model.Spec.Config = &apiextensionsv1.JSON{Raw: raw}
	}
	return model
}

func makeDeployment(name, ns string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
		},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 1,
			Replicas:           replicas,
			UpdatedReplicas:    replicas,
			ReadyReplicas:      replicas,
			AvailableReplicas:  replicas,
		},
	}
}

func TestAutotuner_Run_RejectsSharedGPU(t *testing.T) {
	t.Parallel()

	shared := "my-group"
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{Name: "shared-model", Namespace: "test-ns"},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
			GPU:     &aiv1alpha2.GPUSpec{Shared: shared},
		},
	}

	fc := fakeclient.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(model).
		Build()

	tuner := New(Options{
		Client:     fc,
		KubeClient: fake.NewSimpleClientset(),
		ModelName:  "shared-model",
		Namespace:  "test-ns",
		BenchFn:    func(ctx context.Context) (float64, error) { return 100, nil },
	})

	err := tuner.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared GPU groups")
}

func TestAutotuner_Run_CoordinateDescent(t *testing.T) {
	t.Parallel()

	initialCfg := map[string]any{"maxNumSeqs": float64(8)}
	model := makeModel("test-model", "test-ns", "vllm", initialCfg)
	deploy := makeDeployment("test-model", "test-ns", 1)

	fc := fakeclient.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(model, deploy).
		WithStatusSubresource(model).
		Build()

	clientset := fake.NewSimpleClientset()

	// Simulate benchmark results: return higher TPS for maxNumSeqs=2.
	callCount := 0
	benchFn := func(ctx context.Context) (float64, error) {
		callCount++
		// Baseline gets 70, then subsequent calls alternate.
		if callCount == 1 {
			return 70.0, nil // Baseline
		}
		// Read the current config from the model.
		m := &aiv1alpha2.Model{}
		if err := fc.Get(ctx, ctrlclient.ObjectKey{Name: "test-model", Namespace: "test-ns"}, m); err != nil {
			return 70.0, nil
		}
		cfg := m.Spec.GetConfigMap()
		if v, ok := cfg["maxNumSeqs"]; ok {
			if f, ok := v.(float64); ok && f == 2 {
				return 80.0, nil // Better!
			}
		}
		return 65.0, nil // Worse
	}

	// Use a minimal search space for fast tests.
	space := SearchSpace{
		Parameters: []Parameter{
			{Name: "maxNumSeqs", Values: []any{float64(1), float64(2), float64(4)}},
		},
	}

	tuner := New(Options{
		Client:         fc,
		KubeClient:     clientset,
		ModelName:      "test-model",
		Namespace:      "test-ns",
		BenchFn:        benchFn,
		RolloutTimeout: 5 * time.Second,
		Space:          space,
	})

	err := tuner.Run(context.Background())
	require.NoError(t, err)

	// Verify that the best config was applied.
	finalModel := &aiv1alpha2.Model{}
	err = fc.Get(context.Background(), ctrlclient.ObjectKey{Name: "test-model", Namespace: "test-ns"}, finalModel)
	require.NoError(t, err)
	finalCfg := finalModel.Spec.GetConfigMap()
	assert.Equal(t, float64(2), finalCfg["maxNumSeqs"])

	// Verify the ConfigMap was saved.
	cm, err := clientset.CoreV1().ConfigMaps("test-ns").Get(context.Background(), "test-model-autotune-log", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Contains(t, cm.Data, "results.tsv")
	assert.Contains(t, cm.Data, "summary.json")
}

func TestAutotuner_ValidateCandidate_RejectsHighGPUMem(t *testing.T) {
	t.Parallel()

	tuner := &Autotuner{}

	rejected, reason := tuner.validateCandidate(map[string]any{
		"gpuMemoryUtilization": "0.99",
	})
	assert.True(t, rejected)
	assert.Contains(t, reason, "safety cap")

	rejected, _ = tuner.validateCandidate(map[string]any{
		"gpuMemoryUtilization": "0.95",
	})
	assert.False(t, rejected)
}

func TestAutotuner_ValidateCandidate_AcceptsNormalConfig(t *testing.T) {
	t.Parallel()

	tuner := &Autotuner{}
	rejected, _ := tuner.validateCandidate(map[string]any{
		"maxNumSeqs": float64(16),
	})
	assert.False(t, rejected)
}

func TestConfigDeltaString(t *testing.T) {
	t.Parallel()

	base := map[string]any{"a": 1, "b": "x"}
	candidate := map[string]any{"a": 2, "b": "x"}

	delta := configDeltaString(base, candidate)
	assert.Equal(t, "a=2", delta)
}

func TestConfigDeltaString_NoChange(t *testing.T) {
	t.Parallel()

	cfg := map[string]any{"a": 1}
	delta := configDeltaString(cfg, cfg)
	assert.Equal(t, "(no change)", delta)
}
