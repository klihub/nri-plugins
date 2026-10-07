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
	"testing"

	logger "github.com/containers/nri-plugins/pkg/log"
	"github.com/stretchr/testify/require"
)

// TestAdmitsAgreesWithGetOffer pins that Admits answers as GetOffer does
// for every usage shape, since each takes its own path through Admits.
func TestAdmitsAgreesWithGetOffer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *CpuUsage
	}{
		{"BestEffort", &CpuUsage{ID: "be", Name: "be", Shared: testNode1}},
		{"shared-only which fits", &CpuUsage{
			ID: "s", Name: "s", Shared: testNode1, Charge: 500,
		}},
		{"shared-only which does not", &CpuUsage{
			ID: "s", Name: "s", Shared: testNode0, Charge: 9000,
		}},
		{"exclusive-only which fits", &CpuUsage{ID: "e", Name: "e", Exclusive: testCpu5}},
		{"exclusive-only which chokes a user", &CpuUsage{
			ID: "e", Name: "e", Exclusive: testNode0,
		}},
		{"mixed", &CpuUsage{
			ID: "m", Name: "m", Shared: testNode1, Exclusive: NewCpuMask(2), Charge: 250,
		}},
		{"declares nothing at all", &CpuUsage{ID: "n", Name: "n"}},
		{"a replacement of a user we have", &CpuUsage{
			ID: "shared1", Name: "shared1", Shared: testNode1, Charge: 500,
		}},
		{"a replacement which frees as much as it takes", &CpuUsage{
			ID: "shared1", Name: "shared1", Shared: testNode0, Charge: 500,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Two accountings, so neither call answers about what
			// the other did. Neither mutates, as asserted below.
			byOffer := testAccounting(t)
			_, offerErr := byOffer.GetOffer(tc.usage)

			byAdmits := testAccounting(t)
			before := testAvailable(t, byAdmits)

			v := byAdmits.Admits(tc.usage)

			require.Len(t, v, 1, "one verdict per candidate")
			require.Equal(t, offerErr == nil, v[0].Admits,
				"verdict, where the offer said %v", offerErr)
			require.Equal(t, offerErr == nil, v[0].Err == nil,
				"an inadmissible candidate says why, an admissible one does not")
			require.True(t, v[0].Exact, "a small accounting does not truncate")
			require.Equal(t, before, testAvailable(t, byAdmits), "Admits changed nothing")
		})
	}
}

// TestAdmitsClassifiesWhyNot pins that the error type tells why a
// candidate is inadmissible, as for every other capacity answer.
func TestAdmitsClassifiesWhyNot(t *testing.T) {
	a := testAccounting(t)

	v := a.Admits(
		&CpuUsage{ID: "ok", Name: "ok", Shared: testNode1, Charge: 250},
		nil,
		&CpuUsage{ID: "", Name: "no ID"},
		&CpuUsage{ID: "neg", Name: "neg", Shared: testNode1, Charge: -1},
		&CpuUsage{ID: "big", Name: "big", Shared: testNode0, Charge: 9000},
	)
	require.Len(t, v, 5, "one verdict per candidate, positionally")

	require.True(t, v[0].Admits, "a usage which fits")
	require.NoError(t, v[0].Err, "and it says nothing further")

	// Malformed: cannot be considered at all, which is not a capacity verdict.
	for i, name := range []string{"nil usage", "empty ID", "negative charge"} {
		var ce *CapacityError
		require.False(t, v[i+1].Admits, name)
		require.Error(t, v[i+1].Err, name)
		require.False(t, errors.As(v[i+1].Err, &ce), "%s is not a capacity problem", name)
	}

	// Well formed, but too big. Its type says so.
	var ce *CapacityError
	require.False(t, v[4].Admits, "a usage which does not fit")
	require.True(t, errors.As(v[4].Err, &ce), "a lacking capacity is a *CapacityError")
	require.Equal(t, 7750, ce.Lacking, "milli-CPU lacking")
}

func TestAdmitsWithoutCandidates(t *testing.T) {
	require.Empty(t, testAccounting(t).Admits(), "no candidates, no verdicts")
}

