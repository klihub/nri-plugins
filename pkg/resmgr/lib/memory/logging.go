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

	logger "github.com/containers/nri-plugins/pkg/log"
)

var (
	log     = logger.Get("libmem")
	details = logger.Get("libmem-details")
)

func (a *Allocator) DumpConfig() {
	log.Info("memory allocator configuration")
	for id := range a.masks.nodes.all.Slice() {
		n := a.nodes[id]
		log.Info("  %s node #%d with %s memory", n.memType, n.id, prettySize(n.capacity))
		log.Info("    distance vector %v", n.distance.vector)
		n.ForeachDistance(func(d int, nodes NodeMask) bool {
			log.Info("      at distance %d: %s", d, nodes)
			return true
		})
	}
}

func (a *Allocator) DumpState() {
	a.DumpRequests()
	a.DumpZones()
}

func (a *Allocator) DumpRequests() {
	if !details.DebugEnabled() {
		return
	}

	if len(a.users) == 0 {
		details.Debug("  no allocated requests")
		return
	}

	details.Debug("  requests:")
	for _, req := range SortRequests(a.requests,
		func(r1, r2 *Request) int {
			return int(r2.Created() - r1.Created())
		},
	) {
		details.Debug("    - %s (assigned zone %s)", req, req.Zone())
	}
}

func (a *Allocator) DumpZones() {
	if !details.DebugEnabled() {
		return
	}

	if len(a.zones) == 0 {
		details.Debug("  no zones in use")
		return
	}

	zones := make([]NodeMask, 0, len(a.zones))
	for z := range a.zones {
		zones = append(zones, z)
	}
	slices.SortFunc(zones, func(z1, z2 NodeMask) int {
		if diff := z1.Size() - z2.Size(); diff != 0 {
			return diff
		}
		return int(z1 - z2)
	})

	details.Debug("  zones:")
	for _, z := range zones {
		var (
			zone = a.zones[z]
			free = prettySize(a.ZoneFree(z))
			capa = prettySize(a.ZoneCapacity(z))
			used = prettySize(a.ZoneUsage(z))
		)
		details.Debug("   - zone %s, free %s (capacity %s, used %s)", z, free, capa, used)
		if len(zone.users) == 0 {
			continue
		}

		for _, req := range SortRequests(zone.users,
			func(r1, r2 *Request) int {
				return int(r2.Created() - r1.Created())
			},
		) {
			details.Debug("      %s", req)
		}
	}
}
