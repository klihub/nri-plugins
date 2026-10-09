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
	"github.com/containers/nri-plugins/pkg/lib/hardware"
	"github.com/containers/nri-plugins/pkg/resmgr/cpuclass"
	libcpu "github.com/containers/nri-plugins/pkg/resmgr/lib/cpu"
	corev1 "k8s.io/api/core/v1"
)

type LibCpuScore struct {
	*LibCpuOffer
	reserved  func() int
	isolated  func() int
	headroom  func() int
	affinity  func() float64
	hints     func() [2]float64
	ccHints   func() *cpuclass.AllocationHints
	prioCapa  func() int
	colocated func() int
}

func (o *LibCpuOffer) Score(affinities map[int]int32) *LibCpuScore {
	if o == nil {
		log.Warnf("trying to get score for nil offer")
		return nil
	}

	return &LibCpuScore{
		LibCpuOffer: o,

		reserved: DelayedEval(func() int { return o.ReservedCpus().Size() }),
		isolated: DelayedEval(func() int { return o.IsolatedCpus().Size() }),
		headroom: DelayedEval(func() int {
			if o.usage == nil {
				panic("nil o.usage")
			}

			qry := &libcpu.CpuUsage{
				ID:        "headroom-query",
				Name:      "headroom-query",
				Exclusive: o.usage.Exclusive,
				Shared:    o.supply.SharedCpus(),
			}
			hs, _, err := o.supply.lib.account.Headroom(qry)
			if err != nil {
				log.Warnf("failed to get headroom via pool: %v", err)
				return -1
			}
			return hs[0]
		}),
		affinity: DelayedEval(func() float64 {
			return affinityScore(affinities, o.supply.node)
		}),
		hints: DelayedEval(func() [2]float64 {
			topo := o.req.ctr.GetTopologyHints()
			if len(topo) == 0 {
				return [2]float64{0, 0}
			}
			topo.ResolvePartialHints(nodeHintToCPUs(o.supply.node.Machine()))
			hints := make(map[string]float64, len(topo))
			for p, h := range topo {
				log.Debugf(" - LibCpu topology hint %s", h.String())
				hints[p] = o.supply.node.HintScore(h)
			}
			all, nonzero := combineHintScores(hints)
			return [2]float64{all, nonzero}
		}),
		ccHints: DelayedEval(func() *cpuclass.AllocationHints {
			var (
				cpuClasses *cpuclass.Handler
				class      string
				count      int
			)

			if cpuClasses = o.Lib().policy.cpuClasses; cpuClasses == nil {
				return nil
			}
			if count = o.req.opt.ExclusiveCpu; count == 0 {
				return nil
			}
			if class = o.req.opt.CpuClass.StringValue(); class == "" {
				return nil
			}

			hints := o.Lib().policy.cpuClasses.Hints(cpuclass.AllocationIntent{
				ClassName:      class,
				CurrentCpus:    libcpu.NewCpuMask(),
				FreeCpus:       o.cpus,
				RequestedCount: count,
			})

			return &hints
		}),
		prioCapa: DelayedEval(func() int {
			prio := o.req.opt.CpuPriority.CpuPrioValue()

			if prio == CpuPrioNone {
				return 0
			}

			cpus := libcpu.NewCpuMask()

			switch prio {
			case CpuPrioLow:
				cpus = o.supply.node.Machine().CoreKindCPUs(hardware.EfficientCore)
				if cpus.Size() == 0 {
					cpus = o.Lib().policy.cpuAllocator.GetCPUPriorities()[prio.Value()]
				}
			case CpuPrioHigh:
				cpus = o.supply.node.Machine().CoreKindCPUs(hardware.PerformanceCore)
				if cpus.Size() == 0 {
					cpus = o.Lib().policy.cpuAllocator.GetCPUPriorities()[prio.Value()]
				}
			case CpuPrioNormal:
				cpus = o.Lib().policy.cpuAllocator.GetCPUPriorities()[prio.Value()]
			}

			return 1000 * o.cpus.Intersection(cpus).Size()
		}),
		colocated: DelayedEval(func() int {
			colocated := 0
			for _, g := range o.Lib().policy.allocations.grants {
				if g.GetCPUNode().NodeID() == o.supply.node.NodeID() {
					colocated++
				}
			}
			return colocated
		}),
	}
}

