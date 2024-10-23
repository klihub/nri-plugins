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

package libcpu

import "fmt"

// Zone is a set of CPUs that can be used to fulfill CPU allocation
// requests.
type Zone struct {
	addr     string
	cpus     CpuMask
	isolated CpuMask
	shared   CpuMask
	users    map[string]*Request
}

// ForeachZone calls fn with each zone in use, until fn returns false.
func (a *Allocator) ForeachZone(fn func(*Zone) bool) {
	for _, z := range a.zones {
		if !fn(z) {
			return
		}
	}
}

// GetZone returns the zone corresponding to the given set of CPUs.
func (a *Allocator) GetZone(cpus CpuMask) *Zone {
	var (
		addr = cpus.HexaString()
		z    = a.zones[addr]
	)

	if z == nil {
		z = &Zone{
			addr:     addr,
			cpus:     cpus.Clone(),
			isolated: cpus.Intersection(a.isolated),
			shared:   cpus.Clone().AndNot(a.isolated),
			users:    make(map[string]*Request),
		}

		a.zones[addr] = z
	}

	return z
}

func (a *Allocator) ZoneSharedCapacity(z *Zone) int {
	capacity := 1000*z.shared.Size() - z.Usage()
	a.ForeachZone(func(o *Zone) bool {
		if z != o && o.IsSubzoneOf(z) {
			capacity -= o.Usage()
		}
		return true
	})
	return capacity
}

func (a *Allocator) ZoneHasCapacity(z *Zone, exclusive, shared int) bool {
	sharedNeeded := shared
	if exclusive > 0 {
		if exclusive > z.isolated.Size() {
			sharedNeeded += 1000 * exclusive
		}
	} else {
		if shared == 0 {
			shared = 2
		}
	}
	return a.ZoneSharedCapacity(z) >= shared
}

type Overflow map[string]int

func (a *Allocator) checkZoneUsage() Overflow {
	overflow := make(Overflow)

	a.ForeachZone(func(o *Zone) bool {
		if free := a.ZoneSharedCapacity(o); free < 0 {
			overflow[o.cpus.String()] = -free
			log.Info("*** overflow: %s = %d", o.cpus, -free)
		}
		return true
	})

	return overflow
}

func (of Overflow) Error() error {
	if len(of) == 0 {
		return nil
	}

	var err error

	for zone, amount := range of {
		e := fmt.Errorf("zone %s lacks %dm CPU capacity", zone, amount)
		if err != nil {
			err = fmt.Errorf("%w, %w", err, e)
		} else {
			err = e
		}
	}

	return fmt.Errorf("%w: %w", ErrNoCpu, err)
}

func (z *Zone) Usage() int {
	usage := 0
	for _, req := range z.users {
		log.Debug("zone %s, accounting for %s", z.cpus, req)
		usage += req.shared
	}
	log.Debug("zone usage(%s): %d", z.cpus, usage)
	return usage
}

func (z *Zone) IsSubzoneOf(o *Zone) bool {
	return z.cpus.IsSubsetOf(o.cpus)
}

func (z *Zone) IsDisjoint(o *Zone) bool {
	return z.cpus.IsDisjoint(o.cpus)
}

func (z *Zone) PartialOverlap(o *Zone) bool {
	return !z.IsDisjoint(o) && !z.IsSubzoneOf(o) && !o.IsSubzoneOf(z)
}

func (z *Zone) assign(req *Request) {
	z.users[req.ID()] = req
}

func (z *Zone) unassign(req *Request) {
	delete(z.users, req.ID())
}
