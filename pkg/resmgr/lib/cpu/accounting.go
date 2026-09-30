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
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"

	logger "github.com/containers/nri-plugins/pkg/log"
)

// Accounting is a CPU capacity accounting. The package documentation
// describes its model. It takes no locks; callers serialise concurrent use.
type Accounting struct {
	cpus      *CpuMask // bounding CPU set of the accounting
	pools     map[string]*pool
	users     map[string]*user
	exclusive *CpuMask // CPUs allocated exclusively by any user
	version   int64    // bumped on every change, which expires offers
}

// pool is a declared set of CPUs and its users.
type pool struct {
	cpus      *CpuMask // declared set; pool identity, never changes
	effective *CpuMask // cpus \ a.exclusive; nil while equal to cpus
	users     map[string]*user
}

// user represents a single CPU user in a pool.
type user struct {
	id        string
	name      string
	pool      *pool
	exclusive *CpuMask
	shared    *CpuMask
	charge    int
}

var (
	// maxImplicitPools caps the sets one traversal visits. Set it with
	// SetMaxImplicitPools().
	maxImplicitPools = 4096

	log = logger.Get("libcpu")
)

// SetMaxImplicitPools sets the most distinct sets of CPUs one traversal of
// [Accounting.Available] or [Accounting.AvailableEach] visits before it stops
// and reports its answers inexact. It returns the previous limit. A limit
// below 4096 is raised to 4096.
func SetMaxImplicitPools(limit int) int {
	if limit < 4096 {
		limit = 4096
	}

	previous := maxImplicitPools
	maxImplicitPools = limit

	return previous
}

// NewAccounting creates an accounting for the given bounding set of CPUs, its
// full capacity. A usage taking CPUs outside it exclusively is refused. The set
// is also a pool, so it constrains every set of CPUs within it.
func NewAccounting(cpus *CpuMask) *Accounting {
	// Cloned and sealed: the identity of the bounding pool never changes.
	cpus = cpus.Clone()
	cpus.Seal()

	return &Accounting{
		cpus: cpus,
		pools: map[string]*pool{
			cpus.Key(): {
				cpus:  cpus,
				users: map[string]*user{},
			},
		},
		users:     map[string]*user{},
		exclusive: sealed(NewCpuMask()),
	}
}

// AllCpus returns a copy of the bounding CPU set of the accounting.
func (a *Accounting) AllCpus() *CpuMask {
	return a.cpus.Clone()
}

// SharedCpus returns a copy of the set of CPUs shared by at least one user. A
// CPU allocated exclusively is absent, however many pools in use declare it.
func (a *Accounting) SharedCpus() *CpuMask {
	shared := NewCpuMask()
	for key, p := range a.pools {
		if len(p.users) > 0 {
			log.Debugf("- sharedCpus: pool %q: %s", key, p.eff())
			shared = shared.Union(p.eff())
		}
	}
	return shared
}

// ExclusiveCpus returns a copy of the set of CPUs allocated exclusively to a
// user.
func (a *Accounting) ExclusiveCpus() *CpuMask {
	return a.exclusive.Clone()
}

// UsedPools returns the non-empty effective sets of pools with users, keyed by
// CPU set key. Pools with the same effective set share one entry.
func (a *Accounting) UsedPools() map[string]*CpuMask {
	pools := make(map[string]*CpuMask)
	for _, p := range a.pools {
		if len(p.users) > 0 && !p.eff().IsEmpty() {
			pools[p.eff().Key()] = p.eff().Clone()
		}
	}
	return pools
}

// insert takes the given usage into account without checking capacity. It
// fails if validate refuses the usage. Allocate is the checked path.
func (a *Accounting) insert(usage *CpuUsage) error {
	u, err := a.validate(usage, "")
	if err != nil {
		return err
	}

	log.Debugf("+ insert %q (%s) with %q shared +%q exclusive", u.name, u.id,
		u.shared, u.exclusive)

	a.account(u)

	return nil
}

