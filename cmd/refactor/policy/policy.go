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

package refactored

import (
	"github.com/prometheus/client_golang/prometheus"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	"github.com/containers/nri-plugins/pkg/resmgr/events"
	"github.com/containers/nri-plugins/pkg/resmgr/introspect"
	"github.com/containers/nri-plugins/pkg/resmgr/policy"
	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

const (
	// PolicyName is the well-known name of this policy.
	PolicyName = "refactor"
	// PolicyDescription is a short description of this policy.
	PolicyDescription = "topology-aware policy being refactored"
	// ConfigPath is the path of this policy in the configuration hierarchy.
	PolicyPath = "policy." + PolicyName
)

// policy implements the topology-aware resource assignment policy.
type topologyaware struct {
	cache cache.Cache
	sysfs sysfs.System
	cpus  struct {
		available cpuset.CPUSet
		reserved  cpuset.CPUSet
		isolated  cpuset.CPUSet
		unusable  cpuset.CPUSet
	}
}

var (
	// Our logger instance.
	log logger.Logger = logger.Get("policy")

	// Our runtime configuration.
	cfg = defaultConfig().(*Config)

	// Make sure we implement the policy.Backend interface.
	_ policy.Backend = &topologyaware{}
)

// CreatePolicy creates a new policy instance.
func CreatePolicy(opts *policy.BackendOptions) policy.Backend {
	p := &topologyaware{
		sysfs: opts.System,
		cache: opts.Cache,
	}
	return p
}

// Name returns the well-known name of this policy.
func (p *topologyaware) Name() string {
	return PolicyName
}

// Description returns a short description of this policy.
func (p *topologyaware) Description() string {
	return PolicyDescription
}

// Start prepares this policy for accepting allocation/release requests.
func (p *topologyaware) Start(add []cache.Container, del []cache.Container) error {
	return p.Sync(add, del)
}

// Sync synchronizes the state of this policy.
func (p *topologyaware) Sync(add []cache.Container, del []cache.Container) error {
	log.Info("synchronizing state...")
	return nil
}

// AllocateResources is a resource allocation request for this policy.
func (p *topologyaware) AllocateResources(container cache.Container) error {
	log.Info("allocating resources for %s...", container.PrettyName())
	return nil
}

// ReleaseResources is a resource release request for this policy.
func (p *topologyaware) ReleaseResources(container cache.Container) error {
	log.Info("releasing resources of %s...", container.PrettyName())
	return nil
}

// UpdateResources is a resource allocation update request for this policy.
func (p *topologyaware) UpdateResources(c cache.Container) error {
	log.Info("(not) updating container %s...", c.PrettyName())
	return nil
}

// Rebalance tries to find an optimal allocation of resources for the current containers.
func (p *topologyaware) Rebalance() (bool, error) {
	return true, nil
}

// HandleEvent handles policy-specific events.
func (p *topologyaware) HandleEvent(e *events.Policy) (bool, error) {
	log.Info("received policy event %s.%s with data %v...", e.Source, e.Type, e.Data)
	return true, nil
}

// Introspect provides data for external introspection.
func (p *topologyaware) Introspect(state *introspect.State) {
	return
}

// DescribeMetrics generates policy-specific prometheus metrics data descriptors.
func (p *topologyaware) DescribeMetrics() []*prometheus.Desc {
	return nil
}

// PollMetrics provides policy metrics for monitoring.
func (p *topologyaware) PollMetrics() policy.Metrics {
	return nil
}

// CollectMetrics generates prometheus metrics from cached/polled policy-specific metrics data.
func (p *topologyaware) CollectMetrics(policy.Metrics) ([]prometheus.Metric, error) {
	return nil, nil
}

// GetTopologyZones returns the policy/pool data for 'topology zone' CRDs.
func (p *topologyaware) GetTopologyZones() []*policy.TopologyZone {
	return nil
}

// ExportResourceData provides resource data to export for the container.
func (p *topologyaware) ExportResourceData(c cache.Container) map[string]string {
	return nil
}

// Initialize or reinitialize the policy.
func (p *topologyaware) initialize() error {
	return nil
}

// Register us as a policy implementation.
func init() {
	policy.Register(PolicyName, PolicyDescription, CreatePolicy)
}
