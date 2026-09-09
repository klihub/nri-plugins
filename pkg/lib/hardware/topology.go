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

import (
	"fmt"
	"slices"
	"strings"

	libcpu "github.com/containers/nri-plugins/pkg/lib/cpu"
)

// TopologyIndex is a [Machine]'s CPU topology flattened into one lookup
// table by coordinate: the CPUs of each package, die, cluster, NUMA node,
// cache and core. It shares the zones' sets and is cheap to keep. Use it
// for repeated lookups, such as per allocation.
//
// It never changes and is safe for concurrent use. Every set it returns is
// sealed, and a coordinate the machine lacks yields an empty set, not an
// error.
type TopologyIndex struct {
	m *Machine

	pkg     map[ID]*libcpu.CpuMask
	die     map[DieID]*libcpu.CpuMask
	cluster map[ClusterID]*libcpu.CpuMask
	node    map[ID]*libcpu.CpuMask
	cache   map[CacheID]*libcpu.CpuMask
	core    map[CoreID]*libcpu.CpuMask
	threads map[ID]*libcpu.CpuMask

	pkgs     []ID
	dies     []DieID
	clusters []ClusterID
	nodes    []ID
	caches   []CacheID
	cores    []CoreID
}

// buildIndex flattens a machine's zones into the maps a TopologyIndex
// answers from.
func (m *Machine) buildIndex() *TopologyIndex {
	t := &TopologyIndex{
		m:       m,
		pkg:     map[ID]*libcpu.CpuMask{},
		die:     map[DieID]*libcpu.CpuMask{},
		cluster: map[ClusterID]*libcpu.CpuMask{},
		node:    map[ID]*libcpu.CpuMask{},
		cache:   map[CacheID]*libcpu.CpuMask{},
		core:    map[CoreID]*libcpu.CpuMask{},
		threads: map[ID]*libcpu.CpuMask{},
	}

	for _, z := range m.zones[LevelPackage] {
		t.pkg[z.id] = z.cpus
		t.pkgs = append(t.pkgs, z.id)
	}
	for _, z := range m.zones[LevelDie] {
		id := z.DieID()
		t.die[id] = z.cpus
		t.dies = append(t.dies, id)
	}
	for _, z := range m.zones[LevelCluster] {
		id := z.ClusterID()
		t.cluster[id] = z.cpus
		t.clusters = append(t.clusters, id)
	}
	for _, id := range m.nodeIDs {
		t.node[id] = m.nodes[id].cpus
		t.nodes = append(t.nodes, id)
	}
	for key, cache := range m.caches {
		t.cache[key] = cache.cpus
		t.caches = append(t.caches, key)
	}
	for _, z := range m.zones[LevelCore] {
		id := CoreID{Package: z.pkg, Core: z.id}
		t.core[id] = z.cpus
		t.cores = append(t.cores, id)
	}
	for _, id := range m.cpuIDs {
		t.threads[id] = m.cpus[id].Threads()
	}

	slices.Sort(t.pkgs)
	slices.Sort(t.nodes)
	slices.SortFunc(t.dies, compareDieIDs)
	slices.SortFunc(t.clusters, compareClusterIDs)
	slices.SortFunc(t.caches, compareCacheIDs)
	slices.SortFunc(t.cores, compareCoreIDs)

	return t
}

func compareDieIDs(a, b DieID) int {
	if a.Package != b.Package {
		return a.Package - b.Package
	}
	return a.Die - b.Die
}

func compareClusterIDs(a, b ClusterID) int {
	if d := compareDieIDs(DieID{a.Package, a.Die}, DieID{b.Package, b.Die}); d != 0 {
		return d
	}
	return a.Cluster - b.Cluster
}

func compareCacheIDs(a, b CacheID) int {
	if a.Level != b.Level {
		return a.Level - b.Level
	}
	if a.Kind != b.Kind {
		return int(a.Kind) - int(b.Kind)
	}
	return a.ID - b.ID
}

func compareCoreIDs(a, b CoreID) int {
	if a.Package != b.Package {
		return a.Package - b.Package
	}
	return a.Core - b.Core
}

// TopologyIndex returns this machine's topology as a flat lookup table. It
// is computed once and shared.
func (m *Machine) TopologyIndex() *TopologyIndex {
	return m.index
}

//
// Coordinates
//
// These name a place in the topology and are usable as map keys.
//

// DieID identifies a die within a package.
type DieID struct {
	Package ID
	Die     ID
}

