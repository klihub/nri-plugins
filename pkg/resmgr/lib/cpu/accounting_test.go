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
	"io"
	"testing"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/stretchr/testify/require"
)

type testUsage struct {
	id        string
	exclusive *CpuMask
	shared    *CpuMask
	charge    int
}

func (u *testUsage) ID() string   { return u.id }
func (u *testUsage) Name() string { return u.id }
func (u *testUsage) Charge() int  { return u.charge }

func (u *testUsage) Exclusive() CPUSet {
	if u.exclusive == nil {
		return NewCpuMask()
	}
	return u.exclusive
}

func (u *testUsage) Shared() CPUSet {
	if u.shared == nil {
		return NewCpuMask()
	}
	return u.shared
}

var (
	testAllCpus = NewCpuMask(cpuRange(0, 7)...)
	testNode0   = NewCpuMask(0, 1)
	testNode1   = NewCpuMask(2, 3)
	testCpu4    = NewCpuMask(4)
	testCpu5    = NewCpuMask(5)

	// testProbes are the CPU sets we check available capacity for to see
	// what an accounting looks like from the outside.
	testProbes = []*CpuMask{testAllCpus, testNode0, testNode1, testCpu4, testCpu5}
)

// testAccounting returns an accounting of CPUs 0-7 with two users sharing
// node0, and one user with CPU #4 allocated exclusively.
func testAccounting(t *testing.T) *Accounting {
	a := NewAccounting(testAllCpus)

	for _, u := range []*testUsage{
		{id: "shared1", shared: testNode0, charge: 500},
		{id: "shared2", shared: testNode0, charge: 250},
		{id: "exclusive", exclusive: testCpu4},
	} {
		require.NoError(t, a.Add(u), "add user %q", u.id)
	}

	return a
}

// testAvailable returns the available capacity of the probed CPU sets, the
// externally visible state of the accounting.
func testAvailable(a *Accounting) map[string]int {
	avail := map[string]int{}

	for _, cpus := range testProbes {
		avail[cpus.String()] = a.Available(cpus)
	}

	return avail
}

func TestAdd(t *testing.T) {
	t.Run("shared, exclusive and mixed usage", func(t *testing.T) {
		a := testAccounting(t)

		// Both users of node0 charge the same pool, CPU #4 is fully taken.
		require.Equal(t, map[string]int{
			"0-7": 6250, // 8000 - 1000 - 750
			"0-1": 1250, // 2000 - 750
			"2-3": 2000,
			"4":   0, // 1000 - 1000
			"5":   1000,
		}, testAvailable(a), "available capacity")

		require.NoError(t, a.Add(&testUsage{
			id:        "mixed",
			shared:    testNode1,
			charge:    250,
			exclusive: NewCpuMask(6, 7),
		}), "add mixed user")

		require.Equal(t, map[string]int{
			"0-7": 4000, // 8000 - 3000 - 750 - 250
			"0-1": 1250,
			"2-3": 1750, // 2000 - 250
			"4":   0,
			"5":   1000,
		}, testAvailable(a), "available capacity")
	})

	for _, tc := range []struct {
		name  string
		usage *testUsage
	}{
		{
			name:  "an existing user",
			usage: &testUsage{id: "shared1", shared: testNode1, charge: 100},
		},
		{
			name:  "overlapping shared and exclusive CPUs",
			usage: &testUsage{id: "overlap", shared: testNode0, exclusive: NewCpuMask(1)},
		},
		{
			name:  "a charge without shared CPUs",
			usage: &testUsage{id: "uncharged", exclusive: testCpu5, charge: 100},
		},
		{
			name:  "the exclusive CPU of another user",
			usage: &testUsage{id: "thief", shared: testNode1, exclusive: testCpu4},
		},
		{
			// Users of node0 share CPU #1, so it cannot be taken away from
			// them exclusively.
			name:  "an exclusive CPU of a pool in use",
			usage: &testUsage{id: "greedy", exclusive: NewCpuMask(1)},
		},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			a := testAccounting(t)
			before := testAvailable(a)

			require.Error(t, a.Add(tc.usage), "add user %q", tc.usage.id)

			// A rejected usage must not alter the accounting.
			require.Equal(t, before, testAvailable(a), "available capacity")
		})
	}
}

func TestDelete(t *testing.T) {
	a := testAccounting(t)
	before := testAvailable(a)

	require.Error(t, a.Delete("no such user"), "delete unknown user")
	require.Equal(t, before, testAvailable(a), "available capacity")

	require.NoError(t, a.Delete("shared1"), "delete user")
	require.Equal(t, 1750, a.Available(testNode0), "available in node0, 2000 - 250")
	require.Error(t, a.Delete("shared1"), "delete the same user twice")

	// Deleting a user releases its exclusive CPUs for others to allocate.
	require.NoError(t, a.Delete("exclusive"), "delete exclusive user")
	require.Equal(t, 1000, a.Available(testCpu4), "available on CPU #4")

	require.NoError(t, a.Add(&testUsage{id: "new", exclusive: testCpu4}),
		"reallocate CPU #4 exclusively")
	require.Equal(t, 0, a.Available(testCpu4), "available on CPU #4")
}