// errNilUsage is what every entry point answers a nil usage.
var errNilUsage = errors.New("user: nil usage")

// validate checks the usage for well-formedness, not capacity, and returns
// the user it would be accounted as. Nothing is mutated.
//
// replacingID names the user this usage replaces, if any. Its exclusive CPUs
// are up for grabs, and choking it does not matter.
func (a *Accounting) validate(usage *CpuUsage, replacingID string) (*user, error) {
	if usage == nil {
		return nil, errNilUsage
	}

	id, name := usage.ID, usage.Name
	if id == "" {
		return nil, fmt.Errorf("user: invalid empty ID")
	}
	if name == "" {
		return nil, fmt.Errorf("user %q: invalid empty name", id)
	}

	if _, exists := a.users[id]; exists && id != replacingID {
		return nil, fmt.Errorf("user %q already exists (ID %q)", name, id)
	}

	if usage.Charge != 0 && usage.Shared.IsEmpty() {
		return nil, fmt.Errorf("user %q: charge of %d without any shared CPUs",
			id, usage.Charge)
	}

	if usage.Charge == 0 && !usage.Exclusive.IsEmpty() && !usage.Shared.IsEmpty() {
		return nil, fmt.Errorf("user %q: mixed allocation with 0 charge", id)
	}

	if usage.Charge < 0 {
		return nil, fmt.Errorf("user %q: negative charge %d", id, usage.Charge)
	}

	// Cloned so the caller cannot alter what we account for, and sealed so
	// we cannot either. Not EmptyIfNil: it returns the caller's own mask.
	exclusive := usage.Exclusive.Clone()
	exclusive.Seal()
	if outside := exclusive.Difference(a.cpus); !outside.IsEmpty() {
		return nil, fmt.Errorf("user %q: exclusive CPUs %s are outside the accounting",
			id, outside)
	}

	if taken := exclusive.Intersection(a.allocated(replacingID)); !taken.IsEmpty() {
		return nil, fmt.Errorf("user %q: CPUs %s are already allocated exclusively",
			id, taken.String())
	}

	shared := usage.Shared.Clone()
	shared.Seal()
	if err := a.wouldChoke(shared, exclusive, replacingID); err != nil {
		return nil, fmt.Errorf("user %q: %w", id, err)
	}

	return &user{
		id:        id,
		name:      name,
		exclusive: exclusive,
		shared:    shared,
		charge:    usage.Charge,
	}, nil
}

// allocated returns the CPUs allocated exclusively to anyone but the user
// exemptID, or all of them if there is no such user.
func (a *Accounting) allocated(exemptID string) *CpuMask {
	if exempt, ok := a.users[exemptID]; ok {
		return a.exclusive.Difference(exempt.exclusive)
	}

	return a.exclusive
}

// remove takes the given user out of the accounting. It returns the pools
// whose effective set widened, and an error if the user does not exist.
func (a *Accounting) remove(id string) ([]*pool, error) {
	user, exists := a.users[id]
	if !exists {
		return nil, fmt.Errorf("user %q not found", id)
	}
	delete(a.users, id)
	delete(user.pool.users, id)
	a.exclusive = sealed(a.exclusive.Difference(user.exclusive))
	altered := a.refreshEffective(user.exclusive)
	a.invalidateOffers()

	// Drop a pool with no users: it has no charge, so it never limits a
	// set, and would only slow the enumeration. Keep the bounding pool.
	if len(user.pool.users) == 0 && !user.pool.cpus.Equals(a.cpus) {
		delete(a.pools, user.pool.cpus.Key())
	}

	return altered, nil
}

