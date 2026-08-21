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
	"testing"
)

// testCPUSet wraps *CpuMask but presents a different concrete type so that
// type assertions to *CpuMask fail, exercising the non-fast-path fallback
// code in Difference, Intersection, Equals, IsSubsetOf, and Union.
type testCPUSet struct {
	*CpuMask
}

var _ CPUSet = (*testCPUSet)(nil)

func newTestCPUSet(cpus ...int) *testCPUSet {
	return &testCPUSet{CpuMask: NewCpuMask(cpus...)}
}

// cpuRange returns a sorted []int with all integers from lo to hi inclusive.
func cpuRange(lo, hi int) []int {
	s := make([]int, hi-lo+1)
	for i := range s {
		s[i] = lo + i
	}
	return s
}

// maskListEqual reports whether two CPUSets contain exactly the same CPUs by
// comparing their sorted lists, avoiding any dependence on Equals itself.
func maskListEqual(a, b CPUSet) bool {
	return slices.Equal(a.List(), b.List())
}

// ---- TestNewCpuMask -------------------------------------------------------

func TestNewCpuMask(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected []int
	}{
		{
			name:     "empty",
			cpus:     []int{},
			expected: []int{},
		},
		{
			name:     "cpu-0-lowest-bit-word-0",
			cpus:     []int{0},
			expected: []int{0},
		},
		{
			name:     "cpu-63-highest-bit-word-0",
			cpus:     []int{63},
			expected: []int{63},
		},
		{
			name:     "cpu-64-lowest-bit-word-1",
			cpus:     []int{64},
			expected: []int{64},
		},
		{
			name:     "cpu-127-highest-bit-word-1",
			cpus:     []int{127},
			expected: []int{127},
		},
		{
			name:     "word-0-all-bits-set",
			cpus:     cpuRange(0, 63),
			expected: cpuRange(0, 63),
		},
		{
			name:     "two-words-all-bits-set",
			cpus:     cpuRange(0, 127),
			expected: cpuRange(0, 127),
		},
		{
			name:     "lowest-and-highest-in-word-0",
			cpus:     []int{0, 63},
			expected: []int{0, 63},
		},
		{
			name:     "straddles-word-boundary-63-and-64",
			cpus:     []int{63, 64},
			expected: []int{63, 64},
		},
		{
			name:     "cpu-1023-highest-bit-word-15",
			cpus:     []int{1023},
			expected: []int{1023},
		},
		{
			name:     "cpu-1024-lowest-bit-word-16",
			cpus:     []int{1024},
			expected: []int{1024},
		},
		{
			name:     "lowest-and-highest-across-16-words",
			cpus:     []int{0, 1023},
			expected: []int{0, 1023},
		},
		{
			name:     "sparse-word-boundary-cpus",
			cpus:     []int{0, 64, 128, 512, 960, 1023},
			expected: []int{0, 64, 128, 512, 960, 1023},
		},
		{
			name:     "large-contiguous-range-512-to-1023",
			cpus:     cpuRange(512, 1023),
			expected: cpuRange(512, 1023),
		},
		{
			name:     "duplicate-cpus-are-deduped",
			cpus:     []int{0, 0, 63, 63, 64, 64},
			expected: []int{0, 63, 64},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCpuMask(tc.cpus...)
			got := m.List()
			if !slices.Equal(got, tc.expected) {
				t.Errorf("List() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// ---- TestSetAndClear -------------------------------------------------------

func TestSetAndClear(t *testing.T) {
	type step struct {
		set   []int
		clear []int
	}
	tests := []struct {
		name     string
		initial  []int
		steps    []step
		expected []int
	}{
		{
			name:     "set-cpu-0-on-empty",
			steps:    []step{{set: []int{0}}},
			expected: []int{0},
		},
		{
			name:     "set-cpu-63-highest-bit-word-0",
			steps:    []step{{set: []int{63}}},
			expected: []int{63},
		},
		{
			name:     "set-cpu-64-first-bit-word-1",
			steps:    []step{{set: []int{64}}},
			expected: []int{64},
		},
		{
			name:     "set-cpu-1023",
			steps:    []step{{set: []int{1023}}},
			expected: []int{1023},
		},
		{
			name:     "set-cpu-1024",
			steps:    []step{{set: []int{1024}}},
			expected: []int{1024},
		},
		{
			name:     "set-is-idempotent",
			initial:  []int{0},
			steps:    []step{{set: []int{0}}},
			expected: []int{0},
		},
		{
			name:     "set-multiple-across-word-boundaries",
			steps:    []step{{set: []int{0, 63, 64, 1023}}},
			expected: []int{0, 63, 64, 1023},
		},
		{
			name:     "clear-middle-cpu",
			initial:  []int{0, 1, 2},
			steps:    []step{{clear: []int{1}}},
			expected: []int{0, 2},
		},
		{
			name:     "clear-highest-bit-in-word-0",
			initial:  cpuRange(0, 63),
			steps:    []step{{clear: []int{63}}},
			expected: cpuRange(0, 62),
		},
		{
			name:     "clear-lowest-bit-in-word-1",
			initial:  []int{0, 64},
			steps:    []step{{clear: []int{64}}},
			expected: []int{0},
		},
		{
			name:     "clear-cpu-1023",
			initial:  []int{0, 1023},
			steps:    []step{{clear: []int{1023}}},
			expected: []int{0},
		},
		{
			name:     "clear-nonexistent-is-noop",
			initial:  []int{0, 2},
			steps:    []step{{clear: []int{1}}},
			expected: []int{0, 2},
		},
		{
			name:    "clear-out-of-range-is-noop",
			initial: []int{0}, // mask covers only word 0
			// CPU 128 is word 2, well past the end — must not panic.
			steps:    []step{{clear: []int{128}}},
			expected: []int{0},
		},
		{
			name:    "clear-exactly-at-mask-length-boundary",
			initial: []int{0, 64}, // mask has 2 words (indices 0 and 1)
			// CPU 128 → word 2 == len(mask); must be a no-op, not a panic.
			steps:    []step{{clear: []int{128}}},
			expected: []int{0, 64},
		},
		{
			name:     "set-then-clear",
			steps:    []step{{set: []int{0, 1, 2}}, {clear: []int{1}}},
			expected: []int{0, 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCpuMask(tc.initial...)
			for _, s := range tc.steps {
				for _, cpu := range s.set {
					m.Set(cpu)
				}
				for _, cpu := range s.clear {
					m.Clear(cpu)
				}
			}
			if got := m.List(); !slices.Equal(got, tc.expected) {
				t.Errorf("List() = %v, want %v", got, tc.expected)
			}
		})
	}

	t.Run("string-cache-cleared-after-set", func(t *testing.T) {
		m := NewCpuMask(0)
		_ = m.String()
		m.Set(1)
		if got := m.String(); got != "0-1" {
			t.Errorf("string cache not invalidated after Set: got %q, want %q", got, "0-1")
		}
	})

	t.Run("string-cache-cleared-after-clear", func(t *testing.T) {
		m := NewCpuMask(0, 1)
		_ = m.String()
		m.Clear(1)
		if got := m.String(); got != "0" {
			t.Errorf("string cache not invalidated after Clear: got %q, want %q", got, "0")
		}
	})

	// ---- variadic multi-arg Set / Clear calls --------------------------------

	t.Run("set-no-args-is-noop", func(t *testing.T) {
		m := NewCpuMask(5)
		m.Set()
		if got := m.List(); !slices.Equal(got, []int{5}) {
			t.Errorf("Set() no-op: got %v, want [5]", got)
		}
	})

	t.Run("clear-no-args-is-noop", func(t *testing.T) {
		m := NewCpuMask(5)
		m.Clear()
		if got := m.List(); !slices.Equal(got, []int{5}) {
			t.Errorf("Clear() no-op: got %v, want [5]", got)
		}
	})

	t.Run("set-multiple-same-word", func(t *testing.T) {
		m := NewCpuMask()
		m.Set(0, 1, 2, 3, 63)
		want := []int{0, 1, 2, 3, 63}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("set-multiple-across-word-boundaries", func(t *testing.T) {
		m := NewCpuMask()
		m.Set(0, 63, 64, 127, 128, 1023)
		want := []int{0, 63, 64, 127, 128, 1023}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("set-multiple-idempotent", func(t *testing.T) {
		m := NewCpuMask(0, 63, 64)
		m.Set(0, 63, 64, 64, 63, 0) // duplicates must not double-set
		want := []int{0, 63, 64}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("set-multiple-large-range", func(t *testing.T) {
		m := NewCpuMask()
		args := cpuRange(512, 575) // 64 CPUs filling exactly word 8
		m.Set(args...)
		if got := m.List(); !slices.Equal(got, args) {
			t.Errorf("got %v, want %v", got, args)
		}
	})

	t.Run("clear-multiple-same-word", func(t *testing.T) {
		m := NewCpuMask(0, 1, 2, 3, 63)
		m.Clear(1, 2, 63)
		want := []int{0, 3}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("clear-multiple-across-word-boundaries", func(t *testing.T) {
		m := NewCpuMask(0, 63, 64, 127, 128, 1023)
		m.Clear(63, 64, 1023)
		want := []int{0, 127, 128}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("clear-multiple-some-absent", func(t *testing.T) {
		m := NewCpuMask(0, 2, 4)
		m.Clear(1, 2, 3) // 1 and 3 are not set — should be no-op for those
		want := []int{0, 4}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("clear-multiple-out-of-range-mixed-with-valid", func(t *testing.T) {
		m := NewCpuMask(0, 64)
		// 512 is beyond the mask; clearing it must not panic and must be a no-op.
		m.Clear(64, 512)
		want := []int{0}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("clear-all-via-variadic", func(t *testing.T) {
		cpus := []int{0, 63, 64, 127, 128, 255, 1023}
		m := NewCpuMask(cpus...)
		m.Clear(cpus...)
		if !m.IsEmpty() {
			t.Errorf("expected empty after clearing all CPUs, got %v", m.List())
		}
	})

	t.Run("set-then-clear-multi-arg", func(t *testing.T) {
		m := NewCpuMask()
		m.Set(0, 63, 64, 127, 512, 1023)
		m.Clear(63, 127, 1023)
		want := []int{0, 64, 512}
		if got := m.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// ---- TestClone ------------------------------------------------------------

func TestClone(t *testing.T) {
	t.Run("clone-of-empty", func(t *testing.T) {
		m := NewCpuMask()
		c := m.Clone()
		if !c.IsEmpty() {
			t.Errorf("clone of empty mask is not empty: %v", c.List())
		}
	})

	t.Run("clone-matches-original", func(t *testing.T) {
		cpus := []int{0, 63, 64, 127, 512, 1023}
		m := NewCpuMask(cpus...)
		c := m.Clone()
		if !slices.Equal(c.List(), cpus) {
			t.Errorf("clone content mismatch: want %v, got %v", cpus, c.List())
		}
	})

	t.Run("clone-is-independent-mutation-does-not-affect-original", func(t *testing.T) {
		m := NewCpuMask(0, 1, 2)
		c := m.Clone().(*CpuMask)
		c.Set(63)
		if m.Contains(63) {
			t.Error("mutating clone propagated to original")
		}
		if !c.Contains(63) {
			t.Error("mutation of clone did not take effect")
		}
	})

	t.Run("clone-of-sealed-mask-is-not-sealed", func(t *testing.T) {
		m := NewCpuMask(0)
		m.Seal()
		c := m.Clone().(*CpuMask)
		c.Set(1) // must not panic
		if !c.Contains(1) {
			t.Error("clone of sealed mask should be mutable")
		}
	})

	t.Run("clone-of-full-word-mask", func(t *testing.T) {
		m := NewCpuMask(cpuRange(0, 63)...)
		c := m.Clone()
		if !slices.Equal(c.List(), cpuRange(0, 63)) {
			t.Errorf("full-word clone mismatch: got %v", c.List())
		}
	})

	t.Run("clone-of-multi-word-high-cpu-mask", func(t *testing.T) {
		cpus := cpuRange(960, 1023)
		m := NewCpuMask(cpus...)
		c := m.Clone()
		if !slices.Equal(c.List(), cpus) {
			t.Errorf("multi-word clone mismatch: got %v", c.List())
		}
	})
}

// ---- TestSeal -------------------------------------------------------------

func TestSeal(t *testing.T) {
	t.Run("set-on-sealed-mask-panics", func(t *testing.T) {
		m := NewCpuMask(0)
		m.Seal()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from Set on sealed mask, got none")
			}
		}()
		m.Set(1)
	})

	t.Run("clear-on-sealed-mask-panics", func(t *testing.T) {
		m := NewCpuMask(0, 1)
		m.Seal()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from Clear on sealed mask, got none")
			}
		}()
		m.Clear(0)
	})

	t.Run("panic-if-sealed-panics-when-sealed", func(t *testing.T) {
		m := NewCpuMask()
		m.Seal()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from panicIfSealed on sealed mask, got none")
			}
		}()
		m.panicIfSealed()
	})

	t.Run("panic-if-sealed-does-not-panic-when-unsealed", func(t *testing.T) {
		m := NewCpuMask()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("unexpected panic from panicIfSealed on unsealed mask: %v", r)
			}
		}()
		m.panicIfSealed()
	})

	t.Run("read-only-operations-work-on-sealed-mask", func(t *testing.T) {
		m := NewCpuMask(0, 63, 64, 1023)
		m.Seal()
		if !m.Contains(63) {
			t.Error("Contains should work on sealed mask")
		}
		if m.Size() != 4 {
			t.Errorf("Size should work on sealed mask: got %d, want 4", m.Size())
		}
		if m.IsEmpty() {
			t.Error("IsEmpty should work on sealed mask")
		}
		if !slices.Equal(m.List(), []int{0, 63, 64, 1023}) {
			t.Errorf("List should work on sealed mask: got %v", m.List())
		}
	})
}

// ---- TestIsDenseIsSparse ---------------------------------------------

func TestIsDenseIsSparse(t *testing.T) {
	t.Run("cpumask-is-dense-not-sparse", func(t *testing.T) {
		m := NewCpuMask(0, 1, 2)
		if !m.IsDense() {
			t.Error("CpuMask.IsDense() = false, want true")
		}
		if m.IsSparse() {
			t.Error("CpuMask.IsSparse() = true, want false")
		}
	})

	t.Run("cpuset-is-sparse-not-dense", func(t *testing.T) {
		s := NewCpuSet(0, 1, 2)
		if s.IsDense() {
			t.Error("CpuSet.IsDense() = true, want false")
		}
		if !s.IsSparse() {
			t.Error("CpuSet.IsSparse() = false, want true")
		}
	})
}

// ---- TestIsEmpty ------------------------------------------------------

func TestIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected bool
	}{
		{name: "empty", cpus: []int{}, expected: true},
		{name: "single-cpu-0", cpus: []int{0}, expected: false},
		{name: "single-cpu-63", cpus: []int{63}, expected: false},
		{name: "single-cpu-64", cpus: []int{64}, expected: false},
		{name: "word-0-all-bits-set", cpus: cpuRange(0, 63), expected: false},
		{name: "single-cpu-1023", cpus: []int{1023}, expected: false},
		{name: "multi-word-sparse", cpus: []int{0, 64, 512, 1023}, expected: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCpuMask(tc.cpus...)
			if got := m.IsEmpty(); got != tc.expected {
				t.Errorf("IsEmpty() = %v, want %v", got, tc.expected)
			}
		})
	}

	t.Run("empty-after-clearing-all-bits", func(t *testing.T) {
		m := NewCpuMask(0, 1, 63, 64, 1023)
		for _, cpu := range m.List() {
			m.Clear(cpu)
		}
		if !m.IsEmpty() {
			t.Errorf("mask should be empty after clearing all bits, got %v", m.List())
		}
	})

	t.Run("empty-with-trailing-zero-words", func(t *testing.T) {
		// Set then clear a high CPU to leave trailing zero words in the mask array.
		m := NewCpuMask(0, 1023)
		m.Clear(1023)
		m.Clear(0)
		if !m.IsEmpty() {
			t.Errorf("mask with only zero words should report empty, got %v", m.List())
		}
	})
}

// ---- TestSize ---------------------------------------------------------

func TestSize(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected int
	}{
		{name: "empty", cpus: []int{}, expected: 0},
		{name: "single-cpu-0", cpus: []int{0}, expected: 1},
		{name: "single-cpu-63-highest-word-0", cpus: []int{63}, expected: 1},
		{name: "single-cpu-64-lowest-word-1", cpus: []int{64}, expected: 1},
		{name: "single-cpu-127-highest-word-1", cpus: []int{127}, expected: 1},
		{name: "word-0-all-64-bits", cpus: cpuRange(0, 63), expected: 64},
		{name: "two-words-all-128-bits", cpus: cpuRange(0, 127), expected: 128},
		{name: "single-cpu-1023", cpus: []int{1023}, expected: 1},
		{name: "single-cpu-1024", cpus: []int{1024}, expected: 1},
		{name: "large-range-512-to-1023", cpus: cpuRange(512, 1023), expected: 512},
		{name: "sparse-boundary-cpus", cpus: []int{0, 63, 64, 127, 512, 1023}, expected: 6},
		{name: "all-cpus-0-to-1023", cpus: cpuRange(0, 1023), expected: 1024},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCpuMask(tc.cpus...)
			if got := m.Size(); got != tc.expected {
				t.Errorf("Size() = %d, want %d", got, tc.expected)
			}
		})
	}
}

// ---- TestContains -----------------------------------------------------

func TestContains(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		check    int
		expected bool
	}{
		{name: "empty-mask-check-cpu-0", cpus: []int{}, check: 0, expected: false},
		{name: "cpu-0-present", cpus: []int{0}, check: 0, expected: true},
		{name: "cpu-0-absent", cpus: []int{1}, check: 0, expected: false},
		{name: "cpu-63-present", cpus: []int{63}, check: 63, expected: true},
		{name: "cpu-63-absent", cpus: []int{62}, check: 63, expected: false},
		{name: "cpu-64-present", cpus: []int{64}, check: 64, expected: true},
		{name: "cpu-64-absent-only-63-set", cpus: []int{63}, check: 64, expected: false},
		{name: "cpu-63-absent-only-64-set", cpus: []int{64}, check: 63, expected: false},
		{name: "word-boundary-63-and-64-check-63", cpus: []int{63, 64}, check: 63, expected: true},
		{name: "word-boundary-63-and-64-check-64", cpus: []int{63, 64}, check: 64, expected: true},
		{name: "cpu-1023-present", cpus: []int{1023}, check: 1023, expected: true},
		{name: "cpu-1023-absent", cpus: []int{1022}, check: 1023, expected: false},
		{name: "check-beyond-mask-range", cpus: []int{0}, check: 1023, expected: false},
		{name: "middle-of-full-word", cpus: cpuRange(0, 63), check: 32, expected: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCpuMask(tc.cpus...)
			if got := m.Contains(tc.check); got != tc.expected {
				t.Errorf("Contains(%d) = %v, want %v", tc.check, got, tc.expected)
			}
		})
	}

	// ---- variadic multi-arg / zero-arg Contains calls ------------------------

	t.Run("contains-no-args-is-vacuously-true", func(t *testing.T) {
		m := NewCpuMask(0, 1, 2)
		if !m.Contains() {
			t.Error("Contains() with no args should be true")
		}
		if got := NewCpuMask().Contains(); !got {
			t.Error("Contains() on empty mask with no args should be true")
		}
	})

	t.Run("contains-multiple-all-present", func(t *testing.T) {
		m := NewCpuMask(0, 63, 64, 1023)
		if !m.Contains(0, 63, 64, 1023) {
			t.Error("Contains(0, 63, 64, 1023) = false, want true")
		}
	})

	t.Run("contains-multiple-one-missing", func(t *testing.T) {
		m := NewCpuMask(0, 63, 64)
		if m.Contains(0, 63, 64, 1023) {
			t.Error("Contains(0, 63, 64, 1023) = true, want false (1023 absent)")
		}
	})

	t.Run("contains-multiple-duplicates", func(t *testing.T) {
		m := NewCpuMask(0, 64)
		if !m.Contains(0, 0, 64, 64) {
			t.Error("Contains with duplicate CPUs = false, want true")
		}
	})
}

// ---- TestString -------------------------------------------------------

func TestString(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected string
	}{
		{name: "empty", cpus: []int{}, expected: ""},
		{name: "single-cpu-0", cpus: []int{0}, expected: "0"},
		{name: "single-cpu-63", cpus: []int{63}, expected: "63"},
		{name: "single-cpu-64", cpus: []int{64}, expected: "64"},
		{name: "single-cpu-1023", cpus: []int{1023}, expected: "1023"},
		{name: "single-cpu-1024", cpus: []int{1024}, expected: "1024"},
		{name: "full-word-0", cpus: cpuRange(0, 63), expected: "0-63"},
		{name: "straddles-word-boundary-63-64", cpus: []int{63, 64}, expected: "63-64"},
		{name: "two-full-words-0-to-127", cpus: cpuRange(0, 127), expected: "0-127"},
		{name: "lowest-and-highest-of-two-words", cpus: []int{0, 63, 64, 127}, expected: "0,63-64,127"},
		{name: "sparse-word-boundary-cpus", cpus: []int{0, 64, 128, 192}, expected: "0,64,128,192"},
		{name: "low-and-very-high", cpus: []int{0, 1023}, expected: "0,1023"},
		{name: "large-contiguous-range-512-to-1023", cpus: cpuRange(512, 1023), expected: "512-1023"},
		{name: "mixed-ranges-across-words", cpus: []int{0, 1, 2, 64, 65, 128}, expected: "0-2,64-65,128"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCpuMask(tc.cpus...)
			got := m.String()
			if got != tc.expected {
				t.Errorf("String() = %q, want %q", got, tc.expected)
			}
			// Second call must return the cached value unchanged.
			if got2 := m.String(); got2 != got {
				t.Errorf("cached String() = %q, first was %q", got2, got)
			}
		})
	}
}

// ---- TestListAndUnsortedList -----------------------------------------------

func TestListAndUnsortedList(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected []int
	}{
		{name: "empty", cpus: []int{}, expected: []int{}},
		{name: "single-cpu-0", cpus: []int{0}, expected: []int{0}},
		{name: "out-of-order-input-sorted-output", cpus: []int{3, 1, 2, 0}, expected: []int{0, 1, 2, 3}},
		{name: "multi-word-sparse", cpus: []int{0, 64, 127, 512, 1023}, expected: []int{0, 64, 127, 512, 1023}},
		{name: "full-word-0", cpus: cpuRange(0, 63), expected: cpuRange(0, 63)},
		{name: "large-range-960-to-1023", cpus: cpuRange(960, 1023), expected: cpuRange(960, 1023)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewCpuMask(tc.cpus...)
			list := m.List()
			unsorted := m.UnsortedList()
			if !slices.Equal(list, tc.expected) {
				t.Errorf("List() = %v, want %v", list, tc.expected)
			}
			if !slices.Equal(unsorted, list) {
				t.Errorf("UnsortedList() != List(): %v vs %v", unsorted, list)
			}
		})
	}
}

// ---- TestForEachCpu -------------------------------------------------------

func TestForEachCpu(t *testing.T) {
	t.Run("empty-mask-f-never-called", func(t *testing.T) {
		m := NewCpuMask()
		called := false
		m.ForEachCpu(func(_ int) bool {
			called = true
			return true
		})
		if called {
			t.Error("ForEachCpu called f on empty mask")
		}
	})

	t.Run("single-cpu", func(t *testing.T) {
		m := NewCpuMask(42)
		var visited []int
		m.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		if !slices.Equal(visited, []int{42}) {
			t.Errorf("expected [42], got %v", visited)
		}
	})

	t.Run("ascending-order", func(t *testing.T) {
		want := []int{0, 7, 63, 64, 127, 512, 1023}
		m := NewCpuMask(want...)
		var visited []int
		m.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		if !slices.Equal(visited, want) {
			t.Errorf("expected %v, got %v", want, visited)
		}
	})

	t.Run("full-word-0-visits-all-64", func(t *testing.T) {
		m := NewCpuMask(cpuRange(0, 63)...)
		var visited []int
		m.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		if !slices.Equal(visited, cpuRange(0, 63)) {
			t.Errorf("expected CPUs 0-63, got %v", visited)
		}
	})

	t.Run("sparse-bits-exercise-skip-paths", func(t *testing.T) {
		// CPUs spaced 16 bits apart exercise the 16-bit skip branch.
		want := []int{0, 16, 32, 48}
		m := NewCpuMask(want...)
		var visited []int
		m.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		if !slices.Equal(visited, want) {
			t.Errorf("expected %v, got %v", want, visited)
		}
	})

	t.Run("early-termination-stops-iteration", func(t *testing.T) {
		m := NewCpuMask(0, 63, 64, 1023)
		var visited []int
		m.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return len(visited) < 2 // stop after two CPUs
		})
		if !slices.Equal(visited, []int{0, 63}) {
			t.Errorf("expected [0, 63], got %v", visited)
		}
	})

	t.Run("high-cpu-numbers-960-to-1023", func(t *testing.T) {
		want := cpuRange(960, 1023)
		m := NewCpuMask(want...)
		var visited []int
		m.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		if !slices.Equal(visited, want) {
			t.Errorf("expected CPUs 960-1023, got %v", visited)
		}
	})

	t.Run("word-boundary-highest-bits", func(t *testing.T) {
		// Highest bit of each of the first four words.
		want := []int{63, 127, 191, 255}
		m := NewCpuMask(want...)
		var visited []int
		m.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		if !slices.Equal(visited, want) {
			t.Errorf("expected %v, got %v", want, visited)
		}
	})
}

