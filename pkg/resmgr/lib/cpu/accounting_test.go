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
	"slices"
	"testing"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/stretchr/testify/require"
)

var (
	testAllCpus = NewCpuMask(cpuRange(0, 7)...)
	testNode0   = NewCpuMask(0, 1)
	testNode1   = NewCpuMask(2, 3)
	testCpu4    = NewCpuMask(4)
	testCpu5    = NewCpuMask(5)

	// testProbes are the CPU sets testAvailable reports on.
	testProbes = []*CpuMask{testAllCpus, testNode0, testNode1, testCpu4, testCpu5}
)

// These wrappers drop the change report, for tests which only check
// whether a usage is taken.
func allocate(a *Accounting, usage *CpuUsage) error {
	_, _, err := a.Allocate(usage)
	return err
}

func reallocate(a *Accounting, usage *CpuUsage) error {
	_, _, err := a.Reallocate(usage)
	return err
}

func release(a *Accounting, id string) error {
	_, err := a.Release(id)
	return err
}

// deleteUser is the unchecked removal with the altered pools dropped.
func deleteUser(a *Accounting, id string) error {
	_, err := a.remove(id)
	return err
}

func cpuRange(start, end int) []int {
	cpus := make([]int, end-start+1)
	for i := range cpus {
		cpus[i] = start + i
	}
	return cpus
}

// testAccounting returns an accounting of CPUs 0-7 with two users sharing
// node0, and one user with CPU #4 allocated exclusively.
func testAccounting(t *testing.T) *Accounting {
	a := NewAccounting(testAllCpus)

	for _, u := range []*CpuUsage{
		{ID: "shared1", Name: "shared1", Shared: testNode0, Charge: 500},
		{ID: "shared2", Name: "shared2", Shared: testNode0, Charge: 250},
		{ID: "exclusive", Name: "exclusive", Exclusive: testCpu4},
	} {
		require.NoError(t, a.insert(u), "add user %q", u.ID)
	}

	return a
}

// available returns the capacity available in cpus. It fails the test
// if the enumeration was cut short.
func available(t *testing.T, a *Accounting, cpus *CpuMask) int {
	t.Helper()

	avail, exact := a.Available(cpus)
	require.True(t, exact, "enumeration for %s was cut short", cpus)

	return avail
}

// lackingCapacity returns the milli-CPU u lacks. It fails the test if
// the enumeration was cut short.
func lackingCapacity(t *testing.T, a *Accounting, u *user, dropID string) int {
	t.Helper()

	over, exact := a.hypothetical(u, dropID).lackingCapacity()
	require.True(t, exact, "enumeration for %q was cut short", u.id)

	return over
}

// overcommitOf returns how far cpus, or the whole accounting, are over
// capacity. It fails the test if the enumeration was cut short.
func overcommitOf(t *testing.T, a *Accounting, cpus ...*CpuMask) int {
	t.Helper()

	over, exact := a.overcommit(cpus...)
	require.True(t, exact, "overcommit enumeration was cut short")

	return over
}

// testAvailable returns the available capacity of each of testProbes.
func testAvailable(t *testing.T, a *Accounting) map[string]int {
	t.Helper()

	avail := map[string]int{}

	for _, cpus := range testProbes {
		avail[cpus.String()] = available(t, a, cpus)
	}

	return avail
}

func TestCpuUsageValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *CpuUsage
		ok    bool
	}{
		{"nil usage", nil, false},
		{"nil masks are the empty set", &CpuUsage{ID: "a", Name: "a"}, true},
		{"empty ID", &CpuUsage{Name: "a", Shared: testNode0, Charge: 1}, false},
		{"empty name", &CpuUsage{ID: "a", Shared: testNode0, Charge: 1}, false},
		{"charge without shared CPUs", &CpuUsage{ID: "a", Name: "a", Charge: 1}, false},
		{"negative charge", &CpuUsage{ID: "a", Name: "a", Shared: testNode0, Charge: -1}, false},
		{"exclusive outside the bounding set",
			&CpuUsage{ID: "a", Name: "a", Exclusive: NewCpuMask(99)}, false},
		{"nil shared with exclusive CPUs",
			&CpuUsage{ID: "a", Name: "a", Exclusive: testCpu5}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAccounting(testAllCpus)
			err := allocate(a, tc.usage)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

// TestNilUsageIsRefused pins that every entry point taking a usage
// refuses a nil one with an error instead of panicking.
func TestNilUsageIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*Accounting) error
	}{
		{"insert", func(a *Accounting) error { return a.insert(nil) }},
		{"Allocate", func(a *Accounting) error { return allocate(a, nil) }},
		{"update", func(a *Accounting) error { return a.update(nil) }},
		{"Reallocate", func(a *Accounting) error { return reallocate(a, nil) }},
		{"GetOffer", func(a *Accounting) error {
			_, err := a.GetOffer(nil)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAccounting(testAllCpus)
			require.NotPanics(t, func() {
				require.ErrorIs(t, tc.call(a), errNilUsage, "nil usage refused")
			}, "nil usage handled without panicking")
			require.Empty(t, a.users, "no user taken into account")
		})
	}
}

// TestStoredMasksAreIsolated pins that the accounting stores copies of
// the sets a usage declares. It checks the stored sets directly, since
// Available never reads a user's declared shared set.
func TestStoredMasksAreIsolated(t *testing.T) {
	a := NewAccounting(testAllCpus)

	shared, exclusive := testNode0.Clone(), testCpu5.Clone()
	err := allocate(a, &CpuUsage{
		ID: "u", Name: "u", Shared: shared, Exclusive: exclusive, Charge: 500,
	})
	require.NoError(t, err)

	shared.Set(2, 3)
	exclusive.Set(6)

	u, ok := a.users["u"]
	require.True(t, ok, "user is accounted for")
	require.Equal(t, testNode0.List(), u.shared.List(), "stored shared CPUs")
	require.Equal(t, testCpu5.List(), u.exclusive.List(), "stored exclusive CPUs")
}

// TestReportedMasksAreIsolated pins that altering a reported CPU set
// does not alter what the next caller is handed. An offer reports the
// same change to every caller.
func TestReportedMasksAreIsolated(t *testing.T) {
	a := NewAccounting(testAllCpus)
	require.NoError(t, allocate(a, &CpuUsage{
		ID: "sh", Name: "sh", Shared: testNode0, Charge: 500,
	}))

	// Taking CPU 1 moves "sh" onto CPU 0 alone.
	offer, err := a.GetOffer(&CpuUsage{ID: "ex", Name: "ex", Exclusive: NewCpuMask(1)})
	require.NoError(t, err)

	offer.Cpus().Set(7)
	require.True(t, offer.Cpus().Equals(NewCpuMask(1)), "the CPUs of the requester")

	updates := offer.Updates()
	require.Len(t, updates, 1)
	updates[0].Cpus().Set(7)
	require.True(t, updates[0].Cpus().Equals(NewCpuMask(0)), "the CPUs of a change")
	require.True(t, offer.Updates()[0].Cpus().Equals(NewCpuMask(0)),
		"the CPUs the offer still reports")

	// The same for a change reported by Allocate.
	_, changes, err := a.Allocate(&CpuUsage{ID: "ex", Name: "ex", Exclusive: NewCpuMask(1)})
	require.NoError(t, err)
	require.Len(t, changes, 1)
	changes[0].Cpus().Set(7)
	require.True(t, changes[0].Cpus().Equals(NewCpuMask(0)), "the CPUs of a change")
}

func TestAdd(t *testing.T) {
	t.Run("shared, exclusive and mixed usage", func(t *testing.T) {
		a := testAccounting(t)

		// Both users of node0 charge the same pool; CPU #4 is fully taken.
		require.Equal(t, map[string]int{
			"0-7": 6250, // 8000 - 1000 - 750
			"0-1": 1250, // 2000 - 750
			"2-3": 2000,
			"4":   0, // 1000 - 1000
			"5":   1000,
		}, testAvailable(t, a), "available capacity")

		require.NoError(t, a.insert(&CpuUsage{
			ID: "mixed", Name: "mixed",
			Shared:    testNode1,
			Charge:    250,
			Exclusive: NewCpuMask(6, 7),
		}), "add mixed user")

		require.Equal(t, map[string]int{
			"0-7": 4000, // 8000 - 3000 - 750 - 250
			"0-1": 1250,
			"2-3": 1750, // 2000 - 250
			"4":   0,
			"5":   1000,
		}, testAvailable(t, a), "available capacity")
	})

	for _, tc := range []struct {
		name  string
		usage *CpuUsage
	}{
		{
			name:  "an existing user",
			usage: &CpuUsage{ID: "shared1", Name: "shared1", Shared: testNode1, Charge: 100},
		},
		{
			name:  "a charge without shared CPUs",
			usage: &CpuUsage{ID: "uncharged", Name: "uncharged", Exclusive: testCpu5, Charge: 100},
		},
		{
			name:  "the exclusive CPU of another user",
			usage: &CpuUsage{ID: "thief", Name: "thief", Shared: testNode1, Exclusive: testCpu4},
		},
		{
			name:  "exclusive CPUs which choke a pool in use",
			usage: &CpuUsage{ID: "greedy", Name: "greedy", Exclusive: testNode0},
		},
		{
			name:  "shared CPUs which are all allocated exclusively",
			usage: &CpuUsage{ID: "choked", Name: "choked", Shared: testCpu4},
		},
		{
			name:  "shared CPUs one takes exclusively oneself",
			usage: &CpuUsage{ID: "selfish", Name: "selfish", Shared: testCpu5, Exclusive: testCpu5},
		},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			a := testAccounting(t)
			before := testAvailable(t, a)

			require.Error(t, a.insert(tc.usage), "add user %q", tc.usage.ID)

			// A rejected usage must not alter the accounting.
			require.Equal(t, before, testAvailable(t, a), "available capacity")
		})
	}
}

