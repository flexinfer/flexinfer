package proxy

import (
	"context"
	"sort"
	"testing"
	"time"

	aiv1alpha1 "github.com/flexinfer/flexinfer/api/v1alpha1"
	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	"github.com/flexinfer/flexinfer/internal/routing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func setupTestProxyWithRouting(t *testing.T) *Proxy {
	t.Helper()

	RegisterMetrics()

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, aiv1alpha1.AddToScheme(scheme))
	require.NoError(t, aiv1alpha2.AddToScheme(scheme))

	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	return &Proxy{
		client:           k8sClient,
		namespace:        "default",
		maxQueueSize:     100,
		queueTimeout:     60 * time.Second,
		coldStartTimeout: 60 * time.Second,
		router:           routing.NewRouter(),
		routingEnabled:   true,
	}
}

func TestRefreshServiceLabelCache_MultipleClaimants(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	// Create two services that share the same service labels
	svc1 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-a",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationServiceLabels: "abliterated,uncensored",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8000}},
		},
	}
	svc2 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-b",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationServiceLabels: "abliterated,uncensored",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8000}},
		},
	}
	require.NoError(t, p.client.Create(ctx, svc1))
	require.NoError(t, p.client.Create(ctx, svc2))

	p.refreshServiceLabelCache(ctx)

	// serviceLabelCache should still have first claimant (backward compat)
	val, ok := p.serviceLabelCache.Load("abliterated")
	require.True(t, ok)
	assert.Contains(t, []string{"model-a", "model-b"}, val)

	// labelGroupCache should have both claimants
	groupVal, ok := p.labelGroupCache.Load("abliterated")
	require.True(t, ok)
	assert.Len(t, groupVal, 2)
	assert.Contains(t, groupVal, "model-a")
	assert.Contains(t, groupVal, "model-b")

	// Same for "uncensored"
	groupVal2, ok := p.labelGroupCache.Load("uncensored")
	require.True(t, ok)
	assert.Len(t, groupVal2, 2)

	// labelGroupModels reverse index should map each model to both
	membersA, ok := p.labelGroupModels.Load("model-a")
	require.True(t, ok, "model-a should be in labelGroupModels")
	sort.Strings(membersA)
	assert.Equal(t, []string{"model-a", "model-b"}, membersA)

	membersB, ok := p.labelGroupModels.Load("model-b")
	require.True(t, ok, "model-b should be in labelGroupModels")
	sort.Strings(membersB)
	assert.Equal(t, []string{"model-a", "model-b"}, membersB)
}

func TestRefreshServiceLabelCache_SingleClaimant_NoGroup(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	// Create a single service with unique labels
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-solo",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationServiceLabels: "unique-label",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8000}},
		},
	}
	require.NoError(t, p.client.Create(ctx, svc))

	p.refreshServiceLabelCache(ctx)

	// serviceLabelCache should work as before
	val, ok := p.serviceLabelCache.Load("unique-label")
	require.True(t, ok)
	assert.Equal(t, "model-solo", val)

	// labelGroupCache should have the single claimant
	groupVal, ok := p.labelGroupCache.Load("unique-label")
	require.True(t, ok)
	assert.Len(t, groupVal, 1)

	// labelGroupModels should NOT have an entry (no group with <2 members)
	_, ok = p.labelGroupModels.Load("model-solo")
	assert.False(t, ok, "single-claimant model should not be in labelGroupModels")
}

func TestIsModelInLabelGroup(t *testing.T) {
	p := setupTestProxyWithRouting(t)

	// Not in any group initially
	assert.False(t, p.isModelInLabelGroup("model-a"))

	// Manually populate
	p.labelGroupModels.Store("model-a", []string{"model-a", "model-b"})

	assert.True(t, p.isModelInLabelGroup("model-a"))
	assert.False(t, p.isModelInLabelGroup("model-c"))
}

func TestRefreshEndpoints_LabelGroupAggregation(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	// Create two v1alpha2 Models with routing annotation (defense-in-depth)
	modelA := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-a",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationRouting: "least-loaded",
			},
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/a",
		},
	}
	modelB := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-b",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationRouting: "least-loaded",
			},
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/b",
		},
	}
	require.NoError(t, p.client.Create(ctx, modelA))
	require.NoError(t, p.client.Create(ctx, modelB))

	// Create services for both models
	svcA := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-a",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationServiceLabels: "shared-label",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"flexinfer.ai/model": "model-a"},
			Ports:    []corev1.ServicePort{{Port: 8000}},
		},
	}
	svcB := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-b",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationServiceLabels: "shared-label",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"flexinfer.ai/model": "model-b"},
			Ports:    []corev1.ServicePort{{Port: 8000}},
		},
	}
	require.NoError(t, p.client.Create(ctx, svcA))
	require.NoError(t, p.client.Create(ctx, svcB))

	// Create endpoints for both services
	epA := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-a",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8000}},
			},
		},
	}
	epB := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-b",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.2"}},
				Ports:     []corev1.EndpointPort{{Port: 8000}},
			},
		},
	}
	require.NoError(t, p.client.Create(ctx, epA))
	require.NoError(t, p.client.Create(ctx, epB))

	// First refresh labels so groups are built
	p.refreshServiceLabelCache(ctx)

	// Verify groups were built
	assert.True(t, p.isModelInLabelGroup("model-a"))
	assert.True(t, p.isModelInLabelGroup("model-b"))

	// Now refresh endpoints
	p.refreshEndpoints(ctx)

	// Each model's endpoint cache should have its own pod
	cachedA, ok := p.endpointCache.Load("model-a")
	require.True(t, ok, "model-a should have endpoint cache")
	assert.Contains(t, cachedA, "10.0.0.1:8000")

	cachedB, ok := p.endpointCache.Load("model-b")
	require.True(t, ok, "model-b should have endpoint cache")
	assert.Contains(t, cachedB, "10.0.0.2:8000")

	// But the router should have BOTH endpoints for each model (aggregated)
	// We can verify by routing — with least-loaded, it should pick from the combined pool
	target := p.router.Route("model-a", routing.StrategyLeastLoaded, nil, nil)
	assert.Contains(t, []string{"10.0.0.1:8000", "10.0.0.2:8000"}, target,
		"model-a router should have aggregated endpoints from both models")

	target = p.router.Route("model-b", routing.StrategyLeastLoaded, nil, nil)
	assert.Contains(t, []string{"10.0.0.1:8000", "10.0.0.2:8000"}, target,
		"model-b router should have aggregated endpoints from both models")
}

