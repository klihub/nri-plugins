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
	"flag"
	"fmt"
	"os"
	"testing"
	"text/tabwriter"

	"k8s.io/utils/cpuset"
)

// This file benchmarks our dense [CpuMask] and sparse [CpuSet] against each
// other and against the raw k8s.io/utils/cpuset.CPUSet which CpuSet wraps.
// Every operation is measured for several CPU set sizes and densities, since
// both the number of CPUs in the set and the highest CPU number in it affect
// the implementations differently.
//
// Benchmarks are named <operation>/<scenario>/<implementation>, so
//
//	go test -bench 'BenchmarkCPUSet/Contains'
//	go test -bench 'BenchmarkCPUSet/.*/1024cpus'
//	go test -bench 'BenchmarkCPUSet/.*/CpuMask'
//
// all pick out a useful slice of the matrix. For a single table which shows
// the fastest implementation per operation and scenario, run
//
//	CPUSET_BENCH_COMPARE=1 go test -run TestCompareImplementations -v
//
// Note that all implementations are driven through the [CPUSet] interface.
// This adds the same non-inlinable indirection to each of them, which is what
// callers of this package pay anyway, but it does mean that the very cheapest
// operations are measured with a constant overhead included.

// rawCpuSet exposes the raw k8s.io/utils/cpuset.CPUSet through the [CPUSet]
// interface so that it can be benchmarked side by side with our own types. It
// is deliberately as thin as possible: no string or key caching, no seal
// checks. Comparing it against [CpuSet] therefore shows what our wrapper adds
// on top of it, and comparing it against [CpuMask] shows the cost of the
// sparse representation itself.
//
// The embedded cpuset.CPUSet provides Size, IsEmpty, List, UnsortedList and
// String as is. The rest need adapting, mostly because they take or return
// our CPUSet instead of a cpuset.CPUSet. Those methods assume the other set
// is a *rawCpuSet, which is all the benchmarks below ever pass them.
type rawCpuSet struct {
	cpuset.CPUSet
}

// rawCpuSet should implement CPUSet.
var _ CPUSet = (*rawCpuSet)(nil)

func newRawCpuSet(cpus ...int) CPUSet {
	return &rawCpuSet{CPUSet: cpuset.New(cpus...)}
}

func parseRawCpuSet(s string) (CPUSet, error) {
	cpus, err := cpuset.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseFailed, err)
	}
	return &rawCpuSet{CPUSet: cpus}, nil
}

func (s *rawCpuSet) Clone() CPUSet {
	return &rawCpuSet{CPUSet: s.CPUSet.Clone()}
}

func (s *rawCpuSet) Set(cpus ...int) {
	s.CPUSet = s.CPUSet.Union(cpuset.New(cpus...))
}

func (s *rawCpuSet) Clear(cpus ...int) {
	s.CPUSet = s.CPUSet.Difference(cpuset.New(cpus...))
}

func (s *rawCpuSet) Difference(other CPUSet) CPUSet {
	return &rawCpuSet{CPUSet: s.CPUSet.Difference(other.(*rawCpuSet).CPUSet)}
}

func (s *rawCpuSet) Intersection(other CPUSet) CPUSet {
	return &rawCpuSet{CPUSet: s.CPUSet.Intersection(other.(*rawCpuSet).CPUSet)}
}

func (s *rawCpuSet) Union(others ...CPUSet) CPUSet {
	r := s.CPUSet
	for _, other := range others {
		r = r.Union(other.(*rawCpuSet).CPUSet)
	}
	return &rawCpuSet{CPUSet: r}
}

func (s *rawCpuSet) Contains(cpus ...int) bool {
	for _, cpu := range cpus {
		if !s.CPUSet.Contains(cpu) {
			return false
		}
	}
	return true
}

func (s *rawCpuSet) Equals(other CPUSet) bool {
	return s.CPUSet.Equals(other.(*rawCpuSet).CPUSet)
}

func (s *rawCpuSet) IsSubsetOf(other CPUSet) bool {
	return s.CPUSet.IsSubsetOf(other.(*rawCpuSet).CPUSet)
}

func (s *rawCpuSet) Key() string {
	return s.CPUSet.String()
}

func (s *rawCpuSet) Seal() {}

func (*rawCpuSet) IsDense() bool {
	return false
}

