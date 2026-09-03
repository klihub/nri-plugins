// Copyright The NRI Plugins Authors. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliaDGGnce with the License.
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
)

// Accounting implements a minimal CPU accounting scheme. It tries to avoid
// being too opinionated about how an actual CPU allocator should model the
// system. It does not imply any inherent hierachy or other CPU topology on
// an actual allocator. However, there are a few key assumptions. There are
// the following:
//  1. It is possible to make multiple shared allocations from a CPU set.
//  2. It is possible to allocate full CPUs exclusively.
//  3. A single allocation can take both shared and exclusive CPUs.
//  4. Exclusive CPUs are genuinely exclusive, i.e. exclusively allocated
//     CPUs are assumed to be absent from all shared CPU sets and all
//     other eclusive CPU sets.
type Accounting struct {
	pools     map[string]*Pool
	users     map[string]*User
	exclusive *CpuMask // all CPUs allocated exclusively by any user
}

// Pool is a set of CPUs with some associated CPU users.
type Pool struct {
	cpus  *CpuMask
	users map[string]*User
}

// User represents a single CPU user in a pool.
type User struct {
	id        string
	name      string
	pool      *Pool
	exclusive *CpuMask
	shared    *CpuMask
	charge    int
}

// Usage is our externally visible interface for CPU usage. It is kept
// separate from our internal User abstraction, so a client cannot alter
// CPU usage that we have already accounted for.
type Usage interface {
	// ID returns a unique identifier for this CPU usage.
	ID() string
	// Name returns a human-friendly name for this usage.
	Name() string
	// Exclusive returns the exclusively allocated CPUs for this usage.
	Exclusive() CPUSet
	// Shared returns the shared CPUs for this usage.
	Shared() CPUSet
	// Charge returns how much milli-CPU this usages takes from the shared CPUs.
	Charge() int
}

var (
	// maxImplicitPools is the maximum number of (implicit and explicit) pools
	// we check in Available. Enumerating pools is exponential in the number of
	// pools in the worst case, but the practical count should be small for real
	// CPU topologies and sensible allocation models. This is a safety limit for
	// pathological cases. Can be set with SetMaxImplicitPools().
	maxImplicitPools = 4096

	log = logger.Get("libcpu")
)

// SetMaxImplicitPools sets the maximum number of distinct CPU sets the accounting
// algorithm is willing to track for determining how much free capacity is present
// among any given set of CPU. Returns the previously set limit. Capped to >= 4096.
func SetMaxImplicitPools(limit int) int {
	if limit < 4096 {
		limit = 4096
	}

	previous := maxImplicitPools
	maxImplicitPools = limit

	return previous
}

// NewAccounting creates a new accounting instance for the given bounding
// CPU set. The bounding set is currently unused but this might change in
// the future. Don't assume that Add() will succeed with some CPUs absent
// from cpus.
func NewAccounting(cpus *CpuMask) *Accounting {
	cpus = cpus.Clone().(*CpuMask)
	return &Accounting{
		pools: map[string]*Pool{
			cpus.Key(): {
				cpus:  cpus,
				users: map[string]*User{},
			},
		},
		users:     map[string]*User{},
		exclusive: NewCpuMask(),
	}
}

// Add the given usage to the accounting. Returns an error if the user already
// exists, if its shared and exclusive CPUs overlap, if it has a charge without
// any shared CPUs, or if any of its exclusive CPUs is already allocated
// exclusively to another user.
func (a *Accounting) Add(usage Usage) error {
	id, name := usage.ID(), usage.Name()
	if id == "" {
		return fmt.Errorf("user: invalid empty ID")
	}
	if name == "" {
		return fmt.Errorf("user %q: invalid empty name invalid", id)
	}

	if _, exists := a.users[id]; exists {
		return fmt.Errorf("user %q already exists (ID %q)", name, id)
	}

	if both := usage.Shared().Intersection(usage.Exclusive()); !both.IsEmpty() {
		return fmt.Errorf("user %q: CPUs %s are both shared and exclusive",
			id, both.String())
	}

	if usage.Charge() != 0 && usage.Shared().IsEmpty() {
		return fmt.Errorf("user %q: charge of %d without any shared CPUs",
			id, usage.Charge())
	}

	if taken := usage.Exclusive().Intersection(a.exclusive); !taken.IsEmpty() {
		return fmt.Errorf("user %q: CPUs %s are already allocated exclusively",
			id, taken.String())
	}

	if both := usage.Shared().Intersection(a.exclusive); !both.IsEmpty() {
		return fmt.Errorf("user %q: shared CPUs %s are already allocated exclusively",
			id, both.String())
	}

	user := &User{
		id:        id,
		name:      usage.Name(),
		exclusive: NewCpuMask(usage.Exclusive().UnsortedList()...),
		shared:    NewCpuMask(usage.Shared().UnsortedList()...),
		charge:    usage.Charge(),
	}

	// Note that the pool of a user is identified by the very set of CPUs the
	// user takes its shared allocation from. All users of a pool must declare
	// the same set, otherwise each of them ends up in a pool of its own. Such
	// nearly identical pools all overlap each other without any of them being
	// a subset of another, which is the worst case for the implicit pool
	// enumeration in Available.
	user.pool = a.pool(usage.Shared())
	user.pool.users[user.id] = user
	a.users[user.id] = user
	a.exclusive = a.exclusive.Union(user.exclusive).(*CpuMask)

	return nil
}