func TestDelete(t *testing.T) {
	a := testAccounting(t)
	before := testAvailable(t, a)

	require.Error(t, deleteUser(a, "no such user"), "delete unknown user")
	require.Equal(t, before, testAvailable(t, a), "available capacity")

	require.NoError(t, deleteUser(a, "shared1"), "delete user")
	require.Equal(t, 1750, available(t, a, testNode0), "available in node0, 2000 - 250")
	require.Error(t, deleteUser(a, "shared1"), "delete the same user twice")

	// Deleting a user releases its exclusive CPUs for others to allocate.
	require.NoError(t, deleteUser(a, "exclusive"), "delete exclusive user")
	require.Equal(t, 1000, available(t, a, testCpu4), "available on CPU #4")

	require.NoError(t, a.insert(&CpuUsage{ID: "new", Name: "new", Exclusive: testCpu4}),
		"reallocate CPU #4 exclusively")
	require.Equal(t, 0, available(t, a, testCpu4), "available on CPU #4")
}

func TestUpdate(t *testing.T) {
	// Silence the warning for updating an unknown user.
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	t.Run("charge", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.update(&CpuUsage{
			ID: "shared1", Name: "shared1",
			Shared: testNode0,
			Charge: 1000,
		}), "update charge")

		require.Equal(t, 750, available(t, a, testNode0), "available in node0, 2000 - 1250")
	})

	t.Run("shared CPUs", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.update(&CpuUsage{
			ID: "shared1", Name: "shared1",
			Shared: testNode1,
			Charge: 500,
		}), "update shared CPUs")

		require.Equal(t, 1750, available(t, a, testNode0), "available in node0, 2000 - 250")
		require.Equal(t, 1500, available(t, a, testNode1), "available in node1, 2000 - 500")
	})

	t.Run("exclusive CPUs", func(t *testing.T) {
		a := testAccounting(t)

		// An update does not conflict with its own old exclusive CPUs.
		require.NoError(t, a.update(&CpuUsage{ID: "exclusive", Name: "exclusive", Exclusive: testCpu4}),
			"update with unchanged exclusive CPUs")
		require.Equal(t, 0, available(t, a, testCpu4), "available on CPU #4")

		require.NoError(t, a.update(&CpuUsage{ID: "exclusive", Name: "exclusive", Exclusive: testCpu5}),
			"update exclusive CPUs")
		require.Equal(t, 1000, available(t, a, testCpu4), "available on CPU #4")
		require.Equal(t, 0, available(t, a, testCpu5), "available on CPU #5")
	})

	t.Run("adds an unknown user", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, a.update(&CpuUsage{ID: "new", Name: "new", Shared: testNode1, Charge: 250}),
			"update unknown user")
		require.Equal(t, 1750, available(t, a, testNode1), "available in node1, 2000 - 250")
	})

	t.Run("keeps old user intact if rejected", func(t *testing.T) {
		a := testAccounting(t)

		// update deletes before it inserts. A rejected insert must
		// reinstate the deleted user with its original charge.
		require.Error(t, a.update(&CpuUsage{
			ID: "shared1", Name: "shared1",
			Shared:    testNode0,
			Exclusive: testNode0,
		}), "update which chokes itself and the other user of node0")
		require.Equal(t, 1250, available(t, a, testNode0),
			"available in node0 after a rejected update, 2000 - 750")

		// The exclusive CPUs of a reinstated user must be restored too.
		require.Error(t, a.update(&CpuUsage{
			ID: "exclusive", Name: "exclusive",
			Exclusive: testCpu5,
			Charge:    100,
		}), "update with a charge without shared CPUs")
		require.Equal(t, 0, available(t, a, testCpu4), "available on CPU #4")
		require.Equal(t, 1000, available(t, a, testCpu5), "available on CPU #5")
	})
}

// TestAvailableInSets pins that AvailableEach answers for each set, and
// that the tightest answer is what a user needing all of them can take.
func TestAvailableInSets(t *testing.T) {
	var (
		a     = NewAccounting(testAllCpus)
		node2 = NewCpuMask(4, 5)
		node3 = NewCpuMask(6, 7)
	)

	for _, u := range []*CpuUsage{
		{ID: "node0", Name: "node0", Shared: testNode0, Charge: 1500},
		{ID: "node1", Name: "node1", Shared: testNode1, Charge: 1000},
		{ID: "node2", Name: "node2", Shared: node2, Charge: 500},
	} {
		require.NoError(t, a.insert(u), "add usage %q", u.ID)
	}

	require.Equal(t, 5000, available(t, a, a.AllCpus()), "available in all the CPUs, 8000 - 3000")

	require.Equal(t, 500, available(t, a, testNode0), "available in node0, 2000 - 1500")
	require.Equal(t, 1000, available(t, a, testNode1), "available in node1, 2000 - 1000")
	require.Equal(t, 2000, available(t, a, node3), "available in node3")

	// The tightest answer is what all the sets together allow.
	for _, tc := range []struct {
		cpus     []*CpuMask
		each     []int
		tightest int
	}{
		{[]*CpuMask{testNode0, testNode1}, []int{500, 1000}, 500},
		{[]*CpuMask{testNode1, node3}, []int{1000, 2000}, 1000},
		{[]*CpuMask{testNode1, node2, node3}, []int{1000, 1500, 2000}, 1000},
	} {
		each, exact := a.AvailableEach(tc.cpus...)
		require.True(t, exact, "not truncated")
		require.Equal(t, tc.each, each, "available in each of %v", tc.cpus)
		require.Equal(t, tc.tightest, slices.Min(each), "tightest of %v", tc.cpus)

		// Each answer equals what that set reports on its own.
		for i, cpus := range tc.cpus {
			require.Equal(t, available(t, a, cpus), each[i], "available in %s", cpus)
		}
	}
}

func TestAllocate(t *testing.T) {
	t.Run("a shared charge", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, allocate(a, &CpuUsage{
			ID: "new", Name: "new",
			Shared: testNode0,
			Charge: 1250,
		}), "allocate all the available capacity of node0")
		require.Equal(t, 0, available(t, a, testNode0), "available in node0")

		require.Error(t, allocate(a, &CpuUsage{
			ID: "greedy", Name: "greedy",
			Shared: testNode0,
			Charge: 50,
		}), "allocate from a pool without capacity")
	})

	t.Run("too large a shared charge", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(t, a)

		require.Error(t, allocate(a, &CpuUsage{
			ID: "greedy", Name: "greedy",
			Shared: testNode0,
			Charge: 1251,
		}), "allocate more than the available capacity of node0")

		// A rejected allocation must not alter the accounting.
		require.Equal(t, before, testAvailable(t, a), "available capacity")
	})

	t.Run("a charge limited by a larger pool", func(t *testing.T) {
		a := testAccounting(t)
		socket0 := NewCpuMask(cpuRange(0, 3)...)

		require.NoError(t, allocate(a, &CpuUsage{
			ID: "socket0", Name: "socket0",
			Shared: socket0,
			Charge: 3000,
		}), "allocate from socket0")

		// Node0 has 1250 of its own capacity left, but socket0 only 250.
		require.Equal(t, 250, available(t, a, testNode0), "available in node0")
		require.Error(t, allocate(a, &CpuUsage{
			ID: "greedy", Name: "greedy",
			Shared: testNode0,
			Charge: 500,
		}), "allocate more from node0 than socket0 allows")
		require.NoError(t, allocate(a, &CpuUsage{
			ID: "modest", Name: "modest",
			Shared: testNode0,
			Charge: 250,
		}), "allocate what socket0 allows")
	})

	t.Run("exclusive CPUs", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, allocate(a, &CpuUsage{
			ID: "exclusive2", Name: "exclusive2",
			Exclusive: NewCpuMask(5, 6),
		}), "allocate free CPUs exclusively")
		require.Equal(t, 0, available(t, a, testCpu5), "available on CPU #5")

		require.Error(t, allocate(a, &CpuUsage{
			ID: "thief", Name: "thief",
			Exclusive: testCpu4,
		}), "allocate a CPU which is taken")
	})

	t.Run("an exclusive CPU of a pool in use", func(t *testing.T) {
		a := testAccounting(t)

		// node0's users charge 750, which fits one CPU, so the other
		// CPU can be taken exclusively.
		require.NoError(t, allocate(a, &CpuUsage{
			ID: "exclusive0", Name: "exclusive0",
			Exclusive: NewCpuMask(0),
		}), "allocate a CPU a pool in use can spare")
		require.Equal(t, 250, available(t, a, testNode0), "available in node0, 1000 - 750")

		// The last CPU cannot: its users would have none to run on.
		require.Error(t, allocate(a, &CpuUsage{
			ID: "exclusive1", Name: "exclusive1",
			Exclusive: NewCpuMask(1),
		}), "allocate the last CPU of a pool in use")

		// A CPU in no pool in use always has its full capacity
		// available, unless the accounting is already overcommitted.
		require.NoError(t, allocate(a, &CpuUsage{
			ID: "exclusive5", Name: "exclusive5",
			Exclusive: testCpu5,
		}), "allocate a CPU which no pool in use contains")
	})

	t.Run("a usage validate rejects", func(t *testing.T) {
		a := testAccounting(t)

		require.Error(t, allocate(a, &CpuUsage{
			ID: "shared1", Name: "shared1",
			Shared: testNode1,
			Charge: 100,
		}), "allocate for an existing user")
	})

	t.Run("a mixed usage which does not fit", func(t *testing.T) {
		a := testAccounting(t)

		// The charge does not fit node1, so the whole usage is
		// refused and its exclusive CPU stays free.
		require.Error(t, allocate(a, &CpuUsage{
			ID: "mixed", Name: "mixed",
			Shared:    testNode1,
			Charge:    2500,
			Exclusive: testCpu5,
		}), "allocate a charge which does not fit, with an exclusive CPU")
		require.Equal(t, 2000, available(t, a, testNode1), "available in node1")
		require.Equal(t, 1000, available(t, a, testCpu5), "available on CPU #5")

		// The same usage with a charge which fits is taken.
		require.NoError(t, allocate(a, &CpuUsage{
			ID: "mixed", Name: "mixed",
			Shared:    testNode1,
			Charge:    2000,
			Exclusive: testCpu5,
		}), "allocate a charge which fits, with an exclusive CPU")
		require.Equal(t, 0, available(t, a, testNode1), "available in node1")
		require.Equal(t, 0, available(t, a, testCpu5), "available on CPU #5")
	})
}

