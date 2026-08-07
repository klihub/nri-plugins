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
	"github.com/containers/nri-plugins/pkg/irq"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	corev1 "k8s.io/api/core/v1"
)

type IrqAffinity struct {
	Claim []string `json:"claim,omitempty"`
	Mask  []string `json:"mask,omitempty"`
}

func (p *policy) irqCpus(hwIrq *irq.Irq) (claim, mask cpuset.CPUSet) {
	claim, mask = cpuset.New(), cpuset.New()
	for _, g := range p.allocations.grants {
		if g.GetContainer().GetQOSClass() != corev1.PodQOSGuaranteed {
			continue
		}
		if irqs := g.IrqAffinity(); irqs != nil {
			for _, c := range irqs.Claim {
				if hwIrq.Match(c) {
					claim = claim.Union(g.IsolatedCPUs()).Union(g.ExclusiveCPUs())
					log.Debugf("irq: %s claims %s", g.GetContainer().PrettyName(), hwIrq.String())
				}
			}
			for _, m := range irqs.Mask {
				if hwIrq.Match(m) {
					mask = mask.Union(g.IsolatedCPUs()).Union(g.ExclusiveCPUs())
					log.Debugf("irq: %s masks %s", g.GetContainer().PrettyName(), hwIrq.String())
				}
			}
		}
	}

	return claim, mask
}

func (p *policy) applyIrqAffinity(user string) {
	hwIrqs, err := irq.Interrupts()
	if err != nil {
		log.Errorf("failed to read HW interrupts: %v", err)
		return
	}

	for _, hwIrq := range hwIrqs {
		current, err := hwIrq.AffinityCpus()
		if err != nil {
			log.Errorf("%s: failed to read affinity: %v", hwIrq.String(), err)
			continue
		}

		claim, mask := p.irqCpus(hwIrq)
		if both := claim.Intersection(mask); !both.IsEmpty() {
			log.Warnf("%s: both claimed and masked for cpus %s, giving claims priority",
				hwIrq.String(), both.String())
		}

		cpus := p.allowed
		switch {
		case !claim.IsEmpty():
			cpus = claim
		case !mask.IsEmpty():
			cpus = cpus.Difference(mask)
		default:
		}

		if cpus.Equals(current) {
			continue
		}

		if err := hwIrq.SetAffinityCpus(cpus); err != nil {
			log.Errorf("%s: failed to set affinity to cpus %s (for %s): %v",
				hwIrq.String(), claim.String(), user, err)
		}
	}
}
