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

// Benchmarks of [CpuMask] and [CpuSet] against each other and against the
// raw k8s.io/utils/cpuset.CPUSet, over several set sizes and densities.
// Names are <operation>/<scenario>/<implementation>, so
//
//	go test -bench 'BenchmarkCPUSet/Contains'
//	go test -bench 'BenchmarkCPUSet/.*/1024cpus'
//	go test -bench 'BenchmarkCPUSet/.*/CpuMask'
//
// each pick out a slice of the matrix.
//
// New, Parse, Clone, Union, Intersection and Difference are called on the
// concrete type; see benchDirect. The rest go through the [CPUSet]
// interface, which adds the same constant overhead to every implementation.

// rawCpuSet exposes the raw k8s.io/utils/cpuset.CPUSet through the [CPUSet]
// interface, as a baseline for our types. It has no caching and no seal
// checks. Its methods assume any other set is a *rawCpuSet.
type rawCpuSet struct {
	cpuset.CPUSet
}

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

func (s *rawCpuSet) Intersects(other CPUSet) bool {
	return !s.CPUSet.Intersection(other.(*rawCpuSet).CPUSet).IsEmpty()
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
	return s.String()
}

func (s *rawCpuSet) Seal() {}

func (*rawCpuSet) IsDense() bool {
	return false
}

func (*rawCpuSet) IsSparse() bool {
	return true
}

func (s *rawCpuSet) ForEachCpu(f func(cpu int) bool) {
	for _, cpu := range s.UnsortedList() {
		if !f(cpu) {
			return
		}
	}
}

// benchDirect holds one case's operations which are called on the concrete
// type, bound to their data. Clone, Union, Intersection and Difference are
// not in CPUSet. These and New and Parse allocate their result, and wrapping
// a raw one in a [rawCpuSet] would allocate again. So the raw type is
// measured unwrapped, as code using k8s.io/utils/cpuset directly runs.
type benchDirect struct {
	newSet       func()
	parseSet     func() error
	clone        func()
	union        func()
	intersection func()
	difference   func()
}

// benchImpl is one implementation under test.
type benchImpl struct {
	name  string
	new   func(cpus ...int) CPUSet
	parse func(s string) (CPUSet, error)
	// direct binds the concrete-type operations to a case's inputs: cpus and
	// str for New and Parse, a and b for the rest.
	direct func(cpus []int, str string, a, b CPUSet) benchDirect
}

var benchImpls = []benchImpl{
	{
		name:  "CpuMask",
		new:   func(cpus ...int) CPUSet { return NewCpuMask(cpus...) },
		parse: func(s string) (CPUSet, error) { return ParseCpuMask(s) },
		direct: func(cpus []int, str string, a, b CPUSet) benchDirect {
			x, y := a.(*CpuMask), b.(*CpuMask)
			return benchDirect{
				newSet:       func() { sinkMask = NewCpuMask(cpus...) },
				parseSet:     func() (err error) { sinkMask, err = ParseCpuMask(str); return },
				clone:        func() { sinkMask = x.Clone() },
				union:        func() { sinkMask = x.Union(y) },
				intersection: func() { sinkMask = x.Intersection(y) },
				difference:   func() { sinkMask = x.Difference(y) },
			}
		},
	},
	{
		name:  "CpuSet",
		new:   func(cpus ...int) CPUSet { return NewCpuSet(cpus...) },
		parse: func(s string) (CPUSet, error) { return ParseCpuSet(s) },
		direct: func(cpus []int, str string, a, b CPUSet) benchDirect {
			x, y := a.(*CpuSet), b.(*CpuSet)
			return benchDirect{
				newSet:       func() { sinkSet = NewCpuSet(cpus...) },
				parseSet:     func() (err error) { sinkSet, err = ParseCpuSet(str); return },
				clone:        func() { sinkSet = x.Clone() },
				union:        func() { sinkSet = x.Union(y) },
				intersection: func() { sinkSet = x.Intersection(y) },
				difference:   func() { sinkSet = x.Difference(y) },
			}
		},
	},
	{
		// The raw k8s type, the baseline for our two implementations.
		name:  "cpuset.CPUSet",
		new:   newRawCpuSet,
		parse: parseRawCpuSet,
		direct: func(cpus []int, str string, a, b CPUSet) benchDirect {
			x, y := a.(*rawCpuSet).CPUSet, b.(*rawCpuSet).CPUSet
			return benchDirect{
				newSet:       func() { sinkRaw = cpuset.New(cpus...) },
				parseSet:     func() (err error) { sinkRaw, err = cpuset.Parse(str); return },
				clone:        func() { sinkRaw = x.Clone() },
				union:        func() { sinkRaw = x.Union(y) },
				intersection: func() { sinkRaw = x.Intersection(y) },
				difference:   func() { sinkRaw = x.Difference(y) },
			}
		},
	},
}