// TestHeadroomScoresDeclaredPools pins that Headroom reports, per
// candidate, the capacity in the pool it declares, as the accounting
// stands now.
func TestHeadroomScoresDeclaredPools(t *testing.T) {
	a := testAccounting(t)

	room, exact, err := a.Headroom(
		&CpuUsage{ID: "a", Name: "a", Shared: testNode0, Charge: 100},
		&CpuUsage{ID: "b", Name: "b", Shared: testNode1, Charge: 100},
		&CpuUsage{ID: "c", Name: "c", Shared: testNode1, Charge: 0},
	)
	require.NoError(t, err)
	require.True(t, exact, "a small accounting does not truncate")
	require.Equal(t, []int{
		available(t, a, testNode0),
		available(t, a, testNode1),
		available(t, a, testNode1),
	}, room, "the headroom of each declared pool")
}

// TestHeadroomRefusesWhatItCannotScore pins that Headroom refuses a
// candidate without a shared set, and with it the whole call.
func TestHeadroomRefusesWhatItCannotScore(t *testing.T) {
	a := testAccounting(t)

	for _, tc := range []struct {
		name  string
		usage *CpuUsage
	}{
		{"exclusive-only", &CpuUsage{ID: "e", Name: "e", Exclusive: testCpu5}},
		{"declares nothing at all", &CpuUsage{ID: "n", Name: "n"}},
		{"nil usage", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := a.Headroom(tc.usage)
			require.Error(t, err, "no shared set to score")
		})
	}

	t.Run("one bad candidate refuses the whole call", func(t *testing.T) {
		room, _, err := a.Headroom(
			&CpuUsage{ID: "a", Name: "a", Shared: testNode0, Charge: 100},
			&CpuUsage{ID: "e", Name: "e", Exclusive: testCpu5},
		)
		require.Error(t, err)
		require.Nil(t, room, "no partial answers")
	})
}

// TestHeadroomIgnoresExclusiveCpusOfAMixedCandidate pins that a mixed
// candidate is scored on its shared set alone. Admits judges its
// exclusive CPUs.
func TestHeadroomIgnoresExclusiveCpusOfAMixedCandidate(t *testing.T) {
	a := testAccounting(t)

	room, _, err := a.Headroom(&CpuUsage{
		ID: "m", Name: "m", Shared: testNode1, Exclusive: NewCpuMask(2), Charge: 250,
	})
	require.NoError(t, err, "a mixed candidate declares a shared set")
	require.Equal(t, []int{available(t, a, testNode1)}, room)
}

// TestAdmitsAgreesOnAnOvercommittedAccounting pins that Admits, like
// GetOffer, refuses everything while any part of the accounting is over
// capacity, even a usage in an untouched region.
func TestAdmitsAgreesOnAnOvercommittedAccounting(t *testing.T) {
	newOvercommitted := func(t *testing.T) *Accounting {
		t.Helper()

		a := NewAccounting(testAllCpus)
		// 2750 charged on node0's 2000. node1 and CPUs 4-7 are untouched.
		for _, u := range []*CpuUsage{
			{ID: "greedy", Name: "greedy", Shared: testNode0, Charge: 2000},
			{ID: "greedier", Name: "greedier", Shared: testNode0, Charge: 750},
		} {
			require.NoError(t, a.insert(u), "add usage %q", u.ID)
		}
		require.Equal(t, 750, overcommitOf(t, a), "node0 is over its capacity")

		return a
	}

	for _, tc := range []struct {
		name  string
		usage *CpuUsage
	}{
		{"BestEffort in an untouched pool", &CpuUsage{
			ID: "n", Name: "n", Shared: testNode1,
		}},
		{"shared-only in an untouched pool", &CpuUsage{
			ID: "n", Name: "n", Shared: testNode1, Charge: 250,
		}},
		{"exclusive-only in untouched CPUs", &CpuUsage{
			ID: "n", Name: "n", Exclusive: testCpu5,
		}},
		{"declares nothing at all", &CpuUsage{ID: "n", Name: "n"}},
		{"a replacement which frees the overcommit", &CpuUsage{
			ID: "greedier", Name: "greedier", Shared: testNode0, Charge: 0,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			byOffer := newOvercommitted(t)
			_, offerErr := byOffer.GetOffer(tc.usage)

			byAdmits := newOvercommitted(t)
			v := byAdmits.Admits(tc.usage)

			require.Len(t, v, 1, "one verdict per candidate")
			require.Equal(t, offerErr == nil, v[0].Admits,
				"verdict, where the offer said %v", offerErr)
			require.Equal(t, offerErr == nil, v[0].Err == nil,
				"whether it says why, where the offer said %v", offerErr)
		})
	}
}

