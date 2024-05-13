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
	"slices"
)

func (a *Allocator) removeAbsentKinds(kinds KindMask) KindMask {
	return kinds & a.GetAvailableKinds()
}

func (a *Allocator) foreachNodeInMask(nodes NodeMask, fn func(*Node) bool) {
	for _, id := range nodes.IDs() {
		n, ok := a.nodes[id]
		if !ok {
			panic(fmt.Sprintf("non-existent node %d in mask 0x%x", id, nodes))
		}
		if !fn(n) {
			return
		}
	}
}

func (zones *Zones) capacity(nodes NodeMask) int64 {
	if z, ok := zones.zones[nodes]; ok {
		return z.capacity
	}

	var capacity int64
	for _, id := range nodes.IDs() {
		if n := zones.getNode(id); n != nil {
			capacity += n.capacity
		}
	}
	return capacity
}

func (zones *Zones) usage(nodes NodeMask) int64 {
	var u int64
	if z, ok := zones.zones[nodes]; ok {
		u = z.usage
	}

	for m, z := range zones.zones {
		if (m&nodes) == m && m != nodes {
			u += z.usage
		}
	}
	return u
}

func (zones *Zones) add(nodes NodeMask, request *Request) {
	z, ok := zones.zones[nodes]
	if !ok {
		z = &Zone{
			nodes:     nodes,
			capacity:  zones.capacity(nodes),
			usage:     0,
			workloads: map[string]int64{},
		}
		zones.zones[nodes] = z
	}

	z.workloads[request.Workload] = request.Amount
	z.usage += request.Amount
	zones.assign[request.Workload] = z.nodes
	log.Debug("+ zone %s now uses %d due to direct assignment of #%s (%d)",
		z.nodes, z.usage, request.Workload, request.Amount)
}

func (zones *Zones) Clone() *Zones {
	c := &Zones{
		zones:   maps.Clone(zones.zones),
		assign:  maps.Clone(zones.assign),
		getNode: zones.getNode,
	}
	for _, z := range c.zones {
		z.workloads = maps.Clone(z.workloads)
	}
	return c
}

func (zones *Zones) getUsage(nodes NodeMask) map[NodeMask]int64 {
	var (
		usage = map[NodeMask]int64{}
		masks []NodeMask
	)

	for n := range zones.zones {
		usage[n] = zones.usage(n)
		masks = append(masks, n)
	}

	slices.SortFunc(masks, func(a, b NodeMask) int { return int(a - b) })

	for _, n := range masks {
		log.Debug("  * zone %s now uses %d", n, usage[n])
	}

	return usage
}

func (zones *Zones) checkOverflow(nodes NodeMask) map[NodeMask]int64 {
	var (
		overflow = map[NodeMask]int64{}
		masks    = []NodeMask{}
	)

	for n := range zones.zones {
		//if (n & nodes) != 0 {
		c := zones.capacity(n)
		u := zones.usage(n)
		f := c - u
		if f < 0 {
			overflow[n] = -f
			masks = append(masks, n)
		}
		//}
	}

	slices.SortFunc(masks, func(a, b NodeMask) int { return int(a - b) })

	for _, m := range masks {
		log.Debug("  !!!!! node %s overflown by %d bytes", m, overflow[m])
	}

	return overflow
}
