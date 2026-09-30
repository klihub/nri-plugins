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
	query    *CpuMask   // the CPUs Available is queried for
}

// reservedCpus returns the first quarter, at least one, of the CPUs of
// every leaf pool. Mixed usage takes its exclusive CPUs from these and its
// shared CPUs from the rest. The accounting does not require the two to be
// disjoint; this is just the shape measured. Reserving only a quarter keeps
// adjacent overlapping pools from collapsing into identical sets.
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

// maxUsers returns how many users of the given kind the topology can take
// without overcommitting any of its pools. Shared users take one milli-CPU
// each, exclusive ones a full CPU. Mixed users take both, with their
// exclusive CPUs from the reserved quarter of the leaf pools.
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
// usage is spread over all the pools; exclusive CPUs come from the
// disjoint leaf pools, so every user gets CPUs of its own.
func (t *benchTopology) usages(kind usageKind, count int) []*CpuUsage {
	var (
		usages = make([]*CpuUsage, 0, count)
		cpus   = make([][]int, len(t.leaves))
		next   = make([]int, len(t.leaves))
	)

	for i, leaf := range t.leaves {
		cpus[i] = leaf.List()
	}

	// nextCpu hands out the next unused CPU of leaf i % len(t.leaves).
	nextCpu := func(i int) int {
		l := i % len(t.leaves)
		cpu := cpus[l][next[l]]
		next[l]++
		return cpu
	}

	for i := 0; i < count; i++ {
		id := fmt.Sprintf("user #%d", i)
		var u *CpuUsage

		switch kind {
		case sharedUsage:
			u = &CpuUsage{
				ID:     id,
				Name:   id,
				Shared: t.shared[i%len(t.shared)],
				Charge: 1,
			}
		case exclusiveUsage:
			u = &CpuUsage{
				ID:        id,
				Name:      id,
				Exclusive: NewCpuMask(nextCpu(i)),
			}
		case mixedUsage:
			// Cycle pools by the round and the leaf, not by i: i is in
			// sync with the leaf the CPU comes from.
			cpu := nextCpu(i)
			pool := t.poolFor(i/len(t.leaves)+i%len(t.leaves), cpu)
			u = &CpuUsage{
				ID:        id,
				Name:      id,
				Exclusive: NewCpuMask(cpu),
				Shared:    pool.Difference(t.reserved),
				Charge:    1,
			}
		}

		usages = append(usages, u)
	}

	return usages
}

// poolFor returns a shared pool containing cpu, so a mixed user's CPUs
// stay in one pool. Cycling by i spreads users over every level and
// every overlapping pool.
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
						a := NewAccounting(topology.all.Clone())
						for _, u := range topology.usages(kind, users) {
							if err := a.insert(u); err != nil {
								b.Fatalf("failed to add usage: %v", err)
							}
						}

						// Available assumes no pool is
						// overcommitted; check the setup.
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

						// The number of constraint
						// sets drives Available's cost;
						// report it beside the timing.
						// It must stay flat or fall
						// when the constraints change.
						// Overlapping at 2048 CPUs
						// saturates maxImplicitPools.
						//
						// b.Loop's ResetTimer deletes
						// reported metrics, so report
						// after looping.
						b.ReportMetric(float64(len(a.constraints())), "sets")
					})
				}
			}
		}
	}
}

// benchSink keeps benchmarked results live, so that the compiler cannot
// drop anything being measured as dead code.
var benchSink struct {
	sum   int
	exact bool
}