func TestReallocate(t *testing.T) {
	// Silence the warning for reallocating an unknown user.
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	t.Run("a charge which fits", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, reallocate(a, &CpuUsage{
			ID: "shared1", Name: "shared1",
			Shared: testNode0,
			Charge: 1000,
		}), "reallocate a bigger charge which fits")
		require.Equal(t, 750, available(t, a, testNode0), "available in node0, 2000 - 1250")
	})

	t.Run("a charge which does not fit", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(t, a)

		require.Error(t, reallocate(a, &CpuUsage{
			ID: "shared1", Name: "shared1",
			Shared: testNode0,
			Charge: 2000,
		}), "reallocate a charge which does not fit")
		require.Equal(t, before, testAvailable(t, a), "available capacity")
	})

	t.Run("another pool", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, reallocate(a, &CpuUsage{
			ID: "shared1", Name: "shared1",
			Shared: testNode1,
			Charge: 1500,
		}), "reallocate to another pool")
		require.Equal(t, 1750, available(t, a, testNode0), "available in node0, 2000 - 250")
		require.Equal(t, 500, available(t, a, testNode1), "available in node1, 2000 - 1500")
	})

	t.Run("exclusive CPUs", func(t *testing.T) {
		a := testAccounting(t)

		require.NoError(t, reallocate(a, &CpuUsage{ID: "exclusive", Name: "exclusive", Exclusive: testCpu5}),
			"reallocate exclusive CPUs")
		require.Equal(t, 1000, available(t, a, testCpu4), "available on CPU #4")
		require.Equal(t, 0, available(t, a, testCpu5), "available on CPU #5")
	})

	t.Run("rolls back the whole usage", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(t, a)

		// The charge does not fit node1, so CPU #4 must stay with its
		// holder.
		require.Error(t, reallocate(a, &CpuUsage{
			ID: "exclusive", Name: "exclusive",
			Shared: testNode1,
			Charge: 2500,
		}), "reallocate a charge which does not fit")
		require.Equal(t, before, testAvailable(t, a), "available capacity")
		require.Equal(t, 0, available(t, a, testCpu4), "available on CPU #4")
	})

	t.Run("an unknown user", func(t *testing.T) {
		a := testAccounting(t)
		before := testAvailable(t, a)

		// Reallocating an unknown user allocates it, if it fits.
		require.NoError(t, reallocate(a, &CpuUsage{
			ID: "new", Name: "new",
			Shared: testNode1,
			Charge: 500,
		}), "reallocate an unknown user")
		require.Equal(t, 1500, available(t, a, testNode1), "available in node1, 2000 - 500")

		require.NoError(t, release(a, "new"), "release the user")
		require.Error(t, reallocate(a, &CpuUsage{
			ID: "new", Name: "new",
			Shared: testNode1,
			Charge: 2500,
		}), "reallocate an unknown user which does not fit")
		require.Equal(t, before, testAvailable(t, a), "available capacity")
	})
}

// TestReallocateItsOwnExclusiveCpus pins that a reallocation may keep or
// swap the CPUs a user holds exclusively. It is judged without the
// replaced user, so it chokes nobody, itself included.
func TestReallocateItsOwnExclusiveCpus(t *testing.T) {
	t.Run("keeping them", func(t *testing.T) {
		a := testAccounting(t)

		// The user holding CPU #4 asks for the same CPU again.
		require.NoError(t, reallocate(a, &CpuUsage{ID: "exclusive", Name: "exclusive", Exclusive: testCpu4}),
			"reallocate the CPU it holds already")
		require.Equal(t, 0, available(t, a, testCpu4), "available on CPU #4")
	})

	t.Run("swapping them within its own pool", func(t *testing.T) {
		a := NewAccounting(testNode0)
		require.NoError(t, a.insert(&CpuUsage{
			ID: "u", Name: "u", Shared: testNode0, Exclusive: NewCpuMask(0), Charge: 500,
		}), "add a user of node0 holding CPU #0 exclusively")

		// It runs on CPU #1 and holds #0. Swapping them chokes nobody: CPU #0
		// is what it would be left to run on.
		require.NoError(t, reallocate(a, &CpuUsage{
			ID: "u", Name: "u", Shared: testNode0, Exclusive: NewCpuMask(1), Charge: 500,
		}), "swap which CPU of its own pool it holds")
		require.Equal(t, 500, available(t, a, testNode0), "available in node0, 1000 - 500")
	})

	t.Run("swapping them within a pool in use", func(t *testing.T) {
		a := NewAccounting(testNode0)
		require.NoError(t, a.insert(&CpuUsage{ID: "u", Name: "u", Exclusive: NewCpuMask(0)}),
			"add a user holding CPU #0 exclusively")
		require.NoError(t, a.insert(&CpuUsage{ID: "v", Name: "v", Shared: testNode0, Charge: 100}),
			"add a user of node0")

		// User v runs on CPU #1 alone, so anyone else taking that CPU would
		// choke it, but not the user giving #0 back in exchange.
		require.NoError(t, reallocate(a, &CpuUsage{ID: "u", Name: "u", Exclusive: NewCpuMask(1)}),
			"swap which CPU of a pool in use it holds")
		require.Equal(t, 900, available(t, a, testNode0), "available in node0, 1000 - 100")
		requireNobodyChoked(t, a)
	})
}

// TestReallocateFreesAndTakes pins that a reallocation freeing exactly as
// much as it takes fits a full accounting: the old usage comes off first.
func TestReallocateFreesAndTakes(t *testing.T) {
	a := NewAccounting(testNode0)
	require.NoError(t, a.insert(&CpuUsage{ID: "u", Name: "u", Shared: testNode0, Charge: 2000}))
	require.Equal(t, 0, available(t, a, testNode0), "node0 is full")

	require.NoError(t, reallocate(a, &CpuUsage{ID: "u", Name: "u", Shared: testNode0, Charge: 2000}),
		"reallocating the same amount")
}

// TestHypotheticalNoMutation pins that evaluating a candidate leaves nothing
// behind, whether it fits or not.
func TestHypotheticalNoMutation(t *testing.T) {
	a := testAccounting(t)
	before := testAvailable(t, a)

	// Fits: 250 of node0's 1250 remaining.
	fits := &user{id: "ok", name: "ok", shared: testNode0, charge: 250}
	require.Equal(t, 0, lackingCapacity(t, a, fits, ""), "a usage which fits")
	require.Equal(t, before, testAvailable(t, a), "unchanged after a fitting probe")

	// Does not fit: 2000 against 1250 remaining.
	over := &user{id: "no", name: "no", shared: testNode0, charge: 2000}
	require.Equal(t, 750, lackingCapacity(t, a, over, ""), "amount lacking")
	require.Equal(t, before, testAvailable(t, a), "unchanged after a failing probe")
}

// TestHypotheticalSubPool pins the sub-pool case a count-based check misses:
// slicing CPUs out of a pool can overcommit a pool nested inside it.
func TestHypotheticalSubPool(t *testing.T) {
	a := NewAccounting(NewCpuMask(cpuRange(0, 8)...))
	require.NoError(t, a.insert(&CpuUsage{
		ID: "q", Name: "q", Shared: NewCpuMask(0, 1, 2, 3), Charge: 2900,
	}))

	// Taking 0-2 leaves pool q with CPU 3 alone: 1000 against 2900.
	bad := &user{id: "x", name: "x", exclusive: NewCpuMask(0, 1, 2)}
	require.Equal(t, 1900, lackingCapacity(t, a, bad, ""), "slicing 0-2")

	// Taking 6-8 touches nothing charged.
	ok := &user{id: "y", name: "y", exclusive: NewCpuMask(6, 7, 8)}
	require.Equal(t, 0, lackingCapacity(t, a, ok, ""), "slicing 6-8")
}

func TestRelease(t *testing.T) {
	a := testAccounting(t)
	before := testAvailable(t, a)

	require.Error(t, release(a, "no such user"), "release an unknown user")
	require.Equal(t, before, testAvailable(t, a), "available capacity")

	require.NoError(t, allocate(a, &CpuUsage{
		ID: "mixed", Name: "mixed",
		Shared:    testNode1,
		Charge:    1500,
		Exclusive: testCpu5,
	}), "allocate a charge and an exclusive CPU")
	require.Equal(t, 500, available(t, a, testNode1), "available in node1")
	require.Equal(t, 0, available(t, a, testCpu5), "available on CPU #5")

	require.NoError(t, release(a, "mixed"), "release the user")
	require.Equal(t, before, testAvailable(t, a), "available capacity after release")
	require.Error(t, release(a, "mixed"), "release the same user twice")
}