// ---- TestDifference ---------------------------------------------------

func TestDifference(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected []int
	}{
		// *CpuMask fast path
		{
			name:     "mask: empty-minus-empty",
			a:        []int{},
			b:        NewCpuMask(),
			expected: []int{},
		},
		{
			name:     "mask: empty-minus-nonempty",
			a:        []int{},
			b:        NewCpuMask(0, 1, 2),
			expected: []int{},
		},
		{
			name:     "mask: nonempty-minus-empty",
			a:        []int{0, 1, 2},
			b:        NewCpuMask(),
			expected: []int{0, 1, 2},
		},
		{
			name:     "mask: a-minus-a-equals-empty",
			a:        []int{0, 1, 2},
			b:        NewCpuMask(0, 1, 2),
			expected: []int{},
		},
		{
			name:     "mask: disjoint-sets",
			a:        []int{0, 2, 4},
			b:        NewCpuMask(1, 3, 5),
			expected: []int{0, 2, 4},
		},
		{
			name:     "mask: superset-minus-subset",
			a:        []int{0, 1, 2, 3},
			b:        NewCpuMask(1, 2),
			expected: []int{0, 3},
		},
		{
			name:     "mask: subset-minus-superset-equals-empty",
			a:        []int{1, 2},
			b:        NewCpuMask(0, 1, 2, 3),
			expected: []int{},
		},
		{
			name:     "mask: a-longer-than-b-extra-a-words-kept",
			a:        []int{0, 64, 128, 1023},
			b:        NewCpuMask(64),
			expected: []int{0, 128, 1023},
		},
		{
			name:     "mask: b-longer-than-a-extra-b-words-ignored",
			a:        []int{0, 64},
			b:        NewCpuMask(64, 128, 1023),
			expected: []int{0},
		},
		{
			name:     "mask: full-word-0-minus-word-1-leaves-word-0-intact",
			a:        cpuRange(0, 63),
			b:        NewCpuMask(cpuRange(64, 127)...),
			expected: cpuRange(0, 63),
		},
		{
			name:     "mask: high-cpus-partial-difference",
			a:        cpuRange(960, 1023),
			b:        NewCpuMask(cpuRange(992, 1023)...),
			expected: cpuRange(960, 991),
		},
		// testCPUSet fallback path
		{
			name:     "non-mask: basic-difference",
			a:        []int{0, 1, 2, 3},
			b:        newTestCPUSet(1, 2),
			expected: []int{0, 3},
		},
		{
			name:     "non-mask: empty-a",
			a:        []int{},
			b:        newTestCPUSet(0, 1),
			expected: []int{},
		},
		{
			name:     "non-mask: empty-b",
			a:        []int{0, 1},
			b:        newTestCPUSet(),
			expected: []int{0, 1},
		},
		{
			name:     "non-mask: high-cpus",
			a:        cpuRange(512, 1023),
			b:        newTestCPUSet(cpuRange(768, 1023)...),
			expected: cpuRange(512, 767),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuMask(tc.a...)
			got := a.Difference(tc.b)
			exp := NewCpuMask(tc.expected...)
			if !maskListEqual(got, exp) {
				t.Errorf("Difference() = %v, want %v", got.List(), tc.expected)
			}
		})
	}
}