// update replaces the user's usage by remove and insert, without checking
// capacity. If insert refuses the new usage, the old user is reinstated.
func (a *Accounting) update(usage *CpuUsage) error {
	if usage == nil {
		return errNilUsage
	}

	old := a.users[usage.ID]
	if _, err := a.remove(usage.ID); err != nil {
		log.Warnf("user %q not found", usage.ID)
	}

	err := a.insert(usage)
	if err != nil && old != nil {
		// Reinstate unvalidated: it was valid, and nothing accepted
		// since can take its exclusive CPUs or choke its pool.
		a.account(old)
	}

	return err
}

// Allocate takes the given usage into account if it fits, by committing an
// offer for it. It returns the CPUs the requester must be pinned to, and the
// other users which must be re-pinned because their pool narrowed. The caller
// must put those re-pinnings into effect.
//
// It fails if the usage is malformed, if the user already exists, if it would
// leave a user which declared shared CPUs with none to run on, or if it would
// take a set of CPUs over capacity. The last error wraps a *CapacityError. A
// refused usage leaves the accounting untouched.
func (a *Accounting) Allocate(usage *CpuUsage) (*CpuMask, []Change, error) {
	if usage == nil {
		return nil, nil, errNilUsage
	}

	// Allocate means a new user; GetOffer would make this a reallocation.
	if _, exists := a.users[usage.ID]; exists {
		return nil, nil, fmt.Errorf("user %q already exists (ID %q)", usage.Name, usage.ID)
	}

	o, err := a.GetOffer(usage)
	if err != nil {
		return nil, nil, err
	}

	return o.Commit()
}

// Reallocate replaces the given user's usage with a new one if it fits, or
// allocates it if there is no such user. The usage is a complete new
// declaration, not a delta. It returns the CPUs the requester must be pinned
// to, and the other users which must be re-pinned because their pool narrowed
// or widened.
//
// It fails if the usage is malformed or asks for CPUs somebody else holds
// exclusively, if it would choke a user which declared shared CPUs, or if it
// would take a set of CPUs over capacity. The last error wraps a
// *CapacityError. A refused usage leaves the accounting untouched, the old
// user included. It refuses to leave the accounting overcommitted even if the
// old usage caused that, so it cannot reduce an existing overcommit.
//
// It is [Accounting.GetOffer] plus [Offer.Commit]. Take an offer instead to
// see what a redeclaration would cost before committing to it.
func (a *Accounting) Reallocate(usage *CpuUsage) (*CpuMask, []Change, error) {
	o, err := a.GetOffer(usage)
	if err != nil {
		return nil, nil, err
	}

	return o.Commit()
}

// Release takes the given user out of the accounting, freeing its charge and
// its exclusive CPUs, which return to every pool declaring them. It returns
// the users which must be re-pinned because their pool widened, and an error
// if the user does not exist.
func (a *Accounting) Release(id string) ([]Change, error) {
	altered, err := a.remove(id)
	if err != nil {
		return nil, err
	}

	return changesIn(altered), nil
}

// invalidateOffers expires every outstanding offer. account and remove call
// it on every change of state.
func (a *Accounting) invalidateOffers() {
	a.version++
}

// wouldChoke returns an error if a usage declaring shared and taking exclusive
// would leave a user which declared shared CPUs, itself included, with none to
// run on. skipID is the user being replaced, if any: it is not protected, and
// its exclusive CPUs count as free.
func (a *Accounting) wouldChoke(shared, exclusive *CpuMask, skipID string) error {
	allocated := a.allocated(skipID)

	// The usage itself: its declared CPUs minus every exclusive one.
	if !shared.IsEmpty() && shared.Difference(allocated).Difference(exclusive).IsEmpty() {
		return fmt.Errorf("shared CPUs %s: none of them would be left to run on, "+
			"with CPUs %s allocated exclusively", shared,
			shared.Intersection(allocated.Union(exclusive)))
	}

	if exclusive.IsEmpty() {
		return nil
	}

	// Everyone else. Not p.eff(): that still excludes the CPUs of the user
	// being replaced.
	for _, p := range a.pools {
		if p.cpus.IsEmpty() || len(p.users) == 0 {
			continue
		}
		if !p.cpus.Difference(allocated).Difference(exclusive).IsEmpty() {
			continue
		}
		for _, u := range p.users {
			if u.id != skipID {
				return fmt.Errorf("CPUs %s: user %q (ID %q) would have no CPUs "+
					"left to run on", exclusive, u.name, u.id)
			}
		}
	}

	return nil
}

