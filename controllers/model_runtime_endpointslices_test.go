package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
)

const mirroringControllerName = "endpointslicemirroring-controller.k8s.io"

func endpointSliceFor(name, service, managedBy, ip string) *discoveryv1.EndpointSlice {
	ready := true
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				discoveryv1.LabelServiceName: service,
				discoveryv1.LabelManagedBy:   managedBy,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{ip},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
	}
}

type sliceFixture struct {
	t      *testing.T
	client client.Client
	r      *ModelReconciler
	model  *aiv1alpha2.Model
}

func newSliceFixture(t *testing.T, svc *corev1.Service, funcs *interceptor.Funcs, slices ...*discoveryv1.EndpointSlice) *sliceFixture {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(s))
	require.NoError(t, aiv1alpha2.AddToScheme(s))
	model := &aiv1alpha2.Model{ObjectMeta: metav1.ObjectMeta{Name: "warm-lane", Namespace: "default"}}
	b := fake.NewClientBuilder().WithScheme(s).WithObjects(model, svc)
	for _, slice := range slices {
		b = b.WithObjects(slice)
	}
	if funcs != nil {
		b = b.WithInterceptorFuncs(*funcs)
	}
	c := b.Build()
	return &sliceFixture{t: t, client: c, r: &ModelReconciler{Client: c, Scheme: s}, model: model}
}

func (f *sliceFixture) service() *corev1.Service {
	f.t.Helper()
	svc := &corev1.Service{}
	require.NoError(f.t, f.client.Get(context.Background(), client.ObjectKeyFromObject(f.model), svc))
	return svc
}

func (f *sliceFixture) sliceNames() []string {
	f.t.Helper()
	var list discoveryv1.EndpointSliceList
	require.NoError(f.t, f.client.List(context.Background(), &list, client.InNamespace("default")))
	names := make([]string, 0, len(list.Items))
	for _, s := range list.Items {
		names = append(names, s.Name)
	}
	return names
}

func laneService(selector map[string]string, annotations map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "warm-lane", Namespace: "default", Annotations: annotations},
		Spec:       corev1.ServiceSpec{Selector: selector},
	}
}

// The 2026-11-18 warm-lane switch: the Deployment's slice kept the deleted
// pod's address ready beside the slice mirrored from the runtime Endpoints.
func TestRemoveRuntimeServiceSelectorPrunesTheSelectorBuiltSlices(t *testing.T) {
	f := newSliceFixture(t, laneService(map[string]string{LabelModel: "warm-lane"}, nil), nil,
		endpointSliceFor("warm-lane-nbhfd", "warm-lane", endpointSliceControllerName, "10.42.0.160"),
		endpointSliceFor("warm-lane-mirror", "warm-lane", mirroringControllerName, "10.42.0.193"),
		endpointSliceFor("other-lane-abcde", "other-lane", endpointSliceControllerName, "10.42.0.77"),
	)
	ctx := context.Background()

	require.NoError(t, f.r.removeRuntimeServiceSelector(ctx, f.model))

	svc := f.service()
	assert.Nil(t, svc.Spec.Selector)
	_, recorded := selectorRemovedAt(svc)
	assert.True(t, recorded, "the removal time bounds the prune")
	assert.ElementsMatch(t, []string{"warm-lane-mirror", "other-lane-abcde"}, f.sliceNames(),
		"only this Service's selector-built slice goes; the mirrored slice is the lane's real backend")

	// A slice sync already in flight when the selector went writes one more.
	require.NoError(t, f.client.Create(ctx, endpointSliceFor("warm-lane-late", "warm-lane", endpointSliceControllerName, "10.42.0.160")))
	require.NoError(t, f.r.removeRuntimeServiceSelector(ctx, f.model))
	assert.ElementsMatch(t, []string{"warm-lane-mirror", "other-lane-abcde"}, f.sliceNames(),
		"the prune repeats inside the window")
}

func TestRemoveRuntimeServiceSelectorLeavesASettledServiceAlone(t *testing.T) {
	old := time.Now().Add(-staleSlicePruneWindow - time.Minute).UTC().Format(time.RFC3339)
	deletes := 0
	funcs := interceptor.Funcs{DeleteAllOf: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteAllOfOption) error {
		deletes++
		return c.DeleteAllOf(ctx, obj, opts...)
	}}
	f := newSliceFixture(t, laneService(nil, map[string]string{AnnotationSelectorRemovedAt: old}), &funcs)
	before := f.service().ResourceVersion

	require.NoError(t, f.r.removeRuntimeServiceSelector(context.Background(), f.model))

	assert.Equal(t, before, f.service().ResourceVersion, "no Service write on the steady-state pass")
	assert.Zero(t, deletes, "no API call once the window has passed")
}

// A Service an older controller made selectorless has no removal time: it is
// pruned once and then recorded.
func TestRemoveRuntimeServiceSelectorHealsAnUnrecordedSelectorlessService(t *testing.T) {
	f := newSliceFixture(t, laneService(nil, nil), nil,
		endpointSliceFor("warm-lane-nbhfd", "warm-lane", endpointSliceControllerName, "10.42.0.160"),
		endpointSliceFor("warm-lane-mirror", "warm-lane", mirroringControllerName, "10.42.0.193"),
	)

	require.NoError(t, f.r.removeRuntimeServiceSelector(context.Background(), f.model))

	assert.Equal(t, []string{"warm-lane-mirror"}, f.sliceNames())
	_, recorded := selectorRemovedAt(f.service())
	assert.True(t, recorded)
}

// Without the RBAC rule (a chart older than the controller) the lane must
// still come up on its mirrored slice.
func TestRemoveRuntimeServiceSelectorSurvivesAFailedPrune(t *testing.T) {
	funcs := interceptor.Funcs{DeleteAllOf: func(context.Context, client.WithWatch, client.Object, ...client.DeleteAllOfOption) error {
		return errors.New("endpointslices.discovery.k8s.io is forbidden")
	}}
	f := newSliceFixture(t, laneService(map[string]string{LabelModel: "warm-lane"}, nil), &funcs)

	require.NoError(t, f.r.removeRuntimeServiceSelector(context.Background(), f.model))
	assert.Nil(t, f.service().Spec.Selector)
}

func TestRestoreServiceSelectorDropsTheRemovalRecord(t *testing.T) {
	f := newSliceFixture(t, laneService(nil, map[string]string{
		AnnotationSelectorRemovedAt: time.Now().UTC().Format(time.RFC3339),
		"example.com/kept":          "yes",
	}), nil)

	require.NoError(t, f.r.restoreServiceSelector(context.Background(), f.model))

	svc := f.service()
	assert.NotEmpty(t, svc.Spec.Selector)
	assert.NotContains(t, svc.Annotations, AnnotationSelectorRemovedAt,
		"the next switch to runtime management must record its own removal time")
	assert.Equal(t, "yes", svc.Annotations["example.com/kept"])
}