// TestOfferLifecycle pins that an offer is a checked but uncommitted
// allocation: several can coexist, and any change expires them.
func TestOfferLifecycle(t *testing.T) {
	t.Run("committing one offer expires the others", func(t *testing.T) {
		a := testAccounting(t)

		o1, err := a.GetOffer(&CpuUsage{ID: "a", Name: "a", Shared: testNode0, Charge: 500})
		require.NoError(t, err)
		o2, err := a.GetOffer(&CpuUsage{ID: "b", Name: "b", Shared: testNode0, Charge: 500})
		require.NoError(t, err)

		require.True(t, o1.IsValid())
		require.True(t, o2.IsValid())

		_, _, err = o1.Commit()
		require.NoError(t, err)

		require.False(t, o2.IsValid(), "the uncommitted offer expired")
		_, _, err = o2.Commit()
		require.ErrorIs(t, err, ErrExpiredOffer)
	})

	// Every mutating path must expire an outstanding offer. After a
	// release, a stale offer would pin its user to fewer CPUs than the
	// pool has.
	t.Run("every change expires an outstanding offer", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			change func(*Accounting) error
		}{
			{"Allocate", func(a *Accounting) error {
				return allocate(a, &CpuUsage{ID: "new", Name: "new", Exclusive: testCpu5})
			}},
			{"Reallocate", func(a *Accounting) error {
				return reallocate(a, &CpuUsage{
					ID: "shared1", Name: "shared1", Shared: testNode0, Charge: 250,
				})
			}},
			{"Release", func(a *Accounting) error {
				return release(a, "exclusive")
			}},
			{"insert", func(a *Accounting) error {
				return a.insert(&CpuUsage{ID: "new", Name: "new", Exclusive: testCpu5})
			}},
			{"update", func(a *Accounting) error {
				return a.update(&CpuUsage{
					ID: "shared1", Name: "shared1", Shared: testNode0, Charge: 250,
				})
			}},
			{"remove", func(a *Accounting) error {
				return deleteUser(a, "exclusive")
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				a := testAccounting(t)

				o, err := a.GetOffer(&CpuUsage{
					ID: "offered", Name: "offered", Shared: testNode1, Charge: 250,
				})
				require.NoError(t, err)
				require.True(t, o.IsValid(), "a fresh offer")

				version := a.version
				require.NoError(t, tc.change(a), "the change")
				require.Greater(t, a.version, version, "the version after the change")

				require.False(t, o.IsValid(), "the offer outlived the change")
				_, _, err = o.Commit()
				require.ErrorIs(t, err, ErrExpiredOffer, "committing it anyway")
			})
		}
	})

	t.Run("an existing ID is offered as a replacement", func(t *testing.T) {
		a := testAccounting(t)

		o, err := a.GetOffer(&CpuUsage{
			ID: "shared1", Name: "shared1", Shared: testNode1, Charge: 250,
		})
		require.NoError(t, err, "offer for an ID already accounted for")

		_, _, err = o.Commit()
		require.NoError(t, err, "committing it")
		require.Len(t, a.users, 3, "the user was replaced, not duplicated")
	})

	t.Run("a usage which does not fit yields a CapacityError", func(t *testing.T) {
		a := testAccounting(t)
		_, err := a.GetOffer(&CpuUsage{ID: "big", Name: "big", Shared: testNode0, Charge: 9000})
		var ce *CapacityError
		require.ErrorAs(t, err, &ce)
		require.Equal(t, 7750, ce.Lacking, "milli-CPU lacking")
	})
}

// TestOfferReplacesInPlace pins that an offer for an existing ID judges
// the state without that user, as Reallocate does: the same charge fits
// a full node.
func TestOfferReplacesInPlace(t *testing.T) {
	a := NewAccounting(testNode0)
	require.NoError(t, allocate(a, &CpuUsage{
		ID: "u", Name: "u", Shared: testNode0, Charge: 2000,
	}))
	require.Equal(t, 0, available(t, a, testNode0), "node0 is full")

	o, err := a.GetOffer(&CpuUsage{ID: "u", Name: "u", Shared: testNode0, Charge: 2000})
	require.NoError(t, err, "offer replacing the whole charge")

	cpus, updates, err := o.Commit()
	require.NoError(t, err, "committing it")
	require.True(t, cpus.Equals(testNode0), "the CPUs it runs on")
	require.Empty(t, updates, "nobody else has to move")
	require.Len(t, a.users, 1, "the user was replaced, not duplicated")
	require.Equal(t, 0, available(t, a, testNode0), "full, and not overcommitted")
}

// TestOfferCommitAgreesWithReallocate pins that GetOffer plus Commit and
// Reallocate report the same CPUs, changes and resulting state.
func TestOfferCommitAgreesWithReallocate(t *testing.T) {
	// Moving shared1 out of node0 widens what shared2 runs on, so the
	// changes are non-empty.
	replacement := &CpuUsage{
		ID: "shared1", Name: "shared1", Shared: testNode1, Charge: 1000,
	}

	viaOffer := testAccounting(t)
	o, err := viaOffer.GetOffer(replacement)
	require.NoError(t, err, "offer for an existing ID")
	offerCpus, offerUpdates, err := o.Commit()
	require.NoError(t, err, "committing the offer")

	viaReallocate := testAccounting(t)
	reallocCpus, reallocUpdates, err := viaReallocate.Reallocate(replacement)
	require.NoError(t, err, "reallocating directly")

	require.True(t, offerCpus.Equals(reallocCpus), "the CPUs the user gets")
	require.Equal(t, reallocUpdates, offerUpdates, "the changes reported")
	require.Equal(t, testAvailable(t, viaReallocate), testAvailable(t, viaOffer),
		"the resulting accounting")
}

// TestChangesOnAllocateAndRelease pins that taking CPUs exclusively
// narrows what the other users of their pool run on, and that taking
// and giving back both report who must be re-pinned.
func TestChangesOnAllocateAndRelease(t *testing.T) {
	a := NewAccounting(testAllCpus)
	_, _, err := a.Allocate(&CpuUsage{ID: "sh", Name: "sh", Shared: testNode0, Charge: 500})
	require.NoError(t, err)

	// Taking CPU 1 narrows what "sh" may run on.
	cpus, updates, err := a.Allocate(&CpuUsage{ID: "ex", Name: "ex", Exclusive: NewCpuMask(1)})
	require.NoError(t, err)
	require.True(t, cpus.Equals(NewCpuMask(1)), "what the requester got")
	require.Len(t, updates, 1, "one user to re-pin")
	require.Equal(t, "sh", updates[0].ID())
	require.True(t, updates[0].Cpus().Equals(NewCpuMask(0)), "sh runs on CPU 0 alone")

	// Releasing it widens them back.
	updates, err = a.Release("ex")
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.True(t, updates[0].Cpus().Equals(testNode0), "sh runs on node0 again")
}

// TestChangesOfAMixedBystander pins that a mixed user is reported on
// what its pool has left plus its own exclusive CPUs, when another user
// takes a CPU of the same pool. The pool needs three CPUs, or the
// choking rule would refuse the third user.
func TestChangesOfAMixedBystander(t *testing.T) {
	var (
		a    = NewAccounting(testAllCpus)
		pool = NewCpuMask(0, 1, 2)
	)

	_, _, err := a.Allocate(&CpuUsage{
		ID: "mixed", Name: "mixed", Shared: pool, Exclusive: NewCpuMask(0), Charge: 500,
	})
	require.NoError(t, err)
	require.True(t, a.pools[pool.Key()].eff().Equals(NewCpuMask(1, 2)),
		"effective set of the pool, without the mixed user's own CPU 0")

	// A third user takes CPU 1, leaving the pool CPU 2 alone. The offer
	// must report where that leaves the mixed user.
	third := &CpuUsage{ID: "third", Name: "third", Exclusive: NewCpuMask(1)}

	offer, err := a.GetOffer(third)
	require.NoError(t, err)
	require.Len(t, offer.Updates(), 1, "one user to re-pin")
	require.Equal(t, "mixed", offer.Updates()[0].ID())
	require.True(t, offer.Updates()[0].Cpus().Equals(NewCpuMask(0, 2)),
		"offered: CPU 2, what the pool has left, plus its own CPU 0")

	// And the allocation must say the same.
	cpus, updates, err := a.Allocate(third)
	require.NoError(t, err)
	require.True(t, cpus.Equals(NewCpuMask(1)), "what the requester got")
	require.Len(t, updates, 1, "one user to re-pin")
	require.Equal(t, "mixed", updates[0].ID())
	require.True(t, updates[0].Cpus().Equals(NewCpuMask(0, 2)),
		"allocated: CPU 2 plus its own CPU 0")

	// Giving CPU 1 back widens the shared half alone.
	updates, err = a.Release("third")
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.Equal(t, "mixed", updates[0].ID())
	require.True(t, updates[0].Cpus().Equals(pool), "back on all of 0-2")

	requireEffectiveCache(t, a)
	requireNobodyChoked(t, a)
}

// TestRejectedOperationsReportNothing pins that a refused allocation reports
// neither CPUs nor changes, and leaves the accounting as it was.
func TestRejectedOperationsReportNothing(t *testing.T) {
	a := testAccounting(t)
	before := testAvailable(t, a)

	cpus, updates, err := a.Allocate(&CpuUsage{
		ID: "big", Name: "big", Shared: testNode0, Charge: 9000,
	})
	require.Error(t, err)
	require.Nil(t, cpus)
	require.Empty(t, updates)
	require.Equal(t, before, testAvailable(t, a))
}

func TestOvercommit(t *testing.T) {
	t.Run("a pool", func(t *testing.T) {
		a := testAccounting(t)
		require.Equal(t, 0, overcommitOf(t, a), "overcommit")

		// insert takes any valid usage, however much capacity it asks for.
		require.NoError(t, a.insert(&CpuUsage{
			ID: "greedy", Name: "greedy",
			Shared: testNode0,
			Charge: 2000,
		}), "add a usage which does not fit")
		require.Equal(t, 750, overcommitOf(t, a), "overcommit, 2750 charged on 2000")

		require.NoError(t, deleteUser(a, "greedy"), "delete the usage")
		require.Equal(t, 0, overcommitOf(t, a), "overcommit")

		// Allocate refuses the same usage, so it cannot overcommit anything.
		require.Error(t, allocate(a, &CpuUsage{
			ID: "greedy", Name: "greedy",
			Shared: testNode0,
			Charge: 2000,
		}), "allocate a usage which does not fit")
		require.Equal(t, 0, overcommitOf(t, a), "overcommit")
	})

	t.Run("an implicit pool", func(t *testing.T) {
		a := NewAccounting(NewCpuMask(cpuRange(0, 3)...))

		// Each fits its own pool and the pseudo-pool 1,2 has room
		// to spare. Only the union, CPUs 0-3, is over: 4500 charged
		// on 4000.
		for _, u := range []*CpuUsage{
			{ID: "left", Name: "left", Shared: NewCpuMask(0, 1), Charge: 2000},
			{ID: "right", Name: "right", Shared: NewCpuMask(2, 3), Charge: 2000},
			{ID: "middle", Name: "middle", Shared: NewCpuMask(1, 2), Charge: 500},
		} {
			require.NoError(t, a.insert(u), "add usage %q", u.ID)
		}

		require.Equal(t, 500, overcommitOf(t, a), "overcommit of CPUs 0-3")
		require.Equal(t, 500, overcommitOf(t, a, NewCpuMask(cpuRange(0, 3)...)),
			"overcommit of CPUs 0-3")

		// CPUs 0-3 limit every one of those pools, so the excess
		// shows in each.
		require.Equal(t, -500, available(t, a, NewCpuMask(0, 1)), "available in 0,1")
		require.Equal(t, -500, available(t, a, NewCpuMask(1, 2)), "available in 1,2")
	})
}

