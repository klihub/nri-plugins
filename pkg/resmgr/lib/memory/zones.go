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
	"slices"
)

// Zone represents a set of Nodes which are collectively used to
// satisfy some existing or past allocations.
type Zone struct {
	nodes    NodeMask            // IDs of nodes that make up this zone
	types    TypeMask            // types of memory in this zone
	capacity int64               // total memory capacity in this zone
	users    map[string]*Request // requests allocated from this zone
}

// ZoneType returns the TypeMask for the zone.
func (a *Allocator) ZoneType(zone NodeMask) TypeMask {
	return a.zoneType(zone & a.masks.nodes.all)
}

// ZoneCapacity returns the collective memory capacity of the zone.
func (a *Allocator) ZoneCapacity(zone NodeMask) int64 {
	return a.zoneCapacity(zone & a.masks.nodes.hasMemory)
}

// ZoneUsage returns the amount of allocated memory in the zone.
func (a *Allocator) ZoneUsage(zone NodeMask) int64 {
	return a.zoneUsage(zone & a.masks.nodes.hasMemory)
}

// ZoneFree returns the amount of free memory in the zone.
func (a *Allocator) ZoneFree(zone NodeMask) int64 {
	return a.zoneFree(zone & a.masks.nodes.hasMemory)
}

func (a *Allocator) zoneType(zone NodeMask) TypeMask {
	var types TypeMask

	if z, ok := a.zones[zone]; ok {
		types = z.types
	} else {
		for _, id := range (zone & a.masks.nodes.all).Slice() {
			types |= a.nodes[id].Type().Mask()
		}
	}

	return types
}

func (a *Allocator) zoneCapacity(zone NodeMask) int64 {
	var capacity int64

	if z, ok := a.zones[zone]; ok {
		capacity = z.capacity
	} else {
		for _, id := range (zone & a.masks.nodes.hasMemory).Slice() {
			capacity += a.nodes[id].capacity
		}
	}

	return capacity
}

func (a *Allocator) zoneUsage(zone NodeMask) int64 {
	var usage int64

	// Our OOM resolution algorithm considers a zone over-subscribed
	// if the total size of allocations that fully fit into the zone
	// exceeds the total capacity of nodes in the zone. We calculate
	// here the former while zoneCapacity calculates the latter.

	for nodes, z := range a.zones {
		if (zone & nodes) == nodes {
			for _, req := range z.users {
				usage += req.Size()
			}
		}
	}

	return usage
}

func (a *Allocator) zoneFree(zone NodeMask) int64 {
	return a.zoneCapacity(zone) - a.zoneUsage(zone)
}

func (a *Allocator) zoneAssign(zone NodeMask, req *Request) {
	z, ok := a.zones[zone]
	if !ok {
		z = &Zone{
			nodes:    zone,
			types:    a.zoneType(zone),
			capacity: a.zoneCapacity(zone),
			users:    map[string]*Request{},
		}
		a.zones[zone] = z
	}

	z.users[req.ID()] = req
	a.users[req.ID()] = zone
	a.state.add(zone, req.ID())

	log.Debug("+ %s: assigned %s", zoneName(zone), req)
}

func (a *Allocator) zoneRemove(zone NodeMask, id string) {
	z, ok := a.zones[zone]
	if !ok {
		return
	}

	req, ok := z.users[id]
	if !ok {
		return
	}

	delete(z.users, req.ID())
	delete(a.users, req.ID())
	a.state.del(zone, id)

	log.Debug("- %s: removed %s", zoneName(zone), req)
}

func (a *Allocator) zoneUpdate(zone NodeMask, req *Request) {
	from, ok := a.users[req.ID()]
	if ok {
		if from == zone {
			log.Warn("%s: useless move of %s, already assigned here...", zoneName(zone), req)
			return
		}
		a.zoneRemove(from, req.ID())
	}
	a.zoneAssign(zone, req)

	log.Debug("= %s: moved %s from %s", zoneName(zone), req, zoneName(from))
}

func (a *Allocator) zoneShrinkUsage(zone NodeMask, amount int64, limit Inertia, extra TypeMask) int64 {
	z, ok := a.zones[zone]
	if !ok {
		return 0
	}

	log.Debug("^ %s: free %s bytes of memory (new types %s, move <= %s)",
		zoneName(zone), prettySize(amount), extra, limit)

	//
	// Our algorithm for freeing up capacity in a zone is:
	// - find a new zone by expanding this with new nodes and optionally extra types
	// - sort all requests up to an inertia limit by decreasing size
	// - move requests to the new zone, stopping if we we've freed up enough capacity
	//

	nodes, types := a.expand(zone, z.types|extra)

	if nodes == 0 {
		log.Debug("= %s: couldn't move any requests", zoneName(zone))
		return 0
	}

	moved := int64(0)
	for _, req := range z.sortRequests(limit) {
		if !req.IsStrict() || req.Types() == z.types|types {
			a.zoneUpdate(zone|nodes, req)
			moved += req.Size()
			if moved >= amount {
				break
			}
		}
	}

	log.Debug("v %s: freed up %s bytes of memory", zoneName(zone), prettySize(moved))

	return moved
}

func (z *Zone) sortRequests(limit Inertia) []*Request {
	requests := make([]*Request, 0, len(z.users))
	for _, req := range z.users {
		if req.Inertia() <= limit {
			requests = append(requests, req)
		}
	}
	slices.SortFunc(requests, SortRequestsByInertiaAndSize)

	return requests
}

func zoneName(zone NodeMask) string {
	if zone != 0 {
		return "zone<" + zone.MemsetString() + ">"
	} else {
		return "zone<without nodes>"
	}
}
