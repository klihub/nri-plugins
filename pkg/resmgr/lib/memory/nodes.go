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

package libmem

import (
	"fmt"
	"maps"
	"math"
	"math/bits"
	"slices"
	"strconv"
	"strings"

	"github.com/containers/nri-plugins/pkg/utils/cpuset"
)

type Node struct {
	id       ID
	memType  Type
	capacity int64
	normal   bool
	cpus     cpuset.CPUSet
	distance Distance
}

type Distance struct {
	vector []int
	sorted []int
	nodes  map[int]NodeMask
}

func NewNode(id ID, t Type, capa int64, normal bool, cpus cpuset.CPUSet, d []int) (*Node, error) {
	if !t.IsValid() {
		return nil, fmt.Errorf("%w: unknown type %d", ErrInvalidType, t)
	}

	if id > MaxNodeID {
		return nil, fmt.Errorf("%w: %d > %d", ErrInvalidNodeID, id, MaxNodeID)
	}

	dist, err := NewDistance(d)
	if err != nil {
		return nil, err
	}

	return &Node{
		id:       id,
		memType:  t,
		capacity: capa,
		normal:   normal,
		cpus:     cpus.Clone(),
		distance: dist,
	}, nil
}

func (n *Node) Mask() NodeMask {
	return (1 << n.id)
}

func (n *Node) Type() Type {
	return n.memType
}

func (n *Node) Capacity() int64 {
	return n.capacity
}

func (n *Node) IsNormal() bool {
	return n.normal
}

func (n *Node) IsMovable() bool {
	return !n.IsNormal()
}

func (n *Node) HasMemory() bool {
	return n.capacity > 0
}

func (n *Node) CloseCPUs() cpuset.CPUSet {
	return n.cpus
}

func (n *Node) HasCPUs() bool {
	return n.cpus.Size() > 0
}

func (n *Node) Distance() *Distance {
	return n.distance.Clone()
}

func (n *Node) DistanceTo(id ID) int {
	if id < len(n.distance.vector) {
		return n.distance.vector[id]
	}
	return math.MaxInt
}

func (n *Node) ForeachByDistance(fn func(int, NodeMask) bool) {
	for _, d := range n.distance.sorted[1:] {
		if !fn(d, n.distance.nodes[d]) {
			return
		}
	}
}

func NewDistance(vector []int) (Distance, error) {
	if len(vector) > MaxNodeID {
		return Distance{}, fmt.Errorf("%w: too many distances (%d > %d)",
			ErrInvalidNodeID, len(vector), MaxNodeID)
	}

	var (
		sorted []int
		nodes  = make(map[int]NodeMask)
	)

	for id, d := range vector {
		if m, ok := nodes[d]; !ok {
			sorted = append(sorted, d)
			nodes[d] = (1 << id)
		} else {
			nodes[d] = m | (1 << id)
		}
	}

	slices.Sort(sorted)

	return Distance{
		vector: slices.Clone(vector),
		sorted: sorted,
		nodes:  nodes,
	}, nil
}

func (d *Distance) Clone() *Distance {
	return &Distance{
		vector: slices.Clone(d.vector),
		sorted: slices.Clone(d.sorted),
		nodes:  maps.Clone(d.nodes),
	}
}

func (d *Distance) Vector() []int {
	return d.vector
}

func (d *Distance) Sorted() []int {
	return d.sorted
}

func (d *Distance) NodesAt(dist int) NodeMask {
	return d.nodes[dist]
}

type (
	NodeMask uint64
)

const (
	MaxNodeID = 63
)

func NewNodeMask(ids ...ID) NodeMask {
	return NodeMask(0).Set(ids...)
}