// ClusterID identifies a cluster within a die.
type ClusterID struct {
	Package ID
	Die     ID
	Cluster ID
}

// CacheID identifies a cache. The kernel numbers caches within a level and
// a kind, so all three are needed.
type CacheID struct {
	Level int
	Kind  CacheKind
	ID    ID
}

// CoreID identifies a physical core within a package.
type CoreID struct {
	Package ID
	Core    ID
}

// String returns "package#<p>/die#<d>".
func (d DieID) String() string {
	return fmt.Sprintf("package#%d/die#%d", d.Package, d.Die)
}

// String returns "package#<p>/die#<d>/cluster#<c>".
func (c ClusterID) String() string {
	return fmt.Sprintf("package#%d/die#%d/cluster#%d", c.Package, c.Die, c.Cluster)
}

// String returns "L<level>#<id>".
func (c CacheID) String() string {
	return fmt.Sprintf("L%d%s#%d", c.Level, c.Kind.suffix(), c.ID)
}

// String returns "package#<p>/core#<c>".
func (c CoreID) String() string {
	return fmt.Sprintf("package#%d/core#%d", c.Package, c.Core)
}

//
// CPUs by coordinate
//

// PackageCPUs returns the CPUs of the given package.
func (t *TopologyIndex) PackageCPUs(pkg ID) *libcpu.CpuMask {
	if cpus, ok := t.pkg[pkg]; ok {
		return cpus
	}
	return emptyCPUs
}

// DieCPUs returns the CPUs of the given die.
func (t *TopologyIndex) DieCPUs(die DieID) *libcpu.CpuMask {
	if cpus, ok := t.die[die]; ok {
		return cpus
	}
	return emptyCPUs
}

// ClusterCPUs returns the CPUs of the given cluster.
func (t *TopologyIndex) ClusterCPUs(cluster ClusterID) *libcpu.CpuMask {
	if cpus, ok := t.cluster[cluster]; ok {
		return cpus
	}
	return emptyCPUs
}

// MemoryNodeCPUs returns the CPUs local to the given NUMA node, which is empty
// for a node holding only memory.
func (t *TopologyIndex) MemoryNodeCPUs(node ID) *libcpu.CpuMask {
	if cpus, ok := t.node[node]; ok {
		return cpus
	}
	return emptyCPUs
}

// CacheCPUs returns the CPUs sharing the given cache.
func (t *TopologyIndex) CacheCPUs(cache CacheID) *libcpu.CpuMask {
	if cpus, ok := t.cache[cache]; ok {
		return cpus
	}
	return emptyCPUs
}

// CoreCPUs returns the CPUs of the given core, i.e. its hardware threads.
func (t *TopologyIndex) CoreCPUs(core CoreID) *libcpu.CpuMask {
	if cpus, ok := t.core[core]; ok {
		return cpus
	}
	return emptyCPUs
}

// ThreadsOf returns the CPUs sharing a core with the given CPU, including it.
func (t *TopologyIndex) ThreadsOf(cpu ID) *libcpu.CpuMask {
	if cpus, ok := t.threads[cpu]; ok {
		return cpus
	}
	return emptyCPUs
}

// CoreKindCPUs returns the CPUs of the given core kind.
func (t *TopologyIndex) CoreKindCPUs(kind CoreKind) *libcpu.CpuMask {
	return t.m.CoreKindCPUs(kind)
}

//
// Coordinates present in the machine
//
// All are ordered and stable across calls.
//

// PackageIDs returns the ids of all packages.
func (t *TopologyIndex) PackageIDs() []ID {
	return t.pkgs
}

// DieIDs returns every die of the machine, or the dies of the given packages
// if any are named.
func (t *TopologyIndex) DieIDs(pkgs ...ID) []DieID {
	if len(pkgs) == 0 {
		return t.dies
	}
	var out []DieID
	for _, die := range t.dies {
		if slices.Contains(pkgs, die.Package) {
			out = append(out, die)
		}
	}
	return out
}

// ClusterIDs returns every cluster of the machine, or the clusters of the
// given dies if any are named. It is empty on a machine whose clustering says
// nothing; see [LevelCluster].
func (t *TopologyIndex) ClusterIDs(dies ...DieID) []ClusterID {
	if len(dies) == 0 {
		return t.clusters
	}
	var out []ClusterID
	for _, cl := range t.clusters {
		if slices.Contains(dies, DieID{Package: cl.Package, Die: cl.Die}) {
			out = append(out, cl)
		}
	}
	return out
}

