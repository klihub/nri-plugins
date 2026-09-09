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

package hardware

// The helpers in this file are written only against the exported API.
// They add no state and must not reach into unexported fields.

import (
	"slices"

	libcpu "github.com/containers/nri-plugins/pkg/lib/cpu"
)

//
// Containment
//

// ZoneOf returns the smallest zone at the given level which holds all of cpus,
// or an invalid zone if none does.
func ZoneOf(m *Machine, level Level, cpus libcpu.CPUSet) *Zone {
	best := invalidZone
	for _, z := range m.Zones(level) {
		if !cpus.IsSubsetOf(z.cpus) {
			continue
		}
		if !best.valid || z.cpus.Size() < best.cpus.Size() {
			best = z
		}
	}
	return best
}

// ZonesWithin returns the zones at the given level whose CPUs are all in
// cpus. A partly covered zone is left out.
func ZonesWithin(m *Machine, level Level, cpus libcpu.CPUSet) []*Zone {
	var out []*Zone
	for _, z := range m.Zones(level) {
		if z.cpus.IsSubsetOf(cpus) {
			out = append(out, z)
		}
	}
	return out
}

// ZonesOverlapping returns the zones at the given level with any CPU in
// cpus. A partly covered zone is included.
func ZonesOverlapping(m *Machine, level Level, cpus libcpu.CPUSet) []*Zone {
	var out []*Zone
	for _, z := range m.Zones(level) {
		if z.cpus.Intersects(cpus) {
			out = append(out, z)
		}
	}
	return out
}

//
// Threads and cores
//

// AllThreads returns cpus together with every other CPU sharing a core with
// one of them, i.e. cpus rounded up to whole cores.
func AllThreads(m *Machine, cpus libcpu.CPUSet) *libcpu.CpuMask {
	all := libcpu.NewCpuMask()
	cpus.ForEachCpu(func(id int) bool {
		if c := m.CPU(id); c.Valid() && !c.Threads().IsEmpty() {
			all.Set(c.Threads().UnsortedList()...)
		} else {
			all.Set(id)
		}
		return true
	})
	return sealed(all)
}

// SingleThreadPerCore returns the subset of cpus holding only the
// lowest-numbered CPU of each core it covers.
func SingleThreadPerCore(m *Machine, cpus libcpu.CPUSet) *libcpu.CpuMask {
	var (
		out  = libcpu.NewCpuMask()
		done = libcpu.NewCpuMask()
	)

	// List, not UnsortedList: keep the lowest-numbered thread of each core.
	for _, id := range cpus.List() {
		if done.Contains(id) {
			continue
		}
		out.Set(id)
		done.Set(id)
		if c := m.CPU(id); c.Valid() {
			done.Set(c.Threads().UnsortedList()...)
		}
	}

	return sealed(out)
}

//
// Caches
//

// CPUsSharingCache returns cpus together with every other CPU sharing a cache
// at the given level with one of them, i.e. cpus rounded up to whole cache
// groups.
func CPUsSharingCache(m *Machine, level int, cpus libcpu.CPUSet) *libcpu.CpuMask {
	all := libcpu.NewCpuMask()
	cpus.ForEachCpu(func(id int) bool {
		if all.Contains(id) {
			return true
		}
		if cache := m.CPU(id).Cache(level); cache.Valid() {
			all.Set(cache.CPUs().UnsortedList()...)
		} else {
			all.Set(id)
		}
		return true
	})
	return sealed(all)
}

// CacheGrouping is one cache level and its [CacheGroups].
type CacheGrouping struct {
	Level  int
	Groups []*Cache
}

// GroupingCacheLevels returns every cache level for which [CacheGroups] is
// not empty, coarsest first. It is empty if no cache level adds a grouping
// of its own. See "Cache groups" in the package doc.
func GroupingCacheLevels(m *Machine) []CacheGrouping {
	var groupings []CacheGrouping

	// largest caches first, so the coarsest grouping comes first
	levels := m.CacheLevels()
	for i := len(levels) - 1; i >= 0; i-- {
		if groups := CacheGroups(m, levels[i]); len(groups) > 0 {
			groupings = append(groupings, CacheGrouping{
				Level:  levels[i],
				Groups: groups,
			})
		}
	}

	return groupings
}

// CacheGroups returns the caches at the given level that group CPUs
// non-trivially, ordered by package, die, NUMA node and lowest CPU. A cache
// shared by one CPU, by exactly one core, or by a whole die or package is
// left out. A cache matching a cluster or a NUMA node is kept.
func CacheGroups(m *Machine, level int) []*Cache {
	var groups []*Cache

	for _, cache := range m.Caches(level) {
		cpus := cache.CPUs()

		switch {
		case cpus.Size() <= 1:
			continue
		case sameAsSomeZone(m, LevelCore, cpus):
			continue
		case sameAsSomeZone(m, LevelDie, cpus):
			continue
		case sameAsSomeZone(m, LevelPackage, cpus):
			continue
		}

		groups = append(groups, cache)
	}

	slices.SortFunc(groups, func(a, b *Cache) int {
		x, y := groupCoordinates(m, a), groupCoordinates(m, b)
		if x.Package != y.Package {
			return x.Package - y.Package
		}
		if x.Die != y.Die {
			return x.Die - y.Die
		}
		if x.MemoryNode != y.MemoryNode {
			return x.MemoryNode - y.MemoryNode
		}
		return a.CPUs().List()[0] - b.CPUs().List()[0]
	})

	return groups
}

