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

// ErrExpiredOffer reports that the accounting changed after an offer was made.
var ErrExpiredOffer = errors.New("offer expired")

// Offer is an allocation which has been checked but not made. Any number can
// coexist; committing one expires all of them, as does any other change to the
// accounting.
type Offer struct {
	a        *Accounting
	version  int64
	user     *user
	cpus     *CpuMask
	updates  []Change
	replaces string // ID of the user this offer replaces, or ""

	// The room the requester's pool would have left, and whether that
	// is exact.
	headroom      int
	headroomExact bool
}

// GetOffer returns an offer for the given usage, which Commit turns into an
// allocation. It returns a *CapacityError if the usage does not fit. If the
// accounting already holds the ID, the offer replaces that user: the usage is
// its complete new declaration, judged with the old usage gone.
func (a *Accounting) GetOffer(usage *CpuUsage) (*Offer, error) {
	if usage == nil {
		return nil, errNilUsage
	}

	replaces := ""
	if _, exists := a.users[usage.ID]; exists {
		replaces = usage.ID
	}

	u, err := a.validate(usage, replaces)
	if err != nil {
		return nil, err
	}

	h := a.hypothetical(u, replaces)
	// Exactness is not acted on: a truncated check may offer a usage
	// which overcommits a set it never visited. Whether to refuse that is
	// open; Admits reports it.
	// own is the requester's pool as it would stand, nil without one.
	var own *CpuMask
	if !u.shared.IsEmpty() {
		own = h.effective[u.shared.Key()]
	}

	over, headroom, exact := h.capacity(own)
	if over > 0 {
		return nil, fmt.Errorf("user %q: %w", usage.ID, &CapacityError{Lacking: over})
	}

	o := a.newOffer(u, h)
	o.replaces = replaces
	o.headroom, o.headroomExact = headroom, exact

	return o, nil
}

// newOffer records what the hypothetical implies, so Commit is assignment only.
func (a *Accounting) newOffer(u *user, h *hypothetical) *Offer {
	cpus, updates := a.effects(u, h)

	return &Offer{
		a:       a,
		version: a.version,
		user:    u,
		cpus:    cpus,
		updates: updates,
	}
}

// IsValid reports whether the offer can still be committed.
func (o *Offer) IsValid() bool { return o.version == o.a.version }

// Cpus returns the CPUs the requester would be pinned to.
func (o *Offer) Cpus() *CpuMask { return o.cpus.Clone() }

// Updates returns the other users which would have to be re-pinned. The
// requester is not among them; see [Offer.Cpus].
func (o *Offer) Updates() []Change { return slices.Clone(o.updates) }

// Headroom returns the milli-CPU the requester's declared pool would have left
// once this offer is committed, and whether that is exact rather than an upper
// bound. That is how much a shared charge on that pool could still grow, by
// this requester or another. It does not bound exclusive takes or other pools;
// [Accounting.Admits] answers those. It fails for a usage declaring no shared
// CPUs, and means nothing once [Offer.IsValid] is false.
func (o *Offer) Headroom() (int, bool, error) {
	if o.user.shared.IsEmpty() {
		return 0, false, fmt.Errorf("user %q: declares no shared CPUs, so there is "+
			"no pool to report the headroom of", o.user.id)
	}

	return o.headroom, o.headroomExact, nil
}

// Commit turns the offer into an allocation, expiring every other offer.
func (o *Offer) Commit() (*CpuMask, []Change, error) {
	if !o.IsValid() {
		return nil, nil, fmt.Errorf("%w: version %d != %d",
			ErrExpiredOffer, o.version, o.a.version)
	}

	// Remove the replaced user first: the hypothetical was judged without
	// it, so keeping it would count it twice.
	if o.replaces != "" {
		if _, err := o.a.remove(o.replaces); err != nil {
			// Cannot happen: the version check proves no change.
			return nil, nil, fmt.Errorf("user %q: failed to replace: %w",
				o.replaces, err)
		}
	}

	// account, not insert: validated in GetOffer, unchanged since.
	o.a.account(o.user)

	return o.Cpus(), o.Updates(), nil
}

// effects returns the CPUs the user would run on under the hypothetical, and
// the re-pinning of every other user whose pool narrows or widens. Nothing is
// mutated.
func (a *Accounting) effects(u *user, h *hypothetical) (*CpuMask, []Change) {
	// Its own exclusive CPUs are in no effective set; add them back.
	cpus := sealed(h.effective[u.shared.Key()].Union(u.exclusive))

	var updates []Change

	for key, p := range a.pools {
		if p.eff().Equals(h.effective[key]) {
			continue
		}
		for _, other := range p.users {
			if other.id == u.id {
				continue
			}
			updates = append(updates, Change{
				id:   other.id,
				name: other.name,
				cpus: sealed(h.effective[key].Union(other.exclusive)),
			})
		}
	}

	return cpus, sortChanges(updates)
}

// changesIn returns the re-pinning every user of the given pools needs: what
// its pool has left plus what it holds exclusively. It is the counterpart of
// effects for a change already made.
func changesIn(pools []*pool) []Change {
	var changes []Change

	for _, p := range pools {
		for _, u := range p.users {
			changes = append(changes, Change{
				id:   u.id,
				name: u.name,
				cpus: sealed(p.eff().Union(u.exclusive)),
			})
		}
	}

	return sortChanges(changes)
}

// sortChanges orders changes by user ID, for a stable report.
func sortChanges(changes []Change) []Change {
	slices.SortFunc(changes, func(a, b Change) int { return cmp.Compare(a.id, b.id) })
	return changes
}
