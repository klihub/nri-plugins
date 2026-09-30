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
	"slices"
)

// hypothetical is the state the accounting would be in with one usage added
// and at most one removed, computed without altering anything.
type hypothetical struct {
	exclusive *CpuMask            // a.exclusive with add's and without dropID's
	effective map[string]*CpuMask // pool declared Key() -> hypothetical effective set
	charges   map[string]int      // pool declared Key() -> hypothetical total charge
}

// hypothetical builds the state after dropping the user dropID, if any, and
// then accounting for add. Nothing is mutated. A pool losing its last user is
// kept; with no charge, it can only make an answer tighter.
func (a *Accounting) hypothetical(add *user, dropID string) *hypothetical {
	h := &hypothetical{
		exclusive: a.exclusive,
		effective: make(map[string]*CpuMask, len(a.pools)+1),
		charges:   make(map[string]int, len(a.pools)+1),
	}

	if old, ok := a.users[dropID]; ok {
		h.exclusive = h.exclusive.Difference(old.exclusive)
	}
	if add != nil {
		h.exclusive = h.exclusive.Union(add.exclusive)
	}

	for key, p := range a.pools {
		eff := p.cpus
		if p.cpus.Intersects(h.exclusive) {
			eff = p.cpus.Difference(h.exclusive)
		}
		h.effective[key] = eff

		charge := 0
		for _, u := range p.users {
			if u.id != dropID {
				charge += u.charge
			}
		}
		h.charges[key] = charge
	}

	// The candidate's own pool, which may not exist yet.
	if add != nil && !add.shared.IsEmpty() {
		key := add.shared.Key()
		if _, ok := h.effective[key]; !ok {
			h.effective[key] = add.shared.Difference(h.exclusive)
			h.charges[key] = 0
		}
		h.charges[key] += add.charge
	}

	return h
}

// charged returns the charged set of every pool as it would stand.
func (h *hypothetical) charged() []chargedSet {
	sets := make([]chargedSet, 0, len(h.effective))

	for key, eff := range h.effective {
		sets = append(sets, chargedSet{cpus: eff, charge: h.charges[key]})
	}

	return sets
}

// lackingCapacity returns the milli-CPU by which the most overcommitted set
// of CPUs would be over capacity, or 0, and whether that is exact; if not,
// it is a lower bound. 0 means the usage fits only if validate accepted it:
// choking shows no lacking capacity.
func (h *hypothetical) lackingCapacity() (int, bool) {
	lacking, _, exact := h.capacity(nil)

	return lacking, exact
}

// capacity returns, from one enumeration, the lacking capacity, the milli-CPU
// the set own would have left, and whether both are exact. own may be empty.
// If it is not a charged seed, it rides along as an extra probe, and the
// verdict never reads its answer.
func (h *hypothetical) capacity(own *CpuMask) (int, int, bool) {
	pools := h.charged()

	probes := distinct(pools, true)
	charged := len(probes)

	// Where own's answer will be, or -1 when none was asked for.
	at := -1
	if !own.IsEmpty() {
		at = slices.IndexFunc(probes, func(p *CpuMask) bool {
			return p.Key() == own.Key()
		})
		if at < 0 {
			at = len(probes)
			probes = append(probes, own)
		}
	}

	if len(probes) == 0 {
		return 0, 0, true
	}

	limits, exact := traverse(probes, distinct(pools, false), chargeIn(h.exclusive, pools))

	headroom := 0
	if at >= 0 {
		headroom = limits[at]
	}

	lacking := 0
	if charged > 0 {
		lacking = max(-slices.Min(limits[:charged]), 0)
	}

	return lacking, headroom, exact
}
