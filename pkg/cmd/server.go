// Copyright (c) 2020 Red Hat, Inc.

package cmd

import (
	clusterv1client "github.com/open-cluster-management/api/client/cluster/clientset/versioned"
	clusterv1informers "github.com/open-cluster-management/api/client/cluster/informers/externalversions"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	clusterv1alpha1 "github.com/open-cluster-management/api/cluster/v1alpha1"
	clusterapi "github.com/open-cluster-management/clusterset-server/pkg/apis/cluster"
	"github.com/open-cluster-management/clusterset-server/pkg/cache"
	"github.com/open-cluster-management/clusterset-server/pkg/cache/rbac"
	clusterregistry "github.com/open-cluster-management/clusterset-server/pkg/registry/managedcluster"
	clustersetregistry "github.com/open-cluster-management/clusterset-server/pkg/registry/managedclusterset"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/informers"
)

type FilterServer struct {
	*genericapiserver.GenericAPIServer
}

func NewFilterServer(
	client clusterv1client.Interface,
	informerFactory informers.SharedInformerFactory,
	clusterInformer clusterv1informers.SharedInformerFactory,
	apiServerConfig *genericapiserver.Config,
) (*FilterServer, error) {
	apiServer, err := apiServerConfig.Complete(informerFactory).New("filter-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	reviewer := rbac.NewReviewer(rbac.NewSubjectAccessEvaluator(
		informerFactory.Rbac().V1().ClusterRoles().Lister(),
		informerFactory.Rbac().V1().ClusterRoleBindings().Lister(),
		"cluster-amdin",
	))

	clusterCache := cache.NewClusterCache(
		reviewer,
		clusterInformer.Cluster().V1().ManagedClusters(),
		informerFactory.Rbac().V1().ClusterRoles(),
		informerFactory.Rbac().V1().ClusterRoleBindings(),
	)

	clustersetCache := cache.NewClusterSetCache(
		reviewer,
		clusterInformer.Cluster().V1alpha1().ManagedClusterSets(),
		informerFactory.Rbac().V1().ClusterRoles(),
		informerFactory.Rbac().V1().ClusterRoleBindings(),
	)

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(clusterapi.GroupName, clusterapi.Scheme, metav1.ParameterCodec, clusterapi.Codecs)
	v1storage := map[string]rest.Storage{
		"managedclusters": clusterregistry.NewREST(client, clusterCache, clusterCache, clusterInformer.Cluster().V1().ManagedClusters().Lister()),
	}
	v1alpha1storage := map[string]rest.Storage{
		"managedclustersets": clustersetregistry.NewREST(client, clustersetCache, clustersetCache, clusterInformer.Cluster().V1alpha1().ManagedClusterSets().Lister()),
	}
	apiGroupInfo.VersionedResourcesStorageMap[clusterv1.GroupVersion.Version] = v1storage
	apiGroupInfo.VersionedResourcesStorageMap[clusterv1alpha1.GroupVersion.Version] = v1alpha1storage
	if err := apiServer.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, err
	}

	apiServer.AddPostStartHook("start-informers", func(context genericapiserver.PostStartHookContext) error {
		informerFactory.Start(context.StopCh)
		clusterInformer.Start(context.StopCh)
		return nil
	})

	return &FilterServer{apiServer}, nil
}

func (p *FilterServer) Run(stopCh <-chan struct{}) error {
	return p.GenericAPIServer.PrepareRun().Run(stopCh)
}