// TestOvercommitOfCpus pins that Overcommit of some CPU sets reports
// only the limits constraining those CPUs.
func TestOvercommitOfCpus(t *testing.T) {
	a := NewAccounting(testAllCpus)

	// CPUs 0-3 are over their capacity by 500, CPUs 4-7 are untouched.
	for _, u := range []*CpuUsage{
		{ID: "left", Name: "left", Shared: NewCpuMask(0, 1), Charge: 2000},
		{ID: "right", Name: "right", Shared: NewCpuMask(2, 3), Charge: 2000},
		{ID: "middle", Name: "middle", Shared: NewCpuMask(1, 2), Charge: 500},
	} {
		require.NoError(t, a.insert(u), "add usage %q", u.ID)
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
			// 0-7 has 3500 left. A set is limited by the sets
			// containing it, not by narrower ones within it.
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
			require.Equal(t, tc.expect, overcommitOf(t, a, tc.cpus...), "overcommit")

			// For a single set of CPUs this is what Available reports.
			if len(tc.cpus) == 1 {
				require.Equal(t, tc.expect, max(-available(t, a, tc.cpus[0]), 0),
					"available in %s", tc.cpus[0])
			}
		})
	}
}

// TestOvercommits pins that Overcommit per pool in use tells which parts
// are over capacity and by how much. The whole accounting reports the
// worst.
func TestOvercommits(t *testing.T) {
	var (
		a     = NewAccounting(testAllCpus)
		node0 = NewCpuMask(0, 1)
		node1 = NewCpuMask(2, 3)
		node2 = NewCpuMask(4, 5)
	)

	// insert enforces no capacity, so node0 and node2 end up over their
	// pools' capacity while all CPUs together still have some left.
	for _, u := range []*CpuUsage{
		{ID: "node0", Name: "node0", Shared: node0, Charge: 2500},
		{ID: "node1", Name: "node1", Shared: node1, Charge: 1500},
		{ID: "node2", Name: "node2", Shared: node2, Charge: 2800},
		{ID: "exclusive", Name: "exclusive", Exclusive: NewCpuMask(6)},
	} {
		require.NoError(t, a.insert(u), "add usage %q", u.ID)
	}

	require.Equal(t, 200, available(t, a, testAllCpus),
		"available in all the CPUs, 8000 - 1000 - 6800")

	expect := map[string]int{
		node0.Key(): 500, // 2000 - 2500
		node1.Key(): 0,   // 2000 - 1500
		node2.Key(): 800, // 2000 - 2800
	}

	// The user without shared CPUs has nothing to report.
	pools := a.UsedPools()
	require.Len(t, pools, len(expect), "pools in use")
	require.NotContains(t, pools, NewCpuMask().Key(), "the pool without CPUs")

	overcommit := map[string]int{}
	for key, cpus := range pools {
		require.Equal(t, key, cpus.Key(), "key of pool %s", cpus)
		overcommit[key] = overcommitOf(t, a, cpus)
	}
	require.Equal(t, expect, overcommit, "overcommit of the pools in use")

	// collectOvercommits reports the same in a single call.
	require.Equal(t, expect, a.collectOvercommits(), "overcommit of the pools in use")
	require.Equal(t, map[string]int{node0.Key(): 500, node2.Key(): 800},
		a.collectOvercommits(node0, node2), "overcommit of the given pools")

	// The accounting as a whole is as overcommitted as its worst pool.
	require.Equal(t, 800, overcommitOf(t, a), "overcommit of the accounting")

	// Releasing the worst offender leaves the other to report and takes
	// its pool out of use.
	require.NoError(t, release(a, "node2"), "release the user of node2")
	require.NotContains(t, a.UsedPools(), node2.Key(), "pool node2")
	require.NotContains(t, a.collectOvercommits(), node2.Key(), "pool node2")
	require.Equal(t, 500, overcommitOf(t, a), "overcommit of the accounting")
}

// TestOvercommitsOfShrunkenPools pins that a pool's declared and
// effective sets report the same overcommit. The exclusive CPUs between
// them add equal capacity and charge, which cancel.
func TestOvercommitsOfShrunkenPools(t *testing.T) {
	var (
		a    = NewAccounting(testAllCpus)
		cpu0 = NewCpuMask(0)
		cpu1 = NewCpuMask(1)
	)

	for _, u := range []*CpuUsage{
		{ID: "node0", Name: "node0", Shared: testNode0, Charge: 1500},
		{ID: "exclusive", Name: "exclusive", Exclusive: cpu1},
		// Declares the CPU node0 has left: two declared pools
		// with one effective set.
		{ID: "cpu0", Name: "cpu0", Shared: cpu0, Charge: 250},
	} {
		require.NoError(t, a.insert(u), "add usage %q", u.ID)
	}

	require.True(t, a.pools[testNode0.Key()].eff().Equals(cpu0), "effective set of node0")
	require.Len(t, a.constraints(), 2,
		"constraint sets: the bounding pool, and node0 collapsed onto CPU #0")

	// CPU #0 alone is left to take 1750 of charge.
	require.Equal(t, 750, overcommitOf(t, a, testNode0), "overcommit of the declared set")
	require.Equal(t, 750, overcommitOf(t, a, cpu0), "overcommit of the effective set")
	require.Equal(t, 750, overcommitOf(t, a), "overcommit of the accounting")

	// Keyed by declared CPUs. UsedPools keys by effective CPUs and
	// merges these two, so its keys must all be among these.
	overcommit := a.collectOvercommits()
	require.Equal(t, map[string]int{testNode0.Key(): 750, cpu0.Key(): 750}, overcommit,
		"overcommit of the pools in use")
	for key := range a.UsedPools() {
		require.Contains(t, overcommit, key, "pool %q", key)
	}
	require.Equal(t, overcommitOf(t, a, testNode0), overcommit[testNode0.Key()],
		"the collected report and a direct query of the declared set")

	requireEffectiveCache(t, a)
	requireNobodyChoked(t, a)
}

// TestAllocationShapes pins the four usage shapes the model must express.
func TestAllocationShapes(t *testing.T) {
	t.Run("mixed: exclusive CPUs inside the declared shared set", func(t *testing.T) {
		a := NewAccounting(testAllCpus)

		// Declares all of node1 as its pool and holds CPU 2 of it exclusively.
		require.NoError(t, a.insert(&CpuUsage{
			ID: "mixed", Name: "mixed", Shared: testNode1, Charge: 500, Exclusive: NewCpuMask(2),
		}), "add mixed usage")

		p := a.pools[testNode1.Key()]
		require.NotNil(t, p, "the declared pool exists")
		require.True(t, p.eff().Equals(NewCpuMask(3)), "effective set excludes own exclusive CPU")
		// No pool for node1 minus the exclusive CPU was created.
		require.Nil(t, a.pools[NewCpuMask(3).Key()], "no carved-out pool")
	})

	t.Run("BestEffort: shared CPUs, no charge", func(t *testing.T) {
		a := NewAccounting(testAllCpus)
		require.NoError(t, a.insert(&CpuUsage{ID: "be", Name: "be", Shared: testNode0, Charge: 0}))

		// It reserves nothing.
		require.Equal(t, 2000, available(t, a, testNode0), "BestEffort takes no capacity")
		// But it must not be choked: CPUs 0 and 1 cannot both go exclusive.
		require.Error(t, allocate(a, &CpuUsage{ID: "x", Name: "x", Exclusive: testNode0}),
			"choking a BestEffort user")
	})

	t.Run("exclusive-only declares no shared set", func(t *testing.T) {
		a := NewAccounting(testAllCpus)
		require.NoError(t, a.insert(&CpuUsage{ID: "excl", Name: "excl", Exclusive: NewCpuMask(0, 1)}))

		// Attributed to node0 without belonging to it.
		require.Equal(t, 0, available(t, a, testNode0), "node0 fully taken")
		require.NotContains(t, a.pools, testNode0.Key(), "node0 is nobody's pool")

		// It lives in the pool with no CPUs. Its empty effective set is
		// exempt from the choking rule.
		empty := a.pools[NewCpuMask().Key()]
		require.NotNil(t, empty, "the pool with no CPUs")
		require.Len(t, empty.users, 1, "its users")
		require.True(t, empty.eff().IsEmpty(), "its effective set")
	})

	t.Run("shared-only: a charge and no exclusive CPUs", func(t *testing.T) {
		a := NewAccounting(testAllCpus)
		require.NoError(t, a.insert(&CpuUsage{ID: "sh", Name: "sh", Shared: testNode0, Charge: 1500}))

		require.Equal(t, 500, available(t, a, testNode0), "2000 - 1500")
		// Its pool must stay inhabitable: both of its CPUs cannot go exclusive.
		require.Error(t, allocate(a, &CpuUsage{ID: "x", Name: "x", Exclusive: testNode0}),
			"choking a charged user")
		// Nor can one of them, but for lack of capacity: a charge of 1500
		// does not fit the single CPU which would be left.
		require.Error(t, allocate(a, &CpuUsage{ID: "x", Name: "x", Exclusive: NewCpuMask(1)}),
			"a charge which no longer fits")
		// insert enforces no capacity, so it takes it and leaves node0 over.
		require.NoError(t, a.insert(&CpuUsage{ID: "x", Name: "x", Exclusive: NewCpuMask(1)}))
		require.Equal(t, -500, available(t, a, testNode0), "1000 - 1500, over its capacity")
	})

	t.Run("a user of its own exclusive CPUs is refused", func(t *testing.T) {
		a := NewAccounting(testAllCpus)
		// Shared == Exclusive would leave an empty effective set.
		require.Error(t, allocate(a, &CpuUsage{
			ID: "self", Name: "self", Shared: testNode0, Exclusive: testNode0, Charge: 0,
		}), "declaring exactly one's own exclusive CPUs as shared")
	})

	t.Run("a fully exclusive machine", func(t *testing.T) {
		a := NewAccounting(testAllCpus)

		// The bounding pool has no users, so taking every CPU
		// exclusively chokes nobody.
		require.NoError(t, allocate(a, &CpuUsage{ID: "hog", Name: "hog", Exclusive: testAllCpus}),
			"allocate every CPU exclusively")
		require.Equal(t, 0, available(t, a, a.AllCpus()), "no capacity left")
		require.True(t, a.pools[testAllCpus.Key()].eff().IsEmpty(),
			"effective set of the bounding pool")
	})
}

