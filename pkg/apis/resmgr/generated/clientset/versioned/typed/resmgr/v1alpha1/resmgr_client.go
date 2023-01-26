// Copyright 2019-2020 Intel Corporation. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by client-gen. DO NOT EDIT.

package v1alpha1

import (
	"net/http"

	"github.com/intel/nri-resmgr/pkg/apis/resmgr/generated/clientset/versioned/scheme"
	v1alpha1 "github.com/intel/nri-resmgr/pkg/apis/resmgr/v1alpha1"
	rest "k8s.io/client-go/rest"
)

type NriresmgrV1alpha1Interface interface {
	RESTClient() rest.Interface
	AdjustmentsGetter
}

// NriresmgrV1alpha1Client is used to interact with features provided by the nriresmgr.intel.com group.
type NriresmgrV1alpha1Client struct {
	restClient rest.Interface
}

func (c *NriresmgrV1alpha1Client) Adjustments(namespace string) AdjustmentInterface {
	return newAdjustments(c, namespace)
}

// NewForConfig creates a new NriresmgrV1alpha1Client for the given config.
// NewForConfig is equivalent to NewForConfigAndClient(c, httpClient),
// where httpClient was generated with rest.HTTPClientFor(c).
func NewForConfig(c *rest.Config) (*NriresmgrV1alpha1Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	httpClient, err := rest.HTTPClientFor(&config)
	if err != nil {
		return nil, err
	}
	return NewForConfigAndClient(&config, httpClient)
}

// NewForConfigAndClient creates a new NriresmgrV1alpha1Client for the given config and http client.
// Note the http client provided takes precedence over the configured transport values.
func NewForConfigAndClient(c *rest.Config, h *http.Client) (*NriresmgrV1alpha1Client, error) {
	config := *c
	if err := setConfigDefaults(&config); err != nil {
		return nil, err
	}
	client, err := rest.RESTClientForConfigAndClient(&config, h)
	if err != nil {
		return nil, err
	}
	return &NriresmgrV1alpha1Client{client}, nil
}

// NewForConfigOrDie creates a new NriresmgrV1alpha1Client for the given config and
// panics if there is an error in the config.
func NewForConfigOrDie(c *rest.Config) *NriresmgrV1alpha1Client {
	client, err := NewForConfig(c)
	if err != nil {
		panic(err)
	}
	return client
}

// New creates a new NriresmgrV1alpha1Client for the given RESTClient.
func New(c rest.Interface) *NriresmgrV1alpha1Client {
	return &NriresmgrV1alpha1Client{c}
}

func setConfigDefaults(config *rest.Config) error {
	gv := v1alpha1.SchemeGroupVersion
	config.GroupVersion = &gv
	config.APIPath = "/apis"
	config.NegotiatedSerializer = scheme.Codecs.WithoutConversion()

	if config.UserAgent == "" {
		config.UserAgent = rest.DefaultKubernetesUserAgent()
	}

	return nil
}

// RESTClient returns a RESTClient that is used to communicate
// with API server by this client implementation.
func (c *NriresmgrV1alpha1Client) RESTClient() rest.Interface {
	if c == nil {
		return nil
	}
	return c.restClient
}
