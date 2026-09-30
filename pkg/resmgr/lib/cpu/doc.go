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

// Package libcpu accounts for CPU capacity: who has taken how much of which
// CPUs, how much of a set of CPUs is free, and who must be re-pinned when that
// changes. It allocates nothing and picks no CPUs. A caller chooses CPUs by its
// own rules and asks the accounting whether it can take them.
//
// The package implies no CPU topology or hierarchy. It assumes only this:
//
//  1. Multiple shared allocations can be made from a set of CPUs.
//  2. Full CPUs can be allocated exclusively.
//  3. A single allocation can take both shared and exclusive CPUs.
//  4. An exclusively allocated CPU is absent from the CPUs any other user
//     runs on.
//
// Capacity is counted in milli-CPU; 1000 milli-CPU is one full CPU. A CPU
// allocated exclusively is charged its full 1000 against every set of CPUs
// containing it.
//
// # Pools, declared and effective
//
// A pool is a set of CPUs its users charge capacity against. A user declares
// the shared CPUs it wants and lands in the pool of exactly those CPUs, which
// is created if needed. The declared set is the pool's identity and never
// changes, so users never migrate between pools.
//
// What a pool's users run on is derived. With E(P) the effective set of pool P
// and X every CPU allocated exclusively to anyone:
//
//	E(P)        = P.cpus \ X
//	constraints = { E(P) : P a pool, E(P) ≠ ∅ }, deduplicated
//	Charge(S)   = 1000·|S ∩ X| + Σ{ charge of P : E(P) ⊆ S }
//	Limit(S)    = 1000·|S| − Charge(S)
//
// All capacity arithmetic uses effective sets. Take pool 0-1 charging 750 with
// CPU 1 taken exclusively. Keyed on the declared set, 0-1 ⊄ {0}, so {0} would
// look free while it is really 750-charged. Keyed on the effective set, both
// Limit(0-1) and [Accounting.Available] of {0} come out at 250.
//
// [Accounting.Charge] of a set S counts every pool inside S, since the users of
// those pools can only run inside S. A set nobody declared has a charge too, so
// it can limit an allocation.
//
// One well-formedness rule holds: a user which declared a non-empty shared set
// must never be left with an empty effective set. Breaking it is choking. An
// empty set is a subset of every set, so a charge on it would bear on all of
// them. The rule covers a user reserving no capacity too: it still needs
// somewhere to run. Users which declared no shared CPUs are exempt; they live
// in the pool with no CPUs. So is the bounding pool, which has no users, so
// every CPU may go exclusive. No capacity answer can see choking; the mutating
// calls reject it separately.
//
// # Allocation shapes
//
// Three shapes are accepted:
//
//	allocation       Shared  Exclusive     Charge  pool must stay inhabitable
//	exclusive-only   {}      the CPUs      0       no
//	shared-only      P       {}            >= 0    yes
//	mixed            P       CPUs ⊆ P      > 0     yes
//
// The shapes name what is declared, not a quality of service class. A
// shared-only usage with a charge of 0 needs a pool to run in, but is
// invisible to every capacity answer. A mixed usage with a charge of 0 is
// refused: it would keep a pool inhabitable and add nothing to any charge.
//
// A mixed usage declares Shared = P with Exclusive ⊆ P. Declaring only what is
// left of P would create a near-duplicate overlapping pool per container, the
// worst case for the enumeration below. The two sets need not be disjoint: the
// effective set already excludes the user's own exclusive CPUs.
//
// An exclusive-only usage must not declare Shared = P to record where its CPUs
// came from. Its CPUs are charged 1000 each against every set containing them,
// P included, without that. Declaring P would make it look like a shared-only
// usage with a charge of 0, and the two need opposite answers on whether P may
// be emptied of CPUs. Provenance beyond capacity is the caller's bookkeeping.
//
// # The implicit pool enumeration
//
// Every set of CPUs containing a probe limits a new user of that probe: users
// inside that set can only run inside it, so their charge plus the new one must
// fit. Unions of intersecting pools are such sets too: implicit pools, with
// limits of their own. So [Accounting.Available] grows the probe by unioning
// it with intersecting constraint sets, and takes the tightest limit it finds.
//
// Four results make this correct and affordable:
//
//  1. Unions of non-intersecting sets never bind while nothing is
//     overcommitted. Capacity and charge both add up over the disjoint parts,
//     so the union is at least as loose as its tightest part. If a part is
//     overcommitted, the answer may be optimistic.
//  2. Growth follows chains of intersections, not pairs. Two disjoint pools
//     bind together once a probe or an intermediate union bridges them.
//  3. Exclusive CPUs need no constraint sets of their own. The charge of such
//     a CPU is its full capacity, so adding one to a set never tightens its
//     limit. An overcommitted set therefore contains the effective set of some
//     charged pool, which seeds a whole-accounting check.
//  4. An exclusive allocation can be refused on capacity. Its CPU may sit in a
//     pool in use, and taking it narrows that pool's effective set.
//
// Growth only unions a constraint set into the set being grown, never one
// probe into another. So one traversal seeded with many probes visits what
// each would reach alone, and walks their common growth once.
//
// # A worked example
//
// An accounting bounded by 0-3, with pool 0-1 and pool 2-3 each charging 1500:
//
//	S      1000·|S|  Charge(S)  Limit(S)  Available(S)  contained pools
//	{1,2}      2000          0      2000          1000  none
//	0-1        2000       1500       500           500  0-1
//	2-3        2000       1500       500           500  2-3
//	0-2        3000       1500      1500          1000  0-1
//	1-3        3000       1500      1500          1000  2-3
//	0-3        4000       3000      1000          1000  both
//
// The table shows:
//
//   - Charge is monotone: widening a set only adds contained pools.
//   - Limit is not monotone: 500 → 1500 → 1000 along 0-1 → 0-2 → 0-3. Wider
//     sets add capacity, but can pull in whole pools' charges. So Available
//     must enumerate the unions; the largest containing set is not enough.
//   - Limit({1,2}) is 2000, but Available({1,2}) is 1000. {1,2} contains no
//     pool, yet chains the two disjoint pools into 0-3.
//   - Every set spanning both halves gets 1000. Each half alone is tighter at
//     500, bound by its own pool.
//   - Available(0-1) never visits 2-3: a charge on 0-1 does not burden a pool
//     it is not inside. It reaches 0-3 because 0-3 is a constraint set.
//
// So a user declaring shared 0-1 gets 500: pool 0-1 has 500 left, 0-3 has
// 1000. A user declaring shared 0-2 creates a pool with 1500 of its own, but
// still burdens 0-3, so it gets 1000.
//
// # Probing capacity
//
// Available(S) ≥ charge is exact for a shared-only usage declaring S, while
// nothing is overcommitted. A charge bears on exactly the sets containing its
// pool, and growth from S visits exactly those, including the chains a new
// pool S would bridge. So the probe must be the declared set:
//
//   - The exact declared set is exact.
//   - A subset is safe but pessimistic. By result 1 it minimises over more
//     sets, so it can only refuse where there was room.
//   - A superset is unsafe. Say pool 0-3 charges 3900 of its 4000, and nothing
//     else is charged. Available(0-7) is 8000 − 3900 = 4100, so a charge of
//     1000 on shared 0-3 looks fine, but pool 0-3 would go to 4900 against
//     4000. Available(0-3) = 100 refuses it.
//
// Available(E) ≥ n·1000 does not mean n CPUs of E can be taken exclusively. An
// exclusive CPU deletes capacity from every set containing it, including sets
// inside E, which growth from E never visits. Say pool P = 0-8 has no charge
// and pool Q = {0,1,2,3} charges 2900. Available(0-8) = 9000 − 2900 = 6100 ≥
// 3000, yet taking CPUs 0, 1 and 2 leaves Q with effective {3}: 1000 of
// capacity for 2900 of charge. Taking 6, 7 and 8 instead is fine. A charge
// propagates outward only; exclusivity propagates both ways.
//
// Available({c}) visits every set containing CPU c, so Available({c}) ≥ 1000
// is exact for the capacity of that one CPU. Several CPUs need one single-CPU
// probe each: a multi-CPU probe never sees a pool containing only part of it.
//
// Capacity is still not the whole verdict. No capacity answer sees choking,
// and a user reserving nothing is invisible to all of them. So a pool whose
// effective set is one CPU, hosting such a user, reports that CPU free at 1000,
// yet taking it is refused. Per-CPU answers do not compose either: three CPUs
// takeable one by one may be untakeable together, as in the 0-8 example.
// [Accounting.Admits] is the permit. It takes the usage, so none of these
// rules is the caller's to get right.
//
// [Accounting.AvailableEach] answers one probe per argument in one traversal.
// It walks their common growth once and snapshots the charged pools once.
// Benchmarks show the first saves nearly all the cost where many pools
// overlap. On nested topologies batching is cheaper too, for no proven reason.
//
// # What an allocator should use
//
//   - [Accounting.Admits] tells, per candidate usage, whether the accounting
//     would take it: the verdict of [Accounting.GetOffer] without the offer.
//     It checks every rule above, including those no capacity answer sees.
//   - [Accounting.Headroom] scores admitted candidates by the room in the pool
//     each declares, in one traversal. It is a score, not a permit, and it
//     refuses candidates with no shared set.
//   - [Accounting.GetOffer] on the few that survive. Only an offer reports the
//     churn of a choice, [Offer.Updates]: the running containers to re-pin.
//     [Offer.Headroom] is the room the pool would have left, counting the
//     candidate's own charge and exclusive CPUs.
//
// The accessors about sets of CPUs serve diagnostics and introspection. A
// caller using them instead of Admits takes on the probe rules above:
//
//   - [Accounting.Limit] of a set needs no enumeration. It never wrongly
//     refuses, so it refutes cheaply, but it cannot confirm.
//   - [Accounting.Available] and [Accounting.AvailableEach] of candidate
//     pools, one traversal for all. Each probe must be a pool's exact
//     declared set.
//   - [Accounting.AvailableEach] of single CPUs gives exact headroom per CPU:
//     a score for ranking, never a permit.
//   - [Accounting.Charge] and [Accounting.DirectCharge] explain why a set is
//     tight, not whether anything fits in it.
//
// Cost falls in that order. Limit refutes most candidates, one traversal
// answers for the rest, and offers run on a handful.
//
// # Exactness and truncation
//
// [Accounting.Limit] never enumerates, so it is always exact.
// [Accounting.Available] and [Accounting.AvailableEach] enumerate, and report
// true when exact. False means the traversal was cut short at the
// maxImplicitPools limit, and every value is an upper bound of the true one.
// Truncation is not an error: the bounds stay usable.
//
// # Charge and DirectCharge
//
// Both count exclusive CPUs at full capacity, and differ only in scope:
//
//	DirectCharge(S) = 1000·|S ∩ X| + Σ{ charge of users declaring exactly S }
//	Charge(S)       = DirectCharge(S) + Σ{ charge of P : E(P) ⊆ S, P ≠ S }
//
// DirectCharge is about the CPUs, not a pool's own users. For pool 0-7 with
// 3500 of shared charge and two of its CPUs held exclusively, it is 5500, even
// if the holder has no shared CPUs at all. For the shared figure alone,
// subtract 1000·|S ∩ ExclusiveCpus()|.
//
// There is no Capacity accessor: 1000·cpus.Size() consults no accounting state.
//
// # Offers
//
// [Accounting.GetOffer] checks a usage against the state the accounting would
// be in with it, without touching the real state. Any number of offers can
// coexist, so a caller can compare several by [Offer.Updates] before choosing.
// Every change to the accounting, [Offer.Commit] included, expires every
// outstanding offer: [Offer.IsValid] reports it and Commit fails with
// [ErrExpiredOffer].
//
// An ID the accounting already holds names the user the offer would replace,
// so committing it is a reallocation. The usage is that user's complete new
// declaration, judged against the state with the old usage gone. Refusing it
// would force a caller to release first, and a release followed by a failed
// offer leaves the accounting worse off than before.
//
// [Accounting.Allocate] and [Accounting.Reallocate] are GetOffer plus Commit,
// so either costs one enumeration, and the arithmetic has one implementation.
// Allocate insists the user is new. Reallocate does not: redeclaring a user
// the accounting does not have is an allocation. [Accounting.Release] cannot
// fail on capacity, since freeing only adds.
//
// Every public mutation is checked before anything is altered, so a rejected
// one cannot perturb the state. So Available is never negative on an
// accounting only the public API has touched, unless an enumeration was cut
// short: a truncated check can admit a usage it never saw bind.
//
// # Pool identity is the declared set
//
// Keying a pool on what is left of its CPUs would put every container taking a
// CPU exclusively into a near-duplicate pool of its own, overlapping its
// neighbours. Overlapping pools multiply the unions growth reaches, while
// nested ones collapse onto a handful. On an overlapping topology like the one
// BenchmarkAvailable exercises, that measured some 170 times the enumeration
// cost of one pool per declared set.
//
// The declared set also keeps the provenance. A CPU taken exclusively drops
// out of the effective sets containing it and reappears in exactly those when
// released, so a release restores the previous state exactly.
//
// # The maxImplicitPools limit
//
// Growth is exponential in the number of pools in the worst case. Real
// topologies give few distinct unions, so the limit is for pathological cases
// only. It caps the distinct sets one traversal visits, for all its probes
// together. [SetMaxImplicitPools] sets it, with a floor.
//
// The constraint sets are enumerated smallest first, then by key, since they
// are read off a map. The order cannot change a complete answer, but decides
// which sets survive truncation, and identical calls must answer the same.
// Smallest first is fixed, not tighter: in TestAvailableTruncation it truncates
// to 1500, while the reverse order gives 1000, the truth.
//
// # Concurrency
//
// An Accounting takes no locks; callers serialise concurrent use. Nothing of
// the caller's is retained and nothing internal is handed out: a usage is
// copied in, with its masks cloned and sealed, and every set of CPUs reported
// is a copy.
package libcpu
