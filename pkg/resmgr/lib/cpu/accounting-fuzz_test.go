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
	"errors"
	"fmt"
	"io"
	"maps"
	"math/bits"
	"math/rand"
	"slices"
	"strings"
	"testing"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/stretchr/testify/require"
)

const (
	// fuzzCpuCount is the number of CPUs of the fuzzed accounting. The
	// model uses 64-bit masks, so 64 is the limit.
	fuzzCpuCount = 64
	// fuzzAllCpus is the set of all our CPUs.
	fuzzAllCpus = ^uint64(0) >> (64 - fuzzCpuCount)
	// fuzzSteps is the number of operations performed for a single seed.
	fuzzSteps = 128
)

// fuzzPools are the pools users take shared CPUs from: eight NUMA nodes,
// four dies, two sockets, the full system, and two pseudo-pools which
// overlap nodes without being a subset of any. Overlapping pools multiply
// the unions the model enumerates; nested ones keep them few.
var fuzzPools = []uint64{
	0x00000000000000ff, // 0-7, node #0
	0x000000000000ff00, // 8-15, node #1
	0x0000000000ff0000, // 16-23, node #2
	0x00000000ff000000, // 24-31, node #3
	0x000000ff00000000, // 32-39, node #4
	0x0000ff0000000000, // 40-47, node #5
	0x00ff000000000000, // 48-55, node #6
	0xff00000000000000, // 56-63, node #7
	0x000000000000ffff, // 0-15, die #0 of socket #0
	0x00000000ffff0000, // 16-31, die #1 of socket #0
	0x0000ffff00000000, // 32-47, die #0 of socket #1
	0xffff000000000000, // 48-63, die #1 of socket #1
	0x00000000ffffffff, // 0-31, socket #0
	0xffffffff00000000, // 32-63, socket #1
	0xffffffffffffffff, // 0-63, all CPUs
	0x0000000000000ff0, // 4-11, across nodes #0 and #1
	0x0000000ff0000000, // 28-35, across the sockets
}

// fuzzProbes are the sets of CPUs whose available capacity we check.
var fuzzProbes = []uint64{
	0x0000000000000001, // 0
	0x0000000000000010, // 4
	0x00000000000000ff, // 0-7, a node
	0x0000000000000ff0, // 4-11, a pseudo-pool
	0x0000000ff0000000, // 28-35, the pseudo-pool across the sockets
	0x000000000000ffff, // 0-15, a die
	0x00000000ffffffff, // 0-31, a socket
	0xffffffffffffffff, // 0-63, all CPUs
	0x8000000000000001, // 0,63, two distant CPUs
}

// fuzzUser is a single CPU usage in our reference model.
type fuzzUser struct {
	shared    uint64
	exclusive uint64
	charge    int
}

// fuzzModel is a brute-force reference model of the accounting. CPU sets
// are 64-bit masks. Available capacity is the tightest limit among all
// unions of charged pools, enumerated outright without the accounting's
// growth rules. A model taking the same shortcuts would agree with a
// wrong implementation.
//
// The model keys pools by their EFFECTIVE sets. Keyed on declared sets, it
// would miss the charge of a pool whose CPUs went exclusive, and agree
// with an accounting which missed it too.
type fuzzModel struct {
	users map[string]*fuzzUser
}

func (m *fuzzModel) clone() *fuzzModel {
	c := &fuzzModel{users: make(map[string]*fuzzUser, len(m.users))}
	for id, u := range m.users {
		c.users[id] = u
	}
	return c
}

// exclusiveCpus returns all the exclusively allocated CPUs.
func (m *fuzzModel) exclusiveCpus() uint64 {
	cpus := uint64(0)
	for _, u := range m.users {
		cpus |= u.exclusive
	}
	return cpus
}

// otherExclusive returns the CPUs held exclusively by users other than
// except. A reallocation drops the user first, so its CPUs are free.
func (m *fuzzModel) otherExclusive(except string) uint64 {
	exclusive := uint64(0)

	for id, u := range m.users {
		if id != except {
			exclusive |= u.exclusive
		}
	}

	return exclusive
}

// fuzzLimits is a model prepared for calculating available capacity.
type fuzzLimits struct {
	exclusive  uint64
	pools      []uint64 // pools with a charge on them
	charges    []int    // the total charge of those pools
	candidates []uint64 // the unions of every combination of those pools
}

// limits prepares the model for calculating available capacity. Only
// charged pools matter. An uncharged pool only adds capacity to a union,
// and any charged pool it brings in gives as tight a limit alone.
func (m *fuzzModel) limits() *fuzzLimits {
	l := &fuzzLimits{exclusive: m.exclusiveCpus()}

	// Key on the EFFECTIVE set: declared pools collapse onto one effective
	// set when the CPUs between them are held exclusively.
	charges := map[uint64]int{}
	for _, u := range m.users {
		// A user without shared CPUs has no charge; validate rejects one.
		if u.shared != 0 {
			charges[u.shared&^l.exclusive] += u.charge
		}
	}

	for _, p := range slices.Sorted(maps.Keys(charges)) {
		if charges[p] > 0 {
			if p == 0 {
				panic(fmt.Sprintf("model error: effective pool key is "+
					"0x0 (all shared CPUs held exclusively) with "+
					"charge %d: violates the choking rule, which "+
					"forbids leaving a user which declared shared CPUs "+
					"with none to run on",
					charges[p]))
			}
			l.pools = append(l.pools, p)
			l.charges = append(l.charges, charges[p])
		}
	}

	// Collect the distinct unions one pool at a time: the unions so far,
	// plus each with the next pool unioned in. Nested pools coincide a lot,
	// so this is far cheaper than every combination.
	seen := map[uint64]struct{}{0: {}}
	l.candidates = []uint64{0}

	for _, p := range l.pools {
		for i, n := 0, len(l.candidates); i < n; i++ {
			union := l.candidates[i] | p
			if _, ok := seen[union]; !ok {
				seen[union] = struct{}{}
				l.candidates = append(l.candidates, union)
			}
		}
	}

	return l
}