// ---- TestEquals -------------------------------------------------------

func TestEquals(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected bool
	}{
		// *CpuMask fast path
		{
			name:     "mask: both-empty",
			a:        []int{},
			b:        NewCpuMask(),
			expected: true,
		},
		{
			name:     "mask: same-single-cpu-0",
			a:        []int{0},
			b:        NewCpuMask(0),
			expected: true,
		},
		{
			name:     "mask: different-single-cpu",
			a:        []int{0},
			b:        NewCpuMask(1),
			expected: false,
		},
		{
			name:     "mask: one-empty-one-not",
			a:        []int{0},
			b:        NewCpuMask(),
			expected: false,
		},
		{
			name:     "mask: same-full-word-0",
			a:        cpuRange(0, 63),
			b:        NewCpuMask(cpuRange(0, 63)...),
			expected: true,
		},
		{
			name:     "mask: same-multi-word",
			a:        []int{0, 64, 128, 1023},
			b:        NewCpuMask(0, 64, 128, 1023),
			expected: true,
		},
		{
			name:     "mask: different-multi-word",
			a:        []int{0, 64},
			b:        NewCpuMask(0, 128),
			expected: false,
		},
		{
			name:     "mask: high-cpus-equal",
			a:        cpuRange(960, 1023),
			b:        NewCpuMask(cpuRange(960, 1023)...),
			expected: true,
		},
		{
			name:     "mask: high-cpus-differ-by-one",
			a:        cpuRange(960, 1022),
			b:        NewCpuMask(cpuRange(960, 1023)...),
			expected: false,
		},
		{
			// m (built from a) has fewer words than b; b's extra high word is
			// all zero, so the sets are still equal. Exercises the
			// `case c < len(other.mask)` branch in the fast path with a true
			// outcome.
			name: "mask: other-has-extra-all-zero-word-still-equal",
			a:    []int{0},
			b: func() CPUSet {
				o := NewCpuMask(0, 1024)
				o.Clear(1024)
				return o
			}(),
			expected: true,
		},
		{
			// Same as above, but b's extra high word has a bit set, so the
			// sets differ. Exercises the `case c < len(other.mask)` branch
			// with a false outcome.
			name:     "mask: other-has-extra-nonzero-word-not-equal",
			a:        []int{0},
			b:        NewCpuMask(0, 1024),
			expected: false,
		},
		// testCPUSet fallback path
		{
			name:     "non-mask: equal",
			a:        []int{0, 1, 2},
			b:        newTestCPUSet(0, 1, 2),
			expected: true,
		},
		{
			name:     "non-mask: not-equal-different-cpu",
			a:        []int{0, 1},
			b:        newTestCPUSet(0, 2),
			expected: false,
		},
		{
			name:     "non-mask: not-equal-different-size",
			a:        []int{0, 1, 2},
			b:        newTestCPUSet(0, 1),
			expected: false,
		},
		{
			name:     "non-mask: both-empty",
			a:        []int{},
			b:        newTestCPUSet(),
			expected: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuMask(tc.a...)
			if got := a.Equals(tc.b); got != tc.expected {
				t.Errorf("Equals() = %v, want %v", got, tc.expected)
			}
		})
	}

	t.Run("mask: trailing-zero-words-equal-shorter-mask", func(t *testing.T) {
		// Set CPU 64 then clear it, leaving a trailing zero word in the array.
		a := NewCpuMask(0, 64)
		a.Clear(64)        // a.mask = [0x1, 0x0]
		b := NewCpuMask(0) // b.mask = [0x1]
		if !a.Equals(b) {
			t.Error("mask with trailing zero word should equal shorter mask with same bits")
		}
		if !b.Equals(a) {
			t.Error("equality should be symmetric for trailing-zero case")
		}
	})
}

