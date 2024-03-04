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

package memory

import (
	"fmt"
	"slices"

	logger "github.com/containers/nri-plugins/pkg/log"
	system "github.com/containers/nri-plugins/pkg/sysfs"
	idset "github.com/intel/goresctrl/pkg/utils"
)

var (
	log = logger.Get("libmem")
)

type (
	ID    = idset.ID
	IDSet = idset.IDSet
)

// Reservation of memory from a set of NUMA nodes.
type Reservation map[ID]int64

func (r Reservation) Add(id ID, amount int64) {
	r[id] += amount
}

func (r Reservation) Amount() int64 {
	total := int64(0)
	for _, amount := range r {
		total += amount
	}
	return total
}

func (r Reservation) IDs() []ID {
	ids := make([]ID, 0, len(r))
	for id := range r {
		ids = append(ids, id)
	}
	return ids
}

func (r Reservation) IDSet() IDSet {
	return idset.NewIDSet(r.IDs()...)
}

// Allocator tracks allocations from a set of NUMA nodes.
type Allocator struct {
	nodes map[ID]*Node
	ids   []ID
}

func NewAllocator(nodes ...*Node) *Allocator {
	a := &Allocator{
		nodes: make(map[ID]*Node),
		ids:   make([]ID, len(nodes)),
	}

	for _, n := range nodes {
		if _, ok := a.nodes[n.id]; ok {
			panic(fmt.Errorf("can't register %s node #%v, already registered", n.kind, n.id))
		}
		if distCnt, nodeCnt := len(n.distance), len(nodes); distCnt < nodeCnt {
			panic(fmt.Errorf("only %d distances for %s node #%v, %d expected", distCnt,
				n.kind, n.id, nodeCnt))
		}
	}

	for i, n := range nodes {
		a.nodes[n.id] = n
		a.ids[i] = n.id
	}
	slices.SortFunc(a.ids, func(a, b ID) int { return a - b })

	for _, n := range a.nodes {
		if n.overflow != nil {
			if err := a.verifyOverflow(n); err != nil {
				panic(err)
			}
		} else {
			if err := a.setupOverflowByDistance(n); err != nil {
				panic(err)
			}
		}
	}

	a.logOverflowNodes()

	return a
}

func (a *Allocator) Allocate(amount int64, nodes []ID) ([]ID, error) {
	r, _, err := a.AllocateKind(amount, nodes, KindMaskAny)
	return r.IDs(), err
}

func (a *Allocator) AllocateKind(amount int64, nodes []ID, allow KindMask) (Reservation, []ID, error) {
	if allow == 0 {
		allow = KindMaskAny
	}

	free := int64(0)
	scan := []IDSet{idset.NewIDSet()}
	flat := idset.NewIDSet()
	ocnt := 0

	// collect primary nodes and see if they can fulfill the request
	for _, id := range nodes {
		n, ok := a.nodes[id]
		if !ok {
			return nil, nil, fmt.Errorf("can't allocate memory, unavailable node #%v", id)
		}

		if !allow.Has(n.kind) || n.Available() == 0 {
			continue
		}

		free += n.Available()
		scan[0].Add(id)
		flat.Add(id)

		if ocnt < len(n.overflow) {
			ocnt = len(n.overflow)
		}
	}

	// collect overflow fallback nodes until we can fulfill the request
	if free < amount {
		for level := 0; level < ocnt; level++ {
			for _, id := range nodes {
				n := a.nodes[id]
				if level >= len(n.overflow) {
					continue
				}

				for _, oid := range n.overflow[level] {
					on := a.nodes[oid]
					if !allow.Has(on.kind) || flat.Has(oid) {
						continue
					}

					if len(scan) == 1+level {
						scan = append(scan, idset.NewIDSet())
					}

					free += on.Available()
					scan[1+level].Add(oid)
					flat.Add(oid)
				}
			}

			if free >= amount {
				break
			}
		}
	}

	if free < amount {
		return nil, nil, fmt.Errorf("can't allocate memory, only %d of %d available", free, amount)
	}

	// fill in reservation, spread allocation evenly at each level
	full := idset.NewIDSet()
	need := amount
	r := Reservation{}
NEXT:
	for i := 0; i < len(scan); i++ {
		ids := scan[i].SortedMembers()
		for {
			space := []ID{}
			chunk := need / int64(len(ids))
			extra := need % int64(len(ids))
			for _, id := range ids {
				cnt := a.nodes[id].Allocate(chunk + extra)
				r.Add(id, cnt)

				need -= cnt
				if cnt > chunk {
					extra -= cnt - chunk
				}

				if a.nodes[id].Available() == 0 {
					full.Add(id)
				} else {
					space = append(space, id)
				}
			}
			if need == 0 {
				break NEXT
			}
			if len(space) == 0 {
				continue NEXT
			}
			ids = space
		}
	}

	if need > 0 {
		panic(fmt.Errorf("internal error: failed to allocate %d memory from #%s, need %d more",
			amount, flat.String(), need))
	}

	if full.Size() > 0 {
		return r, full.Members(), nil
	}

	return r, nil, nil
}

