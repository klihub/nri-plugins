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
	"math"
	"slices"
	"strings"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/sysfs"
	system "github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	idset "github.com/intel/goresctrl/pkg/utils"
)

type (
	ID    = idset.ID
	IDSet = idset.IDSet
)

var (
	NewIDSet = idset.NewIDSet
	log      = logger.Get("libmem")
)

// Request is a request to allocate some memory to a workload.
type Request struct {
	Workload string   // ID of workload this offer belongs to
	Amount   int64    // amount of memory requested
	Kind     KindMask // kind of memory requested
	Nodes    []ID     // nodes to start allocating memory from
}

// Offer represents potential allocation of some memory to a workload.
//
// An offer contains all the changes, including potential changes to
// other workloads, which are necessary to fulfill the allocation.
// An offer can be committed into an allocation provided that no new
// allocation has taken place since the offer was created. Offers can
// be used to compare various allocation alternatives for a workload.
type Offer struct {
	request  *Request         // allocation request
	nodes    IDSet            // allocated nodes
	updates  map[string]IDSet // updated workloads to fulfill the request
	validity int64            // validity of this offer (wrt. Allocator.generation)
}

// Allocation represents some memory allocated to a workload.
type Allocation struct {
	request *Request // allocation request
	nodes   IDSet    // nodes allocated to fulfill this request
}

// Allocator tracks memory allocations from a set of NUMA nodes.
type Allocator struct {
	nodes       map[ID]*Node
	ids         []ID
	allocations map[string]*Allocation
	generation  int64
}

// Node represents allocatable memory in a NUMA node.
type Node struct {
	id         ID
	kind       Kind
	capacity   int64
	movable    bool
	distance   []int
	closeCPUs  cpuset.CPUSet
	byDistance map[Kind][][]ID
	order      []IDSet
	fallback   [][]ID
}

// AllocatorOption is an option for an allocator.
type AllocatorOption func(*Allocator) error

// WithNodes is an option to set the nodes an allocator can use.
func WithNodes(nodes []*Node) AllocatorOption {
	return func(a *Allocator) error {
		if len(a.nodes) != 0 {
			return fmt.Errorf("failed to set allocator nodes, already set")
		}

		nodeCnt := len(nodes)
		for _, n := range nodes {
			if _, ok := a.nodes[n.id]; ok {
				return fmt.Errorf("failed to set allocator nodes, duplicate node %v", n.id)
			}

			if distCnt := len(n.distance); distCnt < nodeCnt {
				return fmt.Errorf("%d distances set for node #%v, >= %d expected",
					distCnt, n.id, nodeCnt)
			}

			a.nodes[n.id] = n
			a.ids = append(a.ids, n.id)
		}

		return nil
	}
}

// WithSystemNodes is an option to let the allocator discover and pick nodes.
func WithSystemNodes(sys sysfs.System, pickers ...func(sysfs.Node) bool) AllocatorOption {
	return func(a *Allocator) error {
		if len(a.nodes) != 0 {
			return fmt.Errorf("failed to set allocator nodes, already set")
		}

		for _, id := range sys.NodeIDs() {
			sysNode := sys.Node(id)

			picked := len(pickers) == 0
			for _, p := range pickers {
				if p(sysNode) {
					picked = true
					break
				}
			}
			if !picked {
				log.Info("node #%v was not picked, ignoring it...", id)
				continue
			}

			memInfo, err := sysNode.MemoryInfo()
			if err != nil {
				return fmt.Errorf("failed to discover system node #%v: %v", id, err)
			}

			n := &Node{
				id:        sysNode.ID(),
				kind:      TypeToKind(sysNode.GetMemoryType()),
				capacity:  int64(memInfo.MemTotal),
				movable:   !sysNode.HasNormalMemory(),
				distance:  slices.Clone(sysNode.Distance()),
				closeCPUs: sysNode.CPUSet().Clone(),
			}

			a.nodes[id] = n
			a.ids = append(a.ids, id)

			log.Info("discovered and picked %s node #%v with %d capacity, close CPUs %s",
				n.kind, n.id, n.capacity, n.closeCPUs.String())
		}

		return nil
	}
}

// WithFallbackNodes is an option to manually set up/override fallback nodes for the allocator.
func WithFallbackNodes(fallback map[ID][][]ID) AllocatorOption {
	return func(a *Allocator) error {
		if len(fallback) < len(a.nodes) {
			return fmt.Errorf("failed to set fallback nodes, expected %d entries, got %d",
				len(a.nodes), len(fallback))
		}

		for id, nodeFallback := range fallback {
			n, ok := a.nodes[id]
			if !ok {
				return fmt.Errorf("failed to set fallback nodes, node #%v not found", id)
			}

			n.fallback = slices.Clone(nodeFallback)
			for i, fbids := range nodeFallback {
				n.fallback[i] = slices.Clone(fbids)
			}
		}
		return nil
	}
}