// TestExclusiveInPoolInUse pins that a CPU of a pool in use can go
// exclusive if its users keep a CPU, and that the pool's capacity
// shrinks with its effective set.
func TestExclusiveInPoolInUse(t *testing.T) {
	a := NewAccounting(testAllCpus)
	require.NoError(t, a.insert(&CpuUsage{ID: "shared", Name: "shared", Shared: testNode0, Charge: 500}))

	// CPU 1 goes exclusive. The pool still declares 0-1, but its users run
	// on CPU 0 alone, and their charge must fit it.
	require.NoError(t, allocate(a, &CpuUsage{ID: "excl", Name: "excl", Exclusive: NewCpuMask(1)}),
		"take a CPU of a pool in use")

	p := a.pools[testNode0.Key()]
	require.True(t, p.cpus.Equals(testNode0), "declared set is unchanged")
	require.True(t, p.eff().Equals(NewCpuMask(0)), "effective set")
	require.Equal(t, testNode0.List(), a.users["shared"].shared.List(),
		"the user still declares the whole pool")

	// The pool is down to a single CPU worth of capacity.
	require.Equal(t, 500, available(t, a, testNode0), "1000 - 500")
	require.Error(t, allocate(a, &CpuUsage{ID: "more", Name: "more", Shared: testNode0, Charge: 750}),
		"a charge which no longer fits the effective set")

	// Their last CPU cannot be taken either. The check is against the
	// effective set, or a pool in use could be emptied one CPU at a time.
	require.Error(t, allocate(a, &CpuUsage{ID: "last", Name: "last", Exclusive: NewCpuMask(0)}),
		"take the last effective CPU of a pool in use")

	// Releasing the exclusive CPU gives the capacity back.
	require.NoError(t, release(a, "excl"), "release the exclusive CPU")
	require.True(t, p.eff().Equals(testNode0), "effective set after the release")
	require.Equal(t, 1500, available(t, a, testNode0), "2000 - 500")

	requireEffectiveCache(t, a)
}

// TestUserPools pins that every user stays in the pool of its own shared
// CPUs, and that taking CPUs exclusively never changes a pool's declared
// CPUs.
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

	node0 := a.users["shared1"].pool

	// CPU #1 goes to a user of its own. Both users of node0 stay in the
	// pool, which still declares 0-1 while they run on CPU #0.
	require.NoError(t, a.insert(&CpuUsage{ID: "exclusive1", Name: "exclusive1", Exclusive: NewCpuMask(1)}),
		"take CPU #1 of node0 exclusively")
	verify("after taking CPU #1 exclusively")

	require.Same(t, node0, a.users["shared1"].pool, "pool of shared1")
	require.Same(t, node0, a.users["shared2"].pool, "pool of shared2")
	require.Len(t, node0.users, 2, "users of node0")
	require.Contains(t, a.pools, testNode0.Key(), "pool node0")
	require.True(t, node0.eff().Equals(NewCpuMask(0)), "effective set of node0")

	// Releasing it restores the effective set. The pool never moved.
	require.NoError(t, release(a, "exclusive1"), "release CPU #1")
	verify("after releasing CPU #1")

	require.Same(t, node0, a.users["shared1"].pool, "pool of shared1")
	require.True(t, node0.eff().Equals(testNode0), "effective set of node0")

	requireEffectiveCache(t, a)
}

// TestPoolIdentityIsSealed pins that a pool's declared CPUs are the very
// mask validate sealed, not a rebuilt copy.
func TestPoolIdentityIsSealed(t *testing.T) {
	a := NewAccounting(testAllCpus)

	usage := &CpuUsage{ID: "first", Name: "first", Shared: testNode0, Charge: 500}
	require.NoError(t, allocate(a, usage), "the user which creates the pool")

	first := a.users["first"]
	require.Same(t, first.shared, a.pools[testNode0.Key()].cpus,
		"the pool declares the user's own sealed mask, not a rebuild of it")
	require.NotSame(t, usage.Shared, first.shared, "and not the caller's mask either")

	// A second user of the same pool finds it and changes nothing about it.
	require.NoError(t, allocate(a, &CpuUsage{
		ID: "second", Name: "second", Shared: testNode0, Charge: 250,
	}), "a second user of the pool")
	require.Same(t, first.shared, a.users["second"].pool.cpus, "the pool is the same one")

	// Taking one of its CPUs exclusively narrows the effective set and
	// leaves the declared one alone.
	require.NoError(t, allocate(a, &CpuUsage{
		ID: "excl", Name: "excl", Exclusive: NewCpuMask(1),
	}), "take CPU 1 of the pool exclusively")
	require.Same(t, first.shared, a.pools[testNode0.Key()].cpus, "the declared set")

	// Every pool refuses alteration, the bounding one included.
	for key, p := range a.pools {
		require.Panics(t, func() { p.cpus.Set(7) },
			"pool %q (%s): declared CPUs must be sealed", key, p.cpus)
	}
}

func TestEmptyPools(t *testing.T) {
	a := NewAccounting(testAllCpus)
	require.Len(t, a.pools, 1, "pools of a new accounting")

	require.NoError(t, a.insert(&CpuUsage{ID: "node1", Name: "node1", Shared: testNode1, Charge: 500}),
		"add a user of node1")
	require.Len(t, a.pools, 2, "pools")

	// A pool is dropped with its last user, so pools do not pile up.
	require.NoError(t, deleteUser(a, "node1"), "delete the user of node1")
	require.Len(t, a.pools, 1, "pools")
	require.NotContains(t, a.pools, testNode1.Key(), "pool node1")

	// So is the pool of the users without any shared CPUs.
	require.NoError(t, a.insert(&CpuUsage{ID: "exclusive", Name: "exclusive", Exclusive: testCpu4}),
		"add a user without shared CPUs")
	require.Len(t, a.pools, 2, "pools")
	require.NoError(t, deleteUser(a, "exclusive"), "delete the user")
	require.Len(t, a.pools, 1, "pools")

	// Our bounding set of CPUs stays, whether it has users or not.
	require.NoError(t, a.insert(&CpuUsage{ID: "all", Name: "all", Shared: testAllCpus, Charge: 500}),
		"add a user of all the CPUs")
	require.Len(t, a.pools, 1, "pools, the user joins the bounding pool")
	require.NoError(t, deleteUser(a, "all"), "delete the user of all the CPUs")
	require.Contains(t, a.pools, testAllCpus.Key(), "the bounding pool")
}

// TestPoolPerSharedCpus pins that users declaring the same shared CPUs
// share a single pool, so they add a single CPU set to the enumeration.
func TestPoolPerSharedCpus(t *testing.T) {
	a := NewAccounting(testAllCpus)
	sets := len(a.constraints())

	// The exclusive CPUs must avoid node0, the shared CPUs of the users.
	for cpu := 2; cpu <= 7; cpu++ {
		require.NoError(t, a.insert(&CpuUsage{
			ID:        fmt.Sprintf("user #%d", cpu),
			Name:      fmt.Sprintf("user #%d", cpu),
			Shared:    testNode0,
			Exclusive: NewCpuMask(cpu),
			Charge:    100,
		}), "add user #%d", cpu)
	}

	// One pool per declared shared set: the bounding pool and node0's,
	// however many users share it.
	require.Equal(t, 2, len(a.pools), "pools: bounding pool plus single node0 pool")

	// Constraint sets do not grow with the user count: pools with equal
	// effective sets are deduplicated.
	require.Equal(t, sets, len(a.constraints()), "constraint sets do not grow")
}

// TestConstraintsAreOrdered pins that constraint sets come out smallest
// first, then by key, so a truncated enumeration answers the same on
// every call.
func TestConstraintsAreOrdered(t *testing.T) {
	a := NewAccounting(testAllCpus)

	for _, u := range []*CpuUsage{
		{ID: "node1", Name: "node1", Shared: testNode1, Charge: 500},
		{ID: "node0", Name: "node0", Shared: testNode0, Charge: 500},
		{ID: "cpu4", Name: "cpu4", Shared: testCpu4, Charge: 500},
		{ID: "die0", Name: "die0", Shared: NewCpuMask(cpuRange(0, 3)...), Charge: 500},
	} {
		require.NoError(t, a.insert(u), "add usage %q", u.ID)
	}

	want := []string{"4", "0-1", "2-3", "0-3", "0-7"}

	// Repeatedly, since a single call could come out ordered by luck.
	for call := 0; call < 8; call++ {
		got := []string{}
		for _, c := range a.constraints() {
			got = append(got, c.String())
		}
		require.Equal(t, want, got, "constraint sets, call #%d", call)
	}
}

