// Copyright (c) 2020 Red Hat, Inc.

package cmd

import (
	clusterv1informers "github.com/open-cluster-management/api/client/cluster/informers/externalversions"
	clusterv1 "github.com/open-cluster-management/api/cluster/v1"
	clusterv1alpha1 "github.com/open-cluster-management/api/cluster/v1alpha1"
	clusterapi "github.com/open-cluster-management/clusterset-server/pkg/apis/cluster"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/informers"
)

type FilterServer struct {
	*genericapiserver.GenericAPIServer
}

func NewFilterServer(
	informerFactory informers.SharedInformerFactory,
	clusterInformer clusterv1informers.SharedInformerFactory,
	apiServerConfig *genericapiserver.Config,
) (*FilterServer, error) {
	apiServer, err := apiServerConfig.Complete(informerFactory).New("filter-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(clusterapi.GroupName, clusterapi.Scheme, metav1.ParameterCodec, clusterapi.Codecs)
	v1storage := map[string]rest.Storage{}
	v1alpha1storage := map[string]rest.Storage{}
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