func TestUpdate(t *testing.T) {
	// Updating an unknown user logs a warning which we don't need to see.
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	t.Run("charge", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Update(&testUsage{
			id:     "shared1",
			shared: testNode0,
			charge: 1000,
		}), "update charge")

		require.Equal(t, 750, a.Available(testNode0), "available in node0, 2000 - 1250")
	})

	t.Run("shared CPUs", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Update(&testUsage{
			id:     "shared1",
			shared: testNode1,
			charge: 500,
		}), "update shared CPUs")

		require.Equal(t, 1750, a.Available(testNode0), "available in node0, 2000 - 250")
		require.Equal(t, 1500, a.Available(testNode1), "available in node1, 2000 - 500")
	})

	t.Run("exclusive CPUs", func(t *testing.T) {
		a := testAccounting(t)

		// An update must not conflict with the exclusive CPUs of the user
		// it replaces.
		require.NoError(t, a.Update(&testUsage{id: "exclusive", exclusive: testCpu4}),
			"update with unchanged exclusive CPUs")
		require.Equal(t, 0, a.Available(testCpu4), "available on CPU #4")

		require.NoError(t, a.Update(&testUsage{id: "exclusive", exclusive: testCpu5}),
			"update exclusive CPUs")
		require.Equal(t, 1000, a.Available(testCpu4), "available on CPU #4")
		require.Equal(t, 0, a.Available(testCpu5), "available on CPU #5")
	})

	t.Run("adds an unknown user", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Update(&testUsage{id: "new", shared: testNode1, charge: 250}),
			"update unknown user")
		require.Equal(t, 1750, a.Available(testNode1), "available in node1, 2000 - 250")
	})

	t.Run("keeps old user intact if rejected", func(t *testing.T) {
		a := testAccounting(t)

		// Update deletes before it adds, so a usage rejected by Add must
		// leave the deleted user reinstated with its original charge.
		require.Error(t, a.Update(&testUsage{
			id:        "shared1",
			shared:    testNode0,
			exclusive: NewCpuMask(1),
		}), "update with overlapping shared and exclusive CPUs")
		require.Equal(t, 1250, a.Available(testNode0),
			"available in node0 after a rejected update, 2000 - 750")

		// The exclusive CPUs of a reinstated user must be restored, too.
		require.Error(t, a.Update(&testUsage{
			id:        "exclusive",
			exclusive: testCpu5,
			charge:    100,
		}), "update with a charge without shared CPUs")
		require.Equal(t, 0, a.Available(testCpu4), "available on CPU #4")
		require.Equal(t, 1000, a.Available(testCpu5), "available on CPU #5")
	})
}

// Available takes any number of CPU sets, reporting the tightest limit of all
// of them, and the bounding set of the accounting without arguments.
func TestAvailableInSets(t *testing.T) {
	var (
		a     = NewAccounting(testAllCpus)
		node2 = NewCpuMask(4, 5)
		node3 = NewCpuMask(6, 7)
	)

	for _, u := range []*testUsage{
		{id: "node0", shared: testNode0, charge: 1500},
		{id: "node1", shared: testNode1, charge: 1000},
		{id: "node2", shared: node2, charge: 500},
	} {
		require.NoError(t, a.Add(u), "add usage %q", u.id)
	}

	// Without arguments, the bounding set of CPUs of the accounting.
	require.Equal(t, 5000, a.Available(), "available in all the CPUs, 8000 - 3000")
	require.Equal(t, a.Available(testAllCpus), a.Available(), "available")

	require.Equal(t, 500, a.Available(testNode0), "available in node0, 2000 - 1500")
	require.Equal(t, 1000, a.Available(testNode1), "available in node1, 2000 - 1000")
	require.Equal(t, 2000, a.Available(node3), "available in node3")

	// Several sets report the tightest of their limits.
	require.Equal(t, 500, a.Available(testNode0, testNode1),
		"available in node0 and node1")
	require.Equal(t, 1000, a.Available(testNode1, node3),
		"available in node1 and node3")
	require.Equal(t, 1000, a.Available(testNode1, node2, node3),
		"available in node1, node2 and node3")

	// Which is the tightest of what each of them reports on its own.
	for _, cpus := range [][]*CpuMask{
		{testNode0, testNode1},
		{testNode1, node3},
		{testNode1, node2, node3},
	} {
		tightest := a.Available(cpus[0])
		for _, c := range cpus[1:] {
			tightest = min(tightest, a.Available(c))
		}
		require.Equal(t, tightest, a.Available(cpus...), "available in %v", cpus)
	}
}

