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
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"

	libcpu "github.com/containers/nri-plugins/pkg/lib/cpu"
)

// Where things are, relative to the host root.
const (
	sysCPUDir  = "sys/devices/system/cpu"
	sysNodeDir = "sys/devices/system/node"
	procMemDir = "proc"
)

// Discover reads the topology of a machine and returns it.
func Discover(opts ...Option) (*Machine, error) {
	o := &options{}
	for _, apply := range opts {
		if err := apply(o); err != nil {
			return nil, err
		}
	}
	if o.fsys == nil {
		o.fsys = HostFS("/")
	}

	d := &discovery{
		m: &Machine{
			fsys:   o.fsys,
			cpus:   map[ID]*CPU{},
			nodes:  map[ID]*MemoryNode{},
			caches: map[CacheID]*Cache{},
			kinds:  map[CoreKind]*libcpu.CpuMask{},
			zones:  map[Level][]*Zone{},
		},
		opts: o,
	}

	for _, step := range []struct {
		what string
		run  func() error
	}{
		{"CPUs", d.discoverCPUs},
		{"core kinds", d.discoverCoreKinds},
		{"NUMA nodes", d.discoverMemoryNodes},
		{"memory kinds", d.classifyMemory},
		{"zones", d.buildZones},
	} {
		if err := step.run(); err != nil {
			return nil, fmt.Errorf("failed to discover %s: %w", step.what, err)
		}
	}

	d.m.index = d.m.buildIndex()

	return d.m, nil
}

// discovery is the state of one Discover call.
type discovery struct {
	m    *Machine
	opts *options
}

func (d *discovery) fsys() fs.FS {
	return d.m.fsys
}

//
// CPUs
//

// discoverCPUs reads the machine-wide CPU sets the kernel publishes, then
// every cpuN directory.
func (d *discovery) discoverCPUs() error {
	m := d.m

	// Only "present" is essential. A missing set stays empty, except that
	// "online" falls back to "present".
	m.possible = d.readCPUsOrEmpty(path.Join(sysCPUDir, "possible"))
	m.present = d.readCPUsOrEmpty(path.Join(sysCPUDir, "present"))
	m.online = d.readCPUsOrEmpty(path.Join(sysCPUDir, "online"))
	m.isolated = d.readCPUsOrEmpty(path.Join(sysCPUDir, "isolated"))

	if m.online.IsEmpty() && !m.present.IsEmpty() {
		m.online = m.present
	}
	if m.possible.IsEmpty() {
		m.possible = m.present
	}

	names, ids, err := globIDs(d.fsys(), path.Join(sysCPUDir, "cpu[0-9]*"))
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return fmt.Errorf("no CPUs found under %s", sysCPUDir)
	}

	for i, name := range names {
		cpu, err := d.discoverCPU(name, ids[i])
		if err != nil {
			return fmt.Errorf("cpu%d: %w", ids[i], err)
		}
		m.cpus[cpu.id] = cpu
		m.cpuIDs = append(m.cpuIDs, cpu.id)
	}

	slices.Sort(m.cpuIDs)

	// No "present": trust the directories instead.
	if m.present.IsEmpty() {
		present := libcpu.NewCpuMask(m.cpuIDs...)
		present.Seal()
		m.present = present
		if m.online.IsEmpty() {
			m.online = present
		}
	}

	return nil
}

// discoverCPU reads one cpuN directory. An offline CPU's topology ids stay
// unknown.
func (d *discovery) discoverCPU(dir string, id ID) (*CPU, error) {
	c := &CPU{
		m:        d.m,
		id:       id,
		valid:    true,
		dir:      dir,
		online:   d.m.online.Contains(id),
		isolated: d.m.isolated.Contains(id),
		pkg:      unknownID,
		die:      unknownID,
		cluster:  unknownID,
		node:     unknownID,
		core:     unknownID,
	}

	if c.online {
		if err := d.readCPUTopology(c); err != nil {
			return nil, err
		}
	}

	c.freq = d.readCPUFreq(c)

	caches, err := d.discoverCPUCaches(c)
	if err != nil {
		return nil, err
	}
	c.caches = caches

	return c, nil
}

