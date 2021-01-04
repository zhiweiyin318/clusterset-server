// Copyright (c) 2020 Red Hat, Inc.

package cmd

import (
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	clusterv1client "github.com/open-cluster-management/api/client/cluster/clientset/versioned"
	clusterv1informers "github.com/open-cluster-management/api/client/cluster/informers/externalversions"
	"github.com/open-cluster-management/clusterset-server/pkg/cmd/options"
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

	clusterClient, err := clusterv1client.NewForConfig(clusterCfg)
	if err != nil {
		return err
	}
	clusterInformers := clusterv1informers.NewSharedInformerFactory(clusterClient, 10*time.Minute)

	informerFactory := informers.NewSharedInformerFactory(kubeClient, 10*time.Minute)

	apiServerConfig, err := s.APIServerConfig()
	if err != nil {
		return err
	}

	proxyServer, err := NewFilterServer(clusterClient, informerFactory, clusterInformers, apiServerConfig)
	if err != nil {
		return err
	}

	informerFactory.Start(stopCh)
	clusterInformers.Start(stopCh)
	return proxyServer.Run(stopCh)
}