func TestAllocate(t *testing.T) {
	t.Run("a shared charge", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Allocate(&testUsage{
			id:     "new",
			shared: testNode0,
			charge: 1250,
		}), "allocate all the available capacity of node0")
		require.Equal(t, 0, a.Available(testNode0), "available in node0")

		require.Error(t, a.Allocate(&testUsage{
			id:     "greedy",
			shared: testNode0,
			charge: 50,
		}), "allocate from a pool without capacity")
	})

	t.Run("too large a shared charge", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		require.Error(t, a.Allocate(&testUsage{
			id:     "greedy",
			shared: testNode0,
			charge: 1251,
		}), "allocate more than the available capacity of node0")

		// A rejected allocation must not alter the accounting.
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("a charge limited by a larger pool", func(t *testing.T) {
		a := testAccounting(t)
		socket0 := NewCpuMask(cpuRange(0, 3)...)

		require.NoError(t, a.Allocate(&testUsage{
			id:     "socket0",
			shared: socket0,
			charge: 3000,
		}), "allocate from socket0")

		// Node0 has 1250 of its own capacity left, but socket0 only 250.
		require.Equal(t, 250, a.Available(testNode0), "available in node0")
		require.Error(t, a.Allocate(&testUsage{
			id:     "greedy",
			shared: testNode0,
			charge: 500,
		}), "allocate more from node0 than socket0 allows")
		require.NoError(t, a.Allocate(&testUsage{
			id:     "modest",
			shared: testNode0,
			charge: 250,
		}), "allocate what socket0 allows")
	})

	t.Run("exclusive CPUs", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Allocate(&testUsage{
			id:        "exclusive2",
			exclusive: NewCpuMask(5, 6),
		}), "allocate free CPUs exclusively")
		require.Equal(t, 0, a.Available(testCpu5), "available on CPU #5")

		require.Error(t, a.Allocate(&testUsage{
			id:        "thief",
			exclusive: testCpu4,
		}), "allocate a CPU which is taken")
	})

	t.Run("an exclusive CPU of a pool in use", func(t *testing.T) {
		a := testAccounting(t)

		// The users of node0 share CPUs #0 and #1 whatever capacity node0 has
		// left, so neither can be taken away from them exclusively.
		require.Error(t, a.Allocate(&testUsage{
			id:        "exclusive0",
			exclusive: NewCpuMask(0),
		}), "allocate a CPU of a pool in use")

		// A CPU which no pool in use contains always has its full capacity
		// available: dropping it from a set of CPUs takes 1000 of capacity
		// away without releasing any charge, so no set containing it can be
		// left with less, unless it is overcommitted already.
		require.NoError(t, a.Allocate(&testUsage{
			id:        "exclusive5",
			exclusive: testCpu5,
		}), "allocate a CPU which no pool in use contains")
	})

	t.Run("a usage Add rejects", func(t *testing.T) {
		a := testAccounting(t)

		require.Error(t, a.Allocate(&testUsage{
			id:     "shared1",
			shared: testNode1,
			charge: 100,
		}), "allocate for an existing user")
	})

	t.Run("a mixed usage which does not fit", func(t *testing.T) {
		a := testAccounting(t)

		// The charge does not fit node1, so the whole usage is refused, and
		// the exclusive CPU it asked for must be free afterwards.
		require.Error(t, a.Allocate(&testUsage{
			id:        "mixed",
			shared:    testNode1,
			charge:    2500,
			exclusive: testCpu5,
		}), "allocate a charge which does not fit, with an exclusive CPU")
		require.Equal(t, 2000, a.Available(testNode1), "available in node1")
		require.Equal(t, 1000, a.Available(testCpu5), "available on CPU #5")

		// The same usage with a charge which fits is taken.
		require.NoError(t, a.Allocate(&testUsage{
			id:        "mixed",
			shared:    testNode1,
			charge:    2000,
			exclusive: testCpu5,
		}), "allocate a charge which fits, with an exclusive CPU")
		require.Equal(t, 0, a.Available(testNode1), "available in node1")
		require.Equal(t, 0, a.Available(testCpu5), "available on CPU #5")
	})
}

