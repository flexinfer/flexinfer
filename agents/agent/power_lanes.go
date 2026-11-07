package agent

import (
	"context"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

// LaneSource lists the lanes Active on a node's GPU, for energy attribution.
type LaneSource interface {
	ActiveLanes(ctx context.Context, node string) ([]string, error)
}

// modelLaneSource reads Model objects: a lane is Active on the node's GPU
// while it is phase Ready, status.gpu.node names the node, and it allocates a
// GPU (CPU-only lanes sharing the node, such as qwen3-1p7b-tools-radeonvii,
// draw no GPU power). It sees the agent's own namespace only, where every
// fleet lane lives.
type modelLaneSource struct {
	c         client.Reader
	namespace string
}

// NewModelLaneSource builds a LaneSource from the in-cluster config. The
// agent ServiceAccount needs get/list on models.ai.flexinfer in its
// namespace, which the chart grants only when agent.gpuPower.nodes is set.
func (a *Agent) NewModelLaneSource() (LaneSource, error) {
	c, err := newModelReader()
	if err != nil {
		return nil, err
	}
	return modelLaneSource{c: c, namespace: a.namespace}, nil
}

// newModelReader builds an uncached client that reads Model objects with the
// agent's in-cluster ServiceAccount.
func newModelReader() (client.Reader, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	scheme := runtime.NewScheme()
	if err := aiv1alpha2.AddToScheme(scheme); err != nil {
		return nil, err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("model client: %w", err)
	}
	return c, nil
}

func (s modelLaneSource) ActiveLanes(ctx context.Context, node string) ([]string, error) {
	var list aiv1alpha2.ModelList
	if err := s.c.List(ctx, &list, client.InNamespace(s.namespace)); err != nil {
		return nil, err
	}
	var out []string
	for i := range list.Items {
		m := &list.Items[i]
		if m.Status.Phase != aiv1alpha2.ModelPhaseReady || m.Status.GPU == nil || m.Status.GPU.Node != node {
			continue
		}
		if m.Spec.GetGPUCount() == 0 || m.Spec.GetGPUVendor() == aiv1alpha2.GPUVendorCPU {
			continue
		}
		out = append(out, m.Name)
	}
	sort.Strings(out)
	return out, nil
}
