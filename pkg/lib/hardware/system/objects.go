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

package system

import (
	"fmt"
	"path"
	"slices"
	"strconv"

	libcpu "github.com/containers/nri-plugins/pkg/lib/cpu"
	"github.com/containers/nri-plugins/pkg/lib/hardware"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	"github.com/intel/goresctrl/pkg/sst"
	idset "github.com/intel/goresctrl/pkg/utils"
)

// toCPUSet converts a hardware CPU set to a cpuset.CPUSet.
func toCPUSet(cpus interface{ List() []int }) cpuset.CPUSet {
	return cpuset.New(cpus.List()...)
}

//
// CPU
//

// cpu implements [CPU] over a hardware.CPU.
type cpu struct {
	sys *system
	hw  *hardware.CPU
}

func (c *cpu) ID() idset.ID {
	return c.hw.ID()
}

func (c *cpu) PackageID() idset.ID {
	return c.hw.PackageID()
}

// DieID returns the CPU's die, 0 if unreported, as in pkg/sysfs.
func (c *cpu) DieID() idset.ID {
	return sysfsID(c.hw.DieID())
}

// ClusterID returns the CPU's cluster, 0 if unreported, as in pkg/sysfs.
func (c *cpu) ClusterID() idset.ID {
	return sysfsID(c.hw.ClusterID())
}

// sysfsID is the id pkg/sysfs reports for a hardware id: 0 for -1, the zero
// value of a field it never assigns.
func sysfsID(id idset.ID) idset.ID {
	return max(id, 0)
}

func (c *cpu) NodeID() idset.ID {
	return c.hw.NodeID()
}

func (c *cpu) CoreID() idset.ID {
	return c.hw.CoreID()
}

func (c *cpu) ThreadCPUSet() cpuset.CPUSet {
	return toCPUSet(c.hw.Threads())
}

func (c *cpu) BaseFrequency() uint64 {
	return c.hw.Freq().Base
}

func (c *cpu) FrequencyRange() CPUFreq {
	freq := c.hw.Freq()
	return CPUFreq{Base: freq.Base, Min: freq.Min, Max: freq.Max}
}

func (c *cpu) EPP() EPP {
	return EPP(c.hw.Freq().EPP)
}

func (c *cpu) Online() bool {
	return c.hw.Online()
}

func (c *cpu) Isolated() bool {
	return c.hw.Isolated()
}

// SetFrequencyLimits writes the scaling limits for this CPU, clamped to the
// range cpufreq reports. A CPU without cpufreq support is left alone.
func (c *cpu) SetFrequencyLimits(min, max uint64) error {
	freq := c.hw.Freq()
	if freq.Min == 0 {
		return nil
	}

	min /= 1000
	max /= 1000
	if min < freq.Min && min != 0 {
		min = freq.Min
	}
	if min > freq.Max {
		min = freq.Max
	}
	if max < freq.Min && max != 0 {
		max = freq.Min
	}
	if max > freq.Max {
		max = freq.Max
	}

	dir := path.Join(sysCPUDir, "cpu"+strconv.Itoa(c.hw.ID()), "cpufreq")
	if err := c.sys.write(path.Join(dir, "scaling_min_freq"), min); err != nil {
		return err
	}

	return c.sys.write(path.Join(dir, "scaling_max_freq"), max)
}

// SstClos returns the SST-CP CLOS this CPU is in, or -1 when SST prioritization
// is not in effect.
func (c *cpu) SstClos() int {
	if clos, ok := c.sys.sstClos[c.hw.ID()]; ok {
		return clos
	}
	return -1
}

func (c *cpu) CacheCount() int {
	return len(c.hw.Caches())
}

func (c *cpu) GetCaches() []*Cache {
	return c.sys.wrapCaches(c.hw.Caches())
}