func TestReallocate(t *testing.T) {
	// Reallocating an unknown user logs a warning which we don't need to see.
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	t.Run("a charge which fits", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Reallocate(&testUsage{
			id:     "shared1",
			shared: testNode0,
			charge: 1000,
		}), "reallocate a bigger charge which fits")
		require.Equal(t, 750, a.Available(testNode0), "available in node0, 2000 - 1250")
	})

	t.Run("a charge which does not fit", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		require.Error(t, a.Reallocate(&testUsage{
			id:     "shared1",
			shared: testNode0,
			charge: 2000,
		}), "reallocate a charge which does not fit")
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("another pool", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Reallocate(&testUsage{
			id:     "shared1",
			shared: testNode1,
			charge: 1500,
		}), "reallocate to another pool")
		require.Equal(t, 1750, a.Available(testNode0), "available in node0, 2000 - 250")
		require.Equal(t, 500, a.Available(testNode1), "available in node1, 2000 - 1500")
	})

	t.Run("exclusive CPUs", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Reallocate(&testUsage{id: "exclusive", exclusive: testCpu5}),
			"reallocate exclusive CPUs")
		require.Equal(t, 1000, a.Available(testCpu4), "available on CPU #4")
		require.Equal(t, 0, a.Available(testCpu5), "available on CPU #5")
	})

	t.Run("rolls back the whole usage", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		// The charge does not fit node1, so the user which held CPU #4
		// exclusively must be back with that CPU when we are done.
		require.Error(t, a.Reallocate(&testUsage{
			id:     "exclusive",
			shared: testNode1,
			charge: 2500,
		}), "reallocate a charge which does not fit")
		require.Equal(t, before, testAvailable(a), "available capacity")
		require.Equal(t, 0, a.Available(testCpu4), "available on CPU #4")
	})

	t.Run("an unknown user", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		// Reallocating a user we don't know allocates it, if it fits.
		require.NoError(t, a.Reallocate(&testUsage{
			id:     "new",
			shared: testNode1,
			charge: 500,
		}), "reallocate an unknown user")
		require.Equal(t, 1500, a.Available(testNode1), "available in node1, 2000 - 500")

		require.NoError(t, a.Release("new"), "release the user")
		require.Error(t, a.Reallocate(&testUsage{
			id:     "new",
			shared: testNode1,
			charge: 2500,
		}), "reallocate an unknown user which does not fit")
		require.Equal(t, before, testAvailable(a), "available capacity")
	})
}

func TestRelease(t *testing.T) {
	a := testAccounting(t)
	before := testAvailable(a)

	require.Error(t, a.Release("no such user"), "release an unknown user")
	require.Equal(t, before, testAvailable(a), "available capacity")

	require.NoError(t, a.Allocate(&testUsage{
		id:        "mixed",
		shared:    testNode1,
		charge:    1500,
		exclusive: testCpu5,
	}), "allocate a charge and an exclusive CPU")
	require.Equal(t, 500, a.Available(testNode1), "available in node1")
	require.Equal(t, 0, a.Available(testCpu5), "available on CPU #5")

	require.NoError(t, a.Release("mixed"), "release the user")
	require.Equal(t, before, testAvailable(a), "available capacity after release")
	require.Error(t, a.Release("mixed"), "release the same user twice")
}

func TestOvercommit(t *testing.T) {
	t.Run("a pool", func(t *testing.T) {
		a := testAccounting(t)
		require.Equal(t, 0, a.Overcommit(), "overcommit")

		// Add takes any valid usage, however much capacity it asks for.
		require.NoError(t, a.Add(&testUsage{
			id:     "greedy",
			shared: testNode0,
			charge: 2000,
		}), "add a usage which does not fit")
		require.Equal(t, 750, a.Overcommit(), "overcommit, 2750 charged on 2000")

		require.NoError(t, a.Delete("greedy"), "delete the usage")
		require.Equal(t, 0, a.Overcommit(), "overcommit")

		// Allocate refuses the same usage, so it cannot overcommit anything.
		require.Error(t, a.Allocate(&testUsage{
			id:     "greedy",
			shared: testNode0,
			charge: 2000,
		}), "allocate a usage which does not fit")
		require.Equal(t, 0, a.Overcommit(), "overcommit")
	})

	t.Run("an implicit pool", func(t *testing.T) {
		a := NewAccounting(NewCpuMask(cpuRange(0, 3)...))

		// Each of these fits its own pool, and the pseudo-pool 1,2 has room
		// to spare. Only their union, CPUs 0-3, is over its capacity, with
		// 4500 charged on 4000.
		for _, u := range []*testUsage{
			{id: "left", shared: NewCpuMask(0, 1), charge: 2000},
			{id: "right", shared: NewCpuMask(2, 3), charge: 2000},
			{id: "middle", shared: NewCpuMask(1, 2), charge: 500},
		} {
			require.NoError(t, a.Add(u), "add usage %q", u.id)
		}

		require.Equal(t, 500, a.Overcommit(), "overcommit of CPUs 0-3")
		require.Equal(t, 500, a.Overcommit(NewCpuMask(cpuRange(0, 3)...)),
			"overcommit of CPUs 0-3")

		// CPUs 0-3 limit every one of those pools, so the shortfall of the
		// union surfaces from any of them.
		require.Equal(t, -500, a.Available(NewCpuMask(0, 1)), "available in 0,1")
		require.Equal(t, -500, a.Available(NewCpuMask(1, 2)), "available in 1,2")
	})
}