// ---- TestIntersection -------------------------------------------------

func TestIntersection(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected []int
	}{
		// *CpuMask fast path
		{
			name:     "mask: both-empty",
			a:        []int{},
			b:        NewCpuMask(),
			expected: []int{},
		},
		{
			name:     "mask: one-empty",
			a:        []int{0, 1, 2},
			b:        NewCpuMask(),
			expected: []int{},
		},
		{
			name:     "mask: no-overlap",
			a:        []int{0, 2},
			b:        NewCpuMask(1, 3),
			expected: []int{},
		},
		{
			name:     "mask: full-overlap-single-word",
			a:        cpuRange(0, 63),
			b:        NewCpuMask(cpuRange(0, 63)...),
			expected: cpuRange(0, 63),
		},
		{
			name:     "mask: partial-overlap-single-word",
			a:        []int{0, 1, 2},
			b:        NewCpuMask(1, 2, 3),
			expected: []int{1, 2},
		},
		{
			name:     "mask: a-shorter-than-b-extra-b-words-ignored",
			a:        []int{0},
			b:        NewCpuMask(0, 64),
			expected: []int{0},
		},
		{
			name:     "mask: b-shorter-than-a-extra-a-words-dropped",
			a:        []int{0, 64},
			b:        NewCpuMask(0),
			expected: []int{0},
		},
		{
			name:     "mask: multi-word-overlap",
			a:        []int{0, 64, 128, 512},
			b:        NewCpuMask(64, 128, 256, 512),
			expected: []int{64, 128, 512},
		},
		{
			name:     "mask: word-boundary-63-and-64",
			a:        []int{63, 64},
			b:        NewCpuMask(63, 64),
			expected: []int{63, 64},
		},
		{
			name:     "mask: high-cpus-partial-overlap",
			a:        cpuRange(512, 1023),
			b:        NewCpuMask(cpuRange(960, 1023)...),
			expected: cpuRange(960, 1023),
		},
		// testCPUSet fallback path
		{
			name:     "non-mask: partial-overlap",
			a:        []int{0, 1, 2},
			b:        newTestCPUSet(1, 2, 3),
			expected: []int{1, 2},
		},
		{
			name:     "non-mask: empty-a",
			a:        []int{},
			b:        newTestCPUSet(0, 1),
			expected: []int{},
		},
		{
			name:     "non-mask: empty-b",
			a:        []int{0, 1},
			b:        newTestCPUSet(),
			expected: []int{},
		},
		{
			name:     "non-mask: high-cpus",
			a:        cpuRange(512, 1023),
			b:        newTestCPUSet(cpuRange(960, 1023)...),
			expected: cpuRange(960, 1023),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuMask(tc.a...)
			got := a.Intersection(tc.b)
			exp := NewCpuMask(tc.expected...)
			if !maskListEqual(got, exp) {
				t.Errorf("Intersection() = %v, want %v", got.List(), tc.expected)
			}
		})
	}
}

// ---- TestIsSubsetOf ---------------------------------------------------

func TestIsSubsetOf(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected bool
	}{
		// *CpuMask fast path
		{
			name:     "mask: empty-is-subset-of-empty",
			a:        []int{},
			b:        NewCpuMask(),
			expected: true,
		},
		{
			name:     "mask: empty-is-subset-of-nonempty",
			a:        []int{},
			b:        NewCpuMask(0, 1),
			expected: true,
		},
		{
			name:     "mask: nonempty-is-not-subset-of-empty",
			a:        []int{0},
			b:        NewCpuMask(),
			expected: false,
		},
		{
			name:     "mask: set-is-subset-of-itself",
			a:        []int{0, 1, 2},
			b:        NewCpuMask(0, 1, 2),
			expected: true,
		},
		{
			name:     "mask: proper-subset",
			a:        []int{0, 1},
			b:        NewCpuMask(0, 1, 2),
			expected: true,
		},
		{
			name:     "mask: not-subset-has-extra-cpu",
			a:        []int{0, 3},
			b:        NewCpuMask(0, 1, 2),
			expected: false,
		},
		{
			name:     "mask: word-0-all-bits-subset-of-0-to-127",
			a:        cpuRange(0, 63),
			b:        NewCpuMask(cpuRange(0, 127)...),
			expected: true,
		},
		{
			name:     "mask: word-0-not-subset-of-word-1",
			a:        cpuRange(0, 63),
			b:        NewCpuMask(cpuRange(64, 127)...),
			expected: false,
		},
		{
			name:     "mask: a-longer-extra-nonzero-word-not-subset",
			a:        []int{0, 128},
			b:        NewCpuMask(0, 64),
			expected: false,
		},
		{
			name:     "mask: high-cpus-proper-subset",
			a:        cpuRange(992, 1023),
			b:        NewCpuMask(cpuRange(960, 1023)...),
			expected: true,
		},
		{
			name:     "mask: high-cpu-outside-superset-not-subset",
			a:        []int{0, 1023},
			b:        NewCpuMask(cpuRange(960, 1023)...),
			expected: false,
		},
		// testCPUSet fallback path
		{
			name:     "non-mask: proper-subset",
			a:        []int{0, 1},
			b:        newTestCPUSet(0, 1, 2),
			expected: true,
		},
		{
			name:     "non-mask: not-subset",
			a:        []int{0, 3},
			b:        newTestCPUSet(0, 1, 2),
			expected: false,
		},
		{
			name:     "non-mask: empty-is-subset",
			a:        []int{},
			b:        newTestCPUSet(0, 1),
			expected: true,
		},
		{
			name:     "non-mask: high-cpus-subset",
			a:        cpuRange(992, 1023),
			b:        newTestCPUSet(cpuRange(960, 1023)...),
			expected: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuMask(tc.a...)
			if got := a.IsSubsetOf(tc.b); got != tc.expected {
				t.Errorf("IsSubsetOf() = %v, want %v", got, tc.expected)
			}
		})
	}

	t.Run("mask: a-longer-with-trailing-zero-words-is-still-subset", func(t *testing.T) {
		// a has trailing zero words; it should still report as a subset.
		a := NewCpuMask(0, 64)
		a.Clear(64) // a.mask = [0x1, 0x0]
		b := NewCpuMask(0, 1)
		if !a.IsSubsetOf(b) {
			t.Error("mask with trailing zero words should still be recognised as a subset")
		}
	})
}