// limit returns the capacity a new user of exactly the CPUs of set could
// take without overcommitting set.
func (l *fuzzLimits) limit(set uint64) int {
	limit := 1000 * (bits.OnesCount64(set) - bits.OnesCount64(set&l.exclusive))

	for i, p := range l.pools {
		// The charge of a pool is limited to the CPUs of that pool.
		if p&^set == 0 {
			limit -= l.charges[i]
		}
	}

	return limit
}

// available returns the capacity a new user of cpus can take: the
// tightest limit of all sets containing cpus. Checking the unions of cpus
// with the charged pools suffices. Dropping any other CPU from a set keeps
// its limit if the CPU is exclusive and lowers it otherwise.
func (l *fuzzLimits) available(cpus uint64) int {
	avail := l.limit(cpus)

	for _, union := range l.candidates {
		avail = min(avail, l.limit(cpus|union))
	}

	return avail
}

// healthy returns true if no set of CPUs is overcommitted.
func (l *fuzzLimits) healthy() bool {
	return l.available(0) >= 0
}

// available returns the capacity a new user of the given CPUs can take.
func (m *fuzzModel) available(cpus uint64) int {
	return m.limits().available(cpus)
}

// chokes returns true if any user which declared shared CPUs has none left
// to run on, the state Accounting.wouldChoke refuses to create. Users
// which declared no shared CPUs are exempt.
func (m *fuzzModel) chokes() bool {
	exclusive := m.exclusiveCpus()

	for _, u := range m.users {
		if u.shared != 0 && u.shared&^exclusive == 0 {
			return true
		}
	}

	return false
}

// healthyWith returns true if, with the given usage added, no CPU set is
// overcommitted and nobody is choked.
func (m *fuzzModel) healthyWith(id string, u *fuzzUser) bool {
	c := m.clone()
	c.users[id] = u
	return !c.chokes() && c.limits().healthy()
}

// wouldAccount returns true if a checked mutation must take the usage,
// after first removing user replacing, if any. No CPU may be held
// exclusively twice, nobody may be choked, and no set of CPUs may be
// overcommitted.
//
// The model predicts the last rule although the accounting skips unions
// of pools which do not meet. A set's lacking capacity is the sum over
// its intersecting parts, so a set is over only if one part is. The
// accounting grows sets by intersecting pools, reaching every such part.
func (m *fuzzModel) wouldAccount(id string, u *fuzzUser, replacing string) bool {
	c := m.clone()
	delete(c.users, replacing)

	if u.exclusive&c.exclusiveCpus() != 0 {
		return false
	}

	c.users[id] = u

	return !c.chokes() && c.limits().healthy()
}

// wouldAllocate returns true if Allocate must take the given usage. No user
// of that ID may exist: Allocate is for new users.
func (m *fuzzModel) wouldAllocate(id string, u *fuzzUser) bool {
	if _, ok := m.users[id]; ok {
		return false
	}

	return m.wouldAccount(id, u, "")
}

// wouldReallocate returns true if Reallocate must take the given usage for the
// given ID, replacing the user of that ID if there is one.
func (m *fuzzModel) wouldReallocate(id string, u *fuzzUser) bool {
	return m.wouldAccount(id, u, id)
}

// pinnedCpus returns the CPUs user id must be pinned to: its pool's CPUs
// which nobody holds exclusively, plus its own exclusive CPUs.
func (m *fuzzModel) pinnedCpus(id string) uint64 {
	u := m.users[id]
	return u.shared & ^m.exclusiveCpus() | u.exclusive
}

// pinned returns what every user must be pinned to, to tell the users an
// operation moved from those it left alone.
func (m *fuzzModel) pinned() map[string]uint64 {
	pinned := make(map[string]uint64, len(m.users))

	for id := range m.users {
		pinned[id] = m.pinnedCpus(id)
	}

	return pinned
}

// state returns a fingerprint of the model, to tell operations which
// changed something from those which did not. The driver demands a
// version bump for every change, since the version expires offers.
func (m *fuzzModel) state() string {
	state := &strings.Builder{}

	for _, id := range slices.Sorted(maps.Keys(m.users)) {
		u := m.users[id]
		fmt.Fprintf(state, "%q:%016x/%016x/%d;", id, u.shared, u.exclusive, u.charge)
	}

	return state.String()
}

// legalCharge returns the largest charge the given usage can take without
// overcommitting any set of CPUs.
func (m *fuzzModel) legalCharge(id string, u *fuzzUser) int {
	c := m.clone()
	c.users[id] = &fuzzUser{shared: u.shared, exclusive: u.exclusive}

	// The charge bears on the CPUs the user actually runs on, which limit
	// it. Its declared set can be wider and would allow more.
	return c.available(u.shared & ^c.exclusiveCpus())
}

// legalPools returns the pools a new user can take shared CPUs from:
// those with a CPU nobody holds exclusively, as the choking rule requires.
func (m *fuzzModel) legalPools() []uint64 {
	var (
		exclusive = m.exclusiveCpus()
		pools     = []uint64{}
	)

	for _, p := range fuzzPools {
		if p&^exclusive != 0 {
			pools = append(pools, p)
		}
	}

	return pools
}

// legalUsage generates a usage which the accounting must accept, and which
// leaves every set of CPUs within its capacity.
func (m *fuzzModel) legalUsage(rng *rand.Rand, id string) *fuzzUser {
	u := &fuzzUser{}

	// Every fifth user takes no shared CPUs, and hence no charge either.
	if pools := m.legalPools(); len(pools) > 0 && rng.Intn(5) > 0 {
		u.shared = pools[rng.Intn(len(pools))]
	}

	// Exclusive CPUs must not be held by somebody else; validate refuses
	// that outright. They may lie inside our own pool or any pool in use.
	// Those which would choke a user or overcommit are dropped below.
	exclusive := m.otherExclusive("")
	free := ^exclusive & fuzzAllCpus

	// Half of the time take them from our own pool: the mixed shape, which
	// declares a pool and holds part of it.
	if u.shared != 0 && rng.Intn(2) == 0 {
		free &= u.shared
	}

	for cnt := rng.Intn(4); cnt > 0 && free != 0; cnt-- {
		cpu := fuzzPickCpu(rng, free)
		u.exclusive |= cpu
		free &= ^cpu
	}

	// Dropping exclusive CPUs one by one always ends in a legal usage:
	// with none left nobody is choked, as our pool has a CPU outside them.
	for u.exclusive != 0 && !m.healthyWith(id, u) {
		u.exclusive &= u.exclusive - 1
	}

	if u.shared != 0 {
		max := m.legalCharge(id, u)
		if max > 0 {
			u.charge = 50 * rng.Intn(max/50+1)
		}

		// A mixed usage must reserve some capacity. Take the smallest
		// nonzero charge. If even that does not fit, drop the exclusive
		// CPUs, leaving a legal shared-only usage with no charge.
		if u.exclusive != 0 && u.charge == 0 {
			if max >= 50 {
				u.charge = 50
			} else {
				u.exclusive = 0
			}
		}
	}

	return u
}