// Charge returns the capacity taken from the given CPUs, in milli-CPU: the
// full capacity of the exclusive ones among them, plus the charge of every
// pool whose users can only run within them. The CPUs need not be a pool.
func (a *Accounting) Charge(cpus *CpuMask) int {
	return a.charge(cpus)
}

// DirectCharge returns the capacity taken directly from the given pool's CPUs,
// without the charges of pools nested inside it, and whether those CPUs are a
// pool at all; a set which is not reports 0 and false. It is a diagnostic, and
// cannot answer whether an allocation fits. [Accounting.Limit] and
// [Accounting.Available] can.
func (a *Accounting) DirectCharge(pool *CpuMask) (int, bool) {
	p, ok := a.pools[pool.Key()]
	if !ok {
		return 0, false
	}

	return 1000*p.cpus.Intersection(a.exclusive).Size() + p.charge(), true
}

// Limit returns the milli-CPU unclaimed within the given CPUs, ignoring every
// larger set which also constrains them. It is always exact.
// [Accounting.Available] never exceeds it, so it never wrongly refuses, but it
// cannot confirm.
func (a *Accounting) Limit(cpus *CpuMask) int {
	return 1000*cpus.Size() - a.charge(cpus)
}

// Available returns the most milli-CPU a new user of the given CPUs can take
// without exceeding the limit of any pool, explicit or implicit, which
// constrains them, and whether the answer is exact. It is negative if a limit
// is already exceeded. A non-negative answer proves only that no set limiting
// these CPUs is overcommitted.
//
// Probe the exact set a usage will declare; a superset is unsafe. The package
// documentation gives the rules.
func (a *Accounting) Available(cpus *CpuMask) (int, bool) {
	each, exact := a.AvailableEach(cpus)
	return each[0], exact
}

// AvailableEach returns [Accounting.Available] for each given set of CPUs,
// positionally, from one traversal, and whether the answers are exact. It is
// not their minimum. If not exact, the traversal was cut short at
// maxImplicitPools and every value is an upper bound. Without arguments it
// returns no values and true.
func (a *Accounting) AvailableEach(cpus ...*CpuMask) ([]int, bool) {
	if len(cpus) == 0 {
		return nil, true
	}

	// One snapshot serves both growth and the charge of every set.
	pools := a.charged()

	return traverse(cpus, distinct(pools, false), chargeIn(a.exclusive, pools))
}

// traverse returns the tightest limit of the sets of CPUs containing each
// probe, positionally, growing the probes along the intersecting sets of grow.
// charge gives the capacity taken from a set. The bool is false if the
// enumeration was cut short at maxImplicitPools; then every limit is an upper
// bound.
func traverse(probes, grow []*CpuMask, charge func(*CpuMask) int) ([]int, bool) {
	var (
		queue  = slices.Clone(probes)
		seen   = make(map[string]struct{}, len(probes))
		limits = make([]int, len(probes))
		exact  = true
	)

	for _, p := range probes {
		seen[p.Key()] = struct{}{}
	}

	// With one probe, every visited set contains it: skip the subset test.
	single := len(probes) == 1

	// Seed each answer with the probe's own limit. A cutoff part way down
	// the probe list would otherwise leave later probes at math.MaxInt, and
	// break the promise that no answer exceeds Limit. A single probe is
	// dequeued before any cutoff, so it needs no seed.
	if single {
		limits[0] = math.MaxInt
	} else {
		for i, p := range probes {
			limits[i] = 1000*p.Size() - charge(p)
		}
	}

	for len(queue) > 0 {
		set := queue[0]
		queue = queue[1:]

		limit := 1000*set.Size() - charge(set)
		if single {
			limits[0] = min(limits[0], limit)
		} else {
			for i, probe := range probes {
				if probe.IsSubsetOf(set) {
					limits[i] = min(limits[i], limit)
				}
			}
		}

		if len(seen) >= maxImplicitPools {
			log.Warnf("enumeration cut short at %d sets of CPUs while growing %s: "+
				"the limits reported are upper bounds, not exact",
				maxImplicitPools, set)
			exact = false
			break
		}

		for _, s := range grow {
			// Skip sets inside this one or disjoint from it. Neither
			// is tighter unless a part is overcommitted.
			if s.IsSubsetOf(set) || !s.Intersects(set) {
				continue
			}

			union := set.Union(s)
			key := union.Key()
			if _, ok := seen[key]; ok {
				continue
			}

			seen[key] = struct{}{}
			queue = append(queue, union)
		}
	}

	return limits, exact
}

