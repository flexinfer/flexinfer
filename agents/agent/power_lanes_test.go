package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

func laneModel(name, ns string, phase aiv1alpha2.ModelPhase, node string, vendor aiv1alpha2.GPUVendor) *aiv1alpha2.Model {
	m := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}}
	m.Spec.Backend = "llamacpp"
	one := int32(1)
	m.Spec.GPU = &aiv1alpha2.GPUSpec{Vendor: vendor, Count: &one}
	m.Status.Phase = phase
	if node != "" {
		m.Status.GPU = &aiv1alpha2.GPUStatus{Node: node}
	}
	return m
}

func TestModelLaneSource_ActiveLanes(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))
	const ns, node = "flexinfer-system", "cblevins-radeonvii"
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		laneModel("gte-qwen2-1p5b-radeonvii", ns, aiv1alpha2.ModelPhaseReady, node, aiv1alpha2.GPUVendorAMD),
		laneModel("bge-large-radeonvii", ns, aiv1alpha2.ModelPhaseReady, node, aiv1alpha2.GPUVendorAMD),
		// Not Active on this node's GPU:
		laneModel("qwen3-1p7b-tools-radeonvii", ns, aiv1alpha2.ModelPhaseReady, node, aiv1alpha2.GPUVendorCPU),
		laneModel("gemma4-e4b-radeonvii", ns, aiv1alpha2.ModelPhaseIdle, node, aiv1alpha2.GPUVendorAMD),
		laneModel("qwen38-27b-autoround-workhorse", ns, aiv1alpha2.ModelPhaseReady, "cblevins-5930k", aiv1alpha2.GPUVendorAMD),
		laneModel("never-placed", ns, aiv1alpha2.ModelPhaseReady, "", aiv1alpha2.GPUVendorAMD),
		laneModel("other-namespace", "tenant-a", aiv1alpha2.ModelPhaseReady, node, aiv1alpha2.GPUVendorAMD),
	).Build()

	got, err := modelLaneSource{c: c, namespace: ns}.ActiveLanes(context.Background(), node)
	require.NoError(t, err)
	assert.Equal(t, []string{"bge-large-radeonvii", "gte-qwen2-1p5b-radeonvii"}, got)
}
