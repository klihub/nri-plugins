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

package generic

import (
	"fmt"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	idset "github.com/intel/goresctrl/pkg/utils"

	libmem "github.com/containers/nri-plugins/pkg/resmgr/lib/memory"
)

// Pool represents a collection of resources which are typically associated
// to some part of the system topology, for instance a socket, die within a
// socket, or a set of CPUs close(st) to a NUMA node.
type Pool struct {
	name     string
	kind     PoolKind
	id       int
	parent   *Pool
	children []*Pool
	cpus     cpuset.CPUSet
	mems     idset.IDSet
}

type PoolKind int

const (
	// Virtual root in case of multi-socket systems.
	PoolKindRoot PoolKind = iota
	// Socket of all CPU cores.
	PoolKindSocket
	// Die within a socket.
	PoolKindDie
	// NUMA node.
	PoolKindNumaNode
	// A set of cores grouped by some property.
	PoolKindCoreGroup
	// Full physical cores. Includes all sibling threads in all cores.
	PoolKindFullCores
	// Threads in physical cores. Includes only the Nth thread in each core.
	PoolKindThreads
)

var (
	noMem    idset.IDSet = nil
	noParent *Pool       = nil
)

func NewPool(kind PoolKind, id int, cpus cpuset.CPUSet, mems idset.IDSet, parent *Pool, name string) *Pool {
	p := &Pool{
		kind:   kind,
		id:     id,
		parent: parent,
		cpus:   cpus.Clone(),
		mems:   mems.Clone(),
	}

	if parent != nil {
		parent.children = append(parent.children, p)
	}

	return p
}

func (p *Pool) AddChild(child *Pool) {
	child.parent = p
	p.children = append(p.children, child)
}

func (p *Pool) Flatten() {

}

func (p *policy) setupPools() error {
	nodes := []*libmem.Node{}
	for _, id := range p.sys.NodeIDs() {
		nodes = append(nodes, libmem.SystemNode(p.sys.Node(id)))
	}
	_ = libmem.NewAllocator(nodes...)

	root, err := p.buildRootPool()
	if err != nil {
		return err
	}

	// assign memory to pools
	//   1. assign memory IDs to NUMA node pools
	//   2. for each subtree rooted at a NUMA node pool, assign the NUMA node memory ID to
	//     each subtree pool
	//   3. depth-first assing to each node the union of the memory in its children

	// filter out/combine useless nodes in the tree (nodes which only a single child)

	//root = root.Flatten()

	p.root = root
	return nil
}

func (p *policy) buildRootPool() (*Pool, error) {
	log.Debug("building resource pools...")

	root := NewPool(PoolKindRoot, 0, p.allowed, noMem, noParent, "")

	for _, pkgID := range p.sys.PackageIDs() {
		if _, err := p.buildSocketPool(pkgID, root); err != nil {
			return nil, err
		}
	}

	return root, nil
}

func (p *policy) buildSocketPool(pkgID idset.ID, root *Pool) (*Pool, error) {
	pkg := p.sys.Package(pkgID)
	cpu := pkg.CPUSet().Intersection(p.allowed)
	socket := NewPool(PoolKindSocket, pkgID, cpu, noMem, root, "")

	log.Debug("building pool for socket #%d...", pkgID)

	for _, dieID := range pkg.DieIDs() {
		if _, err := p.buildDiePool(dieID, socket); err != nil {
			return nil, err
		}
	}

	return socket, nil
}

func (p *policy) buildDiePool(dieID idset.ID, socket *Pool) (*Pool, error) {
	pkg := p.sys.Package(socket.id)
	cpu := pkg.DieCPUSet(dieID).Intersection(p.allowed)
	name := fmt.Sprintf("#%d:%d", socket.id, dieID)
	die := NewPool(PoolKindDie, dieID, cpu, noMem, socket, name)

	log.Debug("building pool for die %s...", die.name)

	cpuless := []idset.ID{}
	for _, nodeID := range pkg.DieNodeIDs(dieID) {
		if !p.sys.Node(nodeID).CPUSet().IsEmpty() {
			if _, err := p.buildNumaNodePool(nodeID, die); err != nil {
				return nil, err
			}
		} else {
			cpuless = append(cpuless, nodeID)
		}
	}

	if len(cpuless) > 0 {
		log.Debug("die #%d has CPU-less NUMA nodes: %v", dieID, cpuless)
		// assign CPUless NUMA nodes to the closest NUMA node with CPUs
	}

	return die, nil
}

func (p *policy) buildNumaNodePool(nodeID idset.ID, die *Pool) (*Pool, error) {
	sysNode := p.sys.Node(nodeID)
	cpu := sysNode.CPUSet().Intersection(p.allowed)
	mem := idset.NewIDSet(nodeID)
	node := NewPool(PoolKindNumaNode, nodeID, cpu, mem, die, "")

	log.Debug("building pool for NUMA node #%d...", nodeID)

	for _, grp := range p.splitCPUCoreGroups(cpu) {
		if _, err := p.buildCPUGroupPool(grp, node); err != nil {
			return nil, err
		}
	}

	return node, nil
}

func (p *policy) buildCPUGroupPool(g *Pool, parent *Pool) (*Pool, error) {
	log.Debug("building pool for CPU group %q...", g.name)

	group := NewPool(PoolKindCoreGroup, g.id, g.cpus, noMem, parent, g.name)
	for idx, tset := range p.SplitCPUSetByThreads(g.cpus) {
		log.Debug("building pool for thread group %q/#%d...", g.name, idx)
		_ = NewPool(PoolKindThreads, idx, tset, noMem, group, g.name)
	}
	return group, nil
}

func (p *policy) splitCPUCoreGroups(cpus cpuset.CPUSet) []*Pool {
	// XXX TODO split to groups based on at least
	//   - core type (P- vs. E-cores),
	// and perhaps
	//   - EPP profile
	//   - CPU min/max frequency ?

	return []*Pool{
		{
			name: "cores " + cpus.String(),
			cpus: cpus,
		},
	}
}

// SplitCPUSetByThreads splits a cpuset to multiple ones by threads.
func (p *policy) SplitCPUSetByThreads(cset cpuset.CPUSet) []cpuset.CPUSet {
	idsByThread := map[int][]int{}
	threadCnt := 0

	for _, cid := range cset.List() {
		tids := p.sys.CPU(cid).ThreadCPUSet().List()
		for idx, tid := range tids {
			idsByThread[idx] = append(idsByThread[idx], tid)
		}
		if threadCnt < len(tids) {
			threadCnt = len(tids)
		}
	}

	split := make([]cpuset.CPUSet, threadCnt)
	for idx, byThread := range idsByThread {
		split[idx] = cpuset.New(byThread...)
	}

	return split
}