// ---- TestUnion --------------------------------------------------------

func TestUnion(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		others   []CPUSet
		expected []int
	}{
		// *CpuMask fast path
		{
			name:     "mask: empty-union-empty",
			a:        []int{},
			others:   []CPUSet{NewCpuMask()},
			expected: []int{},
		},
		{
			name:     "mask: a-union-empty-returns-a",
			a:        []int{0, 1},
			others:   []CPUSet{NewCpuMask()},
			expected: []int{0, 1},
		},
		{
			name:     "mask: empty-union-b-returns-b",
			a:        []int{},
			others:   []CPUSet{NewCpuMask(0, 1)},
			expected: []int{0, 1},
		},
		{
			name:     "mask: disjoint-single-word",
			a:        []int{0},
			others:   []CPUSet{NewCpuMask(1)},
			expected: []int{0, 1},
		},
		{
			name:     "mask: overlapping",
			a:        []int{0, 1},
			others:   []CPUSet{NewCpuMask(1, 2)},
			expected: []int{0, 1, 2},
		},
		{
			name:     "mask: a-shorter-than-b-includes-b-words",
			a:        []int{0},
			others:   []CPUSet{NewCpuMask(64, 128)},
			expected: []int{0, 64, 128},
		},
		{
			name:     "mask: a-longer-than-b-keeps-extra-a-words",
			a:        []int{0, 64, 128},
			others:   []CPUSet{NewCpuMask(0)},
			expected: []int{0, 64, 128},
		},
		{
			name:     "mask: word-boundary-63-and-64",
			a:        []int{63},
			others:   []CPUSet{NewCpuMask(64)},
			expected: []int{63, 64},
		},
		{
			name:     "mask: multiple-others",
			a:        []int{0},
			others:   []CPUSet{NewCpuMask(64), NewCpuMask(128)},
			expected: []int{0, 64, 128},
		},
		{
			name:     "mask: large-range-union",
			a:        cpuRange(512, 767),
			others:   []CPUSet{NewCpuMask(cpuRange(768, 1023)...)},
			expected: cpuRange(512, 1023),
		},
		// zero-argument union must return a copy of m
		{
			name:     "mask: no-others-returns-copy-of-a",
			a:        []int{0, 63, 64, 1023},
			others:   nil,
			expected: []int{0, 63, 64, 1023},
		},
		{
			name:     "mask: no-others-empty-a-returns-empty",
			a:        []int{},
			others:   nil,
			expected: []int{},
		},
		// testCPUSet fallback path — m's CPUs must be included
		{
			name:     "non-mask: a-union-b",
			a:        []int{0, 1},
			others:   []CPUSet{newTestCPUSet(2, 3)},
			expected: []int{0, 1, 2, 3},
		},
		{
			name:     "non-mask: overlapping",
			a:        []int{0, 1},
			others:   []CPUSet{newTestCPUSet(1, 2)},
			expected: []int{0, 1, 2},
		},
		{
			name:     "non-mask: empty-b-returns-a",
			a:        []int{0, 1},
			others:   []CPUSet{newTestCPUSet()},
			expected: []int{0, 1},
		},
		{
			name:     "non-mask: empty-a-union-b-returns-b",
			a:        []int{},
			others:   []CPUSet{newTestCPUSet(0, 1)},
			expected: []int{0, 1},
		},
		{
			name:     "non-mask: high-cpus",
			a:        []int{0},
			others:   []CPUSet{newTestCPUSet(1023)},
			expected: []int{0, 1023},
		},
		// mixed CpuMask and testCPUSet in others
		{
			name:     "mixed: cpumask-and-non-mask-others",
			a:        []int{0},
			others:   []CPUSet{NewCpuMask(64), newTestCPUSet(128)},
			expected: []int{0, 64, 128},
		},
		{
			name:     "mixed: non-mask-then-cpumask-others",
			a:        []int{0},
			others:   []CPUSet{newTestCPUSet(64), NewCpuMask(128)},
			expected: []int{0, 64, 128},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuMask(tc.a...)
			got := a.Union(tc.others...)
			exp := NewCpuMask(tc.expected...)
			if !maskListEqual(got, exp) {
				t.Errorf("Union() = %v, want %v", got.List(), tc.expected)
			}
		})
	}
}

// ---- TestParseCpuMask -------------------------------------------------

