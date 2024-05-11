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

package libmem

import (
	"fmt"
	"math"
	"slices"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

var (
	// ErrInvalidOffer describes an offer which is not valid any more
	ErrInvalidOffer = fmt.Errorf("offer not valid")
)

// Allocator tracks memory allocations from a set of NUMA nodes.
type Allocator struct {
	nodes       map[ID]*Node
	ids         []ID
	assignments *Assignments
	allocations map[string]*Allocation
	generation  int64
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
	}

	a.logNodes()

	return a, nil
}

type Zone struct {
	nodes     NodeMask // nodes in this zone
	capacity  int64    // total capacity of nodes
	usage     int64    // total *local* usage by fully contained workloads
	workloads map[string]*Allocation
}

type Assignments struct {
	zones map[NodeMask]*Zone
}

// Get an offer for the given request.
func (a *Allocator) GetOffer(req *Request) (*Offer, error) {
	o := &Offer{
		request:  req.Clone(),
		updates:  map[string]NodeMask{},
		validity: a.generation,
	}
	o.request.Kinds.And(a.GetAvailableKinds())

	return nil, nil
}

// Commit the given offer, turning it into an allocation.
func (a *Allocator) Commit(o *Offer) ([]ID, []string, error) {
	if o != nil && o.validity != a.generation {
		return nil, nil, fmt.Errorf("%w: validity %d, allocator generation %d",
			ErrInvalidOffer, o.validity, a.generation)
	}

	return nil, nil, nil
}

// Allocate the given request.
func (a *Allocator) Allocate(req *Request) ([]ID, []string, error) {
	return []ID{0}, nil, nil

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

// GetNodeIDs returns IDs of all the nodes in the allocator.
func (a *Allocator) GetNodeIDs() []ID {
	return slices.Clone(a.ids)
}

// GetNode returns the node object with the given ID.
func (a *Allocator) GetNode(id ID) *Node {
	return a.nodes[id]
}

// GetAvailableKinds returns the mask of available memory kinds.
func (a *Allocator) GetAvailableKinds() KindMask {
	var mask KindMask
	for _, node := range a.nodes {
		mask.SetKind(node.kind)
	}
	return mask
}

// GetClosestNodes returns the closest nodes matching the given kinds.
func (a *Allocator) GetClosestNodes(from ID, kinds KindMask) ([]ID, error) {
	node, ok := a.nodes[from]
	if !ok {
		return nil, fmt.Errorf("unknon node #%v", from)
	}

	nodes := NodeMask(0)
	for _, k := range kinds.Slice() {
		var (
			filter = func(o *Node) bool { return o.Kind() == k }
		)
		for _, d := range node.distance.sorted[1:] {
			ids := a.FilterNodeIDs(node.distance.idsets[d], filter)
			if ids.Size() > 0 {
				nodes.Set(ids.Members()...)
				kinds.ClearKinds(k)
				break
			}
		}
	}

	if !kinds.IsEmpty() {
		return nil, fmt.Errorf("failed to find closest %s node to #%v", kinds, from)
	}

	return nodes.IDs(), nil
}

// GetClosestNodesForCPUs returns the set of matching nodes closest to a requested set.
func (a *Allocator) GetClosestNodesForCPUs(cpus cpuset.CPUSet, kinds KindMask) ([]ID, error) {
	from := NodeMask(0)
	for _, n := range a.nodes {
		if !n.closeCPUs.Intersection(cpus).IsEmpty() {
			from.Set(n.id)
		}
	}

	var (
		need   = kinds
		filter = func(n *Node) bool { need.ClearKind(n.Kind()); return kinds.HasKind(n.Kind()) }
		nodes  = a.FilterNodeIDs(from.IDSet(), filter)
	)

	if !need.IsEmpty() {
		n, k := a.Expand(from.IDs(), need)
		if k != need {
			return nil, fmt.Errorf("failed to find closest %s nodes", need.ClearKinds(k.Slice()...))
		}
		nodes.Add(n...)
	}

	return nodes.SortedMembers(), nil
}

// Distance returns the distance between the given two nodes.
func (a *Allocator) Distance(id1, id2 ID) int {
	n1 := a.nodes[id1]
	n2 := a.nodes[id2]
	if n1 == nil || n2 == nil {
		return math.MaxInt
	}
	return n1.distance.vector[id2]
}

// Expand the given set of nodes with the closest set of allowed kinds.
func (a *Allocator) Expand(from []ID, allow KindMask) ([]ID, KindMask) {
	// For each allowed kind, find all nodes with a minimum distance
	// between any node in the set and any node not in the set. Add
	// all such nodes to the expansion.

	nodes := NewIDSet()
	kinds := KindMask(0)
	for _, k := range allow.Slice() {
		var (
			filter  = func(o *Node) bool { return o.Kind() == k }
			distMap = map[int]IDSet{}
			minDist = math.MaxInt
		)

		for _, id := range from {
			n := a.nodes[id]
			for _, d := range n.distance.sorted[1:] {
				ids := a.FilterNodeIDs(n.distance.idsets[d], filter)
				ids.Del(from...)
				if ids.Size() == 0 || minDist < d {
					continue
				}
				if set, ok := distMap[d]; !ok {
					distMap[d] = ids
				} else {
					set.Add(ids.Members()...)
				}
				minDist = d
			}

		}

		if minDist < math.MaxInt {
			nodes.Add(distMap[minDist].Members()...)
			kinds.SetKind(k)
		}
	}

	return nodes.SortedMembers(), kinds
}

func (a *Allocator) FilterNodeIDs(ids IDSet, filter func(*Node) bool) IDSet {
	filtered := NewIDSet()
	for _, id := range ids.Members() {
		if filter(a.nodes[id]) {
			filtered.Add(id)
		}
	}
	return filtered
}

// Prepare a node to be used by an allocator.
func (a *Allocator) prepareNode(node *Node) {
	idsets := map[int]IDSet{}
	for _, id := range a.ids {
		d := node.distance.vector[id]
		if set, ok := idsets[d]; ok {
			set.Add(id)
		} else {
			idsets[d] = NewIDSet(id)
		}
	}
	node.distance.idsets = idsets
	for d := range node.distance.idsets {
		node.distance.sorted = append(node.distance.sorted, d)
	}
	slices.Sort(node.distance.sorted)
}

func (a *Allocator) logNodes() {
	log.Debug("allocator nodes:")
	for _, id := range a.ids {
		var (
			n       = a.nodes[id]
			movable = map[bool]string{false: "normal", true: "movable"}[n.movable]
		)
		log.Debug("- node #%v: %s %s", n.id, movable, n.kind)
		log.Debug("    distances: %v", n.distance.vector)
		for _, d := range n.distance.sorted[1:] {
			log.Debug("      %d: nodes %s", d, n.distance.idsets[d])
		}
	}
}
