package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// K8sModelActivator implements ModelActivator using a controller-runtime client
// to interact with K8s CRDs (Model, ModelDeployment, Node).
type K8sModelActivator struct {
	client           client.Client
	namespace        string
	coldStartTimeout time.Duration // proxy-level default
}

// NewK8sModelActivator creates a K8sModelActivator with the given client, namespace,
// and default cold-start timeout.
func NewK8sModelActivator(c client.Client, ns string, defaultColdStart time.Duration) *K8sModelActivator {
	return &K8sModelActivator{
		client:           c,
		namespace:        ns,
		coldStartTimeout: defaultColdStart,
	}
}

// TriggerScaleUp signals the controller to scale up a model.
// For v1alpha2 Models, it sets LastActiveTime.
// For v1alpha1 ModelDeployments, it sets replicas and LastAccessTime.
func (a *K8sModelActivator) TriggerScaleUp(ctx context.Context, modelName string) error {
	// Try v1alpha2 Model first: update LastActiveTime to trigger controller scale-up.
	m, err := a.getModel(ctx, modelName)
	if err == nil {
		// Retry on conflict since the controller may also be updating status.
		for i := 0; i < 3; i++ {
			if i > 0 {
				m, err = a.getModel(ctx, modelName)
				if err != nil {
					return err
				}
			}

			now := metav1.Now()
			m.Status.LastActiveTime = &now
			if err := a.client.Status().Update(ctx, m); err != nil {
				if errors.IsConflict(err) {
					slog.Debug("conflict updating lastActiveTime, retrying", "model", modelName, "attempt", i+1)
					continue
				}
				return fmt.Errorf("failed to update Model lastActiveTime: %w", err)
			}
			return nil
		}
		if a.modelHasFreshDemand(ctx, modelName, 30*time.Second) {
			slog.Debug("lastActiveTime already fresh after status conflicts", "model", modelName)
			return nil
		}
		return fmt.Errorf("failed to update Model lastActiveTime after 3 retries (conflict)")
	}
	if !errors.IsNotFound(err) {
		return err
	}

	// Fallback: v1alpha1 ModelDeployment (deprecated)
	md, err := a.getModelDeployment(ctx, modelName)
	if err != nil {
		return err
	}

	// Already scaled up?
	if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
		return nil
	}

	slog.Info("scaling up model", "model", modelName, "from", 0, "to", 1)
	scaleUpsTotal.WithLabelValues(modelName).Inc()

	// First, update LastAccessTime to prevent the controller from immediately
	// scaling back down due to stale idle timeout.
	now := metav1.Now()
	slog.Debug("setting lastAccessTime", "model", modelName, "time", now.Time, "resourceVersion", md.ResourceVersion)
	md.Status.LastAccessTime = &now
	if err := a.client.Status().Update(ctx, md); err != nil {
		slog.Warn("failed to update LastAccessTime before scale-up", "model", modelName, "error", err)
	} else {
		slog.Debug("updated lastAccessTime", "model", modelName)
	}

	// Re-fetch to get latest version after status update
	md, err = a.getModelDeployment(ctx, modelName)
	if err != nil {
		return err
	}

	// Check again in case someone else scaled it up
	if md.Spec.Replicas != nil && *md.Spec.Replicas > 0 {
		return nil
	}

	one := int32(1)
	md.Spec.Replicas = &one
	if err := a.client.Update(ctx, md); err != nil {
		if errors.IsConflict(err) {
			return nil
		}
		return fmt.Errorf("failed to scale up: %w", err)
	}

	return nil
}

func (a *K8sModelActivator) modelHasFreshDemand(ctx context.Context, modelName string, maxAge time.Duration) bool {
	m, err := a.getModel(ctx, modelName)
	if err != nil || m.Status.LastActiveTime == nil {
		return false
	}
	return time.Since(m.Status.LastActiveTime.Time) <= maxAge
}

// TouchLastActiveTime updates the model's LastActiveTime so the controller
// can backfill Model.Status.Phase for observability. Fire-and-forget.
func (a *K8sModelActivator) TouchLastActiveTime(ctx context.Context, modelName string) {
	for i := 0; i < 3; i++ {
		m, err := a.getModel(ctx, modelName)
		if err != nil {
			return
		}
		now := metav1.Now()
		m.Status.LastActiveTime = &now
		if err := a.client.Status().Update(ctx, m); err != nil {
			if errors.IsConflict(err) {
				continue
			}
			slog.Debug("direct load: failed to touch lastActiveTime", "model", modelName, "error", err)
		}
		return
	}
}

// GetColdStartTimeout returns the cold start timeout for a model.
// Uses per-model ColdStartTimeout if specified, otherwise falls back to proxy default.
func (a *K8sModelActivator) GetColdStartTimeout(ctx context.Context, modelName string) time.Duration {
	// Check v1alpha2 Model first
	m, err := a.getModel(ctx, modelName)
	if err == nil {
		if m.Spec.Serverless != nil && m.Spec.Serverless.ColdStartTimeout != nil {
			return m.Spec.Serverless.ColdStartTimeout.Duration
		}
		return a.coldStartTimeout
	}
	if !errors.IsNotFound(err) {
		return a.coldStartTimeout
	}

	// Fallback: v1alpha1 ModelDeployment (deprecated)
	md, err := a.getModelDeployment(ctx, modelName)
	if err == nil && md.Spec.ColdStartTimeoutSeconds != nil {
		return time.Duration(*md.Spec.ColdStartTimeoutSeconds) * time.Second
	}
	return a.coldStartTimeout
}

// IsNodeTerminating checks if a node is marked for spot instance termination.
// Nodes are marked by the drain coordinator setting the flexinfer.ai/spot-terminating annotation.
func (a *K8sModelActivator) IsNodeTerminating(ctx context.Context, nodeName string) bool {
	var node corev1.Node
	if err := a.client.Get(ctx, client.ObjectKey{Name: nodeName}, &node); err != nil {
		return false
	}

	if node.Annotations != nil {
		if node.Annotations["flexinfer.ai/spot-terminating"] == "true" {
			return true
		}
	}

	// Also check for the taint
	for _, taint := range node.Spec.Taints {
		if taint.Key == "flexinfer.ai/spot-terminating" {
			return true
		}
	}

	return false
}

// getModel fetches the v1alpha2 Model resource.
func (a *K8sModelActivator) getModel(ctx context.Context, modelName string) (*aiv1alpha2.Model, error) {
	m := &aiv1alpha2.Model{}
	err := a.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: a.namespace}, m)
	return m, err
}

// getModelDeployment fetches the v1alpha1 ModelDeployment resource.
func (a *K8sModelActivator) getModelDeployment(ctx context.Context, modelName string) (*aiv1alpha1.ModelDeployment, error) {
	md := &aiv1alpha1.ModelDeployment{}
	err := a.client.Get(ctx, client.ObjectKey{Name: modelName, Namespace: a.namespace}, md)
	return md, err
}
