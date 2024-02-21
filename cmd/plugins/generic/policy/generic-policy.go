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

/*
type Pool struct {
	name     string
	kind     PoolKind
	cpus     cpuset.CPUSet
	parent   *Pool
	children []*Pool
}

type PoolKind int

const (
	PoolVirtualRoot PoolKind = iota
	PoolSocket
	PoolDie
	PoolNumaNode
)

func (p *policy) setupPools() error {
	root := &Pool{
		name: "virtual root",
		kind: PoolVirtualRoot,
		cpus: p.allowed.Clone(),
	}
	log.Debug("setting up pool %s...", root.name)

	children := map[*Pool][]*Pool{}
	dieNodes := map[idset.ID]idset.ID{}

	for _, pkgID := range p.sys.PackageIDs() {
		pkg := p.sys.Package(pkgID)
		pkgPool := &Pool{
			name:   fmt.Sprintf("socket #%d", pkgID),
			kind:   PoolSocket,
			cpus:   pkg.CPUSet().Intersection(p.allowed),
			parent: root,
		}
		children[root] = append(children[root], pkgPool)
		log.Debug("setting up pool %s...", pkgPool.name)
		for _, dieID := range pkg.DieIDs() {
			diePool := &Pool{
				name:   fmt.Sprintf("die #%d:%d", pkgID, dieID),
				kind:   PoolDie,
				cpus:   pkg.DieCPUSet(dieID).Intersection(p.allowed),
				parent: pkgPool,
			}
			children[pkgPool] = append(children[pkgPool], diePool)
			log.Debug("setting up pool %s...", diePool.name)
			for _, nodeID := range pkg.DieNodeIDs(dieID) {
				if otherDie, ok := dieNodes[nodeID]; ok {
					return fmt.Errorf("failed to setup pools, NUMA node #%d in dies #%d, %#d",
						nodeID, otherDie, dieID)
				}
				dieNodes[nodeID] = dieID
				nodePool := &Pool{
					name:   fmt.Sprintf("NUMA node #%d", nodeID),
					kind:   PoolNumaNode,
					cpus:   p.sys.Node(nodeID).CPUSet().Intersection(p.allowed),
					parent: diePool,
				}
				children[diePool] = append(children[diePool], nodePool)
				log.Debug("setting up pool %s...", nodePool.name)
			}
		}
	}

	for p, child := range children {
		log.Debug("- pool %s has %d children", p.name, len(child))
		for _, c := range child {
			log.Debug("  + %s", c.name)
		}
		if len(child) == 1 {
			log.Info("  o pool %s is only child, filtering it out...", child[0].name)
			children[p] = append(children[p], children[child[0]]...)
		}
	}

	for p, child := range children {
		log.Debug("- pool %s has %d children", p.name, len(child))
		for _, c := range child {
			log.Debug("  + %s", c.name)
		}
	}

	return nil
}
*/
