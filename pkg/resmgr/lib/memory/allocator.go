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
	state    *checkpoint

	customExpandZone     func(zone NodeMask, types TypeMask, a CustomAllocator) NodeMask
	customHandleOverflow func(overflow map[NodeMask]int64, a CustomAllocator) error
}

type (
	ID = idset.ID
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

// GetOffer returns an allocation offer for the given request. Until the
// offer expires it can be Commit()ed, turning into an allocation. The
// offer expires if any memory is allocated (directly or by committing
// any other unexpired offer) or released.
func (a *Allocator) GetOffer(req *Request) (*Offer, error) {
	return a.getOffer(req)
}

// Allocate allocates memory for the given request. It is equivalent to
// a successful GetOffer() followed by a commit of the returned offer.
func (a *Allocator) Allocate(req *Request) (NodeMask, map[string]NodeMask, error) {
	return a.allocate(req)
}

// Realloc reallocates an allocation with the given extra nodes and memory types.
func (a *Allocator) Realloc(id string, nodes NodeMask, types TypeMask) (NodeMask, map[string]NodeMask, error) {
	return a.realloc(id, nodes&a.masks.nodes.all, types&a.masks.types)
}

// AllocatedZone returns the assigned nodes for the given allocation if it exists.
func (a *Allocator) AllocatedZone(id string) (NodeMask, bool) {
	if zone, ok := a.users[id]; ok {
		return zone, true
	}
	return 0, false
}

// Release releases the allocation with the given ID.
func (a *Allocator) Release(id string) error {
	return a.release(id)
}

// Reset resets the state of the allocator. Effectively it releases all existing
// allocations and invalidates any uncommitted offers.
func (a *Allocator) Reset() {
	a.reset()
	a.invalidateOffers()
}

// ForeachNode calls the given function with each node present in the mask,
// until the function returns false.
func (a *Allocator) ForeachNode(nodes NodeMask, fn func(n *Node) bool) {
	for _, id := range (nodes & a.masks.nodes.all).Slice() {
		if !fn(a.nodes[id]) {
			return
		}
	}
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
		a.masks.AddNode(n)
	}

	a.LogConfig()

	return a, nil
}

func (a *Allocator) getOffer(req *Request) (*Offer, error) {
	if err := a.validateRequest(req); err != nil {
		return nil, err
	}

	if err := a.findInitialZone(req); err != nil {
		return nil, err
	}

	if err := a.ensureNormalMemory(req); err != nil {
		return nil, err
	}

	a.checkpointState()
	defer func() {
		a.restoreState(req)
	}()

	a.requests[req.ID()] = req
	a.zoneAssign(req.zone, req)

	if err := a.resolveOverflow(req.zone); err != nil {
		return nil, err
	}

	updates, err := a.restoreState(req)
	if err != nil {
		return nil, err
	}

	return a.newOffer(req, updates), nil
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
		return fmt.Errorf("%w", ErrAlreadyExists)
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
	req.zone = req.affinity & a.masks.nodes.byTypes[req.types]
	missing := req.types &^ a.zoneType(req.zone)

	if missing != 0 {
		nodes, types := a.expand(req.zone, missing)
		if types != missing {
			return fmt.Errorf("%w: failed to find init nodes of type %s",
				ErrInternalError, missing&^types)
		}

		req.zone |= nodes
		req.types |= types
	}

	return nil
}