func (a *Allocator) Release(r Reservation) error {
	for id, amount := range r {
		n, ok := a.nodes[id]
		if !ok {
			return fmt.Errorf("can't free memory (%d) from nodes #%s, unknown node #%v",
				amount, r.IDSet().String(), id)
		}
		if err := n.Release(amount); err != nil {
			return fmt.Errorf("can't free memory (%d) from nodes #%s: %w",
				amount, r.IDSet().String(), err)
		}
	}

	return nil
}

func (a *Allocator) verifyOverflow(n *Node) error {
	for level, oids := range n.overflow {
		for _, oid := range oids {
			if _, ok := a.nodes[oid]; !ok {
				return fmt.Errorf("%s node #%v, out-of-scope level %d overflow node #%v",
					n.kind, n.id, level, oid)
			}
		}
	}

	return nil
}

// Set up nodes for hanling allocations when nodes run out of capacity.
func (a *Allocator) setupOverflowByDistance(n *Node) error {
	if len(a.nodes) == 1 {
		return nil
	}

	// sort node IDs by distance from n
	closest := make([]ID, len(a.ids), len(a.ids))
	copy(closest, a.ids)
	slices.SortFunc(closest, func(a, b ID) int {
		if a == n.id {
			return -1
		}
		if b == n.id {
			return 1
		}
		return n.distance[a] - n.distance[b]
	})

	if closest[0] != n.id {
		return fmt.Errorf("internal error: %s node #%v: smallest distance not to self (#%v)",
			n.kind, n.id, closest[0])
	}

	// set up overflow nodes, treating node with equal distance as an equivalence classes
	level := 0
	prev := -1
	overflow := [][]ID{}

	for _, oid := range closest[1:len(a.ids)] {
		dist := n.distance[oid]
		if prev == -1 {
			prev = dist
			overflow = append(overflow, []ID{})
		}
		if dist != prev {
			level++
			overflow = append(overflow, []ID{})
			prev = dist
		}
		overflow[level] = append(overflow[level], oid)
	}

	n.overflow = overflow

	return nil
}

func (a *Allocator) logOverflowNodes() {
	for _, id := range a.ids {
		n := a.nodes[id]
		level := 0
		prev := -1
		distances := [][]int{}

		for _, oids := range n.overflow {
			for _, oid := range oids {
				dist := n.distance[oid]
				if dist != prev {
					if prev != -1 {
						level++
					}
					distances = append(distances, []ID{})
					prev = dist
				}
				distances[level] = append(distances[level], dist)
			}
		}

		log.Info("%s node #%v: overflow %v (%v), distance %v", n.kind, n.id, n.overflow,
			distances, n.distance)
	}
}

// Node tracks allocations from a single NUMA node.
type Node struct {
	id        ID
	kind      Kind
	capacity  int64
	allocated int64
	distance  []int
	overflow  [][]ID
}

func NewNode(id ID, kind Kind, capacity int64, distance []int, overflow ...[]ID) *Node {
	n := &Node{
		id:       id,
		kind:     kind,
		capacity: capacity,
		distance: slices.Clone(distance),
		overflow: slices.Clone(overflow),
	}
	for i, oids := range n.overflow {
		n.overflow[i] = slices.Clone(oids)
	}

	return n
}

