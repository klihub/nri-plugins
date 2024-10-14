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
	"math/bits"
	"strconv"
	"strings"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
	idset "github.com/intel/goresctrl/pkg/utils"
)

type ID = idset.ID

var (
	maxCpuCount = 128
)

func SetMaxCpuCount(cnt int) int {
	orig := maxCpuCount
	maxCpuCount = cnt
	return orig
}

type CpuMask []uint64

func cpuMaskSliceSize() int {
	return (maxCpuCount + 63) / 64
}

func bitIndex(id ID) (int, int) {
	return id / 64, id & 63
}

func NewCpuMask(ids ...ID) CpuMask {
	m := make(CpuMask, cpuMaskSliceSize(), cpuMaskSliceSize())
	return m.Set(ids...)
}

func ParseCpuMask(str string) (CpuMask, error) {
	m := NewCpuMask()
	if str == "" {
		return m, nil
	}
	for _, s := range strings.Split(str, ",") {
		switch minmax := strings.SplitN(s, "-", 2); len(minmax) {
		case 2:
			beg, err := strconv.ParseInt(minmax[0], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to parse CPU mask %q: %w",
					ErrInvalidCpuMask, str, err)
			}
			end, err := strconv.ParseInt(minmax[1], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to parse CPU mask %q: %w",
					ErrInvalidCpuMask, str, err)
			}
			if end < beg {
				return nil, fmt.Errorf("%w: invalid range (%d - %d) in CPU mask %q",
					ErrInvalidCpuMask, beg, end, str)
			}
			for id := beg; id <= end; id++ {
				if int(id) >= maxCpuCount {
					return nil, fmt.Errorf("%w: invalid CPU ID in mask %q (range %d-%d)",
						ErrInvalidCpuMask, str, beg, end)
				}
				m.Set(int(id))
			}
		case 1:
			id, err := strconv.ParseInt(minmax[0], 10, 32)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to parse CPU mask %q: %w",
					ErrInvalidCpuMask, str, err)
			}
			if int(id) >= maxCpuCount {
				return nil, fmt.Errorf("%w: invalid CPU ID (%d) in mask %q",
					ErrInvalidCpuMask, id, str)
			}
			m.Set(int(id))
		default:
			return nil, fmt.Errorf("%w: failed to parse CPU mask %q", ErrInvalidCpuMask, str)
		}
	}
	return m, nil
}

func MustParseNodeMask(str string) CpuMask {
	m, err := ParseCpuMask(str)
	if err == nil {
		return m
	}

	panic(err)
}

func (m CpuMask) Clone() CpuMask {
	o := NewCpuMask()
	copy(o, m)
	return o
}

func (m CpuMask) Slice() []ID {
	var ids []ID
	m.Foreach(func(id ID) bool {
		ids = append(ids, id)
		return true
	})
	return ids
}

func (m CpuMask) Set(ids ...ID) CpuMask {
	for _, id := range ids {
		w, b := bitIndex(id)
		m[w] |= (1 << b)
	}
	return m
}

func (m CpuMask) Clear(ids ...ID) CpuMask {
	for _, id := range ids {
		w, b := bitIndex(id)
		m[w] &^= (1 << b)
	}
	return m
}

func (m CpuMask) Contains(ids ...ID) bool {
	for _, id := range ids {
		w, b := bitIndex(id)
		if m[w]&(1<<b) == 0 {
			return false
		}
	}
	return true
}

func (m CpuMask) ContainsAny(ids ...ID) bool {
	for _, id := range ids {
		w, b := bitIndex(id)
		if m[w]&(1<<b) != 0 {
			return true
		}
	}
	return false
}

func (m CpuMask) And(o CpuMask) CpuMask {
	for i := range m {
		m[i] &= o[i]
	}
	return m
}

func (m CpuMask) Or(o CpuMask) CpuMask {
	if o != nil {
		for i := range m {
			m[i] |= o[i]
		}
	}
	return m
}

func (m CpuMask) AndNot(o CpuMask) CpuMask {
	for i := range m {
		m[i] &^= o[i]
	}
	return m
}

func (m CpuMask) Union(o CpuMask) CpuMask {
	return m.Clone().Or(o)
}

func (m CpuMask) Intersection(o CpuMask) CpuMask {
	return m.Clone().And(o)
}

func (m CpuMask) Difference(o CpuMask) CpuMask {
	return m.Clone().AndNot(o)
}

