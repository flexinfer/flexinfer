// Package cache provides a shared cache of Kubernetes objects.
package cache

import (
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listers "k8s.io/client-go/listers/core/v1"
)

// Cache is a shared cache of Kubernetes objects.
type Cache struct {
	nodeLister      listers.NodeLister
	configMapLister listers.ConfigMapLister
	stopCh          chan struct{}
}

// NewCache creates a new Cache.
func NewCache(kubeClient kubernetes.Interface) *Cache {
	factory := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)
	nodeInformer := factory.Core().V1().Nodes()
	configMapInformer := factory.Core().V1().ConfigMaps()

	c := &Cache{
		nodeLister:      nodeInformer.Lister(),
		configMapLister: configMapInformer.Lister(),
		stopCh:          make(chan struct{}),
	}

	factory.Start(c.stopCh)
	factory.WaitForCacheSync(c.stopCh)

	return c
}

// Stop stops the cache's informers.
func (c *Cache) Stop() {
	close(c.stopCh)
}

// GetNode returns a node from the cache.
func (c *Cache) GetNode(name string) (*corev1.Node, error) {
	return c.nodeLister.Get(name)
}

// ListNodes returns all nodes from the cache.
func (c *Cache) ListNodes() ([]*corev1.Node, error) {
	return c.nodeLister.List(labels.Everything())
}

// GetConfigMap returns a configmap from the cache.
func (c *Cache) GetConfigMap(namespace, name string) (*corev1.ConfigMap, error) {
	return c.configMapLister.ConfigMaps(namespace).Get(name)
}