//
// Clusters
//

// SingleCoreClusters says what [LogicalClusters] does with a cluster holding
// only the threads of one core. There is no default.
type SingleCoreClusters bool

const (
	// OmitSingleCoreClusters leaves them out. A die whose every
	// cluster is one core yields nothing.
	OmitSingleCoreClusters SingleCoreClusters = false
	// MergeSingleCoreClusters gathers them into a single cluster, as
	// pkg/sysfs did. A die whose every cluster is one core yields that
	// one merged cluster.
	MergeSingleCoreClusters SingleCoreClusters = true
)

// LogicalClusters returns the CPUs of each cluster of the given die, with
// single-core clusters treated as single says. A merged cluster has no
// [Zone], so these are plain CPU sets. Each is ordered by the cluster id of
// its lowest-numbered CPU:
//
//	id := m.CPU(cpus.List()[0]).ClusterID()
func LogicalClusters(m *Machine, pkg, die ID, single SingleCoreClusters) []*libcpu.CpuMask {
	var (
		want     = DieID{Package: pkg, Die: die}
		clusters []*libcpu.CpuMask
		merged   = libcpu.NewCpuMask()
	)

	for _, z := range m.Zones(LevelCluster) {
		if z.DieID() != want {
			continue
		}
		if sameAsSomeZone(m, LevelCore, z.CPUs()) {
			if single == MergeSingleCoreClusters {
				merged.Set(z.CPUs().UnsortedList()...)
			}
			continue
		}
		clusters = append(clusters, z.CPUs())
	}

	if !merged.IsEmpty() {
		clusters = append(clusters, sealed(merged))
	}

	slices.SortFunc(clusters, func(a, b *libcpu.CpuMask) int {
		return m.CPU(a.List()[0]).ClusterID() - m.CPU(b.List()[0]).ClusterID()
	})

	return clusters
}

//
// NUMA nodes
//

// MemoryNodeDistanceGroup is a set of NUMA nodes all equally far from some
// other node.
type MemoryNodeDistanceGroup struct {
	// Distance is how far the nodes are, as the kernel reports it.
	Distance int
	// Nodes are the nodes at that distance, in increasing order of id.
	Nodes []ID
}

// ClosestMemoryNodes returns the NUMA nodes, other than from, that satisfy
// match, grouped by distance and ordered nearest first. A nil match accepts
// every node. It returns nil if from is not a node.
func ClosestMemoryNodes(m *Machine, from ID, match func(*MemoryNode) bool) []MemoryNodeDistanceGroup {
	origin := m.MemoryNode(from)
	if !origin.Valid() {
		return nil
	}

	byDistance := map[int][]ID{}
	for _, node := range m.MemoryNodes() {
		if node.ID() == from {
			continue
		}
		if match != nil && !match(node) {
			continue
		}
		d := origin.Distance(node.ID())
		if d < 0 {
			continue
		}
		byDistance[d] = append(byDistance[d], node.ID())
	}

	distances := make([]int, 0, len(byDistance))
	for d := range byDistance {
		distances = append(distances, d)
	}
	slices.Sort(distances)

	groups := make([]MemoryNodeDistanceGroup, 0, len(distances))
	for _, d := range distances {
		nodes := byDistance[d]
		slices.Sort(nodes)
		groups = append(groups, MemoryNodeDistanceGroup{Distance: d, Nodes: nodes})
	}

	return groups
}

// MemoryNodesFor returns the ids of the NUMA nodes any of whose CPUs are in
// cpus, i.e. the memory local to those CPUs.
func MemoryNodesFor(m *Machine, cpus libcpu.CPUSet) []ID {
	var out []ID
	for _, node := range m.MemoryNodes() {
		if node.CPUs().Intersects(cpus) {
			out = append(out, node.ID())
		}
	}
	return out
}

// MemoryNodesOfKind returns the ids of the NUMA nodes holding the given kind of
// memory.
func MemoryNodesOfKind(m *Machine, kind MemoryKind) []ID {
	var out []ID
	for _, node := range m.MemoryNodes() {
		if node.Kind() == kind {
			out = append(out, node.ID())
		}
	}
	return out
}

//
// shared helpers
//

// sameAsSomeZone reports whether cpus is exactly the CPUs of one zone at a
// level.
func sameAsSomeZone(m *Machine, level Level, cpus libcpu.CPUSet) bool {
	for _, z := range m.Zones(level) {
		if z.CPUs().Equals(cpus) {
			return true
		}
	}
	return false
}

// groupCoordinates returns where a cache group sits, from its first CPU.
func groupCoordinates(m *Machine, cache *Cache) Coordinates {
	cpus := cache.CPUs().List()
	if len(cpus) == 0 {
		return Coordinates{
			CPU: unknownID, Package: unknownID, Die: unknownID,
			Cluster: unknownID, MemoryNode: unknownID, Core: unknownID,
		}
	}
	return m.TopologyIndex().CoordinatesOf(cpus[0])
}