func SystemNode(sysNode system.Node, overflow ...[]ID) *Node {
	memInfo, err := sysNode.MemoryInfo()
	if err != nil {
		panic(fmt.Errorf("failed to get info for system Node #%v: %w", sysNode.ID(), err))
	}

	n := &Node{
		id:       sysNode.ID(),
		kind:     TypeToKind(sysNode.GetMemoryType()),
		capacity: int64(memInfo.MemTotal),
		distance: slices.Clone(sysNode.Distance()),
		overflow: slices.Clone(overflow),
	}
	for i, oids := range n.overflow {
		n.overflow[i] = slices.Clone(oids)
	}

	return n
}

func (n *Node) Clone() *Node {
	if n == nil {
		return nil
	}

	c := &Node{
		id:        n.id,
		kind:      n.kind,
		capacity:  n.capacity,
		allocated: n.allocated,
		distance:  slices.Clone(n.distance),
		overflow:  slices.Clone(n.overflow),
	}

	for i, oids := range c.overflow {
		c.overflow[i] = slices.Clone(oids)
	}

	return c
}

func (n *Node) ID() ID {
	return n.id
}

func (n *Node) Kind() Kind {
	return n.kind
}

func (n *Node) Capcity() int64 {
	return n.capacity
}

func (n *Node) Allocated() int64 {
	return n.allocated
}

func (n *Node) Distance() []int {
	return slices.Clone(n.distance)
}

func (n *Node) Overflow() [][]ID {
	overflow := slices.Clone(n.overflow)
	for i, oids := range overflow {
		overflow[i] = slices.Clone(oids)
	}
	return overflow
}

func (n *Node) Available() int64 {
	return n.capacity - n.allocated
}

func (n *Node) Allocate(amount int64) int64 {
	if amount > n.Available() {
		amount = n.Available()
	}
	n.allocated += amount
	return amount
}

func (n *Node) Release(amount int64) error {
	if n.allocated < amount {
		return fmt.Errorf("can't free memory from %s node #%v, only %d allocated (< %d)",
			n.kind, n.id, n.allocated, amount)
	}
	return nil
}

// Kind describes known types of memory.
type Kind int

const (
	// Ordinary DRAM.
	KindDRAM = iota
	// Persistent memory (typically available in high capacity).
	KindPMEM
	// High-bandwidth memory (typically available in low capacity).
	KindHBM

	KindMax
)

var (
	typeToKind = map[system.MemoryType]Kind{
		system.MemoryTypeDRAM: KindDRAM,
		system.MemoryTypePMEM: KindPMEM,
		system.MemoryTypeHBM:  KindHBM,
	}
	kindToString = map[Kind]string{
		KindDRAM: "DRAM",
		KindPMEM: "PMEM",
		KindHBM:  "HB-MEM",
	}
)

func TypeToKind(t system.MemoryType) Kind {
	if k, ok := typeToKind[t]; ok {
		return k
	}
	panic(fmt.Errorf("can't provide Kind for unknown memory type %v", t))
}

func (k Kind) String() string {
	if s, ok := kindToString[k]; ok {
		return s
	}
	return fmt.Sprintf("%%!(BAD-memory.Kind:%d)", k)
}

// KindMask describes a set of memory Kinds
type KindMask int

const (
	KindMaskAny = KindMask((1 << KindMax) - 1)
)

func MaskForKinds(kinds ...Kind) KindMask {
	mask := KindMask(0)
	for _, k := range kinds {
		mask |= (1 << k)
	}
	return mask
}

func (m KindMask) KindSlice() []Kind {
	var kinds []Kind
	if m.Has(KindDRAM) {
		kinds = append(kinds, KindDRAM)
	}
	if m.Has(KindPMEM) {
		kinds = append(kinds, KindPMEM)
	}
	if m.Has(KindHBM) {
		kinds = append(kinds, KindHBM)
	}
	return kinds
}

func (m *KindMask) Set(kinds ...Kind) {
	for _, k := range kinds {
		(*m) |= (1 << k)
	}
}

func (m *KindMask) Clear(kinds ...Kind) {
	for _, k := range kinds {
		(*m) &^= (1 << k)
	}
}

func (m KindMask) Has(kinds ...Kind) bool {
	for _, k := range kinds {
		if (m & (1 << k)) == 0 {
			return false
		}
	}
	return true
}
