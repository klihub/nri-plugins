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

package generic

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/containers/nri-plugins/pkg/cpuallocator"
	logger "github.com/containers/nri-plugins/pkg/log"
	system "github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"

	config "github.com/containers/nri-plugins/pkg/apis/config/v1alpha1/resmgr/policy/generic"
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	"github.com/containers/nri-plugins/pkg/resmgr/events"
	policyapi "github.com/containers/nri-plugins/pkg/resmgr/policy"
)

const (
	PolicyName        = "generic"
	PolicyDescription = "A generic, hardware topology aware policy."
)

// GenericPolicy implements a fairly generic, topology aware resource assignment algorithm.
type GenericPolicy struct {
	*policy
}

type policy struct {
	cfg *config.Config
	sys system.System
	cch cache.Cache
	cpu cpuallocator.CPUAllocator

	allowed  cpuset.CPUSet // CPUs we are allowed to use
	reserved cpuset.CPUSet // system-/kube-reserved CPUs
	extra    int           // extra capacity in reserved

	root  *Pool
	pools []*Pool
}

var (
	// our logger instance
	log logger.Logger = logger.NewLogger("policy")
	// make sure policy implements the policy.Backend interface
	_ policyapi.Backend = &GenericPolicy{}
)

func New() policyapi.Backend {
	return &GenericPolicy{
		policy: &policy{},
	}
}

func (_ *policy) Name() string {
	return PolicyName
}

func (_ *policy) Description() string {
	return PolicyDescription
}

func (p *policy) Setup(opts *policyapi.BackendOptions) error {
	cfg, ok := opts.Config.(*config.Config)
	if !ok {
		return fmt.Errorf("config data of wrong type %T", opts.Config)
	}

	p.cfg = cfg
	p.sys = opts.System
	p.cch = opts.Cache
	p.cpu = cpuallocator.NewCPUAllocator(p.sys)

	if err := p.setupAllowedAndReservedCPUs(); err != nil {
		return err
	}

	log.Info("resource setup:")
	log.Info("  - allowed CPUs:  %s", p.allowed.String())
	log.Info("  - reserved CPUs: %s (%d milli-CPU extra capacity)", p.reserved.String(), p.extra)

	if err := p.setupPools(); err != nil {
		return err
	}

	return nil
}

func (p *policy) Start() error {
	log.Info("started...")
	return nil
}

func (p *policy) Reconfigure(newCfg interface{}) error {
	cfg, ok := newCfg.(*config.Config)
	if !ok {
		return fmt.Errorf("config data of wrong type %T", newCfg)
	}
	p.cfg = cfg
	return nil
}

func (p *policy) Sync(add []cache.Container, del []cache.Container) error {
	log.Info("synchronizing state...")
	return nil
}

func (p *policy) AllocateResources(container cache.Container) error {
	log.Info("allocating resources for %s...", container.PrettyName())
	return nil
}

func (p *policy) ReleaseResources(container cache.Container) error {
	log.Info("releasing resources of %s...", container.PrettyName())
	return nil
}

func (p *policy) UpdateResources(c cache.Container) error {
	log.Info("(not) updating container %s...", c.PrettyName())
	return nil
}

func (p *policy) HandleEvent(e *events.Policy) (bool, error) {
	log.Info("received policy event %s.%s with data %v...", e.Source, e.Type, e.Data)
	return true, nil
}

func (p *policy) DescribeMetrics() []*prometheus.Desc {
	return nil
}

func (p *policy) PollMetrics() policyapi.Metrics {
	return nil
}

func (p *policy) CollectMetrics(policyapi.Metrics) ([]prometheus.Metric, error) {
	return nil, nil
}

func (p *policy) GetTopologyZones() []*policyapi.TopologyZone {
	return nil
}

func (p *policy) ExportResourceData(c cache.Container) map[string]string {
	return nil
}

func (p *policy) setupAllowedAndReservedCPUs() error {
	online := p.sys.CPUSet().Difference(p.sys.Offlined())

	amount, kind := p.cfg.AvailableResources.Get(config.CPU)
	switch kind {
	case config.AmountCPUSet:
		cset, err := amount.ParseCPUSet()
		if err != nil {
			return fmt.Errorf("failed to setup available CPUs: %w", err)
		}
		p.allowed = cset

	case config.AmountQuantity:
		return fmt.Errorf("failed to setup available CPUs: %q given, cpuset expected", amount)

	case config.AmountAbsent:
		p.allowed = online.Clone()
	}

	amount, kind = p.cfg.ReservedResources.Get(config.CPU)
	switch kind {
	case config.AmountCPUSet:
		cset, err := amount.ParseCPUSet()
		if err != nil {
			return fmt.Errorf("failed to setup reserved CPUs: %w", err)
		}
		p.reserved = cset
		p.extra = 0

	case config.AmountQuantity:
		qty, err := amount.ParseQuantity()
		if err != nil {
			return fmt.Errorf("failed to setup reserved CPUs: %w", err)
		}
		ncpu := int((qty.MilliValue() + 999.0) / 1000.0)
		if ncpu < 1 {
			ncpu = 1
		}
		cpus := online.Clone()
		cset, err := p.cpu.AllocateCpus(&cpus, ncpu, cpuallocator.PriorityNormal)
		if err != nil {
			return fmt.Errorf("failed to setup reserved CPUs: %w", err)
		}
		p.reserved = cset
		p.extra = 1000*ncpu - int(qty.MilliValue())

	case config.AmountAbsent:
		return fmt.Errorf("failed to setup reserved CPUs: none given")
	}

	return nil
}
