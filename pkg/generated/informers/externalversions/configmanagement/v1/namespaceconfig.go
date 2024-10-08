// Code generated by informer-gen. DO NOT EDIT.

package v1

import (
	"context"
	time "time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
	configmanagementv1 "kpt.dev/configsync/pkg/api/configmanagement/v1"
	versioned "kpt.dev/configsync/pkg/generated/clientset/versioned"
	internalinterfaces "kpt.dev/configsync/pkg/generated/informers/externalversions/internalinterfaces"
	v1 "kpt.dev/configsync/pkg/generated/listers/configmanagement/v1"
)

// NamespaceConfigInformer provides access to a shared informer and lister for
// NamespaceConfigs.
type NamespaceConfigInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() v1.NamespaceConfigLister
}

type namespaceConfigInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
}

// NewNamespaceConfigInformer constructs a new informer for NamespaceConfig type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewNamespaceConfigInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredNamespaceConfigInformer(client, resyncPeriod, indexers, nil)
}

// NewFilteredNamespaceConfigInformer constructs a new informer for NamespaceConfig type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredNamespaceConfigInformer(client versioned.Interface, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.ConfigmanagementV1().NamespaceConfigs().List(context.TODO(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.ConfigmanagementV1().NamespaceConfigs().Watch(context.TODO(), options)
			},
		},
		&configmanagementv1.NamespaceConfig{},
		resyncPeriod,
		indexers,
	)
}

func (f *namespaceConfigInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredNamespaceConfigInformer(client, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *namespaceConfigInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&configmanagementv1.NamespaceConfig{}, f.defaultInformer)
}

func (f *namespaceConfigInformer) Lister() v1.NamespaceConfigLister {
	return v1.NewNamespaceConfigLister(f.Informer().GetIndexer())
}