// TestWorkedExample is the worked example in doc.go.
func TestWorkedExample(t *testing.T) {
	all := NewCpuMask(cpuRange(0, 3)...)
	a := NewAccounting(all)
	for _, u := range []*CpuUsage{
		{ID: "a", Name: "a", Shared: NewCpuMask(0, 1), Charge: 1500},
		{ID: "b", Name: "b", Shared: NewCpuMask(2, 3), Charge: 1500},
	} {
		_, _, err := a.Allocate(u)
		require.NoError(t, err, "allocate %q", u.ID)
	}

	for _, tc := range []struct {
		set                      []int
		charge, limit, available int
	}{
		{[]int{1, 2}, 0, 2000, 1000},
		{[]int{0, 1}, 1500, 500, 500},
		{[]int{2, 3}, 1500, 500, 500},
		{[]int{0, 1, 2}, 1500, 1500, 1000},
		{[]int{1, 2, 3}, 1500, 1500, 1000},
		{[]int{0, 1, 2, 3}, 3000, 1000, 1000},
	} {
		cpus := NewCpuMask(tc.set...)
		t.Run(cpus.String(), func(t *testing.T) {
			require.Equal(t, tc.charge, a.Charge(cpus), "charge")
			require.Equal(t, tc.limit, a.Limit(cpus), "limit")
			avail, exact := a.Available(cpus)
			require.True(t, exact, "not truncated")
			require.Equal(t, tc.available, avail, "available")
		})
	}
}

func TestAvailableEach(t *testing.T) {
	a := testAccounting(t)
	probes := []*CpuMask{testNode0, testNode1, testCpu4, testCpu5}

	each, exact := a.AvailableEach(probes...)
	require.True(t, exact)
	require.Len(t, each, len(probes))

	for i, p := range probes {
		one, ok := a.Available(p)
		require.True(t, ok)
		require.Equal(t, one, each[i], "probe %s agrees with Available", p)
	}

	t.Run("no arguments", func(t *testing.T) {
		each, exact := a.AvailableEach()
		require.Empty(t, each)
		require.True(t, exact)
	})
}

func TestDirectCharge(t *testing.T) {
	a := NewAccounting(NewCpuMask(cpuRange(0, 7)...))
	_, _, err := a.Allocate(&CpuUsage{
		ID: "u", Name: "u", Shared: NewCpuMask(cpuRange(0, 7)...), Charge: 3500,
	})
	require.NoError(t, err)
	_, _, err = a.Allocate(&CpuUsage{ID: "x", Name: "x", Exclusive: NewCpuMask(6, 7)})
	require.NoError(t, err)

	// 3500 of shared charge plus 2000 for the two exclusive CPUs, even though
	// they belong to a user with no shared CPUs.
	direct, ok := a.DirectCharge(NewCpuMask(cpuRange(0, 7)...))
	require.True(t, ok, "0-7 is a pool")
	require.Equal(t, 5500, direct)

	_, ok = a.DirectCharge(NewCpuMask(0, 1))
	require.False(t, ok, "0-1 is not a pool, and must not report 0")
}

// TestChargeIdentity pins that Charge of a pool is its DirectCharge plus
// the charge of the pools nested inside it.
func TestChargeIdentity(t *testing.T) {
	a := testAccounting(t)
	requireChargeIdentity(t, a)

	// With a CPU of a pool in use taken exclusively, a pool bears a
	// charge of its own and a full CPU on top of it.
	require.NoError(t, a.insert(&CpuUsage{ID: "e", Name: "e", Exclusive: NewCpuMask(1)}))
	requireChargeIdentity(t, a)

	// And with two declared pools sharing a single effective set, where the
	// nested charge of one is the charge of the other.
	require.NoError(t, a.insert(&CpuUsage{
		ID: "cpu0", Name: "cpu0", Shared: NewCpuMask(0), Charge: 100,
	}))
	requireChargeIdentity(t, a)
}

// TestAvailableTruncation pins what a traversal cut short at
// maxImplicitPools reports. SetMaxImplicitPools clamps to a floor of
// 4096, so the test sets the unexported limit directly.
func TestAvailableTruncation(t *testing.T) {
	defer func(was int) { maxImplicitPools = was }(maxImplicitPools)
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	// Two pools overlap without either containing the other, so a probe
	// inside both must grow outward to find its binding limit.
	a := NewAccounting(NewCpuMask(cpuRange(0, 3)...))
	for _, u := range []*CpuUsage{
		{ID: "a", Name: "a", Shared: NewCpuMask(0, 1, 2), Charge: 1500},
		{ID: "b", Name: "b", Shared: NewCpuMask(1, 2, 3), Charge: 1500},
	} {
		_, _, err := a.Allocate(u)
		require.NoError(t, err)
	}

	probe := NewCpuMask(1, 2)

	// Untruncated: limit(1-2) is 2000, but growing to 0-3 finds 4000 - 3000.
	truth, exact := a.Available(probe)
	require.True(t, exact, "a small accounting does not truncate")
	require.Equal(t, 1000, truth, "the binding limit is the union 0-3")

	// Cut the traversal off after the first set it grows into. Constraint
	// sets go smallest first: 0-2 and 1-3, with the same limit, before 0-3.
	maxImplicitPools = 2

	got, exact := a.Available(probe)
	require.False(t, exact, "the traversal was cut short")
	require.Equal(t, 1500, got, "stopped at 0-2, never reached 0-3")
	require.Greater(t, got, truth, "a truncated answer is optimistic")
}

// TestAvailableTruncationManyProbes pins that a probe the cutoff never
// dequeues is still answered with its own limit, not boundless capacity.
// That makes an inexact answer an upper bound of the truth.
func TestAvailableTruncationManyProbes(t *testing.T) {
	defer func(was int) { maxImplicitPools = was }(maxImplicitPools)
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	pool0 := NewCpuMask(0, 1, 2)
	pool1 := NewCpuMask(1, 2, 3)

	newAccounting := func() *Accounting {
		a := NewAccounting(NewCpuMask(cpuRange(0, 3)...))
		for _, u := range []*CpuUsage{
			{ID: "a", Name: "a", Shared: pool0, Charge: 1500},
			{ID: "b", Name: "b", Shared: pool1, Charge: 1500},
		} {
			_, _, err := a.Allocate(u)
			require.NoError(t, err)
		}
		return a
	}

	a := newAccounting()

	// Untruncated both probes grow into 0-3 and find 4000 - 3000 there.
	truth, exact := a.AvailableEach(pool0, pool1)
	require.True(t, exact, "a small accounting does not truncate")
	require.Equal(t, []int{1000, 1000}, truth, "the binding limit is the union 0-3")

	// Two probes fill the budget on their own, so the enumeration stops after
	// dequeuing the first and never reaches the second.
	maxImplicitPools = 2

	got, exact := a.AvailableEach(pool0, pool1)
	require.False(t, exact, "the traversal was cut short")
	require.Equal(t, []int{1500, 1500}, got, "each probe's own limit, 3000 - 1500")

	// An answer never exceeds the probe's own limit, which Limit reports
	// exactly.
	for i, probe := range []*CpuMask{pool0, pool1} {
		require.LessOrEqual(t, got[i], a.Limit(probe),
			"available in %s exceeds its own limit", probe)
		require.GreaterOrEqual(t, got[i], truth[i],
			"available in %s is no upper bound of the truth", probe)
	}

	// The same cutoff on a capacity check, which enumerates one probe per
	// charged pool. A charge of 5000 fits neither pool's 1500 left. Each
	// pool is tried, since the order decides which probe is starved.
	for _, p := range []*CpuMask{pool0, pool1} {
		a := newAccounting()
		_, err := a.GetOffer(&CpuUsage{ID: "big", Name: "big", Shared: p, Charge: 5000})
		require.Error(t, err, "a charge of 5000 on pool %s", p)
		require.ErrorAs(t, err, new(*CapacityError), "refused for capacity, pool %s", p)
	}
}

// TestAvailableProbeRules pins the three-way probe rule: exact for the declared
// set, pessimistic for a subset, and unsafe for a superset. The last is the one
// somebody will otherwise "optimise" into existence.
func TestAvailableProbeRules(t *testing.T) {
	all := NewCpuMask(cpuRange(0, 7)...)
	a := NewAccounting(all)
	_, _, err := a.Allocate(&CpuUsage{
		ID: "q", Name: "q", Shared: NewCpuMask(cpuRange(0, 3)...), Charge: 3900,
	})
	require.NoError(t, err)

	super, _ := a.Available(all)
	require.Equal(t, 4100, super, "probing 0-7 looks roomy")

	exact, _ := a.Available(NewCpuMask(cpuRange(0, 3)...))
	require.Equal(t, 100, exact, "probing the set which will be declared")

	// The superset probe said 4100; declaring 0-3 with 1000 does not fit.
	_, _, err = a.Allocate(&CpuUsage{
		ID: "x", Name: "x", Shared: NewCpuMask(cpuRange(0, 3)...), Charge: 1000,
	})
	require.Error(t, err, "a superset probe is not a safe pre-check")
}

// TestNestedExclusiveAllocations pins that overlapping and nested exclusive
// allocations compose, and that releasing them restores the accounting exactly.
func TestNestedExclusiveAllocations(t *testing.T) {
	a := testAccounting(t)
	before := testAvailable(t, a)

	_, _, err := a.Allocate(&CpuUsage{ID: "e1", Name: "e1", Exclusive: NewCpuMask(0)})
	require.NoError(t, err)
	_, _, err = a.Allocate(&CpuUsage{ID: "e2", Name: "e2", Exclusive: NewCpuMask(5)})
	require.NoError(t, err)

	require.NotEqual(t, before, testAvailable(t, a), "capacity changed")

	_, err = a.Release("e2")
	require.NoError(t, err)
	_, err = a.Release("e1")
	require.NoError(t, err)

	require.Equal(t, before, testAvailable(t, a), "releasing restores it exactly")
}

// TestAvailableCountsShrunkenPoolCharge pins that a pool's charge bears
// on its effective set, not its declared one.
func TestAvailableCountsShrunkenPoolCharge(t *testing.T) {
	a := NewAccounting(testAllCpus)
	_, _, err := a.Allocate(&CpuUsage{ID: "sh", Name: "sh", Shared: testNode0, Charge: 750})
	require.NoError(t, err)
	_, _, err = a.Allocate(&CpuUsage{ID: "ex", Name: "ex", Exclusive: NewCpuMask(1)})
	require.NoError(t, err)

	// Pool node0's effective set is {0}, and its 750 must count against it.
	require.Equal(t, 250, available(t, a, NewCpuMask(0)), "CPU 0 bears node0's charge")
}

