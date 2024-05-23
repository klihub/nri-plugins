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
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/containers/nri-plugins/pkg/sysfs"
	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	idset "github.com/intel/goresctrl/pkg/utils"
)

// Allocator implements policy agnostic memory accounting and allocation.
type Allocator struct {
	nodes    map[ID]*Node
	zones    map[NodeMask]*Zone
	requests map[string]*Request
	users    map[string]NodeMask
	masks    *MaskCache
	version  int64
	record   *recording
	custom   CustomFunctions
}

// Offer is a possible memory allocation for a request. A valid offer can be
// committed which turns it into an actual allocation. An offer is invalidated
// by memory allocations (including offer commits) and releases.
type Offer struct {
	a       *Allocator
	version int64
	req     *Request
	updates map[string]NodeMask
}

type (
	// ID is the unique integer ID for a memory node, the NUMA node ID.
	ID = idset.ID
)

const (
	// ForeachDone as a return value terminates iteration by a Foreach* function.
	ForeachDone = false
	// ForeachMore as a return value continues iteration by a Foreach* function.
	ForeachMore = !ForeachDone
)

// AllocatorOption is an opaque option for an Allocator.
type AllocatorOption func(*Allocator) error

// WithSystemNodes returns an option to assign memory to an allocator.
// It uses the given sysfs instance to discover memory nodes.
func WithSystemNodes(sys sysfs.System) AllocatorOption {
	return func(a *Allocator) error {
		nodes := []*Node{}

		for _, id := range sys.NodeIDs() {
			sysNode := sys.Node(id)
			info, err := sysNode.MemoryInfo()
			if err != nil {
				return fmt.Errorf("failed to discover system node #%d: %w", id, err)
			}

			var (
				memType   = TypeForSysfs(sysNode.GetMemoryType())
				capacity  = int64(info.MemTotal)
				isNormal  = sysNode.HasNormalMemory()
				closeCPUs = sysNode.CPUSet()
				distance  = sysNode.Distance()
			)

			n, err := NewNode(id, memType, capacity, isNormal, closeCPUs, distance)
			if err != nil {
				return fmt.Errorf("failed to create node #%d: %w", id, err)
			}

			nodes = append(nodes, n)
		}

		return WithNodes(nodes)(a)
	}
}

// WithNodes returns an option to assign the given memory to an allocator.
func WithNodes(nodes []*Node) AllocatorOption {
	return func(a *Allocator) error {
		if len(a.nodes) > 0 {
			return fmt.Errorf("allocator already has nodes set")
		}

		for _, n := range nodes {
			if _, ok := a.nodes[n.id]; ok {
				return fmt.Errorf("allocator already has node #%d", n.id)
			}
			a.nodes[n.id] = n
		}

		return nil
	}
}

// NewAllocator returns a new allocator instance for the given options.
func NewAllocator(options ...AllocatorOption) (*Allocator, error) {
	return newAllocator(options...)
}

// CPUSetAffinity returns the mask of closest nodes for the given cpuset.
func (a *Allocator) CPUSetAffinity(cpus cpuset.CPUSet) NodeMask {
	nodes := NodeMask(0)
	a.ForeachNode(a.masks.nodes.all, func(n *Node) bool {
		if !cpus.Intersection(n.cpus).IsEmpty() {
			nodes |= n.Mask()
		}
		return ForeachMore
	})
	return nodes
}

// GetOffer returns an offer for an allocation of the given request. This
// can be turned turned into an actual allocation by committing it, if it
// has not expired. An offer expires if any memory is allocated (directly
// or by comitting another offer) or released.
func (a *Allocator) GetOffer(req *Request) (*Offer, error) {
	log.Debug("get offer for %s", req)
	defer a.checkState("post-GetOffer()")
	return a.getOffer(req)
}

// Allocate allocates memory for the given request. It is equivalent to
// the commit of an offer acquired by GetOffer(). Allocate returns the
// nodes used to satisfy the request, together with updates necessary to
// any other existing allocations which had to be altered to fulfill the
// request. The caller is responsible for making sure these updates are
// properly enforced.
func (a *Allocator) Allocate(req *Request) (NodeMask, map[string]NodeMask, error) {
	log.Debug("allocate %s memory close to %s for %s", req.types, req.affinity, req)
	defer a.checkState("post-Allocate()")
	return a.allocate(req)
}