// GetCachesByLevel returns this CPU's caches of one level. Like pkg/sysfs, it
// relies on caches being ordered by level and stops at the first higher one.
func (c *cpu) GetCachesByLevel(level int) []*Cache {
	var caches []*Cache

	for _, cache := range c.hw.Caches() {
		if cache.Level() == level {
			caches = append(caches, c.sys.wrapCache(cache))
		} else if cache.Level() > level {
			break
		}
	}

	return caches
}

func (c *cpu) GetCacheByIndex(idx int) *Cache {
	caches := c.hw.Caches()
	if 0 <= idx && idx < len(caches) {
		return c.sys.wrapCache(caches[idx])
	}
	return nil
}

// GetNthLevelCacheCPUSet returns the CPUs sharing any of this CPU's caches at
// level n, or its thread siblings when it has no caches at all. Separate data
// and instruction caches at one level both contribute.
func (c *cpu) GetNthLevelCacheCPUSet(n int) cpuset.CPUSet {
	caches := c.hw.Caches()
	if len(caches) == 0 {
		return c.ThreadCPUSet()
	}

	cpus := cpuset.New()
	for _, cache := range caches {
		if cache.Level() == n {
			cpus = cpus.Union(toCPUSet(cache.CPUs()))
		} else if cache.Level() > n {
			break
		}
	}

	return cpus
}

// GetLastLevelCaches returns this CPU's caches of its highest level, in the
// reverse order of GetCaches, as in pkg/sysfs.
func (c *cpu) GetLastLevelCaches() []*Cache {
	hw := c.hw.Caches()
	if len(hw) < 1 {
		return nil
	}

	var (
		caches    []*Cache
		lastIndex = len(hw) - 1
		lastLevel = hw[lastIndex].Level()
	)

	for idx := lastIndex; idx >= 0; idx-- {
		caches = append(caches, c.sys.wrapCache(hw[idx]))
		if hw[idx].Level() != lastLevel {
			break
		}
	}

	return caches
}

// GetLastLevelCacheCPUSet returns the CPUs sharing this CPU's highest level
// caches, or its thread siblings when it has none.
func (c *cpu) GetLastLevelCacheCPUSet() cpuset.CPUSet {
	hw := c.hw.Caches()
	if len(hw) < 1 {
		return c.ThreadCPUSet()
	}

	var (
		lastIndex = len(hw) - 1
		lastLevel = hw[lastIndex].Level()
		cpus      = cpuset.New()
	)

	for idx := lastIndex; idx >= 0; idx-- {
		cpus = cpus.Union(toCPUSet(hw[idx].CPUs()))
		if hw[idx].Level() != lastLevel {
			break
		}
	}

	return cpus
}

func (c *cpu) CoreKind() CoreKind {
	return CoreKind(c.hw.Kind())
}

var _ CPU = (*cpu)(nil)

//
// Node
//

// node implements [Node] over a hardware.MemoryNode.
type node struct {
	sys *system
	hw  *hardware.MemoryNode
}

func (n *node) ID() idset.ID {
	return n.hw.ID()
}

// PackageID returns the package this node belongs to. A node without CPUs
// of its own reports 0, as in pkg/sysfs.
func (n *node) PackageID() idset.ID {
	return sysfsID(n.hw.PackageID())
}

// DieID returns the die this node belongs to, with the same caveat as
// [node.PackageID].
func (n *node) DieID() idset.ID {
	return sysfsID(n.hw.DieID())
}

func (n *node) CPUSet() cpuset.CPUSet {
	return toCPUSet(n.hw.CPUs())
}

func (n *node) Distance() []int {
	return n.hw.Distances()
}

func (n *node) DistanceFrom(id idset.ID) int {
	return n.hw.Distance(id)
}

// ClosestNodes returns the other nodes grouped by distance, nearest first.
func (n *node) ClosestNodes() ([]idset.IDSet, []int) {
	groups := hardware.ClosestMemoryNodes(n.sys.hw, n.hw.ID(), nil)

	nodes := make([]idset.IDSet, 0, len(groups))
	distances := make([]int, 0, len(groups))
	for _, g := range groups {
		nodes = append(nodes, idset.NewIDSet(g.Nodes...))
		distances = append(distances, g.Distance)
	}

	return nodes, distances
}

