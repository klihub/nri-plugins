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

	"github.com/containers/nri-plugins/pkg/agent/podresapi"
	libcpu "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
	"github.com/containers/nri-plugins/pkg/topology"
)

type LibCpu struct {
	policy  *policy
	account *libcpu.Accounting
}

func (p *policy) NewLibCpu() *LibCpu {
	return &LibCpu{
		policy:  p,
		account: libcpu.NewAccounting(p.allowed),
	}
}

func (lib *LibCpu) AllowedCpus() *libcpu.CpuMask {
	return lib.policy.allowed
}

func (lib *LibCpu) ReservedCpus() *libcpu.CpuMask {
	return lib.policy.reserved
}

func (lib *LibCpu) IsolatedCpus() *libcpu.CpuMask {
	return lib.policy.isolated
}

func (lib *LibCpu) ExclusiveCpus() *libcpu.CpuMask {
	return lib.account.ExclusiveCpus()
}

func (lib *LibCpu) TakeCpu(from *libcpu.CpuMask, cnt int, prio CpuPrio) (*libcpu.CpuMask, error) {
	if lib == nil {
		panic("nil lib")
	}

	if from.Size() < cnt {
		return nil, fmt.Errorf("not enough CPUs available")
	}

	log.Debugf("LibCpu: AllocateCpus: from=%s, cnt=%d, prio=%s", from, cnt, prio)
	mask, err := lib.policy.cpuAllocator.AllocateCpus(from, cnt, prio.Value().Option())
	log.Debugf("    => mask=%s, err=%v", mask, err)
	return mask, err
}

func (lib *LibCpu) TakeCpuByHints(from *libcpu.CpuMask, cnt int, prio CpuPrio, all topology.Hints) ([]*libcpu.CpuMask, error) {
	if lib == nil {
		panic("nil lib")
	}

	var (
		alternatives []*libcpu.CpuMask
		free         = from.Clone()
	)

	hints := []*libcpu.CpuMask{}
	for provider, h := range all {
		if podresapi.IsPodResourceHint(provider) {
			hints = append(hints, libcpu.MustParseCpuMask(h.CPUs))
		}
	}

	if len(hints) > cnt {
		total := cnt
		perHint := 1
		if len(hints) < total && total%len(hints) == 0 {
			perHint = total / len(hints)
		}

		cpus := libcpu.NewCpuMask()
		for _, hcpu := range hints {
			pick, err := lib.TakeCpu(free.Intersection(hcpu), perHint, prio)
			if err != nil {
				log.Errorf("failed to take CPUs by topology hints: %v", err)
				cpus = nil
				break
			}
			cpus = cpus.Union(pick)
			free = free.Difference(pick)
			total -= perHint
		}

		if cpus != nil {
			if total > 0 {
				pick, err := lib.TakeCpu(free, total, prio)
				if err != nil {
					log.Errorf("failed to remaining take CPUs by topology hints: %v", err)
					cpus = nil
				} else {
					cpus = cpus.Union(pick)
				}
			}
		}

		if cpus != nil {
			alternatives = []*libcpu.CpuMask{cpus}
		}
	}

	cpus, err := lib.TakeCpu(from, cnt, prio)
	if err != nil {
		log.Errorf("failed to take %d CPUs from %s without topology hints: %v",
			cnt, from, err)
	}

	return append(alternatives, cpus), nil
}