func (*rawCpuSet) IsSparse() bool {
	return true
}

func (s *rawCpuSet) ForEachCpu(f func(cpu int) bool) {
	for _, cpu := range s.CPUSet.UnsortedList() {
		if !f(cpu) {
			return
		}
	}
}

// benchImpl is one implementation under test.
type benchImpl struct {
	name  string
	new   func(cpus ...int) CPUSet
	parse func(s string) (CPUSet, error)
}

var benchImpls = []benchImpl{
	{
		name:  "CpuMask",
		new:   func(cpus ...int) CPUSet { return NewCpuMask(cpus...) },
		parse: func(s string) (CPUSet, error) { return ParseCpuMask(s) },
	},
	{
		name:  "CpuSet",
		new:   func(cpus ...int) CPUSet { return NewCpuSet(cpus...) },
		parse: func(s string) (CPUSet, error) { return ParseCpuSet(s) },
	},
	{
		name:  "cpuset.CPUSet",
		new:   newRawCpuSet,
		parse: parseRawCpuSet,
	},
}

// benchScenario describes a CPU set to benchmark with. count CPUs are picked
// stride apart, so count says how much data an operation has to chew through
// and stride how thinly it is spread: the sparse implementations care about
// count only, the dense one about count*stride, the highest CPU in the set.
type benchScenario struct {
	name   string
	count  int
	stride int
}

var benchScenarios = []benchScenario{
	{"1cpu", 1, 1},
	{"8cpus", 8, 1},
	{"8cpus-spread", 8, 128},
	{"64cpus", 64, 1},
	{"64cpus-spread", 64, 16},
	{"256cpus", 256, 1},
	{"256cpus-spread", 256, 4},
	{"1024cpus", 1024, 1},
}

// cpus returns the CPUs of the scenario.
func (sc benchScenario) cpus() []int {
	return strided(0, sc.count, sc.stride)
}

// otherCpus returns a second set of CPUs of the same shape, overlapping the
// first one by half. It is the second operand for the set operations.
func (sc benchScenario) otherCpus() []int {
	return strided(max(1, sc.count/2)*sc.stride, sc.count, sc.stride)
}

// strided returns count CPUs starting at first, stride apart.
func strided(first, count, stride int) []int {
	cpus := make([]int, count)
	for i := range cpus {
		cpus[i] = first + i*stride
	}
	return cpus
}

// benchCase is everything an operation needs to run: an implementation, and a
// scenario pre-built with it.
type benchCase struct {
	impl  benchImpl
	cpus  []int  // CPUs in set a
	str   string // string representation of set a
	a, b  CPUSet // two sets of the same shape, overlapping by half
	hi    int    // highest CPU in a, always present in it
	absnt int    // lowest CPU not in a
}

func newBenchCase(impl benchImpl, sc benchScenario) *benchCase {
	cpus := sc.cpus()
	a := impl.new(cpus...)

	absent := 0
	for a.Contains(absent) {
		absent++
	}

	return &benchCase{
		impl:  impl,
		cpus:  cpus,
		str:   a.String(),
		a:     a,
		b:     impl.new(sc.otherCpus()...),
		hi:    cpus[len(cpus)-1],
		absnt: absent,
	}
}

// sinkStr keeps go vet from complaining about unused String() results.
var sinkStr string