// NewAllocator creates an allocator with the given options.
func NewAllocator(options ...AllocatorOption) (*Allocator, error) {
	a := &Allocator{
		nodes:       make(map[ID]*Node),
		allocations: make(map[string]*Allocation),
	}

	for _, o := range options {
		if err := o(a); err != nil {
			return nil, err
		}
	}

	slices.SortFunc(a.ids, func(a, b ID) int { return a - b })

	for _, n := range a.nodes {
		a.prepareNode(n)
		if n.fallback != nil {
			if err := a.verifyFallback(n); err != nil {
				return nil, fmt.Errorf("fallback node verification failed: %w", err)
			}
		} else {
			if err := a.setupFallbackByDistance(n); err != nil {
				return nil, fmt.Errorf("failed to set up fallbacks by distance: %w", err)
			}
		}
	}

	a.logNodes()

	return a, nil
}

// Get an offer for the given request.
func (a *Allocator) GetOffer(req *Request) (*Offer, error) {

	return nil, nil
}

// Commit the given offer, turning it into an allocation.
func (a *Allocator) Commit(o *Offer) ([]ID, []string, error) {
	return nil, nil, nil
}

// Allocate the given request.
func (a *Allocator) Allocate(req *Request) ([]ID, []string, error) {
	o, err := a.GetOffer(req)
	if err != nil {
		return nil, nil, err
	}

	return a.Commit(o)
}

// Release the allocation of the given workload.
func (a *Allocator) Release(workload string) error {
	return nil
}

func (a *Allocator) NodeDistance(id1, id2 ID) int {
	n1 := a.nodes[id1]
	n2 := a.nodes[id2]
	if n1 == nil || n2 == nil {
		return math.MaxInt
	}
	return n1.distance[id2]
}

// GetClosestNodes returns the closest nodes matching the given kinds.
func (a *Allocator) GetClosestNodes(nodeID ID, kinds KindMask) ([]ID, error) {
	node, ok := a.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("unknown node #%v", nodeID)
	}

	ids := NewIDSet()
	for _, k := range kinds.Slice() {
		closest := node.byDistance[k]
		if len(closest) == 0 {
			return nil, fmt.Errorf("no available %s nodes", k)
		}
		ids.Add(closest[0]...)
	}

	return ids.SortedMembers(), nil

	/*
	   ids := a.PickAndSortNodes(

	   	KindMatcher(kinds),
	   	DistanceSorter(node),

	   )

	   closest := []ID{}
	   min := math.MaxInt

	   	for _, id := range ids {
	   		if id == nodeID {
	   			continue
	   		}

	   		if !kinds.Has(a.nodes[id].kind) {
	   			break
	   		}

	   		d := node.distance[id]
	   		if d > min {
	   			break
	   		}

	   		min = d
	   		closest = append(closest, id)
	   	}

	   return closest, nil
	*/
}

// GetClosestNodesForCPUs returns the set of matching nodes closest to a requested set.
func (a *Allocator) GetClosestNodesForCPUs(cpus cpuset.CPUSet, kinds KindMask) []ID {
	ids := NewIDSet()
	for _, n := range a.nodes {
		if cpus.Intersection(n.closeCPUs).IsEmpty() {
			continue
		}
		if kinds.Has(n.kind) {
			ids.Add(n.id)
		} else {
			closest, err := a.GetClosestNodes(n.id, kinds)
			if err == nil {
				ids.Add(closest...)
			}
		}
	}
	return ids.SortedMembers()
}

func (a *Allocator) PickNodes(predicate func(n *Node) bool) []ID {
	var ids []ID
	for _, id := range a.ids {
		if predicate(a.nodes[id]) {
			ids = append(ids, id)
		}
	}
	return ids
}

type NodePicker func(*Node) bool
type NodeSorter func(ID, ID) int

func (a *Allocator) PickAndSortNodes(picker NodePicker, sorter NodeSorter) []ID {
	var ids []ID

	for _, id := range a.ids {
		if picker(a.nodes[id]) {
			ids = append(ids, id)
		}
	}

	if sorter != nil {
		slices.SortFunc(ids, sorter)
	}

	return ids
}

func KindMatcher(kinds KindMask) func(n *Node) bool {
	return func(n *Node) bool {
		return kinds.Has(n.kind)
	}
}

