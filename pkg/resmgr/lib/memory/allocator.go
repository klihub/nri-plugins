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
	zones       *Zones
	allocations map[string]*Allocation
	generation  int64
}

type Zones struct {
	zones   map[NodeMask]*Zone
	assign  map[string]NodeMask
	getNode func(id ID) *Node
}

type Zone struct {
	nodes     NodeMask // nodes in this zone
	capacity  int64    // total capacity of nodes
	usage     int64    // *local* usage by workloads assigned here
	workloads map[string]int64
}

// NewAllocator creates an allocator with the given options.
func NewAllocator(options ...AllocatorOption) (*Allocator, error) {
	a := &Allocator{
		nodes:       make(map[ID]*Node),
		allocations: make(map[string]*Allocation),
		zones: &Zones{
			zones:  map[NodeMask]*Zone{},
			assign: map[string]NodeMask{},
		},
	}
	a.zones.getNode = a.GetNode

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

// Get an offer for the given request.
func (a *Allocator) GetOffer(req *Request) (*Offer, error) {
	//
	// - adjust allowed node kinds
	//     - filter out unavailable kinds
	// - adjust requested node set
	//     - remove nodes with disallowed kinds
	//     - determine missing allowed kinds
	//     - expand nodes with missing kinds
	//
	// - find node set to assign workload initially to
	//     - check if node set has enough remaining capacity
	//     - expand node set if it has too little capacity, and re-check
	// - assign workload to initial node set
	//
	// - check and adjust assignments if there are overflows
	//     - from smallest to largest node set overlapping with node set of new assignment
	//       - check if allocations directly assigned to node set and strict node subsets overflow
	//

	log.Debug("*** #%s: %s %v nodes, %d bytes", req.Workload, req.Kinds, req.Nodes, req.Amount)

	o := &Offer{
		request:  req.Clone(),
		updates:  map[string]NodeMask{},
		validity: a.generation,
	}
	o.request.Kinds = a.removeAbsentKinds(o.request.Kinds)
	missingKinds := o.request.Kinds
	a.foreachNodeInMask(o.request.Nodes, func(n *Node) bool {
		missingKinds &^= (1 << n.kind)
		return true
	})

	var (
		nodes NodeMask
		kinds KindMask
		free  int64
	)

	if missingKinds != 0 {
		n, _ := a.expand(o.request.Nodes, missingKinds)
		log.Info("missing kinds in %v: %s", o.request.Nodes, missingKinds)
		log.Info("expanded nodes %v with %s %v", o.request.Nodes, kinds, nodes)
		nodes = o.request.Nodes | n
	} else {
		nodes = o.request.Nodes
	}

	for {
		c := a.zones.capacity(nodes)
		u := a.zones.usage(nodes)
		free = c - u
		free = a.zones.free(nodes)
		log.Debug("* zones %s, capacity %d, usage %d => free: %d", nodes, c, u, free)
		if free < o.request.Amount {
			log.Info("free memory %d < requested %d, expanding node...", free, o.request.Amount)
			n, k := a.expand(nodes, o.request.Kinds)
			if n == 0 {
				break
			}
			nodes |= n
			log.Info("expanded with new %s nodes %v to %s", k, n, nodes)
		} else {
			log.Info("%s has enough free capacity (%d >= %d)", nodes, free, o.request.Amount)
			break
		}
	}

	if free < o.request.Amount {
		log.Errorf("not enough free memory (%d < %d)", free, o.request.Amount)
		return nil, fmt.Errorf("not enough free memory (%d < %d)", free, o.request.Amount)
	}

	log.Info("using initial nodes %s", nodes)

	//zones := a.zones.Clone()

	a.zones.add(nodes, o.request)

	_ = a.zones.getUsage(nodes)
	_ = a.zones.checkOverflow(nodes)

	//a.zones = zones

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
				nodes = nodes.Set(ids.IDs()...)
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
			from = from.Set(n.id)
		}
	}

	var (
		need   = kinds
		filter = func(n *Node) bool { need.ClearKind(n.Kind()); return kinds.HasKind(n.Kind()) }
		nodes  = a.FilterNodeIDs(from, filter)
	)

	if !need.IsEmpty() {
		n, k := a.Expand(from.IDs(), need)
		if k != need {
			return nil, fmt.Errorf("failed to find closest %s nodes", need.ClearKinds(k.Slice()...))
		}
		nodes = nodes.Set(n...)
	}

	return nodes.IDs(), nil
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
	n, k := a.expand(NodeMaskForIDs(from...), allow)
	return n.IDs(), k
}

// Expand the given set of nodes with the closest set of allowed kinds.
func (a *Allocator) expand(from NodeMask, allow KindMask) (NodeMask, KindMask) {
	// For each allowed kind, find all nodes with a minimum distance
	// between any node in the set and any node not in the set. Add
	// all such nodes to the expansion.

	log.Debug("=> expand(%s, %s)", from, allow)

	nodes := NodeMask(0)
	kinds := KindMask(0)
	for _, k := range allow.Slice() {
		var (
			filter  = func(o *Node) bool { return o.Kind() == k }
			distMap = map[int]NodeMask{}
			minDist = math.MaxInt
		)

		for _, id := range from.IDs() {
			n := a.nodes[id]
			for _, d := range n.distance.sorted[1:] {
				//log.Debug("- considering nodes at distance %d...", d)
				ids := a.FilterNodeIDs(n.distance.idsets[d], filter)
				//log.Debug("    nodes %s", ids)
				ids = ids &^ from
				if ids.Size() == 0 || minDist < d {
					continue
				}
				distMap[d] |= ids
				minDist = d
			}

		}

		if minDist < math.MaxInt {
			nodes = nodes.Set(distMap[minDist].IDs()...)
			kinds.SetKind(k)
		}
	}

	return nodes, kinds
}

func (a *Allocator) FilterNodeIDs(ids NodeMask, filter func(*Node) bool) NodeMask {
	filtered := NodeMask(0)
	for _, id := range ids.IDs() {
		if filter(a.nodes[id]) {
			filtered = filtered.Set(id)
		}
	}
	return filtered
}

// Prepare a node to be used by an allocator.
func (a *Allocator) prepareNode(node *Node) {
	log.Debug("=> preparing node #%d...", node.id)
	idsets := map[int]NodeMask{}
	for _, id := range a.ids {
		d := node.distance.vector[id]
		log.Debug("  - node #%d at %d distance", id, d)
		//set := idsets[d]
		//idsets[d] = *(set.Set(id))
		idsets[d] |= (1 << id)
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

	log.Debug("distance matrix:")
	for _, id := range a.ids {
		log.Debug("  %d: %v", id, a.nodes[id].distance.vector)
	}
}