// inUsePools returns the declared CPUs of the pools in use, ignoring user
// except, sorted so that generation does not depend on map order.
func (m *fuzzModel) inUsePools(except string) []uint64 {
	pools := map[uint64]struct{}{}

	for id, u := range m.users {
		if id != except && u.shared != 0 {
			pools[u.shared] = struct{}{}
		}
	}

	return slices.Sorted(maps.Keys(pools))
}

// conflictingUsage generates a usage for id which asks for CPUs it cannot
// have: a CPU held exclusively by somebody else, all the CPUs the users of
// a pool in use run on, or a pool of exactly its own exclusive CPUs. Every
// entry point must reject it. except is the user being replaced, if any;
// its CPUs are free. Returns false if there is nothing to conflict with.
//
// Malformed usages, such as one with a charge but no shared CPUs, are not
// generated: validate guards those and no real user hits them.
func (m *fuzzModel) conflictingUsage(rng *rand.Rand, id, except string) (*CpuUsage, string, bool) {
	var (
		taken   = m.otherExclusive(except)
		flavors = []string{}
		choked  = []uint64{}
		selfish = []uint64{}
	)

	if taken != 0 {
		flavors = append(flavors, "exclusive CPU of another user")
	}

	// Taking all the CPUs a pool in use runs on chokes its users.
	for _, p := range m.inUsePools(except) {
		if eff := p & ^taken; eff != 0 {
			choked = append(choked, eff)
		}
	}
	if len(choked) > 0 {
		flavors = append(flavors, "chokes the users of a pool in use")
	}

	// A pool of only the usage's own exclusive CPUs chokes the usage.
	for _, p := range fuzzPools {
		if p&taken == 0 {
			selfish = append(selfish, p)
		}
	}
	if len(selfish) > 0 {
		flavors = append(flavors, "chokes itself")
	}

	if len(flavors) == 0 {
		return nil, "", false
	}

	switch why := flavors[rng.Intn(len(flavors))]; why {
	case "exclusive CPU of another user":
		return fuzzUsage(id, &fuzzUser{exclusive: fuzzPickCpu(rng, taken)}), why, true
	case "chokes the users of a pool in use":
		eff := choked[rng.Intn(len(choked))]
		return fuzzUsage(id, &fuzzUser{exclusive: eff}), why, true
	default:
		p := selfish[rng.Intn(len(selfish))]
		return fuzzUsage(id, &fuzzUser{shared: p, exclusive: p}), why, true
	}
}

// rejectedAdd generates a usage which insert must reject.
func (m *fuzzModel) rejectedAdd(rng *rand.Rand, id string, ids []string) (*CpuUsage, string, bool) {
	if len(ids) > 0 && rng.Intn(3) == 0 {
		// Valid otherwise, so the existing ID is what gets rejected.
		existing := ids[rng.Intn(len(ids))]
		return fuzzUsage(existing, m.legalUsage(rng, existing)), "existing ID", true
	}

	return m.conflictingUsage(rng, id, "")
}

// rejectedUpdate generates a usage which update must reject for user id.
// update deletes the user first, so its own exclusive CPUs are free.
func (m *fuzzModel) rejectedUpdate(rng *rand.Rand, id string) (*CpuUsage, string, bool) {
	return m.conflictingUsage(rng, id, id)
}

// unfittingUsage generates a usage the checked paths must refuse: either
// a conflicting one, which the unchecked paths refuse too, or one whose
// charge exceeds what its CPUs have left. except is the user being
// replaced, if any. Returns false if there is no pool to draw from.
func (m *fuzzModel) unfittingUsage(rng *rand.Rand, id, except string) (*fuzzUser, bool) {
	if rng.Intn(2) == 0 {
		if usage, _, ok := m.conflictingUsage(rng, id, except); ok {
			return fuzzUserOf(usage), true
		}
	}

	c := m.clone()
	delete(c.users, except)

	pools := c.legalPools()
	if len(pools) == 0 {
		return nil, false
	}

	// Ask for more than the user's CPUs have left. Clamp at 0, since an
	// overcommitted state leaves a negative amount.
	u := &fuzzUser{shared: pools[rng.Intn(len(pools))]}
	u.charge = max(c.legalCharge(id, u), 0) + 50*(1+rng.Intn(4))

	return u, true
}

// fuzzMask returns a CPU mask for the given set of CPUs.
func fuzzMask(set uint64) *CpuMask {
	m := NewCpuMask()

	for cpu := 0; cpu < fuzzCpuCount; cpu++ {
		if set&(1<<cpu) != 0 {
			m.Set(cpu)
		}
	}

	return m
}

// fuzzSet returns the given CPU mask as a reference model bitmask.
func fuzzSet(cpus *CpuMask) uint64 {
	set := uint64(0)

	cpus.ForEachCpu(func(cpu int) bool {
		set |= 1 << cpu
		return true
	})

	return set
}

// fuzzUsage returns a CpuUsage for the given user of the reference model.
func fuzzUsage(id string, u *fuzzUser) *CpuUsage {
	return &CpuUsage{
		ID:        id,
		Name:      id,
		Shared:    fuzzMask(u.shared),
		Exclusive: fuzzMask(u.exclusive),
		Charge:    u.charge,
	}
}