// Overcommit given some CPU sets must only report the limits which constrain
// those very CPUs, so that a caller can tell an overcommitted part of the
// system from the rest of it.
func TestOvercommitOfCpus(t *testing.T) {
	a := NewAccounting(testAllCpus)

	// CPUs 0-3 are over their capacity by 500, CPUs 4-7 are untouched.
	for _, u := range []*testUsage{
		{id: "left", shared: NewCpuMask(0, 1), charge: 2000},
		{id: "right", shared: NewCpuMask(2, 3), charge: 2000},
		{id: "middle", shared: NewCpuMask(1, 2), charge: 500},
	} {
		require.NoError(t, a.Add(u), "add usage %q", u.id)
	}

	for _, tc := range []struct {
		name   string
		cpus   []*CpuMask
		expect int
	}{
		{name: "the whole accounting", expect: 500},
		{name: "a pool over its capacity", cpus: []*CpuMask{NewCpuMask(0, 1)}, expect: 500},
		{name: "a single CPU of it", cpus: []*CpuMask{NewCpuMask(0)}, expect: 500},
		{
			// CPUs 0-7 have 3500 of capacity left as a single set. Asking
			// about a set of CPUs says nothing about the narrower sets
			// within it, only about the sets containing it.
			name:   "all the CPUs as a single set",
			cpus:   []*CpuMask{testAllCpus},
			expect: 0,
		},
		{name: "CPUs with capacity left", cpus: []*CpuMask{NewCpuMask(4, 5)}, expect: 0},
		{
			name:   "several sets with capacity left",
			cpus:   []*CpuMask{NewCpuMask(4, 5), NewCpuMask(6, 7), testCpu4},
			expect: 0,
		},
		{
			name:   "several sets, one of them over",
			cpus:   []*CpuMask{NewCpuMask(4, 5), NewCpuMask(1)},
			expect: 500,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expect, a.Overcommit(tc.cpus...), "overcommit")

			// For a single set of CPUs this is what Available reports.
			if len(tc.cpus) == 1 {
				require.Equal(t, tc.expect, max(-a.Available(tc.cpus[0]), 0),
					"available in %s", tc.cpus[0])
			}
		})
	}
}

// Asking Overcommit about each pool in use tells which parts of the system are
// over their capacity and by how much, while the accounting as a whole only
// reports the worst of them.
func TestOvercommits(t *testing.T) {
	var (
		a     = NewAccounting(testAllCpus)
		node0 = NewCpuMask(0, 1)
		node1 = NewCpuMask(2, 3)
		node2 = NewCpuMask(4, 5)
	)

	// Add takes these however much capacity they ask for, so node0 and node2
	// end up over the capacity of their pools, while all the CPUs together
	// still have capacity left.
	for _, u := range []*testUsage{
		{id: "node0", shared: node0, charge: 2500},
		{id: "node1", shared: node1, charge: 1500},
		{id: "node2", shared: node2, charge: 2800},
		{id: "exclusive", exclusive: NewCpuMask(6)},
	} {
		require.NoError(t, a.Add(u), "add usage %q", u.id)
	}

	require.Equal(t, 200, a.Available(testAllCpus),
		"available in all the CPUs, 8000 - 1000 - 6800")

	expect := map[string]int{
		node0.Key(): 500, // 2000 - 2500
		node1.Key(): 0,   // 2000 - 1500
		node2.Key(): 800, // 2000 - 2800
	}

	// Note that the user without shared CPUs is not in a pool of any CPUs, so
	// there is nothing to report for it.
	pools := a.UsedPools()
	require.Len(t, pools, len(expect), "pools in use")
	require.NotContains(t, pools, NewCpuMask().Key(), "the pool without CPUs")

	overcommit := map[string]int{}
	for key, cpus := range pools {
		require.Equal(t, key, cpus.Key(), "key of pool %s", cpus)
		overcommit[key] = a.Overcommit(cpus)
	}
	require.Equal(t, expect, overcommit, "overcommit of the pools in use")

	// CollectOvercommits reports the same for the pools in use, in a single call.
	require.Equal(t, expect, a.CollectOvercommits(), "overcommit of the pools in use")
	require.Equal(t, map[string]int{node0.Key(): 500, node2.Key(): 800},
		a.CollectOvercommits(node0, node2), "overcommit of the given pools")

	// The accounting as a whole is as overcommitted as its worst pool.
	require.Equal(t, 800, a.Overcommit(), "overcommit of the accounting")

	// Releasing the worst offender leaves the other one to report, and takes
	// its pool out of use altogether.
	require.NoError(t, a.Release("node2"), "release the user of node2")
	require.NotContains(t, a.UsedPools(), node2.Key(), "pool node2")
	require.NotContains(t, a.CollectOvercommits(), node2.Key(), "pool node2")
	require.Equal(t, 500, a.Overcommit(), "overcommit of the accounting")
}