// overcommit returns the milli-CPU by which the most overcommitted set of
// CPUs is over capacity, or 0, and whether that is exact. Without arguments
// it checks the whole accounting; otherwise only the given sets and the
// implicit pools containing them, in one enumeration. Only insert and update
// can overcommit.
//
// The whole-accounting check seeds with the effective sets of charged pools,
// since a set without a charged pool cannot be over. Declared sets would give
// the same answer, as an exclusive CPU adds 1000 of capacity and 1000 of
// charge, but effective sets deduplicate.
func (a *Accounting) overcommit(cpus ...*CpuMask) (int, bool) {
	if len(cpus) == 0 {
		cpus = distinct(a.charged(), true)
	}

	if len(cpus) == 0 {
		return 0, true
	}

	limits, exact := a.AvailableEach(cpus...)

	return max(-slices.Min(limits), 0), exact
}

// collectOvercommits returns how far each given set of CPUs is over capacity,
// keyed by Key(), with 0 for a set within it. Without arguments it reports
// every pool in use, keyed by its declared CPUs. It costs one enumeration per
// set.
func (a *Accounting) collectOvercommits(cpus ...*CpuMask) map[string]int {
	if len(cpus) == 0 {
		// Keyed by declared CPUs, checked on effective ones: both
		// have the same limit, as overcommit explains.
		overcommit := make(map[string]int, len(a.pools))
		for _, p := range a.pools {
			if len(p.users) > 0 && !p.cpus.IsEmpty() {
				overcommit[p.cpus.Key()], _ = a.overcommit(p.eff())
			}
		}

		return overcommit
	}

	overcommit := make(map[string]int, len(cpus))
	for _, c := range cpus {
		overcommit[c.Key()], _ = a.overcommit(c)
	}

	return overcommit
}

// account takes the given user into account. The user must be verified, either
// by validate or by having been accounted for before.
func (a *Accounting) account(u *user) {
	// poolFor uses a.exclusive as it stands, so the union and the refresh
	// must follow it.
	u.pool = a.poolFor(u.shared)
	u.pool.users[u.id] = u
	a.users[u.id] = u

	a.exclusive = sealed(a.exclusive.Union(u.exclusive))
	a.refreshEffective(u.exclusive)
	a.invalidateOffers()
}

// poolFor returns the pool for the given set of CPUs, creating it if needed.
// A new pool keeps cpus as its identity, so cpus must be sealed and unshared,
// as validate makes it.
func (a *Accounting) poolFor(cpus *CpuMask) *pool {
	key := cpus.Key()

	p, ok := a.pools[key]
	if !ok {
		p = &pool{cpus: cpus, users: map[string]*user{}}
		p.setEff(a.exclusive)
		a.pools[key] = p
	}

	return p
}

// charge returns the total charge of the users of the pool.
func (p *pool) charge() int {
	charge := 0
	for _, u := range p.users {
		charge += u.charge
	}
	return charge
}