// fuzzUserOf returns the reference model's view of the given usage.
func fuzzUserOf(usage *CpuUsage) *fuzzUser {
	return &fuzzUser{
		shared:    fuzzSet(usage.Shared),
		exclusive: fuzzSet(usage.Exclusive),
		charge:    usage.Charge,
	}
}

// fuzzPickCpu returns a random single CPU of the given set of CPUs.
func fuzzPickCpu(rng *rand.Rand, set uint64) uint64 {
	cpus := []uint64{}

	for cpu := 0; cpu < fuzzCpuCount; cpu++ {
		if bit := uint64(1) << cpu; set&bit != 0 {
			cpus = append(cpus, bit)
		}
	}

	return cpus[rng.Intn(len(cpus))]
}

// fuzzVerify checks the capacity the accounting reports for our probes
// against the reference model.
func fuzzVerify(t *testing.T, a *Accounting, m *fuzzModel, step int, op string) {
	limits := m.limits()
	healthy := limits.healthy()

	// All probes in one traversal, compared to one at a time below.
	probes := make([]*CpuMask, 0, len(fuzzProbes))
	for _, probe := range fuzzProbes {
		probes = append(probes, fuzzMask(probe))
	}

	each, exact := a.AvailableEach(probes...)
	require.True(t, exact, "step %d, %s: enumeration cut short", step, op)
	require.Len(t, each, len(probes), "step %d, %s: one answer per probe", step, op)

	for i, probe := range fuzzProbes {
		cpus := probes[i]
		expect := limits.available(probe)
		combined := each[i]

		actual, ok := a.Available(cpus)
		require.True(t, ok, "step %d, %s: available in %s", step, op, cpus)

		for _, c := range []struct {
			what  string
			value int
		}{
			{"one probe", actual},
			{"many probes", combined},
		} {
			if healthy {
				require.Equal(t, expect, c.value,
					"step %d, %s: available in %s, %s", step, op, cpus, c.what)
				continue
			}

			// With a set overcommitted, the
			// accounting may miss the tightest
			// limit: it skips unions of pools
			// which do not intersect. It must
			// never report less than that.
			require.GreaterOrEqual(t, c.value, expect,
				"step %d, %s: available in %s, %s", step, op, cpus, c.what)
		}

		// While overcommitted, a set reached from another
		// probe may still contain this one, so the batched
		// answer may be tighter. It may never be looser.
		require.LessOrEqual(t, combined, actual,
			"step %d, %s: available in %s: %d for one probe, %d among many",
			step, op, cpus, actual, combined)
	}

	requireTruncatedBounds(t, a, probes, each, step, op)
	requireExclusiveCapacity(t, a, probes[step%len(probes)], step, op)
	requireExclusiveBudgets(t, a, step, op)

	requireChargeIdentity(t, a)
	requireEffectiveCache(t, a)
	requireNobodyChoked(t, a)
}

// requireExclusiveCapacity checks ExclusiveCapacity of probe against a
// greedy take via Admits, which hits the true max when constraints are
// laminar. One rotating probe per step, as greedy costs an Admits per CPU.
func requireExclusiveCapacity(t *testing.T, a *Accounting, probe *CpuMask, step int, op string) {
	n, exact, err := a.ExclusiveCapacity(probe)
	require.NoError(t, err, "step %d, %s: exclusive capacity of %s", step, op, probe)

	greedy := greedyExclusive(t, a, probe)
	if exact {
		require.Equal(t, greedy, n,
			"step %d, %s: exclusive capacity of %s, greedy took", step, op, probe)
	} else {
		require.GreaterOrEqual(t, n, greedy,
			"step %d, %s: exclusive bound of %s below greedy", step, op, probe)
	}

	// Declared nesting keeps effective nesting.
	if a.Laminar() {
		require.True(t, exact, "step %d, %s: laminar yet inexact", step, op)
	}

	// The probe's own limit and every union over it bound a take.
	avail, _ := a.Available(probe)
	require.LessOrEqual(t, n, max(avail, 0)/1000,
		"step %d, %s: exclusive capacity of %s over Available", step, op, probe)
}

// requireExclusiveBudgets checks every Keep against ExclusiveCapacity, and
// one rotating budget against both greedy takes.
func requireExclusiveBudgets(t *testing.T, a *Accounting, step int, op string) {
	budgets, exact := a.ExclusiveBudgets()
	if a.Laminar() {
		require.True(t, exact, "step %d, %s: laminar yet inexact budgets", step, op)
	}
	require.Len(t, budgets, len(a.UsedPools()), "step %d, %s: one budget per pool in use", step, op)

	for _, b := range budgets {
		take, capExact, err := a.ExclusiveCapacity(b.Cpus)
		require.NoError(t, err)
		require.Equal(t, exact, capExact, "step %d, %s: exactness of %s", step, op, b.Cpus)
		if exact {
			require.Equal(t, b.Cpus.Size()-take, b.Keep,
				"step %d, %s: keep of %s vs ExclusiveCapacity", step, op, b.Cpus)
		}
	}

	if len(budgets) > 0 {
		requireBudgetsAgree(t, a, budgets[step%len(budgets):step%len(budgets)+1], exact)
	}
}