func ParseNodeMask(str string) (NodeMask, error) {
	m := NodeMask(0)
	for _, s := range strings.Split(str, ",") {
		switch minmax := strings.SplitN(s, "-", 2); len(minmax) {
		case 2:
			beg, err := strconv.ParseInt(minmax[0], 10, 32)
			if err != nil {
				return 0, fmt.Errorf("%w: failed to parse node mask %q: %w",
					ErrInvalidNodeID, str, err)
			}
			end, err := strconv.ParseInt(minmax[1], 10, 32)
			if err != nil {
				return 0, fmt.Errorf("%w: failed to parse node mask %q: %w",
					ErrInvalidNodes, str, err)
			}
			if end < beg {
				return 0, fmt.Errorf("%w: invalid range (%d - %d) in node mask %q",
					ErrInvalidNodes, beg, end, str)
			}
			for id := beg; id < end; id++ {
				if id > MaxNodeID {
					return 0, fmt.Errorf("%w: invalid node ID in mask %q (range %d-%d)",
						ErrInvalidNodes, str, beg, end)
				}
				m |= (1 << id)
			}
		case 1:
			id, err := strconv.ParseInt(minmax[1], 10, 32)
			if err != nil {
				return 0, fmt.Errorf("%w: failed to parse node mask %q: %w",
					ErrInvalidNodeID, str, err)
			}
			if id > MaxNodeID {
				return 0, fmt.Errorf("%w: invalid node ID (%d) in mask %q",
					ErrInvalidNodes, id, str)
			}
			m |= (1 << id)
		default:
			return 0, fmt.Errorf("%w: failed to parse node mask %q", ErrInvalidNodes, str)
		}
	}
	return m, nil
}

func MustParseNodeMask(str string) NodeMask {
	m, err := ParseNodeMask(str)
	if err == nil {
		return m
	}

	panic(err)
}

func (m NodeMask) Slice() []ID {
	var ids []ID
	m.Foreach(func(id ID) bool {
		ids = append(ids, id)
		return true
	})
	return ids
}

func (m NodeMask) Set(ids ...ID) NodeMask {
	for _, id := range ids {
		m |= (1 << id)
	}
	return m
}

func (m NodeMask) Clear(ids ...ID) NodeMask {
	for _, id := range ids {
		m &^= (1 << id)
	}
	return m
}

func (m NodeMask) Contains(ids ...ID) bool {
	for _, id := range ids {
		if (m & (1 << id)) == 0 {
			return false
		}
	}
	return true
}

func (m NodeMask) ContainsAny(ids ...ID) bool {
	for _, id := range ids {
		if (m & (1 << id)) != 0 {
			return true
		}
	}
	return false
}

func (m NodeMask) And(o NodeMask) NodeMask {
	return m & o
}

func (m NodeMask) Or(o NodeMask) NodeMask {
	return m | o
}

func (m NodeMask) AndNot(o NodeMask) NodeMask {
	return m &^ o
}

func (m NodeMask) Size() int {
	return bits.OnesCount64(uint64(m))
}

func (m NodeMask) String() string {
	b := strings.Builder{}
	b.WriteString("nodes{")
	b.WriteString(m.MemsetString())
	b.WriteString("}")

	return b.String()
}

func (m NodeMask) MemsetString() string {
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

func (m NodeMask) Foreach(fn func(ID) bool) {
	for b := 0; m != 0; b, m = b+8, m>>8 {
		if m&0xff != 0 {
			if m&0xf != 0 {
				if m&0x1 != 0 {
					if !fn(b + 0) {
						return
					}
				}
				if m&0x2 != 0 {
					if !fn(b + 1) {
						return
					}
				}
				if m&0x4 != 0 {
					if !fn(b + 2) {
						return
					}
				}
				if m&0x8 != 0 {
					if !fn(b + 3) {
						return
					}
				}
			}
			if m&0xf0 != 0 {
				if m&0x10 != 0 {
					if !fn(b + 4) {
						return
					}
				}
				if m&0x20 != 0 {
					if !fn(b + 5) {
						return
					}
				}
				if m&0x40 != 0 {
					if !fn(b + 6) {
						return
					}
				}
				if m&0x80 != 0 {
					if !fn(b + 7) {
						return
					}
				}
			}
		}
	}
}