func (a *Allocator) ensureNormalMemory(req *Request) error {
	if (req.zone & a.masks.nodes.normal) != 0 {
		return nil
	}

	zone := req.zone
	types := req.types
	if !req.IsStrict() {
		types |= TypeMaskDRAM
	}

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

// ------------------------------------------------------------------------

type checkpoint struct {
	updates map[string]NodeMask
	reverts map[string]NodeMask
}

func (a *Allocator) checkpointState() {
	a.state = &checkpoint{
		updates: make(map[string]NodeMask),
		reverts: make(map[string]NodeMask),
	}
}

func (a *Allocator) restoreState(req *Request) (map[string]NodeMask, error) {
	log.Debug("reverting allocator to checkpointed state for %s...", req)

	state := a.state

	if state == nil {
		return nil, nil
	}

	a.state = nil

	for id, zone := range state.reverts {
		if zone != 0 {
			req, ok := a.requests[id]
			if !ok {
				return nil, fmt.Errorf("%w: failed to restore checkpoint, request #%s not found",
					ErrInternalError, id)
			}
			a.zoneAssign(zone, req)
		} else {
			z, ok := a.users[id]
			if !ok {
				return nil, fmt.Errorf("%w: failed to restore checkpoint, zone not found for request #%s",
					ErrInternalError, id)
			}
			a.zoneRemove(z, id)
		}
	}

	if req != nil {
		delete(a.requests, req.ID())
	}

	return state.updates, nil
}

func (a *Allocator) commitState() map[string]NodeMask {
	state := a.state
	a.state = nil

	return state.updates
}

func (c *checkpoint) add(zone NodeMask, id string) {
	if c != nil {
		c.updates[id] = zone
		if _, ok := c.reverts[id]; !ok {
			c.reverts[id] = 0
		}
	}
}

func (c *checkpoint) del(zone NodeMask, id string) {
	if c != nil {
		if _, ok := c.reverts[id]; !ok {
			c.reverts[id] = zone
		}
	}
}

type Offer struct {
	a       *Allocator
	req     *Request
	updates map[string]NodeMask
	version int64
}

func (a *Allocator) Masks() *MaskCache {
	return a.masks
}

func (a *Allocator) allocate(req *Request) (NodeMask, map[string]NodeMask, error) {
	o, err := a.getOffer(req)
	if err != nil {
		return 0, nil, err
	}
	return o.Commit()
}

func (a *Allocator) realloc(id string, nodes NodeMask, types TypeMask) (NodeMask, map[string]NodeMask, error) {
	req, ok := a.requests[id]
	if !ok {
		return 0, nil, fmt.Errorf("%w: can't reallocate %s", ErrUnknownID, id)
	}

	if nodes == 0 && types == 0 {
		return req.zone, nil, nil
	}

	a.checkpointState()
	defer func() {
		a.restoreState(nil)
	}()

	newNodes, newTypes := a.expand(req.zone|nodes, req.types|types)
	if newNodes == 0 {
		return 0, nil, fmt.Errorf("%w: failed to reallocate, can't find new %s nodes",
			ErrNoMem, types)
	}

	a.zoneUpdate(req.zone|newNodes, req)

	if err := a.resolveOverflow(req.zone | newNodes); err != nil {
		req.zone = a.users[req.ID()]
		return 0, nil, fmt.Errorf("%w: failed to reallocate: %w", ErrNoMem, err)
	}

	req.zone |= newNodes
	req.types |= newTypes

	return req.zone, a.commitState(), nil
}

func (a *Allocator) release(id string) error {
	zone, ok := a.users[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownID, id)
	}

	a.zoneRemove(zone, id)
	delete(a.requests, id)
	a.invalidateOffers()

	return nil
}

func (a *Allocator) expand(zone NodeMask, types TypeMask) (NodeMask, TypeMask) {
	var nodes NodeMask
	if a.customExpandZone != nil {
		nodes = a.customExpandZone(zone, types, &customAllocator{a})
		types = a.zoneType(nodes)
	} else {
		nodes, types = a.defaultExpand(zone, types)
	}
	return nodes, types
}

func (a *Allocator) defaultExpand(nodes NodeMask, types TypeMask) (NodeMask, TypeMask) {
	var (
		newNodes NodeMask
		newTypes TypeMask
		minDist  = math.MaxInt
		distIDs  = map[int]NodeMask{}
	)

	//
	// for each type find the closest set of nodes of that type from any of the existing nodes
	//

	types.Foreach(func(t Type) bool {
		a.ForeachNode(nodes, func(n *Node) bool {
			n.ForeachDistance(func(dist int, distNodes NodeMask) bool {
				if dist > minDist {
					return false
				}

				extra := distNodes & a.masks.nodes.byTypes[t.Mask()] &^ nodes
				if extra == 0 {
					return dist < minDist // if we're not at minDist yet, check next distance
				}

				distIDs[dist] |= extra
				minDist = dist

				return false // picked nodes, we're done here (next dist > minDist)
			})

			return true
		})

		return true
	})

	if minDist < math.MaxInt {
		newNodes = distIDs[minDist]
		newTypes = a.zoneType(newNodes)
	}

	if newNodes != 0 {
		log.Debug("%s expanded to %s %s", zoneName(nodes), types|newTypes, nodes|newNodes)
	}

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

	if log.DebugEnabled() && len(zones) > 0 {
		log.Debug("overflowing zones:")
		for _, z := range zones {
			log.Debug("  %s: %s", zoneName(z), prettySize(spill[z]))
		}
	}

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
	if a.customHandleOverflow != nil {
		return a.customHandleOverflow(spill, &customAllocator{a})
	} else {
		return a.defaultHandleOverflow(nodes, zones, spill)
	}
}

func (a *Allocator) defaultHandleOverflow(nodes NodeMask, zones []NodeMask, spill map[NodeMask]int64) error {
	//
	// Our default zone overflow resolution algorithm is:
	// - find all zones that overflow and sort them subzones first
	// - try to move enough requests with burstable or less inertia
	// - first by expanding the zone with existing types, then with DRAM, PMEM and HBM,
	// - repeat this but allow moving Guaranteed then Preserved allocations, too
	//

	for zones, spill := a.checkOverflow(nodes); len(zones) != 0; zones, spill = a.checkOverflow(nodes) {
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

func (o *Offer) Commit() (NodeMask, map[string]NodeMask, error) {
	if !o.IsValid() {
		return 0, nil, fmt.Errorf("%w: version %d != %d", ErrExpiredOffer, o.version, o.a.version)
	}
	for id, zone := range o.updates {
		if id == o.req.ID() {
			o.a.zoneAssign(zone, o.req)
			o.a.requests[o.req.ID()] = o.req
		} else {
			req, ok := o.a.requests[id]
			if ok {
				o.a.zoneUpdate(zone, req)
				req.zone = zone
			}
		}
	}

	o.a.invalidateOffers()

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
