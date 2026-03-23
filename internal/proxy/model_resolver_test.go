package proxy

import (
	"context"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newModelResolverTestClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objs) > 0 {
		builder = builder.WithObjects(objs...)
	}

	return builder.Build()
}

func TestModelResolverResolveModelAlias_TableDriven(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen3-30b-a3b-abliterated",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "llamacpp",
			Source:  "HF://test/model",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "qwen3-30b-abliterated",
				Aliases:         []string{"qwen3-30b", "qwen3-moe"},
			},
		},
	}

	resolver := NewModelResolver(newModelResolverTestClient(t, model), "default")

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "served model name resolves to resource name", input: "qwen3-30b-abliterated", want: "qwen3-30b-a3b-abliterated"},
		{name: "primary alias resolves to resource name", input: "qwen3-30b", want: "qwen3-30b-a3b-abliterated"},
		{name: "secondary alias resolves to resource name", input: "qwen3-moe", want: "qwen3-30b-a3b-abliterated"},
		{name: "resource name falls back unchanged", input: "qwen3-30b-a3b-abliterated", want: "qwen3-30b-a3b-abliterated"},
		{name: "missing alias falls back unchanged", input: "not-an-alias", want: "not-an-alias"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolver.ResolveModelAlias(ctx, tt.input))
		})
	}
}

func TestModelResolverResolveModelAlias_RefreshesStaleCache(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cached-model",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/cached",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "cached-served",
				Aliases:         []string{"old-alias"},
			},
		},
	}

	cl := newModelResolverTestClient(t, model)
	resolver := NewModelResolver(cl, "default")

	require.Equal(t, "cached-model", resolver.ResolveModelAlias(ctx, "old-alias"))

	updated := &aiv1alpha2.Model{}
	require.NoError(t, cl.Get(ctx, client.ObjectKey{Name: "cached-model", Namespace: "default"}, updated))
	updated.Spec.LiteLLM.Aliases = []string{"new-alias"}
	require.NoError(t, cl.Update(ctx, updated))

	resolver.modelAliasCacheMu.Lock()
	resolver.lastAliasRefresh = time.Now()
	resolver.modelAliasCacheMu.Unlock()

	assert.Equal(t, "new-alias", resolver.ResolveModelAlias(ctx, "new-alias"))
	assert.Equal(t, "cached-model", resolver.ResolveModelAlias(ctx, "old-alias"))

	resolver.modelAliasCacheMu.Lock()
	resolver.lastAliasRefresh = time.Time{}
	resolver.modelAliasCacheMu.Unlock()

	assert.Equal(t, "cached-model", resolver.ResolveModelAlias(ctx, "new-alias"))
	assert.Equal(t, "old-alias", resolver.ResolveModelAlias(ctx, "old-alias"))
}
