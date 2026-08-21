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
	"math/bits"
	"slices"
	"strconv"
	"strings"

	"k8s.io/utils/cpuset"
)

// CPUSet represents an unordered set of CPUs. It is the common
// interface we expect from every data type that represents a set
// of CPUs. We provide two implementations: a dense [CpuMask] and
// a sparse [CpuSet] which just wraps k8s.io/utils/cpuset.CPUSet.
type CPUSet interface {
	// Clone returns a new unsealed copy of the CPUSet.
	Clone() CPUSet
	// Set adds the given CPUs to an unsealed set.
	Set(cpus ...int)
	// Clear removes the given CPUs from an unsealed set.
	Clear(cpus ...int)
	// Difference returns a new CPUSet containing all CPUs in this set
	// which are not in the other set.
	Difference(other CPUSet) CPUSet
	// Intersection returns a new CPUSet containing all CPUs which are
	// in both sets.
	Intersection(other CPUSet) CPUSet
	// Union returns a new CPUSet containing all CPUs which are in this
	// set or in at least one of the other sets.
	Union(others ...CPUSet) CPUSet
	// Contains returns true if all the given CPU are in the set.
	Contains(cpus ...int) bool
	// Equals returns true if the two sets contain the same CPUs.
	Equals(other CPUSet) bool
	// Size returns the number of CPUs in the set.
	Size() int
	// IsEmpty returns true if the set contains no CPUs.
	IsEmpty() bool
	// IsSubsetOf returns true if all CPUs in this set are also in the other set.
	IsSubsetOf(other CPUSet) bool
	// List returns the list of all CPUs in the set in increasing order.
	List() []int
	// UnsortedList returns an unsorted list of all CPUs in the set.
	UnsortedList() []int
	// String returns a string representation of the set, in a Linux kernel
	// cpuset compatible format.
	String() string
	// Key returns a string usable as a map key for the set. Key() returns
	// the same string for two sets if and only if the two sets are equal.
	Key() string
	// keys are equalof the set, in a Linux kernel cpuset compatible format.
	// Seal the CPUSet. Any attempt to modify a sealed set will panic.
	Seal()
	// IsDense returns true if the set implementation is dense.
	IsDense() bool
	// IsSparse returns true if the set implementation is sparse.
	IsSparse() bool
	// ForEachCpu calls the given function for each CPU in the set. Iteration
	// stops early if the function returns false.
	ForEachCpu(f func(cpu int) bool)
}

var (
	// ErrParseFailed is returned when a CPUSet string cannot be parsed.
	ErrParseFailed = errors.New("faile to parse CPU set")
)

// CpuMask is a dense implementation of CPUSet. It uses bitmasks
// to store CPUs and is a good choice for large CPU sets with many
// CPUs.
type CpuMask struct {
	mask []uint64
	seal bool
	str  string
	key  string
}

// CpuMask should implement CPUSet.
var _ CPUSet = (*CpuMask)(nil)

// NewCpuMask returns a new CpuMask containing the given CPUs.
func NewCpuMask(cpus ...int) *CpuMask {
	mask := make([]uint64, (len(cpus)+63)/64)
	for _, cpu := range cpus {
		w, b := cpu/64, cpu&63
		mask = expand(mask, w)
		mask[w] |= 1 << b
	}
	return &CpuMask{mask: mask}
}