func TestParseCpuMask(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		err      bool
	}{
		{name: "single-high-cpu-1024", input: "1024", expected: "1024"},
		{name: "large-contiguous-range-512-to-1023", input: "512-1023", expected: "512-1023"},
		{name: "two-non-adjacent-ranges", input: "0-63,128-191", expected: "0-63,128-191"},
		{name: "word-boundary-range-63-to-64", input: "63-64", expected: "63-64"},
		{name: "degenerate-range-0-to-0", input: "0-0", expected: "0"},
		{name: "low-and-high-boundary", input: "0,1023", expected: "0,1023"},
		{name: "full-word-0", input: "0-63", expected: "0-63"},
		{name: "duplicate-cpus-deduped", input: "0,0,1,1,63,63", expected: "0-1,63"},
		{name: "unordered-cpus-sorted-in-output", input: "63,0,127,64", expected: "0,63-64,127"},
		{name: "all-cpus-0-to-1023", input: "0-1023", expected: "0-1023"},
		{name: "empty-string-returns-empty-mask", input: "", expected: ""},
		// error cases
		{name: "error-non-numeric", input: "abc", err: true},
		{name: "error-reversed-range", input: "5-3", err: true},
		{name: "error-bad-range-min", input: "a-3", err: true},
		{name: "error-bad-range-max", input: "3-a", err: true},
		{name: "error-empty-part-from-double-comma", input: "1,,2", err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := ParseCpuMask(tc.input)
			if tc.err {
				if err == nil {
					t.Errorf("ParseCpuMask(%q) expected error, got nil (mask=%v)", tc.input, m.List())
				}
				return
			}
			if err != nil {
				t.Errorf("ParseCpuMask(%q) unexpected error: %v", tc.input, err)
				return
			}
			if got := m.String(); got != tc.expected {
				t.Errorf("ParseCpuMask(%q).String() = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ===========================================================================
// CpuSet tests
//
// Binary-operation test cases are labelled "cpuset:" (fast path: both
// operands are *CpuSet, delegating to k8s cpuset methods) and "cpumask:"
// (fallback path: second operand is *CpuMask, exercising the iteration-based
// fallback in Difference, Intersection, Equals, IsSubsetOf, and Union).
// ===========================================================================

// ---- TestNewCpuSet --------------------------------------------------------

func TestNewCpuSet(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected []int
	}{
		{name: "empty", cpus: []int{}, expected: []int{}},
		{name: "single-cpu-0", cpus: []int{0}, expected: []int{0}},
		{name: "single-cpu-63", cpus: []int{63}, expected: []int{63}},
		{name: "single-cpu-64", cpus: []int{64}, expected: []int{64}},
		{name: "word-0-all-bits", cpus: cpuRange(0, 63), expected: cpuRange(0, 63)},
		{name: "straddles-word-boundary", cpus: []int{63, 64}, expected: []int{63, 64}},
		{name: "cpu-1023", cpus: []int{1023}, expected: []int{1023}},
		{name: "lowest-and-highest-multi-word", cpus: []int{0, 1023}, expected: []int{0, 1023}},
		{name: "sparse-word-boundaries", cpus: []int{0, 64, 128, 512, 1023}, expected: []int{0, 64, 128, 512, 1023}},
		{name: "large-contiguous-range", cpus: cpuRange(512, 1023), expected: cpuRange(512, 1023)},
		{name: "duplicate-cpus-deduped", cpus: []int{0, 0, 63, 63, 64, 64}, expected: []int{0, 63, 64}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCpuSet(tc.cpus...)
			if got := s.List(); !slices.Equal(got, tc.expected) {
				t.Errorf("List() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// ---- TestParseCpuSet -------------------------------------------------

func TestParseCpuSet(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		err      bool
	}{
		{name: "empty-string-returns-empty-set", input: "", expected: ""},
		{name: "single-cpu-0", input: "0", expected: "0"},
		{name: "range-and-single", input: "0-3,5", expected: "0-3,5"},
		{name: "word-boundary-range", input: "63-64", expected: "63-64"},
		{name: "high-cpu-1023", input: "1023", expected: "1023"},
		// error cases
		{name: "error-non-numeric", input: "abc", err: true},
		{name: "error-bad-range", input: "3-a", err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := ParseCpuSet(tc.input)
			if tc.err {
				if err == nil {
					t.Errorf("ParseCpuSet(%q) expected error, got nil (set=%v)", tc.input, s.List())
				}
				return
			}
			if err != nil {
				t.Errorf("ParseCpuSet(%q) unexpected error: %v", tc.input, err)
				return
			}
			if got := s.String(); got != tc.expected {
				t.Errorf("ParseCpuSet(%q).String() = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

// ---- TestCpuSetSetAndClear ------------------------------------------------

func TestCpuSetSetAndClear(t *testing.T) {
	type step struct {
		set   []int
		clear []int
	}
	tests := []struct {
		name     string
		initial  []int
		steps    []step
		expected []int
	}{
		{
			name:     "set-cpu-0-on-empty",
			steps:    []step{{set: []int{0}}},
			expected: []int{0},
		},
		{
			name:     "set-cpu-63",
			steps:    []step{{set: []int{63}}},
			expected: []int{63},
		},
		{
			name:     "set-cpu-64-crosses-word",
			steps:    []step{{set: []int{64}}},
			expected: []int{64},
		},
		{
			name:     "set-cpu-1023",
			steps:    []step{{set: []int{1023}}},
			expected: []int{1023},
		},
		{
			name:     "set-is-idempotent",
			initial:  []int{0},
			steps:    []step{{set: []int{0}}},
			expected: []int{0},
		},
		{
			name:     "set-multiple-cpus-in-one-call",
			steps:    []step{{set: []int{0, 63, 64, 1023}}},
			expected: []int{0, 63, 64, 1023},
		},
		{
			name:     "clear-middle-cpu",
			initial:  []int{0, 1, 2},
			steps:    []step{{clear: []int{1}}},
			expected: []int{0, 2},
		},
		{
			name:     "clear-multiple-cpus-in-one-call",
			initial:  []int{0, 1, 2, 3},
			steps:    []step{{clear: []int{1, 2}}},
			expected: []int{0, 3},
		},
		{
			name:     "clear-nonexistent-is-noop",
			initial:  []int{0, 2},
			steps:    []step{{clear: []int{1}}},
			expected: []int{0, 2},
		},
		{
			name:     "clear-out-of-range-is-noop",
			initial:  []int{0},
			steps:    []step{{clear: []int{1023}}},
			expected: []int{0},
		},
		{
			name:     "clear-cpu-1023",
			initial:  []int{0, 1023},
			steps:    []step{{clear: []int{1023}}},
			expected: []int{0},
		},
		{
			name:     "set-then-clear",
			steps:    []step{{set: []int{0, 1, 2}}, {clear: []int{1}}},
			expected: []int{0, 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCpuSet(tc.initial...)
			for _, step := range tc.steps {
				if len(step.set) > 0 {
					s.Set(step.set...)
				}
				if len(step.clear) > 0 {
					s.Clear(step.clear...)
				}
			}
			if got := s.List(); !slices.Equal(got, tc.expected) {
				t.Errorf("List() = %v, want %v", got, tc.expected)
			}
		})
	}

	t.Run("string-cache-cleared-after-set", func(t *testing.T) {
		s := NewCpuSet(0)
		_ = s.String()
		s.Set(1)
		if got := s.String(); got != "0-1" {
			t.Errorf("String() after Set: got %q, want %q", got, "0-1")
		}
	})

	t.Run("string-cache-cleared-after-clear", func(t *testing.T) {
		s := NewCpuSet(0, 1)
		_ = s.String()
		s.Clear(1)
		if got := s.String(); got != "0" {
			t.Errorf("String() after Clear: got %q, want %q", got, "0")
		}
	})

	// ---- variadic zero-arg / multi-arg Set / Clear calls ---------------------

	t.Run("set-no-args-is-noop", func(t *testing.T) {
		s := NewCpuSet(5)
		s.Set()
		if got := s.List(); !slices.Equal(got, []int{5}) {
			t.Errorf("Set() no-op: got %v, want [5]", got)
		}
	})

	t.Run("clear-no-args-is-noop", func(t *testing.T) {
		s := NewCpuSet(5)
		s.Clear()
		if got := s.List(); !slices.Equal(got, []int{5}) {
			t.Errorf("Clear() no-op: got %v, want [5]", got)
		}
	})

	t.Run("set-multiple-across-word-boundaries", func(t *testing.T) {
		s := NewCpuSet()
		s.Set(0, 63, 64, 127, 128, 1023)
		want := []int{0, 63, 64, 127, 128, 1023}
		if got := s.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("clear-multiple-across-word-boundaries", func(t *testing.T) {
		s := NewCpuSet(0, 63, 64, 127, 128, 1023)
		s.Clear(63, 64, 1023)
		want := []int{0, 127, 128}
		if got := s.List(); !slices.Equal(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

// ---- TestCpuSetSeal --------------------------------------------------

func TestCpuSetSeal(t *testing.T) {
	t.Run("set-on-sealed-set-panics", func(t *testing.T) {
		s := NewCpuSet(0)
		s.Seal()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from Set on sealed set, got none")
			}
		}()
		s.Set(1)
	})

	t.Run("clear-on-sealed-set-panics", func(t *testing.T) {
		s := NewCpuSet(0, 1)
		s.Seal()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from Clear on sealed set, got none")
			}
		}()
		s.Clear(0)
	})

	t.Run("panic-if-sealed-panics-when-sealed", func(t *testing.T) {
		s := NewCpuSet()
		s.Seal()
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from panicIfSealed on sealed set, got none")
			}
		}()
		s.panicIfSealed()
	})

	t.Run("panic-if-sealed-does-not-panic-when-unsealed", func(t *testing.T) {
		s := NewCpuSet()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("unexpected panic from panicIfSealed on unsealed set: %v", r)
			}
		}()
		s.panicIfSealed()
	})

	t.Run("read-only-operations-work-on-sealed-set", func(t *testing.T) {
		s := NewCpuSet(0, 63, 64, 1023)
		s.Seal()
		if !s.Contains(63) {
			t.Error("Contains should work on sealed set")
		}
		if s.Size() != 4 {
			t.Errorf("Size should work on sealed set: got %d, want 4", s.Size())
		}
		if s.IsEmpty() {
			t.Error("IsEmpty should work on sealed set")
		}
		if !slices.Equal(s.List(), []int{0, 63, 64, 1023}) {
			t.Errorf("List should work on sealed set: got %v", s.List())
		}
	})
}

// ---- TestCpuSetClone ------------------------------------------------------

func TestCpuSetClone(t *testing.T) {
	t.Run("clone-of-empty", func(t *testing.T) {
		s := NewCpuSet()
		if c := s.Clone(); !c.IsEmpty() {
			t.Errorf("clone of empty is not empty: %v", c.List())
		}
	})

	t.Run("clone-matches-original", func(t *testing.T) {
		cpus := []int{0, 63, 64, 512, 1023}
		s := NewCpuSet(cpus...)
		if c := s.Clone(); !slices.Equal(c.List(), cpus) {
			t.Errorf("clone content mismatch: want %v, got %v", cpus, c.List())
		}
	})

	t.Run("clone-is-independent", func(t *testing.T) {
		s := NewCpuSet(0, 1, 2)
		c := s.Clone().(*CpuSet)
		c.Set(63)
		if s.Contains(63) {
			t.Error("mutating clone propagated to original")
		}
		if !c.Contains(63) {
			t.Error("mutation on clone did not take effect")
		}
	})

	t.Run("clone-of-large-mask", func(t *testing.T) {
		cpus := cpuRange(512, 1023)
		s := NewCpuSet(cpus...)
		if c := s.Clone(); !slices.Equal(c.List(), cpus) {
			t.Errorf("large clone mismatch: got %v", c.List())
		}
	})
}

// ---- TestCpuSetIsEmpty ----------------------------------------------------

func TestCpuSetIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected bool
	}{
		{name: "empty", cpus: []int{}, expected: true},
		{name: "single-cpu-0", cpus: []int{0}, expected: false},
		{name: "single-cpu-63", cpus: []int{63}, expected: false},
		{name: "single-cpu-64", cpus: []int{64}, expected: false},
		{name: "word-0-all-bits", cpus: cpuRange(0, 63), expected: false},
		{name: "cpu-1023", cpus: []int{1023}, expected: false},
		{name: "multi-word-sparse", cpus: []int{0, 64, 512, 1023}, expected: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCpuSet(tc.cpus...)
			if got := s.IsEmpty(); got != tc.expected {
				t.Errorf("IsEmpty() = %v, want %v", got, tc.expected)
			}
		})
	}

	t.Run("empty-after-clearing-all", func(t *testing.T) {
		s := NewCpuSet(0, 63, 64, 1023)
		s.Clear(s.List()...)
		if !s.IsEmpty() {
			t.Errorf("should be empty after clearing all, got %v", s.List())
		}
	})
}

// ---- TestCpuSetSize -------------------------------------------------------

func TestCpuSetSize(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected int
	}{
		{name: "empty", cpus: []int{}, expected: 0},
		{name: "single-cpu-0", cpus: []int{0}, expected: 1},
		{name: "single-cpu-63", cpus: []int{63}, expected: 1},
		{name: "single-cpu-64", cpus: []int{64}, expected: 1},
		{name: "word-0-all-64-bits", cpus: cpuRange(0, 63), expected: 64},
		{name: "two-words-all-128-bits", cpus: cpuRange(0, 127), expected: 128},
		{name: "single-cpu-1023", cpus: []int{1023}, expected: 1},
		{name: "large-range-512-to-1023", cpus: cpuRange(512, 1023), expected: 512},
		{name: "sparse-boundary-cpus", cpus: []int{0, 63, 64, 127, 512, 1023}, expected: 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCpuSet(tc.cpus...)
			if got := s.Size(); got != tc.expected {
				t.Errorf("Size() = %d, want %d", got, tc.expected)
			}
		})
	}
}

// ---- TestCpuSetContains ---------------------------------------------------

func TestCpuSetContains(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		check    int
		expected bool
	}{
		{name: "empty-check-0", cpus: []int{}, check: 0, expected: false},
		{name: "cpu-0-present", cpus: []int{0}, check: 0, expected: true},
		{name: "cpu-0-absent", cpus: []int{1}, check: 0, expected: false},
		{name: "cpu-63-present", cpus: []int{63}, check: 63, expected: true},
		{name: "cpu-64-present", cpus: []int{64}, check: 64, expected: true},
		{name: "cpu-63-absent-only-64-set", cpus: []int{64}, check: 63, expected: false},
		{name: "cpu-64-absent-only-63-set", cpus: []int{63}, check: 64, expected: false},
		{name: "word-boundary-check-63", cpus: []int{63, 64}, check: 63, expected: true},
		{name: "word-boundary-check-64", cpus: []int{63, 64}, check: 64, expected: true},
		{name: "cpu-1023-present", cpus: []int{1023}, check: 1023, expected: true},
		{name: "cpu-1023-absent", cpus: []int{1022}, check: 1023, expected: false},
		{name: "check-beyond-set", cpus: []int{0}, check: 1023, expected: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCpuSet(tc.cpus...)
			if got := s.Contains(tc.check); got != tc.expected {
				t.Errorf("Contains(%d) = %v, want %v", tc.check, got, tc.expected)
			}
		})
	}

	// ---- variadic multi-arg / zero-arg Contains calls ------------------------

	t.Run("contains-no-args-is-vacuously-true", func(t *testing.T) {
		s := NewCpuSet(0, 1, 2)
		if !s.Contains() {
			t.Error("Contains() with no args should be true")
		}
		if got := NewCpuSet().Contains(); !got {
			t.Error("Contains() on empty set with no args should be true")
		}
	})

	t.Run("contains-multiple-all-present", func(t *testing.T) {
		s := NewCpuSet(0, 63, 64, 1023)
		if !s.Contains(0, 63, 64, 1023) {
			t.Error("Contains(0, 63, 64, 1023) = false, want true")
		}
	})

	t.Run("contains-multiple-one-missing", func(t *testing.T) {
		s := NewCpuSet(0, 63, 64)
		if s.Contains(0, 63, 64, 1023) {
			t.Error("Contains(0, 63, 64, 1023) = true, want false (1023 absent)")
		}
	})

	t.Run("contains-multiple-duplicates", func(t *testing.T) {
		s := NewCpuSet(0, 64)
		if !s.Contains(0, 0, 64, 64) {
			t.Error("Contains with duplicate CPUs = false, want true")
		}
	})
}

// ---- TestCpuSetString -----------------------------------------------------

func TestCpuSetString(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected string
	}{
		{name: "empty", cpus: []int{}, expected: ""},
		{name: "single-cpu-0", cpus: []int{0}, expected: "0"},
		{name: "single-cpu-63", cpus: []int{63}, expected: "63"},
		{name: "single-cpu-64", cpus: []int{64}, expected: "64"},
		{name: "full-word-0", cpus: cpuRange(0, 63), expected: "0-63"},
		{name: "straddles-word-boundary", cpus: []int{63, 64}, expected: "63-64"},
		{name: "two-full-words", cpus: cpuRange(0, 127), expected: "0-127"},
		{name: "lowest-and-highest-of-two-words", cpus: []int{0, 63, 64, 127}, expected: "0,63-64,127"},
		{name: "sparse-word-boundaries", cpus: []int{0, 64, 128, 192}, expected: "0,64,128,192"},
		{name: "low-and-very-high", cpus: []int{0, 1023}, expected: "0,1023"},
		{name: "large-range", cpus: cpuRange(512, 1023), expected: "512-1023"},
		{name: "complex", cpus: []int{0, 1, 2, 4, 5, 7}, expected: "0-2,4-5,7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCpuSet(tc.cpus...)
			got := s.String()
			if got != tc.expected {
				t.Errorf("String() = %q, want %q", got, tc.expected)
			}
			if got2 := s.String(); got2 != got {
				t.Errorf("cached String() differs: %q vs %q", got2, got)
			}
		})
	}
}

// ---- TestCpuSetList -------------------------------------------------------

func TestCpuSetList(t *testing.T) {
	tests := []struct {
		name     string
		cpus     []int
		expected []int
	}{
		{name: "empty", cpus: []int{}, expected: []int{}},
		{name: "single", cpus: []int{0}, expected: []int{0}},
		{name: "multi-word-sparse", cpus: []int{0, 64, 127, 512, 1023}, expected: []int{0, 64, 127, 512, 1023}},
		{name: "full-word-0", cpus: cpuRange(0, 63), expected: cpuRange(0, 63)},
		{name: "large-range", cpus: cpuRange(960, 1023), expected: cpuRange(960, 1023)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewCpuSet(tc.cpus...)
			if got := s.List(); !slices.Equal(got, tc.expected) {
				t.Errorf("List() = %v, want %v", got, tc.expected)
			}
			// UnsortedList must contain exactly the same elements.
			unsorted := s.UnsortedList()
			slices.Sort(unsorted)
			if !slices.Equal(unsorted, tc.expected) {
				t.Errorf("UnsortedList() (sorted) = %v, want %v", unsorted, tc.expected)
			}
		})
	}
}