// TestAdmitsBatchingChangesNothing pins that the verdicts, and their
// agreement with an offer, do not depend on how many candidates are
// passed at once, even when the enumeration is cut short.
func TestAdmitsBatchingChangesNothing(t *testing.T) {
	defer func(was int) { maxImplicitPools = was }(maxImplicitPools)
	defer logger.SetOutput(logger.SetOutput(io.Discard))

	// Two pools overlap without either containing the other, so a probe
	// must grow outward to find its binding limit.
	newAccounting := func(t *testing.T) *Accounting {
		t.Helper()

		a := NewAccounting(NewCpuMask(cpuRange(0, 3)...))
		for _, u := range []*CpuUsage{
			{ID: "a", Name: "a", Shared: NewCpuMask(0, 1, 2), Charge: 1500},
			{ID: "b", Name: "b", Shared: NewCpuMask(1, 2, 3), Charge: 1500},
		} {
			require.NoError(t, a.insert(u), "add usage %q", u.ID)
		}

		return a
	}

	candidates := []*CpuUsage{
		{ID: "x", Name: "x", Shared: NewCpuMask(0, 1), Charge: 250},
		{ID: "y", Name: "y", Shared: NewCpuMask(1, 2), Charge: 250},
		{ID: "z", Name: "z", Shared: NewCpuMask(2, 3), Charge: 250},
		{ID: "w", Name: "w", Shared: NewCpuMask(0, 1, 2), Charge: 0},
		{ID: "v", Name: "v", Exclusive: NewCpuMask(0)},
	}

	// 4096 is the production floor, so it means no truncation. The small
	// cutoffs cut the traversal at and within the probe list.
	for _, cutoff := range []int{1, 2, 3, 4, 4096} {
		t.Run(fmt.Sprintf("cutoff=%d", cutoff), func(t *testing.T) {
			maxImplicitPools = cutoff

			batched := newAccounting(t).Admits(candidates...)
			require.Len(t, batched, len(candidates), "one verdict per candidate")

			for i, u := range candidates {
				alone := newAccounting(t).Admits(u)
				require.Equal(t, alone[0].Admits, batched[i].Admits,
					"candidate %q judged alone and in a batch", u.ID)

				_, offerErr := newAccounting(t).GetOffer(u)
				require.Equal(t, offerErr == nil, batched[i].Admits,
					"candidate %q, where the offer said %v", u.ID, offerErr)
			}
		})
	}
}

// TestAdmitsAtTheExactBoundary pins that a charge equal to the capacity
// left fits, one milli-CPU more does not, and the lacking amount is the
// difference. No other test tells these apart. The numbers are
// cross-checked against an offer.
func TestAdmitsAtTheExactBoundary(t *testing.T) {
	a := testAccounting(t)

	room := available(t, a, testNode0)
	require.Greater(t, room, 0, "node0 has capacity left to aim at")

	fits := &CpuUsage{ID: "exact", Name: "exact", Shared: testNode0, Charge: room}
	over := &CpuUsage{ID: "one more", Name: "one more", Shared: testNode0, Charge: room + 1}

	v := a.Admits(fits, over)
	require.Len(t, v, 2)

	require.True(t, v[0].Admits, "a charge equal to the capacity left")
	require.NoError(t, v[0].Err)

	var lacking *CapacityError
	require.False(t, v[1].Admits, "one milli-CPU more than the capacity left")
	require.True(t, errors.As(v[1].Err, &lacking), "a candidate which does not fit")
	require.Equal(t, 1, lacking.Lacking, "milli-CPU lacking")

	// What an offer says about the same two usages.
	_, err := a.GetOffer(fits)
	require.NoError(t, err, "the offer agrees the exact charge fits")

	var offered *CapacityError
	_, err = a.GetOffer(over)
	require.True(t, errors.As(err, &offered), "the offer refuses one more")
	require.Equal(t, offered.Lacking, lacking.Lacking,
		"Admits and the offer report the same shortfall")
}