// requireTruncatedBounds checks a traversal cut short at maxImplicitPools
// against the exact answers of the full one. The fuzzed accounting never
// nears the real cutoff, so the cutoff is lowered here. Every truncated
// answer must hold two bounds:
//
//   - It does not exceed the probe's own limit, which Limit reports
//     exactly. traverse seeds every answer with it.
//   - It is not below the exact answer. Stopping early only leaves sets
//     unvisited.
//
// If the cutoff does not bite, the answers must be exact.
func requireTruncatedBounds(t *testing.T, a *Accounting, probes []*CpuMask, full []int, step int, op string) {
	defer func(was int) { maxImplicitPools = was }(maxImplicitPools)

	limits := make([]int, len(probes))
	for i, probe := range probes {
		limits[i] = a.Limit(probe)
	}

	// maxImplicitPools counts the distinct sets seen, which start as the
	// probes. Cut off at the first set, where the probes run out (leaving a
	// probe never dequeued), and inside the growth beyond, moving with step.
	for _, cutoff := range []int{1, len(probes), len(probes) + 1 + step%len(probes)} {
		maxImplicitPools = cutoff

		got, exact := a.AvailableEach(probes...)
		require.Len(t, got, len(probes),
			"step %d, %s: one answer per probe at a cutoff of %d", step, op, cutoff)

		for i, probe := range probes {
			require.LessOrEqual(t, got[i], limits[i],
				"step %d, %s: available in %s at a cutoff of %d exceeds its own limit",
				step, op, probe, cutoff)
			require.GreaterOrEqual(t, got[i], full[i],
				"step %d, %s: available in %s at a cutoff of %d is below the exact answer",
				step, op, probe, cutoff)
		}

		if exact {
			require.Equal(t, full, got,
				"step %d, %s: a cutoff of %d did not bite, so the answers are exact",
				step, op, cutoff)
		}
	}
}

// requireOfferHeadroomMatches checks that the headroom an offer predicted
// for the requester's pool is what the pool has after the operation.
// Headroom probes the declared set, the offer the effective one. Their
// limits match: an exclusive CPU adds 1000 of capacity and of charge.
func requireOfferHeadroomMatches(t *testing.T, a *Accounting, usage *CpuUsage,
	offered int, offeredExact bool, step int, op, id string) {
	t.Helper()

	after, exact, err := a.Headroom(usage)
	require.NoError(t, err, "step %d, %s %q: headroom of the committed state",
		step, op, id)
	require.True(t, offeredExact, "step %d, %s %q: the offer's headroom was cut short",
		step, op, id)
	require.True(t, exact, "step %d, %s %q: the headroom after was cut short",
		step, op, id)
	require.Equal(t, after[0], offered,
		"step %d, %s %q: the offer promised %dm left, the state has %dm",
		step, op, id, offered, after[0])
}

// requireAdmitsAgrees checks that Admits gives the same verdict as taking
// an offer on the same state. It runs on every operation, so a fast path
// for one usage shape cannot quietly diverge.
func requireAdmitsAgrees(t *testing.T, a *Accounting, usage *CpuUsage, offerErr error,
	step int, op, id string) {
	t.Helper()

	v := a.Admits(usage)

	require.Len(t, v, 1, "step %d, %s %q: one verdict per candidate", step, op, id)
	require.Equal(t, offerErr == nil, v[0].Admits,
		"step %d, %s %q: Admits said %v, the offer said %v",
		step, op, id, v[0].Admits, offerErr)
	require.Equal(t, offerErr == nil, v[0].Err == nil,
		"step %d, %s %q: Admits reported %v, the offer reported %v",
		step, op, id, v[0].Err, offerErr)

	// The lacking amounts must agree too: the right verdict by the wrong
	// arithmetic is still wrong.
	var byAdmits, byOffer *CapacityError
	if errors.As(v[0].Err, &byAdmits) && errors.As(offerErr, &byOffer) {
		require.Equal(t, byOffer.Lacking, byAdmits.Lacking,
			"step %d, %s %q: Admits lacking %dm, the offer lacking %dm",
			step, op, id, byAdmits.Lacking, byOffer.Lacking)
	}
}

// fuzzAllocate offers the usage, allocates it, and checks both against the
// reference model. The offer must say exactly what the allocation does.
//
// Returns whether the usage was taken, and records it in the model if it was.
func fuzzAllocate(t *testing.T, a *Accounting, m *fuzzModel, step int, op, id string, u *fuzzUser) bool {
	var (
		usage  = fuzzUsage(id, u)
		accept = m.wouldAllocate(id, u)
		before = m.pinned()
	)

	offer, offerErr := a.GetOffer(usage)
	requireAdmitsAgrees(t, a, usage, offerErr, step, op, id)

	// Read before the operation, checked after it: an offer's headroom is a
	// claim about a state which does not exist yet.
	headroom, headroomExact, headroomErr := 0, false, error(nil)
	if offer != nil {
		headroom, headroomExact, headroomErr = offer.Headroom()
	}

	cpus, updates, err := a.Allocate(usage)

	if !accept {
		require.Error(t, offerErr, "step %d, %s %q: offer", step, op, id)
		require.Error(t, err, "step %d, %s %q", step, op, id)
		require.Nil(t, cpus, "step %d, %s %q: CPUs of a refused usage", step, op, id)
		require.Empty(t, updates, "step %d, %s %q: changes of a refused usage", step, op, id)
		return false
	}

	require.NoError(t, offerErr, "step %d, %s %q: offer", step, op, id)
	require.NoError(t, err, "step %d, %s %q", step, op, id)

	require.True(t, offer.Cpus().Equals(cpus), "step %d, %s %q: offered %s, allocated %s",
		step, op, id, offer.Cpus(), cpus)
	requireSameChanges(t, offer.Updates(), updates, step, op, id)

	// Committing expires every offer, including this one.
	require.False(t, offer.IsValid(), "step %d, %s %q: the offer outlived the allocation",
		step, op, id)

	if headroomErr == nil {
		requireOfferHeadroomMatches(t, a, usage, headroom, headroomExact, step, op, id)
	}

	m.users[id] = u
	requireMoved(t, m, before, id, updates, step, op)
	require.Equal(t, m.pinnedCpus(id), fuzzSet(cpus),
		"step %d, %s %q: CPUs of the requester", step, op, id)

	return true
}

