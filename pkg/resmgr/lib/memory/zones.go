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
	"slices"
)

type Zone struct {
	nodes    NodeMask
	types    TypeMask
	capacity int64
	users    map[string]*Request
}

func (a *Allocator) ZoneType(zone NodeMask) TypeMask {
	return a.zoneType(zone & a.masks.nodes.all)
}

func (a *Allocator) ZoneCapacity(zone NodeMask) int64 {
	return a.zoneCapacity(zone & a.masks.nodes.all) // a.masks.nodes.hasMemory ?
}

func (a *Allocator) ZoneUsage(zone NodeMask) int64 {
	return a.zoneUsage(zone & a.masks.nodes.all) // a.masks.nodes.hasMemory ?
}

func (a *Allocator) ZoneFree(zone NodeMask) int64 {
	return a.zoneFree(zone & a.masks.nodes.all) // a.masks.nodes.hasMemory ?
}

func (a *Allocator) zoneType(zone NodeMask) TypeMask {
	if z, ok := a.zones[zone]; ok {
		return z.types
	}

	types := TypeMask(0)
	(zone & a.masks.nodes.all).Foreach(func(id ID) bool {
		types |= a.nodes[id].Type().Mask()
		return true
	})
	return types
}

func (a *Allocator) zoneCapacity(zone NodeMask) int64 {
	if z, ok := a.zones[zone]; ok {
		return z.capacity
	}

	capacity := int64(0)
	for _, id := range zone.Slice() {
		capacity += a.nodes[id].capacity
	}
	return capacity
}

func (a *Allocator) zoneUsage(zone NodeMask) int64 {
	usage := int64(0)

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
	log.Debug("zone/%s: assigning %s", zone, req)

	z, ok := a.zones[zone]
	if !ok {
		z = &Zone{
			nodes:    zone,
			types:    a.zoneType(zone),
			capacity: a.zoneCapacity(zone),
			users:    make(map[string]*Request),
		}
		a.zones[zone] = z
	}

	id := req.ID()
	z.users[id] = req
	a.users[id] = zone
	a.state.add(zone, id)

	if log.DebugEnabled() {
		log.Debug("zone/%s: %d users, free: %s - %s = %s", zone, len(z.users),
			prettySize(a.zoneCapacity(zone)), prettySize(a.zoneUsage(zone)),
			prettySize(a.zoneFree(zone)))
	}
}

func (a *Allocator) zoneRemove(zone NodeMask, id string) {
	z, ok := a.zones[zone]
	if !ok {
		log.Error("zone/%s: failed to remove request %s, zone not found", zone, id)
		return
	}

	req, ok := z.users[id]
	if !ok {
		log.Error("zone/%s: failed to remove request %s, request not found in zone", zone, id)
		return
	}

	delete(z.users, id)
	delete(a.users, id)
	a.state.del(zone, id)

	if log.DebugEnabled() {
		log.Debug("zone/%s: removed %s, %d users, free: %s - %s = %s", zone, req, len(z.users),
			prettySize(a.zoneCapacity(zone)), prettySize(a.zoneUsage(zone)),
			prettySize(a.zoneFree(zone)))
	}
}

func (a *Allocator) zoneMove(dst NodeMask, req *Request) error {
	log.Debug("zone/%s: moving request %s here...", dst, req)

	id := req.ID()
	src, ok := a.users[id]
	if !ok {
		log.Error("zone/%s: failed to move %s here, no source zone found", dst, req)
		return fmt.Errorf("%w: failed to move %s to zone/%s, no source zone found",
			ErrInternalError, req, dst)
	}

	a.zoneRemove(src, id)
	a.zoneAssign(dst, req)

	return nil
}

func (a *Allocator) zoneExpand(zone NodeMask, amount int64, movable Inertia, extra TypeMask) int64 {
	z, ok := a.zones[zone]
	if !ok {
		return 0
	}

	types := a.zoneType(zone) | extra

	log.Debug("zone/%s: expand by %s types to free %s, move up till %s requests...",
		zone, extra, prettySize(amount), movable)

	nodes, _ := a.expand(zone, types)
	if nodes == 0 {
		log.Debug("zone/%s: failed to expand, no new %s nodes found", zone, extra)
		return 0
	}

	var (
		moved    int64
		requests = []*Request{}
	)

	for _, req := range z.users {
		requests = append(requests, req)
	}
	slices.SortFunc(requests, SortRequestsByInertiaAndSize)

	for _, req := range requests {
		if req.Inertia() > movable {
			break
		}
		a.zoneMove(zone|nodes, req)
		moved += req.Size()
		if moved >= amount {
			break
		}
	}

	return moved
}