// TestOfferHeadroomIsRoomToGrow pins that an offer's reported headroom
// is exactly how much more a shared-only redeclaration of the same pool
// could charge.
func TestOfferHeadroomIsRoomToGrow(t *testing.T) {
	a := testAccounting(t)

	o, err := a.GetOffer(&CpuUsage{ID: "u", Name: "u", Shared: testNode0, Charge: 250})
	require.NoError(t, err)

	room, exact, err := o.Headroom()
	require.NoError(t, err)
	require.True(t, exact, "a small accounting does not truncate")
	require.Greater(t, room, 0, "there is room left to grow into")

	_, _, err = o.Commit()
	require.NoError(t, err)

	v := a.Admits(
		&CpuUsage{ID: "u", Name: "u", Shared: testNode0, Charge: 250 + room},
		&CpuUsage{ID: "u", Name: "u", Shared: testNode0, Charge: 250 + room + 1},
	)
	require.True(t, v[0].Admits, "growing the charge by exactly the headroom reported")
	require.False(t, v[1].Admits, "one milli-CPU more than that")
}

// TestOfferHeadroomIsTheCommittedState pins that the number describes the state
// the offer would leave behind, for each shape which has a pool to report on.
func TestOfferHeadroomIsTheCommittedState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		usage *CpuUsage
	}{
		{"shared-only", &CpuUsage{
			ID: "s", Name: "s", Shared: testNode0, Charge: 250,
		}},
		// node1 has no users, so it is not among the enumeration's
		// seeds and must be added as a probe of its own.
		{"BestEffort in an uncharged pool", &CpuUsage{
			ID: "b", Name: "b", Shared: testNode1,
		}},
		{"mixed", &CpuUsage{
			ID: "m", Name: "m", Shared: testNode1, Exclusive: NewCpuMask(2), Charge: 250,
		}},
		{"a replacement", &CpuUsage{
			ID: "shared1", Name: "shared1", Shared: testNode0, Charge: 1000,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testAccounting(t)

			o, err := a.GetOffer(tc.usage)
			require.NoError(t, err)
			offered, exact, err := o.Headroom()
			require.NoError(t, err)
			require.True(t, exact)

			_, _, err = o.Commit()
			require.NoError(t, err)

			// Headroom probes the declared set, the offer the
			// effective one. Both have the same limit: an
			// exclusive CPU adds 1000 of capacity and 1000 of
			// charge, which cancel.
			after, exact, err := a.Headroom(tc.usage)
			require.NoError(t, err)
			require.True(t, exact)
			require.Equal(t, after[0], offered,
				"the offer's headroom is what the committed state has left")
		})
	}
}

// TestOfferHeadroomCountsDeletedCapacity pins that a mixed offer's
// headroom falls by more than its charge: its exclusive CPUs take their
// capacity out of the pool.
func TestOfferHeadroomCountsDeletedCapacity(t *testing.T) {
	a := testAccounting(t)

	usage := &CpuUsage{
		ID: "m", Name: "m", Shared: testNode1, Exclusive: NewCpuMask(2), Charge: 250,
	}

	before, _, err := a.Headroom(usage)
	require.NoError(t, err)

	o, err := a.GetOffer(usage)
	require.NoError(t, err)
	after, _, err := o.Headroom()
	require.NoError(t, err)

	require.Equal(t, before[0]-250-1000, after,
		"the charge and the capacity of the CPU taken exclusively both leave")
}

func TestOfferHeadroomRefusesWithoutAPool(t *testing.T) {
	a := testAccounting(t)

	for _, tc := range []struct {
		name  string
		usage *CpuUsage
	}{
		{"exclusive-only", &CpuUsage{ID: "e", Name: "e", Exclusive: testCpu5}},
		{"declares nothing at all", &CpuUsage{ID: "n", Name: "n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, err := a.GetOffer(tc.usage)
			require.NoError(t, err)

			_, _, err = o.Headroom()
			require.Error(t, err, "no shared pool to report headroom for")
		})
	}
}