func (s *LibCpuScore) Lib() *LibCpu {
	return s.supply.lib
}

type LibCpuScoreSortFunc func(a, b *LibCpuScore) int

func LibCpuPoolSorter(fn []LibCpuScoreSortFunc) LibCpuScoreSortFunc {
	return func(a, b *LibCpuScore) int {
		log.Debugf("comparing nodes %s and %s",
			a.supply.node.Name(), b.supply.node.Name())
		switch {
		case a == nil && b == nil:
			return 0
		case a == nil:
			return 1
		case b == nil:
			return -1
		case a.req != b.req:
			log.Warnf("trying to compare LibCpuScores for different requests")
			return 0
		}
		for _, f := range fn {
			if r := f(a, b); r != 0 {
				return r
			}
		}
		return 0
	}
}

func ScoreByCapacity(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by capacity", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by capacity", b.supply.node.Name())
		default:
			log.Debugf("- capacity is a TIE")
		}
	}()

	switch {
	case a.req.opt.ReservedCpus.BoolValue():
		if a.reserved() > b.reserved() {
			return -1
		}
		if b.reserved() > a.reserved() {
			return 1
		}
	case a.req.opt.IsolatedCpus.BoolValue():
		if a.isolated() > b.isolated() {
			return -1
		}
		if b.isolated() > a.isolated() {
			return 1
		}
	}

	return 0
}

func ScoreByAffinity(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by affinity", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by affinity", b.supply.node.Name())
		default:
			log.Debugf("- affinity is a TIE")
		}
	}()

	switch {
	case a.affinity() > b.affinity():
		return -1
	case b.affinity() > a.affinity():
		return 1
	}
	return 0
}

func ScoreByHints(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by hints", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by hints", b.supply.node.Name())
		default:
			log.Debugf("- hints is a TIE")
		}
	}()

	var (
		ah, bh             = a.hints(), b.hints()
		aFull, bFull       = ah[0], bh[0]
		aNonZero, bNonZero = ah[1], bh[1]
	)
	switch {
	case aFull > bFull:
		return -1
	case aFull < bFull:
		return 1
	case aFull == 0:
		if aNonZero > bNonZero {
			return -1
		}
		if aNonZero < bNonZero {
			return 1
		}
	case aFull == bFull && aNonZero == bNonZero && (aFull != 0 || aNonZero != 0):
		return ScoreByNodeId(a, b)

	}
	return 0
}

func ScoreByMemOfferMatch(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by MemOfferMatch", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by MemOfferMatch", b.supply.node.Name())
		default:
			log.Debugf("- MemOfferMatch is a TIE")
		}
	}()

	switch {
	case a.mem != nil && b.mem == nil:
		return -1
	case a.mem == nil && b.mem != nil:
		return 1
	case a.mem == nil:
		return 0
	}

	var (
		aMask  = a.mem.NodeMask()
		aType  = a.Lib().policy.memZoneType(aMask)
		bMask  = b.mem.NodeMask()
		bType  = b.Lib().policy.memZoneType(bMask)
		target = a.req.opt.MemoryType.MemTypeValue()
	)

	switch {
	case aType == target && bType != target:
		return -1
	case aType != target && bType == target:
		return 1
	}

	req, lim := a.req.opt.MemoryRequest, a.req.opt.MemoryLimit
	if req != lim {
		aCapa := a.Lib().policy.poolZoneCapacity(a.supply.node, memoryType(target))
		bCapa := b.Lib().policy.poolZoneCapacity(b.supply.node, memoryType(target))
		switch {
		case lim != 0 && aCapa >= lim && bCapa < lim:
			fallthrough
		case lim == 0 && aCapa > bCapa:
			return -1
		case lim != 0 && aCapa < lim && bCapa >= lim:
			fallthrough
		case lim == 0 && aCapa < bCapa:
			return 1
		}
	}

	return 0

}

