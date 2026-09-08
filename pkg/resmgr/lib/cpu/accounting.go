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
	"cmp"
	"fmt"
	"math"
	"slices"

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
//     other exclusive CPU sets.
type Accounting struct {
	cpus      *CpuMask // the bounding set of CPUs of the accounting
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
		cpus: cpus,
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

// AllCpus returns a copy of the bounding CPU set of the accounting. This is the set
// of CPUs that the accounting is aware of.
func (a *Accounting) AllCpus() *CpuMask {
	return a.cpus.Clone().(*CpuMask)
}

// SharedCpus returns a copy of the set of CPUs that are shared by at least one user.
func (a *Accounting) SharedCpus() *CpuMask {
	shared := NewCpuMask()
	for key, p := range a.pools {
		if len(p.users) > 0 {
			log.Debugf("- sharedCpus: pool %q: %s", key, p.cpus)
			shared = shared.Union(p.cpus).(*CpuMask)
		}
	}
	return shared
}

// ExclusiveCpus returns a copy of the set of CPUs that are allocated exclusively
// to a user.
func (a *Accounting) ExclusiveCpus() *CpuMask {
	return a.exclusive.Clone().(*CpuMask)
}

// UsedPools returns a map of all sets of CPUs with active users,
// keyed by the CPU set key.
func (a *Accounting) UsedPools() map[string]*CpuMask {
	pools := make(map[string]*CpuMask)
	for _, p := range a.pools {
		if len(p.users) > 0 && !p.cpus.IsEmpty() {
			pools[p.cpus.Key()] = p.cpus.Clone().(*CpuMask)
		}
	}
	return pools
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

	if both := a.SharedCpus().Intersection(usage.Exclusive()); !both.IsEmpty() {
		return fmt.Errorf("user %q: exclusive CPUs %s are allocated shared",
			id, both.String())
	}

	log.Debugf("+ add %q (%s) with %q shared +%q exclusive", usage.Name(), id,
		usage.Shared(), usage.Exclusive())

	user := &User{
		id:        id,
		name:      usage.Name(),
		exclusive: NewCpuMask(usage.Exclusive().UnsortedList()...),
		shared:    NewCpuMask(usage.Shared().UnsortedList()...),
		charge:    usage.Charge(),
	}

	a.insert(user)

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

	// A pool without users has no charge on it, so it can never be the
	// tightest limit of any set of CPUs. It would only add to the work of
	// enumerating implicit pools, and pools come and go as users are added,
	// updated and deleted. So drop it, but keep our bounding set: that one
	// is the full capacity we account for.
	if len(user.pool.users) == 0 && !user.pool.cpus.Equals(a.cpus) {
		delete(a.pools, user.pool.cpus.Key())
	}

	return nil
}

// Update updates the given usage. Currently it simply does an internal
// Delete() followed by an Add(). If the new usage is rejected, the old user
// is reinstated as it was.
func (a *Accounting) Update(usage Usage) error {
	old := a.users[usage.ID()]
	if err := a.Delete(usage.ID()); err != nil {
		log.Warnf("user %q not found", usage.ID())
	}

	err := a.Add(usage)
	if err != nil && old != nil {
		// Reinstate the old user as it was, without verifying it again. Add
		// can reject a usage which we already account for: allocating a CPU
		// exclusively does not stop the users of a pool containing that CPU
		// from sharing it, but such a pool is rejected for any new user.
		a.insert(old)
	}

	return err
}

// Allocate takes the given usage into account, provided it fits. Returns an
// error if Add rejects the usage, or if it would take any set of CPUs over
// its capacity. The accounting is left unchanged in either case.
func (a *Accounting) Allocate(usage Usage) error {
	if err := a.Add(usage); err != nil {
		return err
	}

	// The charge of a usage and its exclusive CPUs limit each other: both can
	// fit on their own, yet not together in a set of CPUs which contains both.
	// Checking them separately beforehand would miss that, so we take the
	// usage into account, then check whether we went over any limit, and undo
	// it if we did.
	if over := a.Overcommit(); over > 0 {
		if err := a.Delete(usage.ID()); err != nil {
			log.Errorf("failed to undo the allocation of user %q: %v", usage.ID(), err)
		}

		return fmt.Errorf("user %q: can't allocate %dm from %s with exclusive %s, "+
			"short of %dm", usage.ID(), usage.Charge(), usage.Shared(),
			usage.Exclusive(), over)
	}

	return nil
}

// Reallocate updates the given usage the way Update does, provided it fits.
// Returns an error if Update rejects the usage, or if it would take any set of
// CPUs over its capacity. The accounting is left as it was in either case.
//
// Just like Update, this takes a usage of a user we don't know yet, allocating
// it. And just like Allocate, it refuses to leave the accounting overcommitted,
// whether the usage it replaces had a part in that or not. So it cannot be used
// to reduce an existing overcommit: Update is the one which takes any usage.
func (a *Accounting) Reallocate(usage Usage) error {
	old := a.users[usage.ID()]

	// Note that a rejected Update reinstates the old user itself.
	if err := a.Update(usage); err != nil {
		return err
	}

	// The usage can both free and take capacity, and its charge and exclusive
	// CPUs limit each other, so there is no checking this beforehand. Update
	// first, then undo it if we went over any limit.
	if over := a.Overcommit(); over > 0 {
		if err := a.Delete(usage.ID()); err != nil {
			log.Errorf("failed to undo the reallocation of user %q: %v", usage.ID(), err)
		}
		if old != nil {
			// Reinstate the old user as it was, without verifying it again,
			// for the same reason Update does so.
			a.insert(old)
		}

		return fmt.Errorf("user %q: can't reallocate %dm from %s with exclusive %s, "+
			"short of %dm", usage.ID(), usage.Charge(), usage.Shared(),
			usage.Exclusive(), over)
	}

	return nil
}

// Release takes the given user out of the accounting, freeing its charge and
// its exclusive CPUs. Returns an error if the user does not exist.
func (a *Accounting) Release(id string) error {
	return a.Delete(id)
}

// CarveOut takes the given CPUs out of the shared CPUs of every user which has
// any of them, so that they can be allocated exclusively. It returns the usage
// of every user it altered, in order of ID, which the caller is expected to put
// into effect for those users.
//
// Returns an error without altering the accounting if any of the CPUs is
// already allocated exclusively, if a user would be left without any CPUs to
// take its charge from, or if the users of the shrunken pools would not fit
// into what is left of those pools.
func (a *Accounting) CarveOut(cpus *CpuMask) ([]Usage, error) {
	if taken := cpus.Intersection(a.exclusive); !taken.IsEmpty() {
		return nil, fmt.Errorf("can't carve out %s: CPUs %s are already allocated exclusively",
			cpus, taken)
	}

	var (
		users  []*User
		carved []*User
	)

	for _, u := range a.users {
		if !u.shared.Intersects(cpus) {
			continue
		}

		shared := u.shared.Difference(cpus).(*CpuMask)
		if shared.IsEmpty() && u.charge != 0 {
			return nil, fmt.Errorf("can't carve out %s: user %q (ID %q) would have no "+
				"CPUs left to take its charge of %dm from", cpus, u.name, u.id, u.charge)
		}

		users = append(users, u)
		carved = append(carved, &User{
			id:        u.id,
			name:      u.name,
			exclusive: u.exclusive.Clone().(*CpuMask),
			shared:    shared,
			charge:    u.charge,
		})
	}

	if len(carved) == 0 {
		return nil, nil
	}

	slices.SortFunc(carved, func(a, b *User) int { return cmp.Compare(a.id, b.id) })

	for _, u := range users {
		if err := a.Delete(u.id); err != nil {
			log.Errorf("failed to carve out %s of user %q: %v", cpus, u.id, err)
		}
	}
	for _, u := range carved {
		a.insert(u)
	}

	// Shrinking a pool takes capacity away from it, which its users might not
	// fit into any more. Undo the whole carve-out if that is the case.
	if over := a.Overcommit(); over > 0 {
		for _, u := range carved {
			if err := a.Delete(u.id); err != nil {
				log.Errorf("failed to undo carving out %s of user %q: %v", cpus, u.id, err)
			}
		}
		for _, u := range users {
			a.insert(u)
		}

		return nil, fmt.Errorf("can't carve out %s: the users of the shrunken pools "+
			"would be short of %dm", cpus, over)
	}

	usages := make([]Usage, 0, len(carved))
	for _, u := range carved {
		usages = append(usages, u.clone())
	}

	return usages, nil
}

// PutBack returns the given CPUs to the shared CPUs of the given users, the
// inverse of CarveOut. The usages serve to identify the users, by ID: the CPUs
// are added to the shared CPUs those users have in the accounting right now, so
// the usages CarveOut returned can be handed back as they are. It returns the
// usage of every user it altered, in order of ID, which the caller is expected
// to put into effect for those users.
//
// Returns an error without altering the accounting if any of the CPUs is still
// allocated exclusively, or if any of the users does not exist. Note that this
// can never leave the accounting overcommitted: giving CPUs back to a pool
// only ever adds capacity to it.
func (a *Accounting) PutBack(cpus *CpuMask, usages []Usage) ([]Usage, error) {
	if taken := cpus.Intersection(a.exclusive); !taken.IsEmpty() {
		return nil, fmt.Errorf("can't put back %s: CPUs %s are still allocated exclusively",
			cpus, taken)
	}

	var (
		users   []*User
		widened []*User
		seen    = map[string]struct{}{}
	)

	for _, usage := range usages {
		id := usage.ID()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}

		u, ok := a.users[id]
		if !ok {
			return nil, fmt.Errorf("can't put back %s: user %q not found", cpus, id)
		}

		if cpus.IsSubsetOf(u.shared) {
			continue
		}

		users = append(users, u)
		widened = append(widened, &User{
			id:        u.id,
			name:      u.name,
			exclusive: u.exclusive.Clone().(*CpuMask),
			shared:    u.shared.Union(cpus).(*CpuMask),
			charge:    u.charge,
		})
	}

	if len(widened) == 0 {
		return nil, nil
	}

	slices.SortFunc(widened, func(a, b *User) int { return cmp.Compare(a.id, b.id) })

	for _, u := range users {
		if err := a.Delete(u.id); err != nil {
			log.Errorf("failed to put back %s for user %q: %v", cpus, u.id, err)
		}
	}

	result := make([]Usage, 0, len(widened))
	for _, u := range widened {
		a.insert(u)
		result = append(result, u.clone())
	}

	return result, nil
}

