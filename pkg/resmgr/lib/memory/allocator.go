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

// Allocator tracks memory allocations from a set of NUMA nodes.
type Allocator struct {
	nodes       map[ID]*Node
	ids         []ID
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
		mask.Set(node.kind)
	}
	return mask
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
}

// GetClosestNodesForCPUs returns the set of matching nodes closest to a requested set.
func (a *Allocator) GetClosestNodesForCPUs(cpus cpuset.CPUSet, kinds KindMask) ([]ID, error) {
	if cpus.IsEmpty() || kinds.IsEmpty() {
		return []ID{}, nil
	}

	ids := NewIDSet()
	for _, n := range a.nodes {
		if cpus.Intersection(n.closeCPUs).IsEmpty() {
			continue
		}
		if kinds.Has(n.kind) {
			ids.Add(n.id)
			closest, err := a.GetClosestNodes(n.id, *(kinds.Clone().Clear(n.kind)))
			if err != nil {
				return nil, fmt.Errorf("can't find %s nodes for CPUs %s: %w", kinds, cpus, err)
			}
			ids.Add(closest...)
		} else {
			closest, err := a.GetClosestNodes(n.id, kinds)
			if err != nil {
				return nil, fmt.Errorf("can't find %s nodes for CPUs %s: %w", kinds, cpus, err)
			}
			ids.Add(closest...)
		}
	}
	return ids.SortedMembers(), nil
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

// Expand the given set of nodes with the closest new set of nodes of given kinds.
func (a *Allocator) Expand(from []ID, kinds KindMask) ([]ID, error) {
	// Notes:
	// This might be too greedily expanding nodes now. It tries to find one expansion
	// node of each kind for each node already present in the set. IOW,
	//
	//   - for each requested node kind
	//     - for each node already present in the set
	//       - find node set with shortest distance and one or more node not in the set
	//       - add that set to the expansion
	//
	// This seems to work reasonably when the starting node set has been set up
	// with miminal distances like in the topology-aware policy.
	//
	// I think ideally what we'd like to have is
	//   - for each requested node kind
	//     - find the minimum distance for which we can add a new node
	//     - for each node present in the original set
	//       - find all new nodes of the same kind and distance
	//       - add them to the expansion
	//
	// I am not sure if these would always produce the same result even for
	// topology-aware like node sets...
	//

	expand := NewIDSet()
	for _, k := range kinds.Slice() {
		for _, id := range from {
			n := a.nodes[id]
			rightKind := func(o *Node) bool { return o.Kind() == k }
			for _, d := range n.distance.sorted[1:] {
				ids := a.FilterNodeIDs(n.distance.idsets[d], rightKind)
				ids.Del(from...)
				if ids.Size() > 0 {
					expand.Add(ids.Members()...)
					break
				}
			}
		}
	}

	if expand.Size() == 0 {
		return []ID{}, fmt.Errorf("failed to expand nodes %v with %v nodes", from, kinds)
	}

	return expand.SortedMembers(), nil
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
		d := node.Distance()[id]
		log.Info("*** node %v has neighbor %v at distance %v", node.id, id, d)
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

	idsByDist := slices.Clone(a.ids)
	slices.SortFunc(idsByDist, func(id1, id2 ID) int {
		if diff := node.distance.vector[id1] - node.distance.vector[id2]; diff != 0 {
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
			d := node.distance.vector[ids[len(ids)-1][0]]
			if d == node.distance.vector[id] {
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
		return n.distance.vector[a] - n.distance.vector[b]
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
		dist := n.distance.vector[fbid]
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
				dist := n.distance.vector[fbid]
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
			distances, n.distance.vector, n.order)

		for idx, d := range n.distance.sorted {
			set, ok := n.distance.idsets[d]
			if !ok {
				log.Error("***** no idsets for distance %v!!!", d)
				continue
			}
			log.Info("*** %s node #%v: nodes set %v at distance %v", n.kind, n.id, set, d)
			if idx == 0 {
				if set.Size() != 1 || set.Members()[0] != id {
					log.Error("***** %d node #%v is not it's own closest neighbor!!!",
						n.kind, n.id)
				}
			}
		}

	}

	for _, id := range a.ids {
		n := a.nodes[id]
		for k := KindDRAM; k < KindMax; k++ {
			log.Info("%s node #%v: %s nodes by distance: %v", n.kind, n.id, k, n.byDistance[k])
		}
	}

}