func ScoreByMemOffer(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by MemOffe", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by MemOffer", b.supply.node.Name())
		default:
			log.Debugf("- MemOffer is a TIE")
		}
	}()

	switch {
	case a.mem != nil && b.mem == nil:
		return -1
	case a.mem == nil && b.mem != nil:
		return 1
	case a.mem == nil:
		return 0
	}

	var (
		aMask  = a.mem.NodeMask()
		bMask  = b.mem.NodeMask()
		target = memoryType(a.req.opt.MemoryType.MemTypeValue())
	)

	switch {
	case aMask.Size() < bMask.Size():
		return -1
	case aMask.Size() > bMask.Size():
		return 1
	}

	if target == 0 {
		return 0
	}

	switch {
	case a.supply.node.HasMemoryType(target) && !b.supply.node.HasMemoryType(target):
		return -1
	case !a.supply.node.HasMemoryType(target) && b.supply.node.HasMemoryType(target):
		return 1
	}

	return 0
}

func ScoreByCpuBurstability(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by CPU burstability", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by CPU burstability", b.supply.node.Name())
		default:
			log.Debugf("- CPU burstability is a TIE")
		}
	}()

	if a.req.ctr.GetQOSClass() != corev1.PodQOSBurstable {
		return 0
	}

	limit := a.req.opt.CpuLimit
	aRoom := a.headroom()
	bRoom := b.headroom()

	if aRoom < 0 || bRoom < 0 {
		return 0
	}

	if limit != 0 {
		switch {
		case aRoom >= limit && bRoom < limit:
			return -1
		case aRoom < limit && bRoom >= limit:
			return 1
		}
	} else {
		aLevel := a.supply.node.Kind().TopologyLevel()
		bLevel := b.supply.node.Kind().TopologyLevel()
		target := a.req.opt.BurstableLimit.CpuLevelValue()

		switch {
		case aLevel == target && bLevel != target:
			return -1
		case aLevel != target && bLevel == target:
			return 1
		case aLevel == target && bLevel == target:
			if aRoom > bRoom {
				return -1
			}
			if aRoom < bRoom {
				return 1
			}
		case aLevel.Value() > target.Value() && bLevel.Value() < target.Value():
			return -1
		case aLevel.Value() < target.Value() && bLevel.Value() > target.Value():
			return 1
		}
	}

	return 0
}

func ScoreByCpuClassHints(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by CPU class hints", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by CPU class hints", b.supply.node.Name())
		default:
			log.Debugf("- CPU class hints is a TIE")
		}
	}()

	aHints, bHints := a.ccHints(), b.ccHints()

	if aHints == nil || bHints == nil {
		return 0
	}

	aCpus, bCpus := libcpu.NewCpuMask(), libcpu.NewCpuMask()

	for _, h := range aHints.Prefer {
		for _, hinted := range h.Cpus {
			if a.cpus.Intersection(hinted).Equals(a.cpus) {
				aCpus = hinted
				break
			}
		}
		if aCpus.Size() > 0 {
			break
		}
	}
	for _, h := range bHints.Prefer {
		for _, hinted := range h.Cpus {
			if b.cpus.Intersection(hinted).Equals(b.cpus) {
				bCpus = hinted
				break
			}
		}
		if bCpus.Size() > 0 {
			break
		}
	}

	if a.cpus.Size() > 0 && b.cpus.Size() > 0 {
		switch {
		case aCpus.Size() > 0 && bCpus.Size() == 0:
			return -1
		case aCpus.Size() == 0 && bCpus.Size() > 0:
			return 1
		}
	}

	return 0
}

func ScoreByCpuPrio(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by CPU priority", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by CPU priority", b.supply.node.Name())
		default:
			log.Debugf("- CPU priority is a TIE")
		}
	}()

	var (
		aCapa = a.prioCapa()
		bCapa = b.prioCapa()
		needs = 1000*a.req.opt.ExclusiveCpu + a.req.opt.SharedCpu
	)

	switch {
	case aCapa >= needs && bCapa < needs:
		return -1
	case aCapa < needs && bCapa >= needs:
		return 1
	}

	return 0
}

func ScoreByNodeDepth(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by node depth", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by node depth", b.supply.node.Name())
		default:
			log.Debugf("- node depth is a TIE")
		}
	}()

	switch {
	case a.supply.node.RootDistance() > b.supply.node.RootDistance():
		return -1
	case a.supply.node.RootDistance() < b.supply.node.RootDistance():
		return 1
	}
	return 0
}