// Realloc updates an existing allocation with the given extra affinity
// and memory types. Therefore Realloc is semantically always expansive.
// You cannot remove memory nodes or types from an allocation by Realloc.
// Realloc returns the updated nodes for the allocation, together with
// any updates necessary to other existing allocations for fulfilling the
// request. The caller is responsible to make sure all these updates are
// properly enforced.
func (a *Allocator) Realloc(id string, affinity NodeMask, types TypeMask) (NodeMask, map[string]NodeMask, error) {
	req, ok := a.requests[id]
	if !ok {
		return 0, nil, fmt.Errorf("%w: no request with ID %s", ErrUnknownRequest, id)
	}

	if req.Affinity() == affinity && req.Types() == types {
		return req.Zone(), nil, nil
	}

	affinity &= a.masks.nodes.all
	types &= a.masks.types

	log.Debug("reallocate %s memory close to %s for %s", types, affinity, req)

	if (req.zone&affinity) == affinity && (a.ZoneType(req.zone)&types) == types {
		return req.zone, nil, nil
	}

	defer a.checkState("post-Realloc()")

	return a.realloc(req, affinity&a.masks.nodes.all, types&a.masks.types)
}

// Release releases the allocation with the given ID.
func (a *Allocator) Release(id string) error {
	req, ok := a.requests[id]
	if !ok {
		return fmt.Errorf("%w: no request with ID %s", ErrUnknownRequest, id)
	}

	defer a.checkState("post-Release()")

	log.Debug("release memory for %s", req)
	return a.release(req)
}

// Reset resets the state of the allocator. It effectively releases all
// existing allocations and invalidates all uncommitted offers.
func (a *Allocator) Reset() {
	log.Debug("reset all allocations")
	a.reset()
}

// AssignedZone returns the assigned nodes for the given allocation and
// whether such an allocation was found.
func (a *Allocator) AssignedZone(id string) (NodeMask, bool) {
	if zone, ok := a.users[id]; ok {
		return zone, true
	}
	return 0, false
}

// Masks returns the cache of node and type masks for the allocator.
func (a *Allocator) Masks() *MaskCache {
	return a.masks
}

// ForeachNode calls the given function with each node present in the mask.
// It stops iterating through nodes early if the called function returns
// false, or ForeachDone. Iteration continues if the returned value is true,
// or ForeachMore.
func (a *Allocator) ForeachNode(nodes NodeMask, fn func(n *Node) bool) {
	(nodes & a.masks.nodes.all).Foreach(func(id ID) bool {
		return fn(a.nodes[id])
	})
}

func newAllocator(options ...AllocatorOption) (*Allocator, error) {
	a := &Allocator{
		nodes: make(map[ID]*Node),
		masks: NewMaskCache(),
	}

	a.reset()

	for _, o := range options {
		if err := o(a); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrFailedOption, err)
		}
	}

	for id, n := range a.nodes {
		if len(n.distance.vector) != len(a.nodes) {
			return nil, fmt.Errorf("%w: node #%d has %d distances for %d nodes", ErrInvalidNode,
				id, len(n.distance.vector), len(a.nodes))
		}
		a.masks.addNode(n)
	}

	a.DumpConfig()

	return a, nil
}

func (a *Allocator) checkState(where string) {
	for _, req := range a.requests {
		if assigned, ok := a.users[req.ID()]; ok {
			if assigned != req.Zone() {
				log.Error("internal error: %s: %s assigned to %s, but has zone set to %s",
					where, req, assigned, req.Zone())
			}
		} else {
			log.Error("internal eror: %s: %s not assigned, but has zone set to %s",
				where, req, req.Zone())
		}
	}

	for zone, z := range a.zones {
		for id := range z.users {
			req, ok := a.requests[id]
			if !ok {
				log.Error("internal error: %s: %s present in zone %s, but has no assignment",
					where, req, zone)
				continue
			}
			if req.Zone() != zone {
				log.Error("internal error %s: %s assigned to %s, also present in zone %s",
					where, req, req.Zone(), zone)
			}
		}
	}
}

