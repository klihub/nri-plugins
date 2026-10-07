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
	libmem "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"
)

type LibCpu struct {
	policy  *policy
	account *libcpu.Accounting
}

type LibCpuSupply struct {
	lib  *LibCpu
	node Node
}

type LibCpuRequest struct {
	lib *LibCpu
	ctr cache.Container
	opt *LibCpuOptions
}

type LibCpuOptions struct {
	*Preferences
}

type LibCpuOffer struct {
	req    *LibCpuRequest
	supply *LibCpuSupply
	cpu    *libcpu.Offer
	mem    *libmem.Offer
}

type LibCpuGrant struct {
	lib  *LibCpu
	node Node
	cpu  *libcpu.CpuMask
	mem  *libmem.NodeMask
}

func (p *policy) NewLibCpu() *LibCpu {
	return &LibCpu{
		policy:  p,
		account: libcpu.NewAccounting(p.allowed),
	}
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

func (s *LibCpuSupply) GetOffer(req *LibCpuRequest) (*LibCpuOffer, error) {
	return &LibCpuOffer{
		supply: s,
		req:    req,
	}, nil
}

func (lib *LibCpu) NewRequest(ctr cache.Container) (*LibCpuRequest, error) {
	prefs, err := lib.policy.GetContainerPreferences(ctr)
	if err != nil {
		return nil, err
	}
	return &LibCpuRequest{
		lib: lib,
		ctr: ctr,
		opt: &LibCpuOptions{
			Preferences: prefs,
		},
	}, nil
}
