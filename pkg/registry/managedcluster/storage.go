package managedcluster

import (
	"context"
	"fmt"

	clientset "github.com/open-cluster-management/api/client/cluster/clientset/versioned"
	clusterv1lister "github.com/open-cluster-management/api/client/cluster/listers/cluster/v1"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	"github.com/open-cluster-management/clusterset-server/pkg/cache"
	"k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

type REST struct {
	// client can modify Kubernetes namespaces
	client clientset.Interface
	// lister can enumerate project lists that enforce policy
	lister cache.ClusterLister

	clusterCache   *cache.ClusterCache
	clusterLister  clusterv1lister.ManagedClusterLister
	tableConverter rest.TableConvertor
}

// NewREST returns a RESTStorage object that will work against Project resources
func NewREST(client clientset.Interface, lister cache.ClusterLister, clusterCache *cache.ClusterCache, clusterLister clusterv1lister.ManagedClusterLister) *REST {
	return &REST{
		client: client,
		lister: lister,

		clusterCache:   clusterCache,
		clusterLister:  clusterLister,
		tableConverter: rest.NewDefaultTableConvertor(clusterv1.Resource("managedclusters")),
	}
}

// New returns a new Project
func (s *REST) New() runtime.Object {
	return &clusterv1.ManagedCluster{}
}

func (s *REST) NamespaceScoped() bool {
	return false
}

// NewList returns a new ProjectList
func (*REST) NewList() runtime.Object {
	return &clusterv1.ManagedClusterList{}
}

var _ = rest.Lister(&REST{})

// List retrieves a list of Projects that match label.
func (s *REST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	user, ok := request.UserFrom(ctx)
	if !ok {
		return nil, errors.NewForbidden(clusterv1.Resource("managedclusters"), "", fmt.Errorf("unable to list projects without a user on the context"))
	}
	clusterList, err := s.lister.List(user)
	if err != nil {
		return nil, err
	}

	return clusterList, nil
}

func (c *REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return c.tableConverter.ConvertToTable(ctx, object, tableOptions)
}

var _ = rest.Watcher(&REST{})

func (s *REST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	if ctx == nil {
		return nil, fmt.Errorf("Context is nil")
	}
	userInfo, exists := request.UserFrom(ctx)
	if !exists {
		return nil, fmt.Errorf("no user")
	}

	includeAllExistingClusters := (options != nil) && options.ResourceVersion == "0"

	// allowedNamespaces are the namespaces allowed by scopes.  kube has no scopess, see all
	allowedClusters := sets.NewString("*")

	watcher := cache.NewCacheWatcher(userInfo, allowedClusters, s.clusterCache, includeAllExistingClusters)
	s.clusterCache.AddWatcher(watcher)

	go watcher.Watch()
	return watcher, nil
}

var _ = rest.Getter(&REST{})

// Get retrieves a Project by name
func (s *REST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	return s.client.ClusterV1().ManagedClusters().Get(ctx, name, metav1.GetOptions{})
}
