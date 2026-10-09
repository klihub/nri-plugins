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

	"github.com/containers/nri-plugins/pkg/resmgr/cache"
	libcpu "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
	libmem "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"
)

type LibCpuGrant struct {
	lib  *LibCpu
	node Node
	ctr  cache.Container
	opt  *LibCpuOptions
	cpu  *libcpu.CpuMask
	mem  libmem.NodeMask
}

type LibCpuUpdates struct {
	cpu []libcpu.Change
	mem map[string]libmem.NodeMask
}

func (o *LibCpuOffer) Commit() (*LibCpuGrant, *LibCpuUpdates, error) {
	if o == nil || o.cpu == nil {
		return nil, nil, fmt.Errorf("libcpu: can't commit, nil offer")
	}

	cpu, cpuChanges, err := o.cpu.Commit()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to commit libcpu offer: %w", err)
	}

	/*
		mem, memUpdates, err := o.mem.Commit()
		if err != nil {
			o.supply.lib.account.Release(o.req.ctr.GetID())
			return nil, nil, fmt.Errorf("failed to commit libmem offer: %w", err)
		}*/

	return &LibCpuGrant{
			lib:  o.supply.lib,
			node: o.supply.node,
			ctr:  o.req.ctr,
			opt:  o.req.opt,
			cpu:  cpu,
			//mem:  mem,
		}, &LibCpuUpdates{
			cpu: cpuChanges,
			//mem: memUpdates,
		}, nil
}

func (g *LibCpuGrant) ExclusiveCpus() *libcpu.CpuMask {
	return g.cpu.Intersection(g.lib.ExclusiveCpus())
}

func (g *LibCpuGrant) IsolatedCpus() *libcpu.CpuMask {
	return g.cpu.Intersection(g.lib.IsolatedCpus())
}

func (g *LibCpuGrant) SharedCpus() *libcpu.CpuMask {
	return g.cpu.Difference(g.lib.ExclusiveCpus())
}

func (g *LibCpuGrant) ReservedCpus() *libcpu.CpuMask {
	return g.cpu.Intersection(g.lib.ReservedCpus())
}

func (g *LibCpuGrant) Release() ([]libcpu.Change, error) {
	if g == nil || g.cpu == nil {
		return nil, fmt.Errorf("libcpu: can't release, nil grant")
	}

	updates, err := g.lib.account.Release(g.ctr.GetID())
	if err != nil {
		return nil, err
	}

	g.cpu = nil

	return updates, nil
}

func (g *LibCpuGrant) Verify(grant Grant) error {
	reserved := grant.ReservedCPUs()
	shared := grant.SharedCPUs()
	exclusive := grant.ExclusiveCPUs()

	if grant.CPUType() == cpuReserved {
		log.Infof("LibCpu-grant: reserved %q", g.SharedCpus())
		log.Infof("       grant: reserved %q", reserved)

		if !g.SharedCpus().Equals(reserved) {
			return fmt.Errorf("reserved CPU mismatch: libcpu: %s != grant %s",
				g.SharedCpus(), reserved)
		}
		log.Infof("LibCpu-grant: reserved check OK (%s == %s)", g.SharedCpus(), reserved)
		return nil
	}

	log.Infof("LibCpu-grant: exclusive %q, shared %q",
		g.ExclusiveCpus(), g.SharedCpus())
	log.Infof("       grant: exclusive %q, shared %q",
		grant.ExclusiveCPUs(), grant.SharedCPUs())

	if !g.ExclusiveCpus().Equals(exclusive) {
		return fmt.Errorf("exclusive CPU mismatch: libcpu: %s != grant %s",
			g.ExclusiveCpus(), exclusive)
		log.Infof("LibCpu-grant: exclusive check OK (%s == %s)",
			g.ExclusiveCpus(), exclusive)
	}

	if g.opt.SharedCpu > 0 && !g.SharedCpus().Equals(shared) {
		return fmt.Errorf("shared CPU mismatch: libcpu: %s != grant %s",
			g.SharedCpus(), shared)
		log.Infof("LibCpu-grant: shared check OK (%s == %s)",
			g.SharedCpus(), shared)
	}

	return nil
}
