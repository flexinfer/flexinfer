package proxy

import (
	"context"
	"testing"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Every object exists in both backends under the same key; a "source" label
// records which backend answered.
func cachedReadTestObjects(source string) []client.Object {
	labels := map[string]string{"source": source}
	meta := func(name, namespace string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}
	}
	return []client.Object{
		&aiv1alpha2.Model{ObjectMeta: meta("m", "flexinfer-system")},
		&aiv1alpha2.Model{ObjectMeta: meta("m", "other")},
		&corev1.Service{ObjectMeta: meta("m", "flexinfer-system")},
		&aiv1alpha2.LoRAAdapter{ObjectMeta: meta("a", "flexinfer-system")},
		&corev1.Pod{ObjectMeta: meta("p", "flexinfer-system")},
	}
}

func TestCachedReadClientRoutesHotPathReads(t *testing.T) {
	ctx := context.Background()
	objects := cachedReadTestObjects("cache")
	cache := fake.NewClientBuilder().WithScheme(Scheme).WithObjects(objects...).Build()
	directObjects := cachedReadTestObjects("direct")
	direct := fake.NewClientBuilder().WithScheme(Scheme).
		WithObjects(directObjects...).WithStatusSubresource(directObjects[0]).Build()
	c := &cachedReadClient{Client: direct, cache: cache, namespace: "flexinfer-system"}

	get := func(obj client.Object, name, namespace string) string {
		t.Helper()
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj))
		return obj.GetLabels()["source"]
	}
	assert.Equal(t, "cache", get(&aiv1alpha2.Model{}, "m", "flexinfer-system"))
	assert.Equal(t, "cache", get(&corev1.Service{}, "m", "flexinfer-system"))
	assert.Equal(t, "cache", get(&aiv1alpha2.LoRAAdapter{}, "a", "flexinfer-system"))
	assert.Equal(t, "direct", get(&aiv1alpha2.Model{}, "m", "other"), "other namespaces are not cached")
	assert.Equal(t, "direct", get(&corev1.Pod{}, "p", "flexinfer-system"), "uncached kinds read the API server")

	list := func(l client.ObjectList, opts ...client.ListOption) string {
		t.Helper()
		require.NoError(t, c.List(ctx, l, opts...))
		switch l := l.(type) {
		case *aiv1alpha2.ModelList:
			require.NotEmpty(t, l.Items)
			return l.Items[0].Labels["source"]
		case *aiv1alpha2.LoRAAdapterList:
			require.NotEmpty(t, l.Items)
			return l.Items[0].Labels["source"]
		case *corev1.PodList:
			require.NotEmpty(t, l.Items)
			return l.Items[0].Labels["source"]
		}
		t.Fatalf("unexpected list %T", l)
		return ""
	}
	assert.Equal(t, "cache", list(&aiv1alpha2.ModelList{}, client.InNamespace("flexinfer-system")))
	assert.Equal(t, "cache", list(&aiv1alpha2.LoRAAdapterList{}, client.InNamespace("flexinfer-system"),
		client.MatchingLabels{"source": "cache"}))
	assert.Equal(t, "direct", list(&aiv1alpha2.ModelList{}), "cluster-wide lists are not cached")
	assert.Equal(t, "direct", list(&corev1.PodList{}, client.InNamespace("flexinfer-system")))

	// Writes always reach the API server.
	m := &aiv1alpha2.Model{}
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "m", Namespace: "flexinfer-system"}, m))
	base := m.DeepCopy()
	now := metav1.Now()
	m.Status.LastActiveTime = &now
	require.NoError(t, c.Status().Patch(ctx, m, client.MergeFrom(base)))
	written := &aiv1alpha2.Model{}
	require.NoError(t, direct.Get(ctx, client.ObjectKey{Name: "m", Namespace: "flexinfer-system"}, written))
	assert.NotNil(t, written.Status.LastActiveTime)
}