// fuzzReallocate reallocates the usage and checks it against the reference
// model. It first takes an offer, which treats an existing ID as a
// replacement, and checks that both routes reach the same verdict and
// consequences, so Reallocate can be sugar for GetOffer plus Commit. For
// a new ID both routes judge a plain new user.
//
// Returns whether the usage was taken, and records it in the model if it was.
func fuzzReallocate(t *testing.T, a *Accounting, m *fuzzModel, step int, op, id string, u *fuzzUser) bool {
	var (
		usage  = fuzzUsage(id, u)
		accept = m.wouldReallocate(id, u)
		before = m.pinned()
	)

	// Taking an offer changes nothing, so Reallocate sees the same state.
	offer, offerErr := a.GetOffer(usage)
	requireAdmitsAgrees(t, a, usage, offerErr, step, op, id)

	// Read before the operation, checked after it: an offer's headroom is a
	// claim about a state which does not exist yet.
	headroom, headroomExact, headroomErr := 0, false, error(nil)
	if offer != nil {
		headroom, headroomExact, headroomErr = offer.Headroom()
	}

	cpus, updates, err := a.Reallocate(usage)

	if !accept {
		require.Error(t, offerErr, "step %d, %s %q: offer", step, op, id)
		require.Error(t, err, "step %d, %s %q", step, op, id)
		require.Nil(t, cpus, "step %d, %s %q: CPUs of a refused usage", step, op, id)
		require.Empty(t, updates, "step %d, %s %q: changes of a refused usage", step, op, id)
		return false
	}

	require.NoError(t, offerErr, "step %d, %s %q: offer", step, op, id)
	require.NoError(t, err, "step %d, %s %q", step, op, id)

	require.True(t, offer.Cpus().Equals(cpus),
		"step %d, %s %q: offered %s, reallocated %s", step, op, id, offer.Cpus(), cpus)
	requireSameChanges(t, offer.Updates(), updates, step, op, id)

	// The reallocation expires every offer, including this one.
	require.False(t, offer.IsValid(),
		"step %d, %s %q: the offer outlived the reallocation", step, op, id)

	if headroomErr == nil {
		requireOfferHeadroomMatches(t, a, usage, headroom, headroomExact, step, op, id)
	}

	m.users[id] = u
	requireMoved(t, m, before, id, updates, step, op)
	require.Equal(t, m.pinnedCpus(id), fuzzSet(cpus),
		"step %d, %s %q: CPUs of the requester", step, op, id)

	return true
}

// requireMoved checks the reported changes against the model: exactly the
// users whose pinning changed, with their new CPUs. The requester is left
// out, since its CPUs are the result of the operation.
func requireMoved(t *testing.T, m *fuzzModel, before map[string]uint64, requester string,
	updates []Change, step int, op string) {
	expect := map[string]uint64{}
	for id, now := range m.pinned() {
		if was, ok := before[id]; ok && was != now && id != requester {
			expect[id] = now
		}
	}

	actual := map[string]uint64{}
	for i := range updates {
		c := &updates[i]
		require.NotEqual(t, requester, c.ID(),
			"step %d, %s: the requester is not a side effect of itself", step, op)
		actual[c.ID()] = fuzzSet(c.Cpus())
	}

	require.Equal(t, expect, actual, "step %d, %s: users to re-pin", step, op)
}

// requireSameChanges checks that what an offer promised and what the allocation
// reported are the same report, down to their order.
func requireSameChanges(t *testing.T, offered, made []Change, step int, op, id string) {
	require.Len(t, made, len(offered), "step %d, %s %q: changes offered %v, made %v",
		step, op, id, offered, made)

	for i := range made {
		o, c := &offered[i], &made[i]
		require.Equal(t, o.ID(), c.ID(), "step %d, %s %q: change #%d ID", step, op, id, i)
		require.Equal(t, o.Name(), c.Name(), "step %d, %s %q: change #%d name", step, op, id, i)
		require.True(t, o.Cpus().Equals(c.Cpus()),
			"step %d, %s %q: change #%d for %q, offered %s, made %s",
			step, op, id, i, c.ID(), o.Cpus(), c.Cpus())
	}
}

// TestFuzzModelEffectiveSets pins that the reference model subtracts a pool's
// charge based on its effective set, not its declared set.
func TestFuzzModelEffectiveSets(t *testing.T) {
	// Pool 0-3 charging 500, CPU 3 held exclusively by another user. The
	// charge must count against effective set 0-2, not declared set 0-3.
	m := &fuzzModel{users: map[string]*fuzzUser{
		"shared":    {shared: 0b1111, charge: 500},
		"exclusive": {exclusive: 0b1000},
	}}

	l := m.limits()

	require.Equal(t, []uint64{0b0111}, l.pools, "charged pool effective sets")
	// 0-2: capacity 3000, charge 500.
	require.Equal(t, 2500, l.limit(0b0111), "limit of 0-2")
	// 0-3: capacity 4000, minus 1000 for exclusive CPU 3, minus 500.
	require.Equal(t, 2500, l.limit(0b1111), "limit of 0-3")

	// Two declared pools collapse onto one effective set when CPUs between
	// them are held exclusively, and both charges bear on it.
	m2 := &fuzzModel{users: map[string]*fuzzUser{
		"user1":     {shared: 0b0011, charge: 200}, // CPUs 0-1, charge 200
		"user2":     {shared: 0b0111, charge: 300}, // CPUs 0-2, charge 300
		"exclusive": {exclusive: 0b0100},           // CPU 2 held exclusively
	}}

	l2 := m2.limits()

	// Declared pools 0b0011 and 0b0111 collapse to effective set 0b0011.
	// Merged charge: 200 + 300 = 500.
	require.Equal(t, []uint64{0b0011}, l2.pools, "merged effective pools")
	// Capacity: 1000 * 2 CPUs = 2000. Charge: 500. Limit: 1500.
	require.Equal(t, 1500, l2.limit(0b0011), "limit of merged effective set")
}

