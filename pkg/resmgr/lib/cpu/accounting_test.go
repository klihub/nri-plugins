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
