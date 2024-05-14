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

func (zones *Zones) free(nodes NodeMask) int64 {
	return zones.capacity(nodes) - zones.usage(nodes)
}

func (zones *Zones) add(nodes NodeMask, workload string, amount int64) {
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

	z.workloads[workload] = amount
	z.usage += amount
	zones.assign[workload] = z.nodes
	log.Debug("+ zone %s now uses %d due to direct assignment of #%s (%d)",
		z.nodes, z.usage, workload, amount)
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

func (zones *Zones) checkOverflow(nodes NodeMask) map[NodeMask]int64 {
	var (
		overflow = map[NodeMask]int64{}
		masks    = []NodeMask{}
	)

	for n := range zones.zones {
		//if (n & nodes) != 0 {  // for now, check unaffected nodes, too
		c := zones.capacity(n)
		u := zones.usage(n)
		f := c - u
		if f < 0 {
			overflow[n] = -f
			masks = append(masks, n)
		}
		//}
	}

	return overflow
}

func (zones *Zones) move(to NodeMask, workload string) {
	from := zones.assign[workload]
	if from == 0 {
		panic(fmt.Sprintf("cannot move workload %s, not assigned anywhere", workload))
	}

	z := zones.zones[from]
	amount := z.workloads[workload]
	delete(z.workloads, workload)
	z.usage -= amount

	zones.add(to, workload, amount)
}
