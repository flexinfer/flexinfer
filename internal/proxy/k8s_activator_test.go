package proxy

import (
	"context"
	"fmt"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type statusConflictClient struct {
	client.Client
	status client.SubResourceWriter
}

func (c *statusConflictClient) Status() client.SubResourceWriter {
	return c.status
}

type conflictOnceStatusWriter struct {
	delegate  client.SubResourceWriter
	remaining int
}

func (w *conflictOnceStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return w.delegate.Create(ctx, obj, subResource, opts...)
}

func (w *conflictOnceStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if w.remaining > 0 {
		w.remaining--
		return apierrors.NewConflict(
			schema.GroupResource{Group: aiv1alpha2.GroupVersion.Group, Resource: "models"},
			obj.GetName(),
			fmt.Errorf("simulated status conflict"),
		)
	}
	return w.delegate.Update(ctx, obj, opts...)
}

func (w *conflictOnceStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return w.delegate.Patch(ctx, obj, patch, opts...)
}

func (w *conflictOnceStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return w.delegate.Apply(ctx, obj, opts...)
}

func newActivatorTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
		builder = builder.WithStatusSubresource(objs...)
	}

	return builder.Build()
}

func newConflictRetryActivatorClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	base := newActivatorTestClient(t, objs...)
	return &statusConflictClient{
		Client: base,
		status: &conflictOnceStatusWriter{
			delegate:  base.Status(),
			remaining: 1,
		},
	}
}

func TestK8sModelActivatorTriggerScaleUp(t *testing.T) {
	RegisterMetrics()

	ctx := context.Background()

	t.Run("v1alpha2 status update conflict is retried", func(t *testing.T) {
		model := &aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "alpha2-model",
				Namespace: "default",
			},
			Spec: aiv1alpha2.ModelSpec{
				Backend: "vllm",
				Source:  "HF://test/model",
			},
		}

		cl := newConflictRetryActivatorClient(t, model)
		activator := NewK8sModelActivator(cl, "default", 60*time.Second)

		require.NoError(t, activator.TriggerScaleUp(ctx, "alpha2-model"))

		updated := &aiv1alpha2.Model{}
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: "alpha2-model", Namespace: "default"}, updated))
		require.NotNil(t, updated.Status.LastActiveTime)
	})

	t.Run("v1alpha1 fallback scales from zero", func(t *testing.T) {
		zero := int32(0)
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "alpha1-model",
				Namespace: "default",
			},
			Spec: aiv1alpha1.ModelDeploymentSpec{
				Replicas: &zero,
			},
		}

		cl := newActivatorTestClient(t, md)
		activator := NewK8sModelActivator(cl, "default", 60*time.Second)

		require.NoError(t, activator.TriggerScaleUp(ctx, "alpha1-model"))

		updated := &aiv1alpha1.ModelDeployment{}
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: "alpha1-model", Namespace: "default"}, updated))
		require.NotNil(t, updated.Spec.Replicas)
		assert.Equal(t, int32(1), *updated.Spec.Replicas)
	})

	t.Run("v1alpha1 already scaled is a no-op", func(t *testing.T) {
		one := int32(1)
		md := &aiv1alpha1.ModelDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "already-scaled",
				Namespace: "default",
			},
			Spec: aiv1alpha1.ModelDeploymentSpec{
				Replicas: &one,
			},
		}

		cl := newActivatorTestClient(t, md)
		activator := NewK8sModelActivator(cl, "default", 60*time.Second)

		require.NoError(t, activator.TriggerScaleUp(ctx, "already-scaled"))
		require.NoError(t, activator.TriggerScaleUp(ctx, "already-scaled"))

		updated := &aiv1alpha1.ModelDeployment{}
		require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: "already-scaled", Namespace: "default"}, updated))
		require.NotNil(t, updated.Spec.Replicas)
		assert.Equal(t, int32(1), *updated.Spec.Replicas)
	})
}

func TestK8sModelActivatorGetColdStartTimeout_TableDriven(t *testing.T) {
	RegisterMetrics()

	ctx := context.Background()

	tests := []struct {
		name    string
		objects []client.Object
		model   string
		want    time.Duration
	}{
		{
			name: "v1alpha2 custom timeout wins",
			objects: []client.Object{
				&aiv1alpha2.Model{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "alpha2-custom",
						Namespace: "default",
					},
					Spec: aiv1alpha2.ModelSpec{
						Backend: "vllm",
						Source:  "HF://test/model",
						Serverless: &aiv1alpha2.ServerlessSpec{
							ColdStartTimeout: &metav1.Duration{Duration: 120 * time.Second},
						},
					},
				},
			},
			model: "alpha2-custom",
			want:  120 * time.Second,
		},
		{
			name: "v1alpha2 falls back to proxy default",
			objects: []client.Object{
				&aiv1alpha2.Model{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "alpha2-default",
						Namespace: "default",
					},
					Spec: aiv1alpha2.ModelSpec{
						Backend: "vllm",
						Source:  "HF://test/model",
					},
				},
			},
			model: "alpha2-default",
			want:  60 * time.Second,
		},
		{
			name: "v1alpha1 custom timeout wins on fallback",
			objects: []client.Object{
				&aiv1alpha1.ModelDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "alpha1-custom",
						Namespace: "default",
					},
					Spec: aiv1alpha1.ModelDeploymentSpec{
						ColdStartTimeoutSeconds: int32Ptr(90),
					},
				},
			},
			model: "alpha1-custom",
			want:  90 * time.Second,
		},
		{
			name:  "missing model uses default timeout",
			model: "missing",
			want:  60 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activator := NewK8sModelActivator(newActivatorTestClient(t, tt.objects...), "default", 60*time.Second)
			assert.Equal(t, tt.want, activator.GetColdStartTimeout(ctx, tt.model))
		})
	}
}

func TestK8sModelActivatorIsNodeTerminating_TableDriven(t *testing.T) {
	RegisterMetrics()

	ctx := context.Background()

	tests := []struct {
		name    string
		objects []client.Object
		node    string
		want    bool
	}{
		{
			name: "annotation marks node terminating",
			objects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-annotated",
						Annotations: map[string]string{
							"flexinfer.ai/spot-terminating": "true",
						},
					},
				},
			},
			node: "node-annotated",
			want: true,
		},
		{
			name: "taint marks node terminating",
			objects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-tainted",
					},
					Spec: corev1.NodeSpec{
						Taints: []corev1.Taint{
							{
								Key:    "flexinfer.ai/spot-terminating",
								Effect: corev1.TaintEffectNoSchedule,
							},
						},
					},
				},
			},
			node: "node-tainted",
			want: true,
		},
		{
			name: "clean node is not terminating",
			objects: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-clean",
					},
				},
			},
			node: "node-clean",
			want: false,
		},
		{
			name: "missing node is not terminating",
			node: "missing-node",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			activator := NewK8sModelActivator(newActivatorTestClient(t, tt.objects...), "default", 60*time.Second)
			assert.Equal(t, tt.want, activator.IsNodeTerminating(ctx, tt.node))
		})
	}
}

func int32Ptr(v int32) *int32 {
	return &v
}
