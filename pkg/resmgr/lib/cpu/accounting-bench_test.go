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

package libcpu

import (
	"fmt"
	"io"
	"testing"

	logger "github.com/containers/nri-plugins/pkg/log"
)

// benchTopology is a generated set of pools to benchmark Available with.
type benchTopology struct {
	name     string
	cpus     int
	all      *CpuMask   // all CPUs, the root pool of the accounting
	shared   []*CpuMask // pools shared usage is assigned to
	leaves   []*CpuMask // disjoint pools exclusive CPUs are taken from
	reserved *CpuMask   // CPUs set aside for exclusive allocation
	query    *CpuMask   // the set of CPUs we query available capacity for
}

// reservedCpus returns the first quarter, but at least one, of the CPUs of
// every leaf pool. Mixed usage takes its exclusive CPUs from these and its
// shared allocation from the rest, which keeps the two disjoint as Add
// requires, while all users of a pool still get the very same set of shared
// CPUs. Reserving only a quarter leaves the pools of the overlapping setup
// overlapping: reserving more of every leaf would collapse the remains of
// adjacent, overlapping pools into a single identical set of CPUs.
func reservedCpus(leaves []*CpuMask) *CpuMask {
	reserved := NewCpuMask()

	for _, leaf := range leaves {
		cpus := leaf.List()
		reserved.Set(cpus[:max(1, len(cpus)/4)]...)
	}

	return reserved
}

// hierarchicalTopology generates a sensible pool setup: a system, socket,
// die, NUMA node hierarchy where sibling pools are always disjoint and a
// child pool is always a subset of its parent. The number of pools is
// small and independent of the number of CPUs.
func hierarchicalTopology(cpus int) *benchTopology {
	var (
		all    = maskRange(0, cpus)
		shared = []*CpuMask{}
		leaves []*CpuMask
	)

	for _, count := range []int{1, 2, 4, 8} {
		if count > cpus {
			break
		}

		size := cpus / count
		level := make([]*CpuMask, 0, count)
		for i := 0; i < count; i++ {
			level = append(level, maskRange(i*size, size))
		}

		shared = append(shared, level...)
		leaves = level
	}

	return &benchTopology{
		name:     "hierarchical",
		cpus:     cpus,
		all:      all,
		shared:   shared,
		leaves:   leaves,
		reserved: reservedCpus(leaves),
		query:    leaves[len(leaves)/2],
	}
}

// overlappingTopology generates a less sensible pool setup: sliding windows
// of CPUs where adjacent pools partially overlap and distant ones are
// disjoint. The number of pools grows linearly with the number of CPUs.
func overlappingTopology(cpus int) *benchTopology {
	const (
		window = 8
		stride = window / 2
	)

	var (
		all    = maskRange(0, cpus)
		shared = []*CpuMask{}
		leaves = []*CpuMask{}
	)

	for start := 0; start+window <= cpus; start += stride {
		shared = append(shared, maskRange(start, window))
	}
	for start := 0; start+window <= cpus; start += window {
		leaves = append(leaves, maskRange(start, window))
	}

	return &benchTopology{
		name:     "overlapping",
		cpus:     cpus,
		all:      all,
		shared:   shared,
		leaves:   leaves,
		reserved: reservedCpus(leaves),
		query:    shared[len(shared)/2],
	}
}

type usageKind int

const (
	sharedUsage usageKind = iota
	exclusiveUsage
	mixedUsage
)

func (k usageKind) String() string {
	switch k {
	case sharedUsage:
		return "shared"
	case exclusiveUsage:
		return "exclusive"
	case mixedUsage:
		return "mixed"
	}
	return "unknown"
}

// maxUsers returns the number of users of the given kind the topology can
// take without overcommitting any of its pools. Shared users take a single
// milli-CPU each, exclusive ones a full CPU. Mixed users take both, and get
// their exclusive CPUs from the reserved quarter of the leaf pools.
func (t *benchTopology) maxUsers(kind usageKind) int {
	switch kind {
	case sharedUsage:
		return 1000 * t.cpus
	case exclusiveUsage:
		return t.cpus
	case mixedUsage:
		return t.cpus / 4
	}
	return 0
}

