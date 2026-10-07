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
	"slices"
)

// Verdict is what [Accounting.Admits] answers about one candidate usage.
type Verdict struct {
	// Admits is whether the accounting would take the candidate.
	Admits bool

	// Exact is false only if the enumeration was cut short at
	// maxImplicitPools; then an admission may be optimistic. A refusal is
	// always exact.
	Exact bool

	// Err says why the candidate is refused, and is nil if admitted. A
	// *CapacityError means it is well formed but lacks capacity; any
	// other error means it could not be considered at all.
	Err error
}

// Admits reports, per candidate usage and positionally, whether the
// accounting would take it: the verdict of [Accounting.GetOffer] without the
// offer. Candidates are alternatives, each judged against the accounting as
// it stands, so at most one can be acted on. A candidate whose ID the
// accounting holds is judged as a replacement of that user. Nothing is
// altered.
//
// A candidate reserving nothing and taking no CPU exclusively needs no
// enumeration. Charges without exclusive CPUs share one traversal. Candidates
// taking CPUs exclusively, or replacing a user, cost a traversal each.
func (a *Accounting) Admits(candidates ...*CpuUsage) []Verdict {
	if len(candidates) == 0 {
		return nil
	}

	// A candidate for the shared traversal, validated once.
	type pending struct {
		at       int
		user     *user
		replaces string
	}

	var (
		verdicts = make([]Verdict, len(candidates))
		batch    []pending
	)

	// The shortcuts hold only while the accounting is exact and nothing is
	// overcommitted. Otherwise every candidate takes the general path.
	over, exact := a.overcommit()
	sound := exact && over == 0

	for i, usage := range candidates {
		u, replaces, err := a.candidate(usage)
		if err != nil {
			verdicts[i] = Verdict{Exact: true, Err: err}
			continue
		}

		switch {
		case !sound:
			verdicts[i] = a.admits(u, replaces)

		case u.charge == 0 && u.exclusive.IsEmpty():
			// Changes no limit; a replacement of it only frees.
			verdicts[i] = Verdict{Admits: true, Exact: true}

		case u.exclusive.IsEmpty() && replaces == "":
			// Probing the declared set is exact for a charge. New
			// users only: capacity still counts a replaced charge.
			batch = append(batch, pending{at: i, user: u, replaces: replaces})

		default:
			// Exclusive CPUs bear on sets inside a pool, and a
			// replacement frees too; Available sees neither.
			verdicts[i] = a.admits(u, replaces)
		}
	}

	if len(batch) > 0 {
		probes := make([]*CpuMask, len(batch))
		for n, p := range batch {
			probes[n] = p.user.shared
		}

		room, exact := a.AvailableEach(probes...)

		for n, p := range batch {
			switch {
			case !exact:
				// Cut short: fall back rather than guess.
				verdicts[p.at] = a.admits(p.user, p.replaces)
			case room[n] < p.user.charge:
				verdicts[p.at] = a.lacking(p.user, p.user.charge-room[n], true)
			default:
				verdicts[p.at] = Verdict{Admits: true, Exact: true}
			}
		}
	}

	return verdicts
}

// candidate validates a candidate usage and returns the user it would be,
// with the ID of the user it would replace, if any. Nothing is mutated.
func (a *Accounting) candidate(usage *CpuUsage) (*user, string, error) {
	if usage == nil {
		return nil, "", errNilUsage
	}

	// Same rule as GetOffer, so the two agree on replacements.
	replaces := ""
	if _, exists := a.users[usage.ID]; exists {
		replaces = usage.ID
	}

	u, err := a.validate(usage, replaces)
	if err != nil {
		return nil, "", err
	}

	return u, replaces, nil
}

// admits is the general verdict for any shape: it enumerates the implicit
// pools of the hypothetical state, one traversal per candidate.
func (a *Accounting) admits(u *user, replaces string) Verdict {
	over, exact := a.hypothetical(u, replaces).lackingCapacity()
	if over > 0 {
		return a.lacking(u, over, exact)
	}

	return Verdict{Admits: true, Exact: exact}
}

// lacking is the verdict on a well formed usage short of over milli-CPU,
// wrapped in a *CapacityError.
func (a *Accounting) lacking(u *user, over int, exact bool) Verdict {
	return Verdict{
		Exact: exact,
		Err:   fmt.Errorf("user %q: %w", u.id, &CapacityError{Lacking: over}),
	}
}

// Headroom returns, per candidate usage, the milli-CPU available in the pool
// it declares, from one traversal, and whether the answers are exact; if not,
// each is an upper bound. It is a score for ranking candidates
// [Accounting.Admits] admitted, not a verdict.
//
// It fails if any candidate declares no shared set. A mixed candidate is
// scored on its shared set alone. Rank exclusive candidates by [Offer.Updates].
func (a *Accounting) Headroom(candidates ...*CpuUsage) ([]int, bool, error) {
	if len(candidates) == 0 {
		return nil, true, nil
	}

	pools := make([]*CpuMask, len(candidates))

	for i, usage := range candidates {
		if usage == nil {
			return nil, false, errNilUsage
		}
		if usage.Shared.IsEmpty() {
			return nil, false, fmt.Errorf("user %q: declares no shared CPUs, "+
				"so there is no pool to score", usage.ID)
		}
		pools[i] = usage.Shared
	}

	room, exact := a.AvailableEach(pools...)

	return room, exact, nil
}