// greedyExclusive takes the CPUs of pool one by one, keeping each one Admits
// accepts. Laminar constraints form a matroid, so greedy hits the max in any
// order. It is the ground truth for ExclusiveCapacity.
func greedyExclusive(t *testing.T, a *Accounting, pool *CpuMask) int {
	t.Helper()

	taken := NewCpuMask()
	free := pool.Intersection(a.AllCpus()).Difference(a.ExclusiveCpus())

	free.ForEachCpu(func(cpu int) bool {
		try := taken.Union(NewCpuMask(cpu))
		v := a.Admits(&CpuUsage{ID: "greedy", Name: "greedy", Exclusive: try})
		require.True(t, v[0].Exact, "greedy probe cut short")
		if v[0].Admits {
			taken = try
		}
		return true
	})

	return taken.Size()
}

// exclusiveCapacity calls ExclusiveCapacity, failing on error.
func exclusiveCapacity(t *testing.T, a *Accounting, pool *CpuMask) (int, bool) {
	t.Helper()

	n, exact, err := a.ExclusiveCapacity(pool)
	require.NoError(t, err, "capacity of %s", pool)

	return n, exact
}

// TestExclusiveCapacity pins numbers on the stock accounting: node0 holds
// 750m on 2 CPUs, CPU 4 is exclusive, and the rest is idle. Each number is
// also checked against greedy.
func TestExclusiveCapacity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pool   *CpuMask
		expect int
	}{
		// 750m on 2 CPUs leaves room for 1 whole CPU.
		{"charged node", testNode0, 1},
		// No pool inside. The machine has 6 CPUs of room, the node 2 CPUs.
		{"idle node", testNode1, 2},
		// 7 free CPUs; node0 keeps one.
		{"all CPUs", testAllCpus, 6},
		{"CPU already exclusive", testCpu4, 0},
		{"idle CPU", testCpu5, 1},
		{"empty set", NewCpuMask(), 0},
		// Not a pool; it crosses node0, which caps its half at 1.
		{"set across a pool", NewCpuMask(1, 2, 3), 3},
		{"set across a pool, two in it", NewCpuMask(0, 1, 2), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testAccounting(t)
			before := testAvailable(t, a)

			n, exact := exclusiveCapacity(t, a, tc.pool)
			require.True(t, exact, "nested pools give exact answer")
			require.Equal(t, tc.expect, n, "CPUs takeable from %s", tc.pool)
			require.Equal(t, greedyExclusive(t, a, tc.pool), n, "agrees with greedy")
			require.Equal(t, before, testAvailable(t, a), "nothing changed")
		})
	}
}

// TestExclusiveCapacityCountsNestedCharges pins the inner term: charges
// deep in a pool eat its capacity, not only its own charge.
func TestExclusiveCapacityCountsNestedCharges(t *testing.T) {
	a := NewAccounting(testAllCpus)
	half := NewCpuMask(0, 1, 2, 3)
	for _, u := range []*CpuUsage{
		{ID: "n0", Name: "n0", Shared: testNode0, Charge: 1000},
		{ID: "n1", Name: "n1", Shared: testNode1, Charge: 1000},
		{ID: "half", Name: "half", Shared: half, Charge: 1500},
	} {
		require.NoError(t, a.insert(u), "add user %q", u.ID)
	}

	// Each node alone: 1 spare CPU. Half: 4000-3500 = 500m, no whole CPU.
	n, exact := exclusiveCapacity(t, a, half)
	require.True(t, exact)
	require.Equal(t, 0, n, "half has no whole CPU spare")
	require.Equal(t, greedyExclusive(t, a, half), n)

	// Whole machine: half takes nothing; CPUs 4-7 are free.
	n, _ = exclusiveCapacity(t, a, testAllCpus)
	require.Equal(t, 4, n)
	require.Equal(t, greedyExclusive(t, a, testAllCpus), n)

	// The node alone takes 1; half above caps it at 0.
	n, _ = exclusiveCapacity(t, a, testNode0)
	require.Equal(t, 0, n, "ancestor caps nested pool")
	require.Equal(t, greedyExclusive(t, a, testNode0), n)
}

