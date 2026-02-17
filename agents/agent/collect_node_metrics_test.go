package agent

import (
	"context"
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCollectNodeMetrics_PodListError_DoesNotPanic(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("etcdserver: request timed out")
	})

	a := &Agent{
		kubeClient: client,
		nodeName:   "node-a",
		namespace:  "flexinfer-system",
		sysfsRoot:  t.TempDir(),
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return nil, exec.ErrNotFound
		},
	}

	var got NodeMetrics
	require.NotPanics(t, func() {
		got = a.collectNodeMetrics(context.Background())
	})
	assert.Equal(t, NodeMetrics{}, got)
}

func TestCollectNodeMetrics_PodListError_StillReportsGPU(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("etcdserver: request timed out")
	})

	a := &Agent{
		kubeClient: client,
		nodeName:   "node-a",
		namespace:  "flexinfer-system",
		sysfsRoot:  t.TempDir(),
		runCmd: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "nvidia-smi" {
				return []byte("0, 40, 100, 1000, 900, 10\n"), nil
			}
			return nil, exec.ErrNotFound
		},
	}

	got := a.collectNodeMetrics(context.Background())
	assert.Equal(t, uint64(900), got.FreeVRAMMB)
	assert.Equal(t, 10.0, got.GPUUtilization)
	assert.Equal(t, 0.0, got.TotalKVCacheUsage)
}
