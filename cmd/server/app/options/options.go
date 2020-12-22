// Copyright (c) 2020 Red Hat, Inc.

package options

import (
	"fmt"

	"github.com/spf13/pflag"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericapiserveroptions "k8s.io/apiserver/pkg/server/options"
)

type Options struct {
	KubeConfigFile string
	ServerRun      *genericapiserveroptions.ServerRunOptions
	SecureServing  *genericapiserveroptions.SecureServingOptionsWithLoopback
	Authentication *genericapiserveroptions.DelegatingAuthenticationOptions
	Authorization  *genericapiserveroptions.DelegatingAuthorizationOptions
}

// NewOptions constructs a new set of default options for proxyserver.
func NewOptions() *Options {
	return &Options{
		KubeConfigFile: "",
		ServerRun:      genericapiserveroptions.NewServerRunOptions(),
		SecureServing:  genericapiserveroptions.NewSecureServingOptions().WithLoopback(),
		Authentication: genericapiserveroptions.NewDelegatingAuthenticationOptions(),
		Authorization:  genericapiserveroptions.NewDelegatingAuthorizationOptions(),
	}
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.KubeConfigFile, "kube-config-file", o.KubeConfigFile, "Kubernetes configuration file to connect to kube-apiserver")
	o.ServerRun.AddUniversalFlags(fs)
	o.SecureServing.AddFlags(fs)
	o.Authentication.AddFlags(fs)
	o.Authorization.AddFlags(fs)
}

func (o *Options) SetDefaults() error {
	if err := o.ServerRun.DefaultAdvertiseAddress(o.SecureServing.SecureServingOptions); err != nil {
		return err
	}

	if err := o.SecureServing.MaybeDefaultWithSelfSignedCerts(o.ServerRun.AdvertiseAddress.String(), nil, nil); err != nil {
		return fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	return nil
}

func (o *Options) APIServerConfig() (*genericapiserver.Config, error) {
	scheme := runtime.NewScheme()
	metav1.AddToGroupVersion(scheme, metav1.SchemeGroupVersion)
	serverConfig := genericapiserver.NewConfig(serializer.NewCodecFactory(scheme))
	if err := o.ServerRun.ApplyTo(serverConfig); err != nil {
		return nil, err
	}

	if err := o.SecureServing.ApplyTo(&serverConfig.SecureServing, &serverConfig.LoopbackClientConfig); err != nil {
		return nil, err
	}

	if err := o.Authentication.ApplyTo(&serverConfig.Authentication, serverConfig.SecureServing, nil); err != nil {
		return nil, err
	}

	//TODO: add custormer authorization here
	if err := o.Authorization.ApplyTo(&serverConfig.Authorization); err != nil {
		return nil, err
	}

	return serverConfig, nil
}

func (o *Options) Validate() []error {
	var errors []error
	if errs := o.ServerRun.Validate(); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	if errs := o.SecureServing.Validate(); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	if errs := o.Authentication.Validate(); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	if errs := o.Authorization.Validate(); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// TODO: add more checks
	return errors
}
