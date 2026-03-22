package proxy

import (
	"context"
	"time"
)

// ModelActivator abstracts K8s CRD operations needed by the proxy.
// This decouples the proxy from direct K8s API access, enabling
// unit testing and clean separation of concerns.
type ModelActivator interface {
	// TriggerScaleUp signals the controller to scale up a model.
	// For v1alpha2 Models, it sets LastActiveTime.
	// For v1alpha1 ModelDeployments, it sets replicas and LastAccessTime.
	TriggerScaleUp(ctx context.Context, modelName string) error

	// TouchLastActiveTime updates the model's LastActiveTime status field
	// to signal continued demand. Fire-and-forget; errors are logged, not returned.
	TouchLastActiveTime(ctx context.Context, modelName string)

	// GetColdStartTimeout returns the configured cold-start timeout for a model.
	// Uses per-model ColdStartTimeout if specified, otherwise returns the proxy default.
	GetColdStartTimeout(ctx context.Context, modelName string) time.Duration

	// IsNodeTerminating checks if a node is being drained/cordoned.
	IsNodeTerminating(ctx context.Context, nodeName string) bool
}