// clone returns a copy of the user, for handing it out to a caller.
func (u *User) clone() *User {
	return &User{
		id:        u.id,
		name:      u.name,
		pool:      u.pool,
		exclusive: u.exclusive.Clone().(*CpuMask),
		shared:    u.shared.Clone().(*CpuMask),
		charge:    u.charge,
	}
}

// Available returns the amount of CPU capacity available in the given sets of
// CPUs, in milli-CPUs, where 1000 milli-CPUs equals one full CPU. It is not the
// free capacity, but the most a new user of those CPUs can take without
// exceeding the limit of any pool, explicit or implicit, which constrains them.
// Without arguments it reports the capacity available in the bounding set of
// CPUs of the accounting.
//
// Given several sets of CPUs it reports the tightest limit of all of them, what
// a new user which needs capacity from every one of those sets can take. Note
// that checking several sets in a single call is cheaper than a call per set,
// as it enumerates the implicit pools only once.
//
// The returned capacity is negative if a limit is already exceeded. Note that
// the opposite does not hold: a non-negative capacity does not prove that no
// pool is overcommitted, only that none of the ones limiting the given CPUs is.
func (a *Accounting) Available(cpus ...*CpuMask) int {
	if len(cpus) == 0 {
		cpus = []*CpuMask{a.cpus}
	}

	var (
		sets  = a.constraints()
		queue = slices.Clone(cpus)
		seen  = make(map[string]struct{}, len(cpus))
		limit = math.MaxInt
	)

	for _, c := range cpus {
		seen[c.Key()] = struct{}{}
	}

	// Every set of CPUs which fully contains one of the given ones limits how
	// much capacity a new user of those CPUs can take: the users which take
	// their allocation from within such a set can only ever run within it, so
	// their total charge together with the charge of the new user must fit
	// into the capacity of the set.
	//
	// Beside the explicit pools, the unions of intersecting pools are such
	// implicit pools, too. Therefore we start with the given sets and keep
	// growing them by unioning them with intersecting pools, checking the
	// limit of every distinct set we come up with.
	//
	// Unions of non-intersecting pools need no checking. Both their capacity
	// and their charge are the sum of those of the disjoint parts, so such a
	// union is at least as loose a limit as the tightest of its parts, as
	// long as no part is overcommitted. If one is, we might miss the tightest
	// limit and return an optimistic one.
	for len(queue) > 0 {
		pool := queue[0]
		queue = queue[1:]

		limit = min(limit, 1000*pool.Size()-a.charge(pool))

		if len(seen) >= maxImplicitPools {
			log.Warnf("hit implicit pool limit (%d), limit of %s "+
				"may be overestimated", maxImplicitPools, pool)
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

	return limit
}

// Overcommit returns the amount of milli-CPU by which the most overcommitted
// set of CPUs is over its capacity, or 0 if none is over.
//
// Without arguments it checks the whole accounting. Given some sets of CPUs it
// checks only those and the implicit pools they are part of, answering whether
// any of the limits which constrain those CPUs is exceeded. For a single set
// this is the same as the negative of Available, clamped to 0, but any number
// of sets are checked in a single enumeration of the implicit pools.
//
// Allocate, CarveOut and PutBack never leave the accounting overcommitted, but
// Add and Update take any usage which is otherwise valid, however much capacity
// it takes, so an accounting fed with those can be.
//
// For the whole accounting it is enough to check the sets the charged pools are
// part of. A set without a charged pool has nothing taken from it but
// exclusively allocated CPUs, and the charge of such a CPU is its full
// capacity, so such a set can never be over its own.
func (a *Accounting) Overcommit(cpus ...*CpuMask) int {
	if len(cpus) == 0 {
		for _, p := range a.pools {
			if !p.cpus.IsEmpty() && p.charge() > 0 {
				cpus = append(cpus, p.cpus)
			}
		}
	}

	if len(cpus) == 0 {
		return 0
	}

	return max(-a.Available(cpus...), 0)
}

// CollectOvercommits returns the amount of milli-CPU by which each of the
// given sets of CPUs is over its capacity, keyed by the Key() of the set.
// A set which is not over its capacity is reported as 0. Without arguments
// it reports every pool in use, the same sets of CPUs UsedPools returns.
//
// Note that unlike Overcommit, which finds the worst of the given sets in a
// single enumeration of the implicit pools, this one checks every set on its
// own, so it costs an enumeration per set.
func (a *Accounting) CollectOvercommits(cpus ...*CpuMask) map[string]int {
	if len(cpus) == 0 {
		for _, p := range a.pools {
			if len(p.users) > 0 && !p.cpus.IsEmpty() {
				cpus = append(cpus, p.cpus)
			}
		}
	}

	overcommit := make(map[string]int, len(cpus))
	for _, c := range cpus {
		overcommit[c.Key()] = a.Overcommit(c)
	}

	return overcommit
}

// insert takes the given user into account. The user must be verified, either
// by Add or by having been accounted for before.
func (a *Accounting) insert(user *User) {
	// Note that the pool of a user is identified by the very set of CPUs the
	// user takes its shared allocation from. All users of a pool must declare
	// the same set, otherwise each of them ends up in a pool of its own. Such
	// nearly identical pools all overlap each other without any of them being
	// a subset of another, which is the worst case for the implicit pool
	// enumeration in Available.
	user.pool = a.pool(user.shared)
	user.pool.users[user.id] = user
	a.users[user.id] = user
	a.exclusive = a.exclusive.Union(user.exclusive).(*CpuMask)
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

// charge returns the total charge of the users of the pool.
func (p *Pool) charge() int {
	charge := 0
	for _, u := range p.users {
		charge += u.charge
	}
	return charge
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