// benchScenario is a set of count CPUs, stride apart. The sparse
// implementations depend on count only, the dense one on count*stride.
type benchScenario struct {
	name   string
	count  int
	stride int
	// otherCount is the size of the second operand; zero means count.
	// Asymmetric sizes target binary operations which iterate the smaller
	// operand.
	otherCount int
}

var benchScenarios = []benchScenario{
	{name: "1cpu", count: 1, stride: 1},
	{name: "8cpus", count: 8, stride: 1},
	{name: "8cpus-spread", count: 8, stride: 128},
	{name: "64cpus", count: 64, stride: 1},
	{name: "64cpus-spread", count: 64, stride: 16},
	{name: "256cpus", count: 256, stride: 1},
	{name: "256cpus-spread", count: 256, stride: 4},
	{name: "1024cpus", count: 1024, stride: 1},
	// asymmetric: large against small, and the other way round
	{name: "1024cpus-vs-8", count: 1024, stride: 1, otherCount: 8},
	{name: "8cpus-vs-1024", count: 8, stride: 1, otherCount: 1024},
}

// asymmetric reports whether the scenario's two operands differ in size.
func (sc benchScenario) asymmetric() bool {
	return sc.otherCount != 0 && sc.otherCount != sc.count
}

// others is the number of CPUs in the second operand.
func (sc benchScenario) others() int {
	if sc.otherCount == 0 {
		return sc.count
	}
	return sc.otherCount
}

// cpus returns the CPUs of the scenario.
func (sc benchScenario) cpus() []int {
	return strided(0, sc.count, sc.stride)
}

// otherCpus returns the second operand for the set operations: a set of
// the same shape, overlapping cpus() by half.
func (sc benchScenario) otherCpus() []int {
	return strided(max(1, sc.count/2)*sc.stride, sc.others(), sc.stride)
}

// overlappingCpus returns a set of the same shape which shares at least one
// CPU with cpus(). It differs from otherCpus() only for a single CPU, where
// otherCpus() does not overlap.
func (sc benchScenario) overlappingCpus() []int {
	return strided(sc.count/2*sc.stride, sc.others(), sc.stride)
}

// disjointCpus returns a set of the same shape which starts past cpus() and
// shares no CPU with it. It forces a full scan in operations which stop at
// the first CPU in common.
func (sc benchScenario) disjointCpus() []int {
	return strided(sc.count*sc.stride, sc.others(), sc.stride)
}

// strided returns count CPUs starting at first, stride apart.
func strided(first, count, stride int) []int {
	cpus := make([]int, count)
	for i := range cpus {
		cpus[i] = first + i*stride
	}
	return cpus
}

// benchCase is an implementation and a scenario pre-built with it.
type benchCase struct {
	impl   benchImpl
	cpus   []int       // CPUs in set a
	str    string      // string representation of set a
	a, b   CPUSet      // two sets of the same shape, overlapping by half
	direct benchDirect // operations measured on the concrete type
	o      CPUSet      // a set of the same shape overlapping a
	d      CPUSet      // a set of the same shape sharing no CPU with a
	hi     int         // highest CPU in a, always present in it
	absnt  int         // lowest CPU not in a
}

func newBenchCase(impl benchImpl, sc benchScenario) *benchCase {
	cpus := sc.cpus()
	a := impl.new(cpus...)
	b := impl.new(sc.otherCpus()...)

	absent := 0
	for a.Contains(absent) {
		absent++
	}

	return &benchCase{
		impl:   impl,
		cpus:   cpus,
		str:    a.String(),
		a:      a,
		b:      b,
		o:      impl.new(sc.overlappingCpus()...),
		d:      impl.new(sc.disjointCpus()...),
		hi:     cpus[len(cpus)-1],
		absnt:  absent,
		direct: impl.direct(cpus, a.String(), a, b),
	}
}

// Sinks for unused results. Without them the compiler may elide a result's
// allocation for some implementations only. There is one sink per concrete
// type, so nothing is boxed into an interface.
var (
	sinkStr  string
	sinkMask *CpuMask
	sinkSet  *CpuSet
	sinkRaw  cpuset.CPUSet
)

// benchOps are the measured operations. None of them changes its sets.
var benchOps = []struct {
	name string
	run  func(b *testing.B, c *benchCase)
}{
	{"New", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.direct.newSet()
		}
	}},
	{"Parse", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			if err := c.direct.parseSet(); err != nil {
				b.Fatal(err)
			}
		}
	}},
	{"Clone", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.direct.clone()
		}
	}},
	// Set adds a CPU already in the set, Clear removes one not in it.
	// For a dense scenario the cleared CPU lies past a CpuMask's last
	// word, which CpuMask rejects with a bounds check alone.
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
			c.direct.union()
		}
	}},
	{"Intersection", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.direct.intersection()
		}
	}},
	// Intersects stops at the first CPU in common. Intersects-hit measures
	// that early exit, Intersects-miss the full scan. The miss is comparable
	// to Intersection.
	{"Intersects-hit", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Intersects(c.o)
		}
	}},
	{"Intersects-miss", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.a.Intersects(c.d)
		}
	}},
	{"Difference", func(b *testing.B, c *benchCase) {
		for b.Loop() {
			c.direct.difference()
		}
	}},
	// Equals and IsSubsetOf get a set equal to a: no early exit.
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
	// String and Key are repeated on an unmodified set, so our caches serve
	// them. The raw cpuset.CPUSet caches neither.
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
	// String-uncached builds a fresh set per iteration to measure the cold
	// path. Subtract the New row for the cost of the string alone.
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