func (a *Allocator) getOffer(req *Request) (*Offer, error) {
	a.DumpState()

	if err := a.validateRequest(req); err != nil {
		return nil, err
	}

	if err := a.findInitialZone(req); err != nil {
		return nil, err
	}

	if err := a.ensureNormalMemory(req); err != nil {
		return nil, err
	}

	if err := a.recordChanges(); err != nil {
		return nil, err
	}

	defer func() {
		a.revertRecord(req)
	}()

	a.requests[req.ID()] = req
	a.zoneAssign(req.zone, req)

	if err := a.resolveOverflow(req.zone); err != nil {
		return nil, err
	}

	updates, err := a.revertRecord(req)
	if err != nil {
		return nil, err
	}

	return a.newOffer(req, updates), nil
}

func (a *Allocator) allocate(req *Request) (NodeMask, map[string]NodeMask, error) {
	// TODO(klihub): reorganize getoffer and allocate to minimize wasted work.
	// IOW so that getoffer becomes the saved state of a reverted allocation
	// while allocation becomes just that, an unreverted allocation instead of
	// the current reapplication of an allocation reverted just a moment ago.

	o, err := a.getOffer(req)
	if err != nil {
		return 0, nil, err
	}
	return o.Commit()
}

func (a *Allocator) realloc(req *Request, nodes NodeMask, types TypeMask) (NodeMask, map[string]NodeMask, error) {
	if nodes == 0 && types == 0 {
		return req.zone, nil, nil
	}

	if types == 0 {
		// if only nodes are given, use type mask of those nodes
		types = a.ZoneType(nodes)
	} else {
		// if both nodes and types are given, mask out nodes of other types
		nodes &= a.masks.nodes.byTypes[types]
	}

	if err := a.recordChanges(); err != nil {
		return 0, nil, err
	}

	defer func() {
		a.revertRecord(nil)
	}()

	newNodes, newTypes := a.expand(req.zone|nodes, types)
	if newNodes == 0 {
		return 0, nil, fmt.Errorf("%w: failed to reallocate, can't find new %s nodes",
			ErrNoMem, types)
	}

	a.zoneMove(req.zone|nodes|newNodes, req)

	if err := a.resolveOverflow(req.zone | newNodes); err != nil {
		req.zone = a.users[req.ID()]
		return 0, nil, fmt.Errorf("%w: failed to reallocate: %w", ErrNoMem, err)
	}

	req.zone |= nodes | newNodes
	req.types |= newTypes

	return req.zone, a.commitRecord(), nil
}

func (a *Allocator) release(req *Request) error {
	zone, ok := a.users[req.ID()]
	if !ok {
		return fmt.Errorf("%w: no assigned zone for %s", ErrNoZone, req)
	}

	a.zoneRemove(zone, req.ID())
	delete(a.requests, req.ID())
	a.invalidateOffers()

	return nil
}

func (a *Allocator) reset() {
	a.zones = make(map[NodeMask]*Zone)
	a.users = make(map[string]NodeMask)
	a.requests = make(map[string]*Request)
	a.invalidateOffers()
}

func (a *Allocator) invalidateOffers() {
	a.version++
}

func (a *Allocator) validateRequest(req *Request) error {
	if _, ok := a.requests[req.ID()]; ok {
		return ErrAlreadyExists
	}

	if (req.affinity & a.masks.nodes.all) != req.affinity {
		unknown := req.affinity &^ a.masks.nodes.all
		return fmt.Errorf("%w: unknown nodes requested (%s)", ErrInvalidNode, unknown)
	}

	if (req.types&a.masks.types) != req.types && req.IsStrict() {
		unavailable := req.types &^ a.masks.types
		return fmt.Errorf("%w: unavailable types requested (%s)", ErrInvalidType, unavailable)
	}

	if req.affinity == 0 {
		return fmt.Errorf("%w: request without affinity", ErrNoMem)
	}

	req.types &= a.masks.types
	if req.types == 0 {
		req.types = a.zoneType(req.affinity)
	}

	return nil
}