// MemoryInfo reads this node's current memory usage on every call.
func (n *node) MemoryInfo() (*MemInfo, error) {
	info, err := n.hw.Usage()
	if err != nil {
		return nil, err
	}

	return &MemInfo{
		MemTotal: uint64(info.Total),
		MemFree:  uint64(info.Free),
		MemUsed:  uint64(info.Used),
	}, nil
}

// GetMemoryType returns the kind of memory this node holds. A node hardware
// could not classify is reported as DRAM.
func (n *node) GetMemoryType() MemoryType {
	switch n.hw.Kind() {
	case hardware.MemoryKindPMEM:
		return MemoryTypePMEM
	case hardware.MemoryKindHBM:
		return MemoryTypeHBM
	}
	return MemoryTypeDRAM
}

func (n *node) HasNormalMemory() bool {
	return n.hw.HasNormalMemory()
}

var _ Node = (*node)(nil)

//
// CPUPackage
//

// cpuPackage implements [CPUPackage] over the zones of one package.
type cpuPackage struct {
	sys *system
	hw  *hardware.Zone
}

func (p *cpuPackage) ID() idset.ID {
	return p.hw.ID()
}

func (p *cpuPackage) CPUSet() cpuset.CPUSet {
	return toCPUSet(p.hw.CPUs())
}

func (p *cpuPackage) DieIDs() []idset.ID {
	var ids []idset.ID
	for _, die := range p.dies() {
		ids = append(ids, sysfsID(die.ID()))
	}
	return ids
}

// NodeIDs returns the NUMA nodes whose CPUs are in this package.
func (p *cpuPackage) NodeIDs() []idset.ID {
	return sortedIDs(hardware.MemoryNodesFor(p.sys.hw, p.hw.CPUs()))
}

func (p *cpuPackage) DieNodeIDs(id idset.ID) []idset.ID {
	die := p.die(id)
	if die == nil {
		return []idset.ID{}
	}
	return sortedIDs(hardware.MemoryNodesFor(p.sys.hw, die.CPUs()))
}

func (p *cpuPackage) DieCPUSet(id idset.ID) cpuset.CPUSet {
	die := p.die(id)
	if die == nil {
		return cpuset.New()
	}
	return toCPUSet(die.CPUs())
}

func (p *cpuPackage) DieClusterIDs(die idset.ID) []idset.ID {
	var ids []idset.ID
	for _, cluster := range p.clusters(die) {
		ids = append(ids, sysfsID(cluster.ID()))
	}
	slices.Sort(ids)
	return ids
}

func (p *cpuPackage) DieClusterCPUSet(die, cluster idset.ID) cpuset.CPUSet {
	for _, z := range p.clusters(die) {
		if sysfsID(z.ID()) == cluster {
			return toCPUSet(z.CPUs())
		}
	}
	return cpuset.New()
}

// LogicalDieClusterIDs returns the cluster ids of a die, with clusters holding
// only one core's threads merged into one, as pkg/sysfs reports them.
func (p *cpuPackage) LogicalDieClusterIDs(die idset.ID) []idset.ID {
	var ids []idset.ID
	for _, cpus := range p.logicalClusters(die) {
		ids = append(ids, sysfsID(p.sys.hw.CPU(cpus.List()[0]).ClusterID()))
	}
	slices.Sort(ids)
	return ids
}

func (p *cpuPackage) LogicalDieClusterCPUSet(die, cluster idset.ID) cpuset.CPUSet {
	for _, cpus := range p.logicalClusters(die) {
		if sysfsID(p.sys.hw.CPU(cpus.List()[0]).ClusterID()) == cluster {
			return toCPUSet(cpus)
		}
	}
	return cpuset.New()
}

