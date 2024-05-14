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

	if nodes == NodeMaskForIDs(1) {
		log.Debug("- zone %s, direct usage %d...", nodes, u)
	}

	for m, z := range zones.zones {
		if (m&nodes) == m && m != nodes {
			u += z.usage
			if nodes == NodeMaskForIDs(1) {
				log.Debug("+ zone %s + sub-zone %s direct usage %d = %d...", nodes, m, z.usage, u)
			}
		}
	}
	return u
}

func (zones *Zones) free(nodes NodeMask) int64 {
	return zones.capacity(nodes) - zones.usage(nodes)
}

func (zones *Zones) add(nodes NodeMask, workload string, amount int64) {
	log.Debug("...add #%s (%d) to %s", workload, amount, nodes)
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

	if zones.changes != nil {
		zones.changes[workload] = nodes
	}
}

func (zones *Zones) remove(workload string) error {
	nodes, ok := zones.assign[workload]
	if !ok {
		return fmt.Errorf("can't remove workload %s, no assignment found", workload)
	}
	z, ok := zones.zones[nodes]
	if !ok {
		log.Warn("can't remove workload %s from %s, not found in zone", workload, nodes)
		return nil
	}

	size := z.workloads[workload]
	delete(z.workloads, workload)
	z.usage -= size

	return nil
}

func (zones *Zones) Clone() *Zones {
	c := &Zones{
		zones:       map[NodeMask]*Zone{},
		assign:      maps.Clone(zones.assign),
		getNode:     zones.getNode,
		getKind:     zones.getKind,
		expandNodes: zones.expandNodes,
	}
	for m, z := range zones.zones {
		c.zones[m] = z.Clone()
	}
	return c
}

func (z *Zone) Clone() *Zone {
	return &Zone{
		nodes:     z.nodes,
		kinds:     z.kinds,
		capacity:  z.capacity,
		usage:     z.usage,
		workloads: maps.Clone(z.workloads),
	}
}

func (zones *Zones) checkOverflow(nodes NodeMask) (map[NodeMask]int64, []NodeMask) {
	var (
		overflow = map[NodeMask]int64{}
		masks    = []NodeMask{}
	)

	for n := range zones.zones {
		//if (n & nodes) != 0 { // for now, check unaffected nodes, too
		c := zones.capacity(n)
		u := zones.usage(n)
		f := c - u
		if f < 0 {
			overflow[n] = -f
			masks = append(masks, n)
		}
		//}
	}

	slices.SortFunc(masks, func(a, b NodeMask) int { return int(b - a) })

	return overflow, masks
}

func (zones *Zones) move(to NodeMask, workload string) {
	from := zones.assign[workload]
	if from == 0 {
		panic(fmt.Sprintf("cannot move workload %s, not assigned anywhere", workload))
	}

	z := zones.zones[from]
	amount := z.workloads[workload]

	log.Debug("...move #%s (%d) from %s to %s", workload, amount, from, to)

	delete(z.workloads, workload)
	z.usage -= amount

	zones.add(to, workload, amount)

}

func (zones *Zones) expand(from NodeMask, amount int64) error {
	zf := zones.zones[from]
	if zf == nil {
		panic(fmt.Sprintf("cannot expand %s, no such zone", from))
	}

	workloads := []string{}
	for wl := range zf.workloads {
		workloads = append(workloads, wl)
	}

	kinds := zones.getKind(from)
	n, _ := zones.expandNodes(from, kinds)
	if n == 0 {
		return fmt.Errorf("failed to expand %s with %s nodes", from, kinds)
	}

	to := from | n

	for _, wl := range workloads {
		size := zf.workloads[wl]
		zones.move(to, wl)
		amount -= size
		if amount <= 0 {
			break
		}
	}

	return nil
}

func (zones *Zones) dumpUsage(prefix string) {
	if prefix != "" {
		prefix += " "
	}
	for _, z := range zones.zones {
		log.Debug("%susage of %s: %d of %d", prefix, z.nodes,
			zones.usage(z.nodes), zones.capacity(z.nodes))
	}
}