func (a *Allocator) findInitialZone(req *Request) error {
	//
	// Find an initial zone for the request.
	//
	// The initial zone is the request affinity expanded to contain nodes
	// for all the preferred types. For strict requests this is a mandatory
	// requirement.
	//
	// Note that we only mask out non-preferred types from the expanded
	// initial zone at the end. This allows expressing a preference like
	// 'I want only HBM memory close to node #0' by simply setting affinity
	// to NewNodeMask(0) and type to TypeMaskHBM, even if node #0 itself is
	// of some other type than HBM.
	//

	req.zone = req.affinity & a.masks.nodes.all
	missing := req.types &^ a.zoneType(req.zone)

	log.Debug("- finding initial zone (starting at %s, expanding for %s)", req.zone, missing)

	if missing != 0 {
		nodes, types := a.expand(req.zone, missing)
		if types != missing && req.IsStrict() {
			return fmt.Errorf("failed to find initial nodes of type %s", missing&^types)
		}

		req.zone |= nodes
	}

	if req.IsStrict() {
		req.zone &= a.masks.nodes.byTypes[req.types]
		missing = req.types &^ a.zoneType(req.zone)
		if missing != 0 {
			return fmt.Errorf("failed find initial nodes of type %s", missing)
		}
	} else {
		if req.zone&a.masks.nodes.byTypes[req.types] != 0 {
			req.zone &= a.masks.nodes.byTypes[req.types]
		}
	}

	return nil
}

func (a *Allocator) ensureNormalMemory(req *Request) error {
	//
	// Make sure that request has some initial normal memory.
	//
	// We assume that we always have some normal DRAM present and therefore
	// only force DRAM into the allowed types of non-strict requests.
	//

	if (req.zone & a.masks.nodes.normal) != 0 {
		return nil
	}

	zone := req.zone
	types := req.types & a.zoneType(a.masks.nodes.normal)
	if types == 0 && !req.IsStrict() { // TODO(klihub): should we force this for strict, too ?
		types |= TypeMaskDRAM
	}

	log.Debug("- ensuring normal memory for %s", zone)

	for {
		newNodes, newTypes := a.expand(zone, types)
		if newNodes == 0 {
			return fmt.Errorf("failed to find normal memory (of any type %s)", types)
		}

		zone |= newNodes
		types |= newTypes

		if (zone & a.masks.nodes.normal) != 0 {
			req.zone = zone
			req.types = types

			return nil
		}
	}
}

func (a *Allocator) newOffer(req *Request, updates map[string]NodeMask) *Offer {
	return &Offer{
		a:       a,
		req:     req,
		updates: updates,
		version: a.version,
	}
}

func (a *Allocator) expand(zone NodeMask, types TypeMask) (NodeMask, TypeMask) {
	var nodes NodeMask
	if a.custom.ExpandZone != nil {
		nodes = a.custom.ExpandZone(zone, types, &customAllocator{a}) &^ zone
		types = a.zoneType(nodes)
	} else {
		nodes, types = a.defaultExpand(zone, types)
	}

	if nodes != 0 {
		log.Debug("  + %s expanded by %s %s to %s", zoneName(zone), types, nodes, zone|nodes)
	}

	return nodes, types
}

