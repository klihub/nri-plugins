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
	"github.com/containers/nri-plugins/pkg/topology"
)

type LibCpuSupply struct {
	lib  *LibCpu
	node Node
}

func (lib *LibCpu) NewSupply(node Node) *LibCpuSupply {
	if lib == nil {
		panic("nil lib")
	}

	return &LibCpuSupply{
		lib:  lib,
		node: node,
	}
}

func (s *LibCpuSupply) Node() Node {
	return s.node
}

func (s *LibCpuSupply) AllowedCpus() *libcpu.CpuMask {
	return s.node.AllowedCpus()
}

func (s *LibCpuSupply) ReservedCpus() *libcpu.CpuMask {
	return s.node.ReservedCpus()
}

func (s *LibCpuSupply) IsolatedCpus() *libcpu.CpuMask {
	return s.node.IsolatedCpus()
}

func (s *LibCpuSupply) SharedCpus() *libcpu.CpuMask {
	return s.node.AllowedCpus().Difference(
		s.IsolatedCpus().Union(s.ReservedCpus()),
	)
}

func (s *LibCpuSupply) Clone() *LibCpuSupply {
	return &LibCpuSupply{
		lib:  s.lib,
		node: s.node,
	}
}

func (s *LibCpuSupply) PickExclusiveCpus(req *LibCpuRequest) ([]*libcpu.CpuMask, error) {
	var (
		alternatives = []*libcpu.CpuMask{}
		hints        topology.Hints
	)

	if req.opt.PickByHints.BoolValue() {
		hints = req.ctr.GetTopologyHints()
	}

	if req.opt.IsolatedCpus.BoolValue() {
		free := s.IsolatedCpus().Difference(s.lib.ExclusiveCpus())
		if free.Size() < req.opt.ExclusiveCpu {
			if req.opt.IsolatedCpus.IsStrict() {
				return nil, fmt.Errorf("not enough required isolated CPUs available")
			}
		} else {
			cpus, err := s.lib.TakeCpuByHints(
				free,
				req.opt.ExclusiveCpu,
				req.opt.CpuPriority.CpuPrioValue(),
				hints,
			)
			if err != nil {
				log.Errorf("failed to take isolated CPUs: %v", err)
			} else {
				alternatives = append(alternatives, cpus...)
			}
		}
	}

	free := s.SharedCpus().Difference(s.lib.ExclusiveCpus())

	log.Debugf("libcpu: trying to pick %d exclusive CPUs from %s pool %s (free %s)",
		req.opt.ExclusiveCpu, s.node.Name(), s.SharedCpus(), free)

	if free.Size() >= req.opt.ExclusiveCpu {
		cpus, err := s.lib.TakeCpuByHints(
			free,
			req.opt.ExclusiveCpu,
			req.opt.CpuPriority.CpuPrioValue(),
			hints,
		)
		if err != nil {
			log.Errorf("failed to slice off shared CPUs: %v", err)
		} else {
			alternatives = append(alternatives, cpus...)
		}
	}

	if len(alternatives) == 0 {
		return nil, fmt.Errorf("failed to pick any exclusive CPUs")
	}

	return alternatives, nil
}
