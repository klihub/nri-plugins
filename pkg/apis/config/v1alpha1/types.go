// Copyright The NRI Plugins Authors. All Rights Reserved.
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

package v1alpha1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/instrumentation"
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/log"
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/control"
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy/balloons"
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy/template"
	"github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy/topologyaware"
)

// TopologyAwareConfig represents the configuration for the topology-aware policy.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +genclient
type TopologyAwareConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	TopologyAware topologyaware.Config `json:"topologyAware"`
	Common        CommonConfig         `json:"common,omitempty"`
	Status        ConfigStatus         `json:"status,omitempty"`
}

// TopologyAwareConfigList represents a list of TopologyAwareConfigs.
// +kubebuilder:object:root=true
type TopologyAwareConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []TopologyAwareConfig `json:"items"`
}

// BalloonsConfig represents the configuration for the balloons policy.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +genclient
type BalloonsConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Balloons balloons.Config `json:"balloons"`
	Common   CommonConfig    `json:"common,omitempty"`
	Status   ConfigStatus    `json:"status,omitempty"`
}

// BalloonsConfigList represents a list of BalloonsConfigs.
// +kubebuilder:object:root=true
type BalloonsConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []BalloonsConfig `json:"items"`
}

// TemplateConfig represents the configuration for the template policy.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +genclient
type TemplateConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Template template.Config `json:"template"`
	Common   CommonConfig    `json:"common,omitempty"`
	Status   ConfigStatus    `json:"status,omitempty"`
}

// TemplateConfigList represents a list of TemplateConfigs.
// +kubebuilder:object:root=true
type TemplateConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []TemplateConfig `json:"items"`
}

type CommonConfig struct {
	// +optional
	Control control.Config `json:"control"`
	// +optional
	Log log.Config `json:"log"`
	// +optional
	Instrumentation instrumentation.Config `json:"instrumentation"`
}

type ConfigStatus struct {
	Nodes map[string]NodeStatus `json:"nodes"`
}

type NodeStatus struct {
	// Status of activating the configuration on this node.
	// +kubebuilder:validation:Enum=Success;Failure
	Status string `json:"status"`
	// Generation is the generation the configuration this status was set for.
	Generation int64 `json:"generation"`
	// Error can provide further details of a configuration error.
	Error *string `json:"errors,omitempty"`
	// Last time of success/failure.
	Timestamp metav1.Time `json:"timestamp,omitempty"`
}

const (
	StatusSuccess = metav1.StatusSuccess
	StatusFailure = metav1.StatusFailure
)

func NewNodeStatus(err error, generation int64) *NodeStatus {
	s := &NodeStatus{
		Generation: generation,
		Timestamp:  metav1.Now(),
	}
	if err == nil {
		s.Status = StatusSuccess
		// TODO(klihub): kludge to 'patch away' any old errors from
		// lingering. Needs to be fixed properly...
		e := ""
		s.Error = &e
	} else {
		s.Status = StatusFailure
		e := fmt.Sprintf("%v", err)
		s.Error = &e
	}
	return s
}

// AnyConfig is a generic common interface we expect all configuration
// (root) types to implement. It provides access to the common metadata
// bits. It can be used to check if two configuration objects reference
// the same object instance on the apiserver without having to know the
// exact specific configuration types of these object. The agent uses it
// to filter out duplicate config update events when an expired watch is
// recreated.
// +kubebuilder:object:generate=false
type AnyConfig interface {
	GetObjectMeta() *metav1.ObjectMeta
}

// +kubebuilder:object:generate=false
type ResmgrConfig interface {
	CommonConfig() *CommonConfig
	PolicyConfig() interface{}
}

func (c *TopologyAwareConfig) GetObjectMeta() *metav1.ObjectMeta {
	if c == nil {
		return nil
	}
	return &c.ObjectMeta
}

func (c *TopologyAwareConfig) CommonConfig() *CommonConfig {
	if c == nil {
		return nil
	}
	return &c.Common
}

func (c *TopologyAwareConfig) PolicyConfig() interface{} {
	if c == nil {
		return nil
	}
	return &c.TopologyAware
}

func (c *BalloonsConfig) GetObjectMeta() *metav1.ObjectMeta {
	if c == nil {
		return nil
	}
	return &c.ObjectMeta
}

func (c *BalloonsConfig) CommonConfig() *CommonConfig {
	if c == nil {
		return nil
	}
	return &c.Common
}

func (c *BalloonsConfig) PolicyConfig() interface{} {
	if c == nil {
		return nil
	}
	return &c.Balloons
}

func (c *TemplateConfig) GetObjectMeta() *metav1.ObjectMeta {
	if c == nil {
		return nil
	}
	return &c.ObjectMeta
}

func (c *TemplateConfig) CommonConfig() *CommonConfig {
	if c == nil {
		return nil
	}
	return &c.Common
}

func (c *TemplateConfig) PolicyConfig() interface{} {
	if c == nil {
		return nil
	}
	return &c.Template
}

// Make sure our top-level configs implement the expected interfaces.
var (
	_ AnyConfig    = &TopologyAwareConfig{}
	_ ResmgrConfig = &TopologyAwareConfig{}
	_ ResmgrConfig = &BalloonsConfig{}
	_ AnyConfig    = &BalloonsConfig{}
	_ ResmgrConfig = &TemplateConfig{}
	_ AnyConfig    = &TemplateConfig{}
)

func init() {
	SchemeBuilder.Register(
		&TopologyAwareConfig{}, &TopologyAwareConfigList{},
		&BalloonsConfig{}, &BalloonsConfigList{},
		&TemplateConfig{}, &TemplateConfigList{},
	)
}