func TestGetRoutingStrategy_LabelGroup_DefaultsToLeastLoaded(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	// Create a model WITHOUT routing annotation
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-no-annotation",
			Namespace: "default",
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// Without label group membership, should return default
	strategy := p.getRoutingStrategy(ctx, "model-no-annotation")
	assert.Equal(t, routing.StrategyDefault, strategy)

	// Add to label group
	p.labelGroupModels.Store("model-no-annotation", []string{"model-no-annotation", "model-other"})

	// Now should return least-loaded
	strategy = p.getRoutingStrategy(ctx, "model-no-annotation")
	assert.Equal(t, routing.StrategyLeastLoaded, strategy)
}

func TestGetRoutingStrategy_ExplicitAnnotation_OverridesLabelGroup(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	// Create a model WITH explicit routing annotation
	m := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-with-prefix",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationRouting: "prefix",
			},
		},
		Spec: aiv1alpha2.ModelSpec{
			Backend: "vllm",
			Source:  "HF://test/model",
		},
	}
	require.NoError(t, p.client.Create(ctx, m))

	// Also add to label group
	p.labelGroupModels.Store("model-with-prefix", []string{"model-with-prefix", "model-other"})

	// Explicit annotation should win over label group default
	strategy := p.getRoutingStrategy(ctx, "model-with-prefix")
	assert.Equal(t, routing.StrategyPrefix, strategy)
}

func TestRefreshEndpoints_LabelGroup_PartialEndpoints(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	// Create two models, but only one has running endpoints
	modelA := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-up",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationRouting: "least-loaded",
			},
		},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://test/a"},
	}
	modelB := &aiv1alpha2.Model{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-down",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationRouting: "least-loaded",
			},
		},
		Spec: aiv1alpha2.ModelSpec{Backend: "vllm", Source: "HF://test/b"},
	}
	require.NoError(t, p.client.Create(ctx, modelA))
	require.NoError(t, p.client.Create(ctx, modelB))

	// Service for model-up
	svcUp := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-up",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationServiceLabels: "shared",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"flexinfer.ai/model": "model-up"},
			Ports:    []corev1.ServicePort{{Port: 8000}},
		},
	}
	// Service for model-down (no endpoints)
	svcDown := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-down",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationServiceLabels: "shared",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"flexinfer.ai/model": "model-down"},
			Ports:    []corev1.ServicePort{{Port: 8000}},
		},
	}
	require.NoError(t, p.client.Create(ctx, svcUp))
	require.NoError(t, p.client.Create(ctx, svcDown))

	// Only model-up has endpoints
	epUp := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-up",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
				Ports:     []corev1.EndpointPort{{Port: 8000}},
			},
		},
	}
	// model-down has empty endpoints
	epDown := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-down",
			Namespace: "default",
		},
		Subsets: []corev1.EndpointSubset{},
	}
	require.NoError(t, p.client.Create(ctx, epUp))
	require.NoError(t, p.client.Create(ctx, epDown))

	// Build label groups
	p.refreshServiceLabelCache(ctx)
	assert.True(t, p.isModelInLabelGroup("model-up"))
	assert.True(t, p.isModelInLabelGroup("model-down"))

	// Refresh endpoints
	p.refreshEndpoints(ctx)

	// Both models should route to model-up's endpoint (the only one available)
	target := p.router.Route("model-up", routing.StrategyLeastLoaded, nil, nil)
	assert.Equal(t, "10.0.0.1:8000", target)

	target = p.router.Route("model-down", routing.StrategyLeastLoaded, nil, nil)
	assert.Equal(t, "10.0.0.1:8000", target,
		"model-down should route to model-up's endpoint via label group aggregation")
}

func TestRefreshServiceLabelCache_ActiveAnnotationPrecedence(t *testing.T) {
	p := setupTestProxyWithRouting(t)
	ctx := context.Background()

	// Model A has active annotation with labels
	svcA := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-a",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationActiveServiceLabels: "shared-label",
				AnnotationServiceLabels:       "shared-label,extra-label",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8000}},
		},
	}
	// Model B has active annotation empty (preempted / inactive)
	svcB := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "model-b",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationActiveServiceLabels: "", // inactive in shared group
				AnnotationServiceLabels:       "shared-label",
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 8000}},
		},
	}
	require.NoError(t, p.client.Create(ctx, svcA))
	require.NoError(t, p.client.Create(ctx, svcB))

	p.refreshServiceLabelCache(ctx)

	// Only model-a should claim "shared-label" (model-b has empty active annotation)
	val, ok := p.serviceLabelCache.Load("shared-label")
	require.True(t, ok)
	assert.Equal(t, "model-a", val)

	// No label group since only one claimant
	_, ok = p.labelGroupModels.Load("model-a")
	assert.False(t, ok, "should not form a group when only one model has active labels")
}