// readCPUTopology reads the topology/ ids of an online CPU. The package and
// core ids are required.
func (d *discovery) readCPUTopology(c *CPU) error {
	topo := path.Join(c.dir, "topology")

	pkg, err := readInt(d.fsys(), path.Join(topo, "physical_package_id"))
	if err != nil {
		return fmt.Errorf("no package id: %w", err)
	}
	c.pkg = pkg

	core, err := readInt(d.fsys(), path.Join(topo, "core_id"))
	if err != nil {
		return fmt.Errorf("no core id: %w", err)
	}
	c.core = core

	if c.die, err = readOptionalID(d.fsys(), path.Join(topo, "die_id")); err != nil {
		return err
	}
	if c.cluster, err = readOptionalID(d.fsys(), path.Join(topo, "cluster_id")); err != nil {
		return err
	}

	// core_cpus_list is the current name, thread_siblings_list the old one.
	threads, err := readCPUs(d.fsys(), path.Join(topo, "core_cpus_list"))
	if err != nil {
		threads, err = readCPUs(d.fsys(), path.Join(topo, "thread_siblings_list"))
		if err != nil {
			return fmt.Errorf("no thread siblings: %w", err)
		}
	}
	c.threads = threads

	// The NUMA node is a nodeN symlink in the CPU's directory. Without NUMA
	// there is none, and everything is in node 0.
	if nodes, err := glob(d.fsys(), path.Join(c.dir, "node[0-9]*")); err == nil {
		if len(nodes) == 1 {
			if node, ok := trailingID(nodes[0]); ok {
				c.node = node
			}
		}
	}
	if c.node == unknownID {
		c.node = 0
	}

	return nil
}

// readOptionalID reads an id the kernel may not report. A missing file and a
// negative id both read as unknownID.
func readOptionalID(fsys fs.FS, name string) (ID, error) {
	id, err := readInt(fsys, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return unknownID, nil
	case err != nil:
		return unknownID, err
	case id < 0:
		return unknownID, nil
	}
	return id, nil
}

// readCPUFreq reads what cpufreq says about a CPU, or returns its override.
func (d *discovery) readCPUFreq(c *CPU) Freq {
	if freq, ok := d.opts.freq[c.id]; ok {
		return freq
	}

	dir := path.Join(c.dir, "cpufreq")
	freq := Freq{EPP: EPPUnknown}

	if base, err := readUint64(d.fsys(), path.Join(dir, "base_frequency")); err == nil {
		freq.Base = base
	}
	if min, err := readUint64(d.fsys(), path.Join(dir, "cpuinfo_min_freq")); err == nil {
		freq.Min = min
	}
	if max, err := readUint64(d.fsys(), path.Join(dir, "cpuinfo_max_freq")); err == nil {
		freq.Max = max
	}
	if epp, err := readFile(d.fsys(), path.Join(dir, "energy_performance_preference")); err == nil {
		freq.EPP = ParseEPP(epp)
	}

	return freq
}

//
// Caches
//

// discoverCPUCaches reads the caches of one CPU, lowest level first. A
// shared cache is read once and keyed by (level, kind, id).
func (d *discovery) discoverCPUCaches(c *CPU) ([]*Cache, error) {
	if caches, ok := d.opts.caches[c.id]; ok {
		return d.internCaches(caches), nil
	}

	names, _, err := globIDs(d.fsys(), path.Join(c.dir, "cache", "index[0-9]*"))
	if err != nil {
		return nil, err
	}

	caches := make([]*Cache, 0, len(names))
	for _, name := range names {
		cache, err := d.readCache(name)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		caches = append(caches, cache)
	}

	// Order by level, then kind. Index order is normally the same, but
	// nothing promises that.
	slices.SortStableFunc(caches, func(a, b *Cache) int {
		if a.level != b.level {
			return a.level - b.level
		}
		return int(a.kind) - int(b.kind)
	})

	return caches, nil
}

// readCache reads one cache/indexN directory, returning the shared instance if
// this cache was seen already.
func (d *discovery) readCache(dir string) (*Cache, error) {
	level, err := readInt(d.fsys(), path.Join(dir, "level"))
	if err != nil {
		return nil, fmt.Errorf("no level: %w", err)
	}

	kindStr, err := readFile(d.fsys(), path.Join(dir, "type"))
	if err != nil {
		return nil, fmt.Errorf("no type: %w", err)
	}
	kind, err := parseCacheKind(kindStr)
	if err != nil {
		return nil, err
	}

	// A cache without an id falls back to the lowest CPU sharing it, which is
	// unique within a level and kind.
	id, err := readInt(d.fsys(), path.Join(dir, "id"))
	haveID := err == nil

	cpus, err := readCPUs(d.fsys(), path.Join(dir, "shared_cpu_list"))
	if err != nil {
		return nil, fmt.Errorf("no shared CPUs: %w", err)
	}
	if !haveID {
		if cpus.IsEmpty() {
			return nil, fmt.Errorf("no id and no shared CPUs")
		}
		id = cpus.List()[0]
	}

	key := CacheID{Level: level, Kind: kind, ID: id}
	if have, ok := d.m.caches[key]; ok {
		return have, nil
	}

	size := int64(0)
	if str, err := readFile(d.fsys(), path.Join(dir, "size")); err == nil {
		if size, err = parseSize(str); err != nil {
			return nil, fmt.Errorf("bad size %q: %w", str, err)
		}
	}

	cache := &Cache{
		m:     d.m,
		valid: true,
		id:    id,
		level: level,
		kind:  kind,
		size:  size,
		cpus:  cpus,
	}
	d.m.caches[key] = cache

	return cache, nil
}