// Delete the given user from the accounting. Returns an error if the user does not exist.
func (a *Accounting) Delete(id string) error {
	user, exists := a.users[id]
	if !exists {
		return fmt.Errorf("user %q not found", id)
	}
	delete(a.users, id)
	delete(user.pool.users, id)
	a.exclusive = a.exclusive.Difference(user.exclusive).(*CpuMask)

	return nil
}

// Update updates the given usage. Currently it simply does an internal
// Delete() followed by an Add().
func (a *Accounting) Update(usage Usage) error {
	old := a.users[usage.ID()]
	if err := a.Delete(usage.ID()); err != nil {
		log.Warnf("user %q not found", usage.ID())
	}

	err := a.Add(usage)
	if err != nil && old != nil {
		if err := a.Add(old); err != nil {
			log.Warnf("internal update error, failed to reinstate user %q (%q): %v",
				old.name, old.id, err)
		}
	}

	return err
}

// Available returns the amount of available CPU capacity in the given CPU set.
// The capacity is measured in milli-CPUs, where 1000 milli-CPUs equals 1 CPU.
// It is not the free capacity, but the maximum capacity that can be allocated
// to a new user without exceeding the limits of any of the existing pools.
//
// The returned capacity is negative if a limit is already exceeded. Note that
// the opposite does not hold: a non-negative capacity does not prove that no
// pool is overcommitted, only that none of the ones limiting cpus is.
func (a *Accounting) Available(cpus *CpuMask) int {
	var (
		sets  = a.constraints()
		queue = []*CpuMask{cpus}
		seen  = map[string]struct{}{cpus.Key(): {}}
		avail = 1000 * cpus.Size()
	)

	// Every set of CPUs which fully contains cpus limits how much capacity a
	// new user of cpus can take: the users which take their allocation from
	// within such a set can only ever run within it, so their total charge
	// together with the charge of the new user must fit into the capacity of
	// the set.
	//
	// Beside the explicit pools, the unions of intersecting pools are such
	// implicit pools, too. Therefore we start with cpus itself and keep
	// growing it by unioning it with intersecting pools, checking the limit
	// of every distinct set we come up with.
	//
	// Unions of non-intersecting pools need no checking. Both their capacity
	// and their charge are the sum of those of the disjoint parts, so such a
	// union is at least as loose a limit as the part which contains cpus, as
	// long as no part is overcommitted. If one is, we might miss the tightest
	// limit and return an optimistic capacity.
	for len(queue) > 0 {
		pool := queue[0]
		queue = queue[1:]

		avail = min(avail, 1000*pool.Size()-a.charge(pool))

		if len(seen) >= maxImplicitPools {
			log.Warnf("hit implicit pool limit (%d), available capacity for %s "+
				"may be overestimated", maxImplicitPools, cpus)
			break
		}

		for _, s := range sets {
			if s.IsSubsetOf(pool) || !s.Intersects(pool) {
				continue
			}

			union := pool.Union(s).(*CpuMask)
			key := union.Key()
			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}
			queue = append(queue, union)
		}
	}

	return avail
}

// charge returns the capacity taken from the given set of CPUs: the full
// capacity of the exclusively allocated CPUs among them, plus the charge
// of all the users which take their shared allocation from within them.
func (a *Accounting) charge(cpus *CpuMask) int {
	charge := 1000 * cpus.Intersection(a.exclusive).Size()

	for _, p := range a.pools {
		// Users of the empty pool have no shared CPUs, so Add rejects any
		// charge from them. They only take the exclusive CPUs accounted
		// for above.
		if p.cpus.IsEmpty() || !p.cpus.IsSubsetOf(cpus) {
			continue
		}
		for _, u := range p.users {
			charge += u.charge
		}
	}

	return charge
}

// pool returns the pool for the given set of CPUs, creating it if necessary.
func (a *Accounting) pool(cpus CPUSet) *Pool {
	mask := NewCpuMask(cpus.UnsortedList()...)

	pool, ok := a.pools[mask.Key()]
	if !ok {
		pool = &Pool{
			cpus:  mask,
			users: map[string]*User{},
		}
		a.pools[mask.Key()] = pool
	}

	return pool
}

// constraints returns the distinct sets of CPUs which limit capacity, the
// CPUs of all the pools. Exclusively allocated CPUs need no sets of their
// own: the charge of such a CPU is its full capacity, so adding one to a
// set never tightens the limit of that set.
func (a *Accounting) constraints() []*CpuMask {
	sets := make([]*CpuMask, 0, len(a.pools))

	// Pools are keyed by their CPUs, so they are distinct by construction.
	for _, p := range a.pools {
		if p.cpus.IsEmpty() {
			continue
		}
		sets = append(sets, p.cpus)
	}

	return sets
}

func (u *User) ID() string {
	return u.id
}

func (u *User) Name() string {
	return u.name
}

func (u *User) Exclusive() CPUSet {
	return u.exclusive
}

func (u *User) Shared() CPUSet {
	return u.shared
}

func (u *User) Charge() int {
	return u.charge
}
