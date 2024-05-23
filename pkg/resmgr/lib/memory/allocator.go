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

	logger "github.com/containers/nri-plugins/pkg/log"
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
}

// AllocatorOption is an option for an Allocator.
type AllocatorOption func(*Allocator) error

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

// WithSystemNodes returns an option to assign memory to an allocator.
// It uses the given sysfs instance to discover memory and assigns all
// of it to the allocator.
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

// NewAllocator creates a new allocator instance with the given options.
func NewAllocator(options ...AllocatorOption) (*Allocator, error) {
	return newAllocator(options...)
}

// GetOffer returns an allocation offer for the given request. The offer
// can be committed, turning it into an actual memory allocation.
func (a *Allocator) GetOffer(req *Request) (*Offer, error) {
	return a.getOffer(req)
}

// Allocate allocates memory for the given request. It is equivalent to
// taking and then committing an offer for the request.
func (a *Allocator) Allocate(req *Request) (NodeMask, map[string]NodeMask, error) {
	return a.allocate(req)
}

// Realloc reallocates an existing allocation with the given extra nodes and memory types.
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
// allocations and invalidates any uncommitted valid offers.
func (a *Allocator) Reset() {
	a.reset()
	a.version++
}

func newAllocator(options ...AllocatorOption) (*Allocator, error) {
	a := &Allocator{
		nodes: make(map[ID]*Node),
		masks: NewMaskCache(),
	}

	a.reset()

	for _, o := range options {
		if err := o(a); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrOptionFailed, err)
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

func (a *Allocator) reset() {
	a.zones = make(map[NodeMask]*Zone)
	a.users = make(map[string]NodeMask)
	a.requests = make(map[string]*Request)
	a.version++
}

func (a *Allocator) getOffer(req *Request) (*Offer, error) {
	if err := a.validateRequest(req); err != nil {
		return nil, err
	}

	if req.types == 0 {
		req.types = a.zoneType(req.affinity)
	}

	if err := a.findInitialZone(req); err != nil {
		return nil, err
	}

	a.checkpointState()
	defer func() {
		a.restoreState(req)
	}()

	a.requests[req.ID()] = req
	a.zoneAssign(req.zone, req)

	if err := a.resolveZoneOOM(req.zone); err != nil {
		return nil, err
	}

	updates, err := a.restoreState(req)
	if err != nil {
		return nil, err
	}

	return a.newOffer(req, updates), nil
}

func (a *Allocator) validateRequest(req *Request) error {
	if (req.affinity & a.masks.nodes.all) != req.affinity {
		unknown := req.affinity &^ a.masks.nodes.all
		return fmt.Errorf("%w: unknown nodes requested (%s)", ErrInvalidNode, unknown)
	}

	if (req.types & a.masks.types) != req.types {
		unavailable := req.types &^ a.masks.types
		return fmt.Errorf("%w: unavailable types requested (%s)", ErrInvalidType, unavailable)
	}

	if req.affinity == 0 {
		return fmt.Errorf("%w: request without nodes", ErrNoMem)
	}

	if _, ok := a.requests[req.ID()]; ok {
		return fmt.Errorf("%w", ErrAllocExists)
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

func (a *Allocator) LogConfig() {
	a.LogConfigWithLogger(log.Info)
}

func (a *Allocator) LogConfigWithLogger(logfn func(string, ...interface{})) {
	logfn("memory allocator configuration")
	for id := range a.masks.nodes.all.Slice() {
		n := a.nodes[id]
		logfn("  %s node #%d with %s memory", n.memType, n.id, prettySize(n.capacity))
		logfn("    distance vector %v", n.distance.vector)
		n.ForeachByDistance(func(d int, nodes NodeMask) bool {
			logfn("      at distance %d: %s", d, nodes)
			return true
		})
	}
}

func (a *Allocator) LogZones() {
	a.LogZonesWithLogger(log.Info)
}

func (a *Allocator) LogZonesWithLogger(logfn func(string, ...interface{})) {
	zones := []NodeMask{}
	for z := range a.zones {
		zones = append(zones, z)
	}
	slices.SortFunc(zones, func(a, b NodeMask) int {
		if a.Size() < b.Size() {
			return -1
		}
		if b.Size() < a.Size() {
			return 1
		}
		return int(a - b)
	})

	for _, zone := range zones {
		z := a.zones[zone]
		capa := prettySize(a.zoneCapacity(z.nodes))
		use := prettySize(a.zoneUsage(z.nodes))
		free := prettySize(a.zoneFree(z.nodes))
		logfn("%s (%s): capacity %s, usage: %s, free: %s", z.nodes, z.types, capa, use, free)
		for _, req := range z.users {
			logfn("  + %s", req)
		}
	}
}

func (a *Allocator) LogRequests() {
	a.LogRequestsWithLogger(log.Info)
}

func (a *Allocator) LogRequestsWithLogger(logfn func(string, ...interface{})) {
	ids := []string{}
	for id := range a.requests {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int {
		return strings.Compare(a, b)
	})

	for _, id := range ids {
		req := a.requests[id]
		logfn("  - %s", req)
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

type (
	ID = idset.ID
)

type Offer struct {
	a       *Allocator
	req     *Request
	updates map[string]NodeMask
	version int64
}

var (
	log = logger.Get("libmem")
)

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

	if err := a.zoneMove(req.zone|newNodes, req); err != nil {
		return 0, nil, fmt.Errorf("%w: failed to reallocate, can't move: %w", ErrNoMem, err)
	}

	if err := a.resolveZoneOOM(req.zone); err != nil {
		req.zone = a.users[req.ID()]
		return 0, nil, fmt.Errorf("%w: failed to reallocate: %w", ErrNoMem, err)
	}

	req.zone |= newNodes
	req.types |= newTypes

	return req.zone, a.commitState(), nil
}

func (a *Allocator) release(id string) error {
	log.Debug("=> releasing workload #%s", id)
	zone, ok := a.users[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownAlloc, id)
	}

	a.zoneRemove(zone, id)
	delete(a.requests, id)

	return nil
}

func (a *Allocator) expand(nodes NodeMask, types TypeMask) (NodeMask, TypeMask) {
	extraNodes := NodeMask(0)
	extraTypes := TypeMask(0)
	for _, t := range types.Slice() {
		var (
			min = math.MaxInt
			ids = map[int]NodeMask{}
		)

		for _, id := range nodes.Slice() {
			a.nodes[id].ForeachByDistance(func(d int, dNodes NodeMask) bool {
				if d > min {
					return false
				}

				extra := dNodes & a.masks.nodes.byTypes[t.Mask()] &^ nodes
				if extra == 0 {
					return true
				}

				ids[d] |= extra
				min = d
				return false
			})
		}

		if min < math.MaxInt {
			extraNodes |= ids[min]
			extraTypes |= t.Mask()
		}
	}

	log.Debug("%s expanded by %s to %s %s (%s %s)", nodes, types, extraTypes, extraNodes,
		nodes|extraNodes, types|extraTypes)

	return extraNodes, extraTypes
}

func (a *Allocator) checkZoneOOM(nodes NodeMask) ([]NodeMask, map[NodeMask]int64) {
	var (
		oom   = map[NodeMask]int64{}
		zones = []NodeMask{}
	)

	for zone := range a.zones {
		if (zone & nodes) != 0 {
			if free := a.zoneFree(zone); free < 0 {
				oom[zone] = -free
				zones = append(zones, zone)
			}
		}
	}

	slices.SortFunc(zones, func(z1, z2 NodeMask) int {
		if len(a.zones[z1].users) != 0 && len(a.zones[z2].users) == 0 {
			return -1
		}
		if len(a.zones[z1].users) == 0 && len(a.zones[z2].users) != 0 {
			return 1
		}
		if diff := z2.Size() - z1.Size(); diff < 0 {
			return -1
		} else if diff > 0 {
			return 1
		}
		return int(z2 - z1)
	})

	if log.DebugEnabled() {
		for _, z := range zones {
			log.Debug("- OOM: zone/%s: overflown by %s", z, prettySize(oom[z]))
		}
	}

	return zones, oom
}

func (a *Allocator) resolveZoneOOM(nodes NodeMask) error {
	zones, oom := a.checkZoneOOM(nodes)
	if len(zones) == 0 {
		return nil
	}

	var (
		maxInertia = []Inertia{Burstable, Guaranteed, Preserved}
		extraTypes = []TypeMask{
			a.masks.types & TypeDRAM,
			a.masks.types & (TypeDRAM | TypePMEM),
			a.masks.types & (TypeDRAM | TypePMEM | TypeHBM),
		}
	)

	for _, inertia := range maxInertia {
		for i := 0; i < len(extraTypes); {
			var (
				types = extraTypes[i]
				moved = int64(0)
			)
			if types == 0 || (i > 0 && types == extraTypes[i-1]) {
				i++
				continue
			}

			for _, z := range zones {
				if spill, ok := oom[z]; ok {
					m := a.zoneExpand(z, spill, inertia, types)
					spill -= m
					moved += m
				}
			}

			zones, oom = a.checkZoneOOM(nodes)
			if len(oom) == 0 {
				return nil
			}
			if moved == 0 {
				i++
			}
		}
	}

	unresolved := []string{}
	remaining := int64(0)
	for zone, spill := range oom {
		unresolved = append(unresolved, zone.String())
		remaining += spill
	}

	return fmt.Errorf("%w: failed to resolve OOM, zones %s OOM by %s", ErrNoMem,
		strings.Join(unresolved, ","), prettySize(remaining))
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
				o.a.zoneMove(zone, req)
				req.zone = zone
			}
		}
	}

	o.a.version++

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