// internCaches turns overridden cache descriptions into shared instances.
func (d *discovery) internCaches(caches []*Cache) []*Cache {
	out := make([]*Cache, 0, len(caches))
	for _, c := range caches {
		key := CacheID{Level: c.level, Kind: c.kind, ID: c.id}
		have, ok := d.m.caches[key]
		if !ok {
			c.m, c.valid = d.m, true
			d.m.caches[key] = c
			have = c
		}
		out = append(out, have)
	}
	return out
}

//
// Core kinds
//

// discoverCoreKinds works out which CPUs are performance cores and which
// are efficiency cores. See "Discovery" in the package doc.
func (d *discovery) discoverCoreKinds() error {
	m := d.m

	if len(d.opts.kinds) > 0 {
		for kind, cpus := range d.opts.kinds {
			m.kinds[kind] = cpus
		}
	} else {
		for kind, dir := range coreKindDirs {
			cpus, err := readCPUs(d.fsys(), dir)
			if err != nil || cpus.IsEmpty() {
				continue
			}
			m.kinds[kind] = cpus
		}
	}

	switch len(m.kinds) {
	case 0:
		m.kinds[PerformanceCore] = m.online

	case 1:
		for kind, cpus := range m.kinds {
			// Round the named kind up to whole cores; the other
			// kind gets the rest.
			named := m.allThreads(cpus)
			if named.Equals(m.online) {
				break
			}
			rest := m.online.Difference(named)

			named.Seal()
			other := libcpu.NewCpuMask(rest.UnsortedList()...)
			other.Seal()

			m.kinds[kind] = named
			m.kinds[otherCoreKind(kind)] = other
			break
		}
	}

	return d.checkCoreKinds()
}

// checkCoreKinds rejects an impossible core kind split: kinds must be whole
// cores, a core cannot be of two kinds, and every online CPU must have one.
func (d *discovery) checkCoreKinds() error {
	m := d.m

	seen := libcpu.NewCpuMask()
	for kind, cpus := range m.kinds {
		if missing := m.allThreads(cpus).Difference(cpus); !missing.IsEmpty() {
			return fmt.Errorf("%s CPUs (%s) are missing thread siblings (%s)",
				kind, cpus, missing)
		}
		if overlap := cpus.Intersection(seen); !overlap.IsEmpty() {
			return fmt.Errorf("%s CPUs (%s) overlap another kind (%s)",
				kind, cpus, overlap)
		}
		seen = libcpu.NewCpuMask(seen.Union(cpus).UnsortedList()...)
	}

	if missing := m.online.Difference(seen); !missing.IsEmpty() {
		return fmt.Errorf("CPUs %s are of no known core kind", missing)
	}

	for kind, cpus := range m.kinds {
		cpus.ForEachCpu(func(id int) bool {
			if c, ok := m.cpus[id]; ok {
				c.kind = kind
			}
			return true
		})
	}

	return nil
}

//
// Memory nodes
//