// usages generates the given number of usages of the given kind. Shared
// usage is spread over all the pools, exclusive CPUs are taken from the
// disjoint leaf pools so every user gets CPUs of its own.
func (t *benchTopology) usages(kind usageKind, count int) []Usage {
	var (
		usages = make([]Usage, 0, count)
		cpus   = make([][]int, len(t.leaves))
		next   = make([]int, len(t.leaves))
	)

	for i, leaf := range t.leaves {
		cpus[i] = leaf.List()
	}

	// nextCpu hands out a CPU of the i-th leaf pool. Leaves are disjoint
	// and we never hand out more CPUs than a leaf has, so every user gets
	// CPUs of its own.
	nextCpu := func(i int) int {
		l := i % len(t.leaves)
		cpu := cpus[l][next[l]]
		next[l]++
		return cpu
	}

	for i := 0; i < count; i++ {
		u := &testUsage{id: fmt.Sprintf("user #%d", i)}

		switch kind {
		case sharedUsage:
			u.shared = t.shared[i%len(t.shared)]
			u.charge = 1
		case exclusiveUsage:
			u.exclusive = NewCpuMask(nextCpu(i))
		case mixedUsage:
			// Cycle pools by the round and the leaf, not by i, which is
			// in sync with the leaf the CPU comes from.
			cpu := nextCpu(i)
			pool := t.poolFor(i/len(t.leaves)+i%len(t.leaves), cpu)
			u.exclusive = NewCpuMask(cpu)
			u.shared = pool.Difference(t.reserved).(*CpuMask)
			u.charge = 1
		}

		usages = append(usages, u)
	}

	return usages
}

// poolFor returns a pool containing the given CPU, the pool a mixed user
// takes its shared capacity from. Keeping the exclusive CPU within the
// shared pool keeps the CPUs of the user in a single pool. We cycle
// through all the alternatives so that pools of every level of the
// hierarchy, and all the overlapping pools, get their share of users.
func (t *benchTopology) poolFor(i, cpu int) *CpuMask {
	pools := []*CpuMask{}

	for _, p := range t.shared {
		if p.Contains(cpu) {
			pools = append(pools, p)
		}
	}

	return pools[i%len(pools)]
}

func BenchmarkAvailable(b *testing.B) {
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	for _, cpus := range []int{16, 64, 256, 1024, 2048} {
		for _, newTopology := range []func(int) *benchTopology{
			hierarchicalTopology,
			overlappingTopology,
		} {
			topology := newTopology(cpus)

			for _, kind := range []usageKind{sharedUsage, exclusiveUsage, mixedUsage} {
				for _, users := range []int{8, 64, 512, 2048} {
					if users > topology.maxUsers(kind) {
						continue
					}

					name := fmt.Sprintf("cpus=%d/%s/%s/users=%d",
						cpus, topology.name, kind, users)

					b.Run(name, func(b *testing.B) {
						a := NewAccounting(topology.all.Clone().(*CpuMask))
						for _, u := range topology.usages(kind, users) {
							if err := a.Add(u); err != nil {
								b.Fatalf("failed to add usage: %v", err)
							}
						}

						// Fail if the generated setup overcommits any of
						// its pools. Available assumes it never happens,
						// and such a setup could not exist for real.
						for _, p := range append([]*CpuMask{topology.all}, topology.shared...) {
							if charge, capacity := a.charge(p), 1000*p.Size(); charge > capacity {
								b.Fatalf("overcommitted pool %s: %d > %d",
									p, charge, capacity)
							}
						}

						b.ResetTimer()

						for b.Loop() {
							a.Available(topology.query)
						}

						// Note that ResetTimer, which b.Loop does on its
						// first call, deletes reported metrics, so we can
						// only report ours once we're done looping.
						b.ReportMetric(float64(len(a.constraints())), "sets")
					})
				}
			}
		}
	}
}

// maskRange returns a mask of count consecutive CPUs starting at start.
func maskRange(start, count int) *CpuMask {
	return NewCpuMask(cpuRange(start, start+count-1)...)
}
