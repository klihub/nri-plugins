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

import (
	"fmt"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/containers/nri-plugins/pkg/sysfs"
)

type Allocator struct {
	sys       sysfs.System
	picker    CpuPicker
	cpus      CpuMask
	isolated  CpuMask
	exclusive CpuMask
	zones     map[string]*Zone
	requests  map[string]*Request
	version   int64
}

type AllocatorOption func(*Allocator) error

type Offer struct {
	a       *Allocator
	request *Request
	updates map[string]CpuMask
	version int64
}

var (
	log = logger.Get("libcpu")
)

func WithSystem(sys sysfs.System) AllocatorOption {
	return func(a *Allocator) error {
		for _, o := range []AllocatorOption{
			WithDefaultCpuPicker(sys),
			WithCpus(NewCpuMaskForCPUSet(sys.OnlineCPUs())),
		} {
			a.sys = sys
			if err := o(a); err != nil {
				return err
			}
		}
		return nil
	}
}

func WithCpus(cpus CpuMask) AllocatorOption {
	return func(a *Allocator) error {
		if a.cpus != nil {
			return fmt.Errorf("CPUs already set")
		}
		a.cpus = cpus.Clone()
		return nil
	}
}

func NewAllocator(options ...AllocatorOption) (*Allocator, error) {
	a := &Allocator{
		zones:    make(map[string]*Zone),
		requests: make(map[string]*Request),
		version:  1,
	}

	for _, o := range options {
		if err := o(a); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrFailedSetup, err)
		}
	}

	a.isolated = NewCpuMaskForCPUSet(a.sys.IsolatedCPUs())

	if a.picker == nil {
		return nil, fmt.Errorf("%w: no CPU picker set", ErrFailedSetup)
	}

	log.Info("allocator set up with CPUs %s", a.cpus.KernelString())

	return a, nil
}

func (a *Allocator) GetOffer(req *Request) (*Offer, error) {
	cpus, updates, err := a.allocate(req, false)
	if err != nil {
		return nil, err
	}
	return a.newOffer(req, cpus, updates), nil
}

func (a *Allocator) Allocate(req *Request) (CpuMask, map[string]CpuMask, error) {
	return a.allocate(req, true)
}

func (a *Allocator) Release(id string) (map[string]CpuMask, error) {
	req, ok := a.requests[id]
	if !ok {
		return nil, fmt.Errorf("%w: unknown allocation %s", ErrUnknownRequest, id)
	}

	return a.release(req)
}

func (a *Allocator) newOffer(req *Request, cpus CpuMask, updates map[string]CpuMask) *Offer {
	return &Offer{
		a:       a,
		request: req,
		updates: updates,
		version: a.version,
	}
}

func (a *Allocator) allocate(req *Request, commit bool) (CpuMask, map[string]CpuMask, error) {
	if _, ok := a.requests[req.ID()]; ok {
		return nil, nil, fmt.Errorf("%w: request #%s (%s) already exists", ErrAlreadyExists,
			req.ID(), req.Name())
	}

	req.pool.And(a.cpus)
	z := a.GetZone(req.pool)

	log.Debug("allocating from pool %s", req.pool)

	if !a.ZoneHasCapacity(z, req.exclusive, req.shared) {
		return nil, nil, fmt.Errorf("zone %s does not have free %d+%dm capacity",
			z.cpus.String(), req.exclusive, req.shared)
	}

	// For each existing zone o, we want to ensure that if z and o partially overlap
	// we do have the union, z U o, as a zone. This ensures that we'll make a proper
	// subsequent check to see if z and o has collectively enough resources for both
	// zones' requests.
	a.ForeachZone(func(o *Zone) bool {
		if z.PartialOverlap(o) {
			_ = a.GetZone(z.cpus.Union(o.cpus))
		}
		return true
	})

	z.assign(req)

	overflow := a.checkZoneUsage()
	if err := overflow.Error(); err != nil {
		z.unassign(req)
		return nil, nil, err
	}

	/*
		z.users[req.ID()] = req
		overcommit := map[string]int{}
		a.ForeachZone(func(o *Zone) bool {
			if free := a.ZoneSharedCapacity(o); free < 0 {
				overcommit[o.cpus.String()] = free
			}
			return true
		})

		if len(overcommit) > 0 {
			delete(z.users, req.ID())
			var err error
			for cpus, oc := range overcommit {
				err = fmt.Errorf("%w, %w", err, fmt.Errorf("zone %s lack %dm CPU capacity", cpus, -oc))
			}
			return nil, nil, fmt.Errorf("%w: %w", ErrNoCpu, err)
		}
	*/

	/*
		z.users[req.ID()] = req

		for _, oz := range a.zones {
			if a.ZoneSharedCapacity(oz) < 0 {
				delete(z.users, req.ID())
				return nil, nil, fmt.Errorf("zone %s does not have enough free capacity",
					oz.cpus.String())
			}
		}
	*/

	updates := make(map[string]CpuMask)
	isolated := false

	if req.exclusive > 0 {
		if req.exclusive == 1 && z.isolated.Size() >= 1 {
			isolated = true
			req.private, _ = a.picker.PickCpus(z.isolated, req.exclusive, commit)
		} else {
			req.private, _ = a.picker.PickCpus(z.shared, req.exclusive, commit)
		}

		// generate (and commit if necessary) updates for zones and requests affected
		for _, oz := range a.zones {
			if /*z == oz || */ z.IsDisjoint(oz) {
				continue
			}
			if isolated {
				if commit {
					oz.isolated.AndNot(req.private)
				}
			} else {
				if commit {
					oz.shared.AndNot(req.private)
				}
				for id, r := range oz.users {
					if !r.common.IsDisjoint(req.private) {
						if commit {
							updates[id] = r.common.AndNot(req.private).Clone()
						} else {
							updates[id] = r.common.Clone().AndNot(req.private)
						}
					}
				}
			}
		}
	}

	if req.shared > 0 {
		req.common = z.shared.Clone()
	}

	if !commit {
		z.unassign(req)
	} else {
		a.requests[req.ID()] = req
	}

	if len(updates) == 0 {
		updates = nil
	}

	return req.private.Union(req.common), updates, nil
}

func (a *Allocator) release(req *Request) (map[string]CpuMask, error) {
	delete(a.requests, req.ID())

	req.pool.And(a.cpus)
	z := a.GetZone(req.pool)
	z.unassign(req)

	if req.private.Size() == 0 {
		return nil, nil
	}

	updates := make(map[string]CpuMask)

	// update affected remaining allocations
	for _, o := range a.zones {
		if z.IsDisjoint(o) {
			continue
		}

		var (
			cpus     = o.cpus.Intersection(req.private)
			isolated = cpus.Intersection(a.isolated)
			normal   = cpus.Difference(isolated)
		)

		o.isolated.Or(isolated)

		if normal.Size() != 0 {
			o.shared.Or(normal)
			for orid, or := range o.users {
				if or.shared > 0 {
					or.common = o.shared.Clone()
					updates[orid] = o.shared.Union(or.private)
				}
			}
		}
	}

	if len(updates) == 0 {
		updates = nil
	}

	return updates, nil
}