// TestExclusiveCapacityKeepsZeroChargeUserAlive pins the choke rule: a pool
// whose user charges nothing still keeps one CPU.
func TestExclusiveCapacityKeepsZeroChargeUserAlive(t *testing.T) {
	a := testAccounting(t)
	require.NoError(t, a.insert(&CpuUsage{ID: "be", Name: "be", Shared: testNode1}))

	n, exact := exclusiveCapacity(t, a, testNode1)
	require.True(t, exact)
	require.Equal(t, 1, n, "zero-charge user keeps one CPU")
	require.Equal(t, greedyExclusive(t, a, testNode1), n)
}

// TestExclusiveCapacityOvercommitted pins that an overcommitted accounting
// admits no take at all, so capacity is 0 everywhere, idle CPUs included.
func TestExclusiveCapacityOvercommitted(t *testing.T) {
	a := NewAccounting(testAllCpus)
	require.NoError(t, a.insert(&CpuUsage{
		ID: "over", Name: "over", Shared: testNode0, Charge: 2500,
	}))

	n, _ := exclusiveCapacity(t, a, testCpu5)
	require.Equal(t, 0, n, "nothing admissible while overcommitted")
	require.Equal(t, greedyExclusive(t, a, testCpu5), n)
}

func TestExclusiveCapacityRefuses(t *testing.T) {
	a := testAccounting(t)

	_, _, err := a.ExclusiveCapacity(nil)
	require.Error(t, err, "nil set")

	_, _, err = a.ExclusiveCapacity(NewCpuMask(7, 8))
	require.Error(t, err, "CPU 8 outside accounting")
}

// TestLaminar pins the predicate on declared pools: nested or disjoint pools
// are laminar, a single crossing pair is not.
func TestLaminar(t *testing.T) {
	a := testAccounting(t)
	require.True(t, a.Laminar(), "nodes inside machine")

	require.NoError(t, a.insert(&CpuUsage{
		ID: "x", Name: "x", Shared: NewCpuMask(1, 2), Charge: 100,
	}))
	require.False(t, a.Laminar(), "1-2 crosses both nodes")

	_, err := a.remove("x")
	require.NoError(t, err)
	require.True(t, a.Laminar(), "userless pool gone, laminar again")
}

// TestExclusiveCapacityNotLaminar pins the fallback: crossing pools
// give floor(Available/1000), flagged inexact, never below greedy.
func TestExclusiveCapacityNotLaminar(t *testing.T) {
	a := NewAccounting(testAllCpus)
	for _, u := range []*CpuUsage{
		{ID: "a", Name: "a", Shared: NewCpuMask(0, 1, 2), Charge: 1500},
		{ID: "b", Name: "b", Shared: NewCpuMask(2, 3, 4), Charge: 1500},
	} {
		require.NoError(t, a.insert(u), "add user %q", u.ID)
	}
	require.False(t, a.Laminar())

	pool := NewCpuMask(0, 1, 2, 3, 4)
	n, exact := exclusiveCapacity(t, a, pool)
	require.False(t, exact, "crossing pools give bound only")
	require.Equal(t, available(t, a, pool)/1000, n, "bound is floor(Available/1000)")
	require.GreaterOrEqual(t, n, greedyExclusive(t, a, pool), "bound never below max")
}

// TestExclusiveCapacityJudgesEffectiveSets pins that the answer is exact
// when declared pools cross but the crossing CPU, now exclusive, leaves
// the effective sets disjoint, even though Laminar says no.
func TestExclusiveCapacityJudgesEffectiveSets(t *testing.T) {
	a := NewAccounting(testAllCpus)
	for _, u := range []*CpuUsage{
		{ID: "a", Name: "a", Shared: NewCpuMask(0, 1, 2), Charge: 500},
		{ID: "b", Name: "b", Shared: NewCpuMask(2, 3, 4), Charge: 500},
		{ID: "x", Name: "x", Exclusive: NewCpuMask(2)},
	} {
		require.NoError(t, a.insert(u), "add user %q", u.ID)
	}
	require.False(t, a.Laminar(), "declared pools cross at CPU 2")

	n, exact := exclusiveCapacity(t, a, testAllCpus)
	require.True(t, exact, "effective sets 0-1 and 3-4 are disjoint")
	require.Equal(t, greedyExclusive(t, a, testAllCpus), n)
	require.Equal(t, 5, n, "each pool keeps one of its two, 3 idle")
}