// ---- TestCpuSetForEachCpu --------------------------------------------

func TestCpuSetForEachCpu(t *testing.T) {
	// CpuSet.ForEachCpu iterates over UnsortedList(), which (unlike
	// CpuMask's bitmask-driven iteration) does not guarantee any
	// particular order, so these tests only assert on the set of visited
	// CPUs and call counts, not on ordering.

	t.Run("empty-set-f-never-called", func(t *testing.T) {
		s := NewCpuSet()
		called := false
		s.ForEachCpu(func(_ int) bool {
			called = true
			return true
		})
		if called {
			t.Error("ForEachCpu called f on empty set")
		}
	})

	t.Run("single-cpu", func(t *testing.T) {
		s := NewCpuSet(42)
		var visited []int
		s.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		if !slices.Equal(visited, []int{42}) {
			t.Errorf("expected [42], got %v", visited)
		}
	})

	t.Run("multiple-cpus-visits-all-exactly-once", func(t *testing.T) {
		want := []int{0, 7, 63, 64, 127, 512, 1023}
		s := NewCpuSet(want...)
		var visited []int
		s.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		slices.Sort(visited)
		if !slices.Equal(visited, want) {
			t.Errorf("expected %v (in any order), got %v", want, visited)
		}
	})

	t.Run("full-word-0-visits-all-64", func(t *testing.T) {
		want := cpuRange(0, 63)
		s := NewCpuSet(want...)
		var visited []int
		s.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		slices.Sort(visited)
		if !slices.Equal(visited, want) {
			t.Errorf("expected CPUs 0-63, got %v", visited)
		}
	})

	t.Run("high-cpu-numbers-960-to-1023", func(t *testing.T) {
		want := cpuRange(960, 1023)
		s := NewCpuSet(want...)
		var visited []int
		s.ForEachCpu(func(cpu int) bool {
			visited = append(visited, cpu)
			return true
		})
		slices.Sort(visited)
		if !slices.Equal(visited, want) {
			t.Errorf("expected CPUs 960-1023, got %v", visited)
		}
	})

	t.Run("early-termination-stops-iteration", func(t *testing.T) {
		s := NewCpuSet(0, 63, 64, 1023)
		calls := 0
		s.ForEachCpu(func(_ int) bool {
			calls++
			return false // stop after the very first CPU
		})
		if calls != 1 {
			t.Errorf("expected f to be called exactly once, got %d calls", calls)
		}
	})

	t.Run("partial-termination-stops-after-n", func(t *testing.T) {
		s := NewCpuSet(0, 1, 2, 3, 4)
		calls := 0
		s.ForEachCpu(func(_ int) bool {
			calls++
			return calls < 3 // stop after three CPUs
		})
		if calls != 3 {
			t.Errorf("expected f to be called exactly 3 times, got %d calls", calls)
		}
	})
}

// ---- TestCpuSetDifference -------------------------------------------------

func TestCpuSetDifference(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected []int
	}{
		// *CpuSet fast path
		{name: "cpuset: empty-minus-empty", a: []int{}, b: NewCpuSet(), expected: []int{}},
		{name: "cpuset: empty-minus-nonempty", a: []int{}, b: NewCpuSet(0, 1), expected: []int{}},
		{name: "cpuset: nonempty-minus-empty", a: []int{0, 1}, b: NewCpuSet(), expected: []int{0, 1}},
		{name: "cpuset: a-minus-a", a: []int{0, 1, 2}, b: NewCpuSet(0, 1, 2), expected: []int{}},
		{name: "cpuset: disjoint", a: []int{0, 2}, b: NewCpuSet(1, 3), expected: []int{0, 2}},
		{name: "cpuset: superset-minus-subset", a: []int{0, 1, 2, 3}, b: NewCpuSet(1, 2), expected: []int{0, 3}},
		{name: "cpuset: subset-minus-superset", a: []int{1, 2}, b: NewCpuSet(0, 1, 2, 3), expected: []int{}},
		{name: "cpuset: high-cpus", a: cpuRange(960, 1023), b: NewCpuSet(cpuRange(992, 1023)...), expected: cpuRange(960, 991)},
		// *CpuMask fallback path
		{name: "cpumask: basic", a: []int{0, 1, 2, 3}, b: NewCpuMask(1, 2), expected: []int{0, 3}},
		{name: "cpumask: empty-a", a: []int{}, b: NewCpuMask(0, 1), expected: []int{}},
		{name: "cpumask: empty-b", a: []int{0, 1}, b: NewCpuMask(), expected: []int{0, 1}},
		{name: "cpumask: disjoint", a: []int{0, 2}, b: NewCpuMask(1, 3), expected: []int{0, 2}},
		{name: "cpumask: high-cpus", a: cpuRange(512, 1023), b: NewCpuMask(cpuRange(768, 1023)...), expected: cpuRange(512, 767)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuSet(tc.a...)
			got := a.Difference(tc.b)
			if !maskListEqual(got, NewCpuSet(tc.expected...)) {
				t.Errorf("Difference() = %v, want %v", got.List(), tc.expected)
			}
		})
	}
}