// benchOps are the operations we measure. Every one of them leaves its sets
// unchanged, so that repeated iterations all do the same amount of work.
var benchOps = []struct {
	name string
	run  func(b *testing.B, c *benchCase)
}{
	{"New", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.impl.new(c.cpus...)
		}
	}},
	{"Parse", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			if _, err := c.impl.parse(c.str); err != nil {
				b.Fatal(err)
			}
		}
	}},
	{"Clone", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Clone()
		}
	}},
	// Set adds a CPU which is already in the set, Clear removes one which is
	// not, both leaving the set as it was. Note that for a densely packed
	// scenario the cleared CPU falls beyond the last word of a CpuMask, which
	// CpuMask can reject with a bounds check alone.
	{"Set", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Set(c.hi)
		}
	}},
	{"Clear", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Clear(c.absnt)
		}
	}},
	{"Contains-hit", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Contains(c.hi)
		}
	}},
	{"Contains-miss", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Contains(c.absnt)
		}
	}},
	{"Size", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Size()
		}
	}},
	{"IsEmpty", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.IsEmpty()
		}
	}},
	{"Union", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Union(c.b)
		}
	}},
	{"Intersection", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Intersection(c.b)
		}
	}},
	{"Difference", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Difference(c.b)
		}
	}},
	// Equals and IsSubsetOf are given a set equal to a, the worst case for
	// both: they cannot bail out early.
	{"Equals", func(b *testing.B, c *benchCase) {
		o := c.impl.new(c.cpus...)
		for b.Loop() {
			c.a.Equals(o)
		}
	}},
	{"IsSubsetOf", func(b *testing.B, c *benchCase) {
		o := c.impl.new(c.cpus...)
		for b.Loop() {
			c.a.IsSubsetOf(o)
		}
	}},
	{"List", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.List()
		}
	}},
	// Note that these measure repeated calls on an unmodified set, which is
	// the case the string and key caches exist for. CpuSet caches String,
	// CpuMask caches Key, and the raw cpuset.CPUSet caches neither.
	{"String", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			sinkStr = c.a.String()
		}
	}},
	{"Key", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			sinkStr = c.a.Key()
		}
	}},
	// Both CpuMask and CpuSet cache String, so the op above only ever measures
	// a cache hit for them. This one builds a fresh set for every iteration to
	// measure the cold path too. Subtract the New row from it to get the cost
	// of generating the string itself.
	{"String-uncached", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			sinkStr = c.impl.new(c.cpus...).String()
		}
	}},
	{"ForEachCpu", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.ForEachCpu(func(int) bool { return true })
		}
	}},
}

func BenchmarkCPUSet(b *testing.B) {
	for _, op := range benchOps {
		b.Run(op.name, func(b *testing.B) {
			for _, sc := range benchScenarios {
				b.Run(sc.name, func(b *testing.B) {
					for _, impl := range benchImpls {
						b.Run(impl.name, func(b *testing.B) {
							op.run(b, newBenchCase(impl, sc))
						})
					}
				})
			}
		})
	}
}

// TestCompareImplementations runs the full benchmark matrix and prints a
// table of ns/op per implementation. The last column names the fastest one
// for each operation and scenario, and how much faster it is than the runner
// up. It is a test rather than a benchmark because it needs to compare
// results against each other.
//
// Because it runs every case, it is opt-in:
//
//	CPUSET_BENCH_COMPARE=1 go test -run TestCompareImplementations -v
//
// Each case gets a short run by default, enough to rank the implementations
// but not to trust the absolute numbers. Pass an explicit -benchtime for a
// more accurate, and much slower, table.
func TestCompareImplementations(t *testing.T) {
	if os.Getenv("CPUSET_BENCH_COMPARE") == "" {
		t.Skip("set CPUSET_BENCH_COMPARE=1 to run the implementation comparison")
	}

	if f := flag.Lookup("test.benchtime"); f != nil && f.Value.String() == "1s" {
		if err := f.Value.Set("20ms"); err != nil {
			t.Fatalf("failed to shorten benchmark time: %v", err)
		}
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	defer w.Flush()

	fmt.Fprint(w, "operation\tCPUs\t")
	for _, impl := range benchImpls {
		fmt.Fprintf(w, "%s\t", impl.name)
	}
	fmt.Fprint(w, "fastest (vs 2nd)\t\n")

	for _, op := range benchOps {
		for _, sc := range benchScenarios {
			var (
				best   = benchImpl{}
				bestNs = 0.0
				next   = 0.0
			)

			fmt.Fprintf(w, "%s\t%s\t", op.name, sc.name)

			for _, impl := range benchImpls {
				c := newBenchCase(impl, sc)
				r := testing.Benchmark(func(b *testing.B) { op.run(b, c) })
				ns := float64(r.T.Nanoseconds()) / float64(r.N)

				fmt.Fprintf(w, "%.1f\t", ns)

				switch {
				case bestNs == 0 || ns < bestNs:
					best, bestNs, next = impl, ns, bestNs
				case next == 0 || ns < next:
					next = ns
				}
			}

			fmt.Fprintf(w, "%s (%.1fx)\t\n", best.name, next/bestNs)
		}
		fmt.Fprint(w, "\t\t\t\t\t\t\n")
	}
}
