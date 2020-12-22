// Copyright (c) 2020 Red Hat, Inc.

package app

import (
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/informers"
)

type FilterServer struct {
	*genericapiserver.GenericAPIServer
}

func NewFilterServer(
	informerFactory informers.SharedInformerFactory,
	apiServerConfig *genericapiserver.Config,
) (*FilterServer, error) {
	apiServer, err := apiServerConfig.Complete(informerFactory).New("filter-server", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, err
	}

	return &FilterServer{apiServer}, nil
}

func (p *FilterServer) Run(stopCh <-chan struct{}) error {
	return p.GenericAPIServer.PrepareRun().Run(stopCh)
}