// ---- TestCpuSetEquals -----------------------------------------------------

func TestCpuSetEquals(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected bool
	}{
		// *CpuSet fast path
		{name: "cpuset: both-empty", a: []int{}, b: NewCpuSet(), expected: true},
		{name: "cpuset: same-single", a: []int{0}, b: NewCpuSet(0), expected: true},
		{name: "cpuset: different-single", a: []int{0}, b: NewCpuSet(1), expected: false},
		{name: "cpuset: one-empty-one-not", a: []int{0}, b: NewCpuSet(), expected: false},
		{name: "cpuset: same-full-word-0", a: cpuRange(0, 63), b: NewCpuSet(cpuRange(0, 63)...), expected: true},
		{name: "cpuset: same-multi-word", a: []int{0, 64, 128, 1023}, b: NewCpuSet(0, 64, 128, 1023), expected: true},
		{name: "cpuset: different-multi-word", a: []int{0, 64}, b: NewCpuSet(0, 128), expected: false},
		{name: "cpuset: high-cpus-equal", a: cpuRange(960, 1023), b: NewCpuSet(cpuRange(960, 1023)...), expected: true},
		{name: "cpuset: high-cpus-differ", a: cpuRange(960, 1022), b: NewCpuSet(cpuRange(960, 1023)...), expected: false},
		// *CpuMask fallback path
		{name: "cpumask: equal", a: []int{0, 1, 2}, b: NewCpuMask(0, 1, 2), expected: true},
		{name: "cpumask: not-equal", a: []int{0, 1}, b: NewCpuMask(0, 2), expected: false},
		{name: "cpumask: size-mismatch", a: []int{0, 1, 2}, b: NewCpuMask(0, 1), expected: false},
		{name: "cpumask: both-empty", a: []int{}, b: NewCpuMask(), expected: true},
		{name: "cpumask: high-cpus-equal", a: cpuRange(512, 1023), b: NewCpuMask(cpuRange(512, 1023)...), expected: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuSet(tc.a...)
			if got := a.Equals(tc.b); got != tc.expected {
				t.Errorf("Equals() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// ---- TestCpuSetIntersection -----------------------------------------------

func TestCpuSetIntersection(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected []int
	}{
		// *CpuSet fast path
		{name: "cpuset: both-empty", a: []int{}, b: NewCpuSet(), expected: []int{}},
		{name: "cpuset: one-empty", a: []int{0, 1}, b: NewCpuSet(), expected: []int{}},
		{name: "cpuset: no-overlap", a: []int{0, 2}, b: NewCpuSet(1, 3), expected: []int{}},
		{name: "cpuset: full-overlap", a: cpuRange(0, 63), b: NewCpuSet(cpuRange(0, 63)...), expected: cpuRange(0, 63)},
		{name: "cpuset: partial-overlap", a: []int{0, 1, 2}, b: NewCpuSet(1, 2, 3), expected: []int{1, 2}},
		{name: "cpuset: word-boundary", a: []int{63, 64}, b: NewCpuSet(63, 64), expected: []int{63, 64}},
		{name: "cpuset: high-cpus", a: cpuRange(512, 1023), b: NewCpuSet(cpuRange(960, 1023)...), expected: cpuRange(960, 1023)},
		// *CpuMask fallback path
		{name: "cpumask: partial-overlap", a: []int{0, 1, 2}, b: NewCpuMask(1, 2, 3), expected: []int{1, 2}},
		{name: "cpumask: no-overlap", a: []int{0, 2}, b: NewCpuMask(1, 3), expected: []int{}},
		{name: "cpumask: empty-b", a: []int{0, 1}, b: NewCpuMask(), expected: []int{}},
		{name: "cpumask: high-cpus", a: cpuRange(512, 1023), b: NewCpuMask(cpuRange(960, 1023)...), expected: cpuRange(960, 1023)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuSet(tc.a...)
			got := a.Intersection(tc.b)
			if !maskListEqual(got, NewCpuSet(tc.expected...)) {
				t.Errorf("Intersection() = %v, want %v", got.List(), tc.expected)
			}
		})
	}
}

// ---- TestCpuSetIsSubsetOf -------------------------------------------------

func TestCpuSetIsSubsetOf(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		b        CPUSet
		expected bool
	}{
		// *CpuSet fast path
		{name: "cpuset: empty-subset-empty", a: []int{}, b: NewCpuSet(), expected: true},
		{name: "cpuset: empty-subset-nonempty", a: []int{}, b: NewCpuSet(0, 1), expected: true},
		{name: "cpuset: nonempty-not-subset-of-empty", a: []int{0}, b: NewCpuSet(), expected: false},
		{name: "cpuset: equal-sets", a: []int{0, 1, 2}, b: NewCpuSet(0, 1, 2), expected: true},
		{name: "cpuset: proper-subset", a: []int{0, 1}, b: NewCpuSet(0, 1, 2), expected: true},
		{name: "cpuset: not-subset", a: []int{0, 3}, b: NewCpuSet(0, 1, 2), expected: false},
		{name: "cpuset: word-0-subset-of-0-127", a: cpuRange(0, 63), b: NewCpuSet(cpuRange(0, 127)...), expected: true},
		{name: "cpuset: word-0-not-subset-of-word-1", a: cpuRange(0, 63), b: NewCpuSet(cpuRange(64, 127)...), expected: false},
		{name: "cpuset: high-cpus-subset", a: cpuRange(992, 1023), b: NewCpuSet(cpuRange(960, 1023)...), expected: true},
		{name: "cpuset: high-cpu-not-in-superset", a: []int{0, 1023}, b: NewCpuSet(cpuRange(960, 1023)...), expected: false},
		// *CpuMask fallback path
		{name: "cpumask: proper-subset", a: []int{0, 1}, b: NewCpuMask(0, 1, 2), expected: true},
		{name: "cpumask: not-subset", a: []int{0, 3}, b: NewCpuMask(0, 1, 2), expected: false},
		{name: "cpumask: empty-subset", a: []int{}, b: NewCpuMask(0, 1), expected: true},
		{name: "cpumask: high-cpus-subset", a: cpuRange(992, 1023), b: NewCpuMask(cpuRange(960, 1023)...), expected: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuSet(tc.a...)
			if got := a.IsSubsetOf(tc.b); got != tc.expected {
				t.Errorf("IsSubsetOf() = %v, want %v", got, tc.expected)
			}
		})
	}
}

// ---- TestCpuSetUnion ------------------------------------------------------

func TestCpuSetUnion(t *testing.T) {
	tests := []struct {
		name     string
		a        []int
		others   []CPUSet
		expected []int
	}{
		// *CpuSet fast path
		{name: "cpuset: empty-union-empty", a: []int{}, others: []CPUSet{NewCpuSet()}, expected: []int{}},
		{name: "cpuset: a-union-empty", a: []int{0, 1}, others: []CPUSet{NewCpuSet()}, expected: []int{0, 1}},
		{name: "cpuset: empty-union-b", a: []int{}, others: []CPUSet{NewCpuSet(0, 1)}, expected: []int{0, 1}},
		{name: "cpuset: disjoint", a: []int{0}, others: []CPUSet{NewCpuSet(1)}, expected: []int{0, 1}},
		{name: "cpuset: overlapping", a: []int{0, 1}, others: []CPUSet{NewCpuSet(1, 2)}, expected: []int{0, 1, 2}},
		{name: "cpuset: word-boundary", a: []int{63}, others: []CPUSet{NewCpuSet(64)}, expected: []int{63, 64}},
		{name: "cpuset: multiple-others", a: []int{0}, others: []CPUSet{NewCpuSet(64), NewCpuSet(128)}, expected: []int{0, 64, 128}},
		{name: "cpuset: large-range", a: cpuRange(512, 767), others: []CPUSet{NewCpuSet(cpuRange(768, 1023)...)}, expected: cpuRange(512, 1023)},
		// no-args union must return a copy of a
		{name: "cpuset: no-others-returns-copy", a: []int{0, 63, 64, 1023}, others: nil, expected: []int{0, 63, 64, 1023}},
		{name: "cpuset: no-others-empty", a: []int{}, others: nil, expected: []int{}},
		// *CpuMask fallback path
		{name: "cpumask: a-union-b", a: []int{0, 1}, others: []CPUSet{NewCpuMask(2, 3)}, expected: []int{0, 1, 2, 3}},
		{name: "cpumask: overlapping", a: []int{0, 1}, others: []CPUSet{NewCpuMask(1, 2)}, expected: []int{0, 1, 2}},
		{name: "cpumask: empty-b", a: []int{0, 1}, others: []CPUSet{NewCpuMask()}, expected: []int{0, 1}},
		{name: "cpumask: empty-a", a: []int{}, others: []CPUSet{NewCpuMask(0, 1)}, expected: []int{0, 1}},
		{name: "cpumask: high-cpus", a: []int{0}, others: []CPUSet{NewCpuMask(1023)}, expected: []int{0, 1023}},
		// mixed CpuSet and CpuMask in others
		{name: "mixed: cpuset-and-cpumask", a: []int{0}, others: []CPUSet{NewCpuSet(64), NewCpuMask(128)}, expected: []int{0, 64, 128}},
		{name: "mixed: cpumask-and-cpuset", a: []int{0}, others: []CPUSet{NewCpuMask(64), NewCpuSet(128)}, expected: []int{0, 64, 128}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewCpuSet(tc.a...)
			got := a.Union(tc.others...)
			if !maskListEqual(got, NewCpuSet(tc.expected...)) {
				t.Errorf("Union() = %v, want %v", got.List(), tc.expected)
			}
		})
	}
}