// ParseCpuMask parses the given string representation of a CPU set
// and returns a corresponding new CpuMask.
func ParseCpuMask(s string) (*CpuMask, error) {
	m := NewCpuMask()
	if s == "" {
		return m, nil
	}

	for _, part := range strings.Split(s, ",") {
		if !strings.Contains(part, "-") {
			cpu, err := strconv.Atoi(part)
			if err != nil {
				return &CpuMask{}, fmt.Errorf("%w: %w", ErrParseFailed, err)
			}
			m.Set(cpu)
			continue
		}

		rng := strings.SplitN(part, "-", 2)
		min, err := strconv.Atoi(rng[0])
		if err != nil {
			return &CpuMask{}, fmt.Errorf("%w: invalid range start %q: %w",
				ErrParseFailed, rng[0], err)
		}
		max, err := strconv.Atoi(rng[1])
		if err != nil {
			return &CpuMask{}, fmt.Errorf("%w: invalid range end %q: %w",
				ErrParseFailed, rng[1], err)
		}
		if min > max {
			return &CpuMask{}, fmt.Errorf("%w: invalid range %q", ErrParseFailed, part)
		}

		for cpu := min; cpu <= max; cpu++ {
			m.Set(cpu)
		}
	}

	return m, nil
}

func (m CpuMask) Clone() CPUSet {
	return &CpuMask{mask: slices.Clone(m.mask), str: m.str}
}

func (m *CpuMask) Set(cpus ...int) {
	m.panicIfSealed()

	for _, cpu := range cpus {
		w, b := cpu/64, cpu&63
		m.mask = expand(m.mask, w)
		m.mask[w] |= 1 << b
	}

	m.str = ""
	m.key = ""
}

func (m *CpuMask) Clear(cpus ...int) {
	m.panicIfSealed()

	for _, cpu := range cpus {
		w, b := cpu/64, cpu&63
		if w < len(m.mask) {
			m.mask[w] &^= 1 << b
		}
	}

	m.str = ""
	m.key = ""
}

func (m *CpuMask) Difference(other CPUSet) CPUSet {
	o, ok := other.(*CpuMask)
	if !ok {
		o = NewCpuMask(other.UnsortedList()...)
	}

	r := make([]uint64, len(m.mask))

	for w, v := range m.mask {
		if w < len(o.mask) {
			r[w] = v &^ o.mask[w]
		} else {
			r[w] = v
		}
	}

	return &CpuMask{mask: r}
}

func (m *CpuMask) Intersection(other CPUSet) CPUSet {
	o, ok := other.(*CpuMask)
	if !ok {
		r := NewCpuMask()
		for _, cpu := range other.UnsortedList() {
			if m.Contains(cpu) {
				r.Set(cpu)
			}
		}
		return r
	}

	r := make([]uint64, min(len(m.mask), len(o.mask)))
	for w := range r {
		r[w] = m.mask[w] & o.mask[w]
	}

	return &CpuMask{mask: r}
}

func (m *CpuMask) Union(others ...CPUSet) CPUSet {
	r := &CpuMask{mask: slices.Clone(m.mask)}

	for _, other := range others {
		o, ok := other.(*CpuMask)
		if !ok {
			for _, cpu := range other.UnsortedList() {
				r.Set(cpu)
			}
			continue
		}

		r.mask = expand(r.mask, len(o.mask)-1)
		for w, v := range o.mask {
			r.mask[w] |= v
		}
	}

	return r
}

func (m *CpuMask) Contains(cpus ...int) bool {
	for _, cpu := range cpus {
		w, b := cpu/64, cpu&63
		if w >= len(m.mask) || (m.mask[w]&(1<<b)) == 0 {
			return false
		}
	}
	return true
}

func (m *CpuMask) Equals(other CPUSet) bool {
	o, ok := other.(*CpuMask)
	if !ok {
		if m.Size() != other.Size() {
			return false
		}

		for _, cpu := range other.UnsortedList() {
			if !m.Contains(cpu) {
				return false
			}
		}

		return true
	}

	c := min(len(m.mask), len(o.mask))
	for w := 0; w < c; w++ {
		if m.mask[w] != o.mask[w] {
			return false
		}
	}

	switch {
	case c < len(m.mask):
		for w := c; w < len(m.mask); w++ {
			if m.mask[w] != 0 {
				return false
			}
		}
	case c < len(o.mask):
		for w := c; w < len(o.mask); w++ {
			if o.mask[w] != 0 {
				return false
			}
		}
	}

	return true
}