// ExclusiveCapacity returns the most CPUs a single exclusive-only usage could
// take from the given set now, whichever it picks, and whether that is exact;
// if not, it is an upper bound. The set need not be a pool, but must lie
// inside the accounting. The answer is exact while the pools' effective sets
// are laminar, see [Accounting.Laminar], and 0 on an overcommitted
// accounting. Nothing is altered.
func (a *Accounting) ExclusiveCapacity(pool *CpuMask) (int, bool, error) {
	if pool == nil {
		return 0, false, errors.New("exclusive capacity: nil set of CPUs")
	}
	if outside := pool.Difference(a.cpus); !outside.IsEmpty() {
		return 0, false, fmt.Errorf("exclusive capacity: CPUs %s outside accounting",
			outside)
	}

	caps, ok := a.exclusiveCaps()
	if !ok {
		// Pools cross: fall back to a bound which survives a cutoff.
		avail, _ := a.Available(pool)
		return max(avail, 0) / 1000, false, nil
	}

	return innerCap(caps, pool.Difference(a.exclusive)), true, nil
}

// exclusiveCap is a constraint set with the most CPUs a take may hit.
type exclusiveCap struct {
	cpus *CpuMask
	cap  int
}

// exclusiveCaps returns the cap of every distinct effective pool set, and
// whether those sets are laminar. A cap is the whole CPUs of limit left, at
// most one less than the size for a pool with users. A negative cap means
// overcommitted.
func (a *Accounting) exclusiveCaps() ([]exclusiveCap, bool) {
	var (
		charge = chargeIn(a.exclusive, a.charged())
		index  = make(map[string]int, len(a.pools))
		caps   = make([]exclusiveCap, 0, len(a.pools))
		sets   = make([]*CpuMask, 0, len(a.pools))
	)

	for _, p := range a.pools {
		eff := p.eff()
		if eff.IsEmpty() {
			continue
		}

		// eff has no exclusive CPUs: capacity minus charges.
		limit := 1000*eff.Size() - charge(eff)
		c := limit / 1000
		if limit < 0 && limit%1000 != 0 {
			c-- // floor, not truncate
		}
		if len(p.users) > 0 {
			c = min(c, eff.Size()-1)
		}

		if i, ok := index[eff.Key()]; ok {
			caps[i].cap = min(caps[i].cap, c)
			continue
		}
		index[eff.Key()] = len(caps)
		caps = append(caps, exclusiveCap{cpus: eff, cap: c})
		sets = append(sets, eff)
	}

	return caps, laminar(sets)
}

// innerCap returns the most CPUs takeable from free under laminar caps, by
// the closed form in the package documentation, or 0 if any cap is negative.
func innerCap(caps []exclusiveCap, free *CpuMask) int {
	type node struct {
		cpus       *CpuMask
		cap, inner int
	}

	nodes := []node{{cpus: free, cap: free.Size()}}
	index := map[string]int{free.Key(): 0}

	for _, c := range caps {
		if c.cap < 0 {
			return 0
		}
		cut := c.cpus.Intersection(free)
		if cut.IsEmpty() {
			continue
		}
		if i, ok := index[cut.Key()]; ok {
			nodes[i].cap = min(nodes[i].cap, c.cap)
			continue
		}
		index[cut.Key()] = len(nodes)
		nodes = append(nodes, node{cpus: cut, cap: c.cap})
	}

	// Smallest first, so children precede parents. Only the root has its
	// size, so it comes last.
	slices.SortFunc(nodes, func(a, b node) int {
		return cmp.Compare(a.cpus.Size(), b.cpus.Size())
	})

	// Done nodes with no parent yet. Each new node adopts those inside it.
	var tops []node
	for _, n := range nodes {
		took, covered := 0, 0
		rest := tops[:0]
		for _, t := range tops {
			if t.cpus.IsSubsetOf(n.cpus) {
				took += t.inner
				covered += t.cpus.Size()
			} else {
				rest = append(rest, t)
			}
		}
		n.inner = min(n.cap, took+n.cpus.Size()-covered)
		tops = append(rest, n)
	}

	return tops[len(tops)-1].inner
}

// Laminar reports whether the declared pools nest: any two are nested or
// disjoint. Then [Accounting.ExclusiveCapacity] is exact whatever goes
// exclusive. False does not mean inexact. The answer can change as users
// come and go, since userless pools are dropped.
func (a *Accounting) Laminar() bool {
	sets := make([]*CpuMask, 0, len(a.pools))
	for _, p := range a.pools {
		if !p.cpus.IsEmpty() {
			sets = append(sets, p.cpus)
		}
	}

	return laminar(sets)
}

// laminar reports whether any two of the given sets are nested or
// disjoint. It checks pairwise, as pools are few.
func laminar(sets []*CpuMask) bool {
	for i, s := range sets {
		for _, t := range sets[i+1:] {
			if s.Intersects(t) && !s.IsSubsetOf(t) && !t.IsSubsetOf(s) {
				return false
			}
		}
	}

	return true
}
