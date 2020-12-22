// Copyright (c) 2020 Red Hat, Inc.

package app

import (
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"github.com/open-cluster-management/clusterset-server/cmd/server/app/options"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func Run(s *options.Options, stopCh <-chan struct{}) error {
	if err := s.SetDefaults(); err != nil {
		return err
	}

	if errs := s.Validate(); len(errs) != 0 {
		return utilerrors.NewAggregate(errs)
	}

	clusterCfg, err := clientcmd.BuildConfigFromFlags("", s.KubeConfigFile)
	if err != nil {
		return err
	}

	kubeClient, err := kubernetes.NewForConfig(clusterCfg)
	if err != nil {
		return err
	}

	informerFactory := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)
	informerFactory.Start(stopCh)

	apiServerConfig, err := s.APIServerConfig()
	if err != nil {
		return err
	}

	proxyServer, err := NewFilterServer(informerFactory, apiServerConfig)
	if err != nil {
		return err
	}
	return proxyServer.Run(stopCh)
}