func TestAvailable(t *testing.T) {
	type testCase struct {
		name   string
		cpus   *CpuMask
		usage  []*CpuUsage
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
			usage: []*CpuUsage{
				{ID: "user1@node0", Name: "user1@node0", Shared: NewCpuMask(0, 1), Charge: 750},
				{ID: "user2@node0", Name: "user2@node0", Shared: NewCpuMask(0, 1), Charge: 250},
				{ID: "user1@socket0", Name: "user1@socket0", Shared: NewCpuMask(0, 1, 2, 3), Charge: 250},
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
			usage: []*CpuUsage{
				{ID: "exclusive", Name: "exclusive", Exclusive: NewCpuMask(0)},
				{ID: "shared", Name: "shared", Shared: NewCpuMask(2, 3), Charge: 500},
			},
			expect: map[string]int{
				"0":   0,    // 1000 - 1000
				"0-1": 1000, // 2000 - 1000
				"2-3": 1500, // 2000 - 500
				"0-3": 2500, // 4000 - 1000 - 500
			},
		},
		{
			// A charge is limited to the shared CPUs, while
			// exclusive CPUs are taken from every set containing
			// them.
			name: "exclusive CPUs with a shared charge",
			cpus: NewCpuMask(0, 1, 2, 3),
			usage: []*CpuUsage{
				{
					ID:        "both",
					Name:      "both",
					Exclusive: NewCpuMask(0),
					Shared:    NewCpuMask(1, 2, 3),
					Charge:    2000,
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
			usage: []*CpuUsage{
				{ID: "exclusive", Name: "exclusive", Exclusive: NewCpuMask(0, 1)},
			},
			expect: map[string]int{
				"0":   0,
				"0-1": 0,
				"2-3": 2000,
				"0-3": 2000, // 4000 - 2000
			},
		},
		{
			// Nothing prevents overcommitting a pool. Available
			// reports the excess as negative, but slack elsewhere
			// can hide it, as it does for 4-7 here.
			name: "overcommitted pool",
			cpus: NewCpuMask(0, 1, 2, 3, 4, 5, 6, 7),
			usage: []*CpuUsage{
				{ID: "user1@node0", Name: "user1@node0", Shared: NewCpuMask(0, 1), Charge: 2500},
			},
			expect: map[string]int{
				"0-1": -500, // 2000 - 2500
				"0-3": 1500, // 4000 - 2500
				"4-7": 4000, // unaffected, no pool of ours limits it
				"0-7": 5500, // 8000 - 2500
			},
		},
		{
			// 0,1 and 2,3 do not intersect, but the pseudo-pool 1,2
			// chains them, so their union 0-3 is a limit to check.
			name: "chained pseudo-pool",
			cpus: NewCpuMask(0, 1, 2, 3),
			usage: []*CpuUsage{
				{ID: "left", Name: "left", Shared: NewCpuMask(0, 1), Charge: 500},
				{ID: "middle", Name: "middle", Shared: NewCpuMask(1, 2), Charge: 1000},
				{ID: "right", Name: "right", Shared: NewCpuMask(2, 3), Charge: 1500},
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
				require.NoError(t, a.insert(u), "add usage %s", u.ID)
			}

			for cpus, expect := range tc.expect {
				mask, err := ParseCpuMask(cpus)
				require.NoError(t, err, "parse CPUs %q", cpus)
				require.Equal(t, expect, available(t, a, mask), "available in %q", cpus)
			}
		})
	}
}

// TestSharedAndUsedPoolsReportEffectiveSets pins that both accessors report
// what users can run on, not what they declared.
func TestSharedAndUsedPoolsReportEffectiveSets(t *testing.T) {
	a := NewAccounting(testAllCpus)
	_, _, err := a.Allocate(&CpuUsage{ID: "sh", Name: "sh", Shared: testNode0, Charge: 500})
	require.NoError(t, err)
	_, _, err = a.Allocate(&CpuUsage{ID: "ex", Name: "ex", Exclusive: NewCpuMask(1)})
	require.NoError(t, err)

	require.True(t, a.SharedCpus().Equals(NewCpuMask(0)), "SharedCpus excludes CPU 1")

	used := a.UsedPools()
	require.Contains(t, used, NewCpuMask(0).Key(), "keyed by the effective set")
	require.NotContains(t, used, testNode0.Key(), "not by the declared set")
}

// TestPoolEffectiveSets pins that a pool tracks its declared set separately
// from the set its users can run on.
func TestPoolEffectiveSets(t *testing.T) {
	a := NewAccounting(testAllCpus)

	require.NoError(t, a.insert(&CpuUsage{ID: "shared", Name: "shared", Shared: testNode0, Charge: 500}))

	p := a.pools[testNode0.Key()]
	require.NotNil(t, p, "pool for node0")
	require.True(t, p.cpus.Equals(testNode0), "declared set")
	require.True(t, p.eff().Equals(testNode0), "effective set with nothing exclusive")

	// Take CPU 5, outside node0: node0's effective set is unaffected.
	require.NoError(t, a.insert(&CpuUsage{ID: "excl", Name: "excl", Exclusive: testCpu5}))
	require.True(t, p.eff().Equals(testNode0), "effective set after unrelated exclusive")

	requireEffectiveCache(t, a)
}

// requireNobodyChoked checks that every pool with users and CPUs of its
// own has a CPU left for them. Only the pool with no CPUs may have an
// empty effective set.
func requireNobodyChoked(t *testing.T, a *Accounting) {
	t.Helper()

	for key, p := range a.pools {
		if p.cpus.IsEmpty() || len(p.users) == 0 {
			continue
		}
		require.False(t, p.eff().IsEmpty(),
			"pool %q: %d users choked, declared %s, all of it exclusive",
			key, len(p.users), p.cpus)
	}
}

// requireChargeIdentity checks that Charge of each pool's CPUs is its
// DirectCharge plus the charge of every pool whose users run only
// within it.
func requireChargeIdentity(t *testing.T, a *Accounting) {
	t.Helper()

	for key, p := range a.pools {
		// The pool with no CPUs is exempt: nothing counts an empty
		// set, and it carries no charge.
		if p.cpus.IsEmpty() {
			continue
		}

		direct, ok := a.DirectCharge(p.cpus)
		require.True(t, ok, "pool %s", key)

		nested := 0
		for k2, p2 := range a.pools {
			if k2 == key || p2.eff().IsEmpty() || !p2.eff().IsSubsetOf(p.cpus) {
				continue
			}
			nested += p2.charge()
		}

		require.Equal(t, a.Charge(p.cpus), direct+nested,
			"Charge == Direct + nested for %s", key)
	}
}

// requireEffectiveCache checks that every pool's cached effective set
// equals one recomputed from scratch.
func requireEffectiveCache(t *testing.T, a *Accounting) {
	t.Helper()

	for key, p := range a.pools {
		want := p.cpus.Difference(a.exclusive)
		require.True(t, p.eff().Equals(want),
			"pool %q: cached effective %s, recomputed %s", key, p.eff(), want)

		// The nil-when-unchanged representation must hold too, or two pools
		// with equal effective sets could disagree on Key().
		if p.cpus.Equals(want) {
			require.Nil(t, p.effective, "pool %q: cache set while unchanged", key)
		}
	}
}

// TestInternalMasksAreSealed pins that every CPU set the accounting holds
// is sealed, so a mask shared by a pool, a user and an offer cannot be
// edited through any of them.
func TestInternalMasksAreSealed(t *testing.T) {
	requireSealed := func(t *testing.T, what string, m *CpuMask) {
		t.Helper()

		require.NotNil(t, m, "%s: exists", what)
		require.Panics(t, func() { m.Set(7) }, "%s: sealed", what)
	}

	t.Run("the accounting's own", func(t *testing.T) {
		a := testAccounting(t)

		// CPU 1 goes exclusive, so node0's effective set differs
		// from its declared one, and a.exclusive has been rebuilt.
		_, _, err := a.Allocate(&CpuUsage{ID: "ex1", Name: "ex1", Exclusive: NewCpuMask(1)})
		require.NoError(t, err)

		requireSealed(t, "the bounding set", a.cpus)
		requireSealed(t, "the exclusively allocated CPUs", a.exclusive)

		effective := 0
		for key, p := range a.pools {
			requireSealed(t, fmt.Sprintf("pool %q declared CPUs", key), p.cpus)
			if p.effective != nil {
				requireSealed(t, fmt.Sprintf("pool %q effective CPUs", key), p.effective)
				effective++
			}
		}
		require.Greater(t, effective, 0, "some pool has a narrowed effective set")

		for id, u := range a.users {
			requireSealed(t, fmt.Sprintf("user %q shared CPUs", id), u.shared)
			requireSealed(t, fmt.Sprintf("user %q exclusive CPUs", id), u.exclusive)
		}
	})

	t.Run("the ones an offer and a release report", func(t *testing.T) {
		a := NewAccounting(testAllCpus)
		require.NoError(t, allocate(a, &CpuUsage{
			ID: "sh", Name: "sh", Shared: testNode0, Charge: 500,
		}))

		// Taking CPU 1 narrows what "sh" runs on, so the offer carries a change.
		o, err := a.GetOffer(&CpuUsage{ID: "ex", Name: "ex", Exclusive: NewCpuMask(1)})
		require.NoError(t, err)

		requireSealed(t, "the CPUs of the requester", o.cpus)
		require.Len(t, o.updates, 1, "one user to re-pin")
		requireSealed(t, "the CPUs of a change", o.updates[0].cpus)

		_, _, err = o.Commit()
		require.NoError(t, err)

		// Release builds its masks in changesIn and must seal them.
		changes, err := a.Release("ex")
		require.NoError(t, err)
		require.Len(t, changes, 1)
		requireSealed(t, "the CPUs of a change on release", changes[0].cpus)
	})
}