func (m *CpuMask) Size() int {
	cnt := 0
	for _, v := range m.mask {
		cnt += bits.OnesCount64(v)
	}
	return cnt
}

func (m *CpuMask) IsEmpty() bool {
	for _, v := range m.mask {
		if v != 0 {
			return false
		}
	}
	return true
}

func (m *CpuMask) IsSubsetOf(other CPUSet) bool {
	o, ok := other.(*CpuMask)
	if !ok {
		for _, cpu := range m.UnsortedList() {
			if !other.Contains(cpu) {
				return false
			}
		}
		return true
	}

	c := min(len(m.mask), len(o.mask))
	for w := 0; w < c; w++ {
		if m.mask[w]&^o.mask[w] != 0 {
			return false
		}
	}

	if c < len(m.mask) {
		for w := c; w < len(m.mask); w++ {
			if m.mask[w] != 0 {
				return false
			}
		}
	}

	return true
}

func (m *CpuMask) List() []int {
	cpus := make([]int, 0, m.Size())

	m.ForEachCpu(func(cpu int) bool {
		cpus = append(cpus, cpu)
		return true
	})

	return cpus
}

func (m *CpuMask) UnsortedList() []int {
	return m.List()
}

func (m *CpuMask) String() string {
	if m.str != "" {
		return m.str
	}

	var (
		str        strings.Builder
		rangeStart = -1
		prev       = -1
	)

	flush := func(end int) {
		if rangeStart < 0 {
			return
		}
		if str.Len() > 0 {
			str.WriteString(",")
		}
		str.WriteString(strconv.Itoa(rangeStart))
		if end > rangeStart {
			str.WriteString("-")
			str.WriteString(strconv.Itoa(end))
		}
		rangeStart = -1
	}

	m.ForEachCpu(func(cpu int) bool {
		if rangeStart >= 0 && cpu == prev+1 {
			prev = cpu
			return true
		}
		flush(prev)
		rangeStart = cpu
		prev = cpu
		return true
	})
	flush(prev)

	m.str = str.String()

	return m.str
}

func (m *CpuMask) Key() string {
	if m.key != "" {
		return m.key
	}

	buf, sep := strings.Builder{}, ""
	for _, w := range m.mask {
		buf.WriteString(sep)
		buf.WriteString(fmt.Sprintf("%x", w))
		sep = "-"
	}
	m.key = buf.String()

	return m.key
}

func (m *CpuMask) Seal() {
	m.seal = true
}

func (*CpuMask) IsDense() bool {
	return true
}

func (*CpuMask) IsSparse() bool {
	return false
}

func (m *CpuMask) ForEachCpu(f func(cpu int) bool) {
	for w, v := range m.mask {
		for v != 0 {
			if !f(w*64 + bits.TrailingZeros64(v)) {
				return
			}
			v &= v - 1
		}
	}
}

func (m *CpuMask) panicIfSealed() {
	if m.seal {
		panic("CpuMask is sealed")
	}
}

func expand(mask []uint64, w int) []uint64 {
	if w < len(mask) {
		return mask
	}
	for len(mask) <= w {
		mask = append(mask, 0)
	}
	return mask
}

// CpuSet is a sparse implementation of CPUSet. Internally it
// wraps k8s.io/utils/cpuset.CPUSet and is a good choice for
// representing CPU sets which contain a few CPUs.
type CpuSet struct {
	cpuset.CPUSet
	seal bool
	str  string
}

// CpuSet should implement CPUSet.
var _ CPUSet = (*CpuSet)(nil)

// NewCpuSet returns a new CpuSet containing the given CPUs.
func NewCpuSet(cpus ...int) *CpuSet {
	s := &CpuSet{
		CPUSet: cpuset.New(cpus...),
	}
	return s
}

