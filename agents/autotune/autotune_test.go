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

// TestAutotuner_Run_QualityVeto proves the Goodhart guard flips the outcome:
// maxNumSeqs=2 wins on aggregate TPS (90 > 70) but craters the long-form "novel"
// class (72 -> 38 tok/s, the n-gram-SD pattern from the 2026-06-26 kill-test).
// Without a QualityFn the config is accepted; with one it is vetoed and baseline
// is preserved.
func TestAutotuner_Run_QualityVeto(t *testing.T) {
	t.Parallel()

	run := func(withGuard bool) (map[string]any, string) {
		initialCfg := map[string]any{"maxNumSeqs": float64(8)}
		model := makeModel("test-model", "test-ns", "vllm", initialCfg)
		deploy := makeDeployment("test-model", "test-ns", 1)
		fc := fakeclient.NewClientBuilder().
			WithScheme(testScheme()).
			WithObjects(model, deploy).
			WithStatusSubresource(model).
			Build()
		clientset := fake.NewSimpleClientset()

		isCandidate2 := func(ctx context.Context) bool {
			m := &aiv1alpha2.Model{}
			if err := fc.Get(ctx, ctrlclient.ObjectKey{Name: "test-model", Namespace: "test-ns"}, m); err != nil {
				return false
			}
			if v, ok := m.Spec.GetConfigMap()["maxNumSeqs"]; ok {
				if f, ok := v.(float64); ok && f == 2 {
					return true
				}
			}
			return false
		}

		baseline := true
		benchFn := func(ctx context.Context) (float64, error) {
			if baseline {
				baseline = false
				return 70.0, nil // baseline aggregate TPS
			}
			if isCandidate2(ctx) {
				return 90.0, nil // higher aggregate -> tempting to accept
			}
			return 65.0, nil // other candidates lose on TPS
		}

		opts := Options{
			Client:         fc,
			KubeClient:     clientset,
			ModelName:      "test-model",
			Namespace:      "test-ns",
			BenchFn:        benchFn,
			RolloutTimeout: 5 * time.Second,
			Space:          SearchSpace{Parameters: []Parameter{{Name: "maxNumSeqs", Values: []any{float64(1), float64(2)}}}},
		}
		if withGuard {
			opts.QualityFn = func(ctx context.Context) (map[string]float64, error) {
				if isCandidate2(ctx) {
					return map[string]float64{"lookup": 140, "novel": 38}, nil // long-form regressed
				}
				return map[string]float64{"lookup": 67, "novel": 72}, nil
			}
		}

		tuner := New(opts)
		require.NoError(t, tuner.Run(context.Background()))

		finalModel := &aiv1alpha2.Model{}
		require.NoError(t, fc.Get(context.Background(), ctrlclient.ObjectKey{Name: "test-model", Namespace: "test-ns"}, finalModel))
		cm, err := clientset.CoreV1().ConfigMaps("test-ns").Get(context.Background(), "test-model-autotune-log", metav1.GetOptions{})
		require.NoError(t, err)
		return finalModel.Spec.GetConfigMap(), cm.Data["results.tsv"]
	}

	// Without the guard the throughput-gaming config is accepted.
	noGuardCfg, noGuardTSV := run(false)
	assert.Equal(t, float64(2), noGuardCfg["maxNumSeqs"], "without guard, the higher-TPS config is accepted")
	assert.Contains(t, noGuardTSV, "accepted")

	// With the guard the same config is vetoed and baseline (maxNumSeqs=8) is kept.
	guardCfg, guardTSV := run(true)
	assert.Equal(t, float64(8), guardCfg["maxNumSeqs"], "guard must veto the long-form-regressing config")
	assert.Contains(t, guardTSV, "quality_vetoed")
	assert.NotContains(t, guardTSV, "\taccepted\t")
}

// TestAutotuner_ApplyConfig_SerializesSpeculativeConfig proves applyConfig writes
// the string-valued speculativeConfig parameter onto Model.spec.config as an
// opaque JSON string (not a nested object), round-tripping through the same
// ConfigString accessor the vLLM backend uses to build --speculative-config.
func TestAutotuner_ApplyConfig_SerializesSpeculativeConfig(t *testing.T) {
	t.Parallel()

	model := makeModel("spec-model", "test-ns", "vllm", map[string]any{"maxNumSeqs": float64(8)})
	fc := fakeclient.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(model).
		Build()

	tuner := &Autotuner{
		client:    fc,
		modelName: "spec-model",
		namespace: "test-ns",
	}

	cfg := map[string]any{
		"maxNumSeqs":             float64(8),
		SpeculativeDecodingParam: NgramSpeculativeConfigJSON,
	}
	require.NoError(t, tuner.applyConfig(context.Background(), cfg))

	got := &aiv1alpha2.Model{}
	require.NoError(t, fc.Get(context.Background(), ctrlclient.ObjectKey{Name: "spec-model", Namespace: "test-ns"}, got))

	// Stored as a string and read back identically by ConfigString (what the
	// backend uses), i.e. it is opaque JSON, not a decoded nested object.
	assert.Equal(t, NgramSpeculativeConfigJSON, got.Spec.GetConfigMap()[SpeculativeDecodingParam])
	assert.Equal(t, NgramSpeculativeConfigJSON, got.Spec.ConfigString(SpeculativeDecodingParam, ""))

	// The raw spec.config JSON carries the inner JSON as an escaped string value,
	// confirming we did not flatten it into a nested object.
	assert.Contains(t, string(got.Spec.Config.Raw), `"speculativeConfig":"{\"method\":\"ngram\"`)
}

// TestAutotuner_ApplyConfig_SpeculativeDecodingOff proves the "off" value is an
// empty string, which the backend treats as absent (no --speculative-config).
func TestAutotuner_ApplyConfig_SpeculativeDecodingOff(t *testing.T) {
	t.Parallel()

	model := makeModel("spec-off-model", "test-ns", "vllm",
		map[string]any{SpeculativeDecodingParam: NgramSpeculativeConfigJSON})
	fc := fakeclient.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(model).
		Build()

	tuner := &Autotuner{client: fc, modelName: "spec-off-model", namespace: "test-ns"}

	require.NoError(t, tuner.applyConfig(context.Background(),
		map[string]any{SpeculativeDecodingParam: ""}))

	got := &aiv1alpha2.Model{}
	require.NoError(t, fc.Get(context.Background(), ctrlclient.ObjectKey{Name: "spec-off-model", Namespace: "test-ns"}, got))
	assert.Equal(t, "", got.Spec.ConfigString(SpeculativeDecodingParam, "<unset>"))
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