func (a *Allocator) defaultExpand(nodes NodeMask, types TypeMask) (NodeMask, TypeMask) {
	//
	// Our default expansion algorithm extends the given node set by finding
	// for each type and node present in the (original) set the closest new
	// nodes of that type not in the set yet.
	//
	// TODO(klihub): Note that this implementation really does not consider
	// any of the new nodes when looking for closest neighbors for a type.
	// Is this okay ? Depending on the distance matrix, it sometimes gives
	// results that seem unintuitive at first. Should we do a final pass on
	// the new nodes and add for each type any neighbors which are as close
	// or closer than the used new nodes for that type ? IOW, if for a type
	// T we used new node N at distance D from some of the existing nodes,
	// should we try to look for nodes N' which are at distance D' <= D
	// from any of the newly added nodes ?
	//
	// TODO(klihub): Consider caching the results of expansion per node set,
	// nuking the cache for unused zones (which we remove in Commit()).
	//

	var (
		newNodes NodeMask
		newTypes TypeMask
	)

	types.Foreach(func(t Type) bool {
		var (
			node = NodeMask(0)
			dist = math.MaxInt
		)
		a.ForeachNode(nodes, func(n *Node) bool {
			n.ForeachDistance(func(d int, dn NodeMask) bool {
				if dn &= a.masks.nodes.byTypes[t.Mask()] &^ nodes; dn == 0 {
					return true
				}
				if d <= dist {
					dist = d
					node |= dn
				}
				return false
			})
			return true
		})
		if node != 0 {
			newNodes |= node
			newTypes |= t.Mask()
		}
		return true
	})

	details.Debug("expanded nodes %s by types %s to %s %s", nodes, types, newTypes, newNodes)

	return newNodes, newTypes
}

func (a *Allocator) checkOverflow(nodes NodeMask) ([]NodeMask, map[NodeMask]int64) {
	var (
		zones = []NodeMask{}
		spill = map[NodeMask]int64{}
	)

	for z := range a.zones {
		if nodes == 0 || (z&nodes) != 0 {
			if free := a.zoneFree(z); free < 0 {
				zones = append(zones, z)
				spill[z] = -free
			}
		}
	}

	slices.SortFunc(zones, func(z1, z2 NodeMask) int {
		l1, l2 := len(a.zones[z1].users), len(a.zones[z2].users)
		if l1 != 0 && l2 == 0 {
			return -1
		}
		if l1 == 0 && l2 != 0 {
			return 1
		}
		if (z1 & z2) == z1 {
			return -1
		}
		if (z1 & z2) == z2 {
			return 1
		}
		if diff := z2.Size() - z1.Size(); diff < 0 {
			return -1
		} else if diff > 0 {
			return 1
		}
		return int(z2 - z1)
	})

	return zones, spill
}

func (a *Allocator) resolveOverflow(nodes NodeMask) error {
	zones, spill := a.checkOverflow(nodes)
	if len(zones) == 0 {
		return nil
	}

	return a.handleOverflow(nodes, zones, spill)
}

func (a *Allocator) handleOverflow(nodes NodeMask, zones []NodeMask, spill map[NodeMask]int64) error {
	if a.custom.HandleOverflow != nil {
		return a.custom.HandleOverflow(spill, &customAllocator{a})
	} else {
		return a.defaultHandleOverflow(nodes, zones, spill)
	}
}

func (a *Allocator) defaultHandleOverflow(nodes NodeMask, zones []NodeMask, spill map[NodeMask]int64) error {
	//
	// This is our default zone overflow resolution algorithm.
	//
	//   - find all zones which overflow, sort them subzones first
	//   - shrink zone usage by moving requests to expanded zones, starting with low inertia
	//   - expand zones first with existing types, then with DRAM, PMEM and HBM,
	//   - repeat allowing higher inertia, (Guaranteed, then Preserved, Reserved is immovable)
	//

	for ; len(zones) != 0; zones, spill = a.checkOverflow(nodes) {

		if log.DebugEnabled() {
			log.Debug("- resolving overflow for zones:")
			for _, z := range zones {
				log.Debug("  %s: %s", zoneName(z), prettySize(spill[z]))
				for _, r := range a.zones[z].users {
					log.Debug("    - user %s", r)
				}
			}
		}

		moved := int64(0)
		for _, inertia := range []Inertia{Burstable, Guaranteed, Preserved} {
			types := TypeMask(0)
			for i, extra := range []TypeMask{0, TypeMaskDRAM, TypeMaskPMEM, TypeMaskHBM} {
				extra &= a.masks.types
				if i > 0 && (types|extra) == types {
					continue
				}
				types |= extra

				for _, z := range zones {
					//if i == 0 || (a.zoneType(z)&types) != types {
					if reduce, ok := spill[z]; ok {
						m := a.zoneShrinkUsage(z, reduce, inertia, types)
						reduce -= m
						moved += m
					}
					//}
				}

				zones, spill = a.checkOverflow(nodes)
				if len(zones) == 0 {
					return nil
				}
				log.Debug("+ updated overflow for zones:")
				for _, z := range zones {
					log.Debug("  %s: %s", zoneName(z), prettySize(spill[z]))
					for _, r := range a.zones[z].users {
						log.Debug("    - user %s", r)
					}
				}
			}
		}

		if moved == 0 {
			break
		}
	}

	var (
		failed = []string{}
		total  = int64(0)
	)

	for z, amount := range spill {
		failed = append(failed, z.String())
		total += amount
	}

	return fmt.Errorf("%w: failed to resolve overflow, zones %s overflow by %s",
		ErrNoMem, strings.Join(failed, ","), prettySize(total))
}

