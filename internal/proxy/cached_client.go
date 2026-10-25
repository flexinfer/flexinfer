package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	aiv1alpha2 "github.com/flexinfer/flexinfer/api/v1alpha2"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// cachedReadObjects are the kinds the request path reads on every call:
// Models (several Gets per request), Services (backend port) and LoRAAdapters
// (listed twice per request). They are served from a namespace-scoped
// informer cache. Every other kind and every write goes to the API server, so
// a kind missing list/watch RBAC (pods, nodes) never stalls on an informer.
// Each kind here needs get/list/watch on the proxy Role.
func cachedReadObjects() []client.Object {
	return []client.Object{&aiv1alpha2.Model{}, &corev1.Service{}, &aiv1alpha2.LoRAAdapter{}}
}

func isCachedRead(obj runtime.Object) bool {
	switch obj.(type) {
	case *aiv1alpha2.Model, *aiv1alpha2.ModelList,
		*corev1.Service, *corev1.ServiceList,
		*aiv1alpha2.LoRAAdapter, *aiv1alpha2.LoRAAdapterList:
		return true
	}
	return false
}

// cachedReadClient reads cachedReadObjects in its namespace from cache and
// delegates everything else, including Status() and SubResource() writers,
// to the embedded direct client.
type cachedReadClient struct {
	client.Client
	cache     client.Reader
	namespace string
}

func (c *cachedReadClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Namespace == c.namespace && isCachedRead(obj) {
		return c.cache.Get(ctx, key, obj, opts...)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *cachedReadClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := &client.ListOptions{}
	listOpts.ApplyOptions(opts)
	if listOpts.Namespace == c.namespace && isCachedRead(list) {
		return c.cache.List(ctx, list, opts...)
	}
	return c.Client.List(ctx, list, opts...)
}

// NewCachedReadClient starts a namespace-scoped informer cache for
// cachedReadObjects and returns a client that reads them from it. It returns
// an error, and the caller should keep using direct, if the proxy may not
// watch every cached kind or the informers do not sync within syncTimeout.
func NewCachedReadClient(ctx context.Context, cfg *rest.Config, direct client.Client, namespace string, syncTimeout time.Duration) (client.Client, error) {
	// Without watch an informer still syncs from its initial LIST and then
	// never changes, so check the permission instead of trusting the sync.
	if err := checkCanWatch(ctx, direct, namespace); err != nil {
		return nil, err
	}
	informers, err := cache.New(cfg, cache.Options{
		Scheme:                      direct.Scheme(),
		Mapper:                      direct.RESTMapper(),
		DefaultNamespaces:           map[string]cache.Config{namespace: {}},
		ReaderFailOnMissingInformer: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create informer cache: %w", err)
	}
	// Register every informer up front: with ReaderFailOnMissingInformer a
	// read never creates one lazily.
	for _, obj := range cachedReadObjects() {
		if _, err := informers.GetInformer(ctx, obj, cache.BlockUntilSynced(false)); err != nil {
			return nil, fmt.Errorf("register %T informer: %w", obj, err)
		}
	}
	// The informers run until ctx ends; stop them only when they fail to sync.
	cacheCtx, stopCache := context.WithCancel(ctx)
	synced := false
	defer func() {
		if !synced {
			stopCache()
		}
	}()
	go func() {
		if err := informers.Start(cacheCtx); err != nil {
			slog.Error("informer cache stopped", "error", err)
		}
	}()

	syncCtx, cancel := context.WithTimeout(ctx, syncTimeout)
	defer cancel()
	if !informers.WaitForCacheSync(syncCtx) {
		return nil, fmt.Errorf("informer cache did not sync within %s", syncTimeout)
	}
	synced = true
	return &cachedReadClient{Client: direct, cache: informers, namespace: namespace}, nil
}

// checkCanWatch asks the API server whether this identity may watch every
// cachedReadObjects kind in namespace.
func checkCanWatch(ctx context.Context, c client.Client, namespace string) error {
	for _, obj := range cachedReadObjects() {
		gvk, err := apiutil.GVKForObject(obj, c.Scheme())
		if err != nil {
			return err
		}
		mapping, err := c.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("map %s: %w", gvk.Kind, err)
		}
		review := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Namespace: namespace,
					Verb:      "watch",
					Group:     mapping.Resource.Group,
					Resource:  mapping.Resource.Resource,
				},
			},
		}
		if err := c.Create(ctx, review); err != nil {
			return fmt.Errorf("check watch on %s: %w", mapping.Resource.Resource, err)
		}
		if !review.Status.Allowed {
			return fmt.Errorf("not allowed to watch %s in %s", mapping.Resource.Resource, namespace)
		}
	}
	return nil
}