// BenchmarkAvailableEach compares one AvailableEach traversal over every
// shared pool against one Available call per pool. Batching wins only if
// the probes' growth closures overlap: likely for hierarchical pools, not
// necessarily for overlapping ones. Separate calls take the single-probe
// fast path and skip the containment test. A batched call pays that test
// per probe per set, but snapshots the charged pools once. A win does not
// show which of these caused it.
func BenchmarkAvailableEach(b *testing.B) {
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	for _, cpus := range []int{256, 2048} {
		for _, newTopology := range []func(int) *benchTopology{
			hierarchicalTopology,
			overlappingTopology,
		} {
			topology := newTopology(cpus)

			for _, batched := range []bool{true, false} {
				how := "separate"
				if batched {
					how = "batched"
				}
				name := fmt.Sprintf("cpus=%d/%s/%s", cpus, topology.name, how)

				b.Run(name, func(b *testing.B) {
					// Allocate refuses to overcommit, so a
					// bad setup fails here.
					a := NewAccounting(topology.all.Clone())
					for _, u := range topology.usages(sharedUsage, 64) {
						if _, _, err := a.Allocate(u); err != nil {
							b.Fatalf("failed to allocate usage: %v", err)
						}
					}

					b.ResetTimer()

					for b.Loop() {
						// Both arms consume every result
						// the same way, so neither can be
						// optimised away.
						sum, exact := 0, true

						if batched {
							each, ok := a.AvailableEach(topology.shared...)
							for _, n := range each {
								sum += n
							}
							exact = ok
						} else {
							for _, p := range topology.shared {
								n, ok := a.Available(p)
								sum += n
								exact = exact && ok
							}
						}

						benchSink.sum, benchSink.exact = sum, exact
					}

					b.ReportMetric(float64(len(topology.shared)), "probes")
					b.ReportMetric(float64(len(a.constraints())), "sets")
				})
			}
		}
	}
}

// maskRange returns a mask of count consecutive CPUs starting at start.
func maskRange(start, count int) *CpuMask {
	return NewCpuMask(cpuRange(start, start+count-1)...)
}

// BenchmarkAdmits compares screening k alternative placements of one
// container with Admits against taking an offer for each. One traversal
// answers all shared-only candidates, so expect a large gap there. Each
// exclusive-only candidate needs its own traversal, so expect a small gap.
// If that one reverses, the bookkeeping costs more than it saves.
func BenchmarkAdmits(b *testing.B) {
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	for _, cpus := range []int{256, 2048} {
		for _, newTopology := range []func(int) *benchTopology{
			hierarchicalTopology,
			overlappingTopology,
		} {
			topology := newTopology(cpus)

			for _, kind := range []usageKind{sharedUsage, exclusiveUsage} {
				// One candidate per pool shared usage is
				// assigned to: which may I use?
				candidates := topology.usages(kind, len(topology.shared))
				if len(candidates) == 0 {
					continue
				}
				for i, u := range candidates {
					u.ID = fmt.Sprintf("candidate-%d", i)
					u.Name = u.ID
				}

				for _, batched := range []bool{true, false} {
					how := "offer-each"
					if batched {
						how = "admits"
					}
					name := fmt.Sprintf("cpus=%d/%s/%s/%s",
						cpus, topology.name, kindName(kind), how)

					b.Run(name, func(b *testing.B) {
						a := NewAccounting(topology.all.Clone())
						for _, u := range topology.usages(sharedUsage, 64) {
							if _, _, err := a.Allocate(u); err != nil {
								b.Fatalf("failed to allocate usage: %v", err)
							}
						}

						var admitted int

						b.ResetTimer()

						for b.Loop() {
							if batched {
								for _, v := range a.Admits(candidates...) {
									if v.Admits {
										admitted++
									}
								}
								continue
							}
							for _, u := range candidates {
								if _, err := a.GetOffer(u); err == nil {
									admitted++
								}
							}
						}

						b.StopTimer()

						// admitted keeps both arms live and
						// exposes runs which admit nothing.
						b.ReportMetric(float64(len(candidates)), "candidates")
						b.ReportMetric(float64(admitted)/float64(max(b.N, 1)), "admitted")
					})
				}
			}
		}
	}
}

// kindName names a usage shape for a benchmark case.
func kindName(kind usageKind) string {
	switch kind {
	case sharedUsage:
		return "shared"
	case exclusiveUsage:
		return "exclusive"
	case mixedUsage:
		return "mixed"
	}
	return "unknown"
}