type recording struct {
	updates map[string]NodeMask
	reverts map[string]NodeMask
}

func (a *Allocator) recordChanges() error {
	if a.record != nil {
		return fmt.Errorf("%w: failed, allocator is already recording", ErrInternalError)
	}

	a.record = &recording{
		updates: make(map[string]NodeMask),
		reverts: make(map[string]NodeMask),
	}

	return nil
}

func (a *Allocator) commitRecord() map[string]NodeMask {
	r := a.record
	a.record = nil

	return r.updates
}

func (a *Allocator) revertRecord(req *Request) (map[string]NodeMask, error) {
	if a.record == nil {
		return nil, nil
	}

	log.Debug("reverting recorded changes...")

	record := a.record
	a.record = nil
	for id, orig := range record.reverts {
		r, ok := a.requests[id]
		if !ok {
			if req == nil || req.ID() != id {
				return nil, fmt.Errorf("%w: revert failed, can't find request %s",
					ErrInternalError, id)
			}
			r = req
		}
		current, ok := a.users[id]
		if !ok {
			return nil, fmt.Errorf("%w: revert failed, no zone for request #%s",
				ErrInternalError, id)
		}
		a.zoneRemove(current, id)
		if orig != 0 {
			a.zoneAssign(orig, r)
		}
	}

	if req != nil {
		delete(a.requests, req.ID())
	}

	a.DumpState()

	return record.updates, nil
}

func (r *recording) assign(zone NodeMask, id string) {
	if r == nil {
		return
	}

	r.updates[id] = zone
	if _, ok := r.reverts[id]; ok {
		return
	}
	r.reverts[id] = 0
}

func (r *recording) delete(zone NodeMask, id string) {
	if r == nil {
		return
	}
	if _, ok := r.reverts[id]; ok {
		return
	}
	r.reverts[id] = zone
}

// Commit the given offer, turning it into an allocation. Any other
// pending offers are invalidated, making them uncommittable.
func (o *Offer) Commit() (NodeMask, map[string]NodeMask, error) {
	if !o.IsValid() {
		return 0, nil, fmt.Errorf("%w: version %d != %d", ErrExpiredOffer, o.version, o.a.version)
	}

	o.a.checkState("pre-Commit()")
	defer o.a.checkState("post-Commit()")

	for id, zone := range o.updates {
		if id == o.req.ID() {
			o.a.zoneAssign(zone, o.req)
			o.a.requests[o.req.ID()] = o.req
		} else {
			req, ok := o.a.requests[id]
			if ok {
				o.a.zoneMove(zone, req)
				req.zone = zone
			}
		}
	}

	o.a.invalidateOffers()

	defer o.a.DumpState()

	log.Debug("committed offer %s to %s", o.req, o.req.Zone())

	for z, zone := range o.a.zones {
		if len(zone.users) == 0 {
			log.Debug("removing unused zone %s...", z)
			delete(o.a.zones, z)
		}
	}

	return o.NodeMask(), o.Updates(), nil
}

func (o *Offer) IsValid() bool {
	return o.version == o.a.version
}

func (o *Offer) NodeMask() NodeMask {
	return o.updates[o.req.ID()]
}

func (o *Offer) Updates() map[string]NodeMask {
	u := maps.Clone(o.updates)
	delete(u, o.req.ID())
	if len(u) == 0 {
		return nil
	}
	return u
}