// MemoryNodeIDs returns the ids of all NUMA nodes.
func (t *TopologyIndex) MemoryNodeIDs() []ID {
	return t.nodes
}

// CacheIDs returns every cache of the machine, or the caches at the given
// levels if any are named.
func (t *TopologyIndex) CacheIDs(levels ...int) []CacheID {
	if len(levels) == 0 {
		return t.caches
	}
	var out []CacheID
	for _, id := range t.caches {
		if slices.Contains(levels, id.Level) {
			out = append(out, id)
		}
	}
	return out
}

// CacheLevels returns the cache levels the machine has, lowest first.
func (t *TopologyIndex) CacheLevels() []int {
	return t.m.CacheLevels()
}

// CoreIDs returns every core of the machine, or the cores of the given
// packages if any are named.
func (t *TopologyIndex) CoreIDs(pkgs ...ID) []CoreID {
	if len(pkgs) == 0 {
		return t.cores
	}
	var out []CoreID
	for _, core := range t.cores {
		if slices.Contains(pkgs, core.Package) {
			out = append(out, core)
		}
	}
	return out
}

// CoreKinds returns the core kinds the machine has.
func (t *TopologyIndex) CoreKinds() []CoreKind {
	return t.m.CoreKinds()
}

//
// Whole-machine sets, as [Machine] reports them
//

// AllCPUs returns every CPU the machine has, online or not.
func (t *TopologyIndex) AllCPUs() *libcpu.CpuMask {
	return t.m.PresentCPUs()
}

// OnlineCPUs returns the online CPUs.
func (t *TopologyIndex) OnlineCPUs() *libcpu.CpuMask {
	return t.m.OnlineCPUs()
}

// OfflineCPUs returns the CPUs which are present but not online.
func (t *TopologyIndex) OfflineCPUs() *libcpu.CpuMask {
	return t.m.OfflineCPUs()
}

// IsolatedCPUs returns the CPUs the kernel was told to isolate.
func (t *TopologyIndex) IsolatedCPUs() *libcpu.CpuMask {
	return t.m.IsolatedCPUs()
}

//
// Reverse lookup
//

// CoordinatesOf returns where the given CPU sits in the topology. The ids of
// coordinates the machine does not have, or which are not known for an offline
// CPU, are -1.
func (t *TopologyIndex) CoordinatesOf(cpu ID) Coordinates {
	c := t.m.CPU(cpu)
	if !c.Valid() {
		return Coordinates{
			CPU: unknownID, Package: unknownID, Die: unknownID,
			Cluster: unknownID, MemoryNode: unknownID, Core: unknownID,
		}
	}
	return c.coordinates()
}

// Coordinates is where one CPU sits in the topology.
type Coordinates struct {
	// CPU is the id of the CPU itself.
	CPU ID
	// Package is the package it is in.
	Package ID
	// Die is the die it is in, or -1 if unreported.
	Die ID
	// Cluster is the cluster it is in, or -1 if unreported.
	Cluster ID
	// MemoryNode is the NUMA node it is local to.
	MemoryNode ID
	// Core is the core it is a thread of, numbered within Package.
	Core ID
	// Kind is whether it is a performance or an efficiency core.
	Kind CoreKind
}

// String returns the coordinates in the form
// "cpu#3 package#0/die#0/cluster#1/node#0/core#3 (P-core)".
func (c Coordinates) String() string {
	return fmt.Sprintf(
		"cpu#%d package#%d/die#%d/cluster#%d/node#%d/core#%d (%s)",
		c.CPU, c.Package, c.Die, c.Cluster, c.MemoryNode, c.Core, c.Kind)
}

// String returns a multi-line description of the topology, for logs.
func (t *TopologyIndex) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%d CPUs (%s), %d online, %d isolated\n",
		t.AllCPUs().Size(), t.AllCPUs(), t.OnlineCPUs().Size(),
		t.IsolatedCPUs().Size())
	for _, pkg := range t.PackageIDs() {
		fmt.Fprintf(&b, "  package#%d: %s\n", pkg, t.PackageCPUs(pkg))
		for _, die := range t.DieIDs(pkg) {
			fmt.Fprintf(&b, "    %s: %s\n", die, t.DieCPUs(die))
		}
	}
	for _, node := range t.MemoryNodeIDs() {
		fmt.Fprintf(&b, "  node#%d: %s\n", node, t.MemoryNodeCPUs(node))
	}
	for _, cache := range t.CacheIDs() {
		fmt.Fprintf(&b, "  %s: %s\n", cache, t.CacheCPUs(cache))
	}

	return b.String()
}