func ScoreForceReservedToRoot(a, b *LibCpuScore) (result int) {
	switch {
	case !a.req.opt.ReservedCpus.BoolValue():
		return 0
	case a.supply.node.IsRootNode() && !b.supply.node.IsRootNode():
		return -1
	case !a.supply.node.IsRootNode() && b.supply.node.IsRootNode():
		return 1
	}
	return 0
}

func ScoreByReservedCapacity(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by reserved capacity", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by reserved capacity", b.supply.node.Name())
		default:
			log.Debugf("- reserved capacityis a TIE")
		}
	}()

	if !a.req.opt.ReservedCpus.BoolValue() {
		return 0
	}

	aCapa := a.cpus.Intersection(a.supply.node.ReservedCpus()).Size()
	bCapa := b.cpus.Intersection(b.supply.node.ReservedCpus()).Size()

	return bCapa - aCapa
}

func ScoreByIsolatedCapacity(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by isolated capacity", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by isolated capacity", b.supply.node.Name())
		default:
			log.Debugf("- isolated capacity is a TIE")
		}
	}()

	if !a.req.opt.IsolatedCpus.BoolValue() {
		return 0
	}

	aCapa := a.cpus.Intersection(a.supply.node.IsolatedCpus()).Size()
	bCapa := b.cpus.Intersection(b.supply.node.IsolatedCpus()).Size()
	needs := a.req.opt.ExclusiveCpu

	switch {
	case aCapa >= needs && bCapa < needs:
		return -1
	case aCapa < needs && bCapa >= needs:
		return 1
	}

	return 0
}

func ScoreByNormalCpuPrio(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by normal CPU prio", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by normal CPU prio", b.supply.node.Name())
		default:
			log.Debugf("- normal CPU prio is a TIE")
		}
	}()

	if a.req.opt.CpuPriority.CpuPrioValue() != CpuPrioNormal {
		return 0
	}

	aCapa := a.prioCapa()
	bCapa := b.prioCapa()
	needs := 1000*a.req.opt.ExclusiveCpu + a.req.opt.SharedCpu

	switch {
	case aCapa >= needs && bCapa < needs:
		return -1
	case aCapa < needs && bCapa >= needs:
		return 1
	}

	return 0
}

func ScoreByHeadroom(a, b *LibCpuScore) (result int) {
	aRoom := a.headroom()
	bRoom := b.headroom()

	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by headroom (%d vs %d)",
				a.supply.node.Name(), aRoom, bRoom)
		case result > 0:
			log.Debugf("- node %s WINS by headroom (%d vs %d)",
				b.supply.node.Name(), aRoom, bRoom)
		default:
			log.Debugf("- headroom is a TIE (%d vs %d)", aRoom, bRoom)
		}
	}()

	if aRoom < 0 || bRoom < 0 {
		return 0
	}

	return bRoom - aRoom
}

func ScoreByColocation(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by colocation", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by colocation", b.supply.node.Name())
		default:
			log.Debugf("- colocation is a TIE")
		}
	}()

	return a.colocated() - b.colocated()
}

func ScoreReservedContainer(a, b *LibCpuScore) (result int) {
	return ScoreByReservedCapacity(a, b)
}

func ScoreNormalContainer(a, b *LibCpuScore) (result int) {
	if v := ScoreByIsolatedCapacity(a, b); v != 0 {
		return v
	}

	if v := ScoreByNormalCpuPrio(a, b); v != 0 {
		return v
	}

	if a.req.opt.ExclusiveCpu != 0 {
		if v := ScoreByHeadroom(a, b); v != 0 {
			return v
		}
	}

	if v := ScoreByColocation(a, b); v != 0 {
		return v
	}

	if v := ScoreByHeadroom(a, b); v != 0 {
		return v
	}

	return 0
}

func ScoreByNodeId(a, b *LibCpuScore) (result int) {
	defer func() {
		switch {
		case result < 0:
			log.Debugf("- node %s WINS by node ID", a.supply.node.Name())
		case result > 0:
			log.Debugf("- node %s WINS by node ID", b.supply.node.Name())
		default:
			log.Debugf("- node ID (what !?!)")
		}
	}()

	return a.supply.node.NodeID() - b.supply.node.NodeID()
}

func DelayedEval[T any](fn func() T) func() T {
	var (
		v   T
		set bool
	)
	return func() T {
		if set {
			return v
		}
		v = fn()
		set = true
		return v
	}
}
