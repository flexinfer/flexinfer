package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// BenchmarkFunc abstracts the benchmarking step so it can be mocked in tests.
type BenchmarkFunc func(ctx context.Context) (tps float64, err error)

// Autotuner runs coordinate descent optimization on a Model CR's config.
type Autotuner struct {
	client         client.Client
	kubeClient     kubernetes.Interface
	modelName      string
	namespace      string
	benchFn        BenchmarkFunc
	rolloutTimeout time.Duration
	searchSpace    SearchSpace
	logger         *ExperimentLogger
	now            func() time.Time
}

// Options configures an Autotuner.
type Options struct {
	Client         client.Client
	KubeClient     kubernetes.Interface
	ModelName      string
	Namespace      string
	BenchFn        BenchmarkFunc
	RolloutTimeout time.Duration
	Space          SearchSpace
}

// New creates an Autotuner.
func New(opts Options) *Autotuner {
	if opts.RolloutTimeout <= 0 {
		opts.RolloutTimeout = 5 * time.Minute
	}
	if len(opts.Space.Parameters) == 0 {
		opts.Space = DefaultVLLMSearchSpace()
	}
	return &Autotuner{
		client:         opts.Client,
		kubeClient:     opts.KubeClient,
		modelName:      opts.ModelName,
		namespace:      opts.Namespace,
		benchFn:        opts.BenchFn,
		rolloutTimeout: opts.RolloutTimeout,
		searchSpace:    opts.Space,
		logger:         NewExperimentLogger(opts.KubeClient, opts.Namespace, opts.ModelName),
		now:            time.Now,
	}
}

// Run executes the autotune experiment loop:
// 1. Capture baseline config + benchmark
// 2. Coordinate descent through parameter space
// 3. Leave best config active
func (a *Autotuner) Run(ctx context.Context) error {
	startTime := a.now()

	// Safety: refuse to autotune shared GPU models.
	model, err := a.getModel(ctx)
	if err != nil {
		return fmt.Errorf("get model: %w", err)
	}
	if model.Spec.IsShared() {
		return fmt.Errorf("refusing to autotune model %q: shared GPU groups produce unstable benchmarks", a.modelName)
	}

	// 1. Capture baseline.
	baselineConfig := extractConfig(model)
	bestConfig := copyConfig(baselineConfig)

	fmt.Printf("[autotune] baseline config: %s\n", formatConfigDelta(baselineConfig))

	baselineTPS, err := a.benchFn(ctx)
	if err != nil {
		return fmt.Errorf("baseline benchmark: %w", err)
	}
	bestTPS := baselineTPS
	acceptedSteps := 0

	a.logger.Add(ExperimentEntry{
		Step:   0,
		Action: ActionBaseline,
		TPS:    baselineTPS,
	})
	fmt.Printf("[autotune] baseline: %.1f tok/s\n", baselineTPS)

	// 2. Coordinate descent.
	search := NewCoordinateDescent(a.searchSpace)
	step := 0

	for {
		candidate := search.Next(bestConfig, a.logger.Entries())
		if candidate == nil {
			break
		}
		step++

		delta := configDeltaString(bestConfig, *candidate)

		// Safety: reject dangerous gpuMemoryUtilization values.
		if rejected, reason := a.validateCandidate(*candidate); rejected {
			a.logger.Add(ExperimentEntry{
				Step:        step,
				Action:      ActionSkipped,
				ConfigDelta: delta,
				Error:       reason,
			})
			fmt.Printf("[autotune] step %d: SKIP %s (%s)\n", step, delta, reason)
			continue
		}

		fmt.Printf("[autotune] step %d: trying %s ...\n", step, delta)

		// Apply candidate config.
		if err := a.applyConfig(ctx, *candidate); err != nil {
			a.logger.Add(ExperimentEntry{
				Step:        step,
				Action:      ActionRolloutFailed,
				ConfigDelta: delta,
				Error:       err.Error(),
			})
			fmt.Printf("[autotune] step %d: apply failed: %v, rolling back\n", step, err)
			if rbErr := a.applyConfig(ctx, bestConfig); rbErr != nil {
				return fmt.Errorf("rollback failed after apply error: %w (original: %v)", rbErr, err)
			}
			if err := a.waitForReady(ctx); err != nil {
				return fmt.Errorf("rollback readiness failed: %w", err)
			}
			continue
		}

		// Wait for rollout.
		if err := a.waitForReady(ctx); err != nil {
			a.logger.Add(ExperimentEntry{
				Step:        step,
				Action:      ActionRolloutFailed,
				ConfigDelta: delta,
				Error:       err.Error(),
			})
			fmt.Printf("[autotune] step %d: rollout failed: %v, rolling back\n", step, err)
			if rbErr := a.applyConfig(ctx, bestConfig); rbErr != nil {
				return fmt.Errorf("rollback failed after rollout timeout: %w (original: %v)", rbErr, err)
			}
			if err := a.waitForReady(ctx); err != nil {
				return fmt.Errorf("rollback readiness failed: %w", err)
			}
			continue
		}

		// Benchmark.
		tps, err := a.benchFn(ctx)
		if err != nil {
			a.logger.Add(ExperimentEntry{
				Step:        step,
				Action:      ActionRolloutFailed,
				ConfigDelta: delta,
				Error:       fmt.Sprintf("benchmark error: %v", err),
			})
			fmt.Printf("[autotune] step %d: benchmark failed: %v, rolling back\n", step, err)
			if rbErr := a.applyConfig(ctx, bestConfig); rbErr != nil {
				return fmt.Errorf("rollback failed after benchmark error: %w (original: %v)", rbErr, err)
			}
			if err := a.waitForReady(ctx); err != nil {
				return fmt.Errorf("rollback readiness failed: %w", err)
			}
			continue
		}

		improvement := ((tps - bestTPS) / bestTPS) * 100

		if tps > bestTPS {
			bestTPS = tps
			bestConfig = copyConfig(*candidate)
			acceptedSteps++
			a.logger.Add(ExperimentEntry{
				Step:        step,
				Action:      ActionAccepted,
				TPS:         tps,
				Improvement: improvement,
				ConfigDelta: delta,
			})
			fmt.Printf("[autotune] step %d: ACCEPTED %.1f tok/s (%+.2f%%) %s\n", step, tps, improvement, delta)
		} else {
			a.logger.Add(ExperimentEntry{
				Step:        step,
				Action:      ActionRejected,
				TPS:         tps,
				Improvement: improvement,
				ConfigDelta: delta,
			})
			fmt.Printf("[autotune] step %d: REJECTED %.1f tok/s (%+.2f%%) %s\n", step, tps, improvement, delta)
			// Rollback to best config.
			if err := a.applyConfig(ctx, bestConfig); err != nil {
				return fmt.Errorf("rollback to best config failed: %w", err)
			}
			if err := a.waitForReady(ctx); err != nil {
				return fmt.Errorf("rollback readiness failed: %w", err)
			}
		}
	}

	// 3. Ensure best config is active.
	if err := a.applyConfig(ctx, bestConfig); err != nil {
		return fmt.Errorf("apply final best config: %w", err)
	}
	if err := a.waitForReady(ctx); err != nil {
		return fmt.Errorf("final readiness check: %w", err)
	}

	summary := ExperimentSummary{
		ModelName:     a.modelName,
		StartTime:     startTime,
		EndTime:       a.now(),
		BaselineTPS:   baselineTPS,
		BestTPS:       bestTPS,
		Improvement:   ((bestTPS - baselineTPS) / baselineTPS) * 100,
		TotalSteps:    step,
		AcceptedSteps: acceptedSteps,
		BestConfig:    bestConfig,
	}

	if err := a.logger.Save(ctx, summary); err != nil {
		return fmt.Errorf("save results: %w", err)
	}

	fmt.Printf("\n[autotune] complete: %.1f → %.1f tok/s (%+.2f%%), %d/%d steps accepted\n",
		baselineTPS, bestTPS, summary.Improvement, acceptedSteps, step)
	fmt.Printf("[autotune] results saved to ConfigMap %s/%s\n", a.namespace, a.logger.ConfigMapName())

	return nil
}