func TestCarveOut(t *testing.T) {
	t.Run("CPUs of a pool in use", func(t *testing.T) {
		a := testAccounting(t)
		cpu1 := NewCpuMask(1)

		// CPU #1 is shared by both users of node0, so it cannot be allocated
		// exclusively before it is carved out of their shared CPUs.
		require.Error(t, a.Allocate(&testUsage{id: "exclusive1", exclusive: cpu1}),
			"allocate a CPU of a pool in use")

		usages, err := a.CarveOut(cpu1)
		require.NoError(t, err, "carve out CPU #1")
		require.Len(t, usages, 2, "altered usages")

		for i, id := range []string{"shared1", "shared2"} {
			require.Equal(t, id, usages[i].ID(), "altered usage #%d", i)
			require.Equal(t, []int{0}, usages[i].Shared().List(),
				"shared CPUs of %q", id)
		}

		// Both users now share CPU #0 alone, so it is short of capacity,
		// while CPU #1 can be allocated exclusively.
		require.Equal(t, 250, a.Available(NewCpuMask(0)), "available on CPU #0")
		require.NoError(t, a.Allocate(&testUsage{id: "exclusive1", exclusive: cpu1}),
			"allocate the carved out CPU")
	})

	t.Run("CPUs nobody shares", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		usages, err := a.CarveOut(testCpu5)
		require.NoError(t, err, "carve out CPU #5")
		require.Empty(t, usages, "altered usages")
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("CPUs allocated exclusively", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		_, err := a.CarveOut(testCpu4)
		require.Error(t, err, "carve out a CPU which is allocated exclusively")
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("all the CPUs of a charged user", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		_, err := a.CarveOut(testNode0)
		require.Error(t, err, "carve out all the CPUs of a charged user")
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("more than the rest of a pool can take", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.Allocate(&testUsage{
			id:     "more",
			shared: testNode0,
			charge: 500,
		}), "charge node0 with more than a single CPU can take")

		before := testAvailable(a)

		_, err := a.CarveOut(NewCpuMask(1))
		require.Error(t, err, "carve out a CPU the users of node0 need")
		require.Equal(t, before, testAvailable(a), "available capacity")
		require.Equal(t, 0, a.Overcommit(), "overcommit")
	})
}

func TestPutBack(t *testing.T) {
	t.Run("the inverse of CarveOut", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)
		cpu1 := NewCpuMask(1)

		carved, err := a.CarveOut(cpu1)
		require.NoError(t, err, "carve out CPU #1")
		require.NoError(t, a.Allocate(&testUsage{id: "exclusive1", exclusive: cpu1}),
			"allocate the carved out CPU")

		// CPU #1 is in use, so it cannot be given back yet.
		_, err = a.PutBack(cpu1, carved)
		require.Error(t, err, "put back a CPU which is allocated exclusively")

		require.NoError(t, a.Release("exclusive1"), "release the exclusive CPU")

		// The usages CarveOut returned identify the users to give it back to.
		usages, err := a.PutBack(cpu1, carved)
		require.NoError(t, err, "put back CPU #1")
		require.Len(t, usages, 2, "altered usages")

		for i, id := range []string{"shared1", "shared2"} {
			require.Equal(t, id, usages[i].ID(), "altered usage #%d", i)
			require.Equal(t, []int{0, 1}, usages[i].Shared().List(),
				"shared CPUs of %q", id)
		}

		// Carving out and giving back must leave the accounting as it was.
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("CPUs the users already share", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		usages, err := a.PutBack(NewCpuMask(1), []Usage{&testUsage{id: "shared1"}})
		require.NoError(t, err, "put back a CPU which is shared already")
		require.Empty(t, usages, "altered usages")
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("an unknown user", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(a)

		_, err := a.PutBack(testCpu5, []Usage{&testUsage{id: "no such user"}})
		require.Error(t, err, "put back CPUs to an unknown user")
		require.Equal(t, before, testAvailable(a), "available capacity")
	})

	t.Run("CPUs of another pool", func(t *testing.T) {
		a := testAccounting(t)

		// Nothing ties the CPUs to the pool they were carved out of, they can
		// be given to any user, widening its pool.
		usages, err := a.PutBack(testCpu5, []Usage{&testUsage{id: "shared1"}})
		require.NoError(t, err, "put CPU #5 into the pool of a user")
		require.Len(t, usages, 1, "altered usages")
		require.Equal(t, []int{0, 1, 5}, usages[0].Shared().List(), "shared CPUs")

		// The pool of shared1 is now 0,1,5 while shared2 still shares 0,1, and
		// node0 is a subset of the wider pool, so both charges limit it.
		require.Equal(t, 2250, a.Available(NewCpuMask(0, 1, 5)),
			"available in 0,1,5, 3000 - 500 - 250")
		require.Equal(t, 1750, a.Available(testNode0), "available in node0, 2000 - 250")
	})
}

// Every user must be in the pool of its very own shared CPUs, so that all the
// users of a pool are accounted for against the same set of CPUs. Carving CPUs
// out of a pool and giving them back must keep that true.
func TestUserPools(t *testing.T) {
	a := testAccounting(t)

	verify := func(when string) {
		for id, u := range a.users {
			require.True(t, u.shared.Equals(u.pool.cpus),
				"%s: user %q shares %s but is in pool %s", when, id, u.shared, u.pool.cpus)
			require.Same(t, a.pools[u.pool.cpus.Key()], u.pool,
				"%s: pool %s of user %q", when, u.pool.cpus, id)
		}
	}

	verify("initially")

	carved, err := a.CarveOut(NewCpuMask(1))
	require.NoError(t, err, "carve out CPU #1")
	verify("after carving out CPU #1")

	// Both users of node0 must have moved to the pool of the CPUs left.
	require.Len(t, a.users["shared1"].pool.users, 2, "users in the carved pool")
	require.Same(t, a.users["shared1"].pool, a.users["shared2"].pool, "pool")

	// The pool they came from is gone, it has no users left.
	require.NotContains(t, a.pools, testNode0.Key(), "pool node0")

	_, err = a.PutBack(NewCpuMask(1), carved)
	require.NoError(t, err, "put back CPU #1")
	verify("after putting CPU #1 back")

	require.Contains(t, a.pools, testNode0.Key(), "pool node0")
	require.Len(t, a.users["shared1"].pool.users, 2, "users in pool node0")
}

func TestEmptyPools(t *testing.T) {
	a := NewAccounting(testAllCpus)
	require.Len(t, a.pools, 1, "pools of a new accounting")

	require.NoError(t, a.Add(&testUsage{id: "node1", shared: testNode1, charge: 500}),
		"add a user of node1")
	require.Len(t, a.pools, 2, "pools")

	// A pool is dropped with its last user, so that the pools of a churning
	// accounting do not pile up.
	require.NoError(t, a.Delete("node1"), "delete the user of node1")
	require.Len(t, a.pools, 1, "pools")
	require.NotContains(t, a.pools, testNode1.Key(), "pool node1")

	// So is the pool of the users without any shared CPUs.
	require.NoError(t, a.Add(&testUsage{id: "exclusive", exclusive: testCpu4}),
		"add a user without shared CPUs")
	require.Len(t, a.pools, 2, "pools")
	require.NoError(t, a.Delete("exclusive"), "delete the user")
	require.Len(t, a.pools, 1, "pools")

	// Our bounding set of CPUs stays, whether it has users or not.
	require.NoError(t, a.Add(&testUsage{id: "all", shared: testAllCpus, charge: 500}),
		"add a user of all the CPUs")
	require.Len(t, a.pools, 1, "pools, the user joins the bounding pool")
	require.NoError(t, a.Delete("all"), "delete the user of all the CPUs")
	require.Contains(t, a.pools, testAllCpus.Key(), "the bounding pool")
}

// Users declaring the same shared CPUs must all end up in a single pool, so
// that between them they add a single set of CPUs for Available to consider.
// Otherwise every user adds a nearly identical, overlapping set, with none of
// them being a subset of another, which is the worst case for the enumeration.
// Since this is invisible from the outside, we check it internally.
func TestPoolPerSharedCpus(t *testing.T) {
	a := NewAccounting(testAllCpus)
	sets := len(a.constraints())

	// The exclusive CPUs must avoid node0, the shared CPUs of the users.
	for cpu := 2; cpu <= 7; cpu++ {
		require.NoError(t, a.Add(&testUsage{
			id:        fmt.Sprintf("user #%d", cpu),
			shared:    testNode0,
			exclusive: NewCpuMask(cpu),
			charge:    100,
		}), "add user #%d", cpu)
	}

	require.Equal(t, sets+1, len(a.constraints()), "constraint sets of 6 users")
}

func TestAvailable(t *testing.T) {
	type testCase struct {
		name   string
		cpus   *CpuMask
		usage  []*testUsage
		expect map[string]int
	}

	for _, tc := range []*testCase{
		{
			name: "empty accounting",
			cpus: NewCpuMask(0, 1, 2, 3, 4, 5, 6, 7),
			expect: map[string]int{
				"0-7": 8000,
				"0-1": 2000,
				"3":   1000,
			},
		},
		{
			name: "nested pools",
			cpus: NewCpuMask(0, 1, 2, 3, 4, 5, 6, 7),
			usage: []*testUsage{
				{id: "user1@node0", shared: NewCpuMask(0, 1), charge: 750},
				{id: "user2@node0", shared: NewCpuMask(0, 1), charge: 250},
				{id: "user1@socket0", shared: NewCpuMask(0, 1, 2, 3), charge: 250},
			},
			expect: map[string]int{
				"0-1": 1000, // 2000 - 1000
				"2-3": 2000, // own pool is empty, socket allows more
				"0-3": 2750, // 4000 - 1250
				"4-7": 4000,
				"0-7": 6750, // 8000 - 1250
			},
		},
		{
			name: "exclusive allocation",
			cpus: NewCpuMask(0, 1, 2, 3),
			usage: []*testUsage{
				{id: "exclusive", exclusive: NewCpuMask(0)},
				{id: "shared", shared: NewCpuMask(2, 3), charge: 500},
			},
			expect: map[string]int{
				"0":   0,    // 1000 - 1000
				"0-1": 1000, // 2000 - 1000
				"2-3": 1500, // 2000 - 500
				"0-3": 2500, // 4000 - 1000 - 500
			},
		},
		{
			// The charge of a user is limited to the CPUs it shares, while
			// its exclusive CPUs are taken from every set containing them.
			name: "exclusive CPUs with a shared charge",
			cpus: NewCpuMask(0, 1, 2, 3),
			usage: []*testUsage{
				{
					id:        "both",
					exclusive: NewCpuMask(0),
					shared:    NewCpuMask(1, 2, 3),
					charge:    2000,
				},
			},
			expect: map[string]int{
				"0":   0,    // 1000 - 1000, exclusively allocated
				"1":   1000, // limited by pool 1-3, 3000 - 2000
				"1-3": 1000, // 3000 - 2000
				"0-3": 1000, // 4000 - 1000 - 2000
			},
		},
		{
			name: "multiple exclusive CPUs",
			cpus: NewCpuMask(0, 1, 2, 3),
			usage: []*testUsage{
				{id: "exclusive", exclusive: NewCpuMask(0, 1)},
			},
			expect: map[string]int{
				"0":   0,
				"0-1": 0,
				"2-3": 2000,
				"0-3": 2000, // 4000 - 2000
			},
		},
		{
			// Nothing prevents overcommitting a pool. Available reports the
			// excess as negative capacity for the pools limiting the query,
			// but slack elsewhere can hide it, as it does for 4-7 here.
			name: "overcommitted pool",
			cpus: NewCpuMask(0, 1, 2, 3, 4, 5, 6, 7),
			usage: []*testUsage{
				{id: "user1@node0", shared: NewCpuMask(0, 1), charge: 2500},
			},
			expect: map[string]int{
				"0-1": -500, // 2000 - 2500
				"0-3": 1500, // 4000 - 2500
				"4-7": 4000, // unaffected, no pool of ours limits it
				"0-7": 5500, // 8000 - 2500
			},
		},
		{
			// 0,1 and 2,3 do not intersect, but the pseudo-pool 1,2 chains
			// them together, so the union 0-3 is a limit which needs checking.
			name: "chained pseudo-pool",
			cpus: NewCpuMask(0, 1, 2, 3),
			usage: []*testUsage{
				{id: "left", shared: NewCpuMask(0, 1), charge: 500},
				{id: "middle", shared: NewCpuMask(1, 2), charge: 1000},
				{id: "right", shared: NewCpuMask(2, 3), charge: 1500},
			},
			expect: map[string]int{
				"0":   1000, // 4000 - 3000, all of 0-3 limits us
				"0-1": 1000, // 4000 - 3000, ditto (not 2000 - 500)
				"1-2": 500,  // 3000 - 2500, 1-3 limits us
				"2-3": 500,  // 2000 - 1500
				"0-3": 1000, // 4000 - 3000
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAccounting(tc.cpus)
			for _, u := range tc.usage {
				require.NoError(t, a.Add(u), "add usage %s", u.id)
			}

			for cpus, expect := range tc.expect {
				mask, err := ParseCpuMask(cpus)
				require.NoError(t, err, "parse CPUs %q", cpus)
				require.Equal(t, expect, a.Available(mask), "available in %q", cpus)
			}
		})
	}
}