// chargedSet is a pool's effective set and the total charge of its users.
// The accounting and a hypothetical both reduce to these.
type chargedSet struct {
	cpus   *CpuMask
	charge int
}

// charged returns the charged set of every pool of the accounting.
func (a *Accounting) charged() []chargedSet {
	sets := make([]chargedSet, 0, len(a.pools))

	for _, p := range a.pools {
		sets = append(sets, chargedSet{cpus: p.eff(), charge: p.charge()})
	}

	return sets
}

// chargeIn returns a function giving the capacity taken from a set of CPUs:
// 1000 per exclusive CPU in it, plus the charge of every given set within it.
// The empty set is skipped: it is a subset of every set, and the choking rule
// leaves it only to uncharged pools.
func chargeIn(exclusive *CpuMask, sets []chargedSet) func(*CpuMask) int {
	return func(cpus *CpuMask) int {
		charge := 1000 * cpus.Intersection(exclusive).Size()

		for _, s := range sets {
			if s.cpus.IsEmpty() || !s.cpus.IsSubsetOf(cpus) {
				continue
			}
			charge += s.charge
		}

		return charge
	}
}

// distinct returns the distinct non-empty sets among the given ones, only
// charged ones if chargedOnly is set, ordered by size and then by key. The
// order is for determinism: it decides which sets survive a cutoff.
func distinct(sets []chargedSet, chargedOnly bool) []*CpuMask {
	cpus := make([]*CpuMask, 0, len(sets))
	seen := make(map[string]struct{}, len(sets))

	for _, s := range sets {
		if s.cpus.IsEmpty() || (chargedOnly && s.charge <= 0) {
			continue
		}
		if _, ok := seen[s.cpus.Key()]; ok {
			continue
		}
		seen[s.cpus.Key()] = struct{}{}
		cpus = append(cpus, s.cpus)
	}

	slices.SortFunc(cpus, func(a, b *CpuMask) int {
		if d := cmp.Compare(a.Size(), b.Size()); d != 0 {
			return d
		}
		return cmp.Compare(a.Key(), b.Key())
	})

	return cpus
}

// charge returns the capacity taken from the given set of CPUs; see chargeIn.
func (a *Accounting) charge(cpus *CpuMask) int {
	return chargeIn(a.exclusive, a.charged())(cpus)
}

// constraints returns the distinct sets of CPUs which limit capacity: the
// deduplicated effective CPUs of all pools.
func (a *Accounting) constraints() []*CpuMask {
	return distinct(a.charged(), false)
}

// sealed seals and returns the mask. The accounting never alters a mask in
// place, and sealing turns any attempt into a panic. Nil cannot be sealed.
func sealed(cpus *CpuMask) *CpuMask {
	cpus.Seal()

	return cpus
}

// eff returns the CPUs this pool's users can actually run on.
func (p *pool) eff() *CpuMask {
	if p.effective == nil {
		return p.cpus
	}
	return p.effective
}

// setEff recomputes the effective set against the given exclusive CPUs,
// reporting whether it changed. It stays nil while equal to cpus, so
// nothing is allocated until CPUs actually go exclusive.
func (p *pool) setEff(exclusive *CpuMask) bool {
	var eff *CpuMask
	if p.cpus.Intersects(exclusive) {
		eff = sealed(p.cpus.Difference(exclusive))
	}

	was := p.eff()
	p.effective = eff
	return !was.Equals(p.eff())
}

// refreshEffective recomputes the effective set of every pool whose declared
// CPUs meet the changed ones, and returns the pools which changed. It is the
// only place keeping a.exclusive and pool.effective consistent.
func (a *Accounting) refreshEffective(changed *CpuMask) []*pool {
	var altered []*pool

	for _, p := range a.pools {
		if !p.cpus.Intersects(changed) {
			continue
		}
		if p.setEff(a.exclusive) {
			altered = append(altered, p)
		}
	}

	return altered
}
