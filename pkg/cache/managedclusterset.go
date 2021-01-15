package cache

import (
	"time"

	clusterinformerv1alpha1 "github.com/open-cluster-management/api/client/cluster/informers/externalversions/cluster/v1alpha1"
	clusterv1alpha1lister "github.com/open-cluster-management/api/client/cluster/listers/cluster/v1alpha1"
	clusterv1alpha1 "github.com/open-cluster-management/api/cluster/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	utilwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authentication/user"
	rbacv1informers "k8s.io/client-go/informers/rbac/v1"

	"github.com/open-cluster-management/clusterset-server/pkg/cache/rbac"
)

// ClusterSetLister enforces ability to enumerate clusterset based on role
type ClusterSetLister interface {
	// List returns the list of ManagedClusterr items that the user can access
	List(user user.Info, selector labels.Selector) (*clusterv1alpha1.ManagedClusterSetList, error)
}

type ClusterSetCache struct {
	cache            AuthCache
	clustersetLister clusterv1alpha1lister.ManagedClusterSetLister
}

func NewClusterSetCache(reviewer rbac.Reviewer,
	clustersetInformer clusterinformerv1alpha1.ManagedClusterSetInformer,
	clusterroleInformer rbacv1informers.ClusterRoleInformer,
	clusterrolebindingInformer rbacv1informers.ClusterRoleBindingInformer,
) *ClusterSetCache {
	clustersetCache := &ClusterSetCache{
		clustersetLister: clustersetInformer.Lister(),
	}
	authCache := NewAutchCache(
		reviewer, clusterroleInformer, clusterrolebindingInformer,
		"cluster.open-cluster-management.io", "managedclustersets",
		clustersetInformer.Informer(),
		clustersetCache.ListResources,
	)
	clustersetCache.cache = *authCache

	return clustersetCache
}

func (c *ClusterSetCache) ListResources() ([]*reviewRequest, error) {
	reqs := []*reviewRequest{}
	clusters, err := c.clustersetLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}

	for _, cluster := range clusters {
		req := &reviewRequest{
			name:            cluster.Name,
			resourceVersion: cluster.ResourceVersion,
		}
		reqs = append(reqs, req)
	}
	return reqs, nil
}

func (c *ClusterSetCache) List(userInfo user.Info, selector labels.Selector) (*clusterv1alpha1.ManagedClusterSetList, error) {
	names := c.cache.listNames(userInfo)

	clustersetList := &clusterv1alpha1.ManagedClusterSetList{}
	for key := range names {
		clusterset, err := c.clustersetLister.Get(key)
		if errors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}

		if !selector.Matches(labels.Set(clusterset.Labels)) {
			continue
		}
		clustersetList.Items = append(clustersetList.Items, *clusterset)
	}
	return clustersetList, nil
}

func (c *ClusterSetCache) ListObjects(userInfo user.Info) (runtime.Object, error) {
	return c.List(userInfo, labels.Everything())
}

func (c *ClusterSetCache) Get(name string) (runtime.Object, error) {
	return c.clustersetLister.Get(name)
}

func (c *ClusterSetCache) ConvertResource(name string) runtime.Object {
	cluster, err := c.clustersetLister.Get(name)
	if err != nil {
		cluster = &clusterv1alpha1.ManagedClusterSet{ObjectMeta: metav1.ObjectMeta{Name: name}}
	}

	return cluster
}

func (c *ClusterSetCache) RemoveWatcher(w CacheWatcher) {
	c.cache.RemoveWatcher(w)
}

func (c *ClusterSetCache) AddWatcher(w CacheWatcher) {
	c.cache.AddWatcher(w)
}

// Run begins watching and synchronizing the cache
func (c *ClusterSetCache) Run(period time.Duration) {
	go utilwait.Forever(func() { c.cache.synchronize() }, period)
}
