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

// Package hardware discovers the CPU and memory topology of a machine.
//
// [Discover] reads the topology once and returns an immutable [Machine],
// which can be shared by everything that needs it.
//
// # Zones
//
// A [Zone] is a set of CPUs which share a piece of hardware: a package, a die,
// a cluster, a NUMA node, a cache, a core. Each is tagged with its [Level].
// [Machine.Zones] returns all zones at one level, and [Machine.Levels] says
// which levels a machine has.
//
// Zones are flat, not a tree. Which level contains which depends on the
// hardware, not on the [Level] constants, so containment is a query: [ZoneOf]
// finds the zone at a level holding a set of CPUs, [ZonesWithin] the zones
// fully inside a set, and [ZonesOverlapping] the zones touching it. "The
// caches I can allocate" is Within; "the dies I have to reprogram" is
// Overlapping. A caller wanting a hierarchy builds one over the levels it
// cares about.
//
// The kernel numbers dies, clusters and cores within their package, so an id
// alone does not name one. [TopologyIndex] addresses them by coordinates.
//
// # Handles
//
// [CPU], [MemoryNode], [Cache] and [Zone] are handles into the Machine which
// returned them. They are never nil. A handle for something which does not
// exist reports Valid() == false, and its other methods return zero values.
//
// There is one handle per piece of hardware, so handles are comparable and
// usable as map keys.
//
// # Sets
//
// CPU sets are [libcpu.CpuMask]. Every set a Machine hands out is sealed: it
// is safe for concurrent reads and panics if modified, so Clone it first.
// Sets of other ids -- packages, NUMA nodes, caches -- are sorted []ID.
// Functions take sets as the [libcpu.CPUSet] interface and return
// [libcpu.CpuMask].
//
// # Discovery
//
// Discovery is lenient where the kernel is silent:
//   - Offline CPUs have unknown topology ids.
//   - An unreported die or cluster id is -1. Each package still gets one die
//     zone, and each die one cluster zone, so [Machine.SameZones] reports
//     those levels as the same. Any error but a missing file fails discovery.
//   - Without NUMA, node 0 holds every online CPU and all of the memory.
//   - Without core kind lists, every core is a [PerformanceCore]. If only one
//     kind is listed, the other online CPUs are the other kind. Kinds must
//     be whole cores, and must not overlap.
//   - An asymmetric NUMA distance matrix is averaged into a symmetric one.
//
// # Memory kinds
//
// The kernel does not report a node's [MemoryKind], so it is inferred. A node
// with CPUs holds DRAM. A node with memory but no CPUs is HBM if smaller than
// the average DRAM node, and PMEM otherwise. Without a DRAM node to compare
// against, it stays [MemoryKindUnknown].
//
// # Cache groups
//
// [CacheGroups] lists the caches which group CPUs in a way no other level
// does. A CPU allocator should prefer to keep them intact. On a machine with
// two core kinds each kind may group at a different cache level, so
// [GroupingCacheLevels] returns every such level, coarsest first. Take the
// first for one grouping for the whole machine, or walk them all.
//
// # Filesystem
//
// Discovery reads through an [fs.FS] rooted at the host root, so
// "sys/devices/system/cpu/online" and "proc/meminfo" are both reachable. The
// default is the real filesystem at "/". [WithRoot] points it elsewhere, such
// as a host filesystem mounted in a container. [WithFS] substitutes any fs.FS,
// such as a recorded or synthetic topology in tests.
//
// # Scope
//
// This package only reads topology. It does not cover Intel Speed Select,
// cpufreq control or uncore frequency. A caller which wants to write to the
// tree it read from can supply a [WriterFS].
package hardware
