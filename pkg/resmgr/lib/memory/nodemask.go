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
	"math/bits"
)

// NodeMask represents a set of nodes using a 64-bit integer.
type NodeMask uint64

const (
	MaxNodeMaskID = 63
)

// NodeMaskForIDs returns a node mask representing the given IDs.
func NodeMaskForIDs(ids ...ID) NodeMask {
	m := NodeMask(0)
	for _, id := range ids {
		if id > MaxNodeMaskID {
			panic(fmt.Sprintf("can't store ID %d, > %d", id, MaxNodeMaskID))
		}
		m |= (1 << id)
	}
	return m
}

// NodeMaskForIDSet returns the node mask representing the given IDSet.
func NodeMaskForIDSet(ids IDSet) NodeMask {
	return NodeMaskForIDs(ids.Members()...)
}

// Set sets the given IDs in the node mask.
func (m *NodeMask) Set(ids ...ID) *NodeMask {
	for _, id := range ids {
		if id > MaxNodeMaskID {
			panic(fmt.Sprintf("can't store ID %d, > %d", id, MaxNodeMaskID))
		}
		*m |= (1 << id)
	}

	return m
}

// Clear clears the given IDs in the node mask.
func (m *NodeMask) Clear(ids ...ID) *NodeMask {
	for _, id := range ids {
		if id > MaxNodeMaskID {
			panic(fmt.Sprintf("can't store ID %d, > %d", id, MaxNodeMaskID))
		}
		*m &^= (1 << id)
	}

	return m
}

// Size returns the number of IDs set in the node mask.
func (m NodeMask) Size() int {
	return bits.OnesCount64(uint64(m))
}

// Union returns (m U o), the union of two node masks.
func (m NodeMask) Union(o NodeMask) NodeMask {
	return m | o
}

// Intersection returns (m /\ o), the intersection of two node masks.
func (m NodeMask) Intersection(o NodeMask) NodeMask {
	return m & o
}

// Diff returns (m \ o), mask of nodes present in m but not in o.
func (m NodeMask) Diff(o NodeMask) NodeMask {
	return m &^ o
}

// Contains tests if the node mask contains the given id.
func (m NodeMask) Contains(id ID) bool {
	return (m & (1 << id)) != 0
}

// ContainsAll tests if the node mask contains all the given ids.
func (m NodeMask) ContainsAll(ids ...ID) bool {
	for _, id := range ids {
		if (m & (1 << id)) == 0 {
			return false
		}
	}
	return true
}

// ContainsAny tests if the node mask contains any of the given ids.
func (m NodeMask) ContainsAny(ids ...ID) bool {
	for _, id := range ids {
		if (m & (1 << id)) != 0 {
			return true
		}
	}
	return false
}

// IsDisjoint tests if two node masks represent disjoint sets of nodes.
func (m NodeMask) IsDisjoint(o NodeMask) bool {
	return (m & o) == 0
}

// IsEqual checks if two node masks represent the same set of nodes.
func (m NodeMask) IsEqual(o NodeMask) bool {
	return m == o
}

// IDs returns the IDs present in the node mask.
func (m NodeMask) IDs() []ID {
	var ids []ID

	for b := 0; m != 0; b, m = b+8, m>>8 {
		if m&0xff != 0 {
			if m&0x0f != 0 {
				if m&0x1 != 0 {
					ids = append(ids, b+0)
				}
				if m&0x2 != 0 {
					ids = append(ids, b+1)
				}
				if m&0x4 != 0 {
					ids = append(ids, b+2)
				}
				if m&0x8 != 0 {
					ids = append(ids, b+3)
				}
			}
			if m&0xf0 != 0 {
				if m&0x10 != 0 {
					ids = append(ids, b+4)
				}
				if m&0x20 != 0 {
					ids = append(ids, b+5)
				}
				if m&0x40 != 0 {
					ids = append(ids, b+6)
				}
				if m&0x80 != 0 {
					ids = append(ids, b+7)
				}
			}
		}
	}

	return ids
}

// IDSet returns the IDSet for the IDs present in the node mask.
func (m NodeMask) IDSet() IDSet {
	return NewIDSet(m.IDs()...)
}

// String returns an string representation of the node mask.
func (m NodeMask) String() string {
	return "nodes<" + m.IDSet().String() + ">"
}