// Rollback applies the baseline config. Used by the CLI signal handler for clean exit.
func (a *Autotuner) Rollback(ctx context.Context, config map[string]interface{}) error {
	return a.applyConfig(ctx, config)
}

func (a *Autotuner) getModel(ctx context.Context) (*aiv1alpha2.Model, error) {
	model := &aiv1alpha2.Model{}
	key := types.NamespacedName{Name: a.modelName, Namespace: a.namespace}
	if err := a.client.Get(ctx, key, model); err != nil {
		return nil, err
	}
	return model, nil
}

func extractConfig(model *aiv1alpha2.Model) map[string]interface{} {
	cfg := model.Spec.GetConfigMap()
	if cfg == nil {
		return make(map[string]interface{})
	}
	return cfg
}

func (a *Autotuner) applyConfig(ctx context.Context, cfg map[string]interface{}) error {
	model, err := a.getModel(ctx)
	if err != nil {
		return fmt.Errorf("get model for patch: %w", err)
	}

	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	original := model.DeepCopy()
	model.Spec.Config = &apiextensionsv1.JSON{Raw: raw}

	if err := a.client.Patch(ctx, model, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("patch model config: %w", err)
	}
	return nil
}

func (a *Autotuner) waitForReady(ctx context.Context) error {
	deployName := a.modelName
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	deadline := time.After(a.rolloutTimeout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("rollout timeout after %s", a.rolloutTimeout)
		case <-ticker.C:
			deploy := &appsv1.Deployment{}
			key := types.NamespacedName{Name: deployName, Namespace: a.namespace}
			if err := a.client.Get(ctx, key, deploy); err != nil {
				continue // Deployment might not exist yet.
			}

			if deploy.Status.ObservedGeneration < deploy.Generation {
				continue // Controller hasn't observed this generation yet.
			}
			if deploy.Spec.Replicas == nil {
				continue
			}
			desired := *deploy.Spec.Replicas
			if desired == 0 {
				continue // Scaled to zero, not ready.
			}
			if deploy.Status.UpdatedReplicas == desired &&
				deploy.Status.ReadyReplicas == desired &&
				deploy.Status.AvailableReplicas == desired {
				return nil
			}
		}
	}
}

func (a *Autotuner) validateCandidate(cfg map[string]interface{}) (rejected bool, reason string) {
	if v, ok := cfg["gpuMemoryUtilization"]; ok {
		var val float64
		switch tv := v.(type) {
		case string:
			parsed, err := strconv.ParseFloat(tv, 64)
			if err == nil {
				val = parsed
			}
		case float64:
			val = tv
		}
		if val > MaxGPUMemoryUtilization {
			return true, fmt.Sprintf("gpuMemoryUtilization %.2f exceeds safety cap %.2f", val, MaxGPUMemoryUtilization)
		}
	}
	return false, ""
}

func configDeltaString(base, candidate map[string]interface{}) string {
	var parts []string
	for k, v := range candidate {
		if base[k] != v {
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		}
	}
	if len(parts) == 0 {
		return "(no change)"
	}
	return strings.Join(parts, ",")
}

func formatConfigDelta(cfg map[string]interface{}) string {
	var parts []string
	for k, v := range cfg {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, ", ")
}
