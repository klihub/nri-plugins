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
	"fmt"

	libcpu "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
	libmem "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"
)

type LibCpuOffer struct {
	req    *LibCpuRequest
	supply *LibCpuSupply
	usage  *libcpu.CpuUsage
	cpu    *libcpu.Offer
	mem    *libmem.Offer
	cpus   *libcpu.CpuMask
	mems   *libmem.NodeMask
}

func (s *LibCpuSupply) GetOffer(req *LibCpuRequest) (*LibCpuOffer, error) {
	var (
		u   *libcpu.CpuUsage
		o   *libcpu.Offer
		err error
	)

	switch {
	case req.opt.PreserveCpu.BoolValue():
		if !s.node.IsRootNode() {
			log.Warnf("refusing to give preserved CPU offer from non-root %s",
				s.node.Name())
			return nil, nil
		}
		if req.opt.ExclusiveCpu != 0 {
			return nil, fmt.Errorf("preserved CPUs with non-zero exclusive CPUs")
		}

		u = &libcpu.CpuUsage{
			ID:     req.ctr.GetID(),
			Name:   req.ctr.GetName(),
			Shared: s.SharedCpus(),
			Charge: req.opt.SharedCpu,
		}
		o, err = s.lib.account.GetOffer(u)
		if err != nil {
			return nil, fmt.Errorf("failed to get preserved CPU offer: %w", err)
		}

	case req.opt.ReservedCpus.BoolValue():
		if req.opt.ExclusiveCpu != 0 {
			return nil, fmt.Errorf("reserved CPUs with non-zero exclusive CPUs")
		}

		if !s.ReservedCpus().IsEmpty() {
			u = &libcpu.CpuUsage{
				ID:     req.ctr.GetID(),
				Name:   req.ctr.GetName(),
				Shared: s.ReservedCpus(),
				Charge: req.opt.SharedCpu,
			}

			o, err = s.lib.account.GetOffer(u)
			if err != nil {
				return nil, fmt.Errorf("failed to get reserved CPU offer: %w", err)
			}
		}
		fallthrough

	case req.opt.ExclusiveCpu == 0:
		u = &libcpu.CpuUsage{
			ID:     req.ctr.GetID(),
			Name:   req.ctr.GetName(),
			Shared: s.SharedCpus(),
			Charge: req.opt.SharedCpu,
		}
		o, err = s.lib.account.GetOffer(u)
		if err != nil {
			return nil, fmt.Errorf("failed to get reserved CPU offer: %w", err)
		}

	default: //req.opt.ExclusiveCpu != 0
		alternatives, err := s.PickExclusiveCpus(req)
		if err != nil {
			return nil, fmt.Errorf("failed to pick exclusive CPUs: %w", err)
		}

		for _, exclusive := range alternatives {
			u := &libcpu.CpuUsage{
				ID:        req.ctr.GetID(),
				Name:      req.ctr.GetName(),
				Exclusive: exclusive,
			}
			if req.opt.SharedCpu != 0 {
				u.Shared = s.SharedCpus().Union(exclusive)
				u.Charge = req.opt.SharedCpu
			}
			o, err = s.lib.account.GetOffer(u)
			if err != nil {
				log.Warnf("failed to get CPU offer for exclusive %s: %v",
					exclusive, err)
			} else {
				break
			}
		}
	}

	mem, err := s.lib.policy.getMemOfferForLibCpu(s.node, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory offer for LibCpu request: %w", err)
	}

	return &LibCpuOffer{
		req:    req,
		supply: s,
		usage:  u,
		cpu:    o,
		mem:    mem,
		cpus:   o.Cpus(),
	}, nil
}

func (o *LibCpuOffer) Lib() *LibCpu {
	return o.supply.lib
}

func (o *LibCpuOffer) ExclusiveCpus() *libcpu.CpuMask {
	return o.cpus.Intersection(o.supply.lib.ExclusiveCpus())
}

func (o *LibCpuOffer) IsolatedCpus() *libcpu.CpuMask {
	return o.cpus.Intersection(o.supply.lib.IsolatedCpus())
}

func (o *LibCpuOffer) SharedCpus() *libcpu.CpuMask {
	return o.cpus.Difference(o.supply.lib.ExclusiveCpus())
}

func (o *LibCpuOffer) ReservedCpus() *libcpu.CpuMask {
	return o.cpus.Intersection(o.supply.lib.ReservedCpus())
}