// ParseCpuSet parses the given string representation of a CPU set
// and returns a corresponding new CpuSet.
func ParseCpuSet(s string) (*CpuSet, error) {
	cpus, err := cpuset.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParseFailed, err)
	}
	return &CpuSet{
		CPUSet: cpus,
	}, nil
}

func (s *CpuSet) Clone() CPUSet {
	return &CpuSet{
		CPUSet: s.CPUSet.Clone(),
	}
}

func (s *CpuSet) Set(cpus ...int) {
	s.panicIfSealed()
	s.CPUSet = s.CPUSet.Union(cpuset.New(cpus...))
	s.str = ""
}

func (s *CpuSet) Clear(cpus ...int) {
	s.panicIfSealed()
	s.CPUSet = s.CPUSet.Difference(cpuset.New(cpus...))
	s.str = ""
}

func (s *CpuSet) Difference(other CPUSet) CPUSet {
	o, ok := other.(*CpuSet)
	if ok {
		return &CpuSet{
			CPUSet: s.CPUSet.Difference(o.CPUSet),
		}
	}

	r := NewCpuSet()
	for _, cpu := range s.UnsortedList() {
		if !other.Contains(cpu) {
			r.Set(cpu)
		}
	}
	return r

}

func (s *CpuSet) Intersection(other CPUSet) CPUSet {
	o, ok := other.(*CpuSet)
	if ok {
		return &CpuSet{
			CPUSet: s.CPUSet.Intersection(o.CPUSet),
		}
	}

	r := NewCpuSet()
	for _, cpu := range s.UnsortedList() {
		if other.Contains(cpu) {
			r.Set(cpu)
		}
	}
	return r
}

func (s *CpuSet) Union(others ...CPUSet) CPUSet {
	r := s.CPUSet.Clone()
	for _, other := range others {
		o, ok := other.(*CpuSet)
		if ok {
			r = r.Union(o.CPUSet)
			continue
		}

		r = r.Union(cpuset.New(other.UnsortedList()...))
	}
	return &CpuSet{CPUSet: r}
}

func (s *CpuSet) Contains(cpus ...int) bool {
	for _, cpu := range cpus {
		if !s.CPUSet.Contains(cpu) {
			return false
		}
	}
	return true
}

func (s *CpuSet) Equals(other CPUSet) bool {
	o, ok := other.(*CpuSet)
	if ok {
		return s.CPUSet.Equals(o.CPUSet)
	}

	for _, cpu := range s.UnsortedList() {
		if !other.Contains(cpu) {
			return false
		}
	}
	return true
}

func (s *CpuSet) Size() int {
	return s.CPUSet.Size()
}

func (s *CpuSet) IsEmpty() bool {
	return s.CPUSet.IsEmpty()
}

func (s *CpuSet) IsSubsetOf(other CPUSet) bool {
	o, ok := other.(*CpuSet)
	if ok {
		return s.CPUSet.IsSubsetOf(o.CPUSet)
	}

	for _, cpu := range s.UnsortedList() {
		if !other.Contains(cpu) {
			return false
		}
	}
	return true
}

func (s *CpuSet) List() []int {
	return s.CPUSet.List()
}

func (s *CpuSet) UnsortedList() []int {
	return s.CPUSet.UnsortedList()
}

func (s *CpuSet) String() string {
	if s.str != "" {
		return s.str
	}

	s.str = s.CPUSet.String()

	return s.str
}

func (s *CpuSet) Key() string {
	return s.String()
}

func (s *CpuSet) Seal() {
	s.seal = true
}

func (*CpuSet) IsDense() bool {
	return false
}

func (*CpuSet) IsSparse() bool {
	return true
}

func (m *CpuSet) ForEachCpu(f func(cpu int) bool) {
	for _, cpu := range m.UnsortedList() {
		if !f(cpu) {
			return
		}
	}
}

func (s *CpuSet) panicIfSealed() {
	if s.seal {
		panic("CpuSet is sealed")
	}
}