func (m CpuMask) IsSubsetOf(o CpuMask) bool {
	for i, w := range m {
		if w&o[i] != w {
			return false
		}
	}
	return true
}

func (m CpuMask) IsDisjoint(o CpuMask) bool {
	for i, w := range m {
		if w&o[i] != 0 {
			return false
		}
	}
	return true
}

func (m CpuMask) Size() int {
	cnt := 0
	for _, w := range m {
		cnt += bits.OnesCount64(w)
	}
	return cnt
}

func (m CpuMask) IsEmpty() bool {
	for _, w := range m {
		if w != 0 {
			return false
		}
	}
	return true
}

func (m CpuMask) IsEqual(o CpuMask) bool {
	for i, w := range m {
		if w != o[i] {
			return false
		}
	}
	return true
}

func (m CpuMask) String() string {
	b := strings.Builder{}
	b.WriteString("cpumask{")
	b.WriteString(m.KernelString())
	b.WriteString("}")

	return b.String()
}

func (m CpuMask) KernelString() string {
	var (
		b         = strings.Builder{}
		sep       = ""
		beg       = -1
		end       = -1
		dumpRange = func() {
			switch {
			case beg < 0:
			case beg == end:
				b.WriteString(sep)
				b.WriteString(strconv.Itoa(beg))
				sep = ","
			case beg <= end-1:
				b.WriteString(sep)
				b.WriteString(strconv.Itoa(beg))
				b.WriteString("-")
				b.WriteString(strconv.Itoa(end))
				sep = ","
			}
		}
	)

	m.Foreach(func(id ID) bool {
		switch {
		case beg < 0:
			beg, end = id, id
		case beg >= 0 && id == end+1:
			end = id
		default:
			dumpRange()
			beg, end = id, id
		}
		return true
	})

	dumpRange()

	return b.String()
}

func (m CpuMask) HexaString() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%x", m[0]))
	for _, w := range m[1:] {
		b.WriteString(fmt.Sprintf("-%x", w))
	}
	return b.String()
}

func (m CpuMask) Foreach(fn func(ID) bool) {
	for i, w := range m {
		if w == 0 {
			continue
		}
		base := i * 64
		for b := 0; w != 0; b, w = b+8, w>>8 {
			if w&0xff != 0 {
				if w&0xf != 0 {
					if w&0x1 != 0 {
						if !fn(base + b + 0) {
							return
						}
					}
					if w&0x2 != 0 {
						if !fn(base + b + 1) {
							return
						}
					}
					if w&0x4 != 0 {
						if !fn(base + b + 2) {
							return
						}
					}
					if w&0x8 != 0 {
						if !fn(base + b + 3) {
							return
						}
					}
				}
				if w&0xf0 != 0 {
					if w&0x10 != 0 {
						if !fn(base + b + 4) {
							return
						}
					}
					if w&0x20 != 0 {
						if !fn(base + b + 5) {
							return
						}
					}
					if w&0x40 != 0 {
						if !fn(base + b + 6) {
							return
						}
					}
					if w&0x80 != 0 {
						if !fn(base + b + 7) {
							return
						}
					}
				}
			}
		}
	}
}

func NewCpuMaskForCPUSet(cset cpuset.CPUSet) CpuMask {
	return NewCpuMask(cset.UnsortedList()...)
}

func (m CpuMask) CPUSet() cpuset.CPUSet {
	return cpuset.New(m.Slice()...)
}

func (m CpuMask) AndCPUSet(o cpuset.CPUSet) CpuMask {
	return m.And(NewCpuMaskForCPUSet(o))
}

func (m CpuMask) OrCPUSet(o cpuset.CPUSet) CpuMask {
	return m.Set(o.UnsortedList()...)
}

func (m CpuMask) AndNotCPUSet(o cpuset.CPUSet) CpuMask {
	return m.AndNot(NewCpuMaskForCPUSet(o))
}

func (m CpuMask) UnionCPUSet(o cpuset.CPUSet) CpuMask {
	return m.Clone().OrCPUSet(o)
}

func (m CpuMask) IntersectionCPUSet(o cpuset.CPUSet) CpuMask {
	return m.Clone().AndCPUSet(o)
}

func (m CpuMask) DifferenceCPUSet(o cpuset.CPUSet) CpuMask {
	return m.Clone().AndNotCPUSet(o)
}