func DistanceSorter(n *Node) func(ID, ID) int {
	return func(i, j ID) int {
		if diff := n.distance[i] - n.distance[j]; diff != 0 {
			return diff
		} else {
			return i - j
		}
	}
}

func (a *Allocator) prepareNode(node *Node) {
	idsByDist := slices.Clone(a.ids)
	slices.SortFunc(idsByDist, func(id1, id2 ID) int {
		if diff := node.distance[id1] - node.distance[id2]; diff != 0 {
			return diff
		} else {
			return id1 - id2
		}
	})

	byDistance := map[Kind][][]ID{}
	for _, id := range idsByDist {
		if id == node.id {
			continue
		}

		n := a.nodes[id]
		ids := byDistance[n.kind]
		if ids == nil {
			ids = [][]ID{{id}}
		} else {
			d := node.distance[ids[len(ids)-1][0]]
			if d == node.distance[id] {
				ids[len(ids)-1] = append(ids[len(ids)-1], id)
			} else {
				ids = append(ids, []ID{id})
			}
		}
		byDistance[n.kind] = ids
	}
	node.byDistance = byDistance
}

// Verify user provided fallback nodes.
func (a *Allocator) verifyFallback(n *Node) error {
	for level, fbids := range n.fallback {
		for _, fbid := range fbids {
			if _, ok := a.nodes[fbid]; !ok {
				return fmt.Errorf("%s node #%v, out-of-scope level %d fallback node #%v",
					n.kind, n.id, level, fbid)
			}
		}
	}

	return nil
}

// Set up nodes for handling allocations when nodes run out of capacity.
func (a *Allocator) setupFallbackByDistance(n *Node) error {
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

	// set up fallback nodes, treating all nodes with equal distance equally good
	level := 0
	prev := -1
	fallback := [][]ID{}

	for _, fbid := range closest[1:len(a.ids)] {
		dist := n.distance[fbid]
		if prev == -1 {
			prev = dist
			fallback = append(fallback, []ID{})
		}
		if dist != prev {
			level++
			fallback = append(fallback, []ID{})
			prev = dist
		}
		fallback[level] = append(fallback[level], fbid)
	}

	n.fallback = append([][]ID{{n.id}}, fallback...)
	for i, fbids := range n.fallback {
		n.order = append(n.order, NewIDSet(fbids...))
		if i > 0 {
			n.order[i].Add(n.order[i-1].Members()...)
		}
	}

	return nil
}

func (a *Allocator) logNodes() {
	for _, id := range a.ids {
		n := a.nodes[id]
		level := 0
		prev := -1
		distances := [][]int{}

		for _, fbids := range n.fallback {
			for _, fbid := range fbids {
				dist := n.distance[fbid]
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

		log.Info("%s node #%v: fallback %v (%v), distance %v, order %v", n.kind, n.id, n.fallback,
			distances, n.distance, n.order)
	}

	for _, id := range a.ids {
		n := a.nodes[id]
		for k := KindDRAM; k < KindMax; k++ {
			log.Info("%s node #%v: %s nodes by distance: %v", n.kind, n.id, k, n.byDistance[k])
		}
	}

}

func NewNode(id ID, kind Kind, capacity int64, movable bool, dist []int, closeCPUs cpuset.CPUSet, fallback [][]ID) *Node {
	n := &Node{
		id:        id,
		kind:      kind,
		capacity:  capacity,
		distance:  slices.Clone(dist),
		closeCPUs: closeCPUs.Clone(),
		fallback:  slices.Clone(fallback),
	}
	for i, fbids := range n.fallback {
		n.fallback[i] = slices.Clone(fbids)
	}

	return n
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

func (n *Node) CloseCPUs() cpuset.CPUSet {
	return n.closeCPUs
}

func (n *Node) IsMovable() bool {
	return n.movable
}

func (n *Node) Distance() []int {
	return slices.Clone(n.distance)
}

// Kind describes known types of memory.
type Kind int

const (
	// Ordinary DRAM.
	KindDRAM Kind = iota
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

func MaskForTypes(types ...system.MemoryType) KindMask {
	mask := KindMask(0)
	for _, t := range types {
		mask |= (1 << TypeToKind(t))
	}
	return mask
}

func (m KindMask) Slice() []Kind {
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

func (m KindMask) String() string {
	kinds := []string{}
	for k := KindDRAM; k < KindMax; k++ {
		if m.Has(k) {
			kinds = append(kinds, k.String())
		}
	}
	return "{" + strings.Join(kinds, ",") + "}"
}