// TestFuzzLimits cross-checks the model's shortcut, checking only the
// unions of charged pools, against brute force over every set of CPUs.
func TestFuzzLimits(t *testing.T) {
	// Keep the number of CPUs low: we check every set of them.
	const (
		cpus    = 8
		allCpus = uint64(1)<<cpus - 1
	)

	rng := rand.New(rand.NewSource(1))

	for i := 0; i < 100; i++ {
		var m *fuzzModel
		for {
			m = &fuzzModel{users: map[string]*fuzzUser{}}

			for u := 0; u < 1+rng.Intn(6); u++ {
				shared := uint64(rng.Intn(int(allCpus) + 1))
				charge := 0
				if shared != 0 {
					charge = 250 * rng.Intn(6)
				}
				// Exclusive CPUs may overlap the user's
				// own shared CPUs, the mixed shape, but
				// no CPU is held exclusively twice.
				m.users[fmt.Sprintf("user #%d", u)] = &fuzzUser{
					shared:    shared,
					exclusive: uint64(rng.Intn(int(allCpus)+1)) & ^m.exclusiveCpus(),
					charge:    charge,
				}
			}

			// Skip models which choke a user. The
			// accounting forbids that, so the panic in
			// limits() stays unreachable, while states
			// with collapsing effective pools do not.
			if !m.chokes() {
				break
			}
		}

		limits := m.limits()

		for probe := uint64(0); probe <= allCpus; probe++ {
			// Brute force over every superset.
			expect := limits.limit(probe)
			for set := uint64(0); set <= allCpus; set++ {
				if set&probe == probe {
					expect = min(expect, limits.limit(set))
				}
			}

			require.Equal(t, expect, limits.available(probe),
				"model #%d: available in %s", i, fuzzMask(probe))
		}
	}
}

// TestFuzzGeneratorShapes pins that legalUsage reaches all four usage
// shapes, and puts exclusive CPUs inside pools in use, including its own.
func TestFuzzGeneratorShapes(t *testing.T) {
	shapes := map[string]int{}

	// Mirror a campaign: a fresh accounting per seed, driven for the same
	// number of steps, with the same mix of additions and deletions.
	for seed := int64(0); seed < 32; seed++ {
		var (
			rng = rand.New(rand.NewSource(seed))
			m   = &fuzzModel{users: map[string]*fuzzUser{}}
			ids []string
		)

		for step := 0; step < fuzzSteps; step++ {
			if len(ids) > 0 && rng.Intn(4) == 0 {
				i := rng.Intn(len(ids))
				delete(m.users, ids[i])
				ids = slices.Delete(ids, i, i+1)
				continue
			}

			id := fmt.Sprintf("user #%d", step)
			u := m.legalUsage(rng, id)

			switch {
			case u.shared == 0 && u.exclusive != 0:
				shapes["exclusive-only"]++
			case u.shared == 0:
				shapes["nothing at all"]++
			case u.exclusive == 0 && u.charge == 0:
				shapes["BestEffort"]++
			case u.exclusive == 0:
				shapes["shared-only"]++
			case u.charge == 0:
				// Refused by validate; never emit.
				// Counted apart from "mixed" for
				// the assertion below.
				shapes["mixed without a charge"]++
			default:
				shapes["mixed"]++
			}

			// Exclusive CPUs inside the user's own
			// pool, or inside another pool in use.
			if u.exclusive != 0 && u.exclusive&^u.shared == 0 {
				shapes["exclusive inside own pool"]++
			}
			for _, p := range m.inUsePools("") {
				if u.exclusive&p != 0 {
					shapes["exclusive inside another pool in use"]++
					break
				}
			}

			m.users[id] = u
			ids = append(ids, id)

			// legalUsage never chokes anyone, but
			// its charge might overcommit.
			require.True(t, m.limits().healthy(),
				"seed %d, step %d: generated an overcommit", seed, step)
		}
	}

	t.Logf("generated shapes: %v", shapes)

	for _, shape := range []string{
		"exclusive-only",
		"BestEffort",
		"mixed",
		"shared-only",
		"exclusive inside own pool",
		"exclusive inside another pool in use",
	} {
		require.Greater(t, shapes[shape], 0, "generated %q usages", shape)
	}

	// A mixed usage with no charge is refused, so generating one would test
	// rejection where acceptance was meant.
	require.Zero(t, shapes["mixed without a charge"],
		"generated an invalid shape: mixed with no charge")
}

// TestFuzzOperationMix pins that the driver performs every operation in
// fuzzOps, and logs how often.
func TestFuzzOperationMix(t *testing.T) {
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	counts := map[string]int{}

	for seed := int64(0); seed < 16; seed++ {
		fuzzDrive(t, seed, counts)
	}

	t.Logf("operations performed: %v", counts)

	for _, op := range fuzzOps {
		require.Greater(t, counts[op], 0, "performed %q", op)
	}
}

// FuzzAccounting drives an accounting through a pseudorandom sequence of
// operations, most of which must succeed and some of which must be
// rejected. It verifies the reported capacity against a brute force
// reference model after every operation.
func FuzzAccounting(f *testing.F) {
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	for _, seed := range []int64{
		0, 1, 2, 3, 4, 5, 6, 7, 8,
		11, 13, 15, 17, 19, 21, 23,
		42, 47, 52, 57, 62, 64,
		77, 88, 99, 100, 111, 122,
		128, 256, 512, 1024, 2048, 4096,
		987654321,
	} {
		f.Add(seed)
	}

	f.Fuzz(fuzzAccounting)
}

func fuzzAccounting(t *testing.T, seed int64) {
	fuzzDrive(t, seed, map[string]int{})
}

// fuzzOps are the operations the driver chooses from, most of them expected
// to succeed. The repetitions weight the mix. The checked paths Allocate,
// Reallocate and Release are first-class operations: they are what a
// caller actually gets.
var fuzzOps = []string{
	"add", "add", "add",
	"allocate", "allocate", "allocate", "allocate",
	"update", "update",
	"reallocate", "reallocate", "reallocate",
	"delete", "delete",
	"release", "release",
	"add rejected", "update rejected",
	"allocate rejected", "reallocate rejected",
	"delete unknown", "update unknown",
	"overcommit",
}

