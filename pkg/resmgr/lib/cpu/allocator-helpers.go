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
