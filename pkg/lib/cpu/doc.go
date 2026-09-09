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

// Package libcpu provides set types for CPU ids.
//
// [CPUSet] is the interface. It has two implementations:
//
//   - [CpuMask] is dense: a bitmask with one bit per CPU. Set algebra runs a
//     word at a time. It suits sets which are large, long lived, or combined
//     often.
//   - [CpuSet] is sparse: it wraps k8s.io/utils/cpuset.CPUSet, a map. It suits
//     small sets, and code which mostly passes cpuset strings around.
//
// CpuMask wins almost every operation, for large sets by two or three orders
// of magnitude. It trails only marginally on Size and IsEmpty. For a table of
// both against the bare k8s.io/utils/cpuset.CPUSet, run
//
//	CPUSET_BENCH_COMPARE=1 go test -run TestCompareImplementations -v
//
// # Set-producing operations
//
// Clone, Union, Difference and Intersection are not in [CPUSet]. Each
// implementation returns its own type from them. Go has no covariant returns,
// so one interface cannot describe them without forcing callers to assert on
// the result. Callers almost always know what they hold, so they get concrete
// types back.
//
// A caller holding only a CPUSet wraps it in an [AnyCPUSet] to use those four.
// That costs a type switch per operation. [AsCpuMask] and [AsCpuSet] convert a
// set to a known implementation.
//
// # Coming in from k8s.io/utils/cpuset
//
// [WrapCpuSet] takes a cpuset.CPUSet over as a [CpuSet] without copying it. At
// a hundred or so CPUs this is two orders of magnitude cheaper than listing
// and rebuilding. Use it for sets from upstream interfaces. Going back out is
// free: the embedded field of a [CpuSet] is the cpuset.CPUSet itself.
//
// # Nil sets
//
// A nil *CpuMask or *CpuSet reads as the empty set, as receiver or operand, in
// every operation which does not modify it. So a missing map entry or an
// unassigned field needs no guarding:
//
//	free := byNode[id]              // nothing there
//	fmt.Println(free.Size())        // 0
//	fmt.Println(all.Difference(free)) // all of them
//
// Set, Clear and Seal panic on a nil set, because no method can store a new
// set into the caller's variable. Assign one first:
//
//	cpus = cpus.EmptyIfNil()  // nothing becomes an empty set, the rest is kept
//	cpus = cpus.Clone()       // always your own set, unsealed, and a copy
//
// EmptyIfNil costs nothing, but a sealed set comes back sealed and Set still
// panics. Use it for a set you own and know is unsealed. Clone copies. Use it
// for a set of unknown provenance, such as the sealed sets the hardware
// package hands out, and before modifying a set you were given.
//
// # Concurrency
//
// Nothing here is safe for concurrent use without external synchronisation.
// Even reads are not: String, Key and Size fill their caches on first use.
//
// [CPUSet.Seal] makes a set immutable and fills every cache up front. A sealed
// set can be read from any number of goroutines:
//
//	cpus := libcpu.NewCpuMask(ids...)
//	cpus.Seal()
//	// ...safe to hand out now
//
// Clone returns an unsealed copy, which is not safe to share until sealed.
//
// # Gotchas
//
// Keys are per implementation. CpuMask keys on its hex mask words and CpuSet
// on the cpuset string, so the set {0,5} keys as "21" and as "0,5". Never mix
// implementations in one keyed map. For one key for both, take the CpuMask
// form:
//
//	func key(s libcpu.CPUSet) string {
//		if m, ok := s.(*libcpu.CpuMask); ok {
//			return m.Key()	// already the right form, and cached
//		}
//		return libcpu.NewCpuMask(s.UnsortedList()...).Key()
//	}
//
// It is deterministic: NewCpuMask builds the same mask from the same CPUs in
// any order. It is not cached, and for a thousand CPUs it costs some tens of
// microseconds and a few dozen allocations. Key it once and keep the string.
//
// A CpuMask costs memory in proportion to its highest CPU id. One holding only
// CPU 1023 takes 16 words, as much as one holding all of 0-1023. For sparse
// sets with high ids CpuSet may be cheaper.
//
// Mixing implementations is correct but slow. Every binary operation has a
// fast path for its own type. The fallback crosses the interface once per
// CPU. Use one implementation throughout a data structure.
//
// CPU ids must not be negative. A negative id is not rejected, it aliases:
// NewCpuMask(-1) yields the set {63}. Others, Set(-64) among them, panic. The
// parsers do reject negative input.
//
// [CPUSet.UnsortedList] has no defined order, even if CpuMask happens to
// sort. Use [CPUSet.List] when order matters, or [CPUSet.ForEachCpu] to walk
// a CpuMask without allocating.
//
// [CPUSet.Contains] means "all of", so Contains() with no arguments is true.
// Equals depends on that.
//
// [ParseCpuMask] and [ParseCpuSet] take the Linux cpuset list format ("0-3,8")
// and accept the same input. Both inherit the leniency of strconv.Atoi: "+1"
// parses as 1 and "00" as 0. Neither accepts surrounding whitespace.
package libcpu
