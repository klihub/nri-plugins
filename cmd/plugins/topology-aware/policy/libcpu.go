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

package topologyaware

import (
	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	libcpu "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
	corev1 "k8s.io/api/core/v1"
)

type LibCpu struct {
	policy  *policy
	account *libcpu.Accounting
}

type LibCpuRequest struct {
	lib *LibCpu
	ctr cache.Container
	opt *LibCpuOptions
}

type LibCpuOptions struct {
	lib *LibCpu
	ctr cache.Container
	/*
		full        int             // number of full CPUs requested
		fraction    int             // amount of fractional CPU requested
		limit       int             // CPU limit, MaxInt for no limit
		isolate     bool            // prefer isolated exclusive CPUs
		cpuType     cpuType         // preferred CPU type (normal, reserved)
		cpuClass    string          // requested or default CPU class
		prio        cpuPrio         // CPU priority preference, ignored for fraction requests
		memReq      int64           // memory request
		memLim      int64           // memory limit
		memType     memoryType      // requested types of memory
		pickByHints bool            // preference to pick resources by hints
		irqs        *IrqAffinity    // IRQ affinity for this request
		coldStart time.Duration
	*/

	qosClass       corev1.PodQOSClass
	cpuRequest     int
	cpuLimit       int
	preserveCpu    bool
	preferReserved bool
	preferShared   bool

	exclusive     int
	shared        int
	isolate       bool
	strictIsolate bool
	memRequest    int64
	memLimit      int64
}

type LibCpuSupply struct {
	lib  *LibCpu
	node Node
}

func (p *policy) NewLibCpu() *LibCpu {
	return &LibCpu{
		policy:  p,
		account: libcpu.NewAccounting(p.allowed),
	}
}

func (lib *LibCpu) NewRequest(ctr cache.Container) (*LibCpuRequest, error) {
	opt, err := lib.GetRequestOptions(ctr)
	if err != nil {
		return nil, err
	}
	return &LibCpuRequest{
		lib: lib,
		ctr: ctr,
		opt: opt,
	}, nil
}

func (lib *LibCpu) GetRequestOptions(ctr cache.Container) (*LibCpuOptions, error) {
	opt := &LibCpuOptions{
		ctr: ctr,
	}

	if err := opt.getBasicCpuPreferences(); err != nil {
		return nil, err
	}
	return nil, nil
}

func (o *LibCpuOptions) getBasicCpuPreferences() error {
	resources, ok := o.ctr.GetResourceUpdates()
	if !ok {
		resources = o.ctr.GetResourceRequirements()
	}

	o.qosClass = o.ctr.GetQOSClass()
	request := resources.Requests[corev1.ResourceCPU]
	limit := resources.Limits[corev1.ResourceCPU]
	o.cpuRequest = int(request.MilliValue())
	o.cpuLimit = int(limit.MilliValue())
	o.preserveCpu = o.ctr.PreserveCpuResources()

	return nil
}

func (lib *LibCpu) NewSupply(node Node) *LibCpuSupply {
	return &LibCpuSupply{
		lib:  lib,
		node: node,
	}
}

func (s *LibCpuSupply) Node() Node {
	return s.node
}

func (s *LibCpuSupply) IsolatedCpus() *libcpu.CpuMask {
	return s.node.AllowedCpus().Intersection(s.lib.policy.isolated)
}

func (s *LibCpuSupply) ReservedCpus() *libcpu.CpuMask {
	return s.node.AllowedCpus().Intersection(s.lib.policy.reserved)
}

func (s *LibCpuSupply) SharedCpus() *libcpu.CpuMask {
	return s.node.AllowedCpus().Difference(s.IsolatedCpus()).Difference(s.ReservedCpus())
}

func (s *LibCpuSupply) Clone() *LibCpuSupply {
	return &LibCpuSupply{
		lib:  s.lib,
		node: s.node,
	}
}

func (s *LibCpuSupply) IsolatedCPUs() *libcpu.CpuMask {
	return s.node.AllowedCpus().Intersection(s.lib.policy.isolated)
}

func (s *LibCpuSupply) ReservedCPUs() *libcpu.CpuMask {
	return s.node.AllowedCpus().Intersection(s.lib.policy.reserved)
}

func (s *LibCpuSupply) SharableCpus() *libcpu.CpuMask {
	cpus := s.node.AllowedCpus()
	cpus = cpus.Difference(s.lib.policy.isolated)
	cpus = cpus.Difference(s.lib.policy.reserved)
	cpus = cpus.Difference(s.lib.account.ExclusiveCpus())
	return cpus
}

func (s *LibCpuSupply) GrantedReserved() int {
	reserved := s.ReservedCPUs()
	if reserved.Size() == 0 {
		return 0
	}

	return 0
}

/*
type LibCpuRequest struct {
	lib *LibCpu
	ctr cache.Container
}

type LibCpuOffer struct {
	req  *LibCpuRequest
	node Node
	cpus *libcpu.CpuMask
}

type LibCpuGrant struct {
	lib  *LibCpu
	ctr  cache.Container
	node Node
}
*/
