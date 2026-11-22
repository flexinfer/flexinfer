package proxy

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
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

func TestModelResolverResolveModelAlias_DedupesSameModelClaim(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	model := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "qwen36-27b-gptq",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "pvc://qwen36-27b-oci/qwen36-27b",
			LiteLLM: &aiv1alpha2.LiteLLMSpec{
				ServedModelName: "qwen36-27b",
				Aliases:         []string{"qwen36-27b", "qwen36-canary"},
			},
		},
	}

	resolver := NewModelResolver(newModelResolverTestClient(t, model), "default")

	assert.Equal(t, "qwen36-27b-gptq", resolver.ResolveModelAlias(ctx, "qwen36-27b"))
	assert.Equal(t, "qwen36-27b-gptq", resolver.ResolveModelAlias(ctx, "qwen36-canary"))
}

// TestModelResolverRefreshIsInvisibleToConcurrentReaders: a refresh must
// publish a complete cache in one step. Before, it reassigned the sync.Map
// fields (a data race) and then refilled them, so a reader in that window
// missed a label or alias it had resolved a moment earlier.
func TestModelResolverRefreshIsInvisibleToConcurrentReaders(t *testing.T) {
	ctx := context.Background()
	objs := []client.Object{
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "model-a", Namespace: "default",
			Annotations: map[string]string{AnnotationServiceLabels: "shared,only-a"}}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "model-b", Namespace: "default",
			Annotations: map[string]string{AnnotationServiceLabels: "shared"}}},
		&aiv1alpha2.Model{
			ObjectMeta: metav1.ObjectMeta{Name: "qwen-image-21-gfx1100", Namespace: "default"},
			Spec: aiv1alpha2.ModelSpec{Backend: "diffusers", Source: "pvc://a/b",
				LiteLLM: &aiv1alpha2.LiteLLMSpec{Aliases: []string{"qwen-image"}}},
		},
	}
	for i := 0; i < 60; i++ {
		objs = append(objs, &corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("filler-%02d", i), Namespace: "default",
			Annotations: map[string]string{AnnotationServiceLabels: fmt.Sprintf("filler-%02d", i)}}})
	}
	r := NewModelResolver(newModelResolverTestClient(t, objs...), "default")
	require.Equal(t, "model-a", r.ResolveServiceLabel(ctx, "only-a"))
	require.Equal(t, "qwen-image-21-gfx1100", r.ResolveModelAlias(ctx, "qwen-image"))

	stop := make(chan struct{})
	var refresher sync.WaitGroup
	refresher.Add(1)
	go func() {
		defer refresher.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			r.expireCachesForTest()
			r.refreshServiceLabelCache(ctx)
			r.refreshModelAliasCache(ctx)
		}
	}()

	var misses atomic.Int64
	var readers sync.WaitGroup
	for w := 0; w < 4; w++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 2000; i++ {
				if r.ResolveServiceLabel(ctx, "only-a") != "model-a" {
					misses.Add(1)
				}
				if members, ok := r.ResolveServiceLabelGroup(ctx, "shared"); !ok || len(members) != 2 {
					misses.Add(1)
				}
				if !r.IsModelInLabelGroup("model-b") {
					misses.Add(1)
				}
				if r.ResolveModelAlias(ctx, "qwen-image") != "qwen-image-21-gfx1100" {
					misses.Add(1)
				}
			}
		}()
	}
	readers.Wait()
	close(stop)
	refresher.Wait()
	assert.Zero(t, misses.Load(), "a reader saw a cache that was being rebuilt")
}