// logicalClusters returns the clusters of a die of this package, merging the
// single-core ones as pkg/sysfs does.
func (p *cpuPackage) logicalClusters(die idset.ID) []*libcpu.CpuMask {
	z := p.die(die)
	if z == nil {
		return nil
	}
	return hardware.LogicalClusters(p.sys.hw, p.hw.ID(), z.DieID().Die,
		hardware.MergeSingleCoreClusters)
}

// L3CacheIDs returns the ids of the level 3 caches this package's CPUs use.
func (p *cpuPackage) L3CacheIDs() []idset.ID {
	var ids []idset.ID
	for _, z := range p.l3Caches() {
		ids = append(ids, z.ID())
	}
	slices.Sort(ids)
	return ids
}

func (p *cpuPackage) L3CacheCPUSet(id idset.ID) cpuset.CPUSet {
	for _, z := range p.l3Caches() {
		if z.ID() == id {
			return toCPUSet(z.CPUs())
		}
	}
	return cpuset.New()
}

// SstInfo returns the SST status of this package, or nil when SST is not in use.
func (p *cpuPackage) SstInfo() *sst.PackageStatus {
	return p.sys.sstPkg[p.hw.ID()]
}

// dies returns the die zones of this package, ordered by id.
func (p *cpuPackage) dies() []*hardware.Zone {
	return hardware.ZonesWithin(p.sys.hw, hardware.LevelDie, p.hw.CPUs())
}

// die returns one die zone of this package, or nil.
func (p *cpuPackage) die(id idset.ID) *hardware.Zone {
	for _, z := range p.dies() {
		if sysfsID(z.ID()) == id {
			return z
		}
	}
	return nil
}

// clusters returns the cluster zones of one die of this package.
func (p *cpuPackage) clusters(die idset.ID) []*hardware.Zone {
	z := p.die(die)
	if z == nil {
		return nil
	}
	return hardware.ZonesWithin(p.sys.hw, hardware.LevelCluster, z.CPUs())
}

// l3Caches returns the level 3 cache zones overlapping this package's CPUs.
// A cache shared across packages belongs to each, with its whole CPU set.
func (p *cpuPackage) l3Caches() []*hardware.Zone {
	return hardware.ZonesOverlapping(p.sys.hw, hardware.LevelL3Cache, p.hw.CPUs())
}

var _ CPUPackage = (*cpuPackage)(nil)

//
// Cache
//

// Cache has details about a CPU cache.
//
// Its methods tolerate a nil receiver. There is one *Cache per cache, so it
// works as a map key.
type Cache struct {
	hw *hardware.Cache
}

// ID returns the id of the cache, or 0 for a nil receiver.
func (c *Cache) ID() int {
	if c == nil {
		return 0
	}
	return c.hw.ID()
}

// Level returns the level of the cache, or 0 for a nil receiver.
func (c *Cache) Level() int {
	if c == nil {
		return 0
	}
	return c.hw.Level()
}

// Type returns the type of the cache, or 0 for a nil receiver.
func (c *Cache) Type() CacheType {
	if c == nil {
		return 0
	}
	return CacheType(c.hw.Kind())
}

// Size returns the size of the cache in bytes, or 0 for a nil receiver.
func (c *Cache) Size() uint64 {
	if c == nil {
		return 0
	}
	return uint64(c.hw.Size())
}

// SharedCPUSet returns the CPUs sharing the cache, or an empty set for a nil
// receiver.
func (c *Cache) SharedCPUSet() cpuset.CPUSet {
	if c == nil {
		return cpuset.New()
	}
	return toCPUSet(c.hw.CPUs())
}

// String describes the cache, for logs.
func (c *Cache) String() string {
	if c == nil {
		return "<nil cache>"
	}
	return fmt.Sprintf("L%d#%d", c.Level(), c.ID())
}

//
// helpers
//

// sortedIDs returns ids in increasing order, never nil.
func sortedIDs(ids []idset.ID) []idset.ID {
	out := slices.Clone(ids)
	if out == nil {
		out = []idset.ID{}
	}
	slices.Sort(out)
	return out
}