// fuzzDrive drives an accounting through the operations seed picks,
// verifying it against the reference model after each. counts records
// operations performed, not picked: some are skipped early on.
func fuzzDrive(t *testing.T, seed int64, counts map[string]int) {
	var (
		rng   = rand.New(rand.NewSource(seed))
		a     = NewAccounting(fuzzMask(fuzzAllCpus))
		model = &fuzzModel{users: map[string]*fuzzUser{}}
		ids   []string
		next  int
	)

	newId := func() string {
		id := fmt.Sprintf("user #%d", next)
		next++
		return id
	}

	// Pick from ids, not the model's map, so a seed reproduces its run.
	pickId := func() (string, bool) {
		if len(ids) == 0 {
			return "", false
		}
		return ids[rng.Intn(len(ids))], true
	}

	dropId := func(id string) {
		if i := slices.Index(ids, id); i >= 0 {
			ids = slices.Delete(ids, i, i+1)
		}
	}

	for step := 0; step < fuzzSteps; step++ {
		limits := model.limits()
		op := fuzzOps[rng.Intn(len(fuzzOps))]

		wasVersion, wasState := a.version, model.state()

		switch op {
		case "add":
			id := newId()
			u := model.legalUsage(rng, id)
			require.NoError(t, a.insert(fuzzUsage(id, u)), "step %d, %s %q", step, op, id)
			model.users[id] = u
			ids = append(ids, id)
			counts[op]++

		case "allocate":
			id := newId()
			u := model.legalUsage(rng, id)
			require.True(t, fuzzAllocate(t, a, model, step, op, id, u),
				"step %d, %s %q: a usage which fits was refused", step, op, id)
			ids = append(ids, id)
			counts[op]++

		case "allocate rejected":
			id := newId()
			u, ok := model.unfittingUsage(rng, id, "")
			if !ok {
				break
			}
			require.False(t, model.wouldAllocate(id, u),
				"step %d, %s %q: generated a usage which fits", step, op, id)
			fuzzAllocate(t, a, model, step, op, id, u)
			counts[op]++

		case "delete":
			id, ok := pickId()
			if !ok {
				break
			}
			require.NoError(t, deleteUser(a, id), "step %d, %s %q", step, op, id)
			delete(model.users, id)
			dropId(id)
			counts[op]++

		case "release":
			id, ok := pickId()
			if !ok {
				break
			}

			before := model.pinned()

			updates, err := a.Release(id)
			require.NoError(t, err, "step %d, %s %q", step, op, id)

			delete(model.users, id)
			dropId(id)

			// Released exclusive CPUs widen the
			// effective sets of the pools declaring them.
			requireMoved(t, model, before, id, updates, step, op)
			counts[op]++

		case "update":
			id, ok := pickId()
			if !ok {
				break
			}
			// update deletes the old user first, so
			// generate the new usage without it.
			delete(model.users, id)
			u := model.legalUsage(rng, id)
			require.NoError(t, a.update(fuzzUsage(id, u)), "step %d, %s %q", step, op, id)
			model.users[id] = u
			counts[op]++

		case "reallocate":
			id, ok := pickId()
			if !ok {
				break
			}
			// Generate the new usage without the old
			// user, whose CPUs are free, then restore
			// it: Reallocate is judged with it present.
			old := model.users[id]
			delete(model.users, id)
			u := model.legalUsage(rng, id)
			model.users[id] = old

			require.True(t, fuzzReallocate(t, a, model, step, op, id, u),
				"step %d, %s %q: a usage which fits was refused", step, op, id)
			counts[op]++

		case "reallocate rejected":
			id, ok := pickId()
			if !ok {
				break
			}
			u, ok := model.unfittingUsage(rng, id, id)
			if !ok {
				break
			}
			require.False(t, model.wouldReallocate(id, u),
				"step %d, %s %q: generated a usage which fits", step, op, id)
			// A rejected reallocation must keep the
			// old user; the verification checks it.
			fuzzReallocate(t, a, model, step, op, id, u)
			counts[op]++

		case "update unknown":
			// Updating a user which does not exist adds it.
			id := newId()
			u := model.legalUsage(rng, id)
			require.NoError(t, a.update(fuzzUsage(id, u)), "step %d, %s %q", step, op, id)
			model.users[id] = u
			ids = append(ids, id)
			counts[op]++

		case "delete unknown":
			id := fmt.Sprintf("no such user #%d", step)
			require.Error(t, deleteUser(a, id), "step %d, %s %q", step, op, id)
			counts[op]++

		case "add rejected":
			usage, why, ok := model.rejectedAdd(rng, newId(), ids)
			if !ok {
				break
			}
			require.Error(t, a.insert(usage), "step %d, %s (%s)", step, op, why)
			counts[op]++

		case "update rejected":
			id, ok := pickId()
			if !ok {
				break
			}
			usage, why, ok := model.rejectedUpdate(rng, id)
			if !ok {
				break
			}
			// A rejected update must keep the old
			// user; the verification checks it.
			require.Error(t, a.update(usage), "step %d, %s %q (%s)", step, op, id, why)
			counts[op]++

		case "overcommit":
			// insert does not reject an overcommit;
			// it shows as negative available capacity.
			pools := model.legalPools()
			if len(pools) == 0 {
				break
			}

			var (
				id     = newId()
				shared = pools[rng.Intn(len(pools))]
				excess = 250 * (1 + rng.Intn(4))
				charge = limits.available(shared) + excess
				u      = &fuzzUser{shared: shared, charge: charge}
			)

			require.NoError(t, a.insert(fuzzUsage(id, u)), "step %d, %s %q", step, op, id)
			model.users[id] = u

			// Every set containing the pool loses
			// the same charge, so the pool is short
			// of exactly the excess.
			require.Equal(t, -excess, available(t, a, fuzzMask(shared)),
				"step %d, %s: available in %s", step, op, fuzzMask(shared))
			fuzzVerify(t, a, model, step, op)

			// Undo it, so we keep exercising sane states, too.
			require.NoError(t, deleteUser(a, id), "step %d, %s: undo %q", step, op, id)
			delete(model.users, id)
			counts[op]++
		}

		// Any state change must bump the version, since it expires
		// outstanding offers. Only that direction is a rule: a rejected
		// update, or adding and undoing an overcommit, may bump it for
		// no net change.
		require.GreaterOrEqual(t, a.version, wasVersion,
			"step %d, %s: the version went backwards", step, op)
		if model.state() != wasState {
			require.Greater(t, a.version, wasVersion,
				"step %d, %s: the state changed without bumping the version", step, op)
		}

		fuzzVerify(t, a, model, step, op)
	}
}