// discoverMemoryNodes reads the NUMA nodes. Without node directories, one
// node holds every online CPU and all of the memory.
func (d *discovery) discoverMemoryNodes() error {
	m := d.m

	names, ids, err := globIDs(d.fsys(), path.Join(sysNodeDir, "node[0-9]*"))
	if err != nil {
		return err
	}

	if len(names) == 0 {
		node := &MemoryNode{
			m:        m,
			valid:    true,
			id:       0,
			cpus:     m.online,
			distance: []int{localDistance},
			normal:   true,
			kind:     MemoryKindDRAM,
			meminfo:  path.Join(procMemDir, "meminfo"),
		}

		// Unknown capacity would read as no memory, so failing is better.
		info, err := readMemInfo(d.fsys(), node.meminfo, node.id)
		if err != nil {
			return fmt.Errorf("no NUMA nodes and no %s: %w", node.meminfo, err)
		}
		node.capacity = info.Total

		m.nodes[0] = node
		m.nodeIDs = []ID{0}
		return nil
	}

	normal := d.readCPUsOrEmpty(path.Join(sysNodeDir, "has_normal_memory"))

	for i, name := range names {
		node := &MemoryNode{
			m:       m,
			valid:   true,
			id:      ids[i],
			dir:     name,
			meminfo: path.Join(name, "meminfo"),
			kind:    MemoryKindUnknown,
			normal:  normal.Contains(ids[i]),
		}

		if node.cpus, err = readCPUs(d.fsys(), path.Join(name, "cpulist")); err != nil {
			return fmt.Errorf("node%d: no CPU list: %w", ids[i], err)
		}
		if node.distance, err = readInts(d.fsys(), path.Join(name, "distance"), " "); err != nil {
			return fmt.Errorf("node%d: no distance vector: %w", ids[i], err)
		}

		info, err := readMemInfo(d.fsys(), node.meminfo, ids[i])
		if err != nil {
			return fmt.Errorf("node%d: %w", ids[i], err)
		}
		node.capacity = info.Total

		m.nodes[node.id] = node
		m.nodeIDs = append(m.nodeIDs, node.id)
	}

	slices.Sort(m.nodeIDs)

	d.symmetrizeDistances()

	return nil
}

// symmetrizeDistances averages a NUMA distance matrix which is not
// symmetric.
func (d *discovery) symmetrizeDistances() {
	m := d.m

	for _, i := range m.nodeIDs {
		for _, j := range m.nodeIDs {
			if i >= j {
				continue
			}
			a, b := m.nodes[i], m.nodes[j]
			if j >= len(a.distance) || i >= len(b.distance) {
				continue
			}
			if a.distance[j] == b.distance[i] {
				continue
			}
			avg := (a.distance[j] + b.distance[i]) / 2
			a.distance[j], b.distance[i] = avg, avg
		}
	}
}

//
// Options
//

// Option configures [Discover].
type Option func(*options) error

// options is the accumulated configuration of a [Discover] call.
type options struct {
	fsys   fs.FS
	kinds  map[CoreKind]*libcpu.CpuMask
	caches map[ID][]*Cache
	freq   map[ID]Freq
}

// WithRoot reads the topology below root instead of "/", for a host filesystem
// mounted elsewhere. It is shorthand for WithFS(HostFS(root)).
func WithRoot(root string) Option {
	return func(o *options) error {
		o.fsys = HostFS(root)
		return nil
	}
}

// WithFS reads the topology through fsys instead of the real filesystem.
// Paths are relative to the host root, so fsys must hold both "sys" and
// "proc".
func WithFS(fsys fs.FS) Option {
	return func(o *options) error {
		if fsys == nil {
			return fmt.Errorf("WithFS: nil filesystem")
		}
		o.fsys = fsys
		return nil
	}
}

//
// helpers
//

// unknownID is the id of a coordinate the machine does not report.
const unknownID = -1

// localDistance is the NUMA distance from a node to itself.
const localDistance = 10

// coreKindDirs is where the kernel lists the CPUs of each core kind.
var coreKindDirs = map[CoreKind]string{
	PerformanceCore: "sys/devices/cpu_core/cpus",
	EfficientCore:   "sys/devices/cpu_atom/cpus",
}

// otherCoreKind returns the kind which is not this one.
func otherCoreKind(kind CoreKind) CoreKind {
	if kind == PerformanceCore {
		return EfficientCore
	}
	return PerformanceCore
}

// readCPUsOrEmpty reads a CPU list, treating anything unreadable as empty.
func (d *discovery) readCPUsOrEmpty(name string) *libcpu.CpuMask {
	if cpus, err := readCPUs(d.fsys(), name); err == nil {
		return cpus
	}
	return emptyCPUs
}

// allThreads rounds a set of CPUs up to whole cores. Discovery needs it
// before a Machine is finished.
func (m *Machine) allThreads(cpus libcpu.CPUSet) *libcpu.CpuMask {
	all := libcpu.NewCpuMask()
	cpus.ForEachCpu(func(id int) bool {
		if c, ok := m.cpus[id]; ok && c.threads != nil {
			all.Set(c.threads.UnsortedList()...)
		} else {
			all.Set(id)
		}
		return true
	})
	return all
}