// takesSecondOperand reports whether op is measured against one of the
// scenario's second operands. Equals and IsSubsetOf build their own.
func takesSecondOperand(op string) bool {
	switch op {
	case "Union", "Intersection", "Difference", "Intersects-hit", "Intersects-miss":
		return true
	}
	return false
}

// skip reports whether to leave out op for sc. An asymmetric scenario adds
// nothing for an operation without a second operand.
func skip(op string, sc benchScenario) bool {
	return sc.asymmetric() && !takesSecondOperand(op)
}

func BenchmarkCPUSet(b *testing.B) {
	for _, op := range benchOps {
		b.Run(op.name, func(b *testing.B) {
			for _, sc := range benchScenarios {
				if skip(op.name, sc) {
					continue
				}
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

// TestBenchOperands checks the operand relationships the benchmark cases
// rely on. A wrong one fails no benchmark; it silently measures something
// other than the row's name.
func TestBenchOperands(t *testing.T) {
	for _, sc := range benchScenarios {
		t.Run(sc.name, func(t *testing.T) {
			var (
				a = NewCpuMask(sc.cpus()...)
				o = NewCpuMask(sc.overlappingCpus()...)
				d = NewCpuMask(sc.disjointCpus()...)
			)

			if got := a.Size(); got != sc.count {
				t.Errorf("cpus() has %d CPUs, want %d", got, sc.count)
			}
			if !a.Intersects(o) {
				t.Errorf("overlappingCpus() %s does not intersect cpus() %s", o, a)
			}
			if a.Intersects(d) {
				t.Errorf("disjointCpus() %s intersects cpus() %s", d, a)
			}
			if o.Size() != sc.others() || d.Size() != sc.others() {
				t.Errorf("second operands have %d and %d CPUs, want %d each",
					o.Size(), d.Size(), sc.others())
			}
			if b := NewCpuMask(sc.otherCpus()...); b.Size() != sc.others() {
				t.Errorf("otherCpus() has %d CPUs, want %d", b.Size(), sc.others())
			}

			// Contains, Set and Clear need hi in a and absnt not.
			for _, impl := range benchImpls {
				c := newBenchCase(impl, sc)
				if !c.a.Contains(c.hi) {
					t.Errorf("%s: a does not contain hi=%d", impl.name, c.hi)
				}
				if c.a.Contains(c.absnt) {
					t.Errorf("%s: a contains absnt=%d", impl.name, c.absnt)
				}
			}
		})
	}
}

// TestCompareImplementations runs the benchmark matrix and prints ns/op per
// implementation, and the fastest one with its lead over the runner-up. It
// is opt-in:
//
//	CPUSET_BENCH_COMPARE=1 go test -run TestCompareImplementations -v
//
// Runs are short by default: enough to rank, not to trust the numbers. Pass
// an explicit -benchtime for an accurate, much slower table.
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
	defer w.Flush() // nolint:errcheck

	fmt.Fprint(w, "operation\tCPUs\t") // nolint:errcheck
	for _, impl := range benchImpls {
		fmt.Fprintf(w, "%s\t", impl.name) // nolint:errcheck
	}
	fmt.Fprint(w, "fastest (vs 2nd)\t\n") // nolint:errcheck

	for _, op := range benchOps {
		for _, sc := range benchScenarios {
			if skip(op.name, sc) {
				continue
			}

			var (
				best   = benchImpl{}
				bestNs = 0.0
				next   = 0.0
			)

			fmt.Fprintf(w, "%s\t%s\t", op.name, sc.name) // nolint:errcheck

			for _, impl := range benchImpls {
				c := newBenchCase(impl, sc)
				r := testing.Benchmark(func(b *testing.B) { op.run(b, c) })
				ns := float64(r.T.Nanoseconds()) / float64(r.N)

				fmt.Fprintf(w, "%.1f\t", ns) // nolint:errcheck

				switch {
				case bestNs == 0 || ns < bestNs:
					best, bestNs, next = impl, ns, bestNs
				case next == 0 || ns < next:
					next = ns
				}
			}

			fmt.Fprintf(w, "%s (%.1fx)\t\n", best.name, next/bestNs) // nolint:errcheck
		}
		fmt.Fprint(w, "\t\t\t\t\t\t\n") // nolint:errcheck
	}
}
